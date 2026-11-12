package main

import (
	"fmt"
	"sort"
	"time"
)

// Pantalla Sin vivo: cada rato de un canal según lo que vio el monitor, con
// las muestras de datos.vivos.jsonl (al aire) y las revisiones sin vivo de
// datos.sinvivo.jsonl, sin cuota extra. Sin ninguna de las dos, el monitor
// estaba apagado: no es un corte. Funciones puras; los handlers están en
// nolivepage.go.

// NoLiveView es la pantalla Sin vivo de un canal en un período.
type NoLiveView struct {
	HasData   bool
	Days      []NoLiveDay // del más nuevo al más viejo
	Cuts      []NoLiveCut // del más nuevo al más viejo
	CutsToday int
	MinToday  int
	OnAirPct  float64       // al aire ÷ (al aire + cortes), sin lo apagado
	Longest   time.Duration // el corte más largo del período
}

// NoLiveDay es la tira de 24 h de un día.
type NoLiveDay struct {
	Day    time.Time
	Label  string
	Parts  []NoLivePart
	CutMin int // minutos en cortes ese día
}

// NoLivePart es un pedazo de la tira: "aire", "corte" o "apagado" (el
// monitor no revisó el canal). Left y Width van en % de las 24 h.
type NoLivePart struct {
	Left, Width float64
	Class       string
	Title       string
}

// NoLiveCut es un corte: un rato en que el monitor revisó el canal y no
// encontró vivo, sin mínimo ni tope de duración. Before y After son los
// programas de antes y de después ("" si el corte empieza o termina con el
// monitor apagado); Ongoing, que sigue ahora.
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

// seen es una revisión del canal: al aire (con su programa) o sin vivo.
type seen struct {
	at   time.Time
	air  bool
	prog string
}

// NoLiveCuts arma la pantalla Sin vivo de un canal: days son los días del
// período (00:00 locales, del más nuevo al más viejo). Entre dos revisiones
// seguidas (a lo sumo segmentGap, dos revisiones completas perdidas) el rato
// es al aire si las dos vieron un vivo y corte si alguna no; más separadas, el
// monitor estaba apagado. Lo que viene después de la última revisión cuenta
// igual hasta ahora.
func NoLiveCuts(samples []LiveSample, checks []NoLiveCheck, channelID string, days []time.Time, now time.Time) NoLiveView {
	var v NoLiveView
	if len(days) == 0 {
		return v
	}
	from := days[len(days)-1]
	pts := channelSeen(samples, checks, channelID, from, now)
	v.HasData = len(pts) > 0
	type run struct {
		span
		first, last int // revisiones en las puntas (-1: inicio del período o ahora)
	}
	var runs []run
	add := func(s, e time.Time, class string, first, last int) {
		if n := len(runs); n > 0 && runs[n-1].class == class {
			runs[n-1].end, runs[n-1].last = e, last
			return
		}
		runs = append(runs, run{span{s, e, class}, first, last})
	}
	for i, p := range pts {
		if i == 0 && p.at.After(from) {
			add(from, p.at, "apagado", -1, i)
		}
		next, end, last := p, now, -1
		if i+1 < len(pts) {
			next, end, last = pts[i+1], pts[i+1].at, i+1
		}
		switch {
		case end.Sub(p.at) > segmentGap:
			add(p.at, end, "apagado", i, last)
		case p.air && next.air:
			add(p.at, end, "aire", i, last)
		default:
			add(p.at, end, "corte", i, last)
		}
	}
	var spans []span
	var air, cuts time.Duration
	today := dayStart(now)
	for _, r := range runs {
		if r.end.After(r.start) {
			spans = append(spans, r.span)
		}
		switch r.class {
		case "aire":
			air += r.end.Sub(r.start)
		case "corte":
			c := NoLiveCut{Start: r.start, End: r.end, Ongoing: r.last < 0}
			if r.first >= 0 && pts[r.first].air {
				c.Before = pts[r.first].prog
			}
			if r.last >= 0 && pts[r.last].air {
				c.After = pts[r.last].prog
			}
			v.Cuts = append(v.Cuts, c)
			cuts += c.Dur()
			v.Longest = max(v.Longest, c.Dur())
			if in := overlap(c.Start, c.End, today, now); in > 0 {
				v.CutsToday++
				v.MinToday += minutes(in)
			}
		}
	}
	if air+cuts > 0 {
		v.OnAirPct = float64(air) * 100 / float64(air+cuts)
	}
	sort.SliceStable(v.Cuts, func(i, j int) bool { return v.Cuts[i].Start.After(v.Cuts[j].Start) })
	for _, d := range days {
		v.Days = append(v.Days, noLiveDay(spans, v.Cuts, d, now))
	}
	return v
}

// channelSeen son las revisiones del canal entre from y now, en orden: las
// muestras de sus vivos y las revisiones que lo encontraron sin vivo.
func channelSeen(samples []LiveSample, checks []NoLiveCheck, channelID string, from, now time.Time) []seen {
	in := func(t time.Time) bool { return !t.Before(from) && !t.After(now) }
	var pts []seen
	for _, s := range samples {
		if s.ChannelID == channelID && in(s.At) {
			pts = append(pts, seen{at: s.At, air: true, prog: ProgramName(s.Title, s.Channel)})
		}
	}
	for _, c := range checks {
		if c.ChannelID == channelID && in(c.At) {
			pts = append(pts, seen{at: c.At})
		}
	}
	sort.SliceStable(pts, func(i, j int) bool { return pts[i].at.Before(pts[j].at) })
	return pts
}

// noLiveDay recorta los ratos al día d (y a ahora, en el de hoy).
func noLiveDay(spans []span, cuts []NoLiveCut, d, now time.Time) NoLiveDay {
	end := d.AddDate(0, 0, 1)
	day := NoLiveDay{Day: d, Label: noLiveLabel(d, now)}
	var merged []span
	for _, sp := range spans {
		s, e := maxTime(sp.start, d), minTime(sp.end, minTime(end, now))
		if e.After(s) {
			merged = append(merged, span{s, e, sp.class})
		}
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
	case "apagado":
		return "Monitor apagado de " + when
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
