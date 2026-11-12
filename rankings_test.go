package main

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Tests de la spec rankings-segmentos-preguntas: los rankings con barras de
// Comentarios, la cinta de Segmentos por asistencia y las preguntas dentro de
// Categorías en Programas.

// TestCommentsRankings (CA1, CA2): las palabras con barra relativa a la
// primera y cada persona con el total y la parte de videos distintos.
func TestCommentsRankings(t *testing.T) {
	now := noonToday()
	msgs, vids := commentsFixture(now)
	v := CommentsSummary(msgs, vids, nil, CommentsQuery{Periodo: "30d", Tipo: "todos"}, now)
	if len(v.WordRows) == 0 || len(v.WordRows) > 10 || v.WordRows[0].W != 100 {
		t.Fatalf("palabras = %+v", v.WordRows)
	}
	// Ana: 3 comentarios (dos en g1 y uno en g2) en 2 videos.
	a := v.Authors[0]
	if a.Name != "Ana" || a.N != 3 || a.Videos != 2 || a.W != 100 || fmt.Sprintf("%.1f", a.WVideos) != "66.7" || a.Initial != "A" || a.Hue < 1 || a.Hue > 5 {
		t.Errorf("Ana = %+v", a)
	}
	for _, c := range []struct{ name, want string }{{"@laura_a", "L"}, {"ñandú", "Ñ"}, {"", "?"}} {
		if got := authorInitial(c.name); got != c.want {
			t.Errorf("inicial de %q = %q, quería %q", c.name, got, c.want)
		}
	}
	// El color sale solo del nombre: el mismo en cada refresco y en cualquier
	// período, y siempre uno de los 5 de serie.
	again := CommentsSummary(msgs, vids, nil, CommentsQuery{Periodo: "7d", Tipo: "todos"}, now)
	for _, b := range again.Authors {
		if b.Name == "Ana" && b.Hue != a.Hue {
			t.Errorf("el color de Ana cambió: %d y %d", a.Hue, b.Hue)
		}
	}
	for _, name := range []string{"", "Beto", "@laura_a", "ñandú", strings.Repeat("x", 500)} {
		if h := authorHue(name); h < 1 || h > 5 {
			t.Errorf("authorHue(%q) = %d", name, h)
		}
	}

	body := getBody(commentsServer(t, now, false).Routes(), "/comentarios")
	words := card(body, `aria-labelledby="cpal-h"`)
	if !strings.Contains(words, `<ol class="wlist">`) || !strings.Contains(words, `class="wbar" style="width: 100%"`) {
		t.Errorf("De qué hablan sin barras:\n%s", words)
	}
	who := card(body, `aria-labelledby="cquien-h"`)
	for _, w := range []string{`<h2 id="cquien-h">Quiénes más comentan</h2>`, `<span class="av h`, `>A</span>Ana`,
		`role="img" aria-label="3 comentarios en 2 videos"`, `class="wbar soft" style="width: 100%"`, `class="wbar" style="width: 66.7%"`,
		"en distintos videos", "repetidos en el mismo video"} {
		if !strings.Contains(who, w) {
			t.Errorf("Quiénes más comentan: falta %q en:\n%s", w, who)
		}
	}
	if strings.Contains(who, "<table>") {
		t.Error("Quiénes más comentan sigue siendo una tabla")
	}
}

// TestSegmentFlow (CA3): la cinta de personas a mensajes, sin los segmentos
// vacíos y sin gráfico si no hay participantes.
func TestSegmentFlow(t *testing.T) {
	segs := []LoyaltySegment{
		{Key: "nuevos", Label: "Nuevos / de paso", People: 6, PeoplePct: 60, MsgsPct: 20},
		{Key: "ocasionales", Label: "Ocasionales", People: 2, PeoplePct: 20, MsgsPct: 20},
		{Key: "habituales", Label: "Habituales", People: 0, PeoplePct: 0, MsgsPct: 0},
		{Key: "nucleo", Label: "Núcleo", People: 2, PeoplePct: 20, MsgsPct: 60},
	}
	f := segmentFlow(segs)
	if f == nil || len(f.Bands) != 3 {
		t.Fatalf("cinta = %+v", f)
	}
	n, o, c := f.Bands[0], f.Bands[1], f.Bands[2]
	if n.Key != "nuevos" || o.Key != "ocasionales" || c.Key != "nucleo" {
		t.Fatalf("orden: %s %s %s", n.Key, o.Key, c.Key)
	}
	// Alturas proporcionales y en orden, de arriba hacia abajo en cada columna.
	if n.LH <= 2.9*c.LH || c.RH <= 2.9*n.RH || o.LY <= n.LY || c.RY <= o.RY {
		t.Errorf("columnas: %+v", f.Bands)
	}
	if !strings.HasPrefix(n.Path, "M") || !strings.HasSuffix(n.Path, "Z") {
		t.Errorf("cinta: %q", n.Path)
	}
	if n.LText != "Nuevos / de paso 60 %" || c.RText != "60 %" || c.Title != "Núcleo: 20 % de las personas y 60 % de los mensajes" {
		t.Errorf("textos: %q %q %q", n.LText, c.RText, c.Title)
	}
	if segmentFlow([]LoyaltySegment{{Key: "nuevos"}, {Key: "nucleo"}}) != nil {
		t.Error("sin participantes no hay gráfico")
	}
	html := renderString(t, "segflow", f)
	for _, w := range []string{`class="segflow"`, `role="img"`, `<g class="sf nucleo"><title>Núcleo: 20 % de las personas y 60 % de los mensajes</title>`,
		">Personas</text>", ">Mensajes</text>", "aria-label=\"De personas a mensajes: Nuevos / de paso 60 % de las personas y 20 % de los mensajes"} {
		if !strings.Contains(html, w) {
			t.Errorf("falta %q en:\n%s", w, html)
		}
	}
}

// TestProgramQuestionsInCats (CA4, CA5, CA6): en Programas no hay tarjeta
// Preguntas; el sector Pregunta, elegido por defecto, muestra los grupos de
// las tandas, las del resumen de Claude o las de reglas.
func TestProgramQuestionsInCats(t *testing.T) {
	now := noonToday()
	day := dayStart(now)
	_, srv, key := claudeProgramServer(t, now)
	path := "/parcial/programas/detalle?tramo=" + url.QueryEscape(key)
	body := getBody(srv.Routes(), path)
	if !strings.Contains(body, `<h3>Categorías y preguntas <span class="ia">Claude</span></h3>`) || strings.Contains(body, "<h3>Preguntas") {
		t.Errorf("con tandas: falta «Categorías y preguntas» o sobra la tarjeta Preguntas")
	}
	det := card(body, `id="cat-detalle"`)
	for _, w := range []string{"¿A qué hora pasan el pronóstico?", "¿Cómo se llama el tema?", ">4</strong>", "6 preguntas · 2 grupos"} {
		if !strings.Contains(det, w) {
			t.Errorf("con tandas: falta %q en el detalle:\n%s", w, det)
		}
	}
	if strings.Contains(det, "Últimos mensajes") {
		t.Error("con tandas: el detalle de Pregunta muestra grupos, no los últimos mensajes")
	}
	// Otra categoría elegida: sus mensajes, como siempre.
	other := card(getBody(srv.Routes(), path+"&abrir=cat-"+CatElogio), `id="cat-detalle"`)
	if strings.Contains(other, "¿A qué hora pasan el pronóstico?") || !strings.Contains(other, "Últimos mensajes") {
		t.Errorf("Elogios:\n%s", other)
	}

	// Sin tandas, con el resumen de Claude: sus preguntas.
	st, sum := programServer(t, now, true)
	sg := Segment{VideoID: "v1", Program: "LA MAÑANA", Start: day.Add(8 * time.Hour)}
	st.AddAnalysis(ClaudeAnalysis{At: day.Add(11 * time.Hour), ChannelID: "UC1", Key: segmentKey("UC1", sg), VideoID: "v1", Program: "LA MAÑANA",
		Model: defaultClaudeModel, From: day.Add(8 * time.Hour), To: day.Add(10 * time.Hour), Sent: 25, Total: 25,
		Result: ClaudeResult{Summary: "Resumen", Questions: []ClaudeGroup{{Text: "¿Repiten la entrevista?", Msgs: 2, People: 2}}}})
	if det := card(getBody(sum.Routes(), path), `id="cat-detalle"`); !strings.Contains(det, "¿Repiten la entrevista?") {
		t.Errorf("con resumen: faltan sus preguntas en el detalle:\n%s", det)
	}

	// Sin Claude: por reglas, y Pregunta elegida porque hay preguntas.
	_, off := programServer(t, now, false)
	body = getBody(off.Routes(), path)
	if !strings.Contains(body, `<h3>Categorías y preguntas <span class="chip">por reglas</span></h3>`) {
		t.Error("sin Claude: falta el título por reglas")
	}
	if det := card(body, `id="cat-detalle"`); !strings.Contains(det, "¿Saludan a Rosario?") || !strings.Contains(det, `<span class="sw pregunta"`) {
		t.Errorf("sin Claude: el detalle tendría que ser Pregunta con la de reglas:\n%s", det)
	}
}
