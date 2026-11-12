package main

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"time"
)

// Categorías en semicírculo (CA16 de diseno-ejecutivo): medio anillo con el
// total al centro, la categoría elegida resaltada y su detalle abajo. Los
// sectores se arman en Go como el resto de los gráficos; cada uno es un link
// que pide la pantalla con la categoría elegida (sin JS propio).

// catGaugeIn es lo que necesita el semicírculo. link arma el hx-get de la
// pantalla con otra categoría elegida, sin «Otros» o con la lista entera.
type catGaugeIn struct {
	rows      []CatRow
	msgs      []Message
	labels    map[string]MessageLabel
	sel       string // la categoría pedida ("": la más grande sin contar Otros)
	noOther   bool
	all       bool
	link      func(sel string, noOther, all bool) string
	now, end  time.Time     // end: dónde termina el minigráfico
	step      time.Duration // franja del minigráfico (15 min, o un día en Comentarios)
	n         int           // franjas del minigráfico
	trend     bool          // En vivo: si sube o baja en la última media hora
	stepLabel string        // "Cada 15 min", "Por día"
	target    string        // a dónde va el parcial (hx-target) y su hx-sync
	sync      string
}

// CatSector es un sector del semicírculo y su fila en la lista de al lado.
type CatSector struct {
	Key, Label, Share, D, Href, Title string
	N                                 int
	On                                bool
}

// CatBar es una barra del minigráfico de la categoría elegida (H en %).
type CatBar struct {
	X, Y  float64 // posición en el SVG (una unidad por franja, alto 100)
	H     float64
	Now   bool
	Title string
}

// CatDetail es el panel de la categoría elegida.
type CatDetail struct {
	Key, Label, Share, Trend, TrendClass, Step, Hidden, AllHref string
	N, People                                                   int
	Bars                                                        []CatBar
	Last                                                        []ClaudeExample
	All                                                         bool
	List                                                        *FoldMsgs
}

// CatGauge es la tarjeta Categorías en semicírculo.
type CatGauge struct {
	Sectors   []CatSector
	Total     int
	NoOther   bool
	OtherHref string
	Aria      string
	Sel       *CatDetail
	Target    string
	Sync      string
}

const (
	gaugeCX, gaugeCY = 240.0, 240.0
	gaugeR, gaugeIn  = 220.0, 140.0 // radio de afuera y de adentro del anillo
	gaugeOn          = 232.0        // el sector elegido sobresale
	gaugeGap         = 0.004        // separación entre sectores, en fracción del semicírculo
	catLastShown     = 4
	catTrendMin      = 10 // mensajes en cada media hora para decir si sube o baja
	catTrendSame     = 2  // puntos de diferencia que todavía son "igual"
)

// catTone dice si que una categoría suba es malo (insultos, quejas, spam) o
// bueno (elogios, preguntas), para el color de la tendencia.
var catTone = map[string]int{CatInsulto: -1, CatQueja: -1, CatSpam: -1, CatElogio: 1, CatPregunta: 1}

// catGauge arma el semicírculo con las filas de catRows / catRowsWith (ya
// ordenadas, Otros al final) y el detalle de la categoría elegida.
func catGauge(in catGaugeIn) CatGauge {
	g := CatGauge{NoOther: in.noOther, Target: in.target, Sync: in.sync}
	var shown []CatRow
	for _, r := range in.rows {
		if r.N > 0 && !(in.noOther && r.Key == CatOtro) {
			shown = append(shown, r)
			g.Total += r.N
		}
	}
	if g.Total == 0 {
		return g
	}
	sel := in.sel
	if !slices.ContainsFunc(shown, func(r CatRow) bool { return r.Key == sel }) {
		sel = shown[0].Key // la más grande: Otros va al final, salvo que sea la única
	}
	g.OtherHref = in.link(sel, !in.noOther, false)
	acc := 0
	for i, r := range shown {
		t0, t1 := float64(acc)/float64(g.Total), float64(acc+r.N)/float64(g.Total)
		acc += r.N
		if len(shown) > 1 {
			// Sin separación en los bordes del semicírculo, solo entre sectores.
			if i > 0 {
				t0 += gaugeGap
			}
			if i < len(shown)-1 {
				t1 -= gaugeGap
			}
		}
		outer := gaugeR
		if r.Key == sel {
			outer = gaugeOn
		}
		s := CatSector{Key: r.Key, Label: r.Label, N: r.N, On: r.Key == sel, Share: pct(share(r.N, g.Total)),
			D: ringPath(gaugeIn, outer, t0, max(t1, t0)), Href: in.link(r.Key, in.noOther, false)}
		s.Title = fmt.Sprintf("%s: %s (%s)", r.Label, plural(r.N, "mensaje", "mensajes"), s.Share)
		g.Sectors = append(g.Sectors, s)
		if i > 0 {
			g.Aria += ", "
		}
		g.Aria += r.Label + " " + s.Share
	}
	g.Sel = catDetail(in, sel, g.Total)
	return g
}

// ringPath es un sector del medio anillo entre las fracciones t0 y t1 (0: el
// borde izquierdo, 1: el derecho), de r1 a r2.
func ringPath(r1, r2, t0, t1 float64) string {
	pt := func(rad, t float64) string {
		a := math.Pi * (1 - t)
		return fmt.Sprintf("%.2f %.2f", gaugeCX+rad*math.Cos(a), gaugeCY-rad*math.Sin(a))
	}
	return fmt.Sprintf("M%s A%.0f %.0f 0 0 1 %s L%s A%.0f %.0f 0 0 0 %s Z", pt(r2, t0), r2, r2, pt(r2, t1), pt(r1, t1), r1, r1, pt(r1, t0))
}

// catDetail es el panel de la categoría elegida: sus números, cómo vino, los
// últimos mensajes y, si se pidió, la lista entera (sin el spam).
func catDetail(in catGaugeIn, sel string, total int) *CatDetail {
	var mine []Message
	for _, m := range in.msgs {
		if c, _ := catOf(m, in.labels); c == sel {
			mine = append(mine, m)
		}
	}
	sort.SliceStable(mine, func(i, j int) bool { return mine[i].At.After(mine[j].At) })
	people := map[string]bool{}
	for _, m := range mine {
		people[m.Author] = true
	}
	d := &CatDetail{Key: sel, Label: catLabels[sel], N: len(mine), People: len(people), Share: pct(share(len(mine), total)),
		Step: in.stepLabel, All: in.all, AllHref: in.link(sel, in.noOther, !in.all)}
	d.Bars = catBars(mine, in.end, in.step, in.n)
	if in.trend {
		d.Trend, d.TrendClass = catTrend(in.msgs, in.labels, sel, in.now)
	}
	if sel == CatSpam {
		// Como en la tarjeta de siempre: los links de spam no se muestran.
		d.Hidden = "Los mensajes de spam no se muestran: " + catExamples[CatSpam] + "."
		d.All = false
		return d
	}
	for _, m := range mine[:min(len(mine), catLastShown)] {
		d.Last = append(d.Last, ClaudeExample{m.Author, m.At.Local().Format("15:04"), m.Text})
	}
	if in.all {
		d.List = foldMsgs(mine, len(mine))
	}
	return d
}

// catBars cuenta los mensajes en n franjas de step que terminan en end: lo
// más viejo a la izquierda y la franja de end (la actual) marcada.
func catBars(msgs []Message, end time.Time, step time.Duration, n int) []CatBar {
	if n <= 0 || step <= 0 {
		return nil
	}
	start := end.Add(-time.Duration(n) * step)
	counts := make([]int, n)
	peak := 1
	for _, m := range msgs {
		if !m.At.After(start) || m.At.After(end) {
			continue
		}
		k := min(int(m.At.Sub(start)/step), n-1)
		counts[k]++
		peak = max(peak, counts[k])
	}
	format := "15:04"
	if step >= 24*time.Hour {
		format = "02/01"
	}
	bars := make([]CatBar, n)
	for k, c := range counts {
		from := start.Add(time.Duration(k) * step)
		h := float64(c) * 100 / float64(peak)
		bars[k] = CatBar{X: float64(k) + 0.12, Y: 100 - h, H: h, Now: k == n-1,
			Title: from.Local().Format(format) + ": " + plural(c, "mensaje", "mensajes")}
	}
	return bars
}

// catTrend compara qué parte del chat fue de la categoría en la última media
// hora contra lo de antes, con al menos catTrendMin mensajes de cada lado.
func catTrend(msgs []Message, labels map[string]MessageLabel, sel string, now time.Time) (string, string) {
	var recent, before, recentIn, beforeIn int
	for _, m := range msgs {
		c, _ := catOf(m, labels)
		if m.At.After(now.Add(-risingWindow)) {
			recent++
			if c == sel {
				recentIn++
			}
		} else {
			before++
			if c == sel {
				beforeIn++
			}
		}
	}
	if recent < catTrendMin || before < catTrendMin {
		return "", ""
	}
	diff := math.Round(share(recentIn, recent) - share(beforeIn, before))
	switch {
	case math.Abs(diff) < catTrendSame:
		return "Igual que antes en la última media hora", ""
	case diff > 0:
		return fmt.Sprintf("▲ %.0f pts en la última media hora", diff), catToneClass(sel, 1)
	default:
		return fmt.Sprintf("▼ %.0f pts en la última media hora", -diff), catToneClass(sel, -1)
	}
}

func catToneClass(cat string, dir int) string {
	switch catTone[cat] * dir {
	case 1:
		return "good"
	case -1:
		return "bad"
	}
	return ""
}
