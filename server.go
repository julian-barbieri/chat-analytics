package main

import (
	"embed"
	"encoding/csv"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/htmx.min.js
var staticFS embed.FS

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"hora":  func(t time.Time) string { return t.Local().Format("15:04") },
	"fecha": func(t time.Time) string { return t.Local().Format("02/01 15:04") },
	"miles": miles,
}).ParseFS(templatesFS, "templates/*.html"))

var categories = []string{CatPregunta, CatQueja, CatElogio, CatInsulto, CatSpam, CatOtro}

var catLabels = map[string]string{
	CatPregunta: "Preguntas", CatQueja: "Quejas", CatElogio: "Elogios",
	CatInsulto: "Insultos", CatSpam: "Spam", CatOtro: "Otros",
}

type Server struct {
	st     *Store
	budget int
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.page)
	mux.HandleFunc("GET /parcial/resumen", s.summary)
	mux.HandleFunc("GET /parcial/feed", s.feed)
	mux.HandleFunc("GET /parcial/extra", s.extra)
	mux.HandleFunc("GET /parcial/vivo", s.liveSection)
	mux.HandleFunc("GET /parcial/vivo/chat", s.liveChat)
	mux.HandleFunc("GET /parcial/vivo/analisis", s.liveAnalysis)
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

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	snap := s.st.Snapshot()
	s.render(w, "page.html", map[string]any{"Channel": snap.Channel, "Cats": catOptions(), "Vivo": r.URL.Query().Get("vivo")})
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

// LiveRow es un vivo en curso con el estado de su chat.
type LiveRow struct {
	Video
	Note string
}

type CatRow struct {
	Key, Label string
	N          int
	Pct        float64
}

func (s *Server) summary(w http.ResponseWriter, r *http.Request) {
	snap := s.st.Snapshot()
	now := time.Now()
	startDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

	var lives []LiveRow
	for _, v := range snap.Videos {
		if v.LiveStatus == "live" {
			lives = append(lives, LiveRow{v, snap.ChatNotes[v.ID]})
		}
	}

	today := since(snap.Msgs, startDay)
	lastHour := since(snap.Msgs, now.Add(-time.Hour))
	chat5 := 0
	for _, m := range since(snap.Msgs, now.Add(-5*time.Minute)) {
		if m.Source == "chat" {
			chat5++
		}
	}
	counts := map[string]int{}
	for _, m := range today {
		counts[Classify(m.Text)]++
	}

	tiles := []Tile{
		{"Mensajes hoy", miles(len(today)), "comentarios + chat"},
		{"Última hora", miles(len(lastHour)), ""},
		{"Chat últimos 5 min", miles(chat5), ""},
		{"Preguntas hoy", miles(counts[CatPregunta]), "para responder"},
		{"Spam e insultos hoy", miles(counts[CatSpam] + counts[CatInsulto]), "a moderar"},
		{"Cuota usada hoy", fmt.Sprintf("%s / %s", miles(snap.Units), miles(s.budget)), "se reinicia a las 04:00 o 05:00 (hora de Argentina)"},
	}

	maxCat := 1
	for _, c := range categories {
		maxCat = max(maxCat, counts[c])
	}
	var rows []CatRow
	for _, c := range categories {
		rows = append(rows, CatRow{c, catLabels[c], counts[c], float64(counts[c]) * 100 / float64(maxCat)})
	}

	s.render(w, "resumen", map[string]any{
		"Channel":  snap.Channel,
		"Lives":    lives,
		"Tiles":    tiles,
		"Activity": activity(snap.Msgs, now),
		"Cats":     rows,
		"ChatNote": snap.ChatNote,
		"LastErr":  snap.LastErr,
		"Updated":  snap.Updated,
		"Started":  !snap.Updated.IsZero(),
	})
}

// activity arma las barras de mensajes cada 5 minutos en las últimas 3 horas.
func activity(msgs []Message, now time.Time) map[string]any {
	const buckets, width, height, step = 36, 720.0, 140.0, 5 * time.Minute
	end := now.Truncate(step).Add(step)
	start := end.Add(-buckets * step)
	counts := make([]int, buckets)
	for _, m := range msgs {
		if m.At.Before(start) || !m.At.Before(end) {
			continue
		}
		counts[int(m.At.Sub(start)/step)]++
	}
	peak := 1
	for _, c := range counts {
		peak = max(peak, c)
	}
	bw := width / buckets
	var bars []Bar
	total := 0
	for i, c := range counts {
		total += c
		h := float64(c) / float64(peak) * (height - 4)
		if c > 0 {
			h = max(h, 3)
		}
		t := start.Add(time.Duration(i) * step)
		bars = append(bars, Bar{
			X: float64(i)*bw + 1, Y: height - h, W: bw - 2, H: h, N: c,
			Label: fmt.Sprintf("%s a %s: %d mensajes", t.Format("15:04"), t.Add(step).Format("15:04"), c),
		})
	}
	var ticks []map[string]any
	for i := 0; i <= buckets; i += 12 {
		ticks = append(ticks, map[string]any{"X": float64(i) * bw, "Label": start.Add(time.Duration(i) * step).Format("15:04")})
	}
	return map[string]any{"Bars": bars, "W": width, "H": height, "Peak": peak, "Total": total, "Ticks": ticks}
}

type FeedRow struct {
	Message
	Cat, CatLabel, Video string
}

func (s *Server) feed(w http.ResponseWriter, r *http.Request) {
	snap := s.st.Snapshot()
	src, cat := r.URL.Query().Get("fuente"), r.URL.Query().Get("cat")
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	titles := videoTitles(snap.Videos)

	msgs := snap.Msgs
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].At.After(msgs[j].At) })
	var rows []FeedRow
	for _, m := range msgs {
		c := Classify(m.Text)
		if (src != "" && m.Source != src) || (cat != "" && c != cat) ||
			(q != "" && !strings.Contains(strings.ToLower(m.Text+" "+m.Author), q)) {
			continue
		}
		video := programOf(m, titles)
		if video == "" {
			video = titles[m.VideoID]
		}
		rows = append(rows, FeedRow{m, c, catLabels[c], video})
		if len(rows) == 60 {
			break
		}
	}
	s.render(w, "feed", map[string]any{"Rows": rows})
}

// LiveButton es un botón para elegir qué chat en vivo se ve.
type LiveButton struct {
	ID, Label string
	Active    bool
}

// pickLive arma los botones de los vivos en curso y decide cuál se muestra: el
// pedido si sigue en vivo, si no el primero. Solo cambia lo que se ve: el
// worker sigue leyendo todos los chats por turnos.
func pickLive(snap Snapshot, want string) (string, []LiveButton) {
	var lives []Video
	for _, v := range snap.Videos {
		if v.LiveStatus == "live" {
			lives = append(lives, v)
		}
	}
	sel := ""
	for _, v := range lives {
		if v.ID == want {
			sel = want
		}
	}
	if sel == "" && len(lives) > 0 {
		sel = lives[0].ID
	}
	labels := map[string]int{}
	for _, v := range lives {
		labels[LiveLabel(v.Title, v.Channel)]++
	}
	var btns []LiveButton
	for _, v := range lives {
		l := LiveLabel(v.Title, v.Channel)
		if labels[l] > 1 { // dos vivos del mismo canal: se distinguen por el programa
			l += " · " + ProgramName(v.Title, v.Channel)
		}
		btns = append(btns, LiveButton{v.ID, l, v.ID == sel})
	}
	return sel, btns
}

func (s *Server) liveSection(w http.ResponseWriter, r *http.Request) {
	sel, _ := pickLive(s.st.Status(), r.URL.Query().Get("video"))
	s.render(w, "vivo", map[string]any{"Video": sel})
}

func (s *Server) liveChat(w http.ResponseWriter, r *http.Request) {
	snap := s.st.Status()
	want := r.URL.Query().Get("video")
	sel, btns := pickLive(snap, want)
	if sel != want {
		// El vivo elegido terminó o empezó uno: se recarga toda la sección,
		// también el análisis, con el vivo que corresponde.
		w.Header().Set("HX-Retarget", "#vivo")
		w.Header().Set("HX-Reswap", "outerHTML")
		s.render(w, "vivo", map[string]any{"Video": sel})
		return
	}
	var label string
	var paused []string
	for _, b := range btns {
		if b.Active {
			label = b.Label
		} else {
			paused = append(paused, b.Label)
		}
	}
	var rows []FeedRow
	for _, m := range s.st.LastChat(sel, 60) {
		c := Classify(m.Text)
		rows = append(rows, FeedRow{m, c, catLabels[c], ""})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].At.After(rows[j].At) })
	s.render(w, "vivo-chat", map[string]any{
		"Buttons": btns, "Label": label, "Paused": strings.Join(paused, ", "),
		"Rows": rows, "Note": snap.ChatNotes[sel], "ChatNote": snap.ChatNote,
		"Banner": chatBanner(snap.ChatInfo, sel, time.Now()),
	})
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
	id := r.URL.Query().Get("video")
	var video Video
	var label string
	for _, v := range snap.Videos {
		if v.ID == id {
			video, label = v, LiveLabel(v.Title, v.Channel)
		}
	}
	if label == "" {
		s.render(w, "vivo-analisis", nil)
		return
	}
	now := time.Now()
	msgs := sinceFor(snap.Msgs, id, now.Add(-24*time.Hour))
	a := Analyze(msgs)
	var offenders []CatRow
	peak := 1
	for _, o := range a.Offenders {
		peak = max(peak, o.Total())
	}
	for _, o := range a.Offenders {
		offenders = append(offenders, CatRow{
			Key: fmt.Sprintf("%d spam · %d insultos", o.Spam, o.Insultos), Label: o.Author,
			N: o.Total(), Pct: float64(o.Total()) * 100 / float64(peak),
		})
	}
	var filterLine []string
	for _, f := range a.Filters {
		filterLine = append(filterLine, f.Key)
	}
	viewers := s.st.Viewers(id, now.Add(-24*time.Hour))
	s.render(w, "vivo-analisis", map[string]any{
		"Label": label, "A": a, "Topics": bars(a.Topics), "Tech": bars(a.Tech),
		"Offenders": offenders, "FilterLine": strings.Join(filterLine, ", "),
		"Part": participationView(video, msgs, viewers, videoTitles(snap.Videos), now),
	})
}

// MomentRow es una fila de la tabla de los momentos con más participación,
// con los números ya escritos.
type MomentRow struct {
	Moment
	End                             time.Time
	ViewersText, PctText, TopicText string
	Href, Hint                      string
}

// participationView arma la sección de momentos con más participación del vivo.
func participationView(v Video, msgs []Message, viewers []ViewerSample, titles map[string]string, now time.Time) map[string]any {
	slots, typical, ok := Participation(msgs, viewers, v.ID, now)
	moments := TopMoments(slots, titles)
	var rows []MomentRow
	for _, m := range moments {
		row := MomentRow{Moment: m, End: m.Start.Add(slotStep), ViewersText: miles(m.Viewers), PctText: pct(m.Pct)}
		var topics []string
		for _, t := range m.Topics {
			topics = append(topics, t.Key)
		}
		row.TopicText = strings.Join(topics, ", ")
		row.Href, row.Hint = reelLink(v, m.Start, now)
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
		"Chart": participationChart(slots, moments, typical, ok, titles), "Moments": rows, "Why": why,
		"ByPct": ok, "Typical": pct(typical), "Concurrent": v.Concurrent, "Missing": v.Concurrent > 0 && missing,
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
}

// participationChart arma las barras del % de participación cada 5 minutos,
// con los momentos del top marcados y la línea de la participación típica.
// Si no hay ningún dato de espectadores (ok false), muestra personas para no
// quedar vacío. El viewBox es de len(slots) × 100: una unidad por franja.
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
	var ticks []Tick
	for i := 0; i <= len(slots) && len(slots) > 0; i += 36 { // cada 3 h
		ticks = append(ticks, Tick{Left: float64(i) / float64(w) * 100, Label: slots[0].Start.Add(time.Duration(i) * slotStep).Local().Format("15:04")})
	}
	out := map[string]any{"Bars": bars, "W": w, "Marks": marks, "Ticks": ticks, "ByPct": ok}
	if !ok {
		out["Max"] = fmt.Sprintf("máx. %d personas", peak.Authors)
		aria := "Personas que escribieron en el chat cada 5 minutos en las últimas 24 horas; "
		if peak.Authors > 0 {
			aria += fmt.Sprintf("máximo de %d personas a las %s", peak.Authors, peak.Start.Local().Format("15:04"))
		} else {
			aria += "todavía sin mensajes"
		}
		out["Aria"] = aria
		return out
	}
	out["TypicalY"] = 100.0
	if scale > 0 {
		out["TypicalY"] = 100 - typical/scale*100
	}
	out["Max"] = "máx. " + pct(scale)
	aria := "Participación del chat cada 5 minutos en las últimas 24 horas (personas que escribieron sobre los que miraban); "
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
	switch {
	case s.Viewers == 0:
		label += " · sin dato de espectadores"
	case s.Authors < minPersonasTop:
		label += fmt.Sprintf(" · menos de %d personas: no entra en el ranking", minPersonasTop)
	default:
		label += fmt.Sprintf(" · %s mirando (%s)", miles(s.Viewers), pct(s.Pct))
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

// decimal escribe un número con un decimal y coma, como se lee en Argentina.
func decimal(f float64) string {
	return strings.Replace(fmt.Sprintf("%.1f", f), ".", ",", 1)
}

func pct(f float64) string { return decimal(f) + " %" }

func (s *Server) extra(w http.ResponseWriter, r *http.Request) {
	snap := s.st.Snapshot()
	day := since(snap.Msgs, time.Now().Add(-24*time.Hour))
	s.render(w, "extra", map[string]any{
		"Words":    TopWords(day, 20),
		"Authors":  TopAuthors(day, 10),
		"Programs": TopPrograms(day, snap.Videos, 10),
		"Videos":   snap.Videos,
		"Channel":  snap.Channel,
	})
}

func (s *Server) export(w http.ResponseWriter, r *http.Request) {
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

func catOptions() []CatRow {
	var out []CatRow
	for _, c := range categories {
		out = append(out, CatRow{Key: c, Label: catLabels[c]})
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
