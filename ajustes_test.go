package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Tests de la spec ajustes-y-sin-vivo (docs/specs/2026-11-12-ajustes-y-sin-vivo.md).

// TestDayRange30d (CA1): "30d" son los 30 días locales hasta hoy.
func TestDayRange30d(t *testing.T) {
	now := noonToday()
	dia, days := dayRange("30d", now)
	if dia != "30d" || len(days) != 30 || !days[0].Equal(dayStart(now)) || !days[29].Equal(dayStart(now).AddDate(0, 0, -29)) {
		t.Fatalf("dayRange(30d) = %s, %d días (%v … %v)", dia, len(days), days[0], days[len(days)-1])
	}
	if dia, days := dayRange("90d", now); dia != "hoy" || len(days) != 1 {
		t.Errorf("un día desconocido cae en Hoy: %s %d", dia, len(days))
	}
}

// TestProgramsPage30d (CA1): la opción y el título de los últimos 30 días.
func TestProgramsPage30d(t *testing.T) {
	now := noonToday()
	_, srv := programServer(t, now, false)
	body := getBody(srv.Routes(), "/programas?dia=30d")
	from := dayStart(now).AddDate(0, 0, -29).Format("02/01")
	for _, w := range []string{`<option value="30d" selected>Últimos 30 días</option>`,
		"Programas de los últimos 30 días · del " + from + " al " + now.Format("02/01"), "LA TARDE", "AYER"} {
		if !strings.Contains(body, w) {
			t.Errorf("falta %q", w)
		}
	}
}

// TestCatGaugeNoAnchor (CA2): tocar el semicírculo no salta a un ancla.
func TestCatGaugeNoAnchor(t *testing.T) {
	now := noonLocal(t)
	st := liveStore(t, now)
	st.Add([]Message{
		{ID: "e1", Source: "chat", VideoID: "b2", Program: "LA TARDE", Author: "Beto", Text: "excelente programa", At: now.Add(-2 * time.Minute)},
		{ID: "q1", Source: "chat", VideoID: "b2", Program: "LA TARDE", Author: "Caro", Text: "¿a qué hora vuelve?", At: now.Add(-3 * time.Minute)},
	})
	body := getBody((&Server{st: st, budget: 9000, now: func() time.Time { return now }}).Routes(), "/parcial/vivo/analisis?canal=UCB&video=b2&ver=programa")
	i := strings.Index(body, `class="cgauge`)
	if i < 0 {
		t.Fatal("falta el semicírculo")
	}
	g := body[i:]
	g = g[:strings.Index(g, `id="cat-detalle"`)]
	if strings.Contains(g, `href="#`) {
		t.Errorf("el semicírculo tiene links a un ancla:\n%s", g)
	}
	for _, w := range []string{`class="gsec`, `tabindex="0"`, `role="button"`, `hx-trigger="click, keyup[key=='Enter']"`, `<button type="button" class="grow`} {
		if !strings.Contains(g, w) {
			t.Errorf("falta %q en:\n%s", w, g)
		}
	}
}

// TestLiveSlotDetail (CA3): tocar una barra de Momentos muestra los mensajes
// de esa franja debajo del gráfico.
func TestLiveSlotDetail(t *testing.T) {
	now := noonLocal(t)
	st := liveStore(t, now)
	at := now.Add(-90 * time.Minute).Truncate(slotStep).Add(time.Minute)
	st.Add(chatRun("b1", "b1", "LA MAÑANA", at, 10*time.Second, 8))
	st.AddViewers(at, []Video{{ID: "b1", LiveStatus: "live", Concurrent: 100}})
	srv := &Server{st: st, budget: 9000, now: func() time.Time { return now }}
	base := "/parcial/vivo/analisis?canal=UCB&video=b2&ver=dia"
	slot := strconv.FormatInt(at.Truncate(slotStep).Unix(), 10)
	body := getBody(srv.Routes(), base)
	if !strings.Contains(body, "pfranja="+slot) || !strings.Contains(body, `class="pbar`) {
		t.Fatalf("las barras de Momentos no piden su franja")
	}
	if strings.Contains(body, `id="pfranja-det"`) {
		t.Error("sin franja elegida no va el detalle")
	}
	body = getBody(srv.Routes(), base+"&pfranja="+slot)
	i := strings.Index(body, `id="pfranja-det"`)
	if i < 0 {
		t.Fatal("falta el detalle de la franja elegida")
	}
	det := body[i:]
	start := at.Truncate(slotStep)
	for _, w := range []string{"Mensajes de las " + start.Local().Format("15:04") + " a " + start.Add(slotStep).Local().Format("15:04"), "mensaje b1 0", "mensaje b1 7", "Cerrar"} {
		if !strings.Contains(det, w) {
			t.Errorf("detalle: falta %q", w)
		}
	}
	if !strings.Contains(body, `class="pbar sel"`) || !strings.Contains(body, `name="pfranja" value="`+slot+`"`) {
		t.Error("la barra elegida no queda marcada o la franja no sigue en el refresco")
	}
	// Una franja que no es del gráfico se ignora y no se refleja.
	for _, bad := range []string{"123", "<x>", strconv.FormatInt(start.Unix()+60, 10)} {
		body = getBody(srv.Routes(), base+"&pfranja="+url.QueryEscape(bad))
		if strings.Contains(body, `id="pfranja-det"`) || strings.Contains(body, `name="pfranja"`) {
			t.Errorf("franja %q: no debería aceptarse", bad)
		}
	}
}

// TestProgramDetailGauge (CA4): Categorías siempre, con y sin Claude y con
// el resumen de Claude.
func TestProgramDetailGauge(t *testing.T) {
	now := noonToday()
	day := dayStart(now)
	key := segmentKey("UC1", Segment{VideoID: "v1", Program: "LA MAÑANA", Start: day.Add(8 * time.Hour)})
	path := "/parcial/programas/detalle?tramo=" + url.QueryEscape(key)
	_, off := programServer(t, now, false)
	body := getBody(off.Routes(), path)
	if !strings.Contains(body, `<h3>Categorías y preguntas <span class="chip">por reglas</span></h3>`) || !strings.Contains(body, `class="cgauge`) {
		t.Error("sin Claude: falta Categorías por reglas")
	}
	st, srv, key := claudeProgramServer(t, now)
	body = getBody(srv.Routes(), "/parcial/programas/detalle?tramo="+url.QueryEscape(key))
	if !strings.Contains(body, `<h3>Categorías y preguntas <span class="ia">Claude</span></h3>`) || !strings.Contains(body, `class="cgauge`) {
		t.Error("con Claude: falta Categorías por Claude")
	}
	// Con el resumen de Claude con temas, Categorías sigue y los temas también.
	sg := Segment{VideoID: "v1", Program: "LA MAÑANA", Start: day.Add(8 * time.Hour)}
	st.AddAnalysis(ClaudeAnalysis{At: day.Add(11 * time.Hour), ChannelID: "UC1", Key: segmentKey("UC1", sg), VideoID: "v1", Program: "LA MAÑANA",
		Model: defaultClaudeModel, From: day.Add(8 * time.Hour), To: day.Add(10 * time.Hour), Sent: 25, Total: 25,
		Result: ClaudeResult{Summary: "Resumen", Topics: []ClaudeGroup{{Text: "El clima", Msgs: 3, People: 2}}}})
	body = getBody(srv.Routes(), "/parcial/programas/detalle?tramo="+url.QueryEscape(key))
	for _, w := range []string{`<h3>Categorías y preguntas <span class="ia">Claude</span></h3>`, `class="cgauge`, `Temas de conversación <span class="ia">Claude</span>`, "El clima"} {
		if !strings.Contains(body, w) {
			t.Errorf("con resumen: falta %q", w)
		}
	}
}

// commentsServerWith arma un Server de Comentarios con la fixture y los
// canales configurados que se pasen.
func commentsServerWith(t *testing.T, chans ...ChannelStatus) *Server {
	t.Helper()
	st, err := OpenStore(t.TempDir() + "/datos.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	msgs, vids := commentsFixture(now)
	st.Add(msgs)
	for _, v := range vids {
		st.AddVideos(now, []Video{{ID: v.ID, Title: v.Title, ChannelID: v.ChannelID, Channel: v.Channel, PublishedAt: v.PublishedAt, Comments: v.Comments, LiveStatus: "none"}})
	}
	st.SetChannels(chans)
	return &Server{st: st}
}

// TestCommentsAllOnlyConfigured (CA6): "Todos" solo suma los canales que
// están hoy en Canales.
func TestCommentsAllOnlyConfigured(t *testing.T) {
	srv := commentsServerWith(t, ChannelStatus{ID: "UC1", Name: "Radio"})
	body := getBody(srv.Routes(), "/comentarios")
	if strings.Contains(body, "Acústico") || strings.Contains(body, "Qué buena sesión") {
		t.Error("Todos muestra un canal que no está en Canales")
	}
	if !strings.Contains(body, "Entrevista al ministro") {
		t.Error("Todos perdió los videos del canal configurado")
	}
	srv = commentsServerWith(t, ChannelStatus{ID: "UC1", Name: "Radio"}, ChannelStatus{ID: "UC2", Name: "Otra"})
	if body := getBody(srv.Routes(), "/comentarios"); !strings.Contains(body, "Acústico") {
		t.Error("con los dos canales configurados, Todos debe sumar los dos")
	}
}

// TestCommentsNoPub (CA7): no hay más "Publicados en 30 días".
func TestCommentsNoPub(t *testing.T) {
	if q := commentsQuery(url.Values{"periodo": {"pub"}}); q.Periodo != "30d" {
		t.Errorf("periodo=pub = %q, quería 30d", q.Periodo)
	}
	srv := commentsServerWith(t, ChannelStatus{ID: "UC1", Name: "Radio"})
	if body := getBody(srv.Routes(), "/comentarios"); strings.Contains(body, "Publicados en 30 días") || strings.Contains(body, "periodo=pub") {
		t.Error("sigue la opción Publicados en 30 días")
	}
}

// TestLikeCurveEmpty (CA8): sin likes repartidos no se dibuja un gráfico vacío.
func TestLikeCurveEmpty(t *testing.T) {
	srv := commentsServerWith(t, ChannelStatus{ID: "UC1", Name: "Radio"})
	now := time.Now()
	// El video g1 se publicó hace 3 días; los likes se guardan desde hace 1 h y no cambiaron.
	srv.st.AddLikes(now.Add(-time.Hour), []Video{{ID: "g1", Likes: 120}})
	srv.st.AddLikes(now.Add(-time.Minute), []Video{{ID: "g1", Likes: 120}})
	body := getBody(srv.Routes(), "/comentarios?video=g1")
	i := strings.Index(body, `id="clikes-h"`)
	if i < 0 {
		t.Fatal("falta la tarjeta de likes")
	}
	card := body[i:]
	card = card[:strings.Index(card, "</section>")]
	from := now.Add(-time.Hour).Local().Format("02/01 15:04")
	if strings.Contains(card, "<svg") || !strings.Contains(card, "Desde el "+from+", cuando se empezaron a guardar los likes, este video no sumó likes nuevos.") {
		t.Errorf("tarjeta de likes:\n%s", card)
	}
}

// TestLivePageNoTopLiked (CA9): En vivo ya no tiene "Lo que más gustó".
func TestLivePageNoTopLiked(t *testing.T) {
	now := noonLocal(t)
	st := liveStore(t, now)
	st.AddVideos(now, []Video{{ID: "x1", Title: "Video con likes", ChannelID: "UCB", Channel: "B", PublishedAt: now.Add(-time.Hour), Likes: 50, LiveStatus: "none"}})
	body := getBody((&Server{st: st, budget: 9000, now: func() time.Time { return now }}).Routes(), "/parcial/vivo/analisis?canal=UCB&video=b2&ver=dia")
	if strings.Contains(body, "Lo que más gustó") {
		t.Error("sigue Lo que más gustó")
	}
}

// TestProgramSegmentsNewLive (CA10): un hueco de más de 4 min antes de otro
// vivo es "sin vivo"; uno de 3 min no; en el mismo vivo siguen los 7 min.
func TestProgramSegmentsNewLive(t *testing.T) {
	orig := time.Local
	t.Cleanup(func() { time.Local = orig })
	time.Local = time.UTC
	day := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	at := func(hm string) time.Time {
		h, _ := strconv.Atoi(hm[:2])
		m, _ := strconv.Atoi(hm[3:])
		return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
	}
	const riva = "🔴RADIO RIVADAVIA en VIVO | VAMOS RIVADAVIA"
	run := func(video, from, to string) []LiveSample {
		return livesEvery(video, "UCA", "Radio Rivadavia", riva, at(from), at(to))
	}
	str := func(segs []Segment) string {
		var parts []string
		for _, s := range segs {
			parts = append(parts, s.Start.Format("15:04")+"-"+s.End.Format("15:04")+" "+s.State)
		}
		return strings.Join(parts, "|")
	}
	now := at("09:30")
	cases := []struct {
		name, want string
		samples    []LiveSample
	}{
		{"otro vivo 6 min después", "09:14-09:17 terminó|09:17-09:23 sin vivo|09:23-09:29 al aire", append(run("a", "09:14", "09:17"), run("b", "09:23", "09:29")...)},
		{"otro vivo 3 min después", "09:14-09:17 terminó|09:20-09:29 al aire", append(run("a", "09:14", "09:17"), run("b", "09:20", "09:29")...)},
		{"mismo vivo 6 min después", "09:14-09:29 al aire", append(run("b", "09:14", "09:17"), run("b", "09:23", "09:29")...)},
	}
	for _, c := range cases {
		if got := str(ProgramSegments(c.samples, "UCA", map[string]bool{"b": true}, at("09:00"), now)); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

// TestNoLiveCuts (CA11 de ajustes-y-sin-vivo; CA3 a CA5 de
// sin-vivo-monitor-apagado): con las revisiones sin vivo, un rato en que el
// monitor revisó el canal y no encontró vivo es corte (sin mínimo ni tope) y
// uno sin ninguna revisión es monitor apagado.
func TestNoLiveCuts(t *testing.T) {
	orig := time.Local
	t.Cleanup(func() { time.Local = orig })
	time.Local = time.UTC
	today := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	at := func(d time.Time, hm string) time.Time {
		h, _ := strconv.Atoi(hm[:2])
		m, _ := strconv.Atoi(hm[3:])
		return d.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
	}
	yday := today.AddDate(0, 0, -1)
	run := func(video, title string, from, to time.Time) []LiveSample {
		return livesEvery(video, "UCA", "Rock & Pop", "🔴 [EN VIVO] "+title+" | ROCK & POP", from, to)
	}
	checks := func(from, to time.Time) []NoLiveCheck {
		var out []NoLiveCheck
		for t := from; !t.After(to); t = t.Add(3 * time.Minute) {
			out = append(out, NoLiveCheck{At: t, ChannelID: "UCA"})
		}
		return out
	}
	var samples []LiveSample
	var noLive []NoLiveCheck
	// Ayer: nada hasta las 06:00 (apagado), 06:00 a 10:00 al aire, corte de
	// 9 min, al aire hasta las 22:00 con un hueco sin muestras de 14:57 a 16:00
	// (apagado) y de noche el monitor andando sin vivo hasta hoy 02:57 (corte de
	// casi 5 h). Después, apagado hasta las 06:02.
	samples = append(samples, run("y1", "MAÑANA", at(yday, "06:00"), at(yday, "10:00"))...)
	noLive = append(noLive, checks(at(yday, "10:03"), at(yday, "10:06"))...)
	for _, s := range run("y2", "MEDIODÍA", at(yday, "10:09"), at(yday, "22:00")) {
		if s.At.Before(at(yday, "15:00")) || !s.At.Before(at(yday, "16:00")) {
			samples = append(samples, s)
		}
	}
	noLive = append(noLive, checks(at(yday, "22:03"), at(today, "02:57"))...)
	// Hoy: un corte de 2 min entre vivos (una búsqueda rápida a las 09:18), uno
	// de 3 min dentro del mismo vivo (10:02) y uno de 7 min antes de LA TARDE.
	samples = append(samples, run("t1", "ESTA MAÑANA", at(today, "06:02"), at(today, "09:17"))...)
	noLive = append(noLive, NoLiveCheck{At: at(today, "09:18"), ChannelID: "UCA"})
	samples = append(samples, run("t2", "VAMOS", at(today, "09:19"), at(today, "10:58"))...)
	noLive = append(noLive, NoLiveCheck{At: at(today, "10:02"), ChannelID: "UCA"})
	noLive = append(noLive, checks(at(today, "11:01"), at(today, "11:04"))...)
	samples = append(samples, run("t3", "LA TARDE", at(today, "11:05"), at(today, "11:56"))...)
	// Otro canal no cuenta: ni su vivo ni su revisión sin vivo.
	samples = append(samples, LiveSample{At: at(today, "08:00"), VideoID: "z", ChannelID: "UCZ", Channel: "Otro", Title: "X"})
	noLive = append(noLive, NoLiveCheck{At: at(today, "07:00"), ChannelID: "UCZ"})
	now := at(today, "12:00")
	days := []time.Time{today, yday}
	v := NoLiveCuts(samples, noLive, "UCA", days, now)
	if !v.HasData || len(v.Days) != 2 || v.Days[0].Label != "Hoy" || v.Days[1].Label != "Ayer" {
		t.Fatalf("vista = %+v", v)
	}
	str := func(v NoLiveView) string {
		var cuts []string
		for _, c := range v.Cuts {
			cuts = append(cuts, fmt.Sprintf("%s %s-%s %s→%s", c.Start.Format("02"), c.Start.Format("15:04"), c.End.Format("15:04"), c.Before, c.After))
		}
		return strings.Join(cuts, "|")
	}
	want := "12 10:58-11:05 VAMOS→LA TARDE|12 10:01-10:04 VAMOS→VAMOS|12 09:17-09:19 ESTA MAÑANA→VAMOS|11 22:00-02:57 MEDIODÍA→|11 10:00-10:09 MAÑANA→MEDIODÍA"
	if got := str(v); got != want {
		t.Errorf("cortes:\n got %s\nwant %s", got, want)
	}
	if v.Cuts[3].Ongoing {
		t.Errorf("el corte que terminó con el monitor apagado no sigue: %+v", v.Cuts[3])
	}
	if v.CutsToday != 4 || v.MinToday != 7+3+2+177 || v.Longest != 4*time.Hour+57*time.Minute {
		t.Errorf("números: hoy %d cortes, %d min, el más largo %v", v.CutsToday, v.MinToday, v.Longest)
	}
	// Al aire ÷ (al aire + cortes), sin lo apagado.
	onAir := 240 + 288 + 360 + 195 + 42 + 54 + 55
	cuts := 9 + 297 + 2 + 3 + 7
	if want := float64(onAir) * 100 / float64(onAir+cuts); fmt.Sprintf("%.2f", v.OnAirPct) != fmt.Sprintf("%.2f", want) {
		t.Errorf("%% al aire = %.2f, quería %.2f", v.OnAirPct, want)
	}
	classes := func(d NoLiveDay) string {
		var out []string
		for _, p := range d.Parts {
			out = append(out, p.Class)
		}
		return strings.Join(out, ",")
	}
	if got := classes(v.Days[0]); got != "corte,apagado,aire,corte,aire,corte,aire,corte,aire" {
		t.Errorf("tira de hoy: %s", got)
	}
	if got := classes(v.Days[1]); got != "apagado,aire,corte,aire,apagado,aire,corte" {
		t.Errorf("tira de ayer: %s", got)
	}
	if v.Days[0].CutMin != 189 || v.Days[1].CutMin != 129 {
		t.Errorf("minutos por día: %d y %d", v.Days[0].CutMin, v.Days[1].CutMin)
	}
	p := v.Days[0].Parts[3]
	if fmt.Sprintf("%.3f %.3f", p.Left, p.Width) != fmt.Sprintf("%.3f %.3f", (9*60+17)*100/1440.0, 2*100/1440.0) || !strings.Contains(p.Title, "09:17 a 09:19") {
		t.Errorf("corte de 2 min en la tira: %+v", p)
	}
	if p := v.Days[1].Parts[4]; !strings.Contains(p.Title, "Monitor apagado") || !strings.Contains(p.Title, "14:57 a 16:00") {
		t.Errorf("rato apagado de ayer: %+v", p)
	}
	// La tira de hoy llega hasta ahora: lo que falta del día queda en blanco.
	last := v.Days[0].Parts[len(v.Days[0].Parts)-1]
	if fmt.Sprintf("%.3f", last.Left+last.Width) != fmt.Sprintf("%.3f", 12*100/24.0) {
		t.Errorf("la tira de hoy termina en %.3f %%", last.Left+last.Width)
	}

	// Un corte en curso: la última revisión no encontró vivo.
	v = NoLiveCuts(samples, append(noLive, NoLiveCheck{At: at(today, "11:59"), ChannelID: "UCA"}), "UCA", days, at(today, "12:01"))
	if len(v.Cuts) == 0 || !v.Cuts[0].Ongoing || v.Cuts[0].After != "" || v.Cuts[0].Before != "LA TARDE" || v.CutsToday != 5 {
		t.Errorf("corte en curso: %+v", v.Cuts)
	}
	// El monitor se apagó después del último vivo: gris, no corte.
	v = NoLiveCuts(samples, noLive, "UCA", days, at(today, "13:00"))
	if got := classes(v.Days[0]); !strings.HasSuffix(got, ",aire,apagado") || len(v.Cuts) != 5 || v.Cuts[0].Ongoing {
		t.Errorf("monitor apagado al final: %s, %+v", got, v.Cuts)
	}
	// Datos de antes de las revisiones sin vivo: entre vivos sin ninguna
	// revisión es apagado, no corte.
	v = NoLiveCuts(samples, nil, "UCA", days, now)
	if len(v.Cuts) != 0 || v.OnAirPct != 100 {
		t.Errorf("sin revisiones sin vivo: %s, %.1f %%", str(v), v.OnAirPct)
	}
	// Un canal que nunca tuvo vivo con el monitor andando: todo corte.
	v = NoLiveCuts(nil, checks(at(today, "08:00"), at(today, "11:57")), "UCA", days, now)
	if !v.HasData || len(v.Cuts) != 1 || v.Cuts[0].Dur() != 4*time.Hour || !v.Cuts[0].Ongoing || v.OnAirPct != 0 {
		t.Errorf("solo revisiones sin vivo: %+v %.1f", v.Cuts, v.OnAirPct)
	}
	// Sin nada del canal.
	if v := NoLiveCuts(nil, nil, "UCA", days, now); v.HasData || len(v.Cuts) != 0 {
		t.Errorf("sin datos: %+v", v)
	}
}

// TestNoLivePage (CA11, CA12): la pestaña, los filtros y lo que no se acepta.
func TestNoLivePage(t *testing.T) {
	now := noonToday()
	_, srv := programServer(t, now, false)
	h := srv.Routes()
	body := getBody(h, "/sin-vivo")
	for _, w := range []string{`<a href="/sin-vivo" aria-current="page">`, `<span>Sin vivo</span></a>`, "<title>Sin vivo", `id="sinvivo"`, `hx-get="/parcial/sin-vivo?`,
		`hx-trigger="every 60s"`, "Cortes hoy", "Sin vivo hoy", "Al aire", "Corte más largo", "Últimos 7 días", "Últimos 30 días",
		"pueden correrse de 1 a 3 minutos", `class="ntira"`,
		// sin-vivo-monitor-apagado (CA5): gris es monitor apagado, sin el tope de 2 h.
		`<span class="sw napagado" aria-hidden="true"></span> Monitor apagado`} {
		if !strings.Contains(body, w) {
			t.Errorf("falta %q", w)
		}
	}
	for _, w := range []string{"2 h", "np largo", "nlargo"} {
		if strings.Contains(body, w) {
			t.Errorf("sobra %q", w)
		}
	}
	for _, path := range []string{"/", "/programas", "/comentarios", "/canales"} {
		if b := getBody(h, path); !strings.Contains(b, `<a href="/sin-vivo"><svg`) || !strings.Contains(b, `<span>Sin vivo</span></a>`) {
			t.Errorf("%s: falta la pestaña Sin vivo", path)
		}
	}
	body = getBody(h, "/parcial/sin-vivo?canal=UC2&periodo=30d")
	if strings.Contains(body, "<h1>") || !strings.Contains(body, `aria-current="true">Otra Radio</a>`) || !strings.Contains(body, `aria-current="true">Últimos 30 días</a>`) {
		t.Errorf("parcial con canal y período:\n%s", body)
	}
	body = getBody(h, "/sin-vivo?canal=%3Cx%3E&periodo=99d")
	if strings.Contains(body, "<x>") || strings.Contains(body, "&lt;x&gt;") || !strings.Contains(body, `aria-current="true">Últimos 7 días</a>`) {
		t.Error("canal o período fuera de la lista")
	}
}
