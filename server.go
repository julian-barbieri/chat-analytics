package main

import (
	"embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"maps"
	"math"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/htmx.min.js
var staticFS embed.FS

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"hora":   func(t time.Time) string { return t.Local().Format("15:04") },
	"fecha":  func(t time.Time) string { return t.Local().Format("02/01 15:04") },
	"miles":  miles,
	"plural": plural,
}).ParseFS(templatesFS, "templates/*.html"))

var categories = []string{CatPregunta, CatQueja, CatElogio, CatInsulto, CatSpam, CatOtro}

var catLabels = map[string]string{
	CatPregunta: "Preguntas", CatQueja: "Quejas", CatElogio: "Elogios",
	CatInsulto: "Insultos", CatSpam: "Spam", CatOtro: "Otros",
}

type Server struct {
	st      *Store
	budget  int
	analyst *Analyst         // nil si el análisis con Claude está apagado
	now     func() time.Time // nil: time.Now; los tests fijan la hora sin tocar time.Local
	chat    ChatSettings     // -chat-seg-pico, -chat-seg y -pico, para mostrarlos y estimar la cuota
	// Lo que dijo claudeConfig al arrancar (nunca la clave) y los flags de
	// Claude, para la tarjeta de Canales cuando el análisis está apagado.
	claudeWarn, claudeModel string
	claudeUSD               float64
}

// ChatSettings son los intervalos del chat que usa el worker (WorkerConfig).
type ChatSettings struct {
	Peak, Off        time.Duration
	PeakFrom, PeakTo int
}

// chatSettings devuelve los del Server o, si no se pasaron (los tests), los
// valores por defecto de los flags.
func (s *Server) chatSettings() ChatSettings {
	if s.chat.Peak == 0 && s.chat.Off == 0 {
		return ChatSettings{Peak: time.Minute, Off: 3 * time.Minute, PeakFrom: 7, PeakTo: 20}
	}
	return s.chat
}

func (s *Server) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.page)
	mux.HandleFunc("GET /programas", s.programsPage)
	mux.HandleFunc("GET /canales", s.channelsPage)
	mux.HandleFunc("GET /parcial/resumen", s.summary)
	mux.HandleFunc("GET /parcial/vivo", s.liveSection)
	mux.HandleFunc("GET /parcial/vivo/canal", s.liveChannel)
	mux.HandleFunc("GET /parcial/vivo/chat", s.liveChat)
	mux.HandleFunc("GET /parcial/vivo/analisis", s.liveAnalysis)
	mux.HandleFunc("GET /parcial/programas", s.programsPartial)
	mux.HandleFunc("GET /parcial/programas/detalle", s.programDetailPartial)
	mux.HandleFunc("GET /parcial/canales/estado", s.channelsState)
	mux.HandleFunc("POST /claude/analizar", s.claudeAnalyze)
	mux.HandleFunc("GET /export.csv", s.export)
	mux.Handle("GET /static/", http.FileServerFS(staticFS))
	return mux
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
	}
}

// page es la pantalla En vivo. La sección del canal va armada en la página
// (sus lugares se llenan con htmx), así el orden de las tarjetas se ve en el HTML.
func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	snap := s.st.Status()
	q := r.URL.Query()
	chanOf, _ := s.dayLives(snap, s.clock())
	sel, _ := pickChannel(snap, q.Get("canal"), q.Get("vivo"), chanOf)
	s.render(w, "page.html", map[string]any{"Tab": "vivo", "Channel": snap.Channel,
		"Vivo": map[string]any{"Canal": sel.Canal, "Video": sel.Video}})
}

type Tile struct {
	Label, Value, Note string
}

type Bar struct {
	X, Y, W, H float64
	Label      string
	N          int
	Class      string // clase CSS extra, por ejemplo "top"
}

type CatRow struct {
	Key, Label string
	N          int
	Pct        float64
}

// summary es la franja de estado de arriba de cada pantalla: cuándo se
// actualizó, el error de la API y la nota global del chat.
func (s *Server) summary(w http.ResponseWriter, r *http.Request) {
	snap := s.st.Status()
	s.render(w, "resumen", map[string]any{
		"Channel":  snap.Channel,
		"ChatNote": snap.ChatNote,
		"LastErr":  snap.LastErr,
		"Updated":  snap.Updated,
		"Started":  !snap.Updated.IsZero(),
	})
}

// catRows cuenta las categorías por reglas de unos mensajes.
func catRows(msgs []Message) []CatRow {
	counts := map[string]int{}
	for _, m := range msgs {
		counts[Classify(m.Text)]++
	}
	peak := 1
	for _, c := range categories {
		peak = max(peak, counts[c])
	}
	var rows []CatRow
	for _, c := range categories {
		rows = append(rows, CatRow{c, catLabels[c], counts[c], float64(counts[c]) * 100 / float64(peak)})
	}
	return rows
}

// LiveButton es un botón para elegir qué se ve: un canal de -canal (Canal) o
// un vivo suelto de -vivos (Canal vacío).
type LiveButton struct {
	Canal, Video, Label string // Video: el vivo en curso del canal, si tiene
	Active              bool
}

// liveSel es lo que muestra la sección del vivo.
type liveSel struct {
	Canal string        // ID del canal; vacío si es un vivo suelto o no hay nada
	Ch    ChannelStatus // el canal elegido
	Video string        // vivo en curso del canal (el primero) o el vivo suelto; vacío si el canal espera
	Label string
}

// pickChannel arma un botón por canal de -canal (con o sin vivo, en ese orden)
// y uno por vivo suelto en curso, y decide qué se muestra: el canal pedido, o
// el canal del vivo pedido (chanOf traduce vivo → canal, también de vivos que
// ya terminaron), o el vivo suelto pedido, o el primer canal en vivo. Solo
// cambia lo que se ve: el worker sigue leyendo todos los chats.
func pickChannel(snap Snapshot, canal, video string, chanOf map[string]string) (liveSel, []LiveButton) {
	known := map[string]bool{}
	for _, c := range snap.Channels {
		known[c.ID] = true
	}
	byChan := map[string][]Video{}
	var loose []Video
	for _, v := range snap.Videos {
		switch {
		case v.LiveStatus != "live":
		case known[v.ChannelID]:
			byChan[v.ChannelID] = append(byChan[v.ChannelID], v)
		default:
			loose = append(loose, v)
		}
	}
	var btns []LiveButton
	for _, c := range snap.Channels {
		b := LiveButton{Canal: c.ID, Label: channelName(c) + " · esperando el próximo vivo"}
		if lv := byChan[c.ID]; len(lv) > 0 {
			b.Video, b.Label = lv[0].ID, channelName(c)+" · en vivo"
		}
		btns = append(btns, b)
	}
	labels := map[string]int{}
	for _, v := range loose {
		labels[LiveLabel(v.Title, v.Channel)]++
	}
	for _, v := range loose {
		l := LiveLabel(v.Title, v.Channel)
		if labels[l] > 1 { // dos vivos sueltos del mismo canal: se distinguen por el programa
			l += " · " + ProgramName(v.Title, v.Channel)
		}
		btns = append(btns, LiveButton{Video: v.ID, Label: l})
	}
	pick := -1
	find := func(ok func(LiveButton) bool) {
		for i, b := range btns {
			if pick < 0 && ok(b) {
				pick = i
			}
		}
	}
	if canal != "" {
		find(func(b LiveButton) bool { return b.Canal == canal })
	}
	if ch := chanOf[video]; video != "" && ch != "" {
		find(func(b LiveButton) bool { return b.Canal == ch })
	}
	if video != "" {
		find(func(b LiveButton) bool { return b.Canal == "" && b.Video == video })
	}
	find(func(b LiveButton) bool { return b.Canal != "" && b.Video != "" }) // un canal en vivo
	find(func(b LiveButton) bool { return b.Canal == "" })                  // un vivo suelto
	find(func(LiveButton) bool { return true })
	if pick < 0 {
		return liveSel{}, nil
	}
	btns[pick].Active = true
	b := btns[pick]
	sel := liveSel{Canal: b.Canal, Video: b.Video, Label: b.Label}
	for _, c := range snap.Channels {
		if c.ID == b.Canal {
			sel.Ch = c
		}
	}
	// Con varios vivos del canal a la vez, el pedido (si sigue en curso) es el que se mira.
	for _, v := range byChan[b.Canal] {
		if v.ID == video {
			sel.Video = video
		}
	}
	return sel, btns
}

func channelName(c ChannelStatus) string {
	if c.Name != "" {
		return c.Name
	}
	return c.ID
}

// dayLives dice de qué canal de -canal es cada vivo. chanOf sirve para
// traducir ?vivo= y tiene todos los videos de la lista más los vivos de hoy de
// datos.vivos.jsonl (un vivo terminado de un canal extra ya no está en la
// lista). today, para el feed, tiene solo los vivos en curso y los de hoy: no
// los videos viejos de la lista (subidas, vivos de otros días).
func (s *Server) dayLives(snap Snapshot, now time.Time) (chanOf, today map[string]string) {
	known := map[string]bool{}
	for _, c := range snap.Channels {
		known[c.ID] = true
	}
	chanOf, today = map[string]string{}, map[string]string{}
	for _, l := range s.st.Lives(dayStart(now)) {
		if known[l.ChannelID] {
			chanOf[l.VideoID], today[l.VideoID] = l.ChannelID, l.ChannelID
		}
	}
	for _, v := range snap.Videos {
		if !known[v.ChannelID] {
			continue
		}
		chanOf[v.ID] = v.ChannelID
		if v.LiveStatus == "live" {
			today[v.ID] = v.ChannelID
		}
	}
	return chanOf, today
}

// channelVideos son los vivos de hoy de un canal, o el vivo suelto.
func channelVideos(sel liveSel, today map[string]string) map[string]bool {
	ids := map[string]bool{}
	if sel.Video != "" {
		ids[sel.Video] = true
	}
	for id, ch := range today {
		if sel.Canal != "" && ch == sel.Canal {
			ids[id] = true
		}
	}
	return ids
}

// searchText dice cómo se está buscando el próximo vivo de un canal. Con el
// tope de cuota alcanzado (paused) el worker no revisa nada.
func searchText(c ChannelStatus, now time.Time, paused bool) string {
	if paused {
		return "en pausa por el tope de cuota"
	}
	if now.Before(c.FastUntil) {
		return "buscando cada " + humanDur(fastEvery) + " hasta " + c.FastUntil.Local().Format("15:04")
	}
	every := c.Every
	if every <= 0 {
		every = 3 * time.Minute
	}
	return "buscando cada " + humanDur(every)
}

func (s *Server) liveSection(w http.ResponseWriter, r *http.Request) {
	snap := s.st.Status()
	q := r.URL.Query()
	chanOf, _ := s.dayLives(snap, time.Now())
	sel, _ := pickChannel(snap, q.Get("canal"), q.Get("video"), chanOf)
	s.render(w, "vivo", map[string]any{"Canal": sel.Canal, "Video": sel.Video})
}

// quotaPaused dice si se llegó al tope de cuota: el worker no revisa videos ni busca vivos.
func (s *Server) quotaPaused(snap Snapshot) bool { return s.budget > 0 && snap.Units >= s.budget }

// keepView son el "ver", las "horas" y lo plegado que está abierto del
// análisis, que manda el chat (con hx-include), para no perderlos cuando se
// recarga la sección.
func keepView(q url.Values) (ver, hours, open string) {
	if v := q.Get("ver"); v == "dia" || v == "programa" {
		ver = v
	}
	if h := q.Get("horas"); h == "24" || h == strconv.Itoa(chartHours) {
		hours = h
	}
	return ver, hours, foldValue(foldParam(q))
}

// foldSections son las secciones plegables de En vivo, en el orden en que se
// escriben en "abrir". Lista cerrada: nada de lo que llega se refleja tal cual.
var foldSections = []string{"preguntas", "quejas", "alertas"}

// foldParam lee qué secciones plegadas están abiertas ("abrir", separadas por
// coma). Un <details> abierto se cerraría en cada refresco de 30 s (htmx no
// conserva el atributo open), por eso el estado viaja en la URL como "ver".
func foldParam(q url.Values) map[string]bool {
	open := map[string]bool{}
	for _, v := range q["abrir"] {
		for _, sec := range strings.Split(v, ",") {
			if slices.Contains(foldSections, sec) {
				open[sec] = true
			}
		}
	}
	return open
}

// foldValue es el "abrir" de unas secciones abiertas, siempre en el mismo orden.
func foldValue(open map[string]bool) string {
	var out []string
	for _, sec := range foldSections {
		if open[sec] {
			out = append(out, sec)
		}
	}
	return strings.Join(out, ",")
}

// foldToggle es el "abrir" con una sección cambiada: abierta si estaba
// cerrada y al revés. Es lo que pide la flecha de esa sección.
func foldToggle(open map[string]bool, sec string) string {
	next := map[string]bool{sec: !open[sec]}
	for k, v := range open {
		if k != sec {
			next[k] = v
		}
	}
	return foldValue(next)
}

// Fold es una sección plegada: si va abierta y qué pide su flecha.
type Fold struct {
	Open bool
	Href string
}

// foldState es lo plegado abierto y la consulta del análisis (canal, video,
// ver y horas) para armar el link de cada flecha.
type foldState struct {
	Open  map[string]bool
	Query url.Values
}

func (f foldState) link(sec string) Fold {
	q := maps.Clone(f.Query)
	if q == nil {
		q = url.Values{}
	}
	if v := foldToggle(f.Open, sec); v != "" {
		q.Set("abrir", v)
	}
	return Fold{Open: f.Open[sec], Href: "/parcial/vivo/analisis?" + q.Encode()}
}

// analysisQuery es la consulta de "Programa en curso" o "Todo el día" de un canal.
func analysisQuery(canal, video, ver string, hours int) url.Values {
	return url.Values{"canal": {canal}, "video": {video}, "ver": {ver}, "horas": {strconv.Itoa(hours)}}
}

// ChatRow es una fila del chat en vivo, con su categoría ya calculada.
type ChatRow struct {
	Message
	Cat, CatLabel string
	SuperLabel    string // "Super Chat" o "Super Sticker"; vacío en mensajes comunes
}

var superLabels = map[string]string{SuperChat: "Super Chat", SuperSticker: "Super Sticker"}

func (s *Server) liveChat(w http.ResponseWriter, r *http.Request) {
	snap := s.st.Status()
	now := time.Now()
	q := r.URL.Query()
	chanOf, today := s.dayLives(snap, now)
	sel, btns := pickChannel(snap, q.Get("canal"), q.Get("video"), chanOf)
	// Sin canal en el pedido pero con un vivo (un link de antes, solo con
	// video=) basta con que ese vivo siga siendo el que se muestra. Sin canal
	// ni vivo (la página se abrió antes de la primera revisión), el canal
	// también cuenta: así aparecen la tira y el selector apenas hay canales.
	checkChannel := q.Get("canal") != "" || q.Get("video") == ""
	if (checkChannel && sel.Canal != q.Get("canal")) || sel.Video != q.Get("video") {
		// Cambió el vivo en curso del canal (o terminó el vivo suelto): se
		// recarga toda la sección, también el análisis. Nunca se pasa a otro
		// canal: pickChannel se queda con el pedido mientras exista.
		w.Header().Set("HX-Retarget", "#vivo")
		w.Header().Set("HX-Reswap", "outerHTML")
		ver, hours, open := keepView(q)
		s.render(w, "vivo", map[string]any{"Canal": sel.Canal, "Video": sel.Video, "Ver": ver, "Horas": hours, "Abrir": open})
		return
	}
	// El feed es del canal: los vivos de hoy, así al cambiar de vivo no se
	// pierde lo anterior.
	var rows []ChatRow
	for _, m := range s.st.LastChatOf(channelVideos(sel, today), 60) {
		c := Classify(m.Text)
		rows = append(rows, ChatRow{m, c, catLabels[c], superLabels[m.Super]})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].At.After(rows[j].At) })
	s.render(w, "vivo-chat", map[string]any{
		"Has": len(btns) > 0, "Waiting": sel.Canal != "" && sel.Video == "",
		"Rows": rows, "Note": snap.ChatNotes[sel.Video], "ChatNote": snap.ChatNote,
		"Banner": chatBanner(snap.ChatInfo, sel.Video, now), "Every": chatEvery(snap.ChatInfo, sel.Video),
	})
}

// chatEvery es el resumen del encabezado del chat: cómo llega y cada cuánto
// se le consulta a YouTube como mínimo (el detalle va en el cartel).
func chatEvery(infos map[string]ChatInfo, id string) string {
	ci, ok := infos[id]
	switch {
	case !ok || ci.Blocked:
		return ""
	case ci.RealTime:
		return "llega en tiempo real continuo"
	case ci.Stream:
		return "llega en tiempo real · lectura mínima cada " + humanDur(ci.Gap)
	}
	return "se lee con consultas comunes · como mínimo cada " + humanDur(ci.Gap)
}

// ChannelCard es la tarjeta Canal de En vivo.
type ChannelCard struct {
	Buttons                         []LiveButton
	Live                            bool
	Program, Since, Nth, Href       string
	Watching                        int64
	Waiting, Banner, How, StripAria string
	Strip                           []StripBlock
}

// liveChannel es la tarjeta Canal (cada 5 s). No recarga la sección: el que
// detecta el cambio de vivo es el chat (cada 2 s), como antes.
func (s *Server) liveChannel(w http.ResponseWriter, r *http.Request) {
	snap := s.st.Status()
	now := s.clock()
	q := r.URL.Query()
	chanOf, _ := s.dayLives(snap, now)
	sel, btns := pickChannel(snap, q.Get("canal"), q.Get("video"), chanOf)
	s.render(w, "vivo-canal", s.channelCard(snap, sel, btns, now))
}

// channelCard arma la tarjeta Canal: qué está al aire (o qué se espera), la
// tira de hoy y cómo sale el canal según los últimos 7 días.
func (s *Server) channelCard(snap Snapshot, sel liveSel, btns []LiveButton, now time.Time) ChannelCard {
	c := ChannelCard{Buttons: btns}
	var video Video
	for _, v := range snap.Videos {
		if v.ID == sel.Video && v.ID != "" {
			video = v
		}
	}
	if video.ID != "" {
		c.Live, c.Href = true, "https://www.youtube.com/watch?v="+url.QueryEscape(video.ID)
		if c.Program = ProgramName(video.Title, video.Channel); c.Program == "" {
			c.Program = video.Title
		}
		c.Watching = video.Concurrent
	}
	if sel.Canal == "" {
		return c // un vivo suelto: no tiene tira ni historial de canal
	}
	paused := s.quotaPaused(snap)
	if video.ID == "" {
		c.Waiting = fmt.Sprintf("Esperando el próximo vivo de %s (%s). Mientras tanto queda lo de hoy.", channelName(sel.Ch), searchText(sel.Ch, now, paused))
	}
	c.Watching = 0
	for _, v := range snap.Videos {
		if v.ChannelID == sel.Canal && v.LiveStatus == "live" {
			c.Watching += v.Concurrent
		}
	}
	week := s.st.Lives(now.Add(-7 * 24 * time.Hour))
	live := liveOf(snap.Videos, sel.Canal)
	from := dayStart(now)
	day := dayStrip(ProgramSegments(week, sel.Canal, live, from, now), sel.Ch, from, now, paused)
	c.Strip, c.Banner, c.StripAria = day["Blocks"].([]StripBlock), day["Banner"].(string), day["Aria"].(string)
	segs := ProgramSegments(week, sel.Canal, live, claudeFrom(now), now)
	if cur, ok := currentSegment(snap.Videos, segs, sel.Canal, sel.Video); ok && video.ID != "" {
		c.Since = "desde las " + cur.Start.Local().Format("15:04")
		c.Nth = nthLive(segs, cur.VideoID, now)
	}
	c.How = howItAirs(ProgramSegments(week, sel.Canal, live, now.Add(-7*24*time.Hour), now))
	return c
}

// nthLive dice "2.º vivo de hoy" si el canal tuvo más de un vivo hoy.
func nthLive(segs []Segment, video string, now time.Time) string {
	var order []string
	seen := map[string]bool{}
	for _, sg := range segs {
		if sg.State == SegNone || sg.End.Before(dayStart(now)) || seen[sg.VideoID] {
			continue
		}
		seen[sg.VideoID] = true
		order = append(order, sg.VideoID)
	}
	if len(order) < 2 {
		return ""
	}
	for i, id := range order {
		if id == video {
			return fmt.Sprintf("%d.º vivo de hoy", i+1)
		}
	}
	return ""
}

// Cómo sale un canal, según lo que se vio en los últimos 7 días.
const (
	airs24h     = "Un vivo de 24 h: el programa se toma del título, que cambia con cada uno"
	airsPerShow = "Un vivo nuevo por programa: al cortarse uno, el monitor busca el siguiente"
	airsUnknown = "Todavía no se vio un cambio de programa"
)

// howItAirs deduce cómo sale un canal con sus tramos: un mismo vivo con dos
// o más programas es un vivo de 24 h; dos o más vivos en un mismo día, un
// vivo por programa.
func howItAirs(segs []Segment) string {
	progs := map[string]map[string]bool{} // vivo → programas
	byDay := map[time.Time]map[string]bool{}
	for _, sg := range segs {
		if sg.State == SegNone {
			continue
		}
		if progs[sg.VideoID] == nil {
			progs[sg.VideoID] = map[string]bool{}
		}
		progs[sg.VideoID][sg.Program] = true
		if len(progs[sg.VideoID]) > 1 {
			return airs24h
		}
		d := dayStart(sg.Start)
		if byDay[d] == nil {
			byDay[d] = map[string]bool{}
		}
		byDay[d][sg.VideoID] = true
	}
	for _, vids := range byDay {
		if len(vids) > 1 {
			return airsPerShow
		}
	}
	return airsUnknown
}

// Banner es el cartel del vivo elegido: si el chat está conectado en tiempo
// real y cada cuánto se le consulta a YouTube.
type Banner struct {
	Class, Title string
	Lines        []string
}

func chatBanner(infos map[string]ChatInfo, id string, now time.Time) *Banner {
	ci, ok := infos[id]
	if !ok {
		return nil
	}
	in := func(t time.Time) string {
		if !t.After(now) {
			return "en instantes"
		}
		return "en " + humanDur(t.Sub(now))
	}
	var b Banner
	switch {
	case ci.Blocked:
		return &Banner{Class: "crit", Title: "YouTube no deja leer este chat con la API key: no se le consulta más mientras siga este vivo."}
	case ci.Open:
		b = Banner{Class: "ok", Title: "Conectado en tiempo real desde hace " + humanDur(now.Sub(ci.Opened)) + ": los mensajes llegan apenas se publican."}
	case ci.Paused:
		b = Banner{Class: "warn", Title: "Chat en pausa por el tope de cuota del día: se retoma cuando se reinicia la cuota."}
	case ci.RealTime:
		b = Banner{Class: "ok", Title: "Reconectando: YouTube corta la conexión de tiempo real cada ~10 s y se vuelve a abrir " + in(ci.Next) + "; no se pierden mensajes."}
	case !ci.Stream:
		b = Banner{Class: "warn", Title: "YouTube no da tiempo real en este chat: se lee con consultas comunes. Próxima consulta " + in(ci.Next) + "."}
	case ci.LastDur > 0:
		b = Banner{Class: "warn", Title: fmt.Sprintf("Sin conexión ahora: YouTube cortó la de tiempo real a los %s. Próxima conexión %s; ahí llegan juntos los mensajes que faltan.",
			humanDur(ci.LastDur), in(ci.Next))}
	default:
		b = Banner{Class: "warn", Title: "Todavía no se conectó a este chat. Primera conexión " + in(ci.Next) + "."}
	}
	if ci.RealTime {
		left := time.Duration(ci.Left) * time.Hour / time.Duration(max(ci.Rate, realTimeUnitsPerHour))
		b.Lines = append(b.Lines, fmt.Sprintf("Consultas al chat de YouTube: tiempo real continuo, una conexión apenas YouTube corta la anterior (unas 6 por minuto, ~%s unidades por hora).", miles(realTimeUnitsPerHour)),
			fmt.Sprintf("Quedan %s unidades para el chat hoy: al ritmo de todos los vivos (~%s por hora) alcanzan para unas %d h %02d min más; después el chat se pausa hasta que se reinicia la cuota.",
				miles(ci.Left), miles(max(ci.Rate, realTimeUnitsPerHour)), int(left/time.Hour), int(left%time.Hour/time.Minute)))
	} else {
		gap := "Consultas al chat de YouTube: como mínimo " + humanDur(ci.Gap) + " entre una y otra"
		if ci.Peak {
			gap += " (horario pico)"
		}
		if ci.Lives > 1 {
			gap += fmt.Sprintf(", por turnos entre %d vivos: si YouTube corta la conexión, a cada uno le toca cada %s", ci.Lives, humanDur(ci.Gap*time.Duration(ci.Lives)))
		}
		b.Lines = append(b.Lines, gap+".")
	}
	if ci.Count > 1 {
		b.Lines = append(b.Lines, fmt.Sprintf("En este vivo: una cada %s en promedio (últimas %d).", humanDur(ci.Avg), ci.Count))
	}
	if !ci.Got.IsZero() {
		b.Lines = append(b.Lines, "Último dato recibido de YouTube: hace "+humanDur(now.Sub(ci.Got))+".")
	}
	return &b
}

// bars convierte conteos en filas de barras horizontales (la más larga, 100 %).
func bars(counts []Count) []CatRow {
	peak := 1
	for _, c := range counts {
		peak = max(peak, c.N)
	}
	var out []CatRow
	for _, c := range counts {
		out = append(out, CatRow{c.Key, c.Key, c.N, float64(c.N) * 100 / float64(peak)})
	}
	return out
}

func (s *Server) liveAnalysis(w http.ResponseWriter, r *http.Request) {
	snap := s.st.Snapshot()
	q := r.URL.Query()
	hours := chartHours
	if q.Get("horas") == "24" {
		hours = 24
	}
	open := foldParam(q)
	for _, c := range snap.Channels {
		if c.ID == q.Get("canal") && c.ID != "" {
			s.channelAnalysis(w, snap, c, q.Get("video"), q.Get("ver"), hours, open, localRequest(r))
			return
		}
	}
	id := q.Get("video")
	var video Video
	var label string
	for _, v := range snap.Videos {
		if v.ID == id {
			video, label = v, LiveLabel(v.Title, v.Channel)
		}
	}
	// El wrapper se reemplaza entero en cada refresco para que conserve la
	// ventana del gráfico elegida (va en su hx-get).
	if label == "" {
		s.render(w, "vivo-analisis", map[string]any{"Video": id, "Hours": hours, "Abrir": foldValue(open)})
		return
	}
	now := s.clock()
	msgs := sinceFor(snap.Msgs, id, now.Add(-24*time.Hour))
	viewers := s.st.Viewers(id, now.Add(-24*time.Hour))
	data := analysisData(msgs, viewers, []string{id}, snap.Videos, video.Concurrent, now, hours, scopeLast24)
	data["Video"], data["Label"], data["Span"], data["Abrir"] = id, label, label+" · últimas 24 h", foldValue(open)
	data["Monitor"] = monitorView(snap.Msgs, now)
	// El análisis con Claude es por canal: un vivo suelto no lo tiene.
	data["Claude"] = claudeView(claudeInput{Configured: s.analyst != nil, Status: snap.Claude, Now: now})
	s.render(w, "vivo-analisis", data)
}

// scope es de qué mensajes habla el análisis, para los textos de las
// tarjetas; short y typical van en las notas de los tiles.
type scope struct{ of, in, window, short, typical string }

var (
	scopeLast24  = scope{"de las últimas 24 h", "en las últimas 24 h", "las últimas 24 h", "las últimas 24 h", "de las últimas 24 h"}
	scopeDay     = scope{"de hoy", "hoy", "lo de hoy (desde las 00:00)", "el día", "del día"}
	scopeProgram = scope{"del programa en curso", "en el programa en curso", "el programa en curso", "este programa", "de este programa"}
)

// monitorView es el bloque "Todo el monitor · últimas 24 h": palabras y
// autores de todos los mensajes (también comentarios y otros canales). Sin
// categorías: ya están en la tarjeta Categorías del alcance elegido.
func monitorView(all []Message, now time.Time) map[string]any {
	day := since(all, now.Add(-24*time.Hour))
	return map[string]any{"Total": len(day), "Words": TopWords(day, 20), "Authors": TopAuthors(day, 10)}
}

// liveTiles son los números de arriba del análisis, sobre los mensajes ya
// filtrados por el selector. Lo "típico" es la mediana de las franjas de
// 5 min anteriores con mensajes, y solo se dice con al menos 6.
func liveTiles(msgs []Message, concurrent int64, sc scope, now time.Time) []Tile {
	slots := map[int]int{}
	people, recent := map[string]bool{}, map[string]bool{}
	supers, questions := 0, 0
	byCur := map[string]int64{}
	for _, m := range msgs {
		people[m.Author] = true
		if m.Super != "" {
			supers++
			byCur[m.Currency] += m.AmountMicros
		}
		if Classify(m.Text) == CatPregunta {
			questions++
		}
		if m.Source != "chat" || m.At.After(now) {
			continue
		}
		k := int(now.Sub(m.At) / slotStep)
		slots[k]++
		if k == 0 {
			recent[m.Author] = true
		}
	}
	var past []float64
	for k, n := range slots {
		if k > 0 {
			past = append(past, float64(n))
		}
	}
	rate := "en los últimos 5 min"
	if len(past) >= 6 {
		if typ := median(past); typ > 0 {
			diff := math.Round((float64(slots[0]) - typ) / typ * 100)
			word := "sobre"
			if diff < 0 {
				diff, word = -diff, "bajo"
			}
			rate = fmt.Sprintf("%.0f %% %s lo típico %s", diff, word, sc.typical)
		}
	}
	watching, wnote := "—", "sin dato de espectadores"
	if concurrent > 0 {
		watching = miles(concurrent)
		wnote = pct(float64(len(recent))*100/float64(concurrent)) + " escribió en los últimos 5 min"
	}
	var snote string
	switch len(byCur) {
	case 0:
	case 1:
		for c, v := range byCur {
			snote = currencyLabel(c) + " " + money(v)
		}
	default:
		snote = fmt.Sprintf("en %d monedas", len(byCur))
	}
	return []Tile{
		{"Mensajes por minuto", decimal(float64(slots[0]) / 5), rate},
		{"Personas que escribieron", miles(len(people)), "en " + sc.short},
		{"Mirando ahora", watching, wnote},
		{"Super Chats", miles(supers), snote},
		{"Preguntas", miles(questions), "por reglas"},
	}
}

// analysisData arma las tarjetas del análisis con los mensajes ya filtrados
// (de un vivo, del día del canal o del programa en curso).
func analysisData(msgs []Message, viewers []ViewerSample, ids []string, videos []Video, concurrent int64, now time.Time, hours int, sc scope) map[string]any {
	a := Analyze(msgs)
	titles := videoTitles(videos)
	part := participationView(msgs, viewers, ids, videos, concurrent, titles, now, hours)
	part["Window"] = sc.window
	sup := superView(Supers(msgs, titles))
	sup["Of"], sup["In"] = sc.of, sc.in
	return map[string]any{
		"Hours": hours, "A": a, "Tech": bars(a.Tech), "Part": part, "Super": sup,
		"Tiles": liveTiles(msgs, concurrent, sc, now), "Cats": catRows(msgs),
	}
}

// channelAnalysis es el análisis de un canal: "Programa en curso" (el vivo
// actual con el programa actual, desde el inicio del tramo menos 5 min) o
// "Todo el día del canal" (sus vivos desde las 00:00 locales). Nunca entran
// mensajes de otro canal.
func (s *Server) channelAnalysis(w http.ResponseWriter, snap Snapshot, ch ChannelStatus, want, ver string, hours int, open map[string]bool, local bool) {
	now := s.clock()
	from := dayStart(now)
	samples := s.st.Lives(from)
	var lives []Video
	ids, live := map[string]bool{}, map[string]bool{}
	for _, v := range snap.Videos {
		if v.ChannelID == ch.ID && v.LiveStatus == "live" {
			lives = append(lives, v)
			ids[v.ID], live[v.ID] = true, true
		}
	}
	// El vivo que se mira va primero: es el del "programa en curso".
	sort.SliceStable(lives, func(i, j int) bool { return lives[i].ID == want && lives[j].ID != want })
	for _, l := range samples {
		if l.ChannelID == ch.ID {
			ids[l.VideoID] = true
		}
	}
	segs := ProgramSegments(samples, ch.ID, live, from, now)
	if ver != "dia" && ver != "programa" {
		ver = "dia"
		if len(lives) > 0 {
			ver = "programa"
		}
	}
	titles := videoTitles(snap.Videos)
	var (
		msgs       []Message
		viewers    []ViewerSample
		pids       []string
		concurrent int64
		span, note string
		sc         = scopeDay
	)
	if ver == "programa" && len(lives) > 0 {
		v := lives[0]
		prog := ProgramName(v.Title, v.Channel)
		start := from
		for _, sg := range segs {
			if sg.VideoID == v.ID && sg.Program == prog {
				start = sg.Start // el último tramo de ese programa
			}
		}
		// Desde 5 min antes (segmentLead): el cambio de título se ve hasta 3 min
		// tarde. Mismo filtro que el análisis con Claude.
		msgs = segmentMessages(snap.Msgs, Segment{VideoID: v.ID, Program: prog, Start: start, State: SegLive}, titles)
		start = start.Add(-segmentLead)
		viewers, pids, concurrent = s.st.Viewers(v.ID, start), []string{v.ID}, v.Concurrent
		span, sc = programLabel(prog)+" (programa en curso)", scopeProgram
	} else {
		if ver == "programa" {
			note = "No hay un programa al aire ahora: se muestra todo el día del canal."
		}
		for _, m := range snap.Msgs {
			if ids[m.VideoID] && !m.At.Before(from) {
				msgs = append(msgs, m)
			}
		}
		for id := range ids {
			pids = append(pids, id)
		}
		sort.Strings(pids)
		for _, id := range pids {
			viewers = append(viewers, s.st.Viewers(id, from)...)
		}
		for _, v := range lives {
			concurrent += v.Concurrent
		}
		span = "todo el día del canal"
	}
	video := ""
	if len(lives) > 0 {
		video = lives[0].ID
	}
	data := analysisData(msgs, viewers, pids, snap.Videos, concurrent, now, hours, sc)
	data["Canal"], data["Video"], data["Ver"], data["Label"], data["Span"], data["Note"] = ch.ID, video, ver, channelName(ch), span, note
	data["Abrir"] = foldValue(open)
	data["Monitor"] = monitorView(snap.Msgs, now)
	cardVer := "dia"
	if ver == "programa" && len(lives) > 0 {
		cardVer = "programa"
	}
	fold := foldState{Open: open, Query: analysisQuery(ch.ID, video, ver, hours)}
	data["Claude"] = s.claudeCard(snap, ch, video, cardVer, local, "", fold)
	data["ClaudeLine"] = s.claudeLine(snap, ch.ID, now)
	s.render(w, "vivo-analisis", data)
}

// claudeLine es la línea de Claude debajo del selector, con el análisis activo.
func (s *Server) claudeLine(snap Snapshot, canal string, now time.Time) string {
	if s.analyst == nil || snap.Claude.Off != "" {
		return ""
	}
	var last time.Time
	for _, a := range s.st.Analyses(canal, claudeFrom(now)) {
		if !a.To.Before(dayStart(now)) && a.At.After(last) {
			last = a.At
		}
	}
	if last.IsZero() {
		return "resumen al cerrar cada programa · todavía sin análisis hoy"
	}
	return "resumen al cerrar cada programa · último análisis " + last.Local().Format("15:04")
}

// claudeAnalyze es el botón "Analizar ahora": solo valida y encola. Nunca
// llama a Claude ni escribe el Store; lo hace RunAnalyst, con el tope.
func (s *Server) claudeAnalyze(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) || !localRequest(r) {
		http.Error(w, "Para pedir un análisis, abrí el dashboard en la PC del monitor.", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	snap := s.st.Snapshot()
	canal, video := r.PostForm.Get("canal"), r.PostForm.Get("video")
	for _, ch := range snap.Channels {
		if ch.ID != canal || canal == "" {
			continue
		}
		var notice string
		queued := false
		if s.analyst != nil && snap.Claude.Off == "" {
			var ok bool
			if ok, notice = s.analyst.Request(ch.ID, video, s.clock()); ok {
				queued = true
			}
		}
		// "En cola" sale del ok de Request y no de Queued: RunAnalyst ya pudo
		// haberlo sacado de la cola y la tarjeta volvería a mostrar el botón.
		in := s.claudeInput(snap, ch, video, "programa", true, notice, queued)
		// El botón manda con hx-include las horas y lo plegado abierto: las
		// flechas de la tarjeta nueva tienen que pedir el mismo análisis.
		hours := chartHours
		if r.PostForm.Get("horas") == "24" {
			hours = 24
		}
		in.Fold = foldState{Open: foldParam(r.PostForm), Query: analysisQuery(ch.ID, video, "programa", hours)}
		s.render(w, "claude-card", claudeView(in))
		return
	}
	http.Error(w, "Ese canal no está en -canal.", http.StatusBadRequest)
}

// claudeCard arma la tarjeta 7 de un canal con lo que ya está guardado.
func (s *Server) claudeCard(snap Snapshot, ch ChannelStatus, want, ver string, local bool, notice string, fold foldState) ClaudeCard {
	in := s.claudeInput(snap, ch, want, ver, local, notice, false)
	in.Fold = fold
	return claudeView(in)
}

// claudeInput junta lo que necesita claudeView. queued: el pedido del botón
// recién entró a la cola.
func (s *Server) claudeInput(snap Snapshot, ch ChannelStatus, want, ver string, local bool, notice string, queued bool) claudeInput {
	now := s.clock()
	in := claudeInput{Configured: s.analyst != nil, Status: snap.Claude, Canal: ch.ID, Ver: ver, Local: local, Notice: notice, Now: now}
	if !in.Configured {
		return in
	}
	from := claudeFrom(now)
	segs := ProgramSegments(s.st.Lives(from), ch.ID, liveOf(snap.Videos, ch.ID), from, now)
	in.Cur, in.HasCur = currentSegment(snap.Videos, segs, ch.ID, want)
	if in.HasCur {
		in.CurMsgs = segmentMessages(snap.Msgs, in.Cur, videoTitles(snap.Videos))
		in.ChannelName = channelName(ch)
	}
	in.Queued = queued || s.analyst.Queued(ch.ID)
	in.Analyses = s.st.Analyses(ch.ID, from)
	// Los ejemplos se buscan entre los mensajes guardados: vencen con la misma retención.
	need := map[string]bool{}
	for _, a := range in.Analyses {
		for _, gs := range [][]ClaudeGroup{a.Result.Questions, a.Result.Complaints} {
			for _, g := range gs {
				for _, id := range g.Examples {
					need[id] = true
				}
			}
		}
	}
	in.Msgs = map[string]Message{}
	for _, m := range snap.Msgs {
		if need[m.ID] {
			in.Msgs[m.ID] = m
		}
	}
	return in
}

// StripBlock es un tramo de la tira "Hoy en el canal". Text va en el
// aria-label y en el <title> del bloque.
type StripBlock struct {
	Class, Program, Range, State, Note, Text string
	X, W                                     float64 // en minutos desde las 00:00, para el SVG
}

var stripClass = map[string]string{SegEnded: "fin", SegLive: "aire", SegNone: "sin"}

// dayStrip arma la tira "Hoy en el canal" y el cartel de continuidad.
func dayStrip(segs []Segment, ch ChannelStatus, from, now time.Time, paused bool) map[string]any {
	var blocks []StripBlock
	for i, sg := range segs {
		end := sg.End
		current := sg.State == SegLive || sg.State == SegNone && i == len(segs)-1
		r := sg.Start.Local().Format("15:04") + " a " + end.Local().Format("15:04")
		if current {
			end, r = now, sg.Start.Local().Format("15:04")+" a ahora"
		}
		b := StripBlock{Class: stripClass[sg.State], Program: sg.Program, Range: r, State: sg.State,
			X: sg.Start.Sub(from).Minutes(), W: max(end.Sub(sg.Start).Minutes(), 1)}
		if sg.State == SegNone && current {
			b.Note = searchText(ch, now, paused)
		}
		parts := []string{r}
		if sg.Program != "" {
			parts = append(parts, sg.Program)
		}
		parts = append(parts, sg.State)
		if b.Note != "" {
			parts = append(parts, b.Note)
		}
		b.Text = strings.Join(parts, " · ")
		blocks = append(blocks, b)
	}
	return map[string]any{
		"Blocks": blocks, "W": max(now.Sub(from).Minutes(), 1), "Banner": ContinuityBanner(segs, now),
		"Aria": fmt.Sprintf("Hoy en el canal desde las %s: %d tramos", from.Local().Format("15:04"), len(blocks)),
	}
}

// Nota fija de la tarjeta y del informe para que nadie lea los totales como ingresos.
const superNote = "Montos brutos que pagó cada persona, en su moneda y sin convertir: no es lo que cobra el canal."

// SuperRow es una fila de las tablas de Super Chats con los números ya escritos.
type SuperRow struct {
	Program, Currency, Total string
	N                        int
}

// SuperItem es uno de los últimos pagos de la tarjeta.
type SuperItem struct {
	At                         time.Time
	Author, Kind, Amount, Text string
}

// superView arma la tarjeta de Super Chats y Super Stickers. Los textos van
// como string: html/template escapa los comentarios y los nombres.
func superView(sum SuperSummary) map[string]any {
	row := func(c SuperTotal) SuperRow {
		return SuperRow{programLabel(c.Program), currencyLabel(c.Currency), money(c.Micros), c.N}
	}
	var cur, prog []SuperRow
	for _, c := range sum.ByCurrency {
		cur = append(cur, row(c))
	}
	for _, c := range sum.ByProgram {
		prog = append(prog, row(c))
	}
	var last []SuperItem
	for _, m := range sum.Last {
		text := m.Text
		if strings.TrimSpace(text) == "" && m.Sticker != "" {
			text = "Super Sticker: " + m.Sticker
		}
		last = append(last, SuperItem{m.At, m.Author, superLabels[m.Super], m.AmountText, text})
	}
	return map[string]any{"ByCurrency": cur, "ByProgram": prog, "Last": last, "Note": superNote}
}

// money escribe un total en micros con separador de miles y dos decimales con
// coma, como se lee en Argentina (8500000000 → "8.500,00"). Los centavos se truncan.
func money(micros int64) string {
	sign := ""
	if micros < 0 {
		sign, micros = "-", -micros
	}
	return fmt.Sprintf("%s%s,%02d", sign, miles(micros/1_000_000), micros%1_000_000/10_000)
}

// usd escribe micro-dólares con dos decimales y coma, con los centavos
// redondeados para arriba (para no mostrar un costo menor al real).
func usd(micros int64) string {
	return money((micros + 9_999) / 10_000 * 10_000)
}

func currencyLabel(c string) string {
	if c == "" {
		return "sin moneda"
	}
	return c
}

func programLabel(p string) string {
	if p == "" {
		return "sin programa"
	}
	return p
}

// MomentRow es una fila de la tabla de los momentos con más participación,
// con los números ya escritos.
type MomentRow struct {
	Moment
	End                             time.Time
	ViewersText, PctText, TopicText string
	Href, Hint                      string
}

// Ventana del gráfico de participación: con las 24 h cada franja medía ~5 px
// y no se distinguían (se vio a mano en CA11), así que por defecto se grafican
// las últimas 6 h. El ranking sigue siendo de 24 h.
const (
	chartHours    = 6
	minChartSlots = 36 // 3 h: con menos, unas pocas franjas quedarían como bloques
)

// chartWindow recorta las franjas que se grafican: las últimas hours horas,
// sin las de antes del primer dato (chat o espectadores; por ejemplo, de antes
// de que se empezara a guardar), y nunca menos de minChartSlots. Un hueco sin
// chat después del primer dato se muestra: es información.
func chartWindow(slots []Slot, hours int) []Slot {
	first := 0
	for first < len(slots) && slots[first].N == 0 && slots[first].Viewers == 0 {
		first++
	}
	from := max(len(slots)-hours*int(time.Hour/slotStep), first)
	return slots[max(min(from, len(slots)-minChartSlots), 0):]
}

// participationView arma la sección de momentos con más participación de uno
// o varios vivos (los de un canal). concurrent es cuántos miran ahora.
func participationView(msgs []Message, viewers []ViewerSample, ids []string, videos []Video, concurrent int64, titles map[string]string, now time.Time, hours int) map[string]any {
	slots, typical, ok := Participation(msgs, viewers, ids, now)
	moments := TopMoments(slots, titles)
	shown := chartWindow(slots, hours)
	// Momentos del top que quedan antes de la ventana: se avisa para que no
	// parezca que faltan marcas.
	var outside []string
	for _, m := range moments {
		if len(shown) > 0 && m.Start.Before(shown[0].Start) {
			outside = append(outside, fmt.Sprintf("N.º %d", m.Rank))
		}
	}
	var outsideNote string
	if len(outside) > 0 {
		them := "verlos"
		if len(outside) == 1 {
			them = "verlo"
		}
		outsideNote = fmt.Sprintf("Antes de las %s, fuera de este gráfico: %s del top. Tocá «Últimas 24 h» para %s.",
			shown[0].Start.Local().Format("15:04"), strings.Join(outside, ", "), them)
	}
	var rows []MomentRow
	for _, m := range moments {
		row := MomentRow{Moment: m, End: m.Start.Add(slotStep), ViewersText: miles(m.Viewers), PctText: pct(m.Pct)}
		var topics []string
		for _, t := range m.Topics {
			topics = append(topics, t.Key)
		}
		row.TopicText = strings.Join(topics, ", ")
		row.Href, row.Hint = momentLink(videos, m.VideoID, m.Start, now)
		rows = append(rows, row)
	}
	// Por qué hay menos de 5, para que no parezca un error.
	var why string
	switch {
	case !ok:
		why = "Sin registro de cuántos miraban en las últimas 24 h: todavía no se puede armar el ranking por %."
	case len(rows) == 0:
		why = fmt.Sprintf("Ninguna franja con dato de espectadores tuvo al menos %d personas escribiendo.", minPersonasTop)
	case len(rows) < maxMomentos:
		why = fmt.Sprintf("Solo %d de %d: el resto de las franjas no llega a %d personas, no tiene dato de espectadores o está a menos de %d minutos de una elegida.",
			len(rows), maxMomentos, minPersonasTop, int(minSeparacion/time.Minute))
	}
	// Franjas con chat y sin muestra de espectadores: de antes de que se
	// guardaran o de cuando se cortó la revisión de videos.
	missing := false
	for _, s := range slots {
		missing = missing || s.N > 0 && s.Viewers == 0
	}
	return map[string]any{
		"Chart": participationChart(shown, moments, typical, ok, titles), "Moments": rows, "Why": why,
		"Windows": []int{chartHours, 24}, "Outside": outsideNote,
		"ByPct": ok, "Typical": pct(typical), "Concurrent": concurrent, "Missing": concurrent > 0 && missing,
	}
}

// Mark es el número de un momento del top arriba del gráfico; Tick, una hora
// debajo. Left es la posición en % del ancho: los textos van en HTML para que
// no se deformen con el SVG estirado (preserveAspectRatio="none").
type Mark struct {
	Left float64
	Rank int
}

type Tick struct {
	Left  float64
	Label string
	End   bool // está en el borde derecho: se alinea a la derecha para no desbordar
}

// ViewersLine es el panel de oyentes arriba de las barras: escalones con el
// promedio de espectadores de cada franja, cortados donde no hay dato.
type ViewersLine struct{ Path, Max, Aria string }

// viewersLine arma el panel de oyentes de la ventana graficada, con su propia
// escala desde 0 (para comparar "muchos" contra "pocos"). Son escalones y no
// una línea entre centros: cada valor es el promedio de toda la franja y una
// franja aislada igual se ve. Sin dato en la ventana no hay panel.
func viewersLine(slots []Slot, span string) (ViewersLine, bool) {
	var peak Slot
	missing := 0
	for _, s := range slots {
		if s.Viewers > peak.Viewers {
			peak = s
		}
		if s.Viewers == 0 {
			missing++
		}
	}
	if peak.Viewers == 0 {
		return ViewersLine{}, false
	}
	var path strings.Builder
	for i, s := range slots {
		if s.Viewers == 0 {
			continue // corta el tramo: el siguiente arranca con otro M, no baja a 0
		}
		y := 100 - float64(s.Viewers)/float64(peak.Viewers)*100
		if i == 0 || slots[i-1].Viewers == 0 {
			fmt.Fprintf(&path, "M%d %.2fH%d", i, y, i+1)
		} else {
			fmt.Fprintf(&path, "V%.2fH%d", y, i+1)
		}
	}
	aria := fmt.Sprintf("Cuántos miraban el vivo cada 5 minutos %s (promedio de lo que informa YouTube); máximo de %s a las %s",
		span, miles(peak.Viewers), peak.Start.Local().Format("15:04"))
	switch {
	case missing == 1:
		aria += "; sin dato en 1 franja"
	case missing > 1:
		aria += fmt.Sprintf("; sin dato en %d franjas", missing)
	}
	return ViewersLine{Path: path.String(), Max: "máx. " + miles(peak.Viewers) + " mirando", Aria: aria}, true
}

// participationChart arma las barras del % de participación cada 5 minutos,
// con los momentos del top marcados y la línea de la participación típica.
// Si no hay ningún dato de espectadores (ok false), muestra personas para no
// quedar vacío. El viewBox es de len(slots) × 100: una unidad por franja.
// slots puede ser una ventana de las 24 h (chartWindow).
func participationChart(slots []Slot, moments []Moment, typical float64, ok bool, titles map[string]string) map[string]any {
	w := max(len(slots), 1)
	rank := map[time.Time]int{}
	for _, m := range moments {
		rank[m.Start] = m.Rank
	}
	// Escala: en modo % la mayor franja elegible (las de pocas personas de
	// madrugada aplastarían al resto) o la típica, para que la línea se vea.
	var scale float64
	var best, peak Slot
	for _, s := range slots {
		if eligible(s) && (best.Viewers == 0 || ranksBefore(s, best)) {
			best = s
		}
		if s.Authors > peak.Authors {
			peak = s
		}
	}
	if ok {
		scale = max(best.Pct, typical)
	} else {
		scale = float64(peak.Authors)
	}
	var bars []Bar
	var marks []Mark
	for i, s := range slots {
		v := float64(s.Authors)
		if ok {
			v = 0
			if eligible(s) {
				v = s.Pct
			}
		}
		h := 0.0
		if v > 0 {
			h = max(v/scale*100, 1)
		}
		b := Bar{X: float64(i) + 0.1, Y: 100 - h, W: 0.8, H: h, N: s.Authors, Label: slotTitle(s, titles)}
		if r := rank[s.Start]; ok && r > 0 {
			b.Class = "top"
			b.Label += fmt.Sprintf(" · N.º %d de los %d con más participación", r, maxMomentos)
			marks = append(marks, Mark{Left: (float64(i) + 0.5) / float64(w) * 100, Rank: r})
		}
		bars = append(bars, b)
	}
	// Horas cada 30 min, 1 h, 2 h o 3 h según el ancho: a lo sumo 9 rótulos.
	step := 36
	for _, s := range []int{6, 12, 24} {
		if len(slots) <= 8*s {
			step = s
			break
		}
	}
	span := "en las últimas 24 horas"
	if len(slots) > 0 && len(slots) < slotCount {
		span = "desde las " + slots[0].Start.Local().Format("15:04")
	}
	var ticks []Tick
	for i := 0; i <= len(slots) && len(slots) > 0; i += step {
		ticks = append(ticks, Tick{Left: float64(i) / float64(w) * 100, Label: slots[0].Start.Add(time.Duration(i) * slotStep).Local().Format("15:04"), End: i == len(slots)})
	}
	out := map[string]any{"Bars": bars, "W": w, "Marks": marks, "Ticks": ticks, "ByPct": ok}
	if !ok {
		out["Max"] = fmt.Sprintf("máx. %d personas", peak.Authors)
		aria := "Personas que escribieron en el chat cada 5 minutos " + span + "; "
		if peak.Authors > 0 {
			aria += fmt.Sprintf("máximo de %d personas a las %s", peak.Authors, peak.Start.Local().Format("15:04"))
		} else {
			aria += "todavía sin mensajes"
		}
		out["Aria"] = aria
		return out
	}
	// La escala del panel es el máximo de la ventana recibida: cambia con 6 h / 24 h.
	if line, has := viewersLine(slots, span); has {
		out["Viewers"] = line
	}
	out["TypicalY"] = 100.0
	if scale > 0 {
		out["TypicalY"] = 100 - typical/scale*100
	}
	out["Max"] = "máx. " + pct(scale)
	aria := "Participación del chat cada 5 minutos " + span + " (personas que escribieron sobre los que miraban); "
	if best.Viewers > 0 {
		aria += fmt.Sprintf("la más alta: %s a las %s (%s personas de %s mirando)", pct(best.Pct), best.Start.Local().Format("15:04"), miles(best.Authors), miles(best.Viewers))
	} else {
		aria += fmt.Sprintf("todavía sin franjas con dato de espectadores y al menos %d personas", minPersonasTop)
	}
	out["Aria"] = aria
	return out
}

// slotTitle es el <title> de una franja: qué pasó y por qué entra o no en el ranking.
func slotTitle(s Slot, titles map[string]string) string {
	label := s.Start.Local().Format("15:04") + " a " + s.Start.Add(slotStep).Local().Format("15:04")
	if p := mainProgram(s.Msgs, titles); p != "" {
		label += " · " + p
	}
	label += fmt.Sprintf(" · %s mensajes · %s personas", miles(s.N), miles(s.Authors))
	if s.Viewers == 0 {
		return label + " · sin dato de espectadores"
	}
	// Cuántos miraban va siempre que haya dato: el panel de oyentes remite a
	// este tooltip también en las franjas que no entran en el ranking.
	label += fmt.Sprintf(" · %s mirando (%s)", miles(s.Viewers), pct(s.Pct))
	if s.Authors < minPersonasTop {
		label += fmt.Sprintf(" · menos de %d personas: no entra en el ranking", minPersonasTop)
	}
	return label
}

// dvrWindow es cuánto deja retroceder YouTube en un vivo con DVR, según su
// ayuda (sin verificar: se confirma a mano antes de mergear). Los vivos más
// largos tampoco quedan enteros como VOD.
const dvrWindow = 12 * time.Hour

// reelLink dice cómo ubicar en YouTube el momento que empieza en slotStart:
// un link con t= si el vivo ya terminó y fue corto, o cuánto retroceder en el
// reproductor.
func reelLink(v Video, slotStart, now time.Time) (href, hint string) {
	age := now.Sub(slotStart)
	if age > dvrWindow {
		return "", "Fuera de lo que se puede retroceder en YouTube: usá la grabación propia"
	}
	back := fmt.Sprintf("~%d h %d min", int(age/time.Hour), int(age%time.Hour/time.Minute))
	// Mientras el vivo está en curso YouTube ignora t= y abre en el directo
	// (probado a mano): se linkea el vivo y se dice cuánto ir para atrás.
	if v.LiveStatus == "live" {
		return "https://www.youtube.com/watch?v=" + url.QueryEscape(v.ID), "Abrí el vivo y retrocedé " + back + " en el reproductor"
	}
	if !v.StartedAt.IsZero() && now.Sub(v.StartedAt) <= dvrWindow && !slotStart.Before(v.StartedAt) {
		sec := int(slotStart.Sub(v.StartedAt) / time.Second)
		return fmt.Sprintf("https://www.youtube.com/watch?v=%s&t=%ds", url.QueryEscape(v.ID), sec), "Ver en YouTube desde ese momento"
	}
	// En un vivo de días (como el de 24 h de Rivadavia) t= caería fuera de lo
	// que se puede retroceder: se dice cuánto ir para atrás desde el directo.
	return "", "Retrocedé " + back + " en el reproductor del vivo"
}

// momentLink linkea el momento en el vivo del que es. Si ese vivo ya no está
// en la lista de videos (un vivo terminado de un canal extra) no se sabe
// cuándo empezó: se linkea sin t= y se dice la hora.
func momentLink(videos []Video, id string, slotStart, now time.Time) (href, hint string) {
	for _, v := range videos {
		if v.ID == id {
			return reelLink(v, slotStart, now)
		}
	}
	return "https://www.youtube.com/watch?v=" + url.QueryEscape(id),
		"Ese vivo ya no está en la lista de videos: abrilo y buscá el momento de las " + slotStart.Local().Format("15:04") + " (hora local)"
}

// decimal escribe un número con un decimal y coma, como se lee en Argentina.
func decimal(f float64) string {
	return strings.Replace(fmt.Sprintf("%.1f", f), ".", ",", 1)
}

func pct(f float64) string { return decimal(f) + " %" }

func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	if key := r.URL.Query().Get("tramo"); key != "" {
		s.exportProgram(w, key, r.URL.Query().Get("dia"))
		return
	}
	snap := s.st.Snapshot()
	titles := videoTitles(snap.Videos)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="mensajes_%s.csv"`, time.Now().Format("2006-01-02_1504")))
	cw := csv.NewWriter(w)
	cw.Write(csvHeader)
	for _, m := range snap.Msgs {
		cw.Write(csvRow(m, titles))
	}
	cw.Flush()
}

func since(msgs []Message, t time.Time) []Message {
	var out []Message
	for _, m := range msgs {
		if !m.At.Before(t) {
			out = append(out, m)
		}
	}
	return out
}

func videoTitles(v []Video) map[string]string {
	out := map[string]string{}
	for _, x := range v {
		out[x.ID] = x.Title
	}
	return out
}

func miles(n any) string {
	s := fmt.Sprint(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// ClaudeCard es la tarjeta 7, "Análisis con Claude". Todo va como string:
// html/template escapa los textos del modelo y de los usuarios.
type ClaudeCard struct {
	Title          string
	DayLink        string // con "Todo el día del canal": link a Programas, lo único que se muestra
	Off            string // apagado o no disponible: lo único que se muestra
	Active         bool   // el análisis está activo (cambia la nota de la tarjeta 2)
	Note, Notice   string // el estado y la respuesta a un pedido del botón que no entró
	Blocks         []ClaudeBlock
	Button, Vals   string // el botón y lo que manda (vacío = sin botón)
	Remote         bool   // sin botón porque el pedido no vino de la PC del monitor
	Spent, LastErr string
}

// ClaudeBlock es un análisis guardado, listo para mostrar.
type ClaudeBlock struct {
	Title, Err, Summary, Tone, Footer string
	Partial, Compact                  bool // Compact: en "Todo el día", solo resumen y sentimiento
	Sent                              *SentimentBar
	Topics, Questions, Complaints     []ClaudeItem
	QFold, CFold                      Fold // preguntas y quejas, plegadas en En vivo
}

type SentimentBar struct {
	Pos, Neu, Neg, NegX int
	Label               string
}

type ClaudeItem struct {
	Text, Count string
	Examples    []ClaudeExample
}

type ClaudeExample struct{ Author, Hour, Text string }

// claudeInput es lo que necesita claudeView, ya leído del Store.
type claudeInput struct {
	Configured    bool
	Status        ClaudeStatus
	Canal, Ver    string // Canal vacío: un vivo suelto
	Cur           Segment
	HasCur        bool
	CurMsgs       []Message // del programa al aire, para estimar el costo del botón
	ChannelName   string
	Analyses      []ClaudeAnalysis
	Msgs          map[string]Message // los de los ejemplos, por ID
	Queued, Local bool
	Notice        string
	Now           time.Time
	Fold          foldState // lo plegado abierto y la consulta del análisis
}

const claudeOffText = "El análisis con Claude está apagado. Para activarlo, mirá la sección del README."

// claudeView arma la tarjeta 7: con "Programa en curso", el último análisis
// del tramo al aire y el botón; con "Todo el día", un bloque por programa.
func claudeView(in claudeInput) ClaudeCard {
	st := in.Status
	title := "Resumen de Claude del programa en curso"
	switch {
	case in.Canal == "":
		title = "Resumen de Claude"
	case in.Ver == "dia":
		title = "Resúmenes de Claude"
	}
	switch {
	case !in.Configured:
		return ClaudeCard{Title: title, Off: claudeOffText}
	case st.Off != "":
		return ClaudeCard{Title: title, Off: "El análisis con Claude se apagó hasta reiniciar el monitor. " + st.Off}
	case in.Canal == "":
		return ClaudeCard{Title: title, Off: "El análisis con Claude es por canal: elegí el botón del canal para verlo."}
	case in.Ver == "dia":
		// Los resúmenes de cada programa del día se ven en Programas de hoy.
		return ClaudeCard{Title: title, Active: true, DayLink: "/programas?" + url.Values{"canal": {in.Canal}}.Encode()}
	}
	c := ClaudeCard{Title: title, Active: true, Notice: in.Notice, LastErr: st.LastErr,
		Spent: "Gastado hoy en Claude: US$ " + usd(st.Spent) + " de " + usd(st.Tope)}
	if st.Capped {
		c.Note = "Se llegó al tope de gasto de Claude de hoy (US$ " + usd(st.Tope) + "): se retoma mañana"
	}
	// Por tramo, el final reemplaza al parcial; entre iguales, el último.
	best := map[string]ClaudeAnalysis{}
	for _, a := range in.Analyses {
		if b, ok := best[a.Key]; !ok || b.Partial || !a.Partial {
			best[a.Key] = a
		}
	}
	key := segmentKey(in.Canal, in.Cur)
	busy := in.HasCur && st.Busy == key
	switch {
	case busy:
		c.Note = "Analizando… (puede tardar un minuto)"
	case in.Queued:
		c.Note = "En cola: el análisis se hace en unos instantes."
	}
	if a, ok := best[key]; ok && in.HasCur {
		b := claudeBlock(a, in.Msgs, false)
		b.QFold, b.CFold = in.Fold.link("preguntas"), in.Fold.link("quejas")
		c.Blocks = append(c.Blocks, b)
	} else if c.Note == "" {
		c.Note = "Todavía no hay análisis de este programa: se hace solo cuando termina"
	}
	if !in.HasCur || busy || in.Queued || st.Capped {
		return c
	}
	if !in.Local {
		c.Remote = true
		return c
	}
	c.Button = "Analizar ahora"
	if st.Model != "" {
		user, _ := claudePrompt(in.ChannelName, in.Cur.Program, in.Cur.Start, in.Now, in.CurMsgs)
		c.Button += " (≈ US$ " + usd(claudeEstimate(st.Model, claudeRequestBytes(user))) + ")"
	}
	vals, _ := json.Marshal(map[string]string{"canal": in.Canal, "video": in.Cur.VideoID})
	c.Vals = string(vals)
	return c
}

// claudeBlock arma un análisis guardado. Los ejemplos que ya vencieron (o
// que no están) no se muestran.
func claudeBlock(a ClaudeAnalysis, msgs map[string]Message, compact bool) ClaudeBlock {
	b := ClaudeBlock{Title: a.Program, Partial: a.Partial, Compact: compact, Err: a.Error}
	if compact {
		end := a.To.Local().Format("15:04")
		if a.Partial {
			end += ", parcial"
		}
		b.Title = a.From.Local().Format("15:04") + " a " + end + " · " + a.Program
	}
	if b.Err != "" {
		return b
	}
	r := a.Result
	b.Summary, b.Tone = r.Summary, r.Sentiment.Tone
	if s := r.Sentiment; s.Positive+s.Neutral+s.Negative > 0 {
		b.Sent = &SentimentBar{Pos: s.Positive, Neu: s.Neutral, Neg: s.Negative, NegX: s.Positive + s.Neutral,
			Label: fmt.Sprintf("Sentimiento: %d %% positivo, %d %% neutro, %d %% negativo", s.Positive, s.Neutral, s.Negative)}
	}
	items := func(gs []ClaudeGroup) []ClaudeItem {
		var out []ClaudeItem
		for _, g := range gs {
			it := ClaudeItem{Text: g.Text, Count: plural(g.Msgs, "mensaje", "mensajes") + " · " + plural(g.People, "persona", "personas")}
			for _, id := range g.Examples {
				if m, ok := msgs[id]; ok {
					it.Examples = append(it.Examples, ClaudeExample{m.Author, m.At.Local().Format("15:04"), m.Text})
				}
			}
			out = append(out, it)
		}
		return out
	}
	b.Topics, b.Questions, b.Complaints = items(r.Topics), items(r.Questions), items(r.Complaints)
	b.Footer = fmt.Sprintf("Analizado a las %s con %s · %s de %s mensajes", a.At.Local().Format("15:04"), a.Model, miles(a.Sent), miles(a.Total))
	return b
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return miles(n) + " " + many
}

// Pantalla Canales, en solo lectura: los canales se cambian con -canal y los
// intervalos con -chat-seg-pico y -chat-seg. Nada acá escribe ni llama a la API.

// ChannelRow es la tarjeta de un canal en Canales.
type ChannelRow struct {
	Name, Input, Role string
	Live              bool
	Status, How       string
	Hours, Chat       string
}

// QuotaPart es un pedazo de la barra de cuota (Class elige el color).
type QuotaPart struct {
	Class, Label, N string
	W               float64 // % de quotaScale
}

// chatText describe los intervalos del chat, que valen para todos los vivos.
func chatText(cs ChatSettings) string {
	if cs.PeakFrom == cs.PeakTo {
		return "cada " + humanDur(cs.Off) + " todo el día (sin horario pico), por turnos entre todos los vivos"
	}
	return fmt.Sprintf("cada %s en el pico (%02d a %02d h) y cada %s fuera, por turnos entre todos los vivos",
		humanDur(cs.Peak), cs.PeakFrom, cs.PeakTo, humanDur(cs.Off))
}

var weekdayShort = [...]string{"dom", "lun", "mar", "mié", "jue", "vie", "sáb"}

// hoursText son las horas en vivo de cada uno de los últimos 7 días.
func hoursText(lh HoursOnAir) string {
	if lh.NoHistory {
		return "sin historial (para la cuota se suponen 24 h en vivo)"
	}
	var parts []string
	for _, d := range lh.Days {
		parts = append(parts, fmt.Sprintf("%s %s: %s h", weekdayShort[d.Day.Weekday()], d.Day.Format("02/01"), decimal(d.Hours)))
	}
	return strings.Join(parts, " · ")
}

func (s *Server) channelsPage(w http.ResponseWriter, r *http.Request) {
	now := s.clock()
	snap := s.st.Status()
	data := s.channelsData(snap, now)
	data["Tab"], data["Channel"] = "canales", snap.Channel
	data["Claude"] = s.claudeSetup(snap, now)
	data["FastEvery"], data["FastFor"], data["FastMax"] = humanDur(fastEvery), humanDur(fastFor), fastUnitsPerCut()
	every := 3 * time.Minute
	for _, c := range snap.Channels {
		if c.Every > 0 {
			every = c.Every
		}
	}
	data["Every"] = humanDur(every)
	s.render(w, "canales.html", data)
}

// channelsState es el estado de los canales y la cuota (cada 5 s).
func (s *Server) channelsState(w http.ResponseWriter, r *http.Request) {
	s.render(w, "canales-estado", s.channelsData(s.st.Status(), s.clock()))
}

// channelsData arma las tarjetas de los canales y la cuota estimada.
func (s *Server) channelsData(snap Snapshot, now time.Time) map[string]any {
	cs := s.chatSettings()
	week := s.st.Lives(now.Add(-7 * 24 * time.Hour))
	paused := s.quotaPaused(snap)
	ids := map[string]bool{}
	var rows []ChannelRow
	for i, ch := range snap.Channels {
		ids[ch.ID] = true
		row := ChannelRow{Name: channelName(ch), Input: ch.Input, Role: "canal extra", Chat: chatText(cs)}
		if row.Input == "" {
			row.Input = ch.ID
		}
		if i == 0 {
			row.Role = "canal principal"
		}
		live := liveOf(snap.Videos, ch.ID)
		row.Live = len(live) > 0
		segs := ProgramSegments(week, ch.ID, live, claudeFrom(now), now)
		if cur, ok := currentSegment(snap.Videos, segs, ch.ID, ""); row.Live {
			row.Status = "Al aire"
			if ok {
				row.Status += ": " + programLabel(cur.Program) + " desde " + cur.Start.Local().Format("15:04")
				if n := nthLive(segs, cur.VideoID, now); n != "" {
					row.Status += " · " + n
				}
			}
		} else {
			row.Status = "Sin vivo"
			if !ch.WaitSince.IsZero() {
				row.Status += " desde " + ch.WaitSince.Local().Format("15:04")
			}
			row.Status += " · " + searchText(ch, now, paused)
		}
		row.How = howItAirs(ProgramSegments(week, ch.ID, live, now.Add(-7*24*time.Hour), now))
		row.Hours = hoursText(LiveHours(week, map[string]bool{ch.ID: true}, cs.PeakFrom, cs.PeakTo, now))
		rows = append(rows, row)
	}
	budget := s.budget
	if budget <= 0 {
		budget = 9000
	}
	// En esta etapa todos los vivos se leen por turnos: un solo grupo con las
	// horas en vivo de la unión de los canales.
	lh := LiveHours(week, ids, cs.PeakFrom, cs.PeakTo, now)
	q := EstimateQuota(len(snap.Channels), lh.Cuts, []ChatGroup{{PeakEvery: cs.Peak, OffEvery: cs.Off, PeakHours: lh.Peak, OffHours: lh.Off}}, budget)
	parts := []QuotaPart{
		{"q-vid", "Revisar videos y vivos", miles(q.Videos + q.Fast), 0},
		{"q-com", "Comentarios", "~" + miles(q.Comments), 0},
		{"q-chat", "Chat de todos los vivos (por turnos)", miles(q.Chat[0]), 0},
	}
	left := 100.0
	for i, n := range []int{q.Videos + q.Fast, q.Comments, q.Chat[0]} {
		parts[i].W = min(float64(n)*100/quotaScale, left)
		left -= parts[i].W
	}
	hours := "sin historial: se suponen 24 h en vivo"
	if !lh.NoHistory {
		hours = fmt.Sprintf("%s h en vivo por día en el pico y %s h fuera, según los últimos 7 días", decimal(lh.Peak), decimal(lh.Off))
	}
	return map[string]any{
		"Rows": rows, "Quota": q, "Parts": parts, "Hours": hours, "Used": miles(snap.Units),
		"Total": miles(q.Total), "Limit": miles(q.Limit), "Budget": miles(budget),
		"LimitX": float64(q.Limit) * 100 / quotaScale, "BudgetX": float64(budget) * 100 / quotaScale,
		"Aria": fmt.Sprintf("Cuota estimada por día: %s unidades de %s (videos y vivos %s, comentarios %s, chat %s); el chat se pausa en %s y todo se frena en %s",
			miles(q.Total), miles(quotaScale), miles(q.Videos+q.Fast), miles(q.Comments), miles(q.Chat[0]), miles(q.Limit), miles(budget)),
		"ChatNow": chatNow(cs, now), "Reserve": miles(reserveForComments),
	}
}

// chatNow es el intervalo que vale en este momento.
func chatNow(cs ChatSettings, now time.Time) string {
	if peakHour(now.Local().Hour(), cs.PeakFrom, cs.PeakTo) {
		return "Ahora: cada " + humanDur(cs.Peak) + " (horario pico)."
	}
	return "Ahora: cada " + humanDur(cs.Off) + " (fuera del horario pico)."
}

// ClaudeSetup es la tarjeta "Análisis con Claude" de Canales: nunca la clave
// ni parte de ella, solo si está cargada.
type ClaudeSetup struct {
	Chip, ChipClass    string
	Model, Tope, Spent string
	Last, Warn         string
}

func (s *Server) claudeSetup(snap Snapshot, now time.Time) ClaudeSetup {
	st := snap.Claude
	c := ClaudeSetup{Model: s.claudeModel, Spent: "US$ " + usd(st.Spent), Tope: "sin tope (apagado)", Warn: s.claudeWarn}
	if c.Model == "" {
		c.Model = defaultClaudeModel
	}
	switch {
	case s.analyst != nil && st.Off != "":
		// Claude rechazó la clave o el modelo: el análisis no corre hasta reiniciar.
		c.Chip, c.ChipClass = "Apagado hasta reiniciar", "warn"
		if st.Model != "" {
			c.Model = st.Model
		}
		c.Tope = "US$ " + usd(st.Tope) + " por día"
	case s.analyst != nil:
		c.Chip, c.ChipClass = "Clave cargada", "ok"
		if st.Model != "" {
			c.Model = st.Model
		}
		c.Tope = "US$ " + usd(st.Tope) + " por día"
	case strings.Contains(s.claudeWarn, "CLAUDE_API_KEY"):
		c.Chip, c.ChipClass = "Falta CLAUDE_API_KEY", "warn"
	case s.claudeUSD == 0 && s.claudeWarn == "":
		c.Chip, c.ChipClass = "Apagado (-claude-usd 0)", "off"
	default:
		c.Chip, c.ChipClass = "Apagado", "off"
	}
	if s.analyst == nil && s.claudeUSD > 0 {
		c.Tope = "US$ " + usd(int64(math.Round(s.claudeUSD*1e6))) + " por día (apagado)"
	}
	switch {
	case st.Off != "":
		c.Warn = st.Off
	case st.LastErr != "":
		c.Warn = st.LastErr
	}
	var last ClaudeAnalysis
	var lastCh string
	for _, ch := range snap.Channels {
		for _, a := range s.st.Analyses(ch.ID, claudeFrom(now)) {
			if !a.To.Before(dayStart(now)) && a.At.After(last.At) {
				last, lastCh = a, channelName(ch)
			}
		}
	}
	c.Last = "Todavía no hubo análisis hoy."
	if !last.At.IsZero() {
		c.Last = fmt.Sprintf("Último análisis: %s, %s de %s.", last.At.Local().Format("15:04"), programLabel(last.Program), lastCh)
	}
	return c
}
