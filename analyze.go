package main

import (
	"sort"
	"strings"
	"time"
)

// Análisis por reglas de los mensajes de un vivo para el dashboard. Es una
// aproximación barata: los borradores de respuesta y el análisis fino los
// hace Claude con el CSV.

// Quejas técnicas por tipo. Se buscan como frases enteras sobre el texto sin
// tildes, así "eco" no matchea "economía". Se evitan palabras sueltas como
// "audio" o "corte", que en una radio de noticias suelen ser parte del tema
// ("los audios de...", "cortes de luz").
var techIssues = []struct {
	Key   string
	Words []string
}{
	{"Audio", []string{"no se escucha", "no se oye", "se escucha mal", "se oye mal", "se escucha bajo", "se escucha muy bajo",
		"sin audio", "sin sonido", "mal audio", "mal sonido", "no hay audio", "no hay sonido", "subi el volumen",
		"subile el volumen", "suban el volumen", "volumen bajo", "hay eco", "tiene eco"}},
	{"Cortes e imagen", []string{"se corta", "se corto", "entrecortado", "se colgo", "se tilda", "se tildo", "se traba",
		"se trabo", "trabado", "se congela", "se congelo", "congelado", "pantalla negra", "sin imagen", "pixelado",
		"sin señal"}},
	{"Demora", []string{"delay", "lag", "buffer", "buffering", "desfasado", "desincronizado", "cargando"}},
}

var unaccent = strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u")

// phraseText devuelve el texto en minúsculas, sin tildes y con un espacio entre
// palabras (y en los bordes), para buscar frases enteras.
func phraseText(text string) string {
	return " " + strings.Join(textWords(unaccent.Replace(text)), " ") + " "
}

// TechIssue devuelve el tipo de queja técnica del mensaje, o "".
func TechIssue(text string) string {
	t := phraseText(text)
	for _, k := range techIssues {
		for _, w := range k.Words {
			if strings.Contains(t, " "+w+" ") {
				return k.Key
			}
		}
	}
	return ""
}

// Preguntas que son saludos y no hace falta responder al aire.
var greetings = []string{"como estas", "como andan", "como anda", "como va", "como le va", "todo bien", "que tal"}

// Analysis es lo que muestra el dashboard de cada vivo.
type Analysis struct {
	Total      int
	Topics     []Count   // temas: palabras o pares de palabras, en cuántos mensajes aparecen
	Questions  []Message // preguntas que vale la pena mirar
	Tech       []Count   // quejas técnicas por tipo
	TechMsgs   []Message
	Filters    []Count // palabras de las listas de spam e insultos que aparecieron
	Candidates []Count // palabras que casi solo aparecen en spam e insultos: revisarlas antes de cargarlas
	Links      int     // mensajes con links
}

func Analyze(msgs []Message) Analysis {
	a := Analysis{Total: len(msgs), Topics: TopTopics(msgs, 5)}
	tech := map[string]int{}
	filters := map[string]int{}
	seenQ := map[string]bool{}
	var bad []Message
	for _, m := range msgs {
		cat := Classify(m.Text)
		t := strings.ToLower(m.Text)
		if q := strings.Join(strings.Fields(t), " "); cat == CatPregunta && worthAnswering(q) && !seenQ[q] {
			seenQ[q] = true
			a.Questions = append(a.Questions, m)
		}
		if k := TechIssue(m.Text); k != "" {
			tech[k]++
			a.TechMsgs = append(a.TechMsgs, m)
		}
		// Escribir todo en mayúsculas cuenta como insulto en Classify, pero acá
		// solo interesan los insultos de verdad: si no, gritar marca a cualquiera.
		if cat == CatInsulto && !containsAny(t, insultos) {
			cat = CatOtro
		}
		if cat != CatSpam && cat != CatInsulto {
			continue
		}
		bad = append(bad, m)
		if reLink.MatchString(t) {
			a.Links++
		}
		for _, w := range append(append([]string(nil), insultos...), spamWords...) {
			if strings.Contains(t, w) {
				filters[w]++
			}
		}
	}

	// Primero lo que más likes tiene (comentarios) y después lo más nuevo.
	sort.SliceStable(a.Questions, func(i, j int) bool {
		qi, qj := a.Questions[i], a.Questions[j]
		if qi.Likes != qj.Likes {
			return qi.Likes > qj.Likes
		}
		return qi.At.After(qj.At)
	})
	a.Questions = a.Questions[:min(len(a.Questions), 8)]

	for _, k := range techIssues {
		if tech[k.Key] > 0 {
			a.Tech = append(a.Tech, Count{k.Key, tech[k.Key]})
		}
	}
	sort.SliceStable(a.TechMsgs, func(i, j int) bool { return a.TechMsgs[i].At.After(a.TechMsgs[j].At) })
	a.TechMsgs = a.TechMsgs[:min(len(a.TechMsgs), 5)]

	a.Filters = topN(filters, 12)
	a.Candidates = candidates(bad, msgs, 8)
	return a
}

// worthAnswering descarta preguntas de una o dos palabras ("?", "quién habla?"),
// las que son charla entre usuarios ("@juan ¿tenés IG?") y los saludos.
func worthAnswering(q string) bool {
	if len([]rune(q)) < 15 || strings.HasPrefix(q, "@") {
		return false
	}
	t := phraseText(q)
	for _, g := range greetings {
		if strings.Contains(t, " "+g+" ") {
			return false
		}
	}
	return true
}

// candidates busca palabras que se repiten en spam e insultos y casi nunca en
// el resto: posibles filtros nuevos (por ejemplo apodos ofensivos).
func candidates(bad, all []Message, n int) []Count {
	inBad := map[string]int{}
	for _, m := range bad {
		for _, w := range uniqueWords(m.Text) {
			inBad[w]++
		}
	}
	inAll := map[string]int{}
	for _, m := range all {
		for _, w := range uniqueWords(m.Text) {
			if inBad[w] > 0 {
				inAll[w]++
			}
		}
	}
	out := map[string]int{}
	for w, b := range inBad {
		// Las variantes de palabras que ya están en las listas ("pelotudos") no suman.
		if b >= 2 && b*10 >= inAll[w]*7 && !containsAny(w, insultos) && !containsAny(w, spamWords) {
			out[w] = b
		}
	}
	return topN(out, n)
}

// uniqueWords son las palabras de 4 letras o más, sin stopwords ni repetidas.
func uniqueWords(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range textWords(text) {
		if len([]rune(w)) >= 4 && !stopwords[w] && !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

// TopTopics devuelve los temas más hablados: cada palabra frecuente, o el par
// de palabras seguidas que la acompaña casi siempre ("dólar blue" en vez de
// "dólar"). Cuenta en cuántos mensajes aparece cada uno.
func TopTopics(msgs []Message, n int) []Count {
	single, pairs := map[string]int{}, map[string]int{}
	for _, m := range msgs {
		seen := map[string]bool{}
		prev := ""
		for _, w := range textWords(m.Text) {
			if len([]rune(w)) < 4 || stopwords[w] {
				prev = ""
				continue
			}
			if !seen[w] {
				seen[w] = true
				single[w]++
			}
			if p := prev + " " + w; prev != "" && !seen[p] {
				seen[p] = true
				pairs[p]++
			}
			prev = w
		}
	}
	var out []Count
	used := map[string]bool{}
	for _, c := range topN(single, 4*n) {
		if len(out) == n {
			break
		}
		if used[c.Key] || c.N < 2 {
			continue
		}
		best := Count{}
		for p, k := range pairs {
			a, b, _ := strings.Cut(p, " ")
			if (a == c.Key || b == c.Key) && k >= max(3, c.N/2) && !used[a] && !used[b] &&
				(k > best.N || k == best.N && p < best.Key) {
				best = Count{p, k}
			}
		}
		if best.N > 0 {
			c = best
		}
		for _, w := range strings.Fields(c.Key) {
			used[w] = true
		}
		out = append(out, c)
	}
	return out
}

// sinceFor devuelve los mensajes de un video desde t.
func sinceFor(msgs []Message, videoID string, t time.Time) []Message {
	var out []Message
	for _, m := range msgs {
		if m.VideoID == videoID && !m.At.Before(t) {
			out = append(out, m)
		}
	}
	return out
}

// Momentos con más participación del chat, para elegir qué usar en reels.
const (
	slotStep       = 5 * time.Minute
	slotCount      = 288              // 24 h, la misma ventana que el resto del análisis del vivo
	minPersonasTop = 3                // con menos, 2 personas de madrugada con 20 mirando ganarían
	minSeparacion  = 15 * time.Minute // que una misma charla no ocupe varios puestos
	maxMomentos    = 5
)

// Slot es una franja de 5 minutos del chat de un vivo.
type Slot struct {
	Start   time.Time
	Msgs    []Message // solo para los momentos; no se muestran todos
	N       int       // mensajes
	Authors int       // personas distintas
	Viewers int64     // promedio de las muestras; 0 = sin dato
	Pct     float64   // participación; vale solo si Viewers > 0
}

// Participation arma las franjas de las últimas 24 h del chat de uno o varios
// vivos (los de un canal) y la participación típica (mediana del % de las
// franjas con espectadores). Las personas se cuentan por nombre visible y los
// espectadores son los que YouTube informa "mirando ahora": es una aproximación.
func Participation(msgs []Message, viewers []ViewerSample, videoIDs []string, now time.Time) (slots []Slot, typical float64, ok bool) {
	want := map[string]bool{}
	for _, id := range videoIDs {
		want[id] = true
	}
	end := now.Truncate(slotStep).Add(slotStep)
	start := end.Add(-slotCount * slotStep)
	index := func(t time.Time) int {
		if t.Before(start) || !t.Before(end) {
			return -1
		}
		return int(t.Sub(start) / slotStep)
	}
	slots = make([]Slot, slotCount)
	authors := make([]map[string]bool, slotCount)
	for i := range slots {
		slots[i].Start = start.Add(time.Duration(i) * slotStep)
		authors[i] = map[string]bool{}
	}
	for _, m := range msgs {
		i := index(m.At)
		if m.Source != "chat" || !want[m.VideoID] || i < 0 {
			continue
		}
		slots[i].Msgs = append(slots[i].Msgs, m)
		slots[i].N++
		if m.Author != "" {
			authors[i][m.Author] = true
		}
	}
	// Promedio por vivo y por franja: con dos vivos del canal a la vez, los que
	// miraban son la suma de los promedios de cada uno.
	type key struct {
		slot int
		id   string
	}
	sum, cnt := map[key]int64{}, map[key]int64{}
	for _, v := range viewers {
		// Viewers 0 es "sin dato" (el canal oculta el contador), no "nadie mirando".
		if i := index(v.At); want[v.VideoID] && v.Viewers > 0 && i >= 0 {
			sum[key{i, v.VideoID}] += v.Viewers
			cnt[key{i, v.VideoID}]++
		}
	}
	total := make([]int64, slotCount)
	for k, n := range cnt {
		total[k.slot] += (sum[k] + n/2) / n // promedio redondeado
	}
	var pcts []float64
	for i := range slots {
		s := &slots[i]
		s.Authors = len(authors[i])
		if total[i] > 0 {
			s.Viewers = total[i]
			s.Pct = float64(s.Authors) / float64(s.Viewers) * 100
			pcts = append(pcts, s.Pct)
		}
	}
	if len(pcts) == 0 {
		return slots, 0, false
	}
	return slots, median(pcts), true
}

func median(xs []float64) float64 {
	xs = append([]float64(nil), xs...)
	sort.Float64s(xs)
	n := len(xs)
	if n%2 == 1 {
		return xs[n/2]
	}
	return (xs[n/2-1] + xs[n/2]) / 2
}

// Moment es una de las franjas con más participación. Es una franja sola y
// no un grupo: así el % se compara entre intervalos de la misma duración
// (las personas se acumulan en el intervalo y "mirando" es instantáneo).
type Moment struct {
	Slot             // la franja de 5 minutos (N, Authors, Viewers, Pct)
	Rank     int     // 1 a maxMomentos
	VideoID  string  // vivo con más mensajes en la franja: el que se linkea para el reel
	Program  string  // programa más frecuente en la franja
	Topics   []Count // temas de la franja
	Examples []Message
}

// eligible dice si la franja entra en el ranking por %.
func eligible(s Slot) bool { return s.Viewers > 0 && s.Authors >= minPersonasTop }

// ranksBefore ordena el ranking: más %, después más personas, después la más reciente.
func ranksBefore(a, b Slot) bool {
	if a.Pct != b.Pct {
		return a.Pct > b.Pct
	}
	if a.Authors != b.Authors {
		return a.Authors > b.Authors
	}
	return a.Start.After(b.Start)
}

// TopMoments devuelve las franjas con más % de participación (personas que
// escribieron sobre los que miraban), separadas al menos minSeparacion entre
// sí. Solo entran las que tienen dato de espectadores y minPersonasTop personas.
func TopMoments(slots []Slot, titles map[string]string) []Moment {
	var ranked []Slot
	for _, s := range slots {
		if eligible(s) {
			ranked = append(ranked, s)
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranksBefore(ranked[i], ranked[j]) })
	var out []Moment
	for _, s := range ranked {
		if len(out) == maxMomentos {
			break
		}
		near := false
		for _, m := range out {
			d := s.Start.Sub(m.Start)
			near = near || d > -minSeparacion && d < minSeparacion
		}
		if !near {
			out = append(out, newMoment(s, len(out)+1, titles))
		}
	}
	return out
}

func newMoment(s Slot, rank int, titles map[string]string) Moment {
	m := Moment{Slot: s, Rank: rank}
	msgs := append([]Message(nil), s.Msgs...)
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].At.Before(msgs[j].At) })
	m.Program = mainProgram(msgs, titles)
	m.VideoID = mainVideo(msgs)
	m.Topics = TopTopics(msgs, 3)
	topic := ""
	if len(m.Topics) > 0 {
		topic = " " + m.Topics[0].Key + " "
	}
	for _, msg := range msgs {
		if len(m.Examples) == 2 {
			break
		}
		// Un Super Sticker (o un Super Chat sin comentario) no tiene texto: cuenta
		// como participación pero no sirve de ejemplo.
		if strings.TrimSpace(msg.Text) == "" {
			continue
		}
		if c := Classify(msg.Text); c == CatSpam || c == CatInsulto {
			continue
		}
		if topic == "" || strings.Contains(" "+strings.Join(textWords(msg.Text), " ")+" ", topic) {
			m.Examples = append(m.Examples, msg)
		}
	}
	return m
}

// mainVideo devuelve el vivo con más mensajes (si empatan, el primero que llegó a esa cantidad).
func mainVideo(msgs []Message) string {
	counts := map[string]int{}
	best := ""
	for _, m := range msgs {
		counts[m.VideoID]++
		if counts[m.VideoID] > counts[best] {
			best = m.VideoID
		}
	}
	return best
}

// mainProgram devuelve el programa con más mensajes (si empatan, el primero que llegó a esa cantidad).
func mainProgram(msgs []Message, titles map[string]string) string {
	counts := map[string]int{}
	best := ""
	for _, m := range msgs {
		p := programOf(m, titles)
		if p == "" {
			continue
		}
		counts[p]++
		if counts[p] > counts[best] {
			best = p
		}
	}
	return best
}

// SuperTotal es la cantidad y la suma de pagos de una moneda (y, en
// ByProgram, de un programa). La suma va en micros y nunca mezcla monedas.
type SuperTotal struct {
	Program  string
	Currency string
	N        int
	Micros   int64
}

// SuperSummary son los totales de Super Chats y Super Stickers.
type SuperSummary struct {
	ByCurrency []SuperTotal // más pagos primero, después código de moneda
	ByProgram  []SuperTotal // programa con más pagos primero; adentro, por moneda
	Last       []Message    // los últimos 10, del más nuevo al más viejo
}

// Supers suma los pagos por moneda y por programa. Son los montos brutos que
// pagó cada persona, en su moneda y sin convertir: no es lo que cobra el canal.
func Supers(msgs []Message, titles map[string]string) SuperSummary {
	var sum SuperSummary
	byCur := map[string]*SuperTotal{}
	byProg := map[[2]string]*SuperTotal{}
	perProg := map[string]int{}
	for _, m := range msgs {
		if m.Super == "" {
			continue
		}
		c := byCur[m.Currency]
		if c == nil {
			c = &SuperTotal{Currency: m.Currency}
			byCur[m.Currency] = c
		}
		c.N++
		c.Micros += m.AmountMicros
		p := programOf(m, titles)
		k := [2]string{p, m.Currency}
		pc := byProg[k]
		if pc == nil {
			pc = &SuperTotal{Program: p, Currency: m.Currency}
			byProg[k] = pc
		}
		pc.N++
		pc.Micros += m.AmountMicros
		perProg[p]++
		sum.Last = append(sum.Last, m)
	}
	for _, c := range byCur {
		sum.ByCurrency = append(sum.ByCurrency, *c)
	}
	sort.Slice(sum.ByCurrency, func(i, j int) bool {
		a, b := sum.ByCurrency[i], sum.ByCurrency[j]
		if a.N != b.N {
			return a.N > b.N
		}
		return a.Currency < b.Currency
	})
	for _, c := range byProg {
		sum.ByProgram = append(sum.ByProgram, *c)
	}
	sort.Slice(sum.ByProgram, func(i, j int) bool {
		a, b := sum.ByProgram[i], sum.ByProgram[j]
		if perProg[a.Program] != perProg[b.Program] {
			return perProg[a.Program] > perProg[b.Program]
		}
		if a.Program != b.Program {
			return a.Program < b.Program
		}
		return a.Currency < b.Currency
	})
	sort.SliceStable(sum.Last, func(i, j int) bool { return sum.Last[i].At.After(sum.Last[j].At) })
	sum.Last = sum.Last[:min(len(sum.Last), 10)]
	return sum
}

// Estados de un tramo del día de un canal (los textos de la tira).
const (
	SegEnded = "terminó"
	SegLive  = "al aire"
	SegNone  = "sin vivo"
)

// segmentGap: sin muestras de un vivo en más de 2 revisiones de videos (3 min
// cada una) y 1 min de margen, el vivo se cortó o terminó.
const segmentGap = 2*3*time.Minute + time.Minute

// Segment es un tramo del día de un canal: un programa de un vivo o un rato
// sin vivo. Start y End son la primera y la última muestra (hasta 3 min
// después del cambio real); en "sin vivo", del fin de lo anterior al inicio
// de lo siguiente o a ahora.
type Segment struct {
	VideoID, Program string
	Start, End       time.Time
	State            string
}

// dayStart es las 00:00 locales del día de t ("todo el día del canal").
func dayStart(t time.Time) time.Time {
	t = t.Local()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
}

// ProgramSegments arma los programas de un canal entre from y now con las
// muestras de vivos: un tramo nuevo por cada vivo y cada vez que cambia el
// programa del título; las muestras repetidas (por ejemplo, tras reiniciar)
// se juntan, y un hueco de más de segmentGap corta el tramo y deja uno "sin
// vivo". live son los vivos del canal en curso ahora (de la lista de videos):
// solo el último tramo de cada uno está al aire, así el programa anterior
// termina apenas cambia el título y no recién cuando pasa segmentGap. Si
// nada está al aire, termina con un "sin vivo" hasta now.
func ProgramSegments(samples []LiveSample, channelID string, live map[string]bool, from, now time.Time) []Segment {
	var mine []LiveSample
	for _, s := range samples {
		if s.ChannelID == channelID && !s.At.Before(from) && !s.At.After(now) {
			mine = append(mine, s)
		}
	}
	sort.SliceStable(mine, func(i, j int) bool { return mine[i].At.Before(mine[j].At) })
	var segs []Segment
	open := map[string]int{} // vivo → su último tramo
	for _, s := range mine {
		p := ProgramName(s.Title, s.Channel)
		if i, ok := open[s.VideoID]; ok && segs[i].Program == p && s.At.Sub(segs[i].End) <= segmentGap {
			segs[i].End = s.At
			continue
		}
		open[s.VideoID] = len(segs)
		segs = append(segs, Segment{VideoID: s.VideoID, Program: p, Start: s.At, End: s.At})
	}
	var out []Segment
	var covered time.Time // hasta dónde hubo algún vivo
	onAir := false
	for i, sg := range segs {
		if !covered.IsZero() && sg.Start.Sub(covered) > segmentGap {
			out = append(out, Segment{Start: covered, End: sg.Start, State: SegNone})
		}
		sg.State = SegEnded
		if open[sg.VideoID] == i && live[sg.VideoID] {
			sg.State, onAir = SegLive, true
		}
		out = append(out, sg)
		if sg.End.After(covered) {
			covered = sg.End
		}
	}
	switch {
	case covered.IsZero():
		out = append(out, Segment{Start: from, End: now, State: SegNone})
	case !onAir:
		out = append(out, Segment{Start: covered, End: now, State: SegNone})
	}
	return out
}

// ContinuityBanner explica el último cambio de programa de hoy: si fue en el
// mismo vivo o si empezó otro y el monitor lo enganchó. Vacío si no hubo
// cambio hoy.
func ContinuityBanner(segs []Segment, now time.Time) string {
	var progs []Segment
	for _, s := range segs {
		if s.State != SegNone {
			progs = append(progs, s)
		}
	}
	if len(progs) < 2 {
		return ""
	}
	last := progs[len(progs)-1]
	var prev *Segment
	for i := range progs[:len(progs)-1] {
		if p := &progs[i]; p.Start.Before(last.Start) && (prev == nil || !p.End.Before(prev.End)) {
			prev = p
		}
	}
	if prev == nil || !dayStart(last.Start).Equal(dayStart(now)) {
		return ""
	}
	at := last.Start.Local().Format("15:04")
	if prev.VideoID == last.VideoID {
		if prev.Program == last.Program || last.Start.Sub(prev.End) > segmentGap {
			return "" // el mismo programa después de un corte: no hubo cambio que contar
		}
		return "A las " + at + " cambió el programa y el chat siguió sin cortes"
	}
	msg := "A las " + at + " empezó un vivo nuevo y el monitor lo enganchó solo."
	if prev.State != SegLive {
		msg += " El vivo de " + prev.Program + " terminó a las " + prev.End.Local().Format("15:04") + "."
	}
	return msg
}
