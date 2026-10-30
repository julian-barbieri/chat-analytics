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

// Offender es alguien que mandó spam o insultos más de una vez.
type Offender struct {
	Author         string
	Spam, Insultos int
}

func (o Offender) Total() int { return o.Spam + o.Insultos }

// Analysis es lo que muestra el dashboard de cada vivo.
type Analysis struct {
	Total      int
	Topics     []Count   // temas: palabras o pares de palabras, en cuántos mensajes aparecen
	Questions  []Message // preguntas que vale la pena mirar
	Tech       []Count   // quejas técnicas por tipo
	TechMsgs   []Message
	Offenders  []Offender
	Filters    []Count // palabras de las listas de spam e insultos que aparecieron
	Candidates []Count // palabras que casi solo aparecen en spam e insultos: revisarlas antes de cargarlas
	Links      int     // mensajes con links
}

func Analyze(msgs []Message) Analysis {
	a := Analysis{Total: len(msgs), Topics: TopTopics(msgs, 5)}
	tech := map[string]int{}
	byAuthor := map[string]*Offender{}
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
		o := byAuthor[m.Author]
		if o == nil {
			o = &Offender{Author: m.Author}
			byAuthor[m.Author] = o
		}
		if cat == CatSpam {
			o.Spam++
		} else {
			o.Insultos++
		}
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

	for _, o := range byAuthor {
		if o.Total() >= 2 {
			a.Offenders = append(a.Offenders, *o)
		}
	}
	sort.Slice(a.Offenders, func(i, j int) bool {
		if a.Offenders[i].Total() != a.Offenders[j].Total() {
			return a.Offenders[i].Total() > a.Offenders[j].Total()
		}
		return a.Offenders[i].Author < a.Offenders[j].Author
	})
	a.Offenders = a.Offenders[:min(len(a.Offenders), 10)]

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

// Participation arma las franjas de las últimas 24 h del chat de un vivo y la
// participación típica (mediana del % de las franjas con espectadores). Las
// personas se cuentan por nombre visible y los espectadores son los que
// YouTube informa "mirando ahora": es una aproximación.
func Participation(msgs []Message, viewers []ViewerSample, videoID string, now time.Time) (slots []Slot, typical float64, ok bool) {
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
		if m.Source != "chat" || m.VideoID != videoID || i < 0 {
			continue
		}
		slots[i].Msgs = append(slots[i].Msgs, m)
		slots[i].N++
		if m.Author != "" {
			authors[i][m.Author] = true
		}
	}
	sum, cnt := make([]int64, slotCount), make([]int64, slotCount)
	for _, v := range viewers {
		// Viewers 0 es "sin dato" (el canal oculta el contador), no "nadie mirando".
		if i := index(v.At); v.VideoID == videoID && v.Viewers > 0 && i >= 0 {
			sum[i] += v.Viewers
			cnt[i]++
		}
	}
	var pcts []float64
	for i := range slots {
		s := &slots[i]
		s.Authors = len(authors[i])
		if cnt[i] > 0 {
			s.Viewers = (sum[i] + cnt[i]/2) / cnt[i] // promedio redondeado
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
	m.Topics = TopTopics(msgs, 3)
	topic := ""
	if len(m.Topics) > 0 {
		topic = " " + m.Topics[0].Key + " "
	}
	for _, msg := range msgs {
		if len(m.Examples) == 2 {
			break
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
