package main

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Tests de la spec rediseno-envivo-programas.

// from recorta el HTML desde marker hasta end (o hasta el final si no está).
func from(body, marker, end string) string {
	i := strings.Index(body, marker)
	if i < 0 {
		return ""
	}
	body = body[i:]
	if end != "" {
		if j := strings.Index(body[len(marker):], end); j >= 0 {
			body = body[:len(marker)+j]
		}
	}
	return body
}

// motivesServer: el vivo de LA TARDE con negativos y positivos en los
// últimos 5 min y sus motivos.
func motivesServer(t *testing.T, now time.Time, claude bool) *Server {
	t.Helper()
	st := liveStore(t, now)
	srv := &Server{st: st, budget: 9000, now: func() time.Time { return now }, analyst: NewAnalyst()}
	if !claude {
		return srv
	}
	st.SetClaudeStatus(ClaudeStatus{On: true, Model: defaultClaudeModel, Tope: 2_000_000})
	cur := chatRun("s", "b2", "LA TARDE", now.Add(-3*time.Minute), 5*time.Second, 6)
	st.Add(cur)
	var ls []MessageLabel
	for i, m := range cur {
		sent := "positivo"
		if i < 2 {
			sent = "negativo"
		}
		ls = append(ls, MessageLabel{ID: m.ID, VideoID: "b2", At: m.At, Program: "LA TARDE", Sentiment: sent})
	}
	st.AddBatch(ClaudeBatch{At: now, ChannelID: "UCB", From: cur[0].At, To: cur[5].At, Labels: ls,
		Motives: []BatchMotive{{Kind: "negativo", Text: "No les gusta el invitado", IDs: []string{"s0", "s1"}},
			{Kind: "positivo", Text: "La música", IDs: []string{"s2", "s3"}}}})
	return srv
}

// TestLiveNowLayout (CA1): en Ahora el chat va a lo ancho, debajo el
// sentimiento sin los motivos y debajo #motivos con el filtro y tres columnas.
func TestLiveNowLayout(t *testing.T) {
	now := noonLocal(t)
	srv := motivesServer(t, now, true)
	body := getBody(srv.Routes(), "/parcial/vivo/analisis?canal=UCB&video=b2")
	sent := from(body, `<section id="sentimiento"`, `<section id="motivos"`)
	mot := from(body, `<section id="motivos"`, "")
	if sent == "" || mot == "" {
		t.Fatalf("faltan #sentimiento o #motivos (o están fuera de orden):\n%s", body)
	}
	for _, w := range []string{"Por qué hay mensajes negativos", "Qué está gustando", `id="mot-5"`} {
		if strings.Contains(sent, w) {
			t.Errorf("%q sigue dentro de #sentimiento", w)
		}
	}
	for _, w := range []string{`class="clima"`, `class="schart"`, `class="sgrid"`} {
		if !strings.Contains(sent, w) {
			t.Errorf("#sentimiento: falta %q", w)
		}
	}
	last := -1
	for _, w := range []string{`id="mot-5" class="vbtn" aria-pressed="true"`, `id="mot-30"`, `id="mot-60"`, `id="mot-programa"`, `class="mcols"`,
		"Por qué hay mensajes negativos", "No les gusta el invitado", "Qué está gustando", "La música", `<section id="preguntas"`} {
		i := strings.Index(mot, w)
		if i < 0 || i < last {
			t.Errorf("#motivos: %q en %d (el anterior en %d)", w, i, last)
		}
		last = max(last, i)
	}
	// El CSS de Ahora: el chat a lo ancho y sin sticky, una fila por mensaje,
	// y el orden chat → sentimiento → motivos.
	page := getBody(srv.Routes(), "/?canal=UCB")
	for _, w := range []string{
		"#vivo.t-ahora #chat { order: 1; }", "#vivo.t-ahora #sentimiento { order: 2; }", "#vivo.t-ahora #motivos { order: 3; }",
		"#vivo.t-ahora #chat .vfeed li { display: grid;",
		".mcols { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr));",
	} {
		if !strings.Contains(page, w) {
			t.Errorf("CSS: falta %q", w)
		}
	}
	if strings.Contains(page, "position: sticky; top: 16px") {
		t.Error("el chat de Ahora ya no es sticky")
	}
}

// questionsWindowServer: un grupo con 3 preguntas hace 2 min y otro con 2
// hace 40 min, dentro de LA TARDE.
func questionsWindowServer(t *testing.T, now time.Time, recent bool) *Server {
	t.Helper()
	st := liveStore(t, now)
	st.SetClaudeStatus(ClaudeStatus{On: true, Model: defaultClaudeModel, Tope: 2_000_000})
	srv := &Server{st: st, budget: 9000, now: func() time.Time { return now }, analyst: NewAnalyst()}
	var msgs []Message
	var labels []MessageLabel
	add := func(g string, n int, ago time.Duration) {
		for i := range n {
			id := fmt.Sprintf("q%s%d", g, i)
			at := now.Add(-ago - time.Duration(i)*time.Second)
			msgs = append(msgs, Message{ID: id, Source: "chat", VideoID: "b2", Program: "LA TARDE", Author: "Ana", Text: "¿algo?", At: at})
			labels = append(labels, MessageLabel{ID: id, VideoID: "b2", At: at, Program: "LA TARDE", Sentiment: "neutro", Cat: CatPregunta, Group: "UCB|2|" + g})
		}
	}
	if recent {
		add("a", 3, 2*time.Minute)
	}
	add("b", 2, 40*time.Minute)
	// Antes del inicio de LA TARDE (hace 1 h), dentro de los 5 min de
	// segmentLead que el programa en curso ya toma como suyos.
	add("c", 1, 62*time.Minute)
	st.Add(msgs)
	st.AddBatch(ClaudeBatch{At: now, ChannelID: "UCB", To: now, Labels: labels, NewGroups: []BatchGroup{
		{ID: "UCB|2|a", Program: "LA TARDE", Text: "¿Pregunta reciente?"},
		{ID: "UCB|2|b", Program: "LA TARDE", Text: "¿Pregunta vieja?"},
		{ID: "UCB|2|c", Program: "LA TARDE", Text: "¿Pregunta del arranque?"},
	}})
	return srv
}

// TestQuestionsWindow (CA2, CA3): "Preguntas agrupadas" con la ventana de los
// motivos; el número de arriba cuenta todo el alcance; sin Claude, por reglas
// y sin filtro.
func TestQuestionsWindow(t *testing.T) {
	now := noonLocal(t)
	h := questionsWindowServer(t, now, true).Routes()
	tile := regexp.MustCompile(`<div class="l">Preguntas</div>\s*<div class="v">(\d+)</div>`)
	cases := []struct {
		name, q   string
		want, not []string
	}{
		{"por defecto, 5 min", "", []string{"3 preguntas · 1 grupo en los últimos 5 min", "¿Pregunta reciente?"}, []string{"¿Pregunta vieja?"}},
		{"1 h", "&motivos=60", []string{"5 preguntas · 2 grupos en la última hora", "¿Pregunta reciente?", "¿Pregunta vieja?",
			// Lo plegado conserva la ventana.
			`<summary id="plegar-qg1" hx-get="/parcial/vivo/analisis?abrir=qg1&amp;canal=UCB&amp;horas=`}, nil},
		// Todo el programa cuenta lo mismo que el número de arriba, también lo
		// de los 5 min antes del inicio.
		{"todo el programa", "&motivos=programa", []string{"6 preguntas · 3 grupos en todo el programa", "¿Pregunta vieja?", "¿Pregunta del arranque?"}, nil},
		{"valor raro", "&motivos=%3Cscript%3E", []string{"3 preguntas · 1 grupo en los últimos 5 min"}, []string{"¿Pregunta vieja?", "<script>", "%3Cscript"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := getBody(h, "/parcial/vivo/analisis?canal=UCB&video=b2"+c.q)
			q := card(body, `<section id="preguntas"`)
			if q == "" {
				t.Fatalf("falta #preguntas:\n%s", body)
			}
			for _, w := range c.want {
				if !strings.Contains(q, w) {
					t.Errorf("falta %q en:\n%s", w, q)
				}
			}
			for _, w := range c.not {
				if strings.Contains(q, w) {
					t.Errorf("sobra %q en:\n%s", w, q)
				}
			}
			if c.q == "&motivos=60" && !strings.Contains(q, "motivos=60") {
				t.Errorf("la flecha del grupo perdió motivos=60:\n%s", q)
			}
			if m := tile.FindStringSubmatch(body); m == nil || m[1] != "6" {
				t.Errorf("el número Preguntas cuenta todo el alcance: %v", m)
			}
		})
	}
	t.Run("ventana sin grupos", func(t *testing.T) {
		body := getBody(questionsWindowServer(t, now, false).Routes(), "/parcial/vivo/analisis?canal=UCB&video=b2")
		q := card(body, `<section id="preguntas"`)
		if !strings.Contains(q, "Sin preguntas agrupadas en los últimos 5 min. Elegí un período más largo para ver las demás.") || strings.Contains(q, "todavía no agrupó") {
			t.Errorf("sin grupos en la ventana:\n%s", q)
		}
	})
	t.Run("sin Claude", func(t *testing.T) {
		body := getBody(motivesServer(t, now, false).Routes(), "/parcial/vivo/analisis?canal=UCB&video=b2")
		mot := from(body, `<section id="motivos"`, "")
		if !strings.Contains(mot, "Preguntas para producción") || strings.Contains(mot, `id="mot-5"`) {
			t.Errorf("sin Claude: preguntas por reglas y sin filtro:\n%s", mot)
		}
	})
}

// TestLiveTopicsLayout (CA4): en De qué se habla, Categorías con el detalle
// al costado y De qué se habla debajo, a lo ancho.
func TestLiveTopicsLayout(t *testing.T) {
	now := noonLocal(t)
	srv := motivesServer(t, now, true)
	body := getBody(srv.Routes(), "/parcial/vivo/analisis?canal=UCB&video=b2&pestana=temas")
	c, p := strings.Index(body, `<section id="categorias"`), strings.Index(body, `<section id="palabras"`)
	if c < 0 || p < c {
		t.Fatalf("categorías en %d, palabras en %d", c, p)
	}
	if strings.Contains(body[:p], `<div class="grid2 tstart">`) {
		t.Error("Categorías y De qué se habla ya no van lado a lado")
	}
	if !strings.Contains(body[p:], `class="tcols2"`) {
		t.Error("De qué se habla: temas y palabras en dos columnas")
	}
	page := getBody(srv.Routes(), "/?canal=UCB&pestana=temas")
	if !strings.Contains(page, "#vivo #categorias .cgauge { display: grid;") {
		t.Error("CSS: el detalle de la categoría va al costado en En vivo")
	}
}

// TestProgramSummaryLayout (CA5): el orden de Resumen, el sentimiento en
// barras verticales con su franja y sin Super Chats.
func TestProgramSummaryLayout(t *testing.T) {
	now := noonToday()
	day := dayStart(now)
	_, srv, key := claudeProgramServer(t, now)
	h := srv.Routes()
	base := "/parcial/programas?dia=hoy&tramo=" + url.QueryEscape(key)
	body := getBody(h, base)
	last := -1
	for _, w := range []string{"<h3>Resumen del chat", "<h3>Participación durante el programa", "<h3>Sentimiento del programa", `class="schart`, "<h3>Por qué hubo mensajes negativos", "<h3>Qué fue lo que gustó"} {
		i := strings.Index(body, w)
		if i < 0 || i < last {
			t.Errorf("%q en %d (el anterior en %d)", w, i, last)
		}
		last = max(last, i)
	}
	for _, w := range []string{`id="supers"`, `class="sfilas"`, `class="tcols"`} {
		if strings.Contains(body, w) {
			t.Errorf("sobra %q en Resumen", w)
		}
	}
	slot := day.Add(8*time.Hour + 5*time.Minute)
	f := fmt.Sprint(slot.Unix())
	if !strings.Contains(body, `<a id="sf-`+f+`" class="sbar"`) || !strings.Contains(body, "desde=sent&amp;dia=hoy&amp;franja="+f) {
		t.Error("cada barra del sentimiento pide su franja")
	}
	if !strings.Contains(body, `class="psent"`) {
		t.Error("falta el promedio arriba de las barras")
	}
	sel := getBody(h, base+"&franja="+f+"&desde=sent")
	if !strings.Contains(sel, `class="sbar sel"`) || !strings.Contains(sel, `class="schart has-sel"`) {
		t.Error("la barra elegida queda marcada")
	}
	if k, i := strings.Index(sel, "<h3>Sentimiento del programa"), strings.Index(sel, `id="franja-det"`); k < 0 || i < k {
		t.Error("desde Sentimiento, el detalle va en esa tarjeta")
	}
}

// TestProgramSupersTab (CA6): Super Chats va a la derecha de Chat del
// programa (desde pestana-recomendaciones, la anteúltima), con el total por
// moneda, la tabla y los pagos; sin pagos, lo dice.
func TestProgramSupersTab(t *testing.T) {
	if k := programTabs[len(programTabs)-2]; k.Key != "supers" || k.Label != "Super Chats" {
		t.Fatalf("la anteúltima pestaña es %+v", k)
	}
	if programTabs[len(programTabs)-3].Key != "chat" {
		t.Error("Super Chats va a la derecha de Chat del programa")
	}
	now := noonToday()
	day := dayStart(now)
	_, srv := programServer(t, now, false)
	h := srv.Routes()
	keyMan := segmentKey("UC1", Segment{VideoID: "v1", Program: "LA MAÑANA", Start: day.Add(8 * time.Hour)})
	body := getBody(h, "/parcial/programas?dia=hoy&pestana=supers&tramo="+url.QueryEscape(keyMan))
	for _, w := range []string{`aria-current="page">Super Chats</a>`, `id="supers"`, `class="tiles sbigs"`, "ARS", "<th scope=\"col\">Moneda</th>", "¿Saludan a Rosario?"} {
		if !strings.Contains(body, w) {
			t.Errorf("falta %q", w)
		}
	}
	if strings.Contains(body, "<h3>Resumen del chat") {
		t.Error("la pestaña muestra solo los Super Chats")
	}
	keyPoco := segmentKey("UC1", Segment{VideoID: "v2", Program: "POCO", Start: day.Add(6 * time.Hour)})
	empty := getBody(h, "/parcial/programas?dia=hoy&pestana=supers&tramo="+url.QueryEscape(keyPoco))
	if !strings.Contains(empty, ">Super Chats</a>") || !strings.Contains(empty, "Sin Super Chats ni Super Stickers en este programa.") {
		t.Error("sin pagos, la pestaña está y lo dice")
	}
}

// TestProgramAudienceLayout (CA7): el gráfico de segmentos a la izquierda, la
// tabla a la derecha y Cohortes debajo.
func TestProgramAudienceLayout(t *testing.T) {
	now := noonToday()
	day := dayStart(now)
	_, srv := programServer(t, now, false)
	keyMan := segmentKey("UC1", Segment{VideoID: "v1", Program: "LA MAÑANA", Start: day.Add(8 * time.Hour)})
	body := getBody(srv.Routes(), "/parcial/programas?dia=hoy&pestana=audiencia&tramo="+url.QueryEscape(keyMan))
	seg := from(body, `<div class="segcols">`, "Cohortes: ¿vuelven los que llegan?")
	if seg == "" {
		t.Fatalf("falta .segcols antes de Cohortes:\n%s", body)
	}
	g, tb := strings.Index(seg, `class="segflow"`), strings.Index(seg, "<th scope=\"col\">Segmento</th>")
	if g < 0 || tb < g {
		t.Errorf("gráfico en %d, tabla en %d", g, tb)
	}
	if strings.Contains(from(body, "Segmentos por asistencia", "Cohortes"), `<div class="grid2">`) {
		t.Error("Cohortes ya no va al lado de Segmentos")
	}
}

// TestProgramSentChartReview (revisión 1, puntos 4 a 6): los rótulos de hora
// no se pisan con el del principio, una franja elegida desde Participación
// no atenúa las barras y un promedio de menos de 5 mensajes va sin color.
func TestProgramSentChartReview(t *testing.T) {
	day := dayStart(noonToday())
	start := day.Add(8*time.Hour + 58*time.Minute)
	slots := []SentSlot{{Start: slotOf(start), Pos: 5}}
	c := programSentChart(slots, start, day.Add(11*time.Hour), false)
	var labels []string
	for _, tk := range c.Ticks {
		labels = append(labels, tk.Label)
	}
	if got := strings.Join(labels, ","); got != "08:55,10:00,11:00" {
		t.Errorf("rótulos %s, quería 08:55,10:00,11:00", got)
	}

	now := noonToday()
	_, srv, key := claudeProgramServer(t, now)
	f := fmt.Sprint(day.Add(8*time.Hour + 5*time.Minute).Unix())
	body := getBody(srv.Routes(), "/parcial/programas?dia=hoy&tramo="+url.QueryEscape(key)+"&franja="+f+"&desde=part")
	if strings.Contains(body, `class="schart has-sel"`) {
		t.Error("desde Participación, las barras de Sentimiento no se atenúan")
	}

	st, srv2 := programServer(t, now, true)
	var ls []MessageLabel
	for i := range 3 {
		ls = append(ls, MessageLabel{ID: fmt.Sprintf("man%d", i), VideoID: "v1", At: day.Add(8*time.Hour + time.Duration(i)*time.Minute), Program: "LA MAÑANA", Sentiment: "positivo"})
	}
	st.AddBatch(ClaudeBatch{At: day.Add(8*time.Hour + 4*time.Minute), ChannelID: "UC1", From: ls[0].At, To: ls[2].At, Labels: ls})
	low := getBody(srv2.Routes(), "/parcial/programas?dia=hoy&tramo="+url.QueryEscape(key))
	if strings.Contains(low, `<span class="cbig">`) || !strings.Contains(low, "Menos de 5 mensajes analizados en todo el programa: sin color.") {
		t.Error("con menos de 5 mensajes, el promedio va sin color ni tono en grande")
	}
}
