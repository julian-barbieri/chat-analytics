package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"
)

// WorkerConfig define cada cuánto se consulta YouTube y cuánta cuota se puede usar.
type WorkerConfig struct {
	Channels      []string // el primero es el principal; de los demás solo se siguen los vivos
	Videos        int
	Extra         []string        // videos que se revisan siempre además de los del canal
	VideosEvery   time.Duration   // estado de los videos y de los vivos (3 unidades + 1 por canal extra)
	CommentsEvery time.Duration   // comentarios nuevos (1 unidad por página)
	ChatEvery     time.Duration   // entre lecturas del chat fuera del horario pico (5 unidades cada una),
	ChatPeak      time.Duration   // y en el horario pico; con varios vivos se leen por turnos
	PeakFrom      int             // horario pico en hora local: desde PeakFrom hasta PeakTo (sin incluir);
	PeakTo        int             // iguales = sin horario pico
	Budget        int             // tope diario de unidades que usa el monitor
	RealTime      map[string]bool // vivos con tiempo real continuo: se reconectan apenas YouTube corta
	Settings      *Settings       // canales e intervalos de Canales (ADR 0005); nil: Channels, ChatPeak y ChatEvery, como antes
}

// YouTube corta cada conexión de tiempo real a los ~10 s. Los vivos de
// RealTime se reconectan después de esta espera, sin turnos: 5 unidades por
// conexión, unas 1.800 por hora y por vivo.
var realTimeGap = time.Second

// Cuando un canal se queda sin vivo, se revisa solo ese canal (ChannelLives,
// 2 unidades) cada fastEvery durante fastFor, para enganchar el programa
// siguiente sin esperar la revisión completa: a lo sumo 40 unidades por corte.
// Son var para que los tests las achiquen, igual que workerTick (el bucle).
var (
	fastEvery  = time.Minute
	fastFor    = 20 * time.Minute
	workerTick = time.Second
)

// Cada cuánto se revisan las subidas de los canales extra, para los
// comentarios de sus videos grabados (pantalla Comentarios): 1 unidad por
// canal cada vez, 48 por día, en lugar de 480 si fuera en cada revisión. Es
// var para que los tests la cambien.
var extraUploadsEvery = 30 * time.Minute

// shortsEvery: cada cuánto se piden las listas de shorts y los videos que
// faltan en el archivo; shortsFirst, la primera vez después de arrancar, para
// no sumar pedidos a los del arranque.
var shortsEvery, shortsFirst = 30 * time.Minute, time.Minute

// extraUploads son los videos grabados (ni en vivo ni programados) de los
// canales extra de una revisión con sus subidas: se conservan hasta la
// próxima, así no desaparecen de la lista entre medio.
func extraUploads(vids []Video, chs []ChannelInfo) []Video {
	if len(chs) < 2 {
		return nil
	}
	var out []Video
	for _, v := range vids {
		if v.LiveStatus == "none" && v.ChannelID != chs[0].ID && hasChannel(chs[1:], v.ChannelID) {
			out = append(out, v)
		}
	}
	return out
}

// withExtraUploads suma a una revisión sin subidas de los extra los grabados
// guardados de los canales que siguen configurados y no vinieron en vids.
func withExtraUploads(vids, kept []Video, chs []ChannelInfo) []Video {
	in := map[string]bool{}
	for _, v := range vids {
		in[v.ID] = true
	}
	for _, v := range kept {
		if !in[v.ID] && len(chs) > 1 && hasChannel(chs[1:], v.ChannelID) {
			vids = append(vids, v)
		}
	}
	return vids
}

func hasChannel(chs []ChannelInfo, id string) bool {
	for _, c := range chs {
		if c.ID == id {
			return true
		}
	}
	return false
}

// Unidades por hora de un vivo en tiempo real continuo, para estimar cuánto alcanza la cuota.
const realTimeUnitsPerHour = 1800

// chatInterval lee el chat más seguido en el horario de más audiencia y más
// espaciado el resto, para que el total del día siga entrando en la cuota.
func (c WorkerConfig) chatInterval(t time.Time) (time.Duration, bool) {
	h := t.Local().Hour()
	peak := c.PeakFrom < c.PeakTo && h >= c.PeakFrom && h < c.PeakTo ||
		c.PeakFrom > c.PeakTo && (h >= c.PeakFrom || h < c.PeakTo) // franja que cruza la medianoche
	if peak {
		return c.ChatPeak, true
	}
	return c.ChatEvery, false
}

// ParsePeak lee un horario como "7-20" (de 7:00 a 19:59). Vacío = sin horario pico.
func ParsePeak(s string) (from, to int, err error) {
	if strings.TrimSpace(s) == "" {
		return 0, 0, nil
	}
	a, b, ok := strings.Cut(s, "-")
	from, errA := strconv.Atoi(strings.TrimSpace(a))
	to, errB := strconv.Atoi(strings.TrimSpace(b))
	if !ok || errA != nil || errB != nil || from < 0 || from > 23 || to < 0 || to > 24 || from == to%24 {
		return 0, 0, fmt.Errorf("horario %q inválido: usá horas de 0 a 24, por ejemplo 7-20", s)
	}
	return from, to % 24, nil
}

// Cuánto de la cuota se reserva para videos y comentarios: el chat se corta antes.
const reserveForComments = 1500

// chatState es lo que el worker recuerda del chat de cada vivo.
type chatState struct {
	chatID   string
	token    string
	last     time.Time
	wait     time.Duration // intervalo mínimo que pidió YouTube en la última lectura
	blocked  bool          // YouTube no deja leer este chat
	open     bool          // hay una conexión de streaming abierta
	cancel   context.CancelFunc
	noStream bool // el streaming no anduvo: se lee con lecturas comunes, como antes
	held     bool // el streaming se cortó porque se pausó el canal desde Canales
	note     string
	opened   time.Time     // cuándo se abrió la última conexión de tiempo real
	lastDur  time.Duration // cuánto duró la última conexión que se cerró
	got      time.Time     // último dato que llegó de YouTube
	recent   []time.Time   // últimas conexiones o lecturas, para el cartel del dashboard
}

// ChatInfo es cómo se está consultando el chat de un vivo, para el cartel del dashboard.
type ChatInfo struct {
	Stream   bool          // se usa el tiempo real (si no, lecturas comunes)
	Open     bool          // la conexión de tiempo real está abierta ahora
	Blocked  bool          // YouTube no deja leer este chat
	Paused   bool          // se llegó al tope de cuota del chat
	Opened   time.Time     // cuándo se abrió la última conexión
	LastDur  time.Duration // cuánto duró la última conexión que se cerró
	Next     time.Time     // próxima conexión o lectura, aproximada
	Got      time.Time     // último dato que llegó de YouTube
	Gap      time.Duration // mínimo entre dos conexiones o lecturas de los vivos de su canal
	Peak     bool          // Gap es el del horario pico
	Lives    int           // vivos de su canal que se turnan
	Avg      time.Duration // promedio entre las últimas conexiones o lecturas de este vivo
	Count    int           // cuántas entran en Avg
	RealTime bool          // tiempo real continuo (-tiempo-real): se reconecta apenas YouTube corta
	Left     int           // unidades que le quedan hoy al chat
	Rate     int           // unidades por hora que gasta el chat de todos los vivos, estimadas
	Held     bool          // el canal tiene el chat en pausa desde Canales
}

// chatEvent es lo que manda la goroutine del streaming de un chat al worker.
type chatEvent struct {
	c    *chatState
	b    ChatBatch
	done bool // se cerró la conexión
	err  error
}

// chatGroup son los vivos que se turnan con un mismo intervalo: los de un
// canal de la configuración (por su ID) o los sueltos de -vivos (""). Sin
// Settings hay un solo grupo, "", con el intervalo de los flags: como antes.
type chatGroup struct {
	last   time.Time     // última conexión o lectura de cualquiera de sus vivos
	every  time.Duration // intervalo del canal según el horario pico
	paused bool          // pausado desde Canales: su chat no se lee
}

// groupKey es el grupo de un vivo: su canal, si es uno de groups, o "".
func groupKey(v Video, groups map[string]chatGroup) string {
	if _, ok := groups[v.ChannelID]; ok && v.ChannelID != "" {
		return v.ChannelID
	}
	return ""
}

// nextChat elige el vivo al que le toca abrir el streaming del chat o hacer
// una lectura común. Está listo si no está bloqueado, no tiene el streaming
// abierto, cumplió la espera que pidió YouTube y su canal no está en pausa y
// cumplió su intervalo. Entre los listos va el del canal más atrasado y,
// dentro del canal, el que hace más que no se lee. Elige uno solo: el worker
// hace a lo sumo una acción de chat por vuelta, así nunca hay dos lecturas
// comunes en paralelo. También devuelve cuántos vivos no están bloqueados.
func nextChat(lives []Video, chats map[string]*chatState, groups map[string]chatGroup, now time.Time) (next *Video, active int) {
	var late time.Duration
	for i := range lives {
		c := chats[lives[i].ID]
		if c.blocked {
			continue
		}
		active++
		g := groups[groupKey(lives[i], groups)]
		if c.open || g.paused || now.Sub(c.last) < c.wait || now.Sub(g.last) < g.every {
			continue
		}
		over := now.Sub(g.last) - g.every
		if next == nil || over > late || over == late && c.last.Before(chats[next.ID].last) {
			next, late = &lives[i], over
		}
	}
	return next, active
}

// channelState es lo que el worker recuerda de cada canal de la
// configuración (cfg.Channels o cfg.Settings), en su orden.
type channelState struct {
	info                 ChannelInfo
	input                string // lo que se pasó en -canal o se agregó en Canales
	resolved             bool   // ya se pidió a YouTube (1 unidad)
	err                  string // no existe: no se vuelve a pedir mientras siga en la configuración
	paused               bool   // chat en pausa desde Canales
	peak, off            time.Duration
	live                 bool
	waitSince, fastUntil time.Time
	nextFast             time.Time
}

// trackChannels marca qué canales tienen un vivo en curso en vids (con chat o
// sin él). Un canal que tenía vivo y se quedó sin ninguno pasa a la búsqueda
// rápida; uno que tiene, la corta.
func trackChannels(channels []*channelState, vids []Video, now time.Time) {
	for _, c := range channels {
		if !c.resolved {
			continue
		}
		has := false
		for _, v := range vids {
			has = has || v.ChannelID == c.info.ID && v.LiveStatus == "live"
		}
		switch {
		case has:
			c.live, c.waitSince, c.fastUntil = true, time.Time{}, time.Time{}
		case c.live:
			c.live, c.waitSince, c.fastUntil, c.nextFast = false, now, now.Add(fastFor), now.Add(fastEvery)
			log.Printf("%s se quedó sin vivo: lo reviso cada %s durante %s", c.info.Title, humanDur(fastEvery), humanDur(fastFor))
		}
	}
}

// channelStatuses son los canales ya resueltos, para la vista; pending, los
// que todavía no (o que YouTube no encontró), para la pantalla Canales.
func channelStatuses(channels []*channelState, every time.Duration) (out, pending []ChannelStatus) {
	out = make([]ChannelStatus, 0, len(channels))
	for _, c := range channels {
		if !c.resolved {
			pending = append(pending, ChannelStatus{Input: c.input, Err: c.err, Paused: c.paused})
			continue
		}
		out = append(out, ChannelStatus{ID: c.info.ID, Name: c.info.Title, Input: c.input, Live: c.live, WaitSince: c.waitSince,
			FastUntil: c.fastUntil, Every: every, Paused: c.paused})
	}
	return out, pending
}

// lookupFailed es una falla al buscar un canal nuevo (no "no se encontró"):
// la revisión de los demás sigue y ese canal se vuelve a pedir en la próxima.
type lookupFailed struct{ error }

// principal es el nombre del primer canal resuelto: el que da el título del
// dashboard y del que se bajan subidas y comentarios.
func principal(channels []*channelState) string {
	for _, c := range channels {
		if c.resolved {
			return c.info.Title
		}
	}
	return ""
}

// markShorts marca los videos que están en las listas de shorts.
func markShorts(vids []Video, shorts map[string]bool) {
	for i := range vids {
		vids[i].Short = vids[i].Short || shorts[vids[i].ID]
	}
}

// noShortsList dice si YouTube contestó que la lista no existe (un canal sin
// shorts): no tiene sentido volver a pedirla por los mismos videos.
func noShortsList(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "HTTP 404") || strings.Contains(msg, "playlistNotFound")
}

// reviewShorts pide la lista de shorts de los canales que tienen videos que
// todavía no se clasificaron (en general, solo cuando subieron algo) y los
// videos con comentarios que faltan en el archivo (una sola vez cada uno,
// aunque YouTube no lo devuelva: por ejemplo, si se borró). Todo con spend.
func reviewShorts(yt *YouTube, st *Store, spend func(func() error) error, infos []ChannelInfo, shorts, classified, asked map[string]bool) {
	current := st.Status().Videos
	for _, c := range infos {
		var pending []string
		for _, v := range current {
			if v.ChannelID == c.ID && v.LiveStatus == "none" && v.StartedAt.IsZero() && !classified[v.ID] {
				pending = append(pending, v.ID)
			}
		}
		if len(pending) == 0 {
			continue
		}
		var ids []string
		if err := spend(func() (err error) { ids, err = yt.ChannelShorts(c.Uploads); return err }); err != nil {
			// La lista no está documentada: si falla, quedan como videos comunes.
			log.Printf("aviso: shorts de %s: %v", c.Title, err)
			if !noShortsList(err) {
				continue // se reintenta en shortsEvery
			}
			// Un canal sin shorts puede no tener la lista: no se pide más
			// hasta que suba otra cosa.
		}
		for _, id := range ids {
			shorts[id] = true
		}
		for _, id := range pending {
			classified[id] = true
		}
	}
	var missing []string
	for _, id := range st.MissingVideos() {
		if !asked[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return
	}
	var vids []Video
	if err := spend(func() (err error) { vids, err = yt.Videos(missing); return err }); err != nil {
		log.Printf("videos que faltan: %v", err)
		return
	}
	for _, id := range missing {
		asked[id] = true
	}
	markShorts(vids, shorts)
	st.AddLikes(time.Now(), vids)
	st.AddVideos(time.Now(), vids)
}

// RunWorker corre en una sola goroutine, así el cliente no se comparte, hasta
// que se cancela ctx (con context.Background(), para siempre). Al cancelarlo
// corta sus streaming y no vuelve hasta que terminan, para que nadie siga
// escribiendo en el Store (los tests borran su directorio al terminar).
func RunWorker(ctx context.Context, yt *YouTube, st *Store, cfg WorkerConfig) {
	var (
		channels      []*channelState // en el orden de la configuración; el primero resuelto es el principal
		version       = -1            // de cfg.Settings: si cambia, se aplica en el tick
		nextVideos    time.Time
		nextComments  time.Time
		nextExtra     time.Time                         // próxima revisión de las subidas de los canales extra
		extraKept     []Video                           // sus grabados de la última vez (extraUploads)
		nextShorts    = time.Now().Add(shortsFirst)     // próxima revisión de shorts y de videos que faltan
		shortIDs      = map[string]bool{}               // de las listas UUSH: lo que se sabe hasta reiniciar
		classified    = map[string]bool{}               // videos que ya estaban cuando se pidió la lista de su canal
		askedMissing  = map[string]bool{}               // faltantes ya pedidos: no se repiten hasta reiniciar
		groupLast     = map[string]time.Time{}          // última acción de chat de cada grupo (chatGroup)
		lastComments  = time.Now().Add(-48 * time.Hour) // primera pasada: últimas 48 h
		knownComments = map[string]int64{}
		lives         []Video
		chats         = map[string]*chatState{} // por ID de vivo; el token es de cada chat
		events        = make(chan chatEvent, 256)
		tick          = time.NewTicker(workerTick)
		streams       int // goroutines de streaming que todavía no mandaron su done
	)
	spend := func(f func() error) error {
		before := yt.Units
		err := f()
		st.AddUnits(yt.Units - before)
		return err
	}
	chatFailed := func(c *chatState, id string, err error, stream bool) {
		log.Printf("chat de %s: %v", id, err)
		c.note = "No se pudo leer el chat: " + err.Error()
		msg := err.Error()
		switch {
		case strings.Contains(strings.ToLower(msg), "quota"): // falta de cuota: se reintenta más tarde
		case strings.Contains(strings.ToLower(msg), "too quickly") || strings.Contains(strings.ToLower(msg), "rate limit"):
			c.wait = 30 * time.Second // nos reconectamos muy rápido: se espera un poco y se sigue
		case stream && (isForbidden(err) || strings.Contains(msg, "HTTP 404")):
			// Sin streaming para este chat: se sigue con lecturas comunes.
			c.noStream = true
		case isForbidden(err):
			// Si YouTube no deja leer este chat (por ejemplo, pide OAuth), no insistimos mientras siga este vivo.
			c.blocked = true
		case strings.Contains(msg, "HTTP 400"):
			c.token = "" // token vencido: se sigue desde lo último
		}
	}
	// Lo que llega del streaming se procesa acá, en la goroutine del worker,
	// para no tocar chats ni lives desde otra.
	handle := func(ev chatEvent) {
		st.AddUnits(ev.b.Units)
		var live *Video
		for i := range lives {
			if chats[lives[i].ID] == ev.c {
				live = &lives[i]
			}
		}
		if len(ev.b.Msgs) > 0 {
			if live != nil {
				TagProgram(ev.b.Msgs, live.Title, live.Channel)
			}
			st.Add(ev.b.Msgs)
		}
		if live == nil {
			return // el vivo terminó y su estado ya se olvidó
		}
		c := ev.c
		if ev.b.Token != "" {
			c.token = ev.b.Token
		}
		if !ev.done {
			if len(ev.b.Msgs) > 0 || ev.b.Token != "" {
				c.got, c.wait = time.Now(), 0
			}
			c.note = ""
			return
		}
		c.open, c.cancel, c.last = false, nil, time.Now()
		c.lastDur = c.last.Sub(c.opened)
		switch {
		case c.held:
			c.held = false
			log.Printf("chat de %s: chat en pausa desde Canales", live.ID)
		case ev.err != nil:
			chatFailed(c, live.ID, ev.err, true)
		}
		// El cierre normal de YouTube no se loguea: pasa cada pocos segundos y
		// llenaba la consola. La duración queda en c.lastDur para Canales.
	}

	publish := func() {
		ok, pending := channelStatuses(channels, cfg.VideosEvery)
		st.SetChannels(ok)
		st.SetPending(pending)
	}
	for _, ch := range cfg.Channels {
		if cfg.Settings == nil {
			channels = append(channels, &channelState{input: ch})
		}
	}
	// apply toma una configuración nueva de Canales sin reiniciar: los canales
	// quitados cortan sus streaming y salen de la lista de videos, los pausados
	// cortan el chat (sus vivos se siguen revisando) y los nuevos los resuelve
	// la revisión siguiente, que se adelanta solo si hay alguno (cuesta cuota).
	apply := func(list []ChannelSetting, now time.Time) {
		old := map[string]*channelState{}
		for _, c := range channels {
			old[c.input] = c
		}
		var next []*channelState
		added := false
		for _, set := range list {
			c := old[set.Input]
			if c == nil {
				c, added = &channelState{input: set.Input}, true
			}
			delete(old, set.Input)
			c.paused, c.peak, c.off = set.Paused, set.peak(), set.off()
			next = append(next, c)
		}
		gone, paused := map[string]bool{}, map[string]bool{}
		for _, c := range channels {
			if old[c.input] == c && c.resolved {
				gone[c.info.ID] = true
				log.Printf("%s se quitó de la configuración: dejo de seguirlo", c.info.Title)
			}
		}
		channels = next
		for _, c := range channels {
			if c.resolved {
				paused[c.info.ID] = c.paused
			}
		}
		var kept []Video
		for _, v := range lives {
			c := chats[v.ID]
			switch {
			case gone[v.ChannelID]:
				if c.cancel != nil {
					c.cancel()
				}
				delete(chats, v.ID)
				continue
			case paused[v.ChannelID] && c.cancel != nil:
				c.held = true
				c.cancel()
			}
			kept = append(kept, v)
		}
		lives = kept
		if len(gone) > 0 {
			var vids []Video
			for _, v := range st.Status().Videos {
				if !gone[v.ChannelID] {
					vids = append(vids, v)
				}
			}
			title := principal(channels)
			if title == "" {
				title = st.Status().Channel
			}
			st.SetVideos(title, vids)
			for id := range gone {
				delete(groupLast, id)
			}
		}
		publish()
		if added {
			nextVideos = now
		}
	}

	openStream := func(v Video, c *chatState, now time.Time) {
		c.last = now
		if c.recent = append(c.recent, now); len(c.recent) > 10 {
			c.recent = c.recent[1:]
		}
		// Derivado de ctx: cancelar el worker corta también las conexiones abiertas.
		sctx, cancel := context.WithCancel(ctx)
		c.open, c.cancel, c.opened = true, cancel, now
		streams++
		go func(id, chatID, token string) {
			err := yt.StreamChat(sctx, id, chatID, token, func(b ChatBatch) { events <- chatEvent{c: c, b: b} })
			events <- chatEvent{c: c, done: true, err: err}
		}(v.ID, c.chatID, c.token)
	}

	for {
		now := time.Now()
		if cfg.Settings != nil {
			if list, v := cfg.Settings.Get(); v != version {
				version = v
				apply(list, now)
			}
		}
		used := st.Units()

		reviewed := false
		if now.After(nextVideos) && used < cfg.Budget {
			nextVideos = now.Add(cfg.VideosEvery)
			reviewed = true
			err := spend(func() error {
				var infos []ChannelInfo
				var lookupErr error
				for i, c := range channels {
					if !c.resolved && c.err == "" {
						info, err := yt.LookupChannel(c.input)
						switch {
						case err != nil && cfg.Settings != nil && strings.HasPrefix(err.Error(), "no se encontró el canal"):
							// Agregado desde Canales y YouTube no lo conoce: se anota y no se insiste.
							c.err = err.Error()
						case err != nil && cfg.Settings != nil:
							// Falla de YouTube o de la red: los demás se revisan igual y
							// este se vuelve a pedir en la revisión siguiente.
							lookupErr = lookupFailed{fmt.Errorf("canal %s: %w", c.input, err)}
							log.Printf("canal %s: %v (se vuelve a buscar en la próxima revisión)", c.input, err)
							continue
						case err != nil:
							return err
						}
						for _, o := range channels {
							if err == nil && o != c && o.resolved && o.info.ID == info.ID {
								// Otro link o @handle de un canal que ya está: no se sigue dos veces.
								c.err = "ya está como " + o.info.Title
							}
						}
						if c.err != "" {
							log.Printf("canal %s: %s", c.input, c.err)
							publish()
							continue
						}
						if i == 0 {
							st.SetVideos(info.Title, nil)
						}
						c.info, c.resolved = info, true
					}
					if c.resolved {
						infos = append(infos, c.info)
					}
				}
				if lookupErr != nil && len(infos) == 0 {
					// Ningún canal resuelto (por ejemplo, arrancó sin red): es una
					// falla común y se reintenta a los 30 s.
					return lookupErr.(lookupFailed).error
				}
				var vids []Video
				if len(infos) > 0 {
					all := len(infos) > 1 && !now.Before(nextExtra)
					var err error
					if vids, err = yt.ChannelVideos(infos, cfg.Videos, cfg.Extra, all); err != nil {
						return err
					}
					if all {
						nextExtra, extraKept = now.Add(extraUploadsEvery), extraUploads(vids, infos)
					} else {
						vids = withExtraUploads(vids, extraKept, infos)
					}
				}
				title := principal(channels)
				if title == "" {
					title = st.Status().Channel
				}
				markShorts(vids, shortIDs)
				st.SetVideos(title, vids)
				// Los espectadores, los likes y los vivos de cada canal vienen en
				// el mismo pedido de videos: las series no cuestan cuota.
				st.AddViewers(time.Now(), vids)
				st.AddLikes(time.Now(), vids)
				st.AddLives(time.Now(), vids)
				st.AddVideos(time.Now(), vids)
				lives = nil
				current := map[string]bool{}
				for _, v := range vids {
					if v.LiveStatus == "live" && v.LiveChatID != "" {
						lives = append(lives, v)
						current[v.ID] = true
						if c := chats[v.ID]; c == nil || c.chatID != v.LiveChatID {
							if c != nil && c.cancel != nil {
								c.cancel()
							}
							chats[v.ID] = &chatState{chatID: v.LiveChatID}
						}
					}
				}
				// Los vivos que terminaron se olvidan; si vuelven, se empieza de cero.
				for id, c := range chats {
					if !current[id] {
						if c.cancel != nil {
							c.cancel()
						}
						delete(chats, id)
					}
				}
				trackChannels(channels, vids, now)
				var noLive []string
				for _, c := range channels {
					if c.resolved && !c.live {
						noLive = append(noLive, c.info.ID)
					}
					// Esta revisión ya miró sus vivos: la rápida espera fastEvery desde acá.
					if !c.live && now.Before(c.fastUntil) {
						c.nextFast = now.Add(fastEvery)
					}
				}
				// Para Sin vivo: el monitor miró estos canales y no tenían vivo
				// (sin cuota extra; si la revisión falló, no se guarda nada).
				st.AddNoLive(time.Now(), noLive)
				publish()
				return lookupErr
			})
			st.SetError(err)
			// Si solo falló la búsqueda de un canal nuevo, la revisión anduvo: ese
			// canal se vuelve a pedir en la siguiente, sin repetir todo a los 30 s.
			if err != nil && !errors.As(err, new(lookupFailed)) {
				log.Printf("videos: %v", err)
				nextVideos = now.Add(30 * time.Second) // reintento rápido
			}
		}

		// Shorts de cada canal y videos con comentarios que no están en el
		// archivo, cada shortsEvery: 1 unidad por canal y 1 cada 50 faltantes.
		// Como la búsqueda rápida: nunca en el tick de la revisión completa.
		if !reviewed && !now.Before(nextShorts) && st.Units() < cfg.Budget {
			var infos []ChannelInfo
			for _, c := range channels {
				if c.resolved {
					infos = append(infos, c.info)
				}
			}
			if len(infos) > 0 {
				nextShorts = now.Add(shortsEvery)
				reviewShorts(yt, st, spend, infos, shortIDs, classified, askedMissing)
			}
		}

		// Búsqueda rápida del próximo vivo: nunca en el mismo tick que la
		// revisión completa y nunca con el tope de cuota alcanzado.
		for _, c := range channels {
			if reviewed || c.live || !now.Before(c.fastUntil) || now.Before(c.nextFast) || st.Units() >= cfg.Budget {
				continue
			}
			c.nextFast = now.Add(fastEvery)
			var found []Video
			err := spend(func() (err error) {
				found, err = yt.ChannelLives(c.info.Uploads)
				return err
			})
			if err != nil {
				log.Printf("búsqueda rápida de %s: %v", c.info.Title, err)
				continue
			}
			for i := range found {
				if found[i].ChannelID == "" { // salió de la lista de vivos de este canal
					found[i].ChannelID = c.info.ID
				}
			}
			st.AddLives(time.Now(), found)
			live := false
			for _, v := range found {
				if v.LiveStatus != "live" {
					continue
				}
				live = true
				// Su chat se abre apenas lo deje el intervalo, sin esperar la revisión completa.
				if v.LiveChatID != "" && chats[v.ID] == nil {
					lives = append(lives, v)
					chats[v.ID] = &chatState{chatID: v.LiveChatID}
				}
			}
			if !live {
				st.AddNoLive(time.Now(), []string{c.info.ID})
				continue
			}
			log.Printf("vivo nuevo de %s encontrado a los %s", c.info.Title, humanDur(now.Sub(c.waitSince)))
			merged := mergeChannelLives(st.Status().Videos, c.info.ID, found)
			st.SetVideos(st.Status().Channel, merged)
			trackChannels(channels, merged, now)
			publish()
		}

		if now.After(nextComments) && used < cfg.Budget {
			nextComments = now.Add(cfg.CommentsEvery)
			since := lastComments.Add(-time.Hour) // un poco de solapamiento; los repetidos se ignoran
			lastComments = now
			snap := st.Snapshot()
			total := 0
			for _, v := range snap.Videos {
				if v.Comments == 0 || v.Comments == knownComments[v.ID] {
					continue
				}
				var cs []Message
				err := spend(func() (err error) {
					cs, err = yt.Comments(v.ID, since, 5)
					return err
				})
				if err != nil {
					log.Printf("comentarios de %s: %v", v.ID, err)
					continue
				}
				knownComments[v.ID] = v.Comments
				total += st.Add(cs)
			}
			if total > 0 {
				log.Printf("%d comentarios nuevos", total)
			}
		}

		// El chat se recibe en tiempo real por streaming: una conexión por vivo que
		// queda abierta y cuesta 5 unidades al abrirse. Si YouTube no lo permite,
		// se lee como antes, por turnos. En los dos casos el intervalo es entre dos
		// conexiones o lecturas cualesquiera de los vivos de un canal (cada canal
		// con el suyo): si el streaming se corta seguido, la cuota no gasta más
		// que con las lecturas comunes.
		chatOK := used < cfg.Budget-reserveForComments
		every, peak := cfg.chatInterval(now)
		groups := map[string]chatGroup{"": {last: groupLast[""], every: every}}
		if cfg.Settings != nil {
			for _, c := range channels {
				if c.resolved {
					g := chatGroup{last: groupLast[c.info.ID], every: c.off, paused: c.paused}
					if peak {
						g.every = c.peak
					}
					groups[c.info.ID] = g
				}
			}
		}
		var turn []Video // los que se turnan; los de tiempo real continuo van aparte
		for _, v := range lives {
			c := chats[v.ID]
			if !cfg.RealTime[v.ID] || c.noStream || c.blocked {
				turn = append(turn, v)
				continue
			}
			if !c.open && chatOK && !groups[groupKey(v, groups)].paused && now.Sub(c.last) >= max(realTimeGap, c.wait) {
				openStream(v, c, now)
			}
		}
		live, _ := nextChat(turn, chats, groups, now)
		streaming, polling, open, held := 0, 0, 0, 0
		for _, v := range lives {
			if c := chats[v.ID]; !c.blocked && groups[groupKey(v, groups)].paused {
				held++
			} else if !c.blocked && c.noStream {
				polling++
			} else if !c.blocked {
				streaming++
				if c.open {
					open++
				}
			}
		}
		note := ""
		switch {
		case len(lives) == 0:
			note = "No hay un vivo en este momento."
		case streaming+polling+held == 0:
		case !chatOK:
			note = fmt.Sprintf("Chat en pausa: se llegó al tope de cuota del día (%d unidades). Se reanuda cuando se reinicia la cuota.", cfg.Budget-reserveForComments)
			if streaming > 0 {
				note = fmt.Sprintf("Se llegó al tope de cuota del chat (%d unidades): los vivos conectados siguen en tiempo real, pero si se corta la conexión no se vuelve a abrir hasta que se reinicie la cuota.", cfg.Budget-reserveForComments)
			}
		default:
			// Cuántos vivos están conectados en tiempo real no se dice: no le
			// servía a quien mira (la tarjeta Canal ya dice cómo llega el chat).
			var parts []string
			if polling > 0 {
				p := "Leyendo el chat cada " + humanDur(every)
				if len(groups) > 1 {
					p = fmt.Sprintf("Leyendo el chat con consultas comunes en %d vivo(s), cada canal con su intervalo", polling)
				} else if polling > 1 {
					p = fmt.Sprintf("Leyendo el chat por turnos entre %d vivos: cada uno cada %s", polling, humanDur(every*time.Duration(polling)))
				}
				if streaming > 0 {
					p = "Sin tiempo real en " + strconv.Itoa(polling) + " vivo(s): " + strings.ToLower(p[:1]) + p[1:]
				}
				if peak {
					p += fmt.Sprintf(" (horario pico, %02d a %02d h)", cfg.PeakFrom, cfg.PeakTo)
				}
				parts = append(parts, p)
			}
			if held > 0 {
				parts = append(parts, fmt.Sprintf("Chat en pausa desde Canales en %d vivo(s)", held))
			}
			if len(parts) > 0 {
				note = strings.Join(parts, ". ") + "."
			}
			if live == nil {
				break
			}
			c := chats[live.ID]
			groupLast[groupKey(*live, groups)] = now
			if !c.noStream {
				openStream(*live, c, now)
				break
			}
			c.last = now
			if c.recent = append(c.recent, now); len(c.recent) > 10 {
				c.recent = c.recent[1:]
			}
			var msgs []Message
			err := spend(func() (err error) {
				msgs, c.token, c.wait, err = yt.LiveChatPage(live.ID, c.chatID, c.token)
				return err
			})
			if err != nil {
				chatFailed(c, live.ID, err, false)
				break
			}
			c.note, c.got = "", now
			TagProgram(msgs, live.Title, live.Channel)
			st.Add(msgs)
		}
		byLive := map[string]string{}
		for id, c := range chats {
			if c.note != "" {
				byLive[id] = c.note
			}
		}
		st.SetChatNotes(note, byLive, chatInfos(lives, chats, groups, cfg, now, cfg.Budget-reserveForComments-used))

		select {
		case ev := <-events:
			if ev.done {
				streams-- // antes de handle: el done de un vivo olvidado no llega al final de handle
			}
			handle(ev)
		case <-tick.C:
		case <-ctx.Done():
			tick.Stop()
			for _, c := range chats {
				if c.cancel != nil {
					c.cancel()
				}
			}
			// Se sigue leyendo events hasta el último done: así ninguna goroutine
			// queda trabada en el envío y la cuota que ya respondió YouTube se suma.
			for streams > 0 {
				ev := <-events
				if ev.done {
					streams--
				}
				handle(ev)
			}
			return
		}
	}
}

// mergeChannelLives suma a la lista de videos los vivos que encontró la
// búsqueda rápida de un canal, en lugar de los vivos que ese canal tenía. Los
// videos comunes del canal y los de otros canales quedan como estaban hasta la
// próxima revisión completa.
func mergeChannelLives(videos []Video, channelID string, found []Video) []Video {
	out := make([]Video, 0, len(videos)+len(found))
	seen := map[string]bool{}
	for _, v := range found {
		seen[v.ID] = true
	}
	for _, v := range videos {
		if seen[v.ID] || v.ChannelID == channelID && v.LiveStatus != "none" {
			continue
		}
		out = append(out, v)
	}
	return append(append([]Video(nil), found...), out...)
}

// chatInfos arma el cartel de cada vivo. La próxima conexión o lectura se
// estima con la cola de nextChat dentro de su canal: primero el que hace más
// que no se lee, y entre uno y otro, el intervalo del canal.
func chatInfos(lives []Video, chats map[string]*chatState, groups map[string]chatGroup, cfg WorkerConfig, now time.Time, left int) map[string]ChatInfo {
	_, peak := cfg.chatInterval(now)
	realTime := func(v Video) bool {
		c := chats[v.ID]
		return cfg.RealTime[v.ID] && !c.noStream && !c.blocked
	}
	waiting := map[string][]*chatState{}
	active := map[string]int{} // vivos que se turnan en cada canal
	rate := 0
	for _, v := range lives {
		c, k := chats[v.ID], groupKey(v, groups)
		switch {
		case c.blocked:
		case realTime(v):
			if !groups[k].paused {
				rate += realTimeUnitsPerHour
			}
		default:
			active[k]++
			if !c.open {
				waiting[k] = append(waiting[k], c)
			}
		}
	}
	// Cada vivo en tiempo real continuo gasta lo suyo; cada canal, una conexión
	// o lectura de 5 unidades por intervalo entre todos sus vivos.
	for k, n := range active {
		if g := groups[k]; n > 0 && !g.paused && g.every > 0 {
			rate += int(5 * time.Hour / g.every)
		}
	}
	for _, w := range waiting {
		sort.SliceStable(w, func(i, j int) bool { return w[i].last.Before(w[j].last) })
	}
	out := map[string]ChatInfo{}
	for _, v := range lives {
		c, k := chats[v.ID], groupKey(v, groups)
		g := groups[k]
		ci := ChatInfo{Stream: !c.noStream, Open: c.open, Blocked: c.blocked, Paused: left <= 0, Opened: c.opened,
			LastDur: c.lastDur, Got: c.got, Gap: g.every, Peak: peak, Lives: active[k], RealTime: realTime(v), Left: max(left, 0), Rate: rate,
			Held: g.paused && !c.blocked}
		if ci.RealTime {
			ci.Next = c.last.Add(max(realTimeGap, c.wait))
		}
		if n := len(c.recent); n > 1 {
			ci.Avg, ci.Count = c.recent[n-1].Sub(c.recent[0])/time.Duration(n-1), n
		}
		for i, w := range waiting[k] {
			if w == c && !g.paused {
				ci.Next = g.last.Add(g.every * time.Duration(i+1))
				if t := c.last.Add(c.wait); t.After(ci.Next) {
					ci.Next = t
				}
			}
		}
		out[v.ID] = ci
	}
	return out
}

// humanDur muestra un intervalo como "45 s", "2 min" o "2 min 30 s".
func humanDur(d time.Duration) string {
	d = d.Round(time.Second)
	m, s := int(d/time.Minute), int(d%time.Minute/time.Second)
	switch {
	case m == 0:
		return fmt.Sprintf("%d s", s)
	case s == 0:
		return fmt.Sprintf("%d min", m)
	}
	return fmt.Sprintf("%d min %d s", m, s)
}

func isForbidden(err error) bool {
	return strings.Contains(err.Error(), "HTTP 403") || strings.Contains(err.Error(), "HTTP 401")
}
