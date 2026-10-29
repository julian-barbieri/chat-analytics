package main

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Categorías simples por reglas. Es un primer filtro: el análisis fino
// (temas, tono, borradores de respuesta) lo hace Claude leyendo el CSV.
const (
	CatSpam     = "spam"
	CatInsulto  = "insulto"
	CatPregunta = "pregunta"
	CatQueja    = "queja"
	CatElogio   = "elogio"
	CatOtro     = "otro"
)

var (
	reLink     = regexp.MustCompile(`(?i)(https?://|www\.|\.com\b|\.ar\b|t\.me/|wa\.me/|bit\.ly)`)
	rePhone    = regexp.MustCompile(`\+?\d[\d\s-]{8,}\d`)
	reWord     = regexp.MustCompile(`[\p{L}\p{N}]+`)
	reEmoji    = regexp.MustCompile(`:[\p{L}\p{N}_-]+:`) // el chat trae los emojis como texto: ":hand-pink-waving:"
	spamWords  = []string{"whatsapp", "telegram", "invert", "cripto", "crypto", "bitcoin", "ganá dinero", "gana dinero", "suscribite a mi", "mi canal", "forex"}
	insultos   = []string{"idiota", "imbecil", "imbécil", "pelotudo", "boludo", "hdp", "forro", "basura", "mierda", "inútil", "inutil", "ladri", "corrupto"}
	quejas     = []string{"no se escucha", "se corta", "se cortó", "sin audio", "mal audio", "no anda", "se escucha mal", "cortan", "publicidad", "tanda", "lag", "trabado"}
	elogios    = []string{"genio", "grande", "excelente", "gracias", "bravo", "felicitaciones", "saludos", "abrazo", "lo mejor", "capo", "idolo", "ídolo"}
	preguntaQs = []string{"qué ", "que hora", "cuándo", "cuando ", "dónde", "donde ", "cómo ", "por qué", "quién", "quien "}
	stopwords  = map[string]bool{}
)

func init() {
	for _, w := range strings.Fields(`de la que el en y a los se del las un por con no una su para es al lo como más mas pero sus le ya o este sí si porque esta entre cuando muy sin sobre también me hasta hay donde quien desde todo nos durante todos uno les ni contra otros ese eso ante ellos e esto mí antes algunos qué unos yo otro otras otra él tanto esa estos mucho quienes nada muchos cual poco ella estar estas algunas algo nosotros mi mis tú te ti tu tus ellas nosotras vosotros vosotras os mío mía míos mías tuyo tuya tuyos tuyas suyo suya suyos suyas nuestro nuestra nuestros nuestras vuestro vuestra vuestros vuestras esos esas estoy estás está estamos estáis están ser es son fue era hace hoy ahora bien va van tiene tienen hay jaja jajaja jajajaja q x k che hola buenas buenos buen tardes noches días dias saludos abrazo gracias`) {
		stopwords[w] = true
	}
}

func Classify(text string) string {
	t := strings.ToLower(text)
	switch {
	case reLink.MatchString(t) || rePhone.MatchString(t) || containsAny(t, spamWords):
		return CatSpam
	case containsAny(t, insultos):
		return CatInsulto
	case strings.Contains(t, "?") || strings.HasPrefix(t, "¿") || hasPrefixAny(t, preguntaQs):
		return CatPregunta
	case containsAny(t, quejas):
		return CatQueja
	case containsAny(t, elogios):
		return CatElogio
	}
	if shouting(text) {
		return CatInsulto
	}
	return CatOtro
}

// shouting detecta mensajes largos casi todo en mayúsculas.
func shouting(s string) bool {
	letters, upper := 0, 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters++
			if unicode.IsUpper(r) {
				upper++
			}
		}
	}
	return letters >= 20 && upper*10 >= letters*8
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

func hasPrefixAny(s string, words []string) bool {
	for _, w := range words {
		if strings.HasPrefix(s, w) {
			return true
		}
	}
	return false
}

type Count struct {
	Key string
	N   int
}

// TopWords devuelve las palabras más repetidas (sin stopwords).
func TopWords(msgs []Message, n int) []Count {
	freq := map[string]int{}
	for _, m := range msgs {
		seen := map[string]bool{}
		for _, w := range textWords(m.Text) {
			if len([]rune(w)) < 4 || stopwords[w] || seen[w] {
				continue
			}
			seen[w] = true
			freq[w]++
		}
	}
	return topN(freq, n)
}

// textWords devuelve las palabras del mensaje en minúsculas, sin los emojis del chat.
func textWords(text string) []string {
	return reWord.FindAllString(reEmoji.ReplaceAllString(strings.ToLower(text), " "), -1)
}

// TopAuthors devuelve quiénes más escribieron (sirve para detectar flood).
func TopAuthors(msgs []Message, n int) []Count {
	freq := map[string]int{}
	for _, m := range msgs {
		freq[m.Author]++
	}
	return topN(freq, n)
}

// TopPrograms cuenta mensajes de chat por programa (sirve para comparar
// programas dentro de un mismo vivo de 24 horas).
func TopPrograms(msgs []Message, vids []Video, n int) []Count {
	titles := videoTitles(vids)
	freq := map[string]int{}
	for _, m := range msgs {
		if p := programOf(m, titles); p != "" {
			freq[p]++
		}
	}
	return topN(freq, n)
}

func topN(freq map[string]int, n int) []Count {
	out := make([]Count, 0, len(freq))
	for k, v := range freq {
		out = append(out, Count{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}
