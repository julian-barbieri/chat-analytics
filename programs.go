package main

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Pantalla "Programas de hoy": cada programa (un tramo de ProgramSegments) con
// sus números y el análisis de Claude si lo tiene. No copia todo el histórico:
// las muestras salen de Store.Lives de los días pedidos y los mensajes de
// Store.MessagesOf de los vivos de esos tramos.

// ProgramSeg es un programa de un canal: uno o varios tramos (Parts) del
// mismo programa y del mismo día que se cortaron por una caída de la
// transmisión. Seg va del inicio del primero al fin del último, con el vivo y
// el estado del último; Key es la del primer pedazo (la de la 009), así no
// cambia cuando aparece otro; Closed, por qué terminó el último.
type ProgramSeg struct {
	Channel ChannelStatus
	Seg     Segment
	Parts   []Segment
	Key     string
	Closed  string
}

// pieces son sus pedazos (Seg solo si no los tiene).
func (ps ProgramSeg) pieces() []Segment {
	if len(ps.Parts) == 0 {
		return []Segment{ps.Seg}
	}
	return ps.Parts
}

// HasKey dice si key es la clave de alguno de sus pedazos: un link viejo al
// segundo pedazo sigue llevando al programa unido.
func (ps ProgramSeg) HasKey(key string) bool {
	for _, sg := range ps.pieces() {
		if segmentKey(ps.Channel.ID, sg) == key {
			return true
		}
	}
	return false
}

// videoIDs son los vivos de sus pedazos, sin repetir y en orden.
func (ps ProgramSeg) videoIDs() []string {
	var ids []string
	for _, sg := range ps.pieces() {
		if sg.VideoID != "" && !slices.Contains(ids, sg.VideoID) {
			ids = append(ids, sg.VideoID)
		}
	}
	return ids
}

// programMessages son los mensajes de todos los pedazos del programa, con el
// filtro de segmentMessages en cada uno; un mensaje en el margen de 5 min de
// dos pedazos va una sola vez.
func programMessages(msgs []Message, ps ProgramSeg, titles map[string]string) []Message {
	parts := ps.pieces()
	if len(parts) == 1 {
		return segmentMessages(msgs, parts[0], titles)
	}
	var out []Message
	seen := map[string]bool{}
	for _, sg := range parts {
		for _, m := range segmentMessages(msgs, sg, titles) {
			if !seen[m.ID] {
				seen[m.ID] = true
				out = append(out, m)
			}
		}
	}
	// Los márgenes de dos pedazos de vivos distintos se pisan en el tiempo.
	slices.SortStableFunc(out, func(a, b Message) int { return a.At.Compare(b.At) })
	return out
}

// joins dice si el tramo sg sigue al programa p después de un corte: mismo
// programa, mismo día local y después del fin de su último pedazo. Solo se
// llama con el último programa del canal, así que no hubo otro en el medio.
func joins(p ProgramSeg, sg Segment) bool {
	last := p.Parts[len(p.Parts)-1]
	return sg.Program != "" && strings.EqualFold(sg.Program, last.Program) &&
		dayStart(sg.Start).Equal(dayStart(p.Seg.Start)) && sg.Start.After(last.End)
}

// cutsText es "(se cortó de 08:30 a 08:39)" con los huecos entre pedazos;
// vacío si no hubo corte.
func cutsText(parts []Segment, at func(time.Time) string) string {
	var gaps []string
	for i := 1; i < len(parts); i++ {
		gaps = append(gaps, "de "+at(parts[i-1].End)+" a "+at(parts[i].Start))
	}
	if len(gaps) == 0 {
		return ""
	}
	return " (se cortó " + strings.Join(gaps, " y ") + ")"
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
	case "7d", "30d":
		n := 7
		if dia == "30d" {
			n = 30 // el último mes: lo que entra en la retención de 30 días
		}
		var days []time.Time
		for i := range n {
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
// Los pedazos de un programa cortado se unen antes de mirar si llega a D, así
// uno que se cortó antes de las 00:00 y siguió después es una sola fila con
// la misma clave en Hoy, Ayer y 7 días.
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
			var progs []ProgramSeg
			for i, sg := range segs {
				if sg.State == SegNone {
					continue
				}
				closed := closedWhy(segs, i)
				if past && continuesAfter(samples, ch.ID, sg, to) {
					closed = closedCut // el día se cortó a las 00:00, no el programa
				}
				// Solo con el último programa: si hubo otro en el medio, no se une.
				if n := len(progs); n > 0 && joins(progs[n-1], sg) {
					p := &progs[n-1]
					p.Parts = append(p.Parts, sg)
					p.Seg.End, p.Seg.State, p.Seg.VideoID, p.Closed = sg.End, sg.State, sg.VideoID, closed
					continue
				}
				progs = append(progs, ProgramSeg{Channel: ch, Seg: sg, Parts: []Segment{sg}, Key: segmentKey(ch.ID, sg), Closed: closed})
			}
			for _, ps := range progs {
				if ps.Seg.End.Before(d) || seen[ps.Key] {
					continue
				}
				seen[ps.Key] = true
				mine = append(mine, ps)
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
	Cuts                            int
	OnAir                           bool
	QByClaude                       bool            // Questions cuenta las etiquetas de Claude
	Analysis                        *ClaudeAnalysis // el análisis final de la 009, si hay
	Likes                           int64           // los que ganó el vivo durante el programa
	HasLikes                        bool            // hay al menos dos muestras en algún pedazo
	LikesPer1000                    string          // cada 1.000 espectadores promedio
	Swatch                          string          // su color en el gráfico de likes del día, si se muestra
}

// programRows cuenta los mensajes de cada tramo en una sola pasada. Cada
// mensaje va a su tramo con el mismo criterio que segmentMessages (del vivo,
// con su programa, desde 5 min antes y, si terminó, hasta 5 min después),
// buscando con búsqueda binaria entre los tramos de su vivo: O(M log S).
func programRows(segs []ProgramSeg, msgs []Message, analyses []ClaudeAnalysis, titles map[string]string, labels map[string]MessageLabel) []ProgramRow {
	rows := make([]ProgramRow, len(segs))
	final := map[string]ClaudeAnalysis{}
	for _, a := range analyses {
		if !a.Partial {
			final[a.Key] = a
		}
	}
	// Los mensajes se buscan por pedazo y se suman en la fila del programa.
	type part struct {
		row int
		sg  Segment
	}
	var parts []part
	byVideo := map[string][]int{}
	for i, ps := range segs {
		sg := ps.Seg
		r := ProgramRow{ProgramSeg: ps, Date: sg.Start.Local().Format("02/01"), OnAir: sg.State == SegLive,
			Hours: sg.Start.Local().Format("15:04") + "–", Cuts: len(ps.pieces()) - 1}
		if !r.OnAir {
			r.Hours += sg.End.Local().Format("15:04")
		}
		r.Analysis = finalAnalysis(ps, final)
		rows[i] = r
		for _, pg := range ps.pieces() {
			byVideo[pg.VideoID] = append(byVideo[pg.VideoID], len(parts))
			parts = append(parts, part{i, pg})
		}
	}
	// Por vivo, ordenados por inicio, con el fin más tardío hasta cada uno:
	// así se deja de buscar apenas ningún tramo anterior llega al mensaje.
	type idx struct {
		list   []int
		maxEnd []time.Time // cero: hay uno al aire (sin fin)
	}
	index := map[string]idx{}
	for v, list := range byVideo {
		sort.SliceStable(list, func(i, j int) bool { return parts[list[i]].sg.Start.Before(parts[list[j]].sg.Start) })
		ix := idx{list: list}
		var end time.Time
		open := false
		for _, k := range list {
			sg := parts[k].sg
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
	// Con etiquetas, un tramo con alguna cuenta las preguntas como Categorías.
	labeled, claudeQ := make([]int, len(segs)), make([]int, len(segs))
	var hit []int // filas que ya contaron el mensaje (dos pedazos con margen en común)
	for _, m := range msgs {
		ix, ok := index[m.VideoID]
		if !ok {
			continue
		}
		n := sort.Search(len(ix.list), func(k int) bool { return parts[ix.list[k]].sg.Start.Add(-segmentLead).After(m.At) })
		var prog string
		progDone, ask, askDone := false, false, false
		hit = hit[:0]
		for k := n - 1; k >= 0; k-- {
			if e := ix.maxEnd[k]; !e.IsZero() && m.At.After(e.Add(segmentLead)) {
				break
			}
			p := parts[ix.list[k]]
			i, sg := p.row, p.sg
			if sg.State != SegLive && m.At.After(sg.End.Add(segmentLead)) {
				continue
			}
			if !progDone {
				prog, progDone = programOf(m, titles), true
			}
			if prog != sg.Program || slices.Contains(hit, i) {
				continue
			}
			hit = append(hit, i)
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
			// Como catOf: la categoría de Claude si está etiquetado y, si no
			// (antes de un reinicio, tandas topeadas), la de las reglas.
			if l, ok := labels[m.ID]; ok {
				labeled[i]++
				if l.Cat == CatPregunta {
					claudeQ[i]++
				}
			} else if ask {
				claudeQ[i]++
			}
		}
	}
	for i := range rows {
		rows[i].People = len(people[i])
		if labeled[i] > 0 {
			rows[i].Questions, rows[i].QByClaude = claudeQ[i], true
		}
	}
	return rows
}

// finalAnalysis es el análisis final del último pedazo que tenga uno (el
// analista resume cada pedazo por separado).
func finalAnalysis(ps ProgramSeg, final map[string]ClaudeAnalysis) *ClaudeAnalysis {
	parts := ps.pieces()
	for i := len(parts) - 1; i >= 0; i-- {
		if a, ok := final[segmentKey(ps.Channel.ID, parts[i])]; ok {
			return &a
		}
	}
	return nil
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
	Dia, Canal, Tramo, Comparar string
	// Del detalle (readDetail): lo plegado abierto, la franja elegida y los
	// filtros del chat.
	Abrir, Sent, Desde, Buscar, Cat, Senti, Tema string
	Franja                                       int64
	Pag                                          int
}

func (q programsQuery) values() url.Values {
	v := url.Values{"dia": {q.Dia}}
	if q.Canal != "" {
		v.Set("canal", q.Canal)
	}
	if q.Tramo != "" {
		v.Set("tramo", q.Tramo)
	}
	if q.Comparar != "" && q.Comparar != cmpNone {
		v.Set("comparar", q.Comparar)
	}
	for k, val := range map[string]string{"abrir": q.Abrir, "buscar": q.Buscar, "cat": q.Cat, "senti": q.Senti, "tema": q.Tema, "sent": q.Sent, "desde": q.Desde} {
		if val != "" {
			v.Set(k, val)
		}
	}
	if q.Pag > 0 {
		v.Set("pag", strconv.Itoa(q.Pag))
	}
	if q.Franja > 0 {
		v.Set("franja", strconv.FormatInt(q.Franja, 10))
	}
	return v
}

// URL de la pantalla (para hx-push-url y los links) y del parcial.
func (q programsQuery) Page() string    { return "/programas?" + q.values().Encode() }
func (q programsQuery) Partial() string { return "/parcial/programas?" + q.values().Encode() }

// WithTramo es la misma pantalla con otro programa elegido: lo desplegado,
// la franja y los filtros del chat son del programa anterior y se olvidan.
func (q programsQuery) WithTramo(key string) programsQuery {
	if key == q.Tramo {
		return q
	}
	return programsQuery{Dia: q.Dia, Canal: q.Canal, Tramo: key, Comparar: q.Comparar}
}

// WithComparar es la misma pantalla con otro modo de "Comparar con".
func (q programsQuery) WithComparar(mode string) programsQuery { q.Comparar = mode; return q }

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
	q := programsQuery{Tramo: v.Get("tramo"), Comparar: compareParam(v.Get("comparar"))}
	readDetail(v, &q)
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
		for _, id := range ps.videoIDs() {
			ids[id] = true
		}
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
	// Con Claude, las preguntas de cada fila salen de las tandas: un solo
	// pedido de etiquetas, por el índice por ID del Store.
	var labels map[string]MessageLabel
	if s.claudeActive(snap) && len(msgs) > 0 {
		labels = s.st.Labels(messageIDs(msgs))
	}
	rows := programRows(segs, msgs, analyses, titles, labels)
	// Una hora antes: la última muestra previa es la base del primer programa.
	// Una sola pasada por la serie, no una por vivo.
	likes, viewers := s.st.LikesSince(from.Add(-time.Hour)), map[string][]ViewerSample{}
	for id := range likes {
		if !ids[id] {
			delete(likes, id)
		}
	}
	for id := range ids {
		viewers[id] = s.st.Viewers(id, from)
	}
	programLikes(rows, likes, viewers)
	pick := -1
	for i, r := range rows {
		if q.Tramo != "" && r.HasKey(q.Tramo) {
			pick = i
		}
	}
	// Un programa pedido que ya no está se avisa: no se cae en silencio en otro.
	gone := q.Tramo != "" && pick < 0
	if q.Tramo == "" {
		pick = defaultProgram(rows)
	}
	data := map[string]any{
		"Q": q, "Channels": opts, "Rows": rows, "Multi": len(days) > 1, "Refresh": q.Dia == "hoy" && !q.isOpen("chat"),
		"Title": programsTitle(q.Dia, days), "Updated": now, "Pick": "",
		"Days": []ChannelOption{{"hoy", "Hoy", q.Dia == "hoy"}, {"ayer", "Ayer", q.Dia == "ayer"}, {"7d", "Últimos 7 días", q.Dia == "7d"}, {"30d", "Últimos 30 días", q.Dia == "30d"}},
	}
	// El gráfico de likes del día: con un solo canal y un solo día.
	if len(chans) == 1 && len(days) == 1 && len(rows) > 0 {
		data["LikesChart"] = likesDayChart(rows, likes, days[0], now)
		for i := range rows {
			rows[i].Swatch = swatch(i)
		}
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
		// El refresco de 60 s no arrastra una franja que no es del tramo.
		if !validSlot(q.Franja, rows[pick].Seg, now) {
			q.Franja, q.Desde, q.Sent = 0, "", ""
			data["Q"] = q
		}
		data["Pick"] = rows[pick].Key
		data["Detail"] = s.programDetail(rows[pick].ProgramSeg, programMessages(msgs, rows[pick].ProgramSeg, titles), rows[pick].Analysis, snap, now, q)
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
	case "7d", "30d":
		return fmt.Sprintf("Programas de los últimos %d días · del %s al %s", len(days), days[len(days)-1].Format("02/01"), d.Format("02/01"))
	}
	return "Programas de hoy · " + day
}

// ProgramDetail es el detalle de un programa, listo para mostrar.
type ProgramDetail struct {
	Found                bool
	Title, Line          string
	CSV, Href, Hint      string
	Claude               *ClaudeBlock // el análisis final, si hay y no tuvo error
	ClaudeErr, NoSummary string
	ClaudeFor            string // con cortes: qué parte resume Claude
	Questions            []Message
	Talk                 TalkView // "De qué se habla", la misma tarjeta que En vivo
	// Con Claude y etiquetas en el tramo: los grupos de preguntas de las
	// tandas y las categorías de Claude (programClaudeCards).
	QClaude, CatsClaude bool
	QHeader             string
	QGroups             []QGRow // los qgTop primeros, a la vista
	QRest               []QGRow // el resto, plegado en "Ver N más"
	QMore               Fold    // el plegado del resto (maspreguntas)
	Cats                []CatRow
	Gauge               CatGauge
	CatsPending         int
	Supers              []SuperRow
	SupersNote          string
	SuperMsgs           []ChatLine // cada pago, con los tags del chat del programa
	// Sentimiento de las tandas de Claude: una fila por franja de 5 min y los
	// motivos de todo el programa. Sin Claude, Sent va en false.
	Sent, Motives bool
	SentRows      []SentRow // de la más nueva a la más vieja
	SentAvg       *SentRow  // todo el tramo, arriba de las franjas
	Neg, Pos      []MotiveRow
	NegN, PosN    int
	// Participación de todo el tramo (el gráfico de "Momentos con más
	// participación"); nil en un tramo sin vivo.
	Part map[string]any
	// History compara el programa con sus emisiones de las últimas 4 semanas;
	// nil en un tramo sin programa.
	History *ProgramHistory
	// Chat es "Chat del programa", al final; Slot, los mensajes de la franja
	// elegida, abajo de Participación (SlotIn "part") o de Sentimiento ("sent").
	Chat   ProgramChat
	Slot   *SlotDetail
	SlotIn string
}

// ProgramHistory son las secciones de Programas que comparan emisiones.
type ProgramHistory struct {
	Loyalty      LoyaltyData
	Sent         []EmissionSent
	SentMedian   string
	SentNote     string // por qué no hay filas, si no hay
	Slots        []SlotStat
	SlotsOK      bool
	Map          MapData
	MapOK        bool
	Emissions    int
	MinEmissions int
}

// SentRow es una fila de "Sentimiento del programa": una franja de 5 min con
// sus % (Low: menos de minChartMsgs, sin color).
type SentRow struct {
	Hour, Label   string
	Pos, Neu, Neg float64
	Low           bool
	Start         time.Time
	Href, ID      string // pide los mensajes de la franja; vacíos en el promedio
	Sel           bool
}

// programDetail arma el detalle de un tramo con sus mensajes ya filtrados.
// El CSV lleva el día pedido para que salga el mismo tramo que la fila (en
// "Ayer", un programa que cruza la medianoche se corta a las 00:00).
func (s *Server) programDetail(ps ProgramSeg, msgs []Message, a *ClaudeAnalysis, snap Snapshot, now time.Time, q programsQuery) ProgramDetail {
	sg := ps.Seg
	// Todos los links del detalle (también "Comparar con") llevan este tramo
	// aunque se haya elegido solo o por la clave de otro pedazo, y una franja
	// que no es del tramo no viaja en ninguno.
	q.Tramo = ps.Key
	if !validSlot(q.Franja, sg, now) {
		q.Franja, q.Desde, q.Sent = 0, "", ""
	}
	csvQ := url.Values{"tramo": {ps.Key}}
	if q.Dia != "" {
		csvQ.Set("dia", q.Dia)
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
	parts := ps.pieces()
	span += cutsText(parts, at)
	people := map[string]bool{}
	for _, m := range msgs {
		people[m.Author] = true
	}
	d.Line = fmt.Sprintf("%s · %s · %s · %s de %s", channelName(ps.Channel), span, ps.Closed,
		plural(len(msgs), "mensaje", "mensajes"), plural(len(people), "persona", "personas"))
	// Desde el primer pedazo del último vivo: un corte en el mismo vivo abre
	// donde empezó el programa; uno con vivo nuevo, donde empezó ese vivo.
	last := parts[len(parts)-1]
	open := last.Start
	if i := slices.IndexFunc(parts, func(p Segment) bool { return p.VideoID == last.VideoID }); i >= 0 {
		open = parts[i].Start
	}
	d.Href, d.Hint = momentLink(snap.Videos, last.VideoID, open, now)
	switch {
	case a != nil && a.Error != "":
		d.ClaudeErr = a.Error
	case a != nil:
		b := claudeBlock(*a, messagesByID(msgs), false)
		d.Claude = &b
		if len(parts) > 1 {
			d.ClaudeFor = "Resume solo de " + at(a.From) + " a " + at(a.To) + ": Claude resume cada parte del programa por separado."
		}
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
	d.Questions = an.Questions
	for _, c := range Supers(msgs, videoTitles(snap.Videos)).ByCurrency {
		d.Supers = append(d.Supers, SuperRow{Currency: currencyLabel(c.Currency), Total: money(c.Micros), N: c.N})
	}
	d.SupersNote = superNote
	var ems []Emission
	var from time.Time
	if sg.Program != "" {
		ems, from = s.programHistoryEms(ps, snap, now)
	}
	// Las franjas van antes que los botones: sin % no hay con qué comparar.
	slots, typical, byPct, pad := s.programSlots(ps, msgs, ems, now)
	if len(slots) > 0 {
		cmp, opts, why := compareSetup(ems, sg.Start, q, byPct)
		d.Part = programParticipation(slots, typical, byPct, pad, snap.Videos, cmp)
		if sg.Program != "" {
			d.Part["Compare"], d.Part["CmpWhy"], d.Part["Summary"] = opts, why, EmissionSummary(ems, sg.Start)
			if cmp != nil {
				d.Part["CmpNote"] = cmp.note
			}
		}
	}
	var labels, sentLabels map[string]MessageLabel
	var groups map[string]BatchGroup
	if s.analyst != nil {
		all := s.st.Labels(messageIDs(msgs))
		sentLabels = all // las pestañas de la franja, como las filas de Sentimiento
		s.programSentiment(&d, ps, msgs, all, now)
		if s.claudeActive(snap) && len(all) > 0 {
			labels = all
			groups = s.programClaudeCards(&d, msgs, labels)
		}
	}
	// Categorías siempre: con las etiquetas de las tandas si hay, si no por reglas.
	if !d.CatsClaude {
		d.Cats = catRows(msgs)
	}
	d.Gauge = programGauge(d.Cats, msgs, labels, sg, q, now)
	programSlotLinks(&d, slots, msgs, sentLabels, q)
	programFoldLists(&d, a, msgs, labels, q)
	programCatQuestions(&d)
	programTalk(&d, msgs, an.Topics, sg.State == SegLive, now)
	var topics []ClaudeGroup
	if d.Claude != nil {
		topics = a.Result.Topics
	}
	d.Chat = programChatSection(msgs, labels, groups, topics, q)
	d.SuperMsgs = programSupers(msgs, labels, groups, topics)
	if sg.Program != "" {
		d.History = s.programHistory(ps, ems, from, now)
	}
	return d
}

// programCatQuestions pone las preguntas en el detalle de Categorías cuando
// la elegida es Pregunta (ya no hay una tarjeta aparte): los grupos de las
// tandas con sus flechas (de programFoldLists), las del resumen de Claude o
// las de reglas.
func programCatQuestions(d *ProgramDetail) {
	if d.Gauge.Sel == nil || d.Gauge.Sel.Key != CatPregunta {
		return
	}
	q := &CatQuestions{Header: d.QHeader, Groups: d.QGroups, Rest: d.QRest, More: d.QMore}
	switch {
	case len(d.QGroups) > 0:
	case d.Claude != nil && len(d.Claude.Questions) > 0:
		q.Summary = d.Claude.Questions
	case d.QClaude:
		q.NotGrouped = true
	default:
		q.Rules = d.Questions
	}
	d.Gauge.Sel.Questions = q
}

// programTalk arma "De qué se habla" del detalle: los temas del resumen de
// Claude (ya con su flecha, de programFoldLists) o por reglas, y las palabras
// del programa; "en alza" solo con el programa al aire.
func programTalk(d *ProgramDetail, msgs []Message, topics []Count, live bool, now time.Time) {
	var claude []ClaudeItem
	if d.Claude != nil {
		claude = d.Claude.Topics
	}
	d.Talk = talkView(msgs, TopWords(msgs, wordsShown), topics, claude, live, now)
	d.Talk.Of = "del programa"
	switch {
	case len(claude) > 0 && d.ClaudeFor != "":
		d.Talk.ClaudeNote = d.ClaudeFor
	case len(claude) > 0:
		d.Talk.ClaudeNote = "Del resumen de Claude cuando terminó el programa."
	case d.CatsClaude:
		d.Talk.RulesNote = "Los temas de Claude salen del resumen cuando termina el programa."
	}
}

// programClaudeCards arma Preguntas y Categorías del detalle con las
// etiquetas de las tandas (ADR 0009, 0010): los grupos de preguntas, como en
// En vivo pero sin marcar respondidas, y las categorías de catRowsWith.
func (s *Server) programClaudeCards(d *ProgramDetail, msgs []Message, labels map[string]MessageLabel) map[string]BatchGroup {
	var gids []string
	for _, l := range labels {
		if l.Group != "" {
			gids = append(gids, l.Group)
		}
	}
	groups := s.st.Groups(gids)
	rows, n := questionGroups(msgs, labels, groups, s.answers.Answered)
	d.QClaude = true
	d.QHeader = plural(n, "pregunta", "preguntas") + " · " + plural(len(rows), "grupo", "grupos")
	for _, r := range rows {
		d.QGroups = append(d.QGroups, QGRow{QGroupRow: r, Count: miles(r.N)})
	}
	d.Cats, d.CatsPending = catRowsWith(msgs, labels)
	d.CatsClaude = true
	return groups
}

// programGauge es el semicírculo de Categorías del programa, con el estado en
// "abrir" y las franjas de 15 min del tramo (16 como mucho, hasta el final).
func programGauge(rows []CatRow, msgs []Message, labels map[string]MessageLabel, sg Segment, q programsQuery, now time.Time) CatGauge {
	sel, noOther, all := catPick(q.openSet())
	link := func(sel string, noOther, all bool) string {
		q.Abrir = foldsValue(catOpen(q.openSet(), sel, noOther, all))
		return q.Partial()
	}
	end := sg.End
	if sg.State == SegLive || end.After(now) {
		end = now
	}
	n := min(16, max(1, int((end.Sub(sg.Start)+15*time.Minute-1)/(15*time.Minute))))
	return catGauge(catGaugeIn{rows: rows, msgs: msgs, labels: labels, sel: sel, prefer: CatPregunta, noOther: noOther, all: all, link: link,
		now: now, end: end, step: 15 * time.Minute, n: n, stepLabel: "Cada 15 min", target: "#programas", sync: "#programas:replace"})
}

// CompareOption es un botón de "Comparar con". Off: no hay con qué comparar.
type CompareOption struct {
	Label, Page, Partial string
	On, Off              bool
}

// compareModes son los botones de "Comparar con", en orden.
var compareModes = []struct{ ID, Label string }{
	{cmpNone, "Ninguna"}, {cmpPrev, "Emisión anterior"}, {cmpWeek, "Mismo día la semana pasada"}, {cmpNormal, "Lo normal (4 semanas)"},
}

// chartCompare es la comparación elegida, con el texto que la explica abajo del título.
type chartCompare struct {
	*ChartCompare
	note string
}

// compareSetup arma los botones de "Comparar con" y la comparación del modo
// pedido en q. Un modo sin datos queda deshabilitado (con su motivo en why)
// y el gráfico sale sin comparar. Sin byPct (el gráfico va por personas
// porque falta el dato de cuántos miraban) no se dibuja ninguna comparación:
// todos los modos quedan deshabilitados con un solo motivo.
func compareSetup(ems []Emission, ref time.Time, q programsQuery, byPct bool) (*chartCompare, []CompareOption, string) {
	if !byPct {
		var opts []CompareOption
		for _, m := range compareModes {
			opts = append(opts, CompareOption{Label: m.Label, Page: q.WithComparar(m.ID).Page(), Partial: q.WithComparar(m.ID).Partial(),
				On: m.ID == cmpNone, Off: m.ID != cmpNone})
		}
		return nil, opts, "Sin datos para comparar: las comparaciones son de % de participación y en este programa falta el dato de cuántos miraban."
	}
	band := NormalBand(ems, ref)
	kind := "de lunes a viernes"
	if weekend(ref) {
		kind = "de fin de semana"
	}
	var cmp *chartCompare
	var opts []CompareOption
	var whys []string
	for _, m := range compareModes {
		o := CompareOption{Label: m.Label, Page: q.WithComparar(m.ID).Page(), Partial: q.WithComparar(m.ID).Partial()}
		var c *chartCompare
		switch m.ID {
		case cmpNormal:
			if len(band) == 0 {
				o.Off = true
				whys = append(whys, fmt.Sprintf("%s: hacen falta %d emisiones anteriores %s", m.Label, minEmissions, kind))
				break
			}
			c = &chartCompare{&ChartCompare{Band: band, Label: "lo normal"},
				fmt.Sprintf("Lo normal: la banda va del percentil 25 al 75 de las emisiones anteriores %s a la misma hora (4 semanas) y la línea del medio es la mediana; las barras con borde están por encima de lo normal.", kind)}
		case cmpPrev, cmpWeek:
			e, why := CompareEmission(ems, ref, m.ID)
			if e == nil {
				o.Off = true
				whys = append(whys, m.Label+": "+why)
				break
			}
			line := map[string]float64{}
			for _, s := range e.slots() {
				if s.Viewers > 0 {
					line[clockKey(s.Start)] = s.Pct
				}
			}
			label := dayLabel(e.Day)
			c = &chartCompare{&ChartCompare{Line: line, Label: label}, "La línea violeta: " + label + ", el % de participación a la misma hora."}
		}
		if m.ID == q.Comparar && !o.Off {
			o.On, cmp = true, c
		}
		opts = append(opts, o)
	}
	if cmp == nil {
		opts[0].On = true // sin modo o con uno sin datos, el gráfico de siempre
	}
	why := ""
	if len(whys) > 0 {
		why = "Sin datos para comparar · " + strings.Join(whys, " · ") + "."
	}
	return cmp, opts, why
}

// programHistory junta las emisiones del programa de las últimas 4 semanas
// (loyaltyWindow) y arma sus secciones. Pide al Store solo los mensajes de
// los vivos de esas emisiones (MessagesOf), los espectadores de esos vivos y,
// con Claude, las franjas de sentimiento del canal: nunca Snapshot.
func (s *Server) programHistory(ps ProgramSeg, ems []Emission, from, now time.Time) *ProgramHistory {
	h := &ProgramHistory{Loyalty: Loyalty(ems, now), Emissions: len(ems), MinEmissions: minEmissions}
	switch {
	case s.analyst == nil:
		h.SentNote = "Sale de las tandas de Claude, que están apagadas."
	default:
		var med float64
		h.Sent, med = EmissionSentiment(ems, s.st.SentSlots(ps.Channel.ID, from))
		h.SentMedian = fmt.Sprintf("%.0f %%", med)
		if len(h.Sent) == 0 {
			h.SentNote = "Todavía no hay mensajes de este programa analizados por las tandas de Claude."
		}
	}
	h.Slots, h.SlotsOK = StrongWeakSlots(ems)
	h.Map, h.MapOK = EmissionMap(ems)
	return h
}

// programHistoryEms arma una sola vez las emisiones del programa de las
// últimas 4 semanas, con sus mensajes y espectadores: sirven para la
// historia y para la comparación del gráfico. from es el primer día.
func (s *Server) programHistoryEms(ps ProgramSeg, snap Snapshot, now time.Time) (ems []Emission, from time.Time) {
	var days []time.Time
	for i := range int(loyaltyWindow / (24 * time.Hour)) {
		days = append(days, dayStart(now).AddDate(0, 0, -i))
	}
	from = days[len(days)-1]
	samples := s.st.Lives(from.Add(-12 * time.Hour))
	segs := programSegs([]ChannelStatus{ps.Channel}, samples, snap.Videos, days, now)
	ems = programEmissions(segs, ps.Seg.Program, now)
	ids := map[string]bool{}
	for _, e := range ems {
		for _, id := range e.videoIDs() {
			ids[id] = true
		}
	}
	if len(ids) > 0 {
		fillEmissions(ems, s.st.MessagesOf(ids, from.Add(-segmentLead)), videoTitles(snap.Videos))
		for id := range ids {
			viewers := s.st.Viewers(id, from)
			for i := range ems {
				ems[i].addViewers(id, viewers)
			}
		}
	}
	return ems, from
}

// programEmissions agrupa por día local los tramos con ese programa (sin
// distinguir mayúsculas) que empezaron dentro de la ventana, del más nuevo
// al más viejo. Un título que parpadea parte el tramo, no la emisión.
func programEmissions(segs []ProgramSeg, program string, now time.Time) []Emission {
	var ems []Emission
	for _, ps := range segs {
		for _, sg := range ps.pieces() {
			if !strings.EqualFold(sg.Program, program) || sg.Start.Before(now.Add(-loyaltyWindow)) {
				continue
			}
			if sg.State == SegLive || sg.End.After(now) {
				sg.End = now
			}
			d := dayStart(sg.Start)
			i := slices.IndexFunc(ems, func(e Emission) bool { return e.Day.Equal(d) })
			if i < 0 {
				ems = append(ems, Emission{Day: d, Program: program})
				i = len(ems) - 1
			}
			ems[i].Segs = append(ems[i].Segs, sg)
		}
	}
	sort.SliceStable(ems, func(i, j int) bool { return ems[i].Day.After(ems[j].Day) })
	return ems
}

// fillEmissions reparte los mensajes de chat entre las emisiones con el
// mismo criterio que segmentMessages (del vivo, con su programa, desde 5 min
// antes hasta 5 min después del tramo), en una pasada.
func fillEmissions(ems []Emission, msgs []Message, titles map[string]string) {
	byDay := map[time.Time]int{}
	for i, e := range ems {
		byDay[e.Day] = i
	}
	for _, m := range msgs {
		if m.Source != "chat" {
			continue
		}
		for _, d := range []time.Time{dayStart(m.At), dayStart(m.At.Add(segmentLead)), dayStart(m.At.Add(-segmentLead))} {
			i, ok := byDay[d]
			if !ok || !strings.EqualFold(programOf(m, titles), ems[i].Program) {
				continue
			}
			if slices.ContainsFunc(ems[i].Segs, func(sg Segment) bool {
				return sg.VideoID == m.VideoID && !m.At.Before(sg.Start.Add(-segmentLead)) && !m.At.After(sg.End.Add(segmentLead))
			}) {
				ems[i].Msgs = append(ems[i].Msgs, m)
				break
			}
		}
	}
}

// programSlots son las franjas de 5 min del tramo entero, de su inicio a su
// fin (o a ahora si sigue al aire), con los espectadores del vivo
// (Store.Viewers desde el inicio, no Snapshot). Al aire, pad dice cuántas
// franjas faltan hasta su largo esperado (expectedLength): el gráfico deja ese
// espacio vacío para que cada franja tenga el mismo ancho aunque el programa
// recién empiece. No son franjas: no llevan título ni cuentan en el aria.
func (s *Server) programSlots(ps ProgramSeg, msgs []Message, ems []Emission, now time.Time) (slots []Slot, typical float64, ok bool, pad int) {
	sg, ids := ps.Seg, ps.videoIDs()
	if len(ids) == 0 {
		return nil, 0, false, 0
	}
	end := sg.End
	onAir := sg.State == SegLive || end.After(now)
	if onAir {
		end = now
	}
	// Con un corte, las franjas del hueco quedan vacías: se ve dónde se cayó.
	// Los oyentes de cada vivo, solo durante su pedazo: si el vivo siguió con
	// otro programa, esos no cuentan.
	var viewers []ViewerSample
	for _, pg := range ps.pieces() {
		to := pg.End
		if pg.State == SegLive || to.After(end) {
			to = end
		}
		for _, v := range s.st.Viewers(pg.VideoID, pg.Start) {
			if !v.At.After(to) {
				viewers = append(viewers, v)
			}
		}
	}
	slots, typical, ok = ParticipationRange(msgs, viewers, ids, sg.Start, end)
	if onAir && len(slots) > 0 {
		to := sg.Start.Add(expectedLength(ems, sg.Start))
		for next := slots[len(slots)-1].Start.Add(slotStep); next.Before(to); next = next.Add(slotStep) {
			pad++
		}
	}
	return slots, typical, ok, pad
}

// expectedLength es cuánto suele durar una salida del programa: la mediana,
// entre sus emisiones anteriores (sin la del día de start), del bloque que
// empieza a la hora del reloj más cercana a start. Un programa que sale dos
// veces por día no "dura" de la primera salida a la última. minProgramAxis
// es el mínimo, para que una sola franja no ocupe todo el gráfico. Con menos
// de minEmissions emisiones (como en "Lo normal") no hay largo esperado: una
// sola emisión especial de 9 h dejaría el gráfico casi vacío.
func expectedLength(ems []Emission, start time.Time) time.Duration {
	var spans []float64
	for _, e := range ems {
		if e.Day.Equal(dayStart(start)) {
			continue
		}
		best, bestDist := time.Duration(0), time.Duration(-1)
		for _, r := range runs(e.Segs) {
			if d := clockDistance(r[0], start); bestDist < 0 || d < bestDist {
				best, bestDist = r[1].Sub(r[0]), d
			}
		}
		if best > 0 {
			spans = append(spans, float64(best))
		}
	}
	if len(spans) < minEmissions {
		return minProgramAxis
	}
	return max(time.Duration(median(spans)), minProgramAxis)
}

// runs junta los tramos seguidos (huecos de hasta segmentGap, como un título
// que parpadea) en bloques [inicio, fin], ordenados por inicio.
func runs(segs []Segment) [][2]time.Time {
	segs = slices.Clone(segs)
	sort.Slice(segs, func(i, j int) bool { return segs[i].Start.Before(segs[j].Start) })
	var out [][2]time.Time
	for _, sg := range segs {
		if n := len(out); n > 0 && !sg.Start.After(out[n-1][1].Add(segmentGap)) {
			if sg.End.After(out[n-1][1]) {
				out[n-1][1] = sg.End
			}
			continue
		}
		out = append(out, [2]time.Time{sg.Start, sg.End})
	}
	return out
}

// clockDistance es cuánto separa la hora del reloj local de a y b, sin
// importar el día (a las 23:50 y a las 00:10 hay 20 min).
func clockDistance(a, b time.Time) time.Duration {
	d := a.Sub(dayStart(a)) - b.Sub(dayStart(b))
	if d < 0 {
		d = -d
	}
	return min(d, 24*time.Hour-d)
}

// slotPx es el ancho de cada franja de 5 min en el gráfico de Programas.
const slotPx = 28

// minProgramAxis es el largo mínimo del eje de un programa al aire.
const minProgramAxis = time.Hour

// programParticipation arma el gráfico de participación con las franjas de
// programSlots y, con pad, deja a la derecha (después de ahora) el espacio
// hasta el fin esperado: agranda el viewBox (VW) en vez de sumar franjas
// falsas, así las barras, las líneas y el aria quedan como están y solo se
// reubican los rótulos y las marcas.
func programParticipation(slots []Slot, typical float64, ok bool, pad int, videos []Video, cmp *chartCompare) map[string]any {
	titles := videoTitles(videos)
	var c *ChartCompare
	if cmp != nil {
		c = cmp.ChartCompare
	}
	chart := participationChartCmp(slots, TopMoments(slots, titles), typical, ok, titles, c)
	if pad > 0 {
		padChart(chart, pad, slots[0].Start.Add(time.Duration(len(slots)+pad)*slotStep))
	}
	// Ancho fijo por franja (con las vacías del pad): con pocas franjas no
	// quedan barras anchísimas y dos programas se comparan a simple vista.
	return map[string]any{"Chart": chart, "ByPct": ok, "Typical": pct(typical), "Px": (len(slots) + pad) * slotPx}
}

// padChart agrega pad posiciones vacías a la derecha del gráfico, con el
// rótulo del fin esperado en el borde.
func padChart(chart map[string]any, pad int, end time.Time) {
	w := chart["W"].(int)
	total := float64(w + pad)
	move := func(left float64) float64 { return left / 100 * float64(w) / total * 100 }
	marks := chart["Marks"].([]Mark)
	for i := range marks {
		marks[i].Left = move(marks[i].Left)
	}
	var ticks []Tick
	for _, t := range chart["Ticks"].([]Tick) {
		t.Left, t.End = move(t.Left), false
		ticks = append(ticks, t)
	}
	ticks = append(ticks, Tick{Left: 100, Label: end.Local().Format("15:04"), End: true})
	chart["Ticks"], chart["VW"] = ticks, w+pad
	if aria, has := chart["Aria"].(string); has {
		chart["Aria"] = aria + "; el eje llega hasta las " + end.Local().Format("15:04") + ", lo que suele durar el programa"
	}
}

// programSentiment suma al detalle las franjas de sentimiento del tramo
// (Store.SentSlots, ya sumadas al guardar) y los motivos de las tandas que
// tocan el tramo, contando solo los mensajes del tramo. No pide nada a Claude.
func (s *Server) programSentiment(d *ProgramDetail, ps ProgramSeg, msgs []Message, labels map[string]MessageLabel, now time.Time) {
	sg := ps.Seg
	end := sg.End
	if sg.State == SegLive || end.After(now) {
		end = now
	}
	d.Sent = true
	var all SentSlot
	for _, sl := range s.st.SentSlots(ps.Channel.ID, sg.Start) {
		if !strings.EqualFold(sl.Program, sg.Program) || sl.Start.After(end) || sl.Total() == 0 {
			continue
		}
		b := sentStack(sl)
		p := sentPct(sl)
		d.SentRows = append(d.SentRows, SentRow{Hour: sl.Start.Local().Format("15:04"), Label: b.Label, Low: b.Low, Pos: p.Pos, Neu: p.Neu, Neg: p.Neg, Start: sl.Start})
		all.Pos, all.Neu, all.Neg = all.Pos+sl.Pos, all.Neu+sl.Neu, all.Neg+sl.Neg
	}
	// Lo más nuevo arriba, como los gráficos; el promedio suma los conteos de
	// todas las franjas (una franja de pocos mensajes no pesa como una llena).
	slices.Reverse(d.SentRows)
	if all.Total() > 0 {
		p := sentPct(all)
		d.SentAvg = &SentRow{Hour: "Promedio", Pos: p.Pos, Neu: p.Neu, Neg: p.Neg, Low: all.Total() < minChartMsgs,
			Label: fmt.Sprintf("Promedio del programa: %.0f %% positivo, %.0f %% neutro, %.0f %% negativo (%s)", p.Pos, p.Neu, p.Neg, plural(all.Total(), "mensaje", "mensajes"))}
	}
	byID := messagesByID(msgs)
	for _, l := range labels {
		switch l.Sentiment {
		case "negativo":
			d.NegN++
		case "positivo":
			d.PosN++
		}
	}
	var batches []ClaudeBatch
	for _, b := range s.st.Batches(ps.Channel.ID, sg.Start) {
		if b.From.After(end) {
			continue
		}
		// Una tanda puede tener mensajes de otro programa: de sus motivos
		// cuentan solo los IDs de este tramo.
		c := b
		c.Motives = nil
		for _, mo := range b.Motives {
			var ids []string
			for _, id := range mo.IDs {
				if _, ok := byID[id]; ok {
					ids = append(ids, id)
				}
			}
			if len(ids) > 0 {
				mo.IDs = ids
				c.Motives = append(c.Motives, mo)
			}
		}
		batches = append(batches, c)
	}
	d.Neg = Motives(batches, "negativo", d.NegN, byID, sg.Start)
	d.Pos = Motives(batches, "positivo", d.PosN, byID, sg.Start)
	d.Motives = len(labels) > 0
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
			if ps.HasKey(key) {
				ids := map[string]bool{}
				for _, id := range ps.videoIDs() {
					ids[id] = true
				}
				msgs := s.st.MessagesOf(ids, ps.Seg.Start.Add(-segmentLead))
				return ps, programMessages(msgs, ps, videoTitles(snap.Videos)), true
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
	finals := map[string]ClaudeAnalysis{}
	for _, a := range s.st.Analyses(ps.Channel.ID, ps.Seg.Start) {
		if !a.Partial {
			finals[a.Key] = a
		}
	}
	final := finalAnalysis(ps, finals)
	pq := programsQuery{Dia: dia, Comparar: compareParam(q.Get("comparar"))}
	readDetail(q, &pq)
	s.render(w, "programa-detalle", s.programDetail(ps, msgs, final, snap, now, pq))
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

// Modos de "Comparar con" del gráfico de participación (?comparar=, lista cerrada).
const (
	cmpNone   = "ninguna"
	cmpPrev   = "anterior"
	cmpWeek   = "semana"
	cmpNormal = "normal"
)

// compareParam valida ?comparar=: un valor fuera de la lista es "ninguna".
func compareParam(v string) string {
	for _, m := range compareModes {
		if m.ID == v {
			return v
		}
	}
	return cmpNone
}

// inSegment dice si t cae en el tramo (al aire, sin fin).
func inSegment(t time.Time, sg Segment) bool {
	return !t.Before(sg.Start) && (sg.State == SegLive || !t.After(sg.End))
}

// segmentLikes son los likes que ganó el vivo durante el tramo: YouTube da un
// solo contador por vivo, así que es la última muestra del tramo menos el
// valor al empezar. Un vivo guarda una muestra solo cuando el contador cambia,
// así que ese valor es la última muestra hasta el inicio (o, si no hay, la
// primera del tramo). Sin muestras dentro del tramo no se sabe.
func segmentLikes(samples []LikeSample, sg Segment) (int64, bool) {
	var base, last LikeSample
	hasBase, n := false, 0
	for _, l := range samples {
		if l.VideoID != sg.VideoID {
			continue
		}
		if !l.At.After(sg.Start) {
			base, hasBase = l, true
		}
		if inSegment(l.At, sg) {
			if !hasBase && n == 0 {
				base = l
			}
			last = l
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	return max(0, last.Likes-base.Likes), true
}

// avgViewers es el promedio de espectadores del vivo durante el tramo.
func avgViewers(vs []ViewerSample, sg Segment) int64 {
	var sum, n int64
	for _, v := range vs {
		if v.VideoID == sg.VideoID && inSegment(v.At, sg) {
			sum, n = sum+v.Viewers, n+1
		}
	}
	if n == 0 {
		return 0
	}
	return sum / n
}

// per1000 es n cada 1.000 vistas (o espectadores), con un decimal; "—" sin datos.
func per1000(n, views int64) string {
	if n <= 0 || views <= 0 {
		return "—"
	}
	return decimal(float64(n) * 1000 / float64(views))
}

// programLikes completa los likes de cada fila con las muestras de sus vivos.
func programLikes(rows []ProgramRow, likes map[string][]LikeSample, viewers map[string][]ViewerSample) {
	for i := range rows {
		r := &rows[i]
		var sum, views, n int64
		for _, pg := range r.pieces() {
			if l, ok := segmentLikes(likes[pg.VideoID], pg); ok {
				sum, r.HasLikes = sum+l, true
			}
			if v := avgViewers(viewers[pg.VideoID], pg); v > 0 {
				views, n = views+v, n+1
			}
		}
		r.Likes = sum
		if n > 0 {
			r.LikesPer1000 = per1000(sum, views/n)
		} else {
			r.LikesPer1000 = "—"
		}
	}
}

// swatch es la clase del color de la fila i en el gráfico de likes (se repiten de a 5).
func swatch(i int) string { return fmt.Sprintf("s%d", i%5+1) }

// likesSlot: el gráfico de likes del día va en franjas de 30 min.
const likesSlot = 30 * time.Minute

// likesDayChart son los likes que ganaron los vivos de las filas en cada
// franja de 30 min del día, con el color del programa al aire (s1–s5).
func likesDayChart(rows []ProgramRow, likes map[string][]LikeSample, day, now time.Time) CommentChart {
	n := int(24 * time.Hour / likesSlot)
	ns, labels, classes := make([]int, n), make([]string, n), make([]string, n)
	seen := map[string]bool{}
	for _, r := range rows {
		for _, pg := range r.pieces() {
			if seen[pg.VideoID] {
				continue
			}
			seen[pg.VideoID] = true
			ls := likes[pg.VideoID]
			for i := 1; i < len(ls); i++ {
				k := int(ls[i].At.Sub(day) / likesSlot)
				if d := ls[i].Likes - ls[i-1].Likes; d > 0 && k >= 0 && k < n && ls[i].At.After(day) {
					ns[k] += int(d)
				}
			}
		}
	}
	for k := range n {
		t := day.Add(time.Duration(k) * likesSlot)
		labels[k] = t.Local().Format("15:04") + "–" + t.Add(likesSlot).Local().Format("15:04")
		for i, r := range rows {
			for _, pg := range r.pieces() {
				if inSegment(t, pg) && t.Before(now) {
					classes[k] = swatch(i)
					labels[k] += " · " + programLabel(r.Seg.Program)
				}
			}
		}
	}
	c := barChart(ns, labels, classes, "like", "likes")
	for h := 0; h < 24; h += 6 {
		c.Ticks = append(c.Ticks, Tick{Left: float64(h) * 100 / 24, Label: fmt.Sprintf("%02d:00", h)})
	}
	total := 0
	for _, x := range ns {
		total += x
	}
	c.Aria = "Likes del vivo cada 30 minutos del día: " + miles(total) + " en total"
	return c
}
