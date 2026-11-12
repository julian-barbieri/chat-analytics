package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// Tarjetas de En vivo del diseño ejecutivo (spec diseno-ejecutivo): la frase
// de arriba, los likes, las palabras en alza y quiénes participan. Todo con
// reglas sobre datos ya guardados: no llama a YouTube ni a Claude.

// tileInput es lo que necesitan los números de arriba de En vivo.
type tileInput struct {
	msgs       []Message
	concurrent int64
	hourAgo    int64   // espectadores hace ~1 h (0: sin dato)
	typical    float64 // participación típica del vivo (0: sin dato)
	likes      Tile
	sc         scope
	now        time.Time
}

const (
	risingWindow = 30 * time.Minute // "en alza": los últimos 30 min contra lo de antes
	risingMin    = 3                // menciones en la ventana para marcar ▲
	paceSame     = 5                // % de diferencia que todavía es "su ritmo típico"
	trendBand    = 0.1              // participación: ±10 % de la típica es gris
	hourAgoSlack = 10 * time.Minute // muestra de espectadores aceptable para "hace 1 h"
	regularDays  = 7                // habitual: escribió otro día de los 7 anteriores
	topLikedN    = 5
	wordsShown   = 8
	authorsShown = 5
	shareTop     = 10
)

// chatPace es el ritmo del chat: mensajes de la franja actual de 5 min y la
// diferencia en % contra la mediana de las anteriores con mensajes (ok con
// al menos 6, como siempre).
func chatPace(msgs []Message, now time.Time) (cur int, diff float64, ok bool) {
	slots := map[int]int{}
	for _, m := range msgs {
		if m.Source != "chat" || m.At.After(now) {
			continue
		}
		slots[int(now.Sub(m.At)/slotStep)]++
	}
	var past []float64
	for k, n := range slots {
		if k > 0 {
			past = append(past, float64(n))
		}
	}
	if len(past) < 6 {
		return slots[0], 0, false
	}
	typ := median(past)
	if typ <= 0 {
		return slots[0], 0, false
	}
	return slots[0], math.Round((float64(slots[0]) - typ) / typ * 100), true
}

// liveSummary es la frase de arriba de En vivo: el ritmo del chat, lo que más
// se repite y el mejor momento. Cada parte va solo si hay dato.
func liveSummary(diff float64, paceOK bool, of string, words []Count, best *Moment) string {
	var parts []string
	if paceOK {
		d := math.Round(diff)
		switch {
		case math.Abs(d) < paceSame:
			parts = append(parts, "El chat va a su ritmo típico")
		case d > 0:
			parts = append(parts, fmt.Sprintf("El chat está %.0f %% más activo que lo típico %s", d, of))
		default:
			parts = append(parts, fmt.Sprintf("El chat está %.0f %% menos activo que lo típico %s", -d, of))
		}
	}
	if len(words) > 0 {
		w := "«" + words[0].Key + "»"
		if len(words) > 1 {
			w += " y «" + words[1].Key + "»"
		}
		parts = append(parts, "Lo que más se repite: "+w)
	}
	if best != nil {
		parts = append(parts, fmt.Sprintf("El mejor momento fue a las %s (%s de participación)", best.Start.Local().Format("15:04"), pct(best.Pct)))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ". ") + "."
}

// viewersAgo es cuántos miraban hace ~1 h: la muestra más cercana a now-1h
// dentro de hourAgoSlack (0 si no hay).
func viewersAgo(samples []ViewerSample, now time.Time) int64 {
	target := now.Add(-time.Hour)
	var best int64
	bestD := hourAgoSlack + 1
	for _, v := range samples {
		d := v.At.Sub(target)
		if d < 0 {
			d = -d
		}
		if d <= hourAgoSlack && d < bestD {
			best, bestD = v.Viewers, d
		}
	}
	return best
}

// hourAgoSum suma lo de hace ~1 h de varios vivos, los mismos que suma
// "Mirando ahora". Si a uno le falta la muestra, devuelve 0 (no se compara):
// con un vivo nuevo que empezó hace menos de 1 h, la suma de hoy contra la de
// un solo vivo daría una subida que no existe.
func hourAgoSum(samples [][]ViewerSample, now time.Time) int64 {
	var sum int64
	for _, vs := range samples {
		v := viewersAgo(vs, now)
		if v == 0 {
			return 0
		}
		sum += v
	}
	return sum
}

// liveLikes es el número "Likes del vivo": con cur, el contador del vivo en
// curso y lo que subió en la última media hora; sin cur, lo que ganaron hoy
// los vivos ids (segmentLikes desde from).
func liveLikes(samples map[string][]LikeSample, cur *Video, ids []string, from, now time.Time) Tile {
	if cur != nil {
		t := Tile{Label: "Likes del vivo", Value: "—", Note: "sin dato todavía"}
		ls := samples[cur.ID]
		if cur.Likes == 0 && len(ls) == 0 {
			return t
		}
		n := cur.Likes
		if n == 0 {
			n = ls[len(ls)-1].Likes
		}
		t.Value = miles(n)
		gained, _ := segmentLikes(ls, Segment{VideoID: cur.ID, Start: now.Add(-risingWindow), State: SegLive})
		if gained > 0 {
			t.Note, t.Trend = "+"+miles(gained)+" en la última media hora", "up"
		} else {
			t.Note = "sin likes nuevos en la última media hora"
		}
		return t
	}
	t := Tile{Label: "Likes de hoy", Value: "—", Note: "sin dato todavía"}
	var sum int64
	found := false
	for _, id := range ids {
		if g, ok := segmentLikes(samples[id], Segment{VideoID: id, Start: from, State: SegLive}); ok {
			sum, found = sum+g, true
		}
	}
	if !found {
		return t
	}
	t.Value, t.Note = miles(sum), "ganados hoy por los vivos del canal"
	if sum > 0 {
		t.Trend = "up"
	}
	return t
}

// likesTodayChart son los likes nuevos de los vivos en cada media hora de hoy,
// de las 00:00 (izquierda) a ahora (derecha). La franja en curso va marcada.
func likesTodayChart(samples map[string][]LikeSample, day, now time.Time) CommentChart {
	n := int(now.Sub(day)/likesSlot) + 1
	ns, labels, classes := make([]int, n), make([]string, n), make([]string, n)
	total := 0
	for _, ls := range samples {
		for i := 1; i < len(ls); i++ {
			k := int(ls[i].At.Sub(day) / likesSlot)
			if d := ls[i].Likes - ls[i-1].Likes; d > 0 && k >= 0 && k < n && ls[i].At.After(day) {
				ns[k] += int(d)
				total += int(d)
			}
		}
	}
	for k := range n {
		t := day.Add(time.Duration(k) * likesSlot)
		labels[k] = t.Local().Format("15:04") + "–" + t.Add(likesSlot).Local().Format("15:04")
	}
	classes[n-1] = "now"
	c := barChart(ns, labels, classes, "like nuevo", "likes nuevos")
	// Horas cada 3 h, sin pisar el rótulo de ahora (al menos 1 h antes).
	for h := 0; day.Add(time.Duration(h) * time.Hour).Before(now.Add(-time.Hour)); h += 3 {
		c.Ticks = append(c.Ticks, Tick{Left: float64(h) * float64(time.Hour/likesSlot) * 100 / float64(n), Label: fmt.Sprintf("%02d:00", h)})
	}
	c.Ticks = append(c.Ticks, Tick{Left: 100, Label: "Ahora", End: true})
	c.Aria = "Likes nuevos de los vivos del canal cada 30 minutos de hoy, de las 00:00 a ahora: " + miles(total) + " en total"
	return c
}

// wordsIn cuenta en cuántos mensajes aparece cada palabra (como TopWords).
func wordsIn(msgs []Message) (map[string]int, int) {
	freq := map[string]int{}
	for _, m := range msgs {
		seen := map[string]bool{}
		for _, w := range textWords(m.Text) {
			if !seen[w] {
				seen[w] = true
				freq[w]++
			}
		}
	}
	return freq, len(msgs)
}

// risingWords marca las palabras de words que en los últimos risingWindow
// pesan el doble que antes (proporción de mensajes que las dicen) y aparecen
// en al menos risingMin mensajes. Sin mensajes de antes no hay con qué comparar.
func risingWords(msgs []Message, words []Count, now time.Time) map[string]bool {
	var recent, before []Message
	for _, m := range msgs {
		if m.At.After(now.Add(-risingWindow)) {
			recent = append(recent, m)
		} else {
			before = append(before, m)
		}
	}
	out := map[string]bool{}
	if len(before) == 0 || len(recent) == 0 {
		return out
	}
	rf, rn := wordsIn(recent)
	bf, bn := wordsIn(before)
	for _, w := range words {
		r := rf[w.Key]
		if r >= risingMin && float64(r)/float64(rn) >= 2*float64(bf[w.Key])/float64(bn) {
			out[w.Key] = true
		}
	}
	return out
}

// topShare es el % de los mensajes que escribieron los n que más escriben.
func topShare(msgs []Message, n int) float64 {
	if len(msgs) == 0 {
		return 0
	}
	sum := 0
	for _, c := range TopAuthors(msgs, n) {
		sum += c.N
	}
	return float64(sum) * 100 / float64(len(msgs))
}

// regularSplit cuenta las personas del chat (sin spam, moderadores ni dueño,
// como la fidelización de Programas) que son habituales o nuevas.
func regularSplit(msgs []Message, regulars map[string]bool) (reg, nue int) {
	seen := map[string]bool{}
	for _, m := range msgs {
		// seen primero: participant llama a Classify, que es lo caro.
		if seen[m.Author] || !participant(m) {
			continue
		}
		seen[m.Author] = true
		if regulars[m.Author] {
			reg++
		} else {
			nue++
		}
	}
	return reg, nue
}

// regularsCache guarda, por canal, quiénes escribieron en su chat los
// regularDays días anteriores a hoy. Se arma una vez por día local: leer 7
// días de mensajes en cada refresco de 30 s sería caro. Solo en memoria.
type regularsCache struct {
	mu   sync.Mutex
	byCh map[string]regularSet
}

type regularSet struct {
	day     time.Time
	authors map[string]bool
}

// regulars devuelve los habituales del canal para el día de now (no lo
// modifica quien lo recibe). Lo usan los handlers; lee el Store con copias.
func (s *Server) regulars(chID string, now time.Time) map[string]bool {
	day := dayStart(now)
	c := &s.regs
	c.mu.Lock()
	defer c.mu.Unlock()
	if rs, ok := c.byCh[chID]; ok && rs.day.Equal(day) {
		return rs.authors
	}
	from := day.AddDate(0, 0, -regularDays)
	ids := map[string]bool{}
	for _, l := range s.st.Lives(from) {
		if l.ChannelID == chID && l.At.Before(day) {
			ids[l.VideoID] = true
		}
	}
	authors := map[string]bool{}
	for _, m := range s.st.MessagesOf(ids, from) {
		if m.At.Before(day) && participant(m) {
			authors[m.Author] = true
		}
	}
	if c.byCh == nil {
		c.byCh = map[string]regularSet{}
	}
	c.byCh[chID] = regularSet{day: day, authors: authors}
	return authors
}

// WordRow y TalkerRow son las filas con barra de "De qué se habla" y
// "Quiénes participan"; W es el ancho de la barra en %.
type WordRow struct {
	Key  string
	N    int
	W    float64
	Up   bool
	Tick string // "▲" si está en alza
}

type TalkerRow struct {
	Key string
	N   int
	W   float64
	Tag string // "habitual", "nuevo" o "" sin historia
}

// monitorView arma "De qué se habla" y "Quiénes participan" con los mensajes
// del mismo alcance que el resto del análisis. regulars en nil: sin historia
// (vivo suelto), no se separan habituales y nuevos.
func monitorView(msgs []Message, words []Count, sc scope, now time.Time, regulars map[string]bool) map[string]any {
	up := risingWords(msgs, words, now)
	var wrows []WordRow
	for _, w := range words {
		r := WordRow{Key: w.Key, N: w.N, W: float64(w.N) * 100 / float64(words[0].N), Up: up[w.Key]}
		if r.Up {
			r.Tick = "▲"
		}
		wrows = append(wrows, r)
	}
	authors := TopAuthors(msgs, authorsShown)
	var arows []TalkerRow
	for _, a := range authors {
		r := TalkerRow{Key: a.Key, N: a.N, W: float64(a.N) * 100 / float64(authors[0].N)}
		if len(regulars) > 0 {
			r.Tag = authorTag(msgs, a.Key, regulars)
		}
		arows = append(arows, r)
	}
	out := map[string]any{"Total": len(msgs), "Of": sc.of, "Words": wrows, "Authors": arows, "AnyUp": len(up) > 0}
	if len(msgs) > 0 {
		out["Share"] = pct(topShare(msgs, shareTop))
	}
	// Sin historia del canal todos serían "nuevos": no se muestra la barra.
	if len(regulars) > 0 {
		reg, nue := regularSplit(msgs, regulars)
		if reg+nue > 0 {
			out["Split"] = map[string]any{
				"Reg": reg, "New": nue, "People": reg + nue,
				"RegPct": pct(float64(reg) * 100 / float64(reg+nue)), "NewPct": pct(float64(nue) * 100 / float64(reg+nue)),
				"RegW": float64(reg) * 100 / float64(reg+nue), "NewW": float64(nue) * 100 / float64(reg+nue),
			}
		}
	}
	return out
}

// authorTag es la marca de una persona del top: "del canal" si es moderador o
// dueño, nada si solo mandó spam (no entra en la barra habituales / nuevos)
// y, si no, "habitual" o "nuevo", con el mismo criterio que regularSplit.
func authorTag(msgs []Message, author string, regulars map[string]bool) string {
	part := false
	for _, m := range msgs {
		if m.Author != author || m.Source != "chat" {
			continue
		}
		if m.Role != "" {
			return "del canal"
		}
		part = part || participant(m)
	}
	switch {
	case !part:
		return ""
	case regulars[author]:
		return "habitual"
	}
	return "nuevo"
}

// LikedRow es una fila de "Lo que más gustó · 7 días".
type LikedRow struct{ Kind, Class, Title, Likes, Href string }

// topLiked son los n contenidos del canal con más likes según la última
// revisión: los publicados desde since y los vivos en curso (live), que
// pueden haber empezado antes. Vivos, videos y shorts en una sola lista.
func topLiked(recs []VideoRecord, chID string, live map[string]bool, since time.Time, n int) []LikedRow {
	var pick []VideoRecord
	for _, r := range recs {
		if r.ChannelID == chID && r.Likes > 0 && (!r.PublishedAt.Before(since) || live[r.ID]) {
			pick = append(pick, r)
		}
	}
	sort.SliceStable(pick, func(i, j int) bool { return pick[i].Likes > pick[j].Likes })
	var out []LikedRow
	for _, r := range pick[:min(n, len(pick))] {
		row := LikedRow{Kind: "Video", Class: "video", Title: r.Title, Likes: miles(r.Likes), Href: "https://www.youtube.com/watch?v=" + r.ID}
		switch {
		case r.Live:
			row.Kind, row.Class = "Vivo", "vivo"
		case r.Short:
			row.Kind, row.Class = "Short", "short"
		}
		out = append(out, row)
	}
	return out
}

// likesView es la tarjeta "Likes" de En vivo de un canal.
func (s *Server) likesView(snap Snapshot, ch ChannelStatus, ls liveScope, now time.Time) (Tile, map[string]any) {
	day := dayStart(now)
	ids := map[string]bool{}
	live := map[string]bool{}
	var cur *Video
	for _, v := range snap.Videos {
		if v.ChannelID == ch.ID && v.LiveStatus == "live" {
			ids[v.ID], live[v.ID] = true, true
			if v.ID == ls.video {
				cur = &v
			}
		}
	}
	for _, sg := range ls.segs {
		if sg.VideoID != "" {
			ids[sg.VideoID] = true
		}
	}
	samples := map[string][]LikeSample{}
	var list []string
	for id := range ids {
		samples[id] = s.st.Likes(id, day.AddDate(0, 0, -regularDays))
		list = append(list, id)
	}
	sort.Strings(list)
	var tile Tile
	if ls.prog != "" && cur != nil {
		tile = liveLikes(samples, cur, nil, day, now)
	} else {
		tile = liveLikes(samples, nil, list, day, now)
	}
	view := map[string]any{"Top": topLiked(s.st.VideoArchive(), ch.ID, live, now.AddDate(0, 0, -regularDays), topLikedN)}
	chart := likesTodayChart(samples, day, now)
	for _, b := range chart.Bars {
		if b.N > 0 {
			view["Chart"] = chart
			break
		}
	}
	return tile, view
}
