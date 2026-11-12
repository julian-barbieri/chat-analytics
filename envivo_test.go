package main

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// Tests de la spec en-vivo-unificado: En vivo sin el selector programa/día
// y los resúmenes de ayer que faltan.

// TestLiveScopeAuto (CA1, CA4): con vivo al aire el alcance es el programa en
// curso aunque llegue ver=dia; no hay botones ni ver= en el HTML; sin vivo, lo
// de hoy.
func TestLiveScopeAuto(t *testing.T) {
	now := noonLocal(t)
	st := liveStore(t, now)
	srv := &Server{st: st, budget: 9000, analyst: NewAnalyst(), now: func() time.Time { return now }}
	st.SetClaudeStatus(ClaudeStatus{On: true, Model: defaultClaudeModel, Tope: 2_000_000})
	h := srv.Routes()
	for _, path := range []string{"/parcial/vivo/analisis?canal=UCB&video=b2&ver=dia", "/parcial/vivo/analisis?canal=UCB&video=b2&ver=programa", "/parcial/vivo/analisis?canal=UCB&video=b2"} {
		body := getBody(h, path)
		for _, w := range []string{"(programa en curso)", "Resumen de Claude del programa en curso"} {
			if !strings.Contains(body, w) {
				t.Errorf("%s: falta %q", path, w)
			}
		}
		for _, not := range []string{`id="ver-programa"`, `id="ver-dia"`, "Todo el día del canal", "ver=dia", "ver=programa", `name="ver"`} {
			if strings.Contains(body, not) {
				t.Errorf("%s: sobra %q", path, not)
			}
		}
	}
	if page := getBody(h, "/?canal=UCB&ver=dia"); strings.Contains(page, "ver=dia") {
		t.Error("la página refleja un ver= viejo")
	}
	// Sin vivo: lo de hoy del canal.
	st.SetVideos("Radio B", nil)
	if body := getBody(h, "/parcial/vivo/analisis?canal=UCB&video=b2"); !strings.Contains(body, "Análisis de hoy") || strings.Contains(body, "(programa en curso)") {
		t.Errorf("sin vivo: tendría que ser lo de hoy")
	}
}

// TestLiveDayTotals (CA2): con programa al aire, los mensajes de arriba y
// Personas que escribieron suman el total del día del canal.
func TestLiveDayTotals(t *testing.T) {
	now := noonLocal(t)
	st := liveStore(t, now)
	// Del programa anterior (otro vivo de hoy) y del actual.
	st.Add([]Message{
		{ID: "old1", Source: "chat", VideoID: "b1", Program: "LA MAÑANA", Author: "Viejo", Text: "mañanero", At: now.Add(-150 * time.Minute)},
		{ID: "old2", Source: "chat", VideoID: "b1", Program: "LA MAÑANA", Author: "Otro", Text: "buen día", At: now.Add(-140 * time.Minute)},
		{ID: "new1", Source: "chat", VideoID: "b2", Program: "LA TARDE", Author: "Beto", Text: "hola tarde", At: now.Add(-2 * time.Minute)},
	})
	h := (&Server{st: st, budget: 9000, now: func() time.Time { return now }}).Routes()
	body := getBody(h, "/parcial/vivo/analisis?canal=UCB&video=b2")
	// Programa: m1 y new1 (2 mensajes de 2 personas); el día: 4 de 4.
	for _, w := range []string{"2 mensajes · 4 en el día", `<div class="dayt">4 en el día</div>`} {
		if !strings.Contains(body, w) {
			t.Errorf("falta %q", w)
		}
	}
	i := strings.Index(body, ">Personas que escribieron<")
	if i < 0 || !strings.Contains(body[i:i+300], "4 en el día") {
		t.Error("el total del día va en la tarjeta Personas que escribieron")
	}
	// Sin vivo no hay "en el día": ya es todo el día.
	st.SetVideos("Radio B", nil)
	if body := getBody(h, "/parcial/vivo/analisis?canal=UCB&video=b2"); strings.Contains(body, `class="dayt"`) || strings.Contains(body, " en el día</p>") {
		t.Error("sin vivo no se repite el total del día")
	}
}

// TestDayStripLinks (CA3): cada programa terminado de la tira lleva a su
// detalle en Programas con la misma clave; el de ahora y los ratos sin vivo no.
func TestDayStripLinks(t *testing.T) {
	now := noonToday()
	day := dayStart(now)
	_, srv := programServer(t, now, false)
	keyMan := segmentKey("UC1", Segment{VideoID: "v1", Program: "LA MAÑANA", Start: day.Add(8 * time.Hour)})
	keyCruce := segmentKey("UC1", Segment{VideoID: "v0", Program: "CRUCE", Start: day.Add(-time.Hour)})
	body := getBody(srv.Routes(), "/parcial/vivo/canal?canal=UC1&video=v1")
	for _, key := range []string{keyMan, keyCruce} {
		if !strings.Contains(body, `<a class="tbl" href="/programas?canal=UC1&amp;tramo=`+attrQuery(key)+`"`) {
			t.Errorf("falta el link a %s en:\n%s", key, body)
		}
		// La clave es la de Programas: el detalle lo encuentra.
		if det := getBody(srv.Routes(), "/parcial/programas/detalle?"+url.Values{"tramo": {key}}.Encode()); strings.Contains(det, "Ese programa ya no está guardado") {
			t.Errorf("Programas no encuentra %s", key)
		}
	}
	tira := body[strings.Index(body, `class="tira"`):]
	tira = tira[:strings.Index(tira, "</ol>")]
	if n := strings.Count(tira, `class="tbl"`); n != strings.Count(tira, `class="tb fin"`) {
		t.Errorf("%d links para %d programas terminados", n, strings.Count(tira, `class="tb fin"`))
	}
	if i := strings.Index(tira, `class="tb aire"`); i < 0 || strings.Contains(tira[i:i+200], `class="tbl"`) {
		t.Error("el programa al aire no lleva link")
	}
}

// TestPendingYesterday (CA5, CA6): el analista también pide los programas que
// terminaron ayer sin análisis final, después de los de hoy, con la clave de
// Programas (Ayer); nunca los de anteayer.
func TestPendingYesterday(t *testing.T) {
	now := noonToday()
	day := dayStart(now)
	ch := ChannelStatus{ID: "UC1", Name: "Radio Test"}
	const title = testTitle
	var samples []LiveSample
	samples = append(samples, livesEvery("v5", "UC1", "Radio Test", title+"ANTEAYER", day.Add(-30*time.Hour), day.Add(-29*time.Hour))...)
	samples = append(samples, livesEvery("v6", "UC1", "Radio Test", title+"AYER TARDE", day.Add(-8*time.Hour), day.Add(-6*time.Hour))...)
	samples = append(samples, livesEvery("v7", "UC1", "Radio Test", title+"AYER NOCHE", day.Add(-4*time.Hour), day.Add(-2*time.Hour))...)
	samples = append(samples, livesEvery("v8", "UC1", "Radio Test", title+"HOY", day.Add(8*time.Hour), day.Add(9*time.Hour))...)
	segs := programSegs([]ChannelStatus{ch}, samples, nil, []time.Time{day.AddDate(0, 0, -1)}, now)
	keys := map[string]string{}
	for _, ps := range segs {
		keys[ps.Seg.Program] = ps.Key
	}
	done := map[string]bool{keys["AYER NOCHE"]: true}
	var got []string
	for _, p := range pendingSegments([]ChannelStatus{ch}, samples, nil, done, now) {
		got = append(got, p.Seg.Program)
		if want, ok := keys[p.Seg.Program]; ok && segmentKey("UC1", p.Seg) != want {
			t.Errorf("%s: clave %q, Programas usa %q", p.Seg.Program, segmentKey("UC1", p.Seg), want)
		}
	}
	if strings.Join(got, ",") != "HOY,AYER TARDE" {
		t.Errorf("pendientes = %v, quiero HOY y después AYER TARDE (AYER NOCHE ya está, ANTEAYER no)", got)
	}
}
