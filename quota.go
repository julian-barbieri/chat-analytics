package main

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// Estimación de la cuota de YouTube por día con la configuración actual, para
// la pantalla Canales. Son cuentas puras: no consultan nada ni cambian lo que
// hace el worker.

const (
	quotaScale       = 10000 // lo que da YouTube por día y por proyecto: la escala de la barra
	videoChecksDay   = 480   // una revisión de videos cada 3 min
	commentsPerDay   = 1000  // el techo de comentarios que usan el README y AGENTS.md
	chatUnitsPerRead = 5
	// Por cada canal extra, para la pantalla Comentarios: sus subidas cada
	// extraUploadsEvery (48 listas por día, más hasta 48 de un pedido de videos
	// de más) y sus comentarios, que dependen de cuánto comenten.
	extraUploadsDay  = 96
	extraCommentsDay = 200
)

// ChatGroup son vivos que se leen por turnos con los mismos intervalos: en
// esta etapa, uno solo con todos los canales (y las horas de la unión).
type ChatGroup struct {
	PeakEvery, OffEvery time.Duration
	PeakHours, OffHours float64 // horas en vivo por día dentro y fuera del pico
	Paused              bool
}

// QuotaEstimate es la cuenta del día: Fast va aparte para mostrarlo, pero en
// la barra se suma a Videos (es revisión de vivos).
type QuotaEstimate struct {
	Videos, Comments, Fast, Total int
	Chat                          []int // por grupo
	Limit, Budget                 int   // Limit: Budget − reserveForComments, donde se pausa el chat
	Fits                          bool
	Margin                        int // lo que queda (Fits) o lo que falta antes de que se pause el chat
	Text                          string
}

// EstimateQuota suma lo que gasta un día: videos (3 + 1 por canal extra cada
// 3 min, más las subidas de los extra cada 30 min), comentarios (más los de
// los videos de los extra), la búsqueda rápida por los cortes observados y el
// chat de cada grupo (5 unidades por lectura, una por intervalo).
func EstimateQuota(channels int, cutsPerDay float64, groups []ChatGroup, budget int) QuotaEstimate {
	extra := max(channels, 1) - 1
	q := QuotaEstimate{Videos: videoChecksDay*(2+max(channels, 1)) + extraUploadsDay*extra, Comments: commentsPerDay + extraCommentsDay*extra,
		Budget: budget, Limit: budget - reserveForComments}
	q.Fast = int(math.Round(cutsPerDay * float64(fastUnitsPerCut())))
	q.Total = q.Videos + q.Comments + q.Fast
	for _, g := range groups {
		n := 0
		if !g.Paused {
			reads := 0.0
			if g.PeakEvery > 0 {
				reads += g.PeakHours * 3600 / g.PeakEvery.Seconds()
			}
			if g.OffEvery > 0 {
				reads += g.OffHours * 3600 / g.OffEvery.Seconds()
			}
			n = int(math.Round(reads)) * chatUnitsPerRead
		}
		q.Chat = append(q.Chat, n)
		q.Total += n
	}
	q.Fits = q.Total <= q.Limit
	if q.Fits {
		q.Margin = q.Limit - q.Total
		q.Text = fmt.Sprintf("Entra en el día. Quedan %s unidades antes de que se pause el chat", miles(q.Margin))
	} else {
		q.Margin = q.Total - q.Limit
		q.Text = fmt.Sprintf("No entra: faltan %s unidades", miles(q.Margin))
	}
	return q
}

// fastUnitsPerCut es lo que gasta como mucho la búsqueda rápida de un canal
// que se queda sin vivo: 2 unidades cada fastEvery durante fastFor (40).
func fastUnitsPerCut() int { return 2 * int(fastFor/fastEvery) }

// HoursOnAir son las horas en vivo de unos canales según datos.vivos.jsonl.
type HoursOnAir struct {
	Peak, Off float64    // horas por día, promedio de lo observado
	Days      []DayHours // hoy y los 6 días anteriores, del más nuevo al más viejo
	Cuts      float64    // veces por día que un canal se quedó sin vivo
	NoHistory bool       // menos de 24 h de muestras: se supone 24 h en vivo
}

// DayHours son las horas en vivo de un día local.
type DayHours struct {
	Day   time.Time
	Hours float64
}

// liveSlot: las horas se cuentan por franjas de 5 min con alguna muestra.
const liveSlot = 5 * time.Minute

// localSlot es la franja de 5 min de t contada desde las 00:00 locales. Con
// Truncate sobre la hora absoluta las franjas quedarían alineadas a UTC: en una
// zona que no está a un múltiplo de 5 min, un vivo de 2 h contaría 2 h 5 min.
func localSlot(t time.Time) time.Time {
	d := dayStart(t)
	return d.Add(t.Sub(d).Truncate(liveSlot))
}

// peakHour dice si una hora local cae en el horario pico, con la misma regla
// que WorkerConfig.chatInterval (la franja puede cruzar la medianoche).
func peakHour(h, from, to int) bool {
	return from < to && h >= from && h < to || from > to && (h >= from || h < to)
}

// peakLen es cuántas horas por día tiene el horario pico.
func peakLen(from, to int) float64 {
	switch {
	case from < to:
		return float64(to - from)
	case from > to:
		return float64(24 - from + to)
	}
	return 0
}

// LiveHours saca de las muestras de vivos las horas en vivo por día de esos
// canales en los últimos 7 días (franjas de 5 min con al menos una muestra),
// separadas en pico y fuera. Con menos de 24 h de historial supone 24 h en
// vivo, el peor caso para la cuota. Los cortes son huecos de más de
// segmentGap entre dos muestras del mismo canal.
func LiveHours(samples []LiveSample, ids map[string]bool, peakFrom, peakTo int, now time.Time) HoursOnAir {
	from := now.Add(-7 * 24 * time.Hour)
	slots := map[time.Time]bool{}
	times := map[string][]time.Time{}
	var first time.Time
	for _, s := range samples {
		if !ids[s.ChannelID] || s.At.Before(from) || s.At.After(now) {
			continue
		}
		slots[localSlot(s.At)] = true
		times[s.ChannelID] = append(times[s.ChannelID], s.At)
		if first.IsZero() || s.At.Before(first) {
			first = s.At
		}
	}
	var lh HoursOnAir
	today := dayStart(now)
	for i := range 7 {
		lh.Days = append(lh.Days, DayHours{Day: today.AddDate(0, 0, -i)})
	}
	// Se cuentan franjas enteras y se pasan a horas al final: sumar 5/60 de a
	// uno acumula error de redondeo.
	var peakN, offN int
	perDay := make([]int, len(lh.Days))
	for t := range slots {
		if peakHour(t.Local().Hour(), peakFrom, peakTo) {
			peakN++
		} else {
			offN++
		}
		for i := range lh.Days {
			if dayStart(t).Equal(lh.Days[i].Day) {
				perDay[i]++
			}
		}
	}
	h := liveSlot.Hours()
	for i := range lh.Days {
		lh.Days[i].Hours = float64(perDay[i]) * h
	}
	peak, off := float64(peakN)*h, float64(offN)*h
	if first.IsZero() || now.Sub(first) < 24*time.Hour {
		lh.NoHistory = true
		lh.Peak = peakLen(peakFrom, peakTo)
		lh.Off = 24 - lh.Peak
		return lh
	}
	days := now.Sub(first).Hours() / 24
	cuts := 0
	for _, ts := range times {
		sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
		for i := 1; i < len(ts); i++ {
			if ts[i].Sub(ts[i-1]) > segmentGap {
				cuts++
			}
		}
	}
	lh.Peak, lh.Off, lh.Cuts = peak/days, off/days, float64(cuts)/days
	return lh
}
