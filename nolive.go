package main

import (
	"fmt"
	"sort"
	"time"
)

// Pantalla Sin vivo: los ratos en que un canal no tuvo ningún vivo, armados
// con las muestras de datos.vivos.jsonl (ProgramSegments), sin cuota extra.
// Funciones puras; los handlers están en nolivepage.go.

// maxCut: un rato sin vivo de más de 2 h no es un corte. Puede ser que el
// canal no transmita a esa hora o que el monitor estuviera apagado (sin
// muestras no se distingue), así que no cuenta en los números.
const maxCut = 2 * time.Hour

// NoLiveView es la pantalla Sin vivo de un canal en un período.
type NoLiveView struct {
	HasData   bool
	Days      []NoLiveDay // del más nuevo al más viejo
	Cuts      []NoLiveCut // del más nuevo al más viejo
	CutsToday int
	MinToday  int
	OnAirPct  float64       // al aire ÷ (al aire + cortes), sin los ratos de más de maxCut
	Longest   time.Duration // el corte más largo del período
}

// NoLiveDay es la tira de 24 h de un día.
type NoLiveDay struct {
	Day    time.Time
	Label  string
	Parts  []NoLivePart
	CutMin int // minutos en cortes ese día
}

// NoLivePart es un pedazo de la tira: "aire", "corte" o "largo" (sin vivo
// de más de maxCut). Left y Width van en % de las 24 h.
type NoLivePart struct {
	Left, Width float64
	Class       string
	Title       string
}

// NoLiveCut es un corte: un rato sin vivo de hasta maxCut entre dos
// programas o, si sigue (Ongoing), desde el último hasta ahora.
type NoLiveCut struct {
	Start, End    time.Time
	Before, After string
	Ongoing       bool
}

// Dur es cuánto duró (o lleva) el corte.
func (c NoLiveCut) Dur() time.Duration { return c.End.Sub(c.Start) }

// span es un rato con su clase, antes de recortarlo por día.
type span struct {
	start, end time.Time
	class      string
}

// NoLiveCuts arma la pantalla Sin vivo de un canal: days son los días del
// período (00:00 locales, del más nuevo al más viejo) y live los vivos del
// canal en curso ahora.
func NoLiveCuts(samples []LiveSample, channelID string, live map[string]bool, days []time.Time, now time.Time) NoLiveView {
	var v NoLiveView
	if len(days) == 0 {
		return v
	}
	segs := ProgramSegments(samples, channelID, live, days[len(days)-1], now)
	var spans []span
	var first time.Time
	var long time.Duration
	for i, sg := range segs {
		if sg.State != SegNone {
			if first.IsZero() || sg.Start.Before(first) {
				first = sg.Start
			}
			spans = append(spans, span{sg.Start, sg.End, "aire"})
			continue
		}
		if i == 0 && len(segs) == 1 {
			continue // sin ninguna muestra del canal: no hay datos
		}
		if sg.End.Sub(sg.Start) > maxCut {
			long += sg.End.Sub(sg.Start)
			spans = append(spans, span{sg.Start, sg.End, "largo"})
			continue
		}
		c := NoLiveCut{Start: sg.Start, End: sg.End}
		c.Before, c.After = neighbors(segs, i)
		c.Ongoing = c.After == ""
		v.Cuts = append(v.Cuts, c)
		spans = append(spans, span{sg.Start, sg.End, "corte"})
	}
	v.HasData = !first.IsZero()
	today := dayStart(now)
	var cuts time.Duration
	for _, c := range v.Cuts {
		cuts += c.Dur()
		v.Longest = max(v.Longest, c.Dur())
		if overlap(c.Start, c.End, today, now) > 0 {
			v.CutsToday++
			v.MinToday += minutes(overlap(c.Start, c.End, today, now))
		}
	}
	if total := now.Sub(first) - long; v.HasData && total > 0 {
		v.OnAirPct = float64(total-cuts) * 100 / float64(total)
	}
	sort.SliceStable(v.Cuts, func(i, j int) bool { return v.Cuts[i].Start.After(v.Cuts[j].Start) })
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].start.Before(spans[j].start) })
	for _, d := range days {
		v.Days = append(v.Days, noLiveDay(spans, v.Cuts, d, now))
	}
	return v
}

// neighbors son los programas de antes y de después del rato sin vivo i: el
// que terminó último antes y el primero que empezó después ("" si sigue).
func neighbors(segs []Segment, i int) (before, after string) {
	var end time.Time
	for _, sg := range segs[:i] {
		if sg.State != SegNone && !sg.End.Before(end) {
			before, end = sg.Program, sg.End
		}
	}
	for _, sg := range segs[i+1:] {
		if sg.State != SegNone {
			return before, sg.Program
		}
	}
	return before, ""
}

// noLiveDay recorta los ratos al día d. Los ratos al aire seguidos (otro
// programa o un hueco de menos de newLiveGap entre muestras) van juntos.
func noLiveDay(spans []span, cuts []NoLiveCut, d, now time.Time) NoLiveDay {
	end := d.AddDate(0, 0, 1)
	day := NoLiveDay{Day: d, Label: noLiveLabel(d, now)}
	var merged []span
	for _, sp := range spans {
		s, e := maxTime(sp.start, d), minTime(sp.end, minTime(end, now))
		if !e.After(s) {
			continue
		}
		if n := len(merged); n > 0 && merged[n-1].class == sp.class && s.Sub(merged[n-1].end) <= newLiveGap {
			merged[n-1].end = maxTime(merged[n-1].end, e)
			continue
		}
		merged = append(merged, span{s, e, sp.class})
	}
	full := float64(end.Sub(d))
	for _, sp := range merged {
		day.Parts = append(day.Parts, NoLivePart{
			Left:  float64(sp.start.Sub(d)) * 100 / full,
			Width: float64(sp.end.Sub(sp.start)) * 100 / full,
			Class: sp.class,
			Title: partTitle(sp, d),
		})
	}
	var in time.Duration
	for _, c := range cuts {
		in += overlap(c.Start, c.End, d, end)
	}
	day.CutMin = minutes(in)
	return day
}

func partTitle(sp span, d time.Time) string {
	clock := func(t time.Time) string {
		if t.Equal(d.AddDate(0, 0, 1)) {
			return "24:00"
		}
		return t.Local().Format("15:04")
	}
	when := clock(sp.start) + " a " + clock(sp.end)
	switch sp.class {
	case "corte":
		return "Sin vivo de " + when + " (" + durText(sp.end.Sub(sp.start)) + ")"
	case "largo":
		return "Sin vivo más de 2 h, de " + when
	}
	return "Al aire de " + when
}

// noLiveLabel es "Hoy", "Ayer" o el día de la semana corto con la fecha.
func noLiveLabel(d, now time.Time) string {
	switch today := dayStart(now); {
	case d.Equal(today):
		return "Hoy"
	case d.Equal(today.AddDate(0, 0, -1)):
		return "Ayer"
	}
	return weekdays[d.Weekday()][:3] + " " + d.Format("02/01")
}

// durText es una duración corta: "6 min" o "1 h 05 min".
func durText(d time.Duration) string {
	m := minutes(d)
	if m < 60 {
		return fmt.Sprintf("%d min", m)
	}
	return fmt.Sprintf("%d h %02d min", m/60, m%60)
}

func minutes(d time.Duration) int { return int(d.Round(time.Minute) / time.Minute) }

// overlap es cuánto de [s, e) cae en [from, to).
func overlap(s, e, from, to time.Time) time.Duration {
	return max(0, minTime(e, to).Sub(maxTime(s, from)))
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
