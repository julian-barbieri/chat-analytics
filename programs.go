package main

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
)

// Pantalla "Programas de hoy": cada programa (un tramo de ProgramSegments) con
// sus números y el análisis de Claude si lo tiene. No copia todo el histórico:
// las muestras salen de Store.Lives de los días pedidos y los mensajes de
// Store.MessagesOf de los vivos de esos tramos.

// ProgramSeg es un tramo de un canal con su clave (la de la 009) y por qué terminó.
type ProgramSeg struct {
	Channel ChannelStatus
	Seg     Segment
	Key     string
	Closed  string
}

const (
	closedTitle = "se cerró cuando cambió el título"
	closedEnded = "terminó el vivo"
	closedCut   = "sigue después de las 00:00"
)

var weekdays = [...]string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}

// dayRange traduce ?dia= a los días pedidos (00:00 locales, del más nuevo al
// más viejo). Un valor desconocido vale "hoy".
func dayRange(dia string, now time.Time) (string, []time.Time) {
	today := dayStart(now)
	switch dia {
	case "ayer":
		return dia, []time.Time{today.AddDate(0, 0, -1)}
	case "7d":
		var days []time.Time
		for i := range 7 {
			days = append(days, today.AddDate(0, 0, -i))
		}
		return dia, days
	}
	return "hoy", []time.Time{today}
}

// programSegs arma los tramos de los días pedidos, canal por canal en el
// orden de -canal y del más nuevo al más viejo. El día D son los tramos de
// ProgramSegments desde dayStart(D) − 12 h hasta el fin del día (o ahora)
// que terminan en D o después: para hoy es lo mismo que segmenta la 009, así
// las claves coinciden. Un programa que cruza la medianoche aparece una vez.
// Solo hoy puede haber algo al aire: un día anterior se corta a las 00:00.
func programSegs(channels []ChannelStatus, samples []LiveSample, videos []Video, days []time.Time, now time.Time) []ProgramSeg {
	var out []ProgramSeg
	for _, ch := range channels {
		seen := map[string]bool{}
		var mine []ProgramSeg
		for _, d := range days {
			to, live, past := now, liveOf(videos, ch.ID), false
			if next := d.AddDate(0, 0, 1); next.Before(now) {
				to, live, past = next, nil, true
			}
			segs := ProgramSegments(samples, ch.ID, live, d.Add(-12*time.Hour), to)
			for i, sg := range segs {
				if sg.State == SegNone || sg.End.Before(d) {
					continue
				}
				key := segmentKey(ch.ID, sg)
				if seen[key] {
					continue
				}
				seen[key] = true
				closed := closedWhy(segs, i)
				if past && continuesAfter(samples, ch.ID, sg, to) {
					closed = closedCut // el día se cortó a las 00:00, no el programa
				}
				mine = append(mine, ProgramSeg{Channel: ch, Seg: sg, Key: key, Closed: closed})
			}
		}
		sort.SliceStable(mine, func(i, j int) bool { return mine[i].Seg.Start.After(mine[j].Seg.Start) })
		out = append(out, mine...)
	}
	return out
}

// closedWhy dice por qué terminó el tramo i: si después vino otro programa
// del mismo vivo, cambió el título; si no, terminó (o se cortó) el vivo.
func closedWhy(segs []Segment, i int) string {
	if segs[i].State == SegLive {
		return SegLive
	}
	for _, sg := range segs[i+1:] {
		if sg.VideoID == segs[i].VideoID && sg.State != SegNone {
			if sg.Program != segs[i].Program {
				return closedTitle
			}
			break
		}
	}
	return closedEnded
}

// continuesAfter dice si el programa de un tramo cortado en to (las 00:00 de
// un día anterior) siguió en el mismo vivo después del corte.
func continuesAfter(samples []LiveSample, channelID string, sg Segment, to time.Time) bool {
	for _, s := range samples {
		if s.ChannelID == channelID && s.VideoID == sg.VideoID && s.At.After(to) && s.At.Sub(sg.End) <= segmentGap &&
			ProgramName(s.Title, s.Channel) == sg.Program {
			return true
		}
	}
	return false
}

// ProgramRow es una fila de la tabla de Programas.
type ProgramRow struct {
	ProgramSeg
	Date, Hours                     string
	Msgs, People, Supers, Questions int
	OnAir                           bool
	Analysis                        *ClaudeAnalysis // el análisis final de la 009, si hay
}

// programRows cuenta los mensajes de cada tramo en una sola pasada. Cada
// mensaje va a su tramo con el mismo criterio que segmentMessages (del vivo,
// con su programa, desde 5 min antes y, si terminó, hasta 5 min después),
// buscando con búsqueda binaria entre los tramos de su vivo: O(M log S).
func programRows(segs []ProgramSeg, msgs []Message, analyses []ClaudeAnalysis, titles map[string]string) []ProgramRow {
	rows := make([]ProgramRow, len(segs))
	final := map[string]ClaudeAnalysis{}
	for _, a := range analyses {
		if !a.Partial {
			final[a.Key] = a
		}
	}
	byVideo := map[string][]int{}
	for i, ps := range segs {
		sg := ps.Seg
		r := ProgramRow{ProgramSeg: ps, Date: sg.Start.Local().Format("02/01"), OnAir: sg.State == SegLive,
			Hours: sg.Start.Local().Format("15:04") + "–"}
		if !r.OnAir {
			r.Hours += sg.End.Local().Format("15:04")
		}
		if a, ok := final[ps.Key]; ok {
			r.Analysis = &a
		}
		rows[i] = r
		byVideo[sg.VideoID] = append(byVideo[sg.VideoID], i)
	}
	// Por vivo, ordenados por inicio, con el fin más tardío hasta cada uno:
	// así se deja de buscar apenas ningún tramo anterior llega al mensaje.
	type idx struct {
		list   []int
		maxEnd []time.Time // cero: hay uno al aire (sin fin)
	}
	index := map[string]idx{}
	for v, list := range byVideo {
		sort.SliceStable(list, func(i, j int) bool { return segs[list[i]].Seg.Start.Before(segs[list[j]].Seg.Start) })
		ix := idx{list: list}
		var end time.Time
		open := false
		for _, k := range list {
			sg := segs[k].Seg
			open = open || sg.State == SegLive
			if sg.End.After(end) {
				end = sg.End
			}
			if open {
				ix.maxEnd = append(ix.maxEnd, time.Time{})
			} else {
				ix.maxEnd = append(ix.maxEnd, end)
			}
		}
		index[v] = ix
	}
	people := make([]map[string]bool, len(segs))
	for _, m := range msgs {
		ix, ok := index[m.VideoID]
		if !ok {
			continue
		}
		n := sort.Search(len(ix.list), func(k int) bool { return segs[ix.list[k]].Seg.Start.Add(-segmentLead).After(m.At) })
		var prog string
		progDone, ask, askDone := false, false, false
		for k := n - 1; k >= 0; k-- {
			if e := ix.maxEnd[k]; !e.IsZero() && m.At.After(e.Add(segmentLead)) {
				break
			}
			i := ix.list[k]
			sg := segs[i].Seg
			if sg.State != SegLive && m.At.After(sg.End.Add(segmentLead)) {
				continue
			}
			if !progDone {
				prog, progDone = programOf(m, titles), true
			}
			if prog != sg.Program {
				continue
			}
			r := &rows[i]
			r.Msgs++
			if people[i] == nil {
				people[i] = map[string]bool{}
			}
			people[i][m.Author] = true
			if m.Super != "" {
				r.Supers++
			}
			if !askDone {
				ask, askDone = mayAsk(m.Text) && Classify(m.Text) == CatPregunta, true
			}
			if ask {
				r.Questions++
			}
		}
	}
	for i := range rows {
		rows[i].People = len(people[i])
	}
	return rows
}

// mayAsk descarta rápido lo que Classify nunca marca como pregunta (sin "?",
// sin "¿" al principio y sin las palabras de preguntaQs), así no se corren
// sus regex de spam en todos los mensajes de 7 días.
func mayAsk(text string) bool {
	if strings.Contains(text, "?") {
		return true
	}
	t := strings.ToLower(text)
	return strings.HasPrefix(t, "¿") || hasPrefixAny(t, preguntaQs)
}

// programsQuery son los filtros de la pantalla, ya validados.
type programsQuery struct {
	Dia, Canal, Tramo string
}

func (q programsQuery) values() url.Values {
	v := url.Values{"dia": {q.Dia}}
	if q.Canal != "" {
		v.Set("canal", q.Canal)
	}
	if q.Tramo != "" {
		v.Set("tramo", q.Tramo)
	}
	return v
}

// URL de la pantalla (para hx-push-url y los links) y del parcial.
func (q programsQuery) Page() string    { return "/programas?" + q.values().Encode() }
func (q programsQuery) Partial() string { return "/parcial/programas?" + q.values().Encode() }

// WithTramo es la misma pantalla con otro programa elegido.
func (q programsQuery) WithTramo(key string) programsQuery { q.Tramo = key; return q }

// programChannels son los canales de la configuración y, después, los que
// tienen muestras en datos.vivos.jsonl pero no están configurados (quitados
// desde Canales, también antes de reiniciar, o de un vivo suelto de -vivos):
// sus programas siguen hasta que vencen. No se guarda nada nuevo.
func programChannels(snap Snapshot, samples []LiveSample) []ChannelStatus {
	out := append([]ChannelStatus(nil), snap.Channels...)
	known := func(id string) bool {
		return slices.ContainsFunc(out, func(c ChannelStatus) bool { return c.ID == id })
	}
	// Canales de los vivos que se siguen ahora sin ser de la configuración: los de -vivos.
	loose := map[string]bool{}
	for _, v := range snap.Videos {
		if !known(v.ChannelID) {
			loose[v.ChannelID] = true
		}
	}
	for _, sm := range samples {
		if sm.ChannelID != "" && !known(sm.ChannelID) {
			name := sm.Channel
			if name == "" {
				name = sm.ChannelID
			}
			switch {
			case loose[sm.ChannelID]:
				name += " (vivo suelto)"
			case len(snap.Pending) == 0:
				// Mientras haya canales sin resolver no se sabe si este es uno de ellos.
				name += " (no está en Canales)"
			}
			out = append(out, ChannelStatus{ID: sm.ChannelID, Name: name})
		}
	}
	return out
}

// readPrograms valida ?dia= y ?canal=: un valor desconocido vale el de por
// defecto (hoy, todos los canales).
func readPrograms(v url.Values, channels []ChannelStatus) programsQuery {
	q := programsQuery{Tramo: v.Get("tramo")}
	q.Dia, _ = dayRange(v.Get("dia"), time.Now())
	for _, c := range channels {
		if c.ID == v.Get("canal") && c.ID != "" {
			q.Canal = c.ID
		}
	}
	return q
}

// ChannelOption es una opción del select de canal.
type ChannelOption struct {
	ID, Name string
	Selected bool
}

// programsData arma la tabla y el detalle elegido.
func (s *Server) programsData(v url.Values) map[string]any {
	now := s.clock()
	snap := s.st.Status()
	_, days := dayRange(v.Get("dia"), now)
	oldest := days[len(days)-1].Add(-12 * time.Hour)
	samples := s.st.Lives(oldest)
	all := programChannels(snap, samples)
	q := readPrograms(v, all)
	var chans []ChannelStatus
	opts := []ChannelOption{{Name: "Todos los canales", Selected: q.Canal == ""}}
	for _, c := range all {
		opts = append(opts, ChannelOption{c.ID, channelName(c), c.ID == q.Canal})
		if q.Canal == "" || c.ID == q.Canal {
			chans = append(chans, c)
		}
	}
	segs := programSegs(chans, samples, snap.Videos, days, now)
	ids, from := map[string]bool{}, now
	for _, ps := range segs {
		ids[ps.Seg.VideoID] = true
		if t := ps.Seg.Start.Add(-segmentLead); t.Before(from) {
			from = t
		}
	}
	var msgs []Message
	if len(ids) > 0 {
		msgs = s.st.MessagesOf(ids, from)
	}
	var analyses []ClaudeAnalysis
	for _, c := range chans {
		analyses = append(analyses, s.st.Analyses(c.ID, oldest)...)
	}
	titles := videoTitles(snap.Videos)
	rows := programRows(segs, msgs, analyses, titles)
	pick := -1
	for i, r := range rows {
		if r.Key == q.Tramo {
			pick = i
		}
	}
	// Un programa pedido que ya no está se avisa: no se cae en silencio en otro.
	gone := q.Tramo != "" && pick < 0
	if q.Tramo == "" {
		pick = defaultProgram(rows)
	}
	data := map[string]any{
		"Q": q, "Channels": opts, "Rows": rows, "Multi": len(days) > 1, "Refresh": q.Dia == "hoy",
		"Title": programsTitle(q.Dia, days), "Updated": now, "Pick": "",
		"Days": []ChannelOption{{"hoy", "Hoy", q.Dia == "hoy"}, {"ayer", "Ayer", q.Dia == "ayer"}, {"7d", "Últimos 7 días", q.Dia == "7d"}},
	}
	if len(rows) == 0 {
		data["Empty"] = "Todavía no hay programas guardados para ese día."
		if first := s.st.FirstLive(); !first.IsZero() && first.After(days[len(days)-1]) {
			data["Since"] = "Hay programas guardados desde el " + first.Local().Format("02/01") + "."
		}
	}
	switch {
	case gone:
		data["Detail"] = ProgramDetail{}
	case pick >= 0:
		data["Pick"] = rows[pick].Key
		data["Detail"] = s.programDetail(rows[pick].ProgramSeg, segmentMessages(msgs, rows[pick].Seg, titles), rows[pick].Analysis, snap, now, q.Dia)
	}
	return data
}

// defaultProgram es el programa que se muestra sin tramo pedido: el último
// que terminó o, si no hay, uno al aire. -1 si no hay filas.
func defaultProgram(rows []ProgramRow) int {
	pick := -1
	for i, r := range rows {
		if !r.OnAir && (pick < 0 || rows[pick].OnAir || r.Seg.End.After(rows[pick].Seg.End)) {
			pick = i
		}
		if r.OnAir && pick < 0 {
			pick = i
		}
	}
	return pick
}

func programsTitle(dia string, days []time.Time) string {
	d := days[0]
	day := weekdays[d.Weekday()] + " " + d.Format("02/01")
	switch dia {
	case "ayer":
		return "Programas de ayer · " + day
	case "7d":
		return "Programas de los últimos 7 días · del " + days[len(days)-1].Format("02/01") + " al " + d.Format("02/01")
	}
	return "Programas de hoy · " + day
}

// ProgramDetail es el detalle de un programa, listo para mostrar.
type ProgramDetail struct {
	Found                 bool
	Title, Line           string
	CSV, Href, Hint       string
	Claude                *ClaudeBlock // el análisis final, si hay y no tuvo error
	ClaudeErr, NoSummary  string
	Topics                []CatRow
	Questions             []Message
	Supers                []SuperRow
	SupersNote, SupersMsg string
}

// programDetail arma el detalle de un tramo con sus mensajes ya filtrados.
// El CSV lleva el día pedido para que salga el mismo tramo que la fila (en
// "Ayer", un programa que cruza la medianoche se corta a las 00:00).
func (s *Server) programDetail(ps ProgramSeg, msgs []Message, a *ClaudeAnalysis, snap Snapshot, now time.Time, dia string) ProgramDetail {
	sg := ps.Seg
	csvQ := url.Values{"tramo": {ps.Key}}
	if dia != "" {
		csvQ.Set("dia", dia)
	}
	d := ProgramDetail{Found: true, Title: programLabel(sg.Program), CSV: "/export.csv?" + csvQ.Encode()}
	at := func(t time.Time) string {
		if dayStart(t).Equal(dayStart(now)) {
			return t.Local().Format("15:04")
		}
		return t.Local().Format("02/01 15:04")
	}
	span := at(sg.Start) + " a " + at(sg.End)
	if sg.State == SegLive {
		span = at(sg.Start) + " a ahora"
	}
	people := map[string]bool{}
	for _, m := range msgs {
		people[m.Author] = true
	}
	d.Line = fmt.Sprintf("%s · %s · %s · %s de %s", channelName(ps.Channel), span, ps.Closed,
		plural(len(msgs), "mensaje", "mensajes"), plural(len(people), "persona", "personas"))
	d.Href, d.Hint = momentLink(snap.Videos, sg.VideoID, sg.Start, now)
	switch {
	case a != nil && a.Error != "":
		d.ClaudeErr = a.Error
	case a != nil:
		b := claudeBlock(*a, messagesByID(msgs), false)
		d.Claude = &b
	case s.analyst == nil:
		d.NoSummary = claudeOffText
	case snap.Claude.Off != "":
		d.NoSummary = "El análisis con Claude se apagó hasta reiniciar el monitor. " + snap.Claude.Off
	case snap.Claude.Tope <= 0:
		d.NoSummary = claudeZeroText
	default:
		d.NoSummary = "Todavía no hay resumen: se hace solo cuando termina el programa."
	}
	an := Analyze(msgs)
	d.Topics, d.Questions = bars(an.Topics), an.Questions
	for _, c := range Supers(msgs, videoTitles(snap.Videos)).ByCurrency {
		d.Supers = append(d.Supers, SuperRow{Currency: currencyLabel(c.Currency), Total: money(c.Micros), N: c.N})
	}
	d.SupersNote = superNote
	return d
}

func messagesByID(msgs []Message) map[string]Message {
	out := make(map[string]Message, len(msgs))
	for _, m := range msgs {
		out[m.ID] = m
	}
	return out
}

// findProgram busca un tramo por su clave entre los del día pedido (sin día,
// los de los últimos 7) del canal de la clave, y devuelve sus mensajes.
func (s *Server) findProgram(key, dia string, snap Snapshot, now time.Time) (ProgramSeg, []Message, bool) {
	canal, _, _ := strings.Cut(key, "|")
	if dia == "" {
		dia = "7d"
	}
	_, days := dayRange(dia, now)
	samples := s.st.Lives(days[len(days)-1].Add(-12 * time.Hour))
	for _, ch := range programChannels(snap, samples) {
		if ch.ID != canal || canal == "" {
			continue
		}
		for _, ps := range programSegs([]ChannelStatus{ch}, samples, snap.Videos, days, now) {
			if ps.Key == key {
				msgs := s.st.MessagesOf(map[string]bool{ps.Seg.VideoID: true}, ps.Seg.Start.Add(-segmentLead))
				return ps, segmentMessages(msgs, ps.Seg, videoTitles(snap.Videos)), true
			}
		}
	}
	return ProgramSeg{}, nil, false
}

func (s *Server) programsPage(w http.ResponseWriter, r *http.Request) {
	data := s.programsData(r.URL.Query())
	data["Tab"], data["Channel"] = "programas", s.st.Status().Channel
	s.render(w, "programas.html", data)
}

// programsPartial es la tabla con el detalle. Si lo pidió un select, la URL
// del navegador pasa a la de la pantalla (no la del parcial) para que se
// pueda recargar o compartir.
func (s *Server) programsPartial(w http.ResponseWriter, r *http.Request) {
	data := s.programsData(r.URL.Query())
	if t := r.Header.Get("HX-Trigger"); t == "f-canal" || t == "f-dia" {
		w.Header().Set("HX-Push-Url", data["Q"].(programsQuery).Page())
	}
	s.render(w, "programas-lista", data)
}

// programDetailPartial es el detalle de un programa. Sin tramo, el mismo que
// elige la pantalla (el último que terminó o el al aire).
func (s *Server) programDetailPartial(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("tramo") == "" {
		d, _ := s.programsData(q)["Detail"].(ProgramDetail)
		s.render(w, "programa-detalle", d)
		return
	}
	now := s.clock()
	snap := s.st.Status()
	dia := q.Get("dia")
	if dia != "" {
		dia, _ = dayRange(dia, now)
	}
	ps, msgs, ok := s.findProgram(q.Get("tramo"), dia, snap, now)
	if !ok {
		s.render(w, "programa-detalle", ProgramDetail{})
		return
	}
	var final *ClaudeAnalysis
	for _, a := range s.st.Analyses(ps.Channel.ID, ps.Seg.Start) {
		if a.Key == ps.Key && !a.Partial {
			final = &a
		}
	}
	s.render(w, "programa-detalle", s.programDetail(ps, msgs, final, snap, now, dia))
}

// exportProgram es GET /export.csv?tramo=…&dia=…: los mensajes de un programa
// con el mismo filtro que su análisis y el mismo tramo que su fila.
func (s *Server) exportProgram(w http.ResponseWriter, key, dia string) {
	snap := s.st.Status()
	now := s.clock()
	if dia != "" {
		dia, _ = dayRange(dia, now)
	}
	ps, msgs, ok := s.findProgram(key, dia, snap, now)
	if !ok {
		http.Error(w, "Ese programa ya no está guardado.", http.StatusNotFound)
		return
	}
	titles := videoTitles(snap.Videos)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+csvName(ps.Seg.Program, ps.Seg.Start)+`"`)
	cw := csv.NewWriter(w)
	cw.Write(csvHeader)
	for _, m := range msgs {
		cw.Write(csvRow(m, titles))
	}
	cw.Flush()
}

// csvName es el nombre del CSV de un programa: solo [a-z0-9-] en el programa,
// para que tildes, espacios o comillas no rompan el header.
func csvName(program string, start time.Time) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(unaccent.Replace(strings.ToLower(program))) {
		switch {
		case r == 'ñ':
			r = 'n'
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
		default:
			dash = b.Len() > 0
			continue
		}
		if dash {
			b.WriteByte('-')
			dash = false
		}
		b.WriteRune(r)
	}
	slug := b.String()
	if slug == "" {
		slug = "programa"
	}
	return "mensajes_" + slug + "_" + start.Local().Format("2006-01-02_1504") + ".csv"
}
