package main

import (
	"context"
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
}

// YouTube corta cada conexión de tiempo real a los ~10 s. Los vivos de
// RealTime se reconectan después de esta espera, sin turnos: 5 unidades por
// conexión, unas 1.800 por hora y por vivo.
var realTimeGap = time.Second

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
	Gap      time.Duration // mínimo entre dos conexiones o lecturas cualesquiera
	Peak     bool          // Gap es el del horario pico
	Lives    int           // vivos que se turnan
	Avg      time.Duration // promedio entre las últimas conexiones o lecturas de este vivo
	Count    int           // cuántas entran en Avg
	RealTime bool          // tiempo real continuo (-tiempo-real): se reconecta apenas YouTube corta
	Left     int           // unidades que le quedan hoy al chat
	Rate     int           // unidades por hora que gasta el chat de todos los vivos, estimadas
}

// chatEvent es lo que manda la goroutine del streaming de un chat al worker.
type chatEvent struct {
	c    *chatState
	b    ChatBatch
	done bool // se cerró la conexión
	err  error
}

// nextChat elige el vivo al que le toca abrir el streaming del chat o hacer una
// lectura común: el que hace más tiempo que no se lee, entre los que no están
// bloqueados, no tienen ya el streaming abierto y cumplieron la espera que pidió
// YouTube. También devuelve cuántos vivos no están bloqueados.
func nextChat(lives []Video, chats map[string]*chatState, now time.Time) (next *Video, active int) {
	for i := range lives {
		c := chats[lives[i].ID]
		if c.blocked {
			continue
		}
		active++
		if !c.open && now.Sub(c.last) >= c.wait && (next == nil || c.last.Before(chats[next.ID].last)) {
			next = &lives[i]
		}
	}
	return next, active
}

// RunWorker corre para siempre en una sola goroutine, así el cliente no se comparte.
func RunWorker(yt *YouTube, st *Store, cfg WorkerConfig) {
	var (
		uploads       []string // lista de subidas de cada canal, en el orden de cfg.Channels
		nextVideos    time.Time
		nextComments  time.Time
		lastChat      time.Time
		lastComments  = time.Now().Add(-48 * time.Hour) // primera pasada: últimas 48 h
		knownComments = map[string]int64{}
		lives         []Video
		chats         = map[string]*chatState{} // por ID de vivo; el token es de cada chat
		events        = make(chan chatEvent, 256)
		tick          = time.NewTicker(time.Second)
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
		if ev.err != nil {
			chatFailed(c, live.ID, ev.err, true)
		} else {
			log.Printf("chat de %s: YouTube cerró la conexión de tiempo real a los %s", live.ID, humanDur(c.lastDur))
		}
	}

	openStream := func(v Video, c *chatState, now time.Time) {
		c.last = now
		if c.recent = append(c.recent, now); len(c.recent) > 10 {
			c.recent = c.recent[1:]
		}
		ctx, cancel := context.WithCancel(context.Background())
		c.open, c.cancel, c.opened = true, cancel, now
		go func(id, chatID, token string) {
			err := yt.StreamChat(ctx, id, chatID, token, func(b ChatBatch) { events <- chatEvent{c: c, b: b} })
			events <- chatEvent{c: c, done: true, err: err}
		}(v.ID, c.chatID, c.token)
	}

	for {
		now := time.Now()
		used := st.Units()
		every, peak := cfg.chatInterval(now)

		if now.After(nextVideos) && used < cfg.Budget {
			nextVideos = now.Add(cfg.VideosEvery)
			err := spend(func() error {
				for _, ch := range cfg.Channels[len(uploads):] {
					title, pl, err := yt.UploadsPlaylist(ch)
					if err != nil {
						return err
					}
					if len(uploads) == 0 {
						st.SetVideos(title, nil)
					}
					uploads = append(uploads, pl)
				}
				vids, err := yt.ChannelVideos(uploads, cfg.Videos, cfg.Extra)
				if err != nil {
					return err
				}
				st.SetVideos(st.Snapshot().Channel, vids)
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
				return nil
			})
			st.SetError(err)
			if err != nil {
				log.Printf("videos: %v", err)
				nextVideos = now.Add(30 * time.Second) // reintento rápido
			}
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
		// conexiones o lecturas cualesquiera: si el streaming se corta seguido, la
		// cuota no gasta más que con las lecturas comunes.
		chatOK := used < cfg.Budget-reserveForComments
		var turn []Video // los que se turnan; los de tiempo real continuo van aparte
		for _, v := range lives {
			c := chats[v.ID]
			if !cfg.RealTime[v.ID] || c.noStream || c.blocked {
				turn = append(turn, v)
				continue
			}
			if !c.open && chatOK && now.Sub(c.last) >= max(realTimeGap, c.wait) {
				openStream(v, c, now)
			}
		}
		live, active := nextChat(turn, chats, now)
		streaming, polling, open := 0, 0, 0
		for _, v := range lives {
			if c := chats[v.ID]; !c.blocked && c.noStream {
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
		case streaming+polling == 0:
		case !chatOK:
			note = fmt.Sprintf("Chat en pausa: se llegó al tope de cuota del día (%d unidades). Se reanuda cuando se reinicia la cuota.", cfg.Budget-reserveForComments)
			if streaming > 0 {
				note = fmt.Sprintf("Se llegó al tope de cuota del chat (%d unidades): los vivos conectados siguen en tiempo real, pero si se corta la conexión no se vuelve a abrir hasta que se reinicie la cuota.", cfg.Budget-reserveForComments)
			}
		default:
			var parts []string
			if streaming > 0 {
				parts = append(parts, fmt.Sprintf("Chat en tiempo real: %d de %d vivo(s) conectados ahora", open, streaming))
			}
			if polling > 0 {
				p := "Leyendo el chat cada " + humanDur(every)
				if polling > 1 {
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
			note = strings.Join(parts, ". ") + "."
			if live == nil || now.Sub(lastChat) < every {
				break
			}
			c := chats[live.ID]
			lastChat = now
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
		st.SetChatNotes(note, byLive, chatInfos(lives, chats, lastChat, cfg, now, active, cfg.Budget-reserveForComments-used))

		select {
		case ev := <-events:
			handle(ev)
		case <-tick.C:
		}
	}
}

// chatInfos arma el cartel de cada vivo. La próxima conexión o lectura se
// estima con la cola de nextChat: primero el que hace más que no se lee, y
// entre una y otra, el intervalo.
func chatInfos(lives []Video, chats map[string]*chatState, lastChat time.Time, cfg WorkerConfig, now time.Time, active, left int) map[string]ChatInfo {
	every, peak := cfg.chatInterval(now)
	realTime := func(id string) bool { c := chats[id]; return cfg.RealTime[id] && !c.noStream && !c.blocked }
	var waiting []*chatState
	for _, v := range lives {
		if c := chats[v.ID]; !c.blocked && !c.open && !realTime(v.ID) {
			waiting = append(waiting, c)
		}
	}
	sort.SliceStable(waiting, func(i, j int) bool { return waiting[i].last.Before(waiting[j].last) })
	// Cada vivo en tiempo real continuo gasta lo suyo; los demás, una conexión o
	// lectura de 5 unidades por intervalo entre todos.
	rate, turns := 0, false
	for _, v := range lives {
		if realTime(v.ID) {
			rate += realTimeUnitsPerHour
		} else if !chats[v.ID].blocked {
			turns = true
		}
	}
	if turns && every > 0 {
		rate += int(5 * time.Hour / every)
	}
	out := map[string]ChatInfo{}
	for _, v := range lives {
		c := chats[v.ID]
		ci := ChatInfo{Stream: !c.noStream, Open: c.open, Blocked: c.blocked, Paused: left <= 0, Opened: c.opened,
			LastDur: c.lastDur, Got: c.got, Gap: every, Peak: peak, Lives: active, RealTime: realTime(v.ID), Left: max(left, 0), Rate: rate}
		if ci.RealTime {
			ci.Next = c.last.Add(max(realTimeGap, c.wait))
		}
		if n := len(c.recent); n > 1 {
			ci.Avg, ci.Count = c.recent[n-1].Sub(c.recent[0])/time.Duration(n-1), n
		}
		for k, w := range waiting {
			if w == c {
				ci.Next = lastChat.Add(every * time.Duration(k+1))
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
