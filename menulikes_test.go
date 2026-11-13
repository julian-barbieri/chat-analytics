package main

import (
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

// Tests de la spec menu-likes-temas: el selector "Ver" del gráfico de
// Comentarios y la tarjeta "De qué se habla" de En vivo y Programas. El menú
// lateral está en TestDashboardTabs.

// TestCommentsQueryVer (CA4): "ver" sale de una lista cerrada y no se refleja
// si es el defecto o no está en la lista.
func TestCommentsQueryVer(t *testing.T) {
	for _, c := range []struct{ in, want, link string }{
		{"", "comentarios", "/comentarios"},
		{"comentarios", "comentarios", "/comentarios"},
		{"likes", "likes", "/comentarios?ver=likes"},
		{"dos", "dos", "/comentarios?ver=dos"},
		{"<script>", "comentarios", "/comentarios"},
	} {
		q := commentsQuery(url.Values{"ver": {c.in}})
		if q.Ver != c.want || q.Page() != c.link {
			t.Errorf("ver=%q: Ver %q, link %q; quería %q, %q", c.in, q.Ver, q.Page(), c.want, c.link)
		}
	}
	// Abrir un video y volver conserva lo elegido.
	q := commentsQuery(url.Values{"ver": {"likes"}})
	if back := q.ToVideo("abcdefghijk").Back(); back.Ver != "likes" {
		t.Errorf("volver del detalle: Ver %q", back.Ver)
	}
}

// TestDayChartViews (CA5): con "comentarios" sin línea, con "likes" barras de
// likes con su escala y con "dos" como antes.
func TestDayChartViews(t *testing.T) {
	d0 := dayStart(noonToday()).AddDate(0, 0, -2)
	days := []CommentDay{
		{Day: d0, N: 4, Likes: 10},
		{Day: d0.AddDate(0, 0, 1), N: 2, Likes: 30},
		{Day: d0.AddDate(0, 0, 2), N: 0, Weekend: true},
	}
	cases := []struct {
		ver      string
		line     bool
		bar1     int
		class1   string
		label1   string
		ariaHead string
	}{
		{"comentarios", false, 2, "", "2 comentarios", "Comentarios por día"},
		{"likes", false, 30, "lk", "30 likes nuevos", "Likes nuevos por día"},
		{"dos", true, 2, "", "2 comentarios", "Comentarios por día"},
	}
	for _, c := range cases {
		ch := dayChart(days, c.ver)
		if (ch.Likes != "") != c.line {
			t.Errorf("%s: línea de likes %q", c.ver, ch.Likes)
		}
		if b := ch.Bars[1]; b.N != c.bar1 || b.Class != c.class1 || !strings.Contains(b.Label, c.label1) {
			t.Errorf("%s: barra = %+v", c.ver, b)
		}
		if !strings.HasPrefix(ch.Aria, c.ariaHead) {
			t.Errorf("%s: aria %q", c.ver, ch.Aria)
		}
		if c.ver == "likes" && ch.Bars[2].Class != "lk" {
			t.Errorf("likes: el fin de semana también va con el color de likes: %+v", ch.Bars[2])
		}
	}
	// Sin likes no hay nada que mostrar: "likes" cae a los comentarios.
	empty := []CommentDay{{Day: d0, N: 3}}
	if ch := dayChart(empty, "likes"); ch.Bars[0].N != 3 || ch.Bars[0].Class == "lk" {
		t.Errorf("likes sin likes: %+v", ch.Bars[0])
	}
}

// commentsServer arma la pantalla Comentarios con los datos de prueba y, si
// likes, las muestras de likes en orden.
func commentsServer(t *testing.T, now time.Time, likes bool) *Server {
	t.Helper()
	st, err := OpenStore(t.TempDir() + "/datos.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	msgs, vids := commentsFixture(now)
	st.SetChannels([]ChannelStatus{{ID: "UC1", Name: "Radio"}, {ID: "UC2", Name: "Otra"}})
	st.Add(msgs)
	for _, v := range vids {
		st.AddVideos(now, []Video{{ID: v.ID, Title: v.Title, ChannelID: v.ChannelID, Channel: v.Channel, PublishedAt: v.PublishedAt,
			Views: v.Views, Short: v.Short, Comments: v.Comments, LiveStatus: "none"}})
	}
	if likes {
		var all []LikeSample
		for _, ss := range commentsLikes(now) {
			all = append(all, ss...)
		}
		sort.Slice(all, func(i, j int) bool { return all[i].At.Before(all[j].At) })
		for _, l := range all {
			st.AddLikes(l.At, []Video{{ID: l.VideoID, Likes: l.Likes, LiveStatus: "none"}})
		}
	}
	return &Server{st: st, now: func() time.Time { return now }}
}

// TestCommentsChartSelector (CA4, CA5): el selector aparece solo con likes,
// marca lo elegido y el título, la leyenda y la aclaración siguen la elección.
func TestCommentsChartSelector(t *testing.T) {
	now := noonToday()
	h := commentsServer(t, now, true).Routes()
	cases := []struct {
		path      string
		on        string
		want, not []string
	}{
		{"/comentarios", "Comentarios", []string{">Comentarios por día</h2>", `<span class="sw pos"`},
			[]string{`class="likesl"`, "cada serie con su escala", `<span class="sw lk"`}},
		{"/comentarios?ver=likes", "Likes", []string{">Likes nuevos por día</h2>", `class="bar lk"`, `<span class="sw lkb"`},
			[]string{`class="likesl"`, "cada serie con su escala", `<span class="sw pos"`}},
		{"/comentarios?ver=dos", "Los dos", []string{">Comentarios y likes por día</h2>", `class="likesl"`, "cada serie con su escala"}, nil},
	}
	for _, c := range cases {
		body := getBody(h, c.path)
		for _, w := range append(c.want, `aria-label="Qué ver en el gráfico"`, `aria-current="true">`+c.on+`</a>`) {
			if !strings.Contains(body, w) {
				t.Errorf("%s: falta %q", c.path, w)
			}
		}
		for _, w := range c.not {
			if strings.Contains(body, w) {
				t.Errorf("%s: sobra %q", c.path, w)
			}
		}
	}
	// Sin likes guardados no hay selector.
	body := getBody(commentsServer(t, now, false).Routes(), "/comentarios?ver=likes")
	if strings.Contains(body, "Qué ver en el gráfico") || !strings.Contains(body, ">Comentarios por día</h2>") {
		t.Error("sin likes: no tendría que haber selector y el gráfico es de comentarios")
	}
}

// TestTalkView (CA6, CA7): temas de Claude si hay; si no, los pares de
// palabras por reglas (una palabra sola ya está en la lista de abajo).
func TestTalkView(t *testing.T) {
	now := noonToday()
	var msgs []Message
	add := func(n int, text string, ago time.Duration) {
		for range n {
			msgs = append(msgs, Message{ID: "m" + string(rune('a'+len(msgs))), Text: text, At: now.Add(-ago)})
		}
	}
	add(6, "dolar blue hoy", 2*time.Hour)
	add(4, "saludos desde rosario", 2*time.Hour)
	add(5, "penal penal", 5*time.Minute)
	words := TopWords(msgs, wordsShown)
	topics := TopTopics(msgs, 5)

	rules := talkView(msgs, words, topics, nil, true, now)
	if len(rules.Claude) > 0 || len(rules.Rules) != 1 || rules.Rules[0].Key != "dolar blue" {
		t.Fatalf("por reglas, solo el par: %+v", rules.Rules)
	}
	if len(rules.Words) != len(words) || rules.Total != len(msgs) {
		t.Errorf("palabras %d de %d, total %d", len(rules.Words), len(words), rules.Total)
	}

	claude := []ClaudeItem{{Text: "El dólar", Count: "6 mensajes · 1 persona"}}
	with := talkView(msgs, words, topics, claude, true, now)
	if len(with.Claude) != 1 || len(with.Rules) != 0 {
		t.Fatalf("con Claude: %+v", with)
	}
	if len(with.Words) != len(words) {
		t.Errorf("con Claude van todas las palabras: %d de %d", len(with.Words), len(words))
	}
	// "penal" sube en la última media hora; sin rising (un programa que ya
	// terminó) no se marca nada.
	up := false
	for _, w := range with.Words {
		up = up || w.Key == "penal" && w.Up
	}
	if !up || !with.AnyUp {
		t.Errorf("penal tendría que estar en alza: %+v", with.Words)
	}
	if done := talkView(msgs, words, topics, claude, false, now); done.AnyUp {
		t.Error("sin rising no se marca nada en alza")
	}
}

// TestTalkCardLive (CA6, CA7): En vivo tiene "De qué se habla" con temas por
// reglas y palabras; con un análisis del programa, los temas son de Claude y
// salen de la tarjeta de Claude.
func TestTalkCardLive(t *testing.T) {
	now := noonToday()
	day := dayStart(now)
	tarde := Segment{VideoID: "v1", Program: "LA TARDE", Start: day.Add(10 * time.Hour)}
	st, _ := analystServer(t, now)
	st.SetClaudeStatus(ClaudeStatus{On: true, Model: defaultClaudeModel, Tope: 2_000_000})
	var msgs []Message
	for i := range 12 {
		msgs = append(msgs, Message{ID: "tl" + string(rune('a'+i)), Source: "chat", VideoID: "v1", Program: "LA TARDE", Author: "Ana",
			Text: "el dolar blue sube", At: day.Add(10*time.Hour + time.Duration(i)*time.Minute)})
	}
	// "golazo" sale recién en los últimos 5 minutos: va con ▲.
	for i := range 4 {
		msgs = append(msgs, Message{ID: "tg" + string(rune('a'+i)), Source: "chat", VideoID: "v1", Program: "LA TARDE", Author: "Beto",
			Text: "que golazo", At: now.Add(-5*time.Minute + time.Duration(i)*time.Second)})
	}
	st.Add(msgs)
	h := (&Server{st: st, budget: 9000, analyst: NewAnalyst(), now: func() time.Time { return now }}).Routes()
	base := "/parcial/vivo/analisis?canal=UC1&video=v1&ver=programa&pestana=temas"
	body := card(getBody(h, base), `id="palabras"`)
	for _, w := range []string{"De qué se habla", `Temas de conversación <span class="chip">por reglas</span>`, ">blue sube<", "Palabras más repetidas del programa en curso",
		`<span>dolar <span class="muted">12</span>`, "cuando termina el programa o con «Analizar ahora»",
		`<span>golazo <span class="muted">4</span> <span class="wup" title="En alza en la última media hora">▲</span></span>`, "▲ En alza: en la última media hora"} {
		if !strings.Contains(body, w) {
			t.Errorf("por reglas: falta %q en:\n%s", w, body)
		}
	}
	st.AddAnalysis(ClaudeAnalysis{At: now.Add(-10 * time.Minute), Key: segmentKey("UC1", tarde), ChannelID: "UC1", VideoID: "v1", Program: "LA TARDE",
		Model: defaultClaudeModel, From: tarde.Start, To: now.Add(-10 * time.Minute), Partial: true, Sent: 12, Total: 12,
		Result: ClaudeResult{Summary: "Resumen", Topics: []ClaudeGroup{{Text: "La suba del dólar", Msgs: 12, People: 1, Examples: []string{"tla"}}}}})
	full := getBody(h, base)
	body = card(full, `id="palabras"`)
	for _, w := range []string{`Temas de conversación <span class="ia">Claude</span>`, `<summary id="plegar-tema1"`, "La suba del dólar · 12 mensajes · 1 persona",
		"parcial", "Palabras más repetidas"} {
		if !strings.Contains(body, w) {
			t.Errorf("con Claude: falta %q en:\n%s", w, body)
		}
	}
	if strings.Contains(body, "por reglas") {
		t.Errorf("con Claude: sobra «por reglas»")
	}
	if c := card(full, `id="claude-card"`); strings.Contains(c, "La suba del dólar") || strings.Contains(c, "Temas:") {
		t.Errorf("los temas no se repiten en la tarjeta de Claude:\n%s", c)
	}
	// La tarjeta que responde "Analizar ahora" tampoco repite los temas.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, postClaude("canal=UC1&video=v1", nil))
	if b := w.Body.String(); w.Code != 200 || !strings.Contains(b, `id="claude-card"`) || strings.Contains(b, "Temas:") || strings.Contains(b, "plegar-tema") {
		t.Errorf("respuesta del botón (%d) con los temas:\n%s", w.Code, b)
	}
	// Sin vivo (todo el día): por reglas.
	st.SetVideos("Radio Test", nil)
	if c := card(getBody(h, "/parcial/vivo/analisis?canal=UC1&video=v1&pestana=temas"), `id="palabras"`); !strings.Contains(c, `<span class="chip">por reglas</span>`) {
		t.Errorf("todo el día: los temas van por reglas:\n%s", c)
	}
}

// TestTalkCardProgram (CA6, CA8): el detalle de Programas tiene la misma
// tarjeta, con las palabras más repetidas.
func TestTalkCardProgram(t *testing.T) {
	now := noonToday()
	_, srv := programServer(t, now, false)
	key := segmentKey("UC1", Segment{VideoID: "v1", Program: "LA MAÑANA", Start: dayStart(now).Add(8 * time.Hour)})
	body := card(getBody(srv.Routes(), "/parcial/programas/detalle?pestana=conversacion&tramo="+url.QueryEscape(key)), `id="palabras"`)
	for _, w := range []string{"De qué se habla", `Temas de conversación <span class="chip">por reglas</span>`, "Palabras más repetidas", `class="words"`} {
		if !strings.Contains(body, w) {
			t.Errorf("falta %q en:\n%s", w, body)
		}
	}
}

// card recorta del HTML la sección que empieza en marker (hasta su </section>).
func card(body, marker string) string {
	i := strings.Index(body, marker)
	if i < 0 {
		return ""
	}
	body = body[i:]
	if j := strings.Index(body, "</section>"); j >= 0 {
		body = body[:j]
	}
	return body
}
