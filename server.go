package main

import (
	"cmp"
	"embed"
	"encoding/csv"
	"encoding/json"
	"errors"
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
	"hora":     func(t time.Time) string { return t.Local().Format("15:04") },
	"fecha":    func(t time.Time) string { return t.Local().Format("02/01 15:04") },
	"fechaDia": func(t time.Time) string { return t.Local().Format("02/01") },
	"miles":    miles,
	"decimal":  decimal,
	"plural":   plural,
	"durText":  durText,
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
	settings                *Settings     // canales e intervalos editables (ADR 0005); nil: Canales en solo lectura
	answers                 *Answers      // preguntas marcadas como respondidas (F4); nil: sin el botón
	regs                    regularsCache // habituales por canal, una vez por día (Quiénes participan)
	recMarks                *RecMarks     // marcas Hecha / No sirve de las recomendaciones; nil: sin botones
	parteC                  parteCache    // el parte por rol, armado una vez por día
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
	mux.HandleFunc("GET /comentarios", s.commentsPage)
	mux.HandleFunc("GET /canales", s.channelsPage)
	mux.HandleFunc("GET /sin-vivo", s.noLivePage)
	mux.HandleFunc("GET /parcial/sin-vivo", s.noLivePartial)
	mux.HandleFunc("GET /parcial/resumen", s.summary)
	mux.HandleFunc("GET /parcial/vivo", s.liveSection)
	mux.HandleFunc("GET /parcial/vivo/canal", s.liveChannel)
	mux.HandleFunc("GET /parcial/vivo/chat", s.liveChat)
	mux.HandleFunc("GET /parcial/vivo/analisis", s.liveAnalysis)
	mux.HandleFunc("GET /parcial/programas", s.programsPartial)
	mux.HandleFunc("GET /parcial/programas/detalle", s.programDetailPartial)
	mux.HandleFunc("GET /parcial/comentarios", s.commentsPartial)
	mux.HandleFunc("GET /parcial/canales/estado", s.channelsState)
	mux.HandleFunc("GET /parcial/canales/form", s.channelsForm)
	mux.HandleFunc("GET /parcial/canales/cuota", s.channelsQuota)
	mux.HandleFunc("POST /canales", s.channelsSave)
	mux.HandleFunc("POST /canales/agregar", s.channelsAdd)
	mux.HandleFunc("POST /canales/pausar", s.channelsPause)
	mux.HandleFunc("POST /canales/quitar", s.channelsRemove)
	mux.HandleFunc("POST /canales/claude", s.channelsClaude)
	mux.HandleFunc("GET /parcial/canales/claude", s.channelsClaudeState)
	mux.HandleFunc("GET /parcial/canales/claude/proyeccion", s.channelsClaudeProj)
	mux.HandleFunc("POST /claude/analizar", s.claudeAnalyze)
	mux.HandleFunc("POST /preguntas/respondida", s.answerPost)
	mux.HandleFunc("POST /recomendaciones/marca", s.recMarkPost)
	mux.HandleFunc("GET /parte", s.partePage)
	mux.HandleFunc("GET /parte.txt", s.parteText)
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
		"Vivo": map[string]any{"Canal": sel.Canal, "Video": sel.Video, "Pestana": liveTabParam(q)}})
}

// liveTabs son las pestañas de En vivo. Lista cerrada: la primera (Ahora) no
// viaja en la URL y nada de afuera se refleja.
var liveTabs = []struct{ Key, Label string }{
	{"", "Ahora"}, {"momentos", "Momentos y audiencia"}, {"temas", "De qué se habla"}, {"supers", "Super Chats"},
}

// liveTabParam es la pestaña pedida; "" es Ahora (también lo que no está en la lista).
func liveTabParam(q url.Values) string {
	for _, t := range liveTabs[1:] {
		if t.Key == q.Get("pestana") {
			return t.Key
		}
	}
	return ""
}

// liveTabBar arma las pestañas del análisis: cada una vuelve a pedir la
// sección del canal (o del vivo suelto) con lo demás igual, y deja la
// pantalla en la URL.
func liveTabBar(canal, video, tab string, hours int, open, motives string) TabBar {
	b := TabBar{Aria: "Qué ver del vivo", Target: "#vivo"}
	for _, t := range liveTabs {
		part := url.Values{"video": {video}, "horas": {strconv.Itoa(hours)}}
		page := url.Values{}
		if canal != "" {
			part.Set("canal", canal)
			page.Set("canal", canal)
		} else {
			page.Set("vivo", video)
		}
		for k, v := range map[string]string{"abrir": open, "motivos": motives, "pestana": t.Key} {
			if v != "" {
				part.Set(k, v)
			}
		}
		if t.Key != "" {
			page.Set("pestana", t.Key)
		}
		b.Tabs = append(b.Tabs, TabLink{ID: "tab-" + cmp.Or(t.Key, "ahora"), Label: t.Label, Page: "/?" + page.Encode(), Partial: "/parcial/vivo?" + part.Encode(), On: t.Key == tab})
	}
	return b
}

type Tile struct {
	Label, Value, Note string
	Trend              string // "up" (verde), "down" (naranja) o "" (gris)
}

type Bar struct {
	X, Y, W, H float64
	Label      string
	N          int
	Class      string // clase CSS extra, por ejemplo "top"
	Href, ID   string // en Programas: pide los mensajes de la franja (ID: para el foco)
	Sel        bool   // la franja elegida
}

type CatRow struct {
	Key, Label string
	N          int
	Pct        float64 // largo de la barra, relativo a la categoría más grande
	Share      float64 // % del total
	Example    string  // un mensaje corto de ejemplo, o una descripción
	FoldKey    string  // cat-<Key>: la fila se despliega con sus mensajes
	Fold       Fold
	List       *FoldMsgs // solo si está desplegada
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

// catRows cuenta las categorías por reglas de unos mensajes, de la más
// grande a la más chica y Otros al final, con su % del total y un ejemplo:
// el mensaje más reciente de la categoría, recortado y sin autor. Spam y
// Otros llevan una descripción en vez de un ejemplo (un link de spam no se
// muestra como ejemplo).
func catRows(msgs []Message) []CatRow {
	rows, _ := catRowsWith(msgs, nil)
	return rows
}

// catRowsWith es catRows con la categoría que dio Claude en las tandas para
// los mensajes etiquetados. El spam sale siempre de las reglas (no se le
// manda a Claude) y los que todavía no entraron en una tanda, también:
// pending dice cuántos son.
func catRowsWith(msgs []Message, labels map[string]MessageLabel) (rows []CatRow, pending int) {
	counts, last := map[string]int{}, map[string]string{}
	for _, m := range msgs {
		c, unlabeled := catOf(m, labels)
		if unlabeled {
			pending++
		}
		counts[c]++
		last[c] = m.Text
	}
	peak := 1
	for _, c := range categories {
		peak = max(peak, counts[c])
	}
	for _, c := range categories {
		ex := catExamples[c]
		if ex == "" && last[c] != "" {
			ex = `"` + shortText(last[c], catExampleRunes) + `"`
		}
		rows = append(rows, CatRow{Key: c, Label: catLabels[c], N: counts[c], Pct: float64(counts[c]) * 100 / float64(peak),
			Share: share(counts[c], len(msgs)), Example: ex})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if (rows[i].Key == CatOtro) != (rows[j].Key == CatOtro) {
			return rows[j].Key == CatOtro
		}
		return rows[i].N > rows[j].N
	})
	return rows, pending
}

// catOf es la categoría de un mensaje en la tarjeta: la de Claude si está
// etiquetado y no es spam; si no, la de las reglas (unlabeled: con etiquetas
// en el alcance, este todavía no entró en una tanda).
func catOf(m Message, labels map[string]MessageLabel) (cat string, unlabeled bool) {
	c := Classify(m.Text)
	if l, ok := labels[m.ID]; ok && c != CatSpam {
		return l.Cat, false
	}
	return c, len(labels) > 0 && c != CatSpam
}

const catExampleRunes = 60

const maxFoldMsgs = 20

var catExamples = map[string]string{CatSpam: "links, teléfonos, promos", CatOtro: "saludos y comentarios"}

// shortText recorta un texto a n runas como mucho, con "…" si se cortó.
func shortText(text string, n int) string {
	r := []rune(strings.TrimSpace(text))
	if len(r) <= n {
		return string(r)
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
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
	hours, open, motives, tab := keepView(q)
	s.render(w, "vivo", map[string]any{"Canal": sel.Canal, "Video": sel.Video, "Horas": hours, "Abrir": open, "Motivos": motives, "Pestana": tab})
}

// quotaPaused dice si se llegó al tope de cuota: el worker no revisa videos ni busca vivos.
func (s *Server) quotaPaused(snap Snapshot) bool { return s.budget > 0 && snap.Units >= s.budget }

// keepView son las "horas", lo plegado que está abierto, la ventana de los
// motivos y la pestaña del análisis, que manda el chat (con hx-include), para
// no perderlos cuando se recarga la sección. Un "ver" de antes
// (en-vivo-unificado) se ignora: el alcance sale de si hay vivo.
func keepView(q url.Values) (hours, open, motives, tab string) {
	if h := q.Get("horas"); h == "24" || h == strconv.Itoa(chartHours) {
		hours = h
	}
	return hours, foldValue(foldParam(q)), motiveParam(q), liveTabParam(q)
}

// motiveWindows son las ventanas de "Por qué hay mensajes negativos", "Qué
// está gustando" y "Preguntas agrupadas" (rediseno-envivo-programas): la
// clave del parámetro "motivos", su texto y cuánto mira (0: todo el
// programa o todo el día). Lista cerrada, como "abrir".
var motiveWindows = []struct {
	Key, Label, When string
	Span             time.Duration
}{
	{"5", "Últimos 5 min", "en los últimos 5 min", slotStep},
	{"30", "30 min", "en los últimos 30 min", 30 * time.Minute},
	{"60", "1 h", "en la última hora", time.Hour},
	{"programa", "Todo el programa", "en todo el programa", 0},
}

// motiveSince es desde cuándo mira la ventana elegida ("" son los últimos 5
// min) y su texto. La comparten los motivos y "Preguntas agrupadas". whole
// dice que es todo el alcance (todo el programa o todo el día): las
// preguntas no se filtran, porque los mensajes del programa arrancan
// segmentLead antes de ls.start y el número de arriba los cuenta.
func motiveSince(ls liveScope, motives string, now time.Time) (since time.Time, when string, whole bool) {
	for _, mw := range motiveWindows {
		if mw.Key != motives && !(mw.Key == "5" && motives == "") {
			continue
		}
		if mw.Span > 0 {
			return now.Add(-mw.Span), mw.When, false
		}
		if ls.prog == "" {
			return dayStart(now), "en todo el día", true
		}
		return ls.start, mw.When, true
	}
	return now.Add(-slotStep), motiveWindows[0].When, false
}

// motiveParam es la ventana de los motivos pedida; "" son los últimos 5 min
// (el valor por defecto no viaja en la URL). Lo que no está en la lista, también.
func motiveParam(q url.Values) string {
	v := q.Get("motivos")
	for _, w := range motiveWindows[1:] {
		if w.Key == v {
			return v
		}
	}
	return ""
}

// foldSections son las secciones plegables de En vivo, en el orden en que se
// escriben en "abrir". Lista cerrada: nada de lo que llega se refleja tal cual.
// neg1…neg3 y pos1…pos3 son los mensajes de cada motivo (motivesShown), por
// posición: con una clave por texto, la lista dejaría de ser cerrada. mom1…mom5
// son los momentos para reels (maxMomentos), también por posición.
// tema1…tema5 son los temas del resumen (groups(raw.Temas, 5)), cat-<Cat*> las
// filas de Categorías (sin spam: sus mensajes no se muestran) y qg1…qg30 los
// grupos de "Preguntas agrupadas", también por posición.
var foldSections = slices.Concat([]string{"preguntas", "quejas", "alertas", "maspreguntas", "neg1", "neg2", "neg3", "pos1", "pos2", "pos3", "mom1", "mom2", "mom3", "mom4", "mom5"},
	numbered("tema", 5), catFoldKeys(), numbered("qg", qgFolds))

// numbered es prefix1…prefixN.
func numbered(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = prefix + strconv.Itoa(i+1)
	}
	return out
}

// catFoldKeys son las claves de Categorías en "abrir": la categoría elegida
// en el semicírculo (cat-<Cat*>, también el spam, que muestra sus números),
// sin «Otros» y con la lista entera desplegada.
func catFoldKeys() []string {
	var out []string
	for _, c := range categories {
		out = append(out, "cat-"+c)
	}
	return append(out, "sinotros", "catall")
}

// catPick lee de lo abierto la categoría elegida y los dos botones.
func catPick(open map[string]bool) (sel string, noOther, all bool) {
	for _, c := range categories {
		if open["cat-"+c] {
			sel = c
			break
		}
	}
	return sel, open["sinotros"], open["catall"]
}

// catOpen es lo abierto con otra categoría elegida (una sola a la vez).
func catOpen(open map[string]bool, sel string, noOther, all bool) map[string]bool {
	next := map[string]bool{}
	for k, v := range open {
		if v && !strings.HasPrefix(k, "cat-") && k != "sinotros" && k != "catall" {
			next[k] = true
		}
	}
	next["cat-"+sel], next["sinotros"], next["catall"] = true, noOther, all
	return next
}

// liveGauge es el semicírculo de Categorías de En vivo, con el estado en "abrir".
func liveGauge(rows []CatRow, msgs []Message, labels map[string]MessageLabel, fold foldState, now time.Time) CatGauge {
	sel, noOther, all := catPick(fold.Open)
	link := func(sel string, noOther, all bool) string {
		q := maps.Clone(fold.Query)
		if q == nil {
			q = url.Values{}
		}
		q.Set("abrir", foldValue(catOpen(fold.Open, sel, noOther, all)))
		return "/parcial/vivo/analisis?" + q.Encode()
	}
	return catGauge(catGaugeIn{rows: rows, msgs: msgs, labels: labels, sel: sel, noOther: noOther, all: all, link: link,
		now: now, end: now, step: 15 * time.Minute, n: 12, trend: true, stepLabel: "Cada 15 min",
		target: "#vivo-analisis", sync: "closest #vivo-analisis:replace"})
}

// qgFolds es cuántos grupos de preguntas se pueden desplegar: más abajo, un
// grupo se ve sin flecha (la lista cerrada de "abrir" no puede crecer sin fin).
const qgFolds = 30

// FoldMsgs son los mensajes de una sección desplegada, los más nuevos primero,
// con tope (maxFoldMsgs): una lista de un día entero tapaba la pantalla.
type FoldMsgs struct {
	Msgs []ClaudeExample
	More int
}

// foldMsgs arma la lista de unos mensajes; total es cuántos tiene el grupo
// (puede ser más que los que se encontraron: vencidos o de un análisis viejo).
func foldMsgs(msgs []Message, total int) *FoldMsgs {
	msgs = slices.Clone(msgs)
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].At.After(msgs[j].At) })
	l := &FoldMsgs{}
	for _, m := range msgs[:min(len(msgs), maxFoldMsgs)] {
		l.Msgs = append(l.Msgs, ClaudeExample{m.Author, m.At.Local().Format("15:04"), m.Text})
	}
	l.More = max(total, len(msgs)) - len(l.Msgs)
	return l
}

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

// analysisQuery es la consulta del análisis de un canal.
func analysisQuery(canal, video string, hours int, motives string) url.Values {
	q := url.Values{"canal": {canal}, "video": {video}, "horas": {strconv.Itoa(hours)}}
	if motives != "" {
		q.Set("motivos", motives)
	}
	return q
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
		hours, open, motives, tab := keepView(q)
		s.render(w, "vivo", map[string]any{"Canal": sel.Canal, "Video": sel.Video, "Horas": hours, "Abrir": open, "Motivos": motives, "Pestana": tab})
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
	case ci.Held:
		return "en pausa desde Canales"
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
	Tab                             string // la pestaña de En vivo, para los botones de canal
}

// liveChannel es la tarjeta Canal (cada 5 s). No recarga la sección: el que
// detecta el cambio de vivo es el chat (cada 2 s), como antes.
func (s *Server) liveChannel(w http.ResponseWriter, r *http.Request) {
	snap := s.st.Status()
	now := s.clock()
	q := r.URL.Query()
	chanOf, _ := s.dayLives(snap, now)
	sel, btns := pickChannel(snap, q.Get("canal"), q.Get("video"), chanOf)
	card := s.channelCard(snap, sel, btns, now)
	card.Tab = liveTabParam(q)
	s.render(w, "vivo-canal", card)
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
	// Desde las 12:00 de ayer, como Programas (Hoy): de acá salen las claves
	// de los links de la tira.
	segs := ProgramSegments(week, sel.Canal, live, claudeFrom(now), now)
	day := dayStrip(ProgramSegments(week, sel.Canal, live, from, now), segs, sel.Ch, from, now, paused)
	c.Strip, c.Banner, c.StripAria = day["Blocks"].([]StripBlock), day["Banner"].(string), day["Aria"].(string)
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
	case ci.Held:
		return &Banner{Class: "warn", Title: "Chat en pausa desde Canales: no se lee ni gasta cuota hasta que lo reanudes. Los mensajes de este rato no se guardan."}
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
		out = append(out, CatRow{Key: c.Key, Label: c.Key, N: c.N, Pct: float64(c.N) * 100 / float64(peak)})
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
	tab := liveTabParam(q)
	for _, c := range snap.Channels {
		if c.ID == q.Get("canal") && c.ID != "" {
			s.channelAnalysis(w, snap, c, q.Get("video"), hours, open, motiveParam(q), tab, localRequest(r), slotPick{q.Get("franja"), q.Get("sent"), q.Get("pfranja")})
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
		s.render(w, "vivo-analisis", map[string]any{"Video": id, "Hours": hours, "Abrir": foldValue(open), "Pestana": tab})
		return
	}
	now := s.clock()
	msgs := sinceFor(snap.Msgs, id, now.Add(-24*time.Hour))
	viewers := s.st.Viewers(id, now.Add(-24*time.Hour))
	fold := foldState{Open: open, Query: url.Values{"video": {id}, "horas": {strconv.Itoa(hours)}}}
	if tab != "" {
		fold.Query.Set("pestana", tab)
	}
	x := liveExtra{hourAgo: viewersAgo(viewers, now), since: now.Add(-24 * time.Hour), pfranja: q.Get("pfranja")}
	x.likes = liveLikes(map[string][]LikeSample{id: s.st.Likes(id, now.AddDate(0, 0, -regularDays))}, &video, nil, dayStart(now), now)
	data := analysisData(msgs, viewers, []string{id}, partInput{msgs, viewers, []string{id}}, snap.Videos, video.Concurrent, nil, now, hours, scopeLast24, fold, x)
	data["Video"], data["Label"], data["Span"], data["Abrir"] = id, label, label+" · últimas 24 h", foldValue(open)
	data["Pestana"], data["Tabs"] = tab, liveTabBar("", id, tab, hours, foldValue(open), "")
	data["PFranja"] = data["Part"].(map[string]any)["PFranja"]
	// El análisis con Claude es por canal: un vivo suelto no lo tiene.
	data["Claude"] = claudeView(claudeInput{Configured: s.analyst != nil, Status: snap.Claude, Now: now})
	// Sin canal no hay tandas: alertas y preguntas por reglas.
	data["Alertas"] = alertsData(Alerts(msgs, nil, now), fold)
	data["QG"] = QGData{ByRules: true}
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

// liveTiles son los números de arriba del análisis, sobre los mensajes ya
// filtrados por el selector, en el orden del diseño ejecutivo. Cada uno trae
// su comparación (Trend): lo "típico" del chat es la mediana de las franjas
// de 5 min anteriores con mensajes, y solo se dice con al menos 6.
func liveTiles(in tileInput) []Tile {
	people, recent := map[string]bool{}, map[string]bool{}
	supers, questions := 0, 0
	byCur := map[string]int64{}
	for _, m := range in.msgs {
		people[m.Author] = true
		if m.Super != "" {
			supers++
			byCur[m.Currency] += m.AmountMicros
		}
		if Classify(m.Text) == CatPregunta {
			questions++
		}
		if m.Source == "chat" && !m.At.After(in.now) && in.now.Sub(m.At) < slotStep {
			recent[m.Author] = true
		}
	}
	cur, diff, ok := chatPace(in.msgs, in.now)
	rate := Tile{"Mensajes por minuto", decimal(float64(cur) / 5), "en los últimos 5 min", ""}
	switch {
	case !ok:
	case diff > 0:
		rate.Note, rate.Trend = fmt.Sprintf("%.0f %% sobre lo típico %s", diff, in.sc.typical), "up"
	case diff < 0:
		rate.Note, rate.Trend = fmt.Sprintf("%.0f %% bajo lo típico %s", -diff, in.sc.typical), "down"
	default:
		rate.Note = "igual que lo típico " + in.sc.typical
	}
	watch := Tile{"Mirando ahora", "—", "sin dato de espectadores", ""}
	part := Tile{"Participación", "—", "sin dato de espectadores", ""}
	if in.concurrent > 0 {
		watch.Value, watch.Note = miles(in.concurrent), "según YouTube"
		if in.hourAgo > 0 {
			switch d := math.Round(float64(in.concurrent-in.hourAgo) / float64(in.hourAgo) * 100); {
			case d > 0:
				watch.Note, watch.Trend = fmt.Sprintf("%.0f %% más que hace 1 h", d), "up"
			case d < 0:
				watch.Note, watch.Trend = fmt.Sprintf("%.0f %% menos que hace 1 h", -d), "down"
			default:
				watch.Note = "igual que hace 1 h"
			}
		}
		p := float64(len(recent)) * 100 / float64(in.concurrent)
		part.Value, part.Note = pct(p), "escribió en los últimos 5 min"
		if in.typical > 0 {
			part.Note = "lo típico del vivo: " + pct(in.typical)
			switch {
			case p > in.typical*(1+trendBand):
				part.Trend = "up"
			case p < in.typical*(1-trendBand):
				part.Trend = "down"
			}
		}
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
		watch, part, in.likes, rate,
		{"Personas que escribieron", miles(len(people)), "en " + in.sc.short, ""},
		{"Preguntas", miles(questions), "por reglas", ""},
		{"Super Chats", miles(supers), snote, ""},
	}
}

// partInput es lo que grafica "Momentos con más participación": las últimas
// 24 h del canal (o del vivo suelto), con cualquier alcance. Con el del
// programa en curso, los programas anteriores no se veían.
type partInput struct {
	msgs    []Message
	viewers []ViewerSample
	ids     []string
}

// analysisData arma las tarjetas del análisis con los mensajes ya filtrados
// (de un vivo, del día del canal o del programa en curso); Momentos usa part.
func analysisData(msgs []Message, viewers []ViewerSample, ids []string, part0 partInput, videos []Video, concurrent int64, segs []Segment, now time.Time, hours int, sc scope, fold foldState, x liveExtra) map[string]any {
	a := Analyze(msgs)
	titles := videoTitles(videos)
	part, moments, typical := participationView(part0.msgs, part0.viewers, part0.ids, videos, concurrent, titles, segs, now, hours, fold, x.pfranja)
	part["Window"] = sc.window
	sup := superView(Supers(msgs, titles))
	sup["Of"], sup["In"] = sc.of, sc.in
	tiles := liveTiles(tileInput{msgs: msgs, concurrent: concurrent, hourAgo: x.hourAgo, typical: typical, likes: x.likes, sc: sc, now: now})
	// La frase de arriba: el mejor momento es el del alcance (Momentos mira 24 h).
	var best *Moment
	for i, m := range moments {
		if !m.Start.Before(x.since) {
			best = &moments[i]
			break
		}
	}
	_, diff, paceOK := chatPace(msgs, now)
	// Una sola pasada de palabras para la frase y "De qué se habla"; en la
	// frase solo cuentan las que se repiten (2 mensajes o más).
	top := TopWords(msgs, wordsShown)
	var words []Count
	for _, w := range top[:min(2, len(top))] {
		if w.N >= 2 {
			words = append(words, w)
		}
	}
	talk := talkView(msgs, top, a.Topics, x.topics, true, now)
	talk.Card, talk.Of, talk.ClaudeNote, talk.RulesNote = true, sc.of, x.topicsNote, x.rulesNote
	return map[string]any{
		"Hours": hours, "A": a, "Tech": bars(a.Tech), "Part": part, "Super": sup,
		"Tiles": tiles, "Gauge": liveGauge(catRows(msgs), msgs, nil, fold, now), "CatTotal": a.Total, "CatOf": sc.of,
		"Frase": liveSummary(diff, paceOK, sc.typical, words, best), "Monitor": monitorView(msgs, sc, x.regulars),
		"Talk": talk,
	}
}

// liveExtra es lo de En vivo que no sale de los mensajes del alcance: los
// likes, los espectadores de hace 1 h, desde cuándo cuenta el mejor momento
// y los habituales del canal (nil en un vivo suelto).
type liveExtra struct {
	likes    Tile
	hourAgo  int64
	since    time.Time
	regulars map[string]bool
	pfranja  string // la franja de Momentos pedida, tal como llega
	// Los temas de Claude del programa en curso, sacados de su tarjeta para
	// "De qué se habla" (talkClaude); sin análisis, la nota de cuándo llegan.
	topics                []ClaudeItem
	topicsNote, rulesNote string
}

// channelAnalysis es el análisis de un canal: "Programa en curso" (el vivo
// actual con el programa actual, desde el inicio del tramo menos 5 min) o
// "Todo el día del canal" (sus vivos desde las 00:00 locales). Nunca entran
// mensajes de otro canal.
func (s *Server) channelAnalysis(w http.ResponseWriter, snap Snapshot, ch ChannelStatus, want string, hours int, open map[string]bool, motives, tab string, local bool, pick slotPick) {
	now := s.clock()
	ls := s.channelScope(snap, ch, want, now)
	fold := foldState{Open: open, Query: analysisQuery(ch.ID, ls.video, hours, motives)}
	// Todos los links del análisis (pliegues, franjas, ventanas) se quedan en la pestaña.
	if tab != "" {
		fold.Query.Set("pestana", tab)
	}
	// La franja elegida en "Cómo fue cambiando" viaja como "abrir": solo si
	// es una franja del gráfico, y la pestaña de una lista cerrada.
	sel := pick.valid(sentChartFrom(ls, now), now)
	if sel.ok {
		fold.Query.Set("franja", strconv.FormatInt(sel.at.Unix(), 10))
		fold.Query.Set("sent", sel.tab)
	}
	x := liveExtra{since: ls.start, regulars: s.regulars(ch.ID, now), pfranja: pick.part}
	var likes map[string]any
	x.likes, likes = s.likesView(snap, ch, ls, now)
	// "Hace 1 h" de los mismos vivos que suma "Mirando ahora": el del programa
	// o, con todo el día, todos los vivos en curso del canal.
	var ago [][]ViewerSample
	for _, v := range snap.Videos {
		if v.ChannelID == ch.ID && v.LiveStatus == "live" && (ls.prog == "" || v.ID == ls.video) {
			ago = append(ago, s.st.Viewers(v.ID, now.Add(-time.Hour-hourAgoSlack)))
		}
	}
	x.hourAgo = hourAgoSum(ago, now)
	cardVer := "dia"
	if ls.prog != "" {
		cardVer = "programa"
	}
	card := s.claudeCard(snap, ch, ls.video, cardVer, local, "", fold)
	x.topics, x.topicsNote, x.rulesNote = talkClaude(&card)
	data := analysisData(ls.msgs, ls.viewers, ls.pids, ls.part, snap.Videos, ls.concurrent, ls.segs, now, hours, ls.sc, fold, x)
	data["Likes"] = likes
	data["Canal"], data["Video"], data["Label"], data["Span"] = ch.ID, ls.video, channelName(ch), ls.span
	if ls.prog != "" {
		// Con el programa al aire, el día del canal como contexto (CA2).
		data["DayMsgs"] = miles(ls.dayMsgs)
		data["DayOf"] = map[string]string{"Personas que escribieron": miles(ls.dayPeople) + " en el día"}
	}
	data["Abrir"], data["Motivos"] = foldValue(open), motives
	data["Pestana"], data["Tabs"] = tab, liveTabBar(ch.ID, ls.video, tab, hours, foldValue(open), motives)
	data["PFranja"] = data["Part"].(map[string]any)["PFranja"]
	if sel.ok {
		data["Franja"], data["SentTab"] = fold.Query.Get("franja"), sel.tab
	}
	data["Claude"] = card
	data["ClaudeLine"] = s.claudeLine(snap, ch, now)
	labels := s.st.Labels(messageIDs(ls.msgs))
	if s.claudeActive(snap) && len(labels) > 0 {
		// Categorías por Claude; el CSV sigue por reglas (ADR 0010).
		rows, pending := catRowsWith(ls.msgs, labels)
		data["Gauge"], data["CatPending"] = liveGauge(rows, ls.msgs, labels, fold, now), pending
		data["CatClaude"] = true
	}
	data["Alertas"] = alertsData(Alerts(ls.msgs, labels, now), fold)
	data["Sent"] = s.sentimentData(snap, ch, ls, labels, now, motives, fold, sel)
	since, when, whole := motiveSince(ls, motives, now)
	if whole {
		since = time.Time{}
	}
	qg := s.questionsData(snap, ls, labels, fold, since, when)
	data["QG"] = qg
	if !qg.ByRules {
		// El tile cuenta las mismas preguntas que "Preguntas agrupadas", pero
		// de todo el alcance: la tarjeta se puede mirar en otra ventana.
		for i, t := range data["Tiles"].([]Tile) {
			if t.Label == "Preguntas" {
				data["Tiles"].([]Tile)[i] = Tile{"Preguntas", miles(qg.All), "agrupadas por Claude", ""}
			}
		}
	}
	s.render(w, "vivo-analisis", data)
}

// liveScope es lo que mira el análisis de un canal: con un vivo al aire, el
// programa en curso (el vivo actual con el programa actual, desde el inicio
// del tramo menos 5 min) y, como contexto, cuántos mensajes y personas hubo en
// el día; sin vivo, lo de hoy del canal (sus vivos desde las 00:00 locales).
// No hay selector (en-vivo-unificado): el día entero por programa está en
// Programas. Nunca entran mensajes de otro canal.
type liveScope struct {
	video, prog        string // prog: el programa en curso; "" sin vivo
	start              time.Time
	segs               []Segment
	msgs               []Message
	viewers            []ViewerSample
	pids               []string
	concurrent         int64
	span               string
	sc                 scope
	part               partInput // Momentos: las últimas 24 h del canal
	dayMsgs, dayPeople int       // con programa: los del día del canal
}

func (s *Server) channelScope(snap Snapshot, ch ChannelStatus, want string, now time.Time) liveScope {
	from, from24 := dayStart(now), now.Add(-24*time.Hour)
	var samples []LiveSample
	var lives []Video
	ids, ids24, live := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, v := range snap.Videos {
		if v.ChannelID == ch.ID && v.LiveStatus == "live" {
			lives = append(lives, v)
			ids[v.ID], ids24[v.ID], live[v.ID] = true, true, true
		}
	}
	// El vivo que se mira va primero: es el del "programa en curso".
	sort.SliceStable(lives, func(i, j int) bool { return lives[i].ID == want && lives[j].ID != want })
	// from24 siempre es anterior a las 00:00 de hoy: una sola lectura alcanza.
	for _, l := range s.st.Lives(from24) {
		if l.ChannelID != ch.ID {
			continue
		}
		if !l.At.Before(from24) {
			ids24[l.VideoID] = true
		}
		if !l.At.Before(from) {
			samples = append(samples, l)
			ids[l.VideoID] = true
		}
	}
	ls := liveScope{segs: ProgramSegments(samples, ch.ID, live, from, now), sc: scopeDay, start: from}
	ls.part = s.channelPart(snap, ids24, from24)
	if len(lives) > 0 {
		ls.video = lives[0].ID
		v := lives[0]
		prog := ProgramName(v.Title, v.Channel)
		start := from
		for _, sg := range ls.segs {
			if sg.VideoID == v.ID && sg.Program == prog {
				start = sg.Start // el último tramo de ese programa
			}
		}
		// Desde 5 min antes (segmentLead): el cambio de título se ve hasta 3 min
		// tarde. Mismo filtro que el análisis con Claude.
		ls.msgs = segmentMessages(snap.Msgs, Segment{VideoID: v.ID, Program: prog, Start: start, State: SegLive}, videoTitles(snap.Videos))
		ls.prog, ls.start = prog, start
		ls.viewers, ls.pids, ls.concurrent = s.st.Viewers(v.ID, start.Add(-segmentLead)), []string{v.ID}, v.Concurrent
		ls.span, ls.sc = programLabel(prog)+" (programa en curso)", scopeProgram
		people := map[string]bool{}
		for _, m := range snap.Msgs {
			if ids[m.VideoID] && !m.At.Before(from) {
				ls.dayMsgs++
				people[m.Author] = true
			}
		}
		ls.dayPeople = len(people)
		return ls
	}
	for _, m := range snap.Msgs {
		if ids[m.VideoID] && !m.At.Before(from) {
			ls.msgs = append(ls.msgs, m)
		}
	}
	for id := range ids {
		ls.pids = append(ls.pids, id)
	}
	sort.Strings(ls.pids)
	for _, id := range ls.pids {
		ls.viewers = append(ls.viewers, s.st.Viewers(id, from)...)
	}
	for _, v := range lives {
		ls.concurrent += v.Concurrent
	}
	ls.span = "hoy"
	return ls
}

// channelPart junta los mensajes y espectadores de unos vivos desde since,
// para Momentos.
func (s *Server) channelPart(snap Snapshot, ids map[string]bool, since time.Time) partInput {
	var p partInput
	for _, m := range snap.Msgs {
		if ids[m.VideoID] && !m.At.Before(since) {
			p.msgs = append(p.msgs, m)
		}
	}
	for id := range ids {
		p.ids = append(p.ids, id)
	}
	sort.Strings(p.ids)
	for _, id := range p.ids {
		p.viewers = append(p.viewers, s.st.Viewers(id, since)...)
	}
	return p
}

func messageIDs(msgs []Message) []string {
	ids := make([]string, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
	}
	return ids
}

// AlertsData es la tarjeta de alertas con la flecha de sus mensajes plegados.
type AlertsData struct {
	AlertsView
	Fold Fold
	N    int // mensajes que se pueden ver
}

func alertsData(v AlertsView, fold foldState) AlertsData {
	d := AlertsData{AlertsView: v, Fold: fold.link("alertas")}
	for _, a := range v.Alerts {
		d.N += len(a.Msgs)
	}
	return d
}

// SentData es la tarjeta "Sentimiento del chat". Off o Empty dicen por qué
// no hay nada que mostrar.
type SentData struct {
	Off, Empty string
	View       SentimentView
	Chart      SentChart
	Slot       *SlotDetail // la franja elegida en el gráfico, con sus mensajes
	Neg, Pos   []MotiveRow
	NegN, PosN int
	When       string      // "en los últimos 30 min": la ventana de los motivos
	Wins       []MotiveWin // los botones de la ventana
}

// MotiveWin es un botón de la ventana de los motivos.
type MotiveWin struct {
	Key, Label, Href string
	On               bool
}

// claudeActive dice si el análisis con Claude está prendido.
func (s *Server) claudeActive(snap Snapshot) bool {
	return s.analyst != nil && snap.Claude.Off == "" && snap.Claude.Tope > 0
}

// sentimentData arma el sentimiento del alcance con las franjas ya sumadas
// en el Store (SentSlots) y las etiquetas de los mensajes del alcance.
func (s *Server) sentimentData(snap Snapshot, ch ChannelStatus, ls liveScope, labels map[string]MessageLabel, now time.Time, motives string, fold foldState, sel slotSel) SentData {
	if !s.claudeActive(snap) {
		return SentData{Off: "El sentimiento lo calcula Claude: está apagado (ver Canales)."}
	}
	if len(labels) == 0 {
		where := "en este programa"
		if ls.prog == "" {
			where = "hoy en el canal"
		}
		return SentData{Empty: "Todavía no hay mensajes analizados por Claude " + where + "."}
	}
	cur := slotOf(now)
	all := s.st.SentSlots(ch.ID, now.Add(-7*24*time.Hour))
	var history, today, chart []SentSlot
	chartFrom := sentChartFrom(ls, now)
	tramo := slotOf(ls.start)
	for _, sl := range all {
		if !sl.Start.Before(slotOf(chartFrom)) {
			chart = append(chart, sl)
		}
		switch {
		case ls.prog == "" && !sl.Start.Before(tramo) && sl.Start.Before(cur):
			today = append(today, sl)
		case ls.prog == "" || sl.Program != ls.prog:
		case sl.Start.Before(tramo):
			history = append(history, sl)
		case sl.Start.Before(cur):
			today = append(today, sl)
		}
	}
	if ls.prog == "" {
		today = mergeSlots(today)
	}
	recent := recentSent(ls.msgs, labels, now)
	typ, where, ok := sentimentTypical(history, today)
	byID := map[string]Message{}
	for _, m := range ls.msgs {
		byID[m.ID] = m
	}
	d := SentData{Chart: sentimentChart(chart, ls.segs, chartFrom, now)}
	d.Chart.Target, d.Chart.Sync, d.Chart.Anchor = "#vivo-analisis", "closest #vivo-analisis:replace", "#sent-detalle"
	s.slotLinks(&d, ls, fold, sel)
	since, when, _ := motiveSince(ls, motives, now)
	d.When = when
	for _, mw := range motiveWindows {
		key, label := mw.Key, mw.Label
		if mw.Span == 0 && ls.prog == "" {
			label = "Todo el día"
		}
		on := key == motives || key == "5" && motives == ""
		q := maps.Clone(fold.Query)
		q.Del("motivos")
		if key != "5" {
			q.Set("motivos", key)
		}
		if v := foldValue(fold.Open); v != "" {
			q.Set("abrir", v)
		}
		d.Wins = append(d.Wins, MotiveWin{Key: key, Label: label, On: on, Href: "/parcial/vivo/analisis?" + q.Encode()})
	}
	win := sentCount(ls.msgs, labels, since, now)
	batches := s.st.Batches(ch.ID, since)
	d.NegN, d.PosN = win.Neg, win.Pos
	d.Neg = motiveFolds(Motives(batches, "negativo", win.Neg, byID, since), "neg", fold)
	d.Pos = motiveFolds(Motives(batches, "positivo", win.Pos, byID, since), "pos", fold)
	// El cartel habla de los últimos 5 min: su causa sale de esos 5 min
	// aunque se esté mirando otra ventana.
	cause := ""
	if neg := Motives(s.st.Batches(ch.ID, now.Add(-slotStep)), "negativo", recent.Neg, byID, now.Add(-slotStep)); len(neg) > 0 && neg[0].Text != "Otros" {
		cause = neg[0].Text
	}
	d.View = sentimentCards(recent, typ, where, ok, cause)
	return d
}

// sentChartFrom es desde cuándo grafica "Cómo fue cambiando": 3 h con el
// programa en curso, desde las 00:00 con todo el día.
func sentChartFrom(ls liveScope, now time.Time) time.Time {
	if ls.prog != "" {
		return now.Add(-sentChartSpan)
	}
	return dayStart(now)
}

// slotPick es la franja y la pestaña pedidas, tal como llegan.
type slotPick struct{ franja, sent, part string } // part: la franja de Momentos

// slotSel es la franja elegida, ya validada, y su pestaña.
type slotSel struct {
	at  time.Time
	tab string
	ok  bool
}

// sentTabs son las pestañas del detalle de una franja: lista cerrada, con
// los negativos primero (lo que más le sirve a producción).
var sentTabs = []struct{ Key, Sentiment, Label string }{
	{"neg", "negativo", "Negativos"},
	{"neu", "neutro", "Neutros"},
	{"pos", "positivo", "Positivos"},
}

// valid acepta la franja solo si es el inicio de una franja de 5 min del
// gráfico (desde from hasta la de ahora); la pestaña, si no está en la
// lista, es la primera. Nada de lo que llega se refleja tal cual.
func (p slotPick) valid(from, now time.Time) slotSel {
	n, err := strconv.ParseInt(p.franja, 10, 64)
	if err != nil {
		return slotSel{}
	}
	at := time.Unix(n, 0)
	if !slotOf(at).Equal(at) || at.Before(slotOf(from)) || at.After(slotOf(now)) {
		return slotSel{}
	}
	sel := slotSel{at: at, tab: sentTabs[0].Key, ok: true}
	for _, t := range sentTabs {
		if t.Key == p.sent {
			sel.tab = t.Key
		}
	}
	return sel
}

// SlotDetail son los mensajes de una franja de "Cómo fue cambiando",
// separados por sentimiento; se ve la lista de una pestaña por vez.
type SlotDetail struct {
	Title, Close string
	Tabs         []SlotTab
	List         *FoldMsgs
	Empty        string
}

// SlotTab es una pestaña del detalle de una franja.
type SlotTab struct {
	Key, Label, Href string
	On               bool
}

// slotLinks le pone a cada barra el pedido de su franja y, con una elegida,
// arma su detalle con los mensajes del canal de esos 5 min y sus etiquetas
// (solo esos: nunca las tandas de 7 días).
func (s *Server) slotLinks(d *SentData, ls liveScope, fold foldState, sel slotSel) {
	base := maps.Clone(fold.Query)
	base.Del("franja")
	base.Del("sent")
	if v := foldValue(fold.Open); v != "" {
		base.Set("abrir", v)
	}
	href := func(q url.Values) string { return "/parcial/vivo/analisis?" + q.Encode() }
	for i := range d.Chart.Bars {
		b := &d.Chart.Bars[i]
		q := maps.Clone(base)
		q.Set("franja", strconv.FormatInt(b.Start.Unix(), 10))
		b.Href = href(q)
		b.Sel = sel.ok && b.Start.Equal(sel.at)
	}
	if !sel.ok {
		return
	}
	d.Chart.Sel = true
	ids := map[string]bool{}
	for _, id := range ls.part.ids {
		ids[id] = true
	}
	end := sel.at.Add(slotStep)
	var msgs []Message
	for _, m := range s.st.MessagesOf(ids, sel.at) {
		if m.Source == "chat" && m.At.Before(end) {
			msgs = append(msgs, m)
		}
	}
	labels := s.st.Labels(messageIDs(msgs))
	by := map[string][]Message{}
	for _, m := range msgs {
		if l, ok := labels[m.ID]; ok {
			by[l.Sentiment] = append(by[l.Sentiment], m)
		}
	}
	det := &SlotDetail{Title: "Mensajes de las " + sel.at.Local().Format("15:04") + " a " + end.Local().Format("15:04"), Close: href(base)}
	if p := slotProgram(ls.segs, sel.at, end); p != "" {
		det.Title += " · " + p
	}
	for _, t := range sentTabs {
		q := maps.Clone(base)
		q.Set("franja", strconv.FormatInt(sel.at.Unix(), 10))
		q.Set("sent", t.Key)
		tab := SlotTab{Key: t.Key, Label: t.Label + " (" + miles(len(by[t.Sentiment])) + ")", Href: href(q), On: t.Key == sel.tab}
		det.Tabs = append(det.Tabs, tab)
		if tab.On {
			if in := by[t.Sentiment]; len(in) > 0 {
				det.List = foldMsgs(in, len(in))
			} else {
				det.Empty = "Ningún mensaje " + strings.ToLower(strings.TrimSuffix(t.Label, "s")) + " en esta franja."
			}
		}
	}
	d.Slot = det
}

// slotProgram es el programa que más ocupa la franja [from, to), con el
// mismo nombre que la franja de programas del gráfico; "" sin vivo.
func slotProgram(segs []Segment, from, to time.Time) string {
	best, most := "", time.Duration(0)
	for _, g := range segs {
		end := g.End
		if end.IsZero() || end.After(to) {
			end = to
		}
		if d := end.Sub(maxTime(g.Start, from)); g.Program != "" && d > most {
			best, most = g.Program, d
		}
	}
	return best
}

// motiveFolds le pone a cada motivo con mensajes su flecha (prefix1, prefix2…).
func motiveFolds(rows []MotiveRow, prefix string, fold foldState) []MotiveRow {
	for i := range rows {
		if len(rows[i].Msgs) > 0 {
			rows[i].Key = prefix + strconv.Itoa(i+1)
			rows[i].Fold = fold.link(rows[i].Key)
		}
	}
	return rows
}

// QGData es la tarjeta "Preguntas agrupadas para producción". ByRules: sin
// Claude o sin etiquetas, la lista por reglas de siempre.
type QGData struct {
	ByRules   bool
	N         int    // preguntas en grupos de la ventana: la suma de las cantidades
	All       int    // lo mismo en todo el alcance, para el número de arriba
	When      string // el texto de la ventana: "en los últimos 5 min"
	Header    string
	Top, Rest []QGRow // las qgTop primeras a la vista; el resto, plegado
	Fold      Fold    // "Ver N más": sigue abierto después del refresco
}

// QGRow es un grupo con su cantidad ya escrita y su flecha (Key vacía más
// allá de qgFolds).
type QGRow struct {
	QGroupRow
	Count string
	Key   string
	Fold  Fold
	List  *FoldMsgs // solo si está desplegado
}

// qgTop es cuántos grupos se ven sin desplegar: con muchos, la lista tapaba
// el resto de la pantalla.
const qgTop = 5

// questionsData arma "Preguntas agrupadas" con los mensajes desde since
// (cero: todo el alcance) y when, el texto de la ventana. Sin botones:
// "Respondida" se sigue mostrando si había una marca guardada, pero ya no se
// ofrece marcar.
func (s *Server) questionsData(snap Snapshot, ls liveScope, labels map[string]MessageLabel, fold foldState, since time.Time, when string) QGData {
	if !s.claudeActive(snap) || len(labels) == 0 {
		return QGData{ByRules: true}
	}
	var gids []string
	for _, l := range labels {
		if l.Group != "" {
			gids = append(gids, l.Group)
		}
	}
	groups := s.st.Groups(gids)
	_, all := questionGroups(ls.msgs, labels, groups, s.answers.Answered)
	msgs := ls.msgs
	if !since.IsZero() {
		msgs = nil
		for _, m := range ls.msgs {
			if !m.At.Before(since) {
				msgs = append(msgs, m)
			}
		}
	}
	rows, n := questionGroups(msgs, labels, groups, s.answers.Answered)
	d := QGData{N: n, All: all, When: when, Header: plural(n, "pregunta", "preguntas") + " · " + plural(len(rows), "grupo", "grupos"), Fold: fold.link("maspreguntas")}
	if when != "" {
		d.Header += " " + when
	}
	for i, r := range rows {
		row := QGRow{QGroupRow: r, Count: miles(r.N)}
		if i < qgFolds {
			row.Key = "qg" + strconv.Itoa(i+1)
			row.Fold = fold.link(row.Key)
			if row.Fold.Open {
				var in []Message
				for _, m := range msgs {
					if labels[m.ID].Group == r.ID {
						in = append(in, m)
					}
				}
				row.List = foldMsgs(in, r.N)
			}
		}
		if i < qgTop {
			d.Top = append(d.Top, row)
		} else {
			d.Rest = append(d.Rest, row)
		}
	}
	return d
}

// answerPost es "Marcar respondida" / "Deshacer": solo desde la PC del
// monitor, guarda en Answers (nunca en el Store ni en YouTube) y responde la
// tarjeta de preguntas del mismo alcance.
func (s *Server) answerPost(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) || !localRequest(r) {
		http.Error(w, "Para marcar una pregunta, abrí el dashboard en la PC del monitor.", http.StatusForbidden)
		return
	}
	if s.answers == nil {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	id := r.PostForm.Get("grupo")
	g, ok := s.st.Groups([]string{id})[id]
	if !ok {
		http.Error(w, "Ese grupo de preguntas no existe (puede haber vencido).", http.StatusBadRequest)
		return
	}
	chID, _, _ := strings.Cut(g.ID, "|")
	snap := s.st.Snapshot()
	var ch ChannelStatus
	for _, c := range snap.Channels {
		if c.ID == chID {
			ch = c
		}
	}
	if ch.ID == "" {
		http.Error(w, "Ese canal ya no está en la configuración.", http.StatusBadRequest)
		return
	}
	now := s.clock()
	if err := s.answers.Mark(ch.ID, g.ID, r.PostForm.Get("deshacer") == "1", now); err != nil {
		log.Printf("aviso: no pude guardar la pregunta respondida: %v", err)
		http.Error(w, "No pude guardar la marca: probá de nuevo.", http.StatusInternalServerError)
		return
	}
	ls := s.channelScope(snap, ch, r.PostForm.Get("video"), now)
	labels := s.st.Labels(messageIDs(ls.msgs))
	s.render(w, "vivo-preguntas", map[string]any{"QG": s.questionsData(snap, ls, labels, foldState{}, time.Time{}, ""), "A": Analyze(ls.msgs)})
}

// claudeLine es la línea de Claude debajo del selector: hasta dónde
// analizaron las tandas del canal y con cuánta demora, o por qué no analiza.
func (s *Server) claudeLine(snap Snapshot, ch ChannelStatus, now time.Time) string {
	st := snap.Claude
	switch {
	case s.analyst == nil:
		return ""
	case st.Off != "":
		return "apagado hasta reiniciar el monitor: " + st.Off
	case st.Tope <= 0:
		return "apagado: el tope de gasto es US$ 0 (ver Canales)"
	case !s.batchOn(ch):
		return "este canal no se analiza en tandas: activalo en Canales"
	case st.RealtimeCapped:
		return "se llegó a la parte del tope para tiempo real: se vuelve a las reglas hasta mañana"
	}
	model := st.RealtimeModel
	var last ClaudeBatch
	for _, b := range s.st.Batches(ch.ID, dayStart(now)) {
		if b.Error == "" && b.At.After(last.At) {
			last = b
		}
	}
	if !last.At.IsZero() {
		model = last.Model
	}
	parts := []string{"(" + strings.TrimPrefix(claudeModelLabel(model), "Claude ") + ")"}
	if last.At.IsZero() {
		parts = append(parts, "todavía sin tandas hoy")
	} else {
		parts = append(parts, "analizado hasta las "+last.To.Local().Format("15:04:05"),
			fmt.Sprintf("%d s de demora", int(last.At.Sub(last.To).Seconds())))
	}
	if st.BatchEvery > 0 {
		parts = append(parts, "tandas cada "+humanDur(st.BatchEvery))
	}
	return strings.Join(parts, " · ")
}

// batchOn dice si el canal tiene el check "Analizar el chat con Claude".
func (s *Server) batchOn(ch ChannelStatus) bool {
	if s.settings == nil {
		return true
	}
	list, _ := s.settings.Get()
	for _, l := range list {
		if l.Input == ch.Input {
			return !l.NoClaude
		}
	}
	return true
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
		in.Fold = foldState{Open: foldParam(r.PostForm), Query: analysisQuery(ch.ID, video, hours, motiveParam(r.PostForm))}
		// La tarjeta está en la pestaña De qué se habla: sus flechas se quedan ahí.
		if tab := liveTabParam(r.PostForm); tab != "" {
			in.Fold.Query.Set("pestana", tab)
		}
		// Los temas van en "De qué se habla", como en el refresco del análisis.
		c := claudeView(in)
		talkClaude(&c)
		s.render(w, "claude-card", c)
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
		for _, g := range a.Result.Topics {
			for _, id := range groupIDs(g) {
				need[id] = true
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
// aria-label y en el <title> del bloque; Href, en un programa que terminó,
// lleva a su detalle en Programas.
type StripBlock struct {
	Class, Program, Range, State, Note, Text, Href string
	X, W                                           float64 // en minutos desde las 00:00, para el SVG
}

var stripClass = map[string]string{SegEnded: "fin", SegLive: "aire", SegNone: "sin"}

// dayStrip arma la tira "Hoy en el canal" y el cartel de continuidad. full
// son los tramos desde las 12:00 de ayer, como en Programas (Hoy): un programa
// que empezó antes de las 00:00 en la tira arranca a las 00:00, y su clave
// tiene que ser la de su inicio real para que Programas lo encuentre.
func dayStrip(segs, full []Segment, ch ChannelStatus, from, now time.Time, paused bool) map[string]any {
	keys := map[string]string{}
	for _, sg := range full {
		keys[sg.VideoID+"|"+sg.Program+"|"+sg.End.UTC().Format(time.RFC3339)] = segmentKey(ch.ID, sg)
	}
	var blocks []StripBlock
	for i, sg := range segs {
		end := stripEnd(segs, i)
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
		if key, ok := keys[sg.VideoID+"|"+sg.Program+"|"+sg.End.UTC().Format(time.RFC3339)]; ok && sg.State == SegEnded && sg.Program != "" {
			b.Href = "/programas?" + url.Values{"canal": {ch.ID}, "tramo": {key}}.Encode()
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
	// Lo actual primero en el HTML: el CSS (row-reverse) lo pone a la derecha,
	// como en los gráficos, y el scroll de la tira arranca ahí, así lo que
	// está al aire se ve sin scrollear en un celular.
	slices.Reverse(blocks)
	return map[string]any{
		"Blocks": blocks, "W": max(now.Sub(from).Minutes(), 1), "Banner": ContinuityBanner(segs, now),
		"Aria": fmt.Sprintf("Hoy en el canal desde las %s: %d tramos", from.Local().Format("15:04"), len(blocks)),
	}
}

// stripEnd es dónde termina en la tira el tramo i. Un programa que terminó y
// al que le sigue otro del mismo vivo a lo sumo segmentGap después termina
// donde empieza el siguiente: las muestras son cada 3 min y ese hueco no fue
// un corte. Solo es para dibujar: End sigue siendo la última muestra.
func stripEnd(segs []Segment, i int) time.Time {
	sg := segs[i]
	if sg.State != SegEnded {
		return sg.End
	}
	for _, next := range segs[i+1:] {
		if next.State != SegNone && next.VideoID == sg.VideoID {
			if next.Start.After(sg.End) && next.Start.Sub(sg.End) <= segmentGap {
				return next.Start
			}
			break
		}
	}
	return sg.End
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
	Key                             string    // mom1…mom5: la flecha del momento
	Fold                            Fold      // plegado por defecto: solo horario y título a la vista
	Msgs                            []Message // los mensajes de la franja, en orden (momentListMax)
	More                            int       // los que no entraron en Msgs
}

// momentListMax es cuántos mensajes de un momento se listan como mucho: una
// franja de 5 min con mucho chat haría un HTML enorme en cada refresco.
const momentListMax = 100

// momentMsgs son los mensajes de un momento para leer al desplegarlo: en
// orden, sin los que no tienen texto (Super Stickers) ni el spam.
func momentMsgs(msgs []Message) (out []Message, more int) {
	msgs = slices.Clone(msgs)
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].At.Before(msgs[j].At) })
	for _, m := range msgs {
		if strings.TrimSpace(m.Text) == "" || Classify(m.Text) == CatSpam {
			continue
		}
		if len(out) == momentListMax {
			more++
			continue
		}
		out = append(out, m)
	}
	return out, more
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
func participationView(msgs []Message, viewers []ViewerSample, ids []string, videos []Video, concurrent int64, titles map[string]string, segs []Segment, now time.Time, hours int, fold foldState, pick string) (map[string]any, []Moment, float64) {
	slots, typical, ok := Participation(msgs, viewers, ids, now)
	moments := TopMoments(slots, titles)
	shown := chartWindow(slots, hours)
	sel := pickSlot(shown, pick)
	if sel >= 0 && fold.Query != nil {
		// Los demás links de la sección (plegados, Categorías) conservan la
		// franja: fold.Query es el mismo mapa que usan después.
		fold.Query.Set("pfranja", strconv.FormatInt(shown[sel].Start.Unix(), 10))
	}
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
		row.Key = "mom" + strconv.Itoa(m.Rank)
		row.Fold = fold.link(row.Key)
		row.Msgs, row.More = momentMsgs(m.Msgs)
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
	chart := participationChart(shown, moments, typical, ok, titles)
	if len(shown) > 0 {
		chart["Strip"], chart["Lines"] = programStrip(segs, shown[0].Start, len(shown), now)
	}
	slot, pfranja := momentSlotLinks(chart, shown, sel, segs, fold)
	// La última franja es la de ahora: el borde derecho dice "Ahora".
	if ticks, _ := chart["Ticks"].([]Tick); len(ticks) > 0 && ticks[len(ticks)-1].End {
		ticks[len(ticks)-1].Label = "Ahora"
	}
	typ := typical
	if !ok {
		typ = 0 // sin espectadores no hay participación típica para comparar
	}
	return map[string]any{
		"Chart": chart, "Moments": rows, "Why": why,
		"Windows": []int{chartHours, 24}, "Outside": outsideNote,
		"ByPct": ok, "Typical": pct(typical), "Concurrent": concurrent, "Missing": concurrent > 0 && missing,
		"Slot": slot, "PFranja": pfranja,
	}, moments, typ
}

// pickSlot es la franja de Momentos pedida si es el inicio de una barra del
// gráfico, o -1. Nada de lo que llega se refleja tal cual.
func pickSlot(shown []Slot, pick string) int {
	n, err := strconv.ParseInt(pick, 10, 64)
	if err != nil {
		return -1
	}
	for i, s := range shown {
		if s.Start.Unix() == n {
			return i
		}
	}
	return -1
}

// momentSlotLinks le pone a cada barra de Momentos el pedido de su franja,
// como las barras de Participación de Programas, y con una elegida arma su
// detalle con los mensajes de chat de esos 5 min (los de la franja, sin el
// spam ni los vacíos, como los momentos). Devuelve el detalle y la franja.
func momentSlotLinks(chart map[string]any, shown []Slot, sel int, segs []Segment, fold foldState) (*SlotDetail, string) {
	base := maps.Clone(fold.Query)
	if base == nil {
		base = url.Values{}
	}
	base.Del("pfranja")
	if v := foldValue(fold.Open); v != "" {
		base.Set("abrir", v)
	}
	href := func(q url.Values) string { return "/parcial/vivo/analisis?" + q.Encode() }
	bars, _ := chart["Bars"].([]Bar)
	for i := range bars {
		if i < len(shown) {
			q := maps.Clone(base)
			unix := strconv.FormatInt(shown[i].Start.Unix(), 10)
			q.Set("pfranja", unix)
			bars[i].Href, bars[i].ID, bars[i].Sel = href(q), "mb-"+unix, i == sel
		}
	}
	chart["Links"], chart["Target"], chart["Sync"], chart["Anchor"] = true, "#vivo-analisis", "closest #vivo-analisis:replace", "#pfranja-det"
	if sel < 0 {
		return nil, ""
	}
	sl := shown[sel]
	end := sl.Start.Add(slotStep)
	det := &SlotDetail{Title: "Mensajes de las " + sl.Start.Local().Format("15:04") + " a " + end.Local().Format("15:04"), Close: href(base)}
	if p := slotProgram(segs, sl.Start, end); p != "" {
		det.Title += " · " + p
	}
	msgs, more := momentMsgs(sl.Msgs)
	if len(msgs) == 0 {
		det.Empty = "Ningún mensaje de chat en esta franja."
	} else {
		det.List = foldMsgs(msgs, len(msgs)+more)
	}
	return det, strconv.FormatInt(sl.Start.Unix(), 10)
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

// ViewersLine es la línea de oyentes sobre las barras: escalones con el
// promedio de espectadores de cada franja, cortados donde no hay dato.
type ViewersLine struct{ Path, Max, Aria string }

// viewersLine arma la línea de oyentes de la ventana graficada, dentro del
// mismo SVG de las barras pero con su propia escala desde 0 (para comparar "muchos" contra "pocos"). Son escalones y no
// una línea entre centros: cada valor es el promedio de toda la franja y una
// franja aislada igual se ve. Sin dato en la ventana no hay línea.
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
	// De izquierda (lo más viejo) a derecha (ahora), como las barras.
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
	return participationChartCmp(slots, moments, typical, ok, titles, nil)
}

// ChartCompare es contra qué se compara el gráfico de participación de un
// programa, por hora del reloj (clockKey): la banda de lo normal o la línea
// del % de otra emisión. Label dice contra qué ("martes 03/11").
type ChartCompare struct {
	Band  map[string]Band
	Line  map[string]float64
	Label string
}

// participationChartCmp es participationChart con una comparación (nil:
// ninguna, el gráfico de siempre). Solo dibuja la comparación en modo %.
func participationChartCmp(slots []Slot, moments []Moment, typical float64, ok bool, titles map[string]string, cmp *ChartCompare) map[string]any {
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
		// Lo comparado también entra en la escala, para que nada quede afuera.
		for _, s := range slots {
			if cmp == nil {
				break
			}
			k := clockKey(s.Start)
			if b, has := cmp.Band[k]; has {
				scale = max(scale, b.P75)
			}
			if v, has := cmp.Line[k]; has {
				scale = max(scale, v)
			}
		}
	} else {
		scale = float64(peak.Authors)
	}
	var bars, bands []Bar
	var marks []Mark
	median, line := make([]float64, len(slots)), make([]float64, len(slots))
	hasMed, hasLine := make([]bool, len(slots)), make([]bool, len(slots))
	over := 0
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
		// De izquierda a derecha: lo más nuevo (ahora) en el borde derecho.
		pos := float64(i)
		b := Bar{X: pos + 0.1, Y: 100 - h, W: 0.8, H: h, N: s.Authors, Label: slotTitle(s, titles)}
		if r := rank[s.Start]; ok && r > 0 {
			b.Class = "top"
			b.Label += fmt.Sprintf(" · N.º %d de los %d con más participación", r, maxMomentos)
			marks = append(marks, Mark{Left: (pos + 0.5) / float64(w) * 100, Rank: r})
		}
		if cmp != nil && ok && scale > 0 {
			k, p := clockKey(s.Start), i
			if bd, has := cmp.Band[k]; has {
				bands = append(bands, Bar{X: pos, Y: 100 - bd.P75/scale*100, W: 1, H: (bd.P75 - bd.P25) / scale * 100})
				median[p], hasMed[p] = bd.Med, true
				if eligible(s) && s.Pct > bd.P75 {
					b.Class = strings.TrimSpace(b.Class + " sobre")
					b.Label += fmt.Sprintf(" · por encima de lo normal (lo normal: %s a %s)", pctFine(bd.P25), pctFine(bd.P75))
					over++
				}
			}
			if v, has := cmp.Line[k]; has {
				line[p], hasLine[p] = v, true
			}
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
	// Las horas se cuentan desde el final, para que el borde de ahora (End,
	// a la derecha) siempre tenga rótulo, y salen de izquierda a derecha (el
	// CSS alinea la primera y la última por su posición).
	var ticks []Tick
	for i := len(slots); i >= 0 && len(slots) > 0; i -= step {
		ticks = append(ticks, Tick{Left: float64(i) / float64(w) * 100, Label: slots[0].Start.Add(time.Duration(i) * slotStep).Local().Format("15:04"), End: i == len(slots)})
	}
	slices.Reverse(ticks)
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
	// La escala de la línea es el máximo de la ventana recibida: cambia con 6 h / 24 h.
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
	if len(bands) > 0 {
		out["Bands"], out["Median"] = bands, stepPath(median, hasMed, scale)
		aria += "; comparado con " + cmp.Label + ": " + plural(over, "franja", "franjas") + " por encima de lo normal"
	}
	if p := stepPath(line, hasLine, scale); p != "" {
		out["CmpLine"] = p
		aria += "; comparado con " + cmp.Label + " (línea)"
	}
	out["Aria"] = aria
	return out
}

// stepPath es una línea escalonada con un escalón por posición del gráfico
// (de izquierda a derecha), que se corta donde has es false.
func stepPath(vals []float64, has []bool, scale float64) string {
	if scale <= 0 {
		return ""
	}
	var path strings.Builder
	for i, v := range vals {
		if !has[i] {
			continue
		}
		y := 100 - v/scale*100
		if i == 0 || !has[i-1] {
			fmt.Fprintf(&path, "M%d %.2fH%d", i, y, i+1)
		} else {
			fmt.Fprintf(&path, "V%.2fH%d", y, i+1)
		}
	}
	return path.String()
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
	// Cuántos miraban va siempre que haya dato: la línea de oyentes remite a
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

// pctFine es pct con dos decimales por debajo del 1 %: la participación de
// un programa suele ser de 0,0x % y con un decimal todas se verían iguales.
func pctFine(f float64) string { return decimalFine(f) + " %" }

// decimalFine es decimal con dos decimales por debajo de 1 (ver pctFine).
func decimalFine(f float64) string {
	if f > 0 && f < 1 {
		return strings.Replace(fmt.Sprintf("%.2f", f), ".", ",", 1)
	}
	return decimal(f)
}

func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	if key := r.URL.Query().Get("tramo"); key != "" {
		s.exportProgram(w, key, r.URL.Query().Get("dia"))
		return
	}
	if id := r.URL.Query().Get("video"); id != "" {
		s.exportVideo(w, id)
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
	At                                time.Time // cuándo se analizó
	Partial, Compact                  bool      // Compact: en "Todo el día", solo resumen y sentimiento
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
	Key         string // tema1…: el tema se despliega en En vivo
	Fold        Fold
	List        *FoldMsgs // solo si está desplegado
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

// claudeZeroText: con la clave cargada el analista corre, pero con tope 0 no
// manda nada (ADR 0009).
const claudeZeroText = "El análisis con Claude está apagado: el tope de gasto es US$ 0. Para activarlo, poné un tope en Canales."

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
	case st.Tope <= 0:
		return ClaudeCard{Title: title, Off: claudeZeroText}
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
		topicFolds(&b, a.Result.Topics, in.Msgs, in.Fold)
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
	b := ClaudeBlock{Title: a.Program, At: a.At, Partial: a.Partial, Compact: compact, Err: a.Error}
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

// topicFolds le pone su flecha a cada tema del resumen de En vivo y arma la
// lista de los desplegados con los mensajes guardados del tema.
func topicFolds(b *ClaudeBlock, topics []ClaudeGroup, msgs map[string]Message, fold foldState) {
	for i := range b.Topics {
		if i >= len(topics) {
			break
		}
		it := &b.Topics[i]
		it.Key = "tema" + strconv.Itoa(i+1)
		it.Fold = fold.link(it.Key)
		if !it.Fold.Open {
			continue
		}
		var in []Message
		for _, id := range groupIDs(topics[i]) {
			if m, ok := msgs[id]; ok {
				in = append(in, m)
			}
		}
		it.List = foldMsgs(in, topics[i].Msgs)
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return miles(n) + " " + many
}

// Pantalla Canales. Con Settings (el modo dashboard) se edita desde la PC
// del monitor: agregar, pausar, quitar y los intervalos de cada canal. Los
// handlers solo cambian Settings (con su mutex, detrás de sameOrigin y
// localRequest) y leen el Store; nunca llaman a YouTube: los canales nuevos
// los resuelve el worker. Sin Settings (los tests de antes), solo lectura.

// ChannelRow es la tarjeta de estado de un canal en Canales.
type ChannelRow struct {
	Name, Input, Role string
	Live, Paused      bool
	Pending           bool   // todavía no lo resolvió el worker
	Err               string // YouTube no lo encontró
	Status, How       string
	Hours, Chat       string
}

// QuotaPart es un pedazo de la barra de cuota (Class elige el color).
type QuotaPart struct {
	Class, Label, N string
	W               float64 // % de quotaScale
}

// ChannelForm es un canal en el formulario de Canales.
type ChannelForm struct {
	I                 int
	Input, Name, Role string
	Paused            bool
	Peak, Off         []IntervalOpt
	Estimate          string
	PeakSec, OffSec   int
}

// IntervalOpt es una opción de los selects de intervalo.
type IntervalOpt struct {
	Sec      int
	Label    string
	Selected bool
}

// intervalOpts son las opciones de un select, con la actual elegida y
// agregada si no está en la lista (puede venir de los flags).
func intervalOpts(choices []int, cur int) []IntervalOpt {
	secs := append([]int(nil), choices...)
	if !slices.Contains(secs, cur) {
		secs = append(secs, cur)
		slices.Sort(secs)
	}
	out := make([]IntervalOpt, len(secs))
	for i, n := range secs {
		out[i] = IntervalOpt{n, humanDur(time.Duration(n) * time.Second), n == cur}
	}
	return out
}

// chatText describe los intervalos del chat, que valen para todos los vivos.
func chatText(cs ChatSettings) string {
	if cs.PeakFrom == cs.PeakTo {
		return "cada " + humanDur(cs.Off) + " todo el día (sin horario pico), por turnos entre todos los vivos"
	}
	return fmt.Sprintf("cada %s en el pico (%02d a %02d h) y cada %s fuera, por turnos entre todos los vivos",
		humanDur(cs.Peak), cs.PeakFrom, cs.PeakTo, humanDur(cs.Off))
}

// channelChat describe los intervalos de un canal de la configuración.
func channelChat(cs ChatSettings, c ChannelSetting) string {
	t := fmt.Sprintf("cada %s en el pico (%02d a %02d h) y cada %s fuera", humanDur(c.peak()), cs.PeakFrom, cs.PeakTo, humanDur(c.off()))
	if cs.PeakFrom == cs.PeakTo {
		t = "cada " + humanDur(c.off()) + " todo el día (sin horario pico)"
	}
	if c.Paused {
		t += " · en pausa: no se lee hasta que lo reanudes"
	}
	return t
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

// editable dice si este pedido puede cambiar la configuración: hay Settings
// y viene de la PC del monitor (el dashboard no tiene usuarios).
func (s *Server) editable(r *http.Request) bool { return s.settings != nil && localRequest(r) }

func (s *Server) channelsPage(w http.ResponseWriter, r *http.Request) {
	now := s.clock()
	snap := s.st.Status()
	edit := s.editable(r)
	data := s.channelsData(snap, now, edit)
	data["Tab"], data["Channel"] = "canales", snap.Channel
	data["Claude"] = s.claudeSetupFull(r, snap, now)
	data["FastEvery"], data["FastFor"], data["FastMax"] = humanDur(fastEvery), humanDur(fastFor), fastUnitsPerCut()
	every := 3 * time.Minute
	for _, c := range snap.Channels {
		if c.Every > 0 {
			every = c.Every
		}
	}
	data["Every"] = humanDur(every)
	if s.settings != nil {
		data["Warn"] = s.settings.Warning()
	}
	if edit {
		data["Add"] = s.addData("", "", "")
		data["Form"] = s.formData(snap, now, nil, "", "", false)
	}
	s.render(w, "canales.html", data)
}

// channelsState es el estado de los canales (cada 5 s). No trae el
// formulario: así no pisa lo que se está editando.
func (s *Server) channelsState(w http.ResponseWriter, r *http.Request) {
	s.render(w, "canales-estado", s.channelsData(s.st.Status(), s.clock(), s.editable(r)))
}

// settingsList es la configuración vigente, o la de -canal armada con el
// estado de los canales (sin Settings, los tests de antes).
func (s *Server) settingsList(snap Snapshot) []ChannelSetting {
	if s.settings != nil {
		list, _ := s.settings.Get()
		return list
	}
	cs := s.chatSettings()
	var list []ChannelSetting
	for _, c := range snap.Channels {
		list = append(list, ChannelSetting{Input: c.Input, ChatPeakSec: int(cs.Peak / time.Second), ChatOffSec: int(cs.Off / time.Second)})
	}
	return list
}

// channelsData arma las tarjetas de estado de los canales y, salvo que se
// pueda editar (ahí va en el formulario), la cuota estimada.
func (s *Server) channelsData(snap Snapshot, now time.Time, edit bool) map[string]any {
	cs := s.chatSettings()
	week := s.st.Lives(now.Add(-7 * 24 * time.Hour))
	paused := s.quotaPaused(snap)
	byInput := map[string]ChannelStatus{}
	for _, ch := range snap.Channels {
		byInput[ch.Input] = ch
	}
	errs := map[string]string{}
	for _, p := range snap.Pending {
		errs[p.Input] = p.Err
	}
	var rows []ChannelRow
	addRow := func(ch ChannelStatus, i int) ChannelRow {
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
		return row
	}
	if s.settings == nil {
		for i, ch := range snap.Channels {
			rows = append(rows, addRow(ch, i))
		}
	} else {
		resolved := 0
		for _, set := range s.settingsList(snap) {
			ch, ok := byInput[set.Input]
			if !ok {
				rows = append(rows, ChannelRow{Name: set.Input, Input: set.Input, Role: "canal nuevo", Pending: true, Err: errs[set.Input], Paused: set.Paused})
				continue
			}
			row := addRow(ch, resolved)
			resolved++
			row.Chat, row.Paused = channelChat(cs, set), set.Paused
			rows = append(rows, row)
		}
	}
	data := map[string]any{"Rows": rows, "Editable": edit, "Settings": s.settings != nil, "Used": miles(snap.Units),
		"ChatNow": chatNow(cs, now, s.settings != nil)}
	if !edit {
		_, data["Cuota"] = s.quotaData(snap, now, nil)
	}
	return data
}

// draftOf lee los intervalos de un formulario (canal, pico y fuera, uno por
// canal y en el mismo orden). Lo que no es un número se descarta.
func draftOf(v url.Values) map[string][2]int {
	out := map[string][2]int{}
	ins, peaks, offs := v["canal"], v["pico"], v["fuera"]
	for i, in := range ins {
		if i >= len(peaks) || i >= len(offs) {
			break
		}
		p, err1 := strconv.Atoi(peaks[i])
		o, err2 := strconv.Atoi(offs[i])
		if err1 == nil && err2 == nil {
			out[in] = [2]int{p, o}
		}
	}
	return out
}

// withDraft aplica a la configuración los intervalos del formulario que se
// pueden guardar (o que son los de ahora), para mostrarlos y estimar la cuota.
func withDraft(list []ChannelSetting, draft map[string][2]int) []ChannelSetting {
	out := append([]ChannelSetting(nil), list...)
	for i, c := range out {
		d, ok := draft[c.Input]
		if !ok {
			continue
		}
		if validChatSec(d[0]) || d[0] == c.ChatPeakSec {
			out[i].ChatPeakSec = d[0]
		}
		if validChatSec(d[1]) || d[1] == c.ChatOffSec {
			out[i].ChatOffSec = d[1]
		}
	}
	return out
}

// quotaData estima la cuota del día con la configuración (y los intervalos
// del formulario, si hay): un grupo de chat por canal. Devuelve también las
// filas del formulario, con la cuota de cada chat.
func (s *Server) quotaData(snap Snapshot, now time.Time, draft map[string][2]int) ([]ChannelForm, map[string]any) {
	cs := s.chatSettings()
	week := s.st.Lives(now.Add(-7 * 24 * time.Hour))
	budget := s.budget
	if budget <= 0 {
		budget = 9000
	}
	ids := map[string]bool{}
	for _, c := range snap.Channels {
		ids[c.ID] = true
	}
	lh := LiveHours(week, ids, cs.PeakFrom, cs.PeakTo, now)
	hours := "sin historial: se suponen 24 h en vivo"
	if !lh.NoHistory {
		hours = fmt.Sprintf("%s h en vivo por día en el pico y %s h fuera, según los últimos 7 días", decimal(lh.Peak), decimal(lh.Off))
	}
	var groups []ChatGroup
	var rows []ChannelForm
	chatLabel := "Chat de todos los vivos (por turnos)"
	if s.settings == nil {
		// Sin Settings todos los vivos se turnan con un solo intervalo: un
		// grupo con las horas en vivo de la unión de los canales.
		groups = []ChatGroup{{PeakEvery: cs.Peak, OffEvery: cs.Off, PeakHours: lh.Peak, OffHours: lh.Off}}
	} else {
		chatLabel = "Chat de los canales"
		byInput := map[string]ChannelStatus{}
		for _, ch := range snap.Channels {
			byInput[ch.Input] = ch
		}
		resolved := 0
		for i, c := range withDraft(s.settingsList(snap), draft) {
			ch, ok := byInput[c.Input]
			one := LiveHours(nil, nil, cs.PeakFrom, cs.PeakTo, now) // sin resolver: 24 h, el peor caso
			row := ChannelForm{I: i, Input: c.Input, Name: c.Input, Role: "canal nuevo", Paused: c.Paused, PeakSec: c.ChatPeakSec, OffSec: c.ChatOffSec}
			if ok {
				one = LiveHours(week, map[string]bool{ch.ID: true}, cs.PeakFrom, cs.PeakTo, now)
				row.Name, row.Role = channelName(ch), "canal extra"
				if resolved == 0 {
					row.Role = "canal principal"
				}
				resolved++
			}
			row.Peak, row.Off = intervalOpts(peakChoices, c.ChatPeakSec), intervalOpts(offChoices, c.ChatOffSec)
			row.Estimate = decimal(one.Peak+one.Off) + " h en vivo"
			if one.NoHistory {
				row.Estimate = "sin historial"
			}
			groups = append(groups, ChatGroup{PeakEvery: c.peak(), OffEvery: c.off(), PeakHours: one.Peak, OffHours: one.Off, Paused: c.Paused})
			rows = append(rows, row)
		}
	}
	n := len(groups) // con Settings, un grupo por canal de la configuración
	if s.settings == nil {
		n = len(snap.Channels)
	}
	// Los vivos sueltos de -vivos que no son de un canal configurado leen su
	// chat aparte, con -chat-seg-pico / -chat-seg: suman su propio grupo.
	loose := map[string]bool{}
	for _, v := range snap.Videos {
		if s.settings != nil && v.LiveStatus == "live" && v.LiveChatID != "" && !ids[v.ChannelID] {
			loose[v.ChannelID] = true
		}
	}
	if len(loose) > 0 {
		lo := LiveHours(week, loose, cs.PeakFrom, cs.PeakTo, now)
		groups = append(groups, ChatGroup{PeakEvery: cs.Peak, OffEvery: cs.Off, PeakHours: lo.Peak, OffHours: lo.Off})
	}
	q := EstimateQuota(n, lh.Cuts, groups, budget)
	chat, looseChat := 0, 0
	for i, n := range q.Chat {
		if i >= len(rows) && len(loose) > 0 && s.settings != nil {
			looseChat += n
			continue
		}
		chat += n
		if i < len(rows) {
			why := rows[i].Estimate
			if rows[i].Paused {
				why = "en pausa"
			}
			rows[i].Estimate = fmt.Sprintf("Cuota estimada de este chat: %s unidades por día (%s)", miles(n), why)
		}
	}
	parts := []QuotaPart{
		{"q-vid", "Revisar videos y vivos", miles(q.Videos + q.Fast), 0},
		{"q-com", "Comentarios", "~" + miles(q.Comments), 0},
		{"q-chat", chatLabel, miles(chat), 0},
	}
	sizes := []int{q.Videos + q.Fast, q.Comments, chat}
	if len(loose) > 0 {
		parts = append(parts, QuotaPart{"q-loose", "Vivos sueltos de -vivos", miles(looseChat), 0})
		sizes = append(sizes, looseChat)
	}
	left := 100.0
	for i, n := range sizes {
		parts[i].W = min(float64(n)*100/quotaScale, left)
		left -= parts[i].W
	}
	return rows, map[string]any{
		"Quota": q, "Parts": parts, "Hours": hours, "Used": miles(snap.Units), "Editable": s.settings != nil,
		"Total": miles(q.Total), "Limit": miles(q.Limit), "Budget": miles(budget),
		"LimitX": float64(q.Limit) * 100 / quotaScale, "BudgetX": float64(budget) * 100 / quotaScale,
		"Aria": fmt.Sprintf("Cuota estimada por día: %s unidades de %s (videos y vivos %s, comentarios %s, chat %s); el chat se pausa en %s y todo se frena en %s",
			miles(q.Total), miles(quotaScale), miles(q.Videos+q.Fast), miles(q.Comments), miles(chat+looseChat), miles(q.Limit), miles(budget)),
		"Reserve": miles(reserveForComments),
	}
}

// formData es el formulario de los canales: lo guardado con los intervalos
// del borrador (draft) y un aviso.
func (s *Server) formData(snap Snapshot, now time.Time, draft map[string][2]int, notice, class string, oob bool) map[string]any {
	rows, quota := s.quotaData(snap, now, draft)
	cs := s.chatSettings()
	peak := fmt.Sprintf("%02d a %02d h", cs.PeakFrom, cs.PeakTo)
	if cs.PeakFrom == cs.PeakTo {
		peak = "no hay horario pico"
	}
	list, _ := s.settings.Get()
	dirty := false
	for i, c := range list {
		if i < len(rows) && (rows[i].PeakSec != c.ChatPeakSec || rows[i].OffSec != c.ChatOffSec) {
			dirty = true
		}
	}
	return map[string]any{"Rows": rows, "Cuota": quota, "Notice": notice, "NoticeClass": class, "OOB": oob, "PeakText": peak, "Dirty": dirty}
}

// addData es el formulario "Agregar canal".
func (s *Server) addData(value, notice, class string) map[string]any {
	list, _ := s.settings.Get()
	return map[string]any{"Value": value, "Notice": notice, "NoticeClass": class, "Full": len(list) >= maxChannels, "Max": maxChannels}
}

// channelsForm es "Descartar": el formulario con lo guardado.
func (s *Server) channelsForm(w http.ResponseWriter, r *http.Request) {
	if !s.editable(r) {
		http.Error(w, "Para cambiar la configuración, abrí el dashboard en la PC del monitor.", http.StatusForbidden)
		return
	}
	s.render(w, "canales-form", s.formData(s.st.Status(), s.clock(), nil, "", "", false))
}

// channelsQuota recalcula la barra con lo elegido en el formulario, sin
// guardar nada; las cuotas de cada canal van con hx-swap-oob.
func (s *Server) channelsQuota(w http.ResponseWriter, r *http.Request) {
	if !s.editable(r) {
		http.Error(w, "Para cambiar la configuración, abrí el dashboard en la PC del monitor.", http.StatusForbidden)
		return
	}
	rows, quota := s.quotaData(s.st.Status(), s.clock(), draftOf(r.URL.Query()))
	s.render(w, "canales-cuota-oob", map[string]any{"Cuota": quota, "Rows": rows})
}

// settingsPost son las guardas de los POST de Canales: mismo origen, desde
// la PC del monitor y un cuerpo chico. Si devuelve false ya respondió.
func (s *Server) settingsPost(w http.ResponseWriter, r *http.Request) bool {
	return s.settingsPostN(w, r, 16<<10)
}

// settingsPostN es settingsPost con otro límite de cuerpo.
func (s *Server) settingsPostN(w http.ResponseWriter, r *http.Request, limit int64) bool {
	if s.settings == nil {
		http.NotFound(w, r)
		return false
	}
	if !sameOrigin(r) || !localRequest(r) {
		http.Error(w, "Para cambiar la configuración, abrí el dashboard en la PC del monitor.", http.StatusForbidden)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return false
	}
	return true
}

// renderForm responde el formulario con un aviso. Si no se guardó, el aviso
// lo dice y quedan los intervalos que se estaban editando.
func (s *Server) renderForm(w http.ResponseWriter, r *http.Request, ok, failed string, err error) {
	notice, class := ok, "ok"
	if err != nil {
		notice, class = failed+err.Error(), "warn"
	}
	s.render(w, "canales-form", s.formData(s.st.Status(), s.clock(), draftOf(r.PostForm), notice, class, false))
}

// channelsSave es "Guardar cambios": los intervalos de todos los canales.
func (s *Server) channelsSave(w http.ResponseWriter, r *http.Request) {
	if !s.settingsPost(w, r) {
		return
	}
	ins, peaks, offs := r.PostForm["canal"], r.PostForm["pico"], r.PostForm["fuera"]
	err := s.settings.Update(func(cs []ChannelSetting) ([]ChannelSetting, error) {
		for i, in := range ins {
			if i >= len(peaks) || i >= len(offs) {
				return nil, errors.New("faltan intervalos en el formulario")
			}
			p, err1 := strconv.Atoi(peaks[i])
			o, err2 := strconv.Atoi(offs[i])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("intervalo inválido para %s", in)
			}
			for j := range cs {
				if cs[j].Input == in {
					cs[j].ChatPeakSec, cs[j].ChatOffSec = p, o
				}
			}
		}
		return cs, nil
	})
	s.renderForm(w, r, "Listo: se guardó. Los cambios se aplican sin reiniciar el monitor.", "No se guardó: ", err)
}

// findSetting busca un canal de la configuración por lo que escribió la persona.
func findSetting(cs []ChannelSetting, in string) int {
	return slices.IndexFunc(cs, func(c ChannelSetting) bool { return c.Input == in })
}

var errNoChannel = errors.New("ese canal ya no está en la configuración (recargá la página)")

// channelsPause es "Pausar" / "Reanudar": inmediato, solo el chat.
func (s *Server) channelsPause(w http.ResponseWriter, r *http.Request) {
	if !s.settingsPost(w, r) {
		return
	}
	in := r.PostForm.Get("objetivo")
	paused := false
	err := s.settings.Update(func(cs []ChannelSetting) ([]ChannelSetting, error) {
		i := findSetting(cs, in)
		if i < 0 {
			return nil, errNoChannel
		}
		cs[i].Paused = !cs[i].Paused
		paused = cs[i].Paused
		return cs, nil
	})
	ok := "Se reanudó el chat de " + in + "."
	if paused {
		ok = "Se pausó el chat de " + in + ": no se lee ni gasta cuota, y sus vivos se siguen revisando."
	}
	s.renderForm(w, r, ok, "No se guardó: ", err)
}

// channelsRemove es "Quitar": inmediato. Lo guardado queda hasta que venza.
func (s *Server) channelsRemove(w http.ResponseWriter, r *http.Request) {
	if !s.settingsPost(w, r) {
		return
	}
	in := r.PostForm.Get("objetivo")
	if list, _ := s.settings.Get(); len(list) == 1 && list[0].Input == in {
		s.render(w, "canales-form", s.formData(s.st.Status(), s.clock(), draftOf(r.PostForm),
			"No se puede quitar el único canal: el monitor necesita al menos uno.", "warn", false))
		return
	}
	err := s.settings.Update(func(cs []ChannelSetting) ([]ChannelSetting, error) {
		i := findSetting(cs, in)
		if i < 0 {
			return nil, errNoChannel
		}
		return slices.Delete(cs, i, i+1), nil
	})
	s.renderForm(w, r, "Se quitó "+in+". Lo guardado queda hasta que venza (30 días).", "No se guardó: ", err)
}

// channelsAdd es "Agregar canal": valida con ParseChannel y lo suma al
// final; lo resuelve el worker (el handler no llama a YouTube). Arranca con
// los intervalos más espaciados para no pasarse de la cuota sin querer.
func (s *Server) channelsAdd(w http.ResponseWriter, r *http.Request) {
	if !s.settingsPost(w, r) {
		return
	}
	raw := strings.TrimSpace(r.PostForm.Get("nuevo"))
	in := ParseChannel(raw)
	var add map[string]any
	var err error
	if in == "" || !reChannelInput.MatchString(in) {
		add = s.addData(raw, "No reconozco el canal «"+raw+"»: pegá el link del canal, el @handle o el ID que empieza con UC.", "warn")
	} else {
		peak, off := peakChoices[len(peakChoices)-1], offChoices[len(offChoices)-1]
		err = s.settings.Update(func(cs []ChannelSetting) ([]ChannelSetting, error) {
			return append(cs, ChannelSetting{Input: in, ChatPeakSec: peak, ChatOffSec: off}), nil
		})
		if err != nil {
			add = s.addData(raw, "No se agregó: "+err.Error()+".", "warn")
		} else {
			add = s.addData("", fmt.Sprintf("Se agregó %s: el monitor lo busca en YouTube en la próxima revisión (1 unidad). El chat arranca cada %s en el pico y cada %s fuera: cambialo abajo si hace falta.",
				in, humanDur(time.Duration(peak)*time.Second), humanDur(time.Duration(off)*time.Second)), "ok")
		}
	}
	s.render(w, "canales-agregar", add)
	s.render(w, "canales-form", s.formData(s.st.Status(), s.clock(), draftOf(r.PostForm), "", "", true))
}

// chatNow es el intervalo que vale en este momento (sin Settings) o si
// ahora es horario pico (con Settings, cada canal tiene los suyos).
func chatNow(cs ChatSettings, now time.Time, perChannel bool) string {
	in := peakHour(now.Local().Hour(), cs.PeakFrom, cs.PeakTo)
	switch {
	case perChannel && in:
		return fmt.Sprintf("Ahora es horario pico (%02d a %02d h).", cs.PeakFrom, cs.PeakTo)
	case perChannel:
		return "Ahora es fuera del horario pico."
	case in:
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
	// Con Settings y la clave cargada: las tandas (ADR 0009). Form solo
	// desde la PC del monitor; si no, Text con los valores.
	Realtime bool
	Form     *ClaudeForm
	Text     string
	State    ClaudeState
}

// ClaudeForm es el formulario de la tarjeta de Claude en Canales.
type ClaudeForm struct {
	Models, Batches           []claudeOpt
	Tope                      string
	Channels                  []claudeCheck
	Summary                   string
	Projection                ClaudeProj
	Notice, NoticeClass, Warn string
}

type claudeOpt struct {
	Value, Label string
	Selected     bool
}

type claudeCheck struct {
	Input, Name string
	On          bool
}

// ClaudeProj es la proyección de gasto al lado del modelo de tiempo real.
type ClaudeProj struct {
	Text string
	Fits bool
}

// ClaudeState son las tres tarjetas de estado de las tandas.
type ClaudeState struct {
	Analyzed, AnalyzedSub string
	Spend, SpendSub       string
	Last, LastSub         string
	Note, NoteClass       string
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
		if st.Tope <= 0 {
			c.Tope = "US$ 0 (apagado: poné un tope para activarlo)"
		}
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

// claudeSetupFull es claudeSetup con las tandas: el formulario (desde la PC
// del monitor) o los valores en texto, y las tarjetas de estado.
func (s *Server) claudeSetupFull(r *http.Request, snap Snapshot, now time.Time) ClaudeSetup {
	c := s.claudeSetup(snap, now)
	if s.settings == nil || s.analyst == nil {
		return c
	}
	c.Realtime = true
	cs, _ := s.settings.Claude()
	if s.editable(r) {
		f := s.claudeForm(snap, now, cs, nil, "", "")
		c.Form = &f
	} else {
		c.Text = fmt.Sprintf("Tiempo real: %s, cada %s, tope US$ %s por día. Resúmenes de cada programa: %s.",
			claudeModelLabel(cs.RealtimeModel), humanDur(cs.every()), usd(cs.BudgetMicros), c.Model)
	}
	c.State = s.claudeState(snap, now)
	return c
}

// claudeModelLabel es el nombre de un modelo para mostrar.
func claudeModelLabel(m string) string {
	if l, ok := map[string]string{"claude-haiku-5-5": "Claude Haiku 5.5", "claude-sonnet-5-5": "Claude Sonnet 5.5",
		"claude-opus-5-5": "Claude Opus 5.5"}[m]; ok {
		return l
	}
	return m
}

// claudeForm arma el formulario con cs; analyze son los canales con el check
// (nil: los de la configuración).
func (s *Server) claudeForm(snap Snapshot, now time.Time, cs ClaudeSettings, analyze map[string]bool, notice, class string) ClaudeForm {
	f := ClaudeForm{Tope: fmt.Sprintf("%.2f", float64(cs.BudgetMicros)/1e6), Notice: notice, NoticeClass: class, Warn: s.settings.ClaudeWarning(),
		Summary: "Resúmenes de cada programa: " + s.summaryModel(snap) + " (se cambia con -claude-modelo)"}
	models := slices.Sorted(maps.Keys(claudePrices))
	// Haiku primero: es el recomendado para tiempo real.
	slices.SortStableFunc(models, func(a, b string) int { return cmpBool(b == defaultRealtimeModel, a == defaultRealtimeModel) })
	for _, m := range models {
		label := claudeModelLabel(m)
		if m == defaultRealtimeModel {
			label += " (rápido, recomendado)"
		}
		f.Models = append(f.Models, claudeOpt{m, label, m == cs.RealtimeModel})
	}
	for _, sec := range batchChoices {
		f.Batches = append(f.Batches, claudeOpt{strconv.Itoa(sec), humanDur(time.Duration(sec) * time.Second), sec == cs.BatchSec})
	}
	names := map[string]string{}
	for _, ch := range append(snap.Channels, snap.Pending...) {
		names[ch.Input] = channelName(ch)
	}
	list, _ := s.settings.Get()
	on := 0
	for _, l := range list {
		check := !l.NoClaude
		if analyze != nil {
			check = analyze[l.Input]
		}
		if check {
			on++
		}
		name := names[l.Input]
		if name == "" {
			name = l.Input
		}
		f.Channels = append(f.Channels, claudeCheck{l.Input, name, check})
	}
	f.Projection = s.claudeProj(now, cs, on)
	return f
}

func cmpBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// summaryModel es el modelo de los resúmenes de cada programa (-claude-modelo).
func (s *Server) summaryModel(snap Snapshot) string {
	if snap.Claude.Model != "" {
		return snap.Claude.Model
	}
	if s.claudeModel != "" {
		return s.claudeModel
	}
	return defaultClaudeModel
}

// claudeProj es la proyección de gasto por día del tiempo real con cs y
// channels canales con el check.
func (s *Server) claudeProj(now time.Time, cs ClaudeSettings, channels int) ClaudeProj {
	label := claudeModelLabel(cs.RealtimeModel)
	if cs.BudgetMicros <= 0 {
		return ClaudeProj{Text: "Con tope US$ 0 no se manda nada a Claude.", Fits: true}
	}
	if channels == 0 {
		return ClaudeProj{Text: "Ningún canal tiene el check: no hay tandas.", Fits: true}
	}
	today := dayStart(now)
	var spent int64
	var first time.Time
	model := ""
	for _, b := range s.st.Batches("", today) {
		if b.At.Before(today) {
			continue
		}
		spent += b.CostMicros
		if first.IsZero() || b.At.Before(first) {
			first = b.At
		}
		model = b.Model
	}
	p := claudeProjection(spent, model, first, now, cs.RealtimeModel, cs.BatchSec, channels, cs.BudgetMicros)
	how := "con el chat de hoy"
	if p.Hypothesis {
		how = "según la estimación del README"
	}
	if p.Fits {
		return ClaudeProj{Text: fmt.Sprintf("Con %s gastaría ~US$ %s por día (%s): entra en la parte del tope para tiempo real (US$ %s).",
			label, usd(p.Daily), how, usd(p.Share)), Fits: true}
	}
	return ClaudeProj{Text: fmt.Sprintf("Con %s gastaría ~US$ %s por día (%s) y la parte del tope es US$ %s: se cortaría a las ~%s.",
		label, usd(p.Daily), how, usd(p.Share), p.Cut.Local().Format("15:04"))}
}

// claudeState son las tarjetas de estado de las tandas de hoy, de los
// canales con el check.
func (s *Server) claudeState(snap Snapshot, now time.Time) ClaudeState {
	st, today := snap.Claude, dayStart(now)
	off := map[string]bool{}
	if s.settings != nil {
		list, _ := s.settings.Get()
		for _, l := range list {
			off[l.Input] = l.NoClaude
		}
	}
	total, labeled := 0, 0
	batched := false
	for _, ch := range snap.Channels {
		if off[ch.Input] {
			continue
		}
		var first time.Time
		for _, b := range s.st.Batches(ch.ID, today) {
			if !b.At.Before(today) && (first.IsZero() || b.From.Before(first)) {
				first = b.From
			}
		}
		if first.IsZero() {
			continue
		}
		batched = true
		ids := liveOf(snap.Videos, ch.ID)
		for _, l := range s.st.Lives(today) {
			if l.ChannelID == ch.ID {
				ids[l.VideoID] = true
			}
		}
		var check []string
		for _, m := range s.st.MessagesOf(ids, first) {
			if m.Source == "chat" && strings.TrimSpace(m.Text) != "" && Classify(m.Text) != CatSpam {
				check = append(check, m.ID)
			}
		}
		total += len(check)
		labeled += len(s.st.Labels(check))
	}
	rt := s.st.RealtimeSpend(today)
	c := ClaudeState{
		Analyzed: miles(labeled), AnalyzedSub: fmt.Sprintf("de %s (%s en la próxima tanda)", miles(total), miles(st.Pending)),
		Spend: "US$ " + usd(st.Spent), SpendSub: fmt.Sprintf("con los tokens que informa la API · tiempo real US$ %s · resúmenes US$ %s · tope US$ %s",
			usd(rt), usd(max(st.Spent-rt, 0)), usd(st.Tope)),
		Last: "Todavía no hubo tandas hoy",
	}
	if !batched {
		c.AnalyzedSub = fmt.Sprintf("(%s en la próxima tanda)", miles(st.Pending))
	}
	if !st.LastBatch.IsZero() && !st.LastBatch.Before(today) {
		c.Last = "hace " + humanDur(max(now.Sub(st.LastBatch), 0))
		c.LastSub = "sin errores"
		if st.LastBatchErr != "" {
			c.LastSub = st.LastBatchErr
		}
	}
	switch {
	case st.Tope <= 0:
		c.Note, c.NoteClass = claudeZeroText, "warn"
	case st.RealtimeCapped:
		c.Note, c.NoteClass = "Se llegó a la parte del tope para tiempo real (US$ "+usd(realtimeShare(st.Tope))+"): se vuelve a las reglas hasta mañana", "warn"
	}
	return c
}

// channelsClaudeState es el estado de las tandas (cada 10 s).
func (s *Server) channelsClaudeState(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil || s.analyst == nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, "canales-claude-estado", s.claudeState(s.st.Status(), s.clock()))
}

// claudeDraft lee el formulario de Claude. ok es false si algún valor no se
// entiende (el tope acepta coma decimal: "2,5").
func claudeDraft(v url.Values) (ClaudeSettings, map[string]bool, bool) {
	sec, err1 := strconv.Atoi(v.Get("tanda"))
	usdVal, err2 := strconv.ParseFloat(strings.Replace(strings.TrimSpace(v.Get("tope")), ",", ".", 1), 64)
	analyze := map[string]bool{}
	for _, in := range v["analizar"] {
		analyze[in] = true
	}
	if err1 != nil || err2 != nil || math.IsNaN(usdVal) || math.IsInf(usdVal, 0) || usdVal < 0 || usdVal > maxClaudeUSD {
		return ClaudeSettings{}, analyze, false
	}
	return ClaudeSettings{RealtimeModel: v.Get("modelo"), BatchSec: sec, BudgetMicros: int64(math.Round(usdVal * 1e6))}, analyze, true
}

// channelsClaudeProj recalcula la proyección con lo elegido, sin guardar.
func (s *Server) channelsClaudeProj(w http.ResponseWriter, r *http.Request) {
	if !s.editable(r) || s.analyst == nil {
		http.Error(w, "Para cambiar la configuración, abrí el dashboard en la PC del monitor.", http.StatusForbidden)
		return
	}
	cs, analyze, ok := claudeDraft(r.URL.Query())
	if !ok || validClaudeSettings(cs) != nil {
		s.render(w, "canales-claude-proy", ClaudeProj{Text: "Revisá los valores: el tope va de 0 a 50 dólares.", Fits: false})
		return
	}
	s.render(w, "canales-claude-proy", s.claudeProj(s.clock(), cs, len(analyze)))
}

// channelsClaude es "Guardar" de la tarjeta de Claude: modelo de tiempo
// real, tandas, tope y qué canales se analizan. El analista lo toma sin
// reiniciar; el handler no llama a Claude ni escribe el Store.
func (s *Server) channelsClaude(w http.ResponseWriter, r *http.Request) {
	if s.analyst == nil {
		http.NotFound(w, r)
		return
	}
	if !s.settingsPostN(w, r, 1<<10) {
		return
	}
	cs, analyze, ok := claudeDraft(r.PostForm)
	if !ok {
		http.Error(w, "El tope tiene que ser un monto entre 0 y 50 dólares.", http.StatusBadRequest)
		return
	}
	if err := s.settings.UpdateClaude(cs, analyze); err != nil {
		http.Error(w, "No se guardó: "+err.Error()+".", http.StatusBadRequest)
		return
	}
	notice := "Listo: se guardó. El análisis toma los cambios en unos segundos, sin reiniciar el monitor."
	if cs.BudgetMicros == 0 {
		notice = "Listo: se guardó con tope US$ 0, así que el análisis con Claude queda apagado."
	}
	s.render(w, "canales-claude-form", s.claudeForm(s.st.Status(), s.clock(), cs, nil, notice, "ok"))
}
