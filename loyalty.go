package main

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

// Historia de un programa en Programas: fidelización del chat, emisión por
// emisión, franjas fuertes y débiles y mapa por emisión. Todo son funciones
// puras sobre las emisiones de las últimas 4 semanas (loyaltyWindow), sin
// importar el filtro Día. Las personas se cuentan por nombre visible: es una
// aproximación (alguien puede cambiar de nombre o dos pueden llamarse igual).

// loyaltyWindow es la ventana fija de la historia: entra en la retención de 30 días.
const loyaltyWindow = 28 * 24 * time.Hour

const week = 7 * 24 * time.Hour

// Emission es una emisión de un programa: sus tramos de un mismo día local,
// con sus mensajes de chat y los espectadores de sus vivos.
type Emission struct {
	Day     time.Time
	Program string
	Segs    []Segment
	Msgs    []Message
	Viewers []ViewerSample
}

// span es de dónde a dónde fue la emisión (del primer tramo al último).
func (e Emission) span() (from, to time.Time) {
	for i, sg := range e.Segs {
		if i == 0 || sg.Start.Before(from) {
			from = sg.Start
		}
		if sg.End.After(to) {
			to = sg.End
		}
	}
	return from, to
}

func (e Emission) videoIDs() []string {
	var ids []string
	for _, sg := range e.Segs {
		if sg.VideoID != "" && !slices.Contains(ids, sg.VideoID) {
			ids = append(ids, sg.VideoID)
		}
	}
	return ids
}

// addViewers suma los espectadores de un vivo de la emisión dentro de su horario.
func (e *Emission) addViewers(videoID string, viewers []ViewerSample) {
	if !slices.Contains(e.videoIDs(), videoID) {
		return
	}
	from, to := e.span()
	for _, v := range viewers {
		if !v.At.Before(from) && v.At.Before(to) {
			e.Viewers = append(e.Viewers, v)
		}
	}
}

// slots son las franjas de 5 min de la emisión con su participación.
func (e Emission) slots() []Slot {
	from, to := e.span()
	if from.IsZero() || !to.After(from) {
		return nil
	}
	// El último instante no abre una franja nueva: el tramo de 10:00 a 11:00 son 12 franjas.
	s, _, _ := ParticipationRange(e.Msgs, e.Viewers, e.videoIDs(), from, to.Add(-time.Nanosecond))
	return s
}

// emissionLabel es "Mar 03/11".
func emissionLabel(d time.Time) string {
	w := weekdayShort[d.Weekday()]
	return strings.ToUpper(w[:1]) + w[1:] + " " + d.Format("02/01")
}

// participant dice si un mensaje cuenta para la fidelización: chat con
// autor, sin spam y sin moderadores ni el dueño del canal.
func participant(m Message) bool {
	return m.Source == "chat" && m.Author != "" && m.Role == "" && Classify(m.Text) != CatSpam
}

// LoyaltyData es la sección "Fidelización del chat".
type LoyaltyData struct {
	Emissions, Participants int
	Return                  float64 // % de los que escribieron hace 7 a 14 días que volvieron en los últimos 7
	ReturnOK                bool
	Attendance, Sticky      float64 // emisiones por participante; % únicos por emisión ÷ únicos de la ventana
	CorePeople, CoreMsgs    float64
	AtRisk                  int
	Segments                []LoyaltySegment
	Cohorts                 []Cohort // de la semana más vieja a la más nueva
}

type LoyaltySegment struct {
	Key, Label, Rule   string
	People             int
	PeoplePct, MsgsPct float64
}

// Cohort son los que escribieron por primera vez (en la ventana) en una
// semana y en qué semanas siguientes volvieron.
type Cohort struct {
	Start time.Time
	New   int
	Cells []HeatCell
}

// HeatCell es una celda con intensidad de color (Class de m0 a m4).
type HeatCell struct {
	Text, Class string
	OK          bool
	Val         float64
}

// Cortes de los segmentos por asistencia, en parte de las emisiones: así un
// programa semanal y uno diario se comparan igual.
const habitualShare, coreShare = 0.2, 0.5

// Loyalty calcula la fidelización de las emisiones (cualquier orden) hasta now.
func Loyalty(ems []Emission, now time.Time) LoyaltyData {
	from := now.Add(-loyaltyWindow)
	type person struct {
		ems         map[int]bool
		msgs        int
		first, last time.Time
		weeks       [4]bool
	}
	people := map[string]*person{}
	perEm := make([]map[string]bool, len(ems))
	total := 0
	for i, e := range ems {
		perEm[i] = map[string]bool{}
		for _, m := range e.Msgs {
			if !participant(m) || m.At.Before(from) || m.At.After(now) {
				continue
			}
			p := people[m.Author]
			if p == nil {
				p = &person{ems: map[int]bool{}, first: m.At, last: m.At}
				people[m.Author] = p
			}
			p.ems[i], perEm[i][m.Author] = true, true
			p.msgs++
			total++
			if m.At.Before(p.first) {
				p.first = m.At
			}
			if m.At.After(p.last) {
				p.last = m.At
			}
			p.weeks[weekOf(m.At, now)] = true
		}
	}
	n := len(ems)
	l := LoyaltyData{Emissions: n, Participants: len(people)}
	habit := max(int(math.Ceil(habitualShare*float64(n))), 2)
	core := max(int(math.Ceil(coreShare*float64(n))), habit)
	segs := []LoyaltySegment{
		{Key: "nuevos", Label: "Nuevos / de paso", Rule: "1 vivo"},
		{Key: "ocasionales", Label: "Ocasionales", Rule: vivosRange(2, habit-1)},
		{Key: "habituales", Label: "Habituales", Rule: vivosRange(habit, core-1)},
		{Key: "nucleo", Label: "Núcleo", Rule: fmt.Sprintf("%d+ vivos", core)},
	}
	segMsgs := make([]int, len(segs))
	var attended, prev, back int
	for _, p := range people {
		e := len(p.ems)
		attended += e
		k := 0
		switch {
		case e >= core:
			k = 3
		case e >= habit:
			k = 2
		case e >= 2:
			k = 1
		}
		segs[k].People++
		segMsgs[k] += p.msgs
		if k >= 2 && now.Sub(p.last) >= 14*24*time.Hour {
			l.AtRisk++
		}
		if p.weeks[2] {
			prev++
			if p.weeks[3] {
				back++
			}
		}
	}
	for k := range segs {
		segs[k].PeoplePct = share(segs[k].People, len(people))
		segs[k].MsgsPct = share(segMsgs[k], total)
	}
	l.Segments = segs
	l.CorePeople, l.CoreMsgs = segs[3].PeoplePct, segs[3].MsgsPct
	if len(people) > 0 {
		l.Attendance = float64(attended) / float64(len(people))
		var uniq int
		for _, m := range perEm {
			uniq += len(m)
		}
		if n > 0 {
			l.Sticky = float64(uniq) / float64(n) / float64(len(people)) * 100
		}
	}
	if prev > 0 {
		l.Return, l.ReturnOK = share(back, prev), true
	}
	for w := range 4 {
		c := Cohort{Start: from.Add(time.Duration(w) * week)}
		var members []*person
		for _, p := range people {
			if weekOf(p.first, now) == w {
				members = append(members, p)
			}
		}
		c.New = len(members)
		for j := range 4 {
			if w+j > 3 || c.New == 0 {
				c.Cells = append(c.Cells, HeatCell{Text: "—"})
				continue
			}
			k := 0
			for _, p := range members {
				if p.weeks[w+j] {
					k++
				}
			}
			v := share(k, c.New)
			c.Cells = append(c.Cells, HeatCell{Text: fmt.Sprintf("%.0f %%", v), OK: true, Val: v, Class: heatClass(v, 0, 100)})
		}
		l.Cohorts = append(l.Cohorts, c)
	}
	return l
}

// weekOf es la semana de 7 días de la ventana (0 la más vieja, 3 la que
// termina en now).
func weekOf(t, now time.Time) int {
	return 3 - min(max(int(now.Sub(t)/week), 0), 3)
}

func vivosRange(lo, hi int) string {
	switch {
	case lo > hi:
		return "—"
	case lo == hi:
		return fmt.Sprintf("%d vivos", lo)
	}
	return fmt.Sprintf("%d–%d vivos", lo, hi)
}

func share(part, whole int) float64 {
	if whole == 0 {
		return 0
	}
	return float64(part) * 100 / float64(whole)
}

// heatClass reparte un valor entre lo y hi en cinco clases (m0 a m4).
func heatClass(v, lo, hi float64) string {
	k := 0
	if hi > lo {
		k = min(max(int((v-lo)/(hi-lo)*5), 0), 4)
	}
	return fmt.Sprintf("m%d", k)
}

// EmissionSent es una fila de "Emisión por emisión".
type EmissionSent struct {
	Day                  time.Time
	Label, Mark, NegText string
	Pos, Neu, Neg, Width float64 // % del sentimiento; Width: largo de la barra según los mensajes analizados
	N, Tech              int
}

// minTechMark son las quejas técnicas que tiene que tener una emisión para
// que su marca lo diga.
const minTechMark = 3

// EmissionSentiment suma las franjas de sentimiento de las tandas de Claude
// de cada emisión (las del programa y dentro de su horario). Solo las
// emisiones con mensajes analizados, en el orden de ems, y la mediana de %
// negativo; con 3 o más, una emisión que se aleja 5 pts o más se marca.
func EmissionSentiment(ems []Emission, slots []SentSlot) ([]EmissionSent, float64) {
	var rows []EmissionSent
	peak := 0
	for _, e := range ems {
		var sum SentSlot
		for _, sl := range slots {
			if !strings.EqualFold(sl.Program, e.Program) {
				continue
			}
			for _, sg := range e.Segs {
				if !sl.Start.Before(slotOf(sg.Start)) && sl.Start.Before(sg.End) {
					sum.Pos, sum.Neu, sum.Neg = sum.Pos+sl.Pos, sum.Neu+sl.Neu, sum.Neg+sl.Neg
					break
				}
			}
		}
		if sum.Total() == 0 {
			continue
		}
		tech := 0
		for _, m := range e.Msgs {
			if m.Source == "chat" && TechIssue(m.Text) != "" {
				tech++
			}
		}
		p := sentPct(sum)
		rows = append(rows, EmissionSent{Day: e.Day, Label: emissionLabel(e.Day), Pos: p.Pos, Neu: p.Neu, Neg: p.Neg, N: sum.Total(), Tech: tech,
			NegText: fmt.Sprintf("%.0f %% negativo", p.Neg)})
		peak = max(peak, sum.Total())
	}
	var negs []float64
	for _, r := range rows {
		negs = append(negs, r.Neg)
	}
	if len(negs) == 0 {
		return nil, 0
	}
	med := median(negs)
	for i := range rows {
		r := &rows[i]
		r.Width = float64(r.N) * 100 / float64(peak)
		if len(rows) < minEmissions || math.Abs(r.Neg-med) < 5 {
			continue
		}
		r.Mark = "Menos negativa de lo normal"
		if r.Neg > med {
			r.Mark = "Más negativa de lo normal"
		}
		if r.Tech >= minTechMark {
			r.Mark += " · con quejas técnicas"
		}
	}
	return rows, med
}

// SlotStat es una fila de "Franjas fuertes y débiles": una franja de 5 min
// del reloj comparada entre emisiones.
type SlotStat struct {
	Clock, Range, PctText string
	Strong                bool
	Msgs                  int
	Viewers               int64
	Pct                   float64
	Top, Of               int // en el top 5 de su emisión, de cuántas emisiones la tienen
}

// minEmissions son las emisiones que hacen falta para comparar.
const minEmissions = 3

const strongSlots, weakSlots = 4, 2

// StrongWeakSlots compara las franjas de 5 min por hora del reloj entre las
// emisiones: las que están en al menos la mitad, con la mediana de mensajes,
// el rango normal (p25 a p75), los espectadores y el % de participación
// (medianas), y en cuántas emisiones quedó en el top 5 por %. Devuelve las 4
// más altas (fuertes) y las 2 más bajas (débiles, la más baja primero). Sin
// espectadores ordena por mensajes. false con menos de minEmissions emisiones.
func StrongWeakSlots(ems []Emission) ([]SlotStat, bool) {
	if len(ems) < minEmissions {
		return nil, false
	}
	type acc struct {
		start         time.Time
		msgs          []float64
		viewers, pcts []float64
		top, of       int
	}
	byClock := map[string]*acc{}
	for _, e := range ems {
		slots := e.slots()
		var ranked []Slot
		for _, s := range slots {
			if s.Viewers > 0 {
				ranked = append(ranked, s)
			}
		}
		sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Pct > ranked[j].Pct })
		top := map[string]bool{}
		for _, s := range ranked[:min(5, len(ranked))] {
			top[clockKey(s.Start)] = true
		}
		for _, s := range slots {
			k := clockKey(s.Start)
			a := byClock[k]
			if a == nil {
				a = &acc{start: s.Start}
				byClock[k] = a
			}
			a.of++
			a.msgs = append(a.msgs, float64(s.N))
			if s.Viewers > 0 {
				a.viewers = append(a.viewers, float64(s.Viewers))
				a.pcts = append(a.pcts, s.Pct)
			}
			if top[k] {
				a.top++
			}
		}
	}
	byPct := false
	for _, a := range byClock {
		byPct = byPct || len(a.pcts) > 0
	}
	var rows []SlotStat
	for _, a := range byClock {
		if a.of*2 < len(ems) {
			continue
		}
		r := SlotStat{Clock: clockKey(a.start) + "–" + clockKey(a.start.Add(slotStep)),
			Msgs: int(math.Round(median(a.msgs))), Top: a.top, Of: a.of, PctText: "—",
			Range: miles(int(math.Round(quantile(a.msgs, 0.25)))) + "–" + miles(int(math.Round(quantile(a.msgs, 0.75))))}
		if len(a.pcts) > 0 {
			r.Viewers, r.Pct = int64(math.Round(median(a.viewers))), median(a.pcts)
			r.PctText = pct(r.Pct)
		} else if byPct {
			continue // sin dato de espectadores no se compara con las que tienen
		}
		rows = append(rows, r)
	}
	value := func(r SlotStat) float64 {
		if byPct {
			return r.Pct
		}
		return float64(r.Msgs)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if value(rows[i]) != value(rows[j]) {
			return value(rows[i]) > value(rows[j])
		}
		return rows[i].Clock < rows[j].Clock
	})
	strong := min(strongSlots, len(rows))
	weak := min(weakSlots, len(rows)-strong)
	out := slices.Clone(rows[:strong])
	for i := range out {
		out[i].Strong = true
	}
	tail := slices.Clone(rows[len(rows)-weak:])
	slices.Reverse(tail)
	return append(out, tail...), true
}

func clockKey(t time.Time) string { return t.Local().Format("15:04") }

func minuteOfDay(t time.Time) int {
	l := t.Local()
	return l.Hour()*60 + l.Minute()
}

// quantile interpola entre los valores ordenados (q entre 0 y 1).
func quantile(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	sort.Float64s(s)
	pos := q * float64(len(s)-1)
	lo := int(pos)
	if lo+1 >= len(s) {
		return s[lo]
	}
	return s[lo] + (s[lo+1]-s[lo])*(pos-float64(lo))
}

// MapRow es una fila del mapa por emisión.
type MapRow struct {
	Label string
	Cells []HeatCell
}

// MapData es el "Mapa por emisión": % de participación cada 30 min.
type MapData struct {
	Cols   []string
	Rows   []MapRow
	Median MapRow
}

// maxMapRows son las emisiones que muestra el mapa.
const maxMapRows = 10

const mapBlock = 30 * time.Minute

// EmissionMap arma el mapa de las primeras maxMapRows emisiones de ems (lo
// último primero): cada celda es el promedio del % de sus franjas de 5 min
// con espectadores; la última fila, la mediana de cada columna. La
// intensidad (m0 a m4) va del valor más bajo al más alto del mapa. false si
// no hay ningún dato de espectadores.
func EmissionMap(ems []Emission) (MapData, bool) {
	ems = ems[:min(maxMapRows, len(ems))]
	type cell struct {
		sum float64
		n   int
	}
	cols := map[string]int{} // columna → minuto del día, para ordenar
	grid := make([]map[string]cell, len(ems))
	for i, e := range ems {
		grid[i] = map[string]cell{}
		for _, s := range e.slots() {
			if s.Viewers == 0 {
				continue
			}
			b := s.Start.Truncate(mapBlock)
			k := clockKey(b)
			cols[k] = minuteOfDay(b)
			c := grid[i][k]
			c.sum, c.n = c.sum+s.Pct, c.n+1
			grid[i][k] = c
		}
	}
	if len(cols) == 0 {
		return MapData{}, false
	}
	var m MapData
	for k := range cols {
		m.Cols = append(m.Cols, k)
	}
	sort.Slice(m.Cols, func(i, j int) bool { return cols[m.Cols[i]] < cols[m.Cols[j]] })
	lo, hi := math.Inf(1), math.Inf(-1)
	put := func(v float64) HeatCell {
		lo, hi = min(lo, v), max(hi, v)
		// Dos decimales bajo el 1 %: si no, casi todo el mapa dice 0,1.
		return HeatCell{Text: decimalFine(v), OK: true, Val: v}
	}
	for i, e := range ems {
		r := MapRow{Label: emissionLabel(e.Day)}
		for _, k := range m.Cols {
			if c, ok := grid[i][k]; ok {
				r.Cells = append(r.Cells, put(c.sum/float64(c.n)))
			} else {
				r.Cells = append(r.Cells, HeatCell{Text: "—"})
			}
		}
		m.Rows = append(m.Rows, r)
	}
	m.Median.Label = "Mediana"
	for j := range m.Cols {
		var vs []float64
		for _, r := range m.Rows {
			if r.Cells[j].OK {
				vs = append(vs, r.Cells[j].Val)
			}
		}
		m.Median.Cells = append(m.Median.Cells, put(median(vs)))
	}
	for _, r := range append(m.Rows, m.Median) {
		for j := range r.Cells {
			if r.Cells[j].OK {
				r.Cells[j].Class = heatClass(r.Cells[j].Val, lo, hi)
			}
		}
	}
	return m, true
}

// Band es lo normal de una franja del reloj entre las emisiones anteriores:
// percentiles 25 y 75 y la mediana del % de participación, de N emisiones.
type Band struct {
	P25, Med, P75 float64
	N             int
}

// weekend dice si el día es sábado o domingo: lo normal de un programa de
// lunes a viernes no se mezcla con el del fin de semana.
func weekend(t time.Time) bool {
	w := t.Local().Weekday()
	return w == time.Saturday || w == time.Sunday
}

// priorEmissions son las emisiones de días anteriores al de ref, en el orden de ems.
func priorEmissions(ems []Emission, ref time.Time) []Emission {
	day := dayStart(ref)
	var out []Emission
	for _, e := range ems {
		if e.Day.Before(day) {
			out = append(out, e)
		}
	}
	return out
}

// NormalBand es lo normal de cada franja (por hora del reloj, clockKey) en
// las emisiones anteriores al día de ref y del mismo tipo de día (de lunes a
// viernes o de fin de semana). Una franja con menos de minEmissions
// emisiones con dato de espectadores no entra.
func NormalBand(ems []Emission, ref time.Time) map[string]Band {
	pcts := map[string][]float64{}
	for _, e := range priorEmissions(ems, ref) {
		if weekend(e.Day) != weekend(ref) {
			continue
		}
		for _, s := range e.slots() {
			if s.Viewers > 0 {
				k := clockKey(s.Start)
				pcts[k] = append(pcts[k], s.Pct)
			}
		}
	}
	out := map[string]Band{}
	for k, xs := range pcts {
		if len(xs) >= minEmissions {
			out[k] = Band{P25: quantile(xs, 0.25), Med: median(xs), P75: quantile(xs, 0.75), N: len(xs)}
		}
	}
	return out
}

// CompareEmission es la emisión contra la que se compara: con "anterior", la
// más reciente antes del día de ref (de cualquier día); con "semana", la del
// mismo día 7 días antes. Si no hay, devuelve por qué.
func CompareEmission(ems []Emission, ref time.Time, mode string) (*Emission, string) {
	prior := priorEmissions(ems, ref)
	switch mode {
	case cmpPrev:
		if len(prior) == 0 {
			return nil, "no hay emisiones anteriores en 4 semanas"
		}
		e := slices.MaxFunc(prior, func(a, b Emission) int { return a.Day.Compare(b.Day) })
		return &e, ""
	case cmpWeek:
		d := dayStart(ref).AddDate(0, 0, -7)
		if i := slices.IndexFunc(prior, func(e Emission) bool { return e.Day.Equal(d) }); i >= 0 {
			e := prior[i]
			return &e, ""
		}
		return nil, "no hubo emisión el " + dayLabel(d)
	}
	return nil, ""
}

// dayLabel es "martes 03/11".
func dayLabel(d time.Time) string { return weekdays[d.Weekday()] + " " + d.Format("02/01") }

// emissionPct es la participación de una emisión (la mediana del % de sus
// franjas con dato, como la "Típica" del gráfico) y su pico de espectadores.
func emissionPct(e Emission) (p float64, peak int64, ok bool) {
	var pcts []float64
	for _, s := range e.slots() {
		if s.Viewers > 0 {
			pcts = append(pcts, s.Pct)
			peak = max(peak, s.Viewers)
		}
	}
	if len(pcts) == 0 {
		return 0, 0, false
	}
	return median(pcts), peak, true
}

// EmissionSummary es la línea de arriba del gráfico: la participación de la
// emisión del día de ref, su puesto entre las de 4 semanas (con al menos
// minEmissions con dato) y su pico de espectadores contra el récord. Vacía si
// la emisión no tiene dato de espectadores.
func EmissionSummary(ems []Emission, ref time.Time) string {
	type row struct {
		day  time.Time
		pct  float64
		peak int64
	}
	var rows []row
	cur := -1
	for _, e := range ems {
		p, peak, ok := emissionPct(e)
		if !ok {
			continue
		}
		if e.Day.Equal(dayStart(ref)) {
			cur = len(rows)
		}
		rows = append(rows, row{e.Day, p, peak})
	}
	if cur < 0 {
		return ""
	}
	c := rows[cur]
	parts := []string{"Participación de esta emisión: " + pctFine(c.pct)}
	rank, rec := 1, -1
	for i, r := range rows {
		if i == cur {
			continue
		}
		if r.pct > c.pct {
			rank++
		}
		if rec < 0 || r.peak > rows[rec].peak {
			rec = i
		}
	}
	if len(rows) >= minEmissions {
		parts = append(parts, fmt.Sprintf("%d.ª de %d emisiones en 4 semanas", rank, len(rows)))
	}
	peak := "pico " + miles(c.peak) + " mirando"
	switch {
	case rec < 0:
		parts = append(parts, peak)
	case c.peak >= rows[rec].peak:
		parts = append(parts, peak+", el más alto en 4 semanas")
	default:
		parts = append(parts, peak, "récord "+miles(rows[rec].peak)+", el "+dayLabel(rows[rec].day))
	}
	return strings.Join(parts, " · ")
}
