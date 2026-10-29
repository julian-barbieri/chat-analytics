package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	cases := map[string]string{
		"¿A qué hora vuelve Nelson?":             CatPregunta,
		"Escribime al whatsapp +54 11 5555 5555": CatSpam,
		"Miren mi canal www.ejemplo.com":         CatSpam,
		"Se corta todo el tiempo el audio":       CatQueja,
		"Genio Nelson, saludos desde Rosario":    CatElogio,
		"sos un corrupto":                        CatInsulto,
		"opino distinto sobre el tema":           CatOtro,
	}
	for in, want := range cases {
		if got := Classify(in); got != want {
			t.Errorf("Classify(%q) = %s, quería %s", in, got, want)
		}
	}
}

func TestFlowAgainstFakeAPI(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("key") != "k" {
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/channels":
			switch {
			case q.Get("id") == "C":
				w.Write([]byte(`{"items":[{"snippet":{"title":"Radio Test"},"contentDetails":{"relatedPlaylists":{"uploads":"UU1"}}}]}`))
			case q.Get("forHandle") == "@otra":
				w.Write([]byte(`{"items":[{"snippet":{"title":"Otra Radio"},"contentDetails":{"relatedPlaylists":{"uploads":"UU2"}}}]}`))
			default:
				w.Write([]byte(`{"items":[]}`))
			}
		case "/playlistItems":
			// El vivo de 24 h no está entre las últimas subidas: solo aparece en la lista de vivos.
			// La otra radio tiene un vivo en curso (v3) y uno viejo (v4) que no se sigue.
			ids := map[string][]string{"UU1": {"v1"}, "UULV1": {"v2"}, "UULV2": {"v3", "v4"}}
			var out []string
			for _, id := range ids[q.Get("playlistId")] {
				out = append(out, `{"contentDetails":{"videoId":"`+id+`"}}`)
			}
			w.Write([]byte(`{"items":[` + strings.Join(out, ",") + `]}`))
		case "/videos":
			items := map[string]string{
				"v1": `{"id":"v1","snippet":{"title":"Entrevista","channelTitle":"Radio Test","publishedAt":"` + now + `","liveBroadcastContent":"none"},"statistics":{"viewCount":"10","commentCount":"2"}}`,
				"v2": `{"id":"v2","snippet":{"title":"🔴RADIO TEST en VIVO | LA MAÑANA","channelTitle":"Radio Test","publishedAt":"` + old + `","liveBroadcastContent":"live"},"statistics":{"viewCount":"100","commentCount":"0"},"liveStreamingDetails":{"activeLiveChatId":"chat1","concurrentViewers":"40"}}`,
				"v3": `{"id":"v3","snippet":{"title":"La tarde de Otra","channelTitle":"Otra Radio","publishedAt":"` + now + `","liveBroadcastContent":"live"},"statistics":{"viewCount":"5","commentCount":"0"},"liveStreamingDetails":{"activeLiveChatId":"chat2","concurrentViewers":"7"}}`,
				"v4": `{"id":"v4","snippet":{"title":"Vivo de ayer","channelTitle":"Otra Radio","publishedAt":"` + old + `","liveBroadcastContent":"none"},"statistics":{"viewCount":"50","commentCount":"9"}}`,
			}
			var out []string
			for _, id := range strings.Split(q.Get("id"), ",") {
				out = append(out, items[id])
			}
			w.Write([]byte(`{"items":[` + strings.Join(out, ",") + `]}`))
		case "/commentThreads":
			w.Write([]byte(`{"items":[
			 {"id":"c1","snippet":{"totalReplyCount":0,"topLevelComment":{"snippet":{"authorDisplayName":"Ana","textOriginal":"¿Cuándo repiten?","publishedAt":"` + now + `","likeCount":3}}}},
			 {"id":"c2","snippet":{"totalReplyCount":0,"topLevelComment":{"snippet":{"authorDisplayName":"Viejo","textOriginal":"viejo","publishedAt":"` + old + `","likeCount":0}}}}]}`))
		case "/liveChat/messages":
			text := map[string]string{"chat1": "Grande Nelson", "chat2": "Hoy hablan del dólar"}[q.Get("liveChatId")]
			w.Write([]byte(`{"pollingIntervalMillis":1,"items":[{"id":"m-` + q.Get("liveChatId") + `","snippet":{"displayMessage":"` + text + `","publishedAt":"` + now + `"},"authorDetails":{"displayName":"Beto"}}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	defer func(g time.Duration) { minChatGap = g }(minChatGap)
	minChatGap = time.Millisecond

	yt := NewYouTube("k")
	yt.BaseURL = srv.URL
	title, up, err := yt.UploadsPlaylist("C")
	if err != nil || title != "Radio Test" || up != "UU1" {
		t.Fatalf("UploadsPlaylist: %v %q %q", err, title, up)
	}
	title2, up2, err := yt.UploadsPlaylist("@otra")
	if err != nil || title2 != "Otra Radio" || up2 != "UU2" {
		t.Fatalf("UploadsPlaylist(@otra): %v %q %q", err, title2, up2)
	}
	vids, err := yt.ChannelVideos([]string{up, up2}, 5, []string{"v1"})
	if err != nil || len(vids) != 3 || vids[0].ID != "v2" || vids[0].LiveChatID != "chat1" || vids[0].Concurrent != 40 ||
		vids[1].ID != "v3" || vids[1].Channel != "Otra Radio" || vids[2].ID != "v1" {
		t.Fatalf("ChannelVideos: %v %+v", err, vids)
	}
	cs, err := yt.Comments("v1", time.Now().Add(-24*time.Hour), 3)
	if err != nil || len(cs) != 1 || cs[0].Author != "Ana" {
		t.Fatalf("Comments: %v %+v", err, cs)
	}
	// Dos lecturas, una de cada vivo, con el programa de cada uno.
	chat, err := yt.LiveChats(vids[:2], 5*time.Second, 2)
	if err != nil || len(chat) != 2 {
		t.Fatalf("LiveChats: %v %+v", err, chat)
	}
	if chat[0].Program != "LA MAÑANA" || chat[1].Program != "La tarde de Otra" || chat[1].VideoID != "v3" {
		t.Errorf("programas del chat = %q, %q", chat[0].Program, chat[1].Program)
	}
	md := report(title+", "+title2, vids, append(cs, chat...), time.Now(), yt.Units, nil, 1)
	for _, want := range []string{"EN VIVO (40 mirando)", "| Otra Radio | EN VIVO (7 mirando)", "| pregunta | 1 |", "| elogio | 1 |", "- LA MAÑANA: 1", "- La tarde de Otra: 1"} {
		if !strings.Contains(md, want) {
			t.Errorf("el informe no contiene %q", want)
		}
	}
	if strings.Contains(md, "- Entrevista: 1") || strings.Contains(md, "Vivo de ayer") {
		t.Errorf("los comentarios no deberían contarse como programa ni aparecer vivos viejos de otros canales")
	}
	// 2 canales, subidas + vivos de cada canal, videos, comentarios, 2 lecturas de chat.
	if yt.Units != 2+1+2+1+1+2*5 {
		t.Errorf("unidades = %d", yt.Units)
	}
}

func TestNextChat(t *testing.T) {
	now := time.Now()
	lives := []Video{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	chats := map[string]*chatState{
		"a": {last: now.Add(-time.Minute)},
		"b": {last: now.Add(-2 * time.Minute)},
		"c": {last: now.Add(-3 * time.Minute), blocked: true},
	}
	if next, active := nextChat(lives, chats, now); next == nil || next.ID != "b" || active != 2 {
		t.Fatalf("nextChat = %v, %d; quería b, 2", next, active)
	}
	// Si YouTube pidió esperar más, le toca al otro.
	chats["b"].wait = 5 * time.Minute
	if next, _ := nextChat(lives, chats, now); next == nil || next.ID != "a" {
		t.Fatalf("nextChat con espera = %v; quería a", next)
	}
	// Con el streaming abierto no hace falta leerlo, pero sigue contando como activo.
	chats["b"].wait, chats["b"].open = 0, true
	if next, active := nextChat(lives, chats, now); next == nil || next.ID != "a" || active != 2 {
		t.Fatalf("nextChat con streaming abierto = %v, %d; quería a, 2", next, active)
	}
	// Un vivo nuevo (nunca leído) va primero.
	chats["d"] = &chatState{}
	if next, active := nextChat(append(lives, Video{ID: "d"}), chats, now); next == nil || next.ID != "d" || active != 3 {
		t.Fatalf("nextChat con vivo nuevo = %v, %d; quería d, 3", next, active)
	}
}

func TestParseChannel(t *testing.T) {
	cases := map[string]string{
		"UCLAj5TzCrNbqe3LosSKHLcQ":                             "UCLAj5TzCrNbqe3LosSKHLcQ",
		" @RadioRivadaviaAM630 ":                               "@RadioRivadaviaAM630",
		"https://www.youtube.com/@RadioRivadaviaAM630/streams": "@RadioRivadaviaAM630",
		"youtube.com/channel/UCLAj5TzCrNbqe3LosSKHLcQ":         "UCLAj5TzCrNbqe3LosSKHLcQ",
		"https://www.youtube.com/watch?v=gAqhej1HbKY":          "",
		"https://example.com/@alguien":                         "",
	}
	for in, want := range cases {
		if got := ParseChannel(in); got != want {
			t.Errorf("ParseChannel(%q) = %q, quería %q", in, got, want)
		}
	}
}

func TestHumanDur(t *testing.T) {
	cases := map[time.Duration]string{45 * time.Second: "45 s", 2 * time.Minute: "2 min", 150 * time.Second: "2 min 30 s"}
	for in, want := range cases {
		if got := humanDur(in); got != want {
			t.Errorf("humanDur(%s) = %q, quería %q", in, got, want)
		}
	}
}

func TestStoreDedupAndRetention(t *testing.T) {
	path := t.TempDir() + "/datos.jsonl"
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	old := Message{ID: "viejo", Text: "x", At: time.Now().Add(-40 * 24 * time.Hour)}
	m := Message{ID: "a", Text: "hola", At: time.Now()}
	if n := st.Add([]Message{m, m, old}); n != 2 {
		t.Fatalf("Add = %d, quería 2", n)
	}
	st2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(st2.Snapshot().Msgs); got != 1 {
		t.Fatalf("después de recargar hay %d mensajes, quería 1 (se borra lo de más de 30 días)", got)
	}
}

func TestStoreUnitsPersist(t *testing.T) {
	path := t.TempDir() + "/datos.jsonl"
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	st.AddUnits(3)
	st.AddUnits(5)
	st2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st2.Units(); got != 8 {
		t.Fatalf("después de reabrir la cuota es %d, quería 8", got)
	}
	// Una cuota guardada de otro día no cuenta.
	if err := os.WriteFile(st2.quota, []byte(`{"Day":"2000-01-01","Units":500}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st3, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st3.Units(); got != 0 {
		t.Fatalf("cuota de otro día = %d, quería 0", got)
	}
}

func TestParseVideoID(t *testing.T) {
	cases := map[string]string{
		"gAqhej1HbKY": "gAqhej1HbKY",
		"https://www.youtube.com/watch?v=gAqhej1HbKY":     "gAqhej1HbKY",
		"youtube.com/watch?v=gAqhej1HbKY&t=30s":           "gAqhej1HbKY",
		"https://youtu.be/gAqhej1HbKY?si=abc":             "gAqhej1HbKY",
		"https://www.youtube.com/live/gAqhej1HbKY?si=abc": "gAqhej1HbKY",
		"https://www.youtube.com/@RadioRivadaviaAM630":    "",
	}
	for in, want := range cases {
		if got := ParseVideoID(in); got != want {
			t.Errorf("ParseVideoID(%q) = %q, quería %q", in, got, want)
		}
	}
}

func TestProgramName(t *testing.T) {
	cases := []struct{ title, channel, want string }{
		{"🔴RADIO RIVADAVIA en VIVO | CRISTINA SIN VUELTAS con CRISTINA PÉREZ", "Radio Rivadavia AM 630", "CRISTINA SIN VUELTAS con CRISTINA PÉREZ"},
		{"🔴RADIO RIVADAVIA en VIVO | CRISTINA SIN VUELTAS con CRISTINA PÉREZ", "", "CRISTINA SIN VUELTAS con CRISTINA PÉREZ"},
		{"🔴 [EN VIVO] QUIÉN PAGA LA FIESTA con MARTÍN CICCIOLI, MAGUI AICEGA y GABY ACOSTA | ROCK & POP", "Rock And Pop",
			"QUIÉN PAGA LA FIESTA con MARTÍN CICCIOLI, MAGUI AICEGA y GABY ACOSTA"},
		{"Entrevista a Fulano | Parte 2", "Radio Rivadavia AM 630", "Entrevista a Fulano | Parte 2"},
		{"Editorial de Nelson", "", "Editorial de Nelson"},
		{"🔴 EN VIVO", "", "EN VIVO"},
		{"SE FUE LARGA con FERBO, BELU NEGRI, DOMI FAENA Y TEO D´ELIA | EN VIVO", "LUZU TV", "SE FUE LARGA con FERBO, BELU NEGRI, DOMI FAENA Y TEO D´ELIA"},
		{"🔴 [EN VIVO] QUIÉN PAGA LA FIESTA | ROCK & POP | EN VIVO", "Rock And Pop", "QUIÉN PAGA LA FIESTA"},
		{"C5N EN VIVO | Toda la información en un solo lugar | Seguí la transmisión las 24 horas", "C5N",
			"Toda la información en un solo lugar | Seguí la transmisión las 24 horas"},
	}
	for _, c := range cases {
		if got := ProgramName(c.title, c.channel); got != c.want {
			t.Errorf("ProgramName(%q, %q) = %q, quería %q", c.title, c.channel, got, c.want)
		}
	}
}

func TestChatBanner(t *testing.T) {
	now := time.Date(2026, 10, 28, 19, 0, 0, 0, time.UTC)
	if b := chatBanner(nil, "v1", now); b != nil {
		t.Errorf("sin vivo: %+v", b)
	}
	cases := []struct {
		ci    ChatInfo
		class string
		want  []string
	}{
		{ChatInfo{Stream: true, Open: true, Opened: now.Add(-40 * time.Second), Got: now.Add(-3 * time.Second), Gap: time.Minute, Peak: true, Lives: 1},
			"ok", []string{"Conectado en tiempo real desde hace 40 s", "como mínimo 1 min entre una y otra (horario pico).", "hace 3 s"}},
		{ChatInfo{Stream: true, LastDur: 8 * time.Second, Next: now.Add(47 * time.Second), Gap: time.Minute, Lives: 2,
			Avg: 2 * time.Minute, Count: 5},
			"warn", []string{"cortó la de tiempo real a los 8 s", "Próxima conexión en 47 s", "por turnos entre 2 vivos", "cada 2 min", "últimas 5"}},
		{ChatInfo{Next: now.Add(-time.Second), Gap: 3 * time.Minute, Lives: 1}, "warn", []string{"consultas comunes. Próxima consulta en instantes"}},
		{ChatInfo{Stream: true, Next: now.Add(time.Minute), Gap: time.Minute, Lives: 1}, "warn", []string{"Todavía no se conectó", "Primera conexión en 1 min"}},
		{ChatInfo{Stream: true, Paused: true, Gap: time.Minute, Lives: 1}, "warn", []string{"tope de cuota"}},
		{ChatInfo{Blocked: true}, "crit", []string{"no deja leer"}},
		{ChatInfo{Stream: true, RealTime: true, Next: now.Add(time.Second), Left: 3600, Gap: time.Minute, Lives: 1},
			"ok", []string{"Reconectando", "se vuelve a abrir en 1 s", "tiempo real continuo", "1.800 unidades por hora", "Quedan 3.600 unidades", "unas 2 h 00 min"}},
		{ChatInfo{Stream: true, RealTime: true, Open: true, Left: 4200, Rate: 2100},
			"ok", []string{"Conectado en tiempo real", "~2.100 por hora", "unas 2 h 00 min"}},
	}
	for _, c := range cases {
		b := chatBanner(map[string]ChatInfo{"v1": c.ci}, "v1", now)
		text := b.Title + " " + strings.Join(b.Lines, " ")
		if b.Class != c.class {
			t.Errorf("%+v: clase = %q, quería %q", c.ci, b.Class, c.class)
		}
		for _, w := range c.want {
			if !strings.Contains(text, w) {
				t.Errorf("%+v: cartel = %q, falta %q", c.ci, text, w)
			}
		}
	}
}

// TestWorkerRealTime: un vivo de RealTime se reconecta apenas YouTube corta
// la conexión, sin esperar el intervalo de los turnos.
func TestWorkerRealTime(t *testing.T) {
	defer func(d time.Duration) { realTimeGap = d }(realTimeGap)
	realTimeGap = 10 * time.Millisecond
	now := time.Now().UTC().Format(time.RFC3339)
	conns := make(chan struct{}, 100)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/channels":
			w.Write([]byte(`{"items":[{"snippet":{"title":"Radio Test"},"contentDetails":{"relatedPlaylists":{"uploads":"UU1"}}}]}`))
		case "/playlistItems":
			w.Write([]byte(`{"items":[]}`))
		case "/videos":
			w.Write([]byte(`{"items":[{"id":"r1","snippet":{"title":"C5N EN VIVO | NOTICIAS","channelTitle":"C5N","publishedAt":"` + now +
				`","liveBroadcastContent":"live"},"statistics":{},"liveStreamingDetails":{"activeLiveChatId":"cr"}}]}`))
		case "/liveChat/messages/stream":
			conns <- struct{}{}
			n := len(conns)
			w.Write([]byte("[" + streamItem(fmt.Sprint("t", n), fmt.Sprint("r-", n), "hola") + "]")) // corta enseguida, como YouTube
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	st, err := OpenStore(t.TempDir() + "/datos.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	yt := NewYouTube("k")
	yt.BaseURL = srv.URL
	go RunWorker(yt, st, WorkerConfig{
		Channels: []string{"C"}, Videos: 5, Extra: []string{"r1"}, VideosEvery: time.Hour, CommentsEvery: time.Hour,
		ChatEvery: time.Hour, ChatPeak: time.Hour, Budget: 9000, RealTime: map[string]bool{"r1": true},
	})
	// Con un tick de 1 s, en 3,5 s tienen que entrar al menos 3 conexiones (los turnos darían 1 por hora).
	time.Sleep(3500 * time.Millisecond)
	if n := len(conns); n < 3 {
		t.Fatalf("conexiones = %d, quería al menos 3", n)
	}
	if msgs := st.LastChat("r1", 10); len(msgs) < 3 || msgs[0].Program != "NOTICIAS" {
		t.Errorf("mensajes = %+v", msgs)
	}
	if ci := st.Status().ChatInfo["r1"]; !ci.RealTime || ci.Left <= 0 || ci.Rate != realTimeUnitsPerHour {
		t.Errorf("estado del chat = %+v", ci)
	}
}

func TestParsePeak(t *testing.T) {
	cases := map[string][2]int{"7-20": {7, 20}, " 22 - 6 ": {22, 6}, "20-24": {20, 0}, "": {0, 0}}
	for in, want := range cases {
		from, to, err := ParsePeak(in)
		if err != nil || from != want[0] || to != want[1] {
			t.Errorf("ParsePeak(%q) = %d, %d, %v; quería %v", in, from, to, err, want)
		}
	}
	for _, in := range []string{"7", "7-7", "0-24", "25-3", "a-b", "-1-5"} {
		if _, _, err := ParsePeak(in); err == nil {
			t.Errorf("ParsePeak(%q) no dio error", in)
		}
	}
}

func TestChatInterval(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 10, 28, h, 30, 0, 0, time.Local) }
	day := WorkerConfig{ChatEvery: 180 * time.Second, ChatPeak: 60 * time.Second, PeakFrom: 7, PeakTo: 20}
	night := day
	night.PeakFrom, night.PeakTo = 22, 2
	none := day
	none.PeakFrom, none.PeakTo = 0, 0
	cases := []struct {
		cfg  WorkerConfig
		hour int
		want time.Duration
	}{
		{day, 6, 180 * time.Second}, {day, 7, 60 * time.Second}, {day, 19, 60 * time.Second}, {day, 20, 180 * time.Second},
		{night, 21, 180 * time.Second}, {night, 23, 60 * time.Second}, {night, 1, 60 * time.Second}, {night, 2, 180 * time.Second},
		{none, 12, 180 * time.Second},
	}
	for _, c := range cases {
		if got, _ := c.cfg.chatInterval(at(c.hour)); got != c.want {
			t.Errorf("chatInterval(%d-%d, %d h) = %s, quería %s", c.cfg.PeakFrom, c.cfg.PeakTo, c.hour, got, c.want)
		}
	}
}

func TestLiveLabel(t *testing.T) {
	cases := []struct{ title, channel, want string }{
		{"🔴RADIO RIVADAVIA en VIVO | CRISTINA SIN VUELTAS con CRISTINA PÉREZ", "Radio Rivadavia AM 630", "RADIO RIVADAVIA en VIVO"},
		{"🔴 [EN VIVO] QUIÉN PAGA LA FIESTA con MARTÍN CICCIOLI | ROCK & POP", "Rock And Pop", "ROCK & POP en VIVO"},
		{"La tarde de Otra", "Otra Radio", "Otra Radio en VIVO"},
		{"SE FUE LARGA con FERBO, BELU NEGRI | EN VIVO", "LUZU TV", "LUZU TV en VIVO"},
	}
	for _, c := range cases {
		if got := LiveLabel(c.title, c.channel); got != c.want {
			t.Errorf("LiveLabel(%q, %q) = %q, quería %q", c.title, c.channel, got, c.want)
		}
	}
}

func TestTechIssue(t *testing.T) {
	cases := map[string]string{
		"No se escucha nada!!":            "Audio",
		"subí el volumen por favor":       "Audio",
		"se corta todo el tiempo":         "Cortes e imagen",
		"tiene mucho delay":               "Demora",
		"hablemos de economía":            "", // "eco" no es una queja
		"los audios de Spagnuolo":         "", // tema, no queja técnica
		"el gobierno no carga la culpa":   "",
		"excelente programa, sin cortes!": "",
	}
	for in, want := range cases {
		if got := TechIssue(in); got != want {
			t.Errorf("TechIssue(%q) = %q, quería %q", in, got, want)
		}
	}
}

func TestAnalyze(t *testing.T) {
	now := time.Now()
	msg := func(author, text string, likes int64) Message {
		now = now.Add(time.Second)
		return Message{ID: author + text, Source: "chat", VideoID: "v", Author: author, Text: text, At: now, Likes: likes}
	}
	a := Analyze([]Message{
		msg("Ana", "el dólar blue sigue subiendo", 0),
		msg("Beto", "qué pasa con el dólar blue hoy?", 5),
		msg("Caro", "el dólar blue no para", 0),
		msg("Dani", "¿Quién habla?", 0),
		msg("Dani", "¿Quién habla?", 0),
		msg("Eva", "No se escucha nada", 0),
		msg("Troll", "sos un pelotudo, gorila", 0),
		msg("Troll", "gorila corrupto", 0),
		msg("Spam", "entrá a www.ejemplo.com", 0),
		msg("Fede", "¿Cuándo vuelve el programa de los sábados?", 0),
		msg("Fede", "¿Cuándo vuelve el programa de los sábados?", 0),
		// Ruido del chat real: emojis como texto, saludos, charla entre usuarios y mayúsculas.
		msg("Gabi", "hola :hand-pink-waving: :hand-pink-waving: :face-with-tears-of-joy:", 0),
		msg("Hugo", "hola :hand-pink-waving: buenas tardes Angeles como estás?", 0),
		msg("Iris", "@JuanCa todo listo? tenés alguna red para hablarte?", 0),
		msg("Grito", "ESTO ES UNA VERGUENZA TOTAL SEÑORES", 0),
		msg("Grito", "NO PUEDE SER LO QUE ESTAN DICIENDO HOY", 0),
	})
	if len(a.Topics) == 0 || a.Topics[0] != (Count{"dólar blue", 3}) {
		t.Errorf("temas = %v, quería primero «dólar blue» en 3 mensajes", a.Topics)
	}
	for _, c := range a.Topics {
		if strings.Contains(c.Key, "hand") || strings.Contains(c.Key, "face") || c.Key == "hola" || c.Key == "buenas" {
			t.Errorf("tema con ruido: %q", c.Key)
		}
	}
	// Las preguntas cortas no cuentan y las repetidas aparecen una vez; primero la de más likes.
	if len(a.Questions) != 2 || a.Questions[0].Author != "Beto" || a.Questions[1].Author != "Fede" {
		t.Errorf("preguntas = %+v", a.Questions)
	}
	if len(a.Tech) != 1 || a.Tech[0] != (Count{"Audio", 1}) || len(a.TechMsgs) != 1 {
		t.Errorf("quejas técnicas = %v", a.Tech)
	}
	if len(a.Offenders) != 1 || a.Offenders[0] != (Offender{"Troll", 0, 2}) {
		t.Errorf("repetidos = %+v", a.Offenders)
	}
	if !strings.Contains(fmt.Sprint(a.Filters), "pelotudo") || !strings.Contains(fmt.Sprint(a.Filters), "corrupto") || a.Links != 1 {
		t.Errorf("filtros = %v, links = %d", a.Filters, a.Links)
	}
	if len(a.Candidates) != 1 || a.Candidates[0] != (Count{"gorila", 2}) {
		t.Errorf("candidatas = %v, quería gorila", a.Candidates)
	}
}

func TestLiveChatSelection(t *testing.T) {
	st, err := OpenStore(t.TempDir() + "/datos.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	st.SetVideos("Radio Test", []Video{
		{ID: "v1", Title: "🔴RADIO TEST en VIVO | LA MAÑANA", Channel: "Radio Test", LiveStatus: "live"},
		{ID: "v2", Title: "🔴 [EN VIVO] LA TARDE | OTRA RADIO", Channel: "Otra Radio", LiveStatus: "live"},
		{ID: "v3", Title: "Vivo terminado", Channel: "Radio Test", LiveStatus: "none"},
	})
	st.Add([]Message{
		{ID: "a", Source: "chat", VideoID: "v1", Author: "Ana", Text: "hola desde la mañana", At: time.Now()},
		{ID: "b", Source: "chat", VideoID: "v2", Author: "Beto", Text: "hola desde la tarde", At: time.Now()},
	})
	h := (&Server{st: st, budget: 9000}).Routes()
	get := func(url string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		return w
	}

	w := get("/parcial/vivo/chat?video=v2")
	body := w.Body.String()
	if w.Header().Get("HX-Retarget") != "" || !strings.Contains(body, "Mostrando OTRA RADIO en VIVO") ||
		!strings.Contains(body, "En pausa: RADIO TEST en VIVO") || !strings.Contains(body, "hola desde la tarde") ||
		strings.Contains(body, "hola desde la mañana") {
		t.Errorf("chat de v2:\n%s", body)
	}
	// Si el vivo pedido terminó, se recarga la sección entera con el primero en vivo.
	w = get("/parcial/vivo/chat?video=v3")
	if w.Header().Get("HX-Retarget") != "#vivo" || !strings.Contains(w.Body.String(), "video=v1") {
		t.Errorf("vivo terminado: retarget %q\n%s", w.Header().Get("HX-Retarget"), w.Body.String())
	}
	if body := get("/parcial/vivo/analisis?video=v1").Body.String(); !strings.Contains(body, "Análisis de RADIO TEST en VIVO") {
		t.Errorf("análisis de v1:\n%s", body)
	}
}

// streamItem arma un pedazo del streaming del chat con un mensaje.
func streamItem(token, id, text string) string {
	return `{"nextPageToken":"` + token + `","items":[{"id":"` + id + `","snippet":{"displayMessage":"` + text +
		`","publishedAt":"` + time.Now().UTC().Format(time.RFC3339) + `"},"authorDetails":{"displayName":"Beto"}}]}`
}

func TestStreamChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/liveChat/messages/stream" || q.Get("key") != "k" || q.Get("liveChatId") != "chat1" {
			w.WriteHeader(404)
			return
		}
		f := w.(http.Flusher)
		switch q.Get("pageToken") {
		case "":
			// Cada pedazo se manda apenas está, como hace YouTube.
			w.Write([]byte("[" + streamItem("t1", "m1", "primero")))
			f.Flush()
			w.Write([]byte(",\n" + streamItem("t2", "m2", "segundo") + "]"))
		case "rechazado":
			w.WriteHeader(403)
			w.Write([]byte(`[{"error":{"code":403,"message":"The caller does not have permission"}}]`))
		case "terminado":
			w.Write([]byte(`[{"error":{"code":403,"message":"The live chat is no longer live."}}]`))
		case "colgado":
			w.Write([]byte("[" + streamItem("t3", "m3", "y después nada")))
			f.Flush()
			<-r.Context().Done()
		}
	}))
	defer srv.Close()
	yt := NewYouTube("k")
	yt.BaseURL = srv.URL

	var got []ChatBatch
	collect := func(b ChatBatch) { got = append(got, b) }
	if err := yt.StreamChat(context.Background(), "v1", "chat1", "", collect); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Units != 5 || got[1].Msgs[0].Text != "primero" || got[2].Token != "t2" ||
		got[2].Msgs[0].VideoID != "v1" || got[2].Msgs[0].Source != "chat" {
		t.Fatalf("pedazos = %+v", got)
	}
	if yt.Units != 0 {
		t.Errorf("StreamChat no debe tocar Units (corre en otra goroutine): %d", yt.Units)
	}

	for _, token := range []string{"rechazado", "terminado"} {
		got = nil
		err := yt.StreamChat(context.Background(), "v1", "chat1", token, collect)
		if err == nil || !isForbidden(err) || strings.Contains(err.Error(), "k&") {
			t.Errorf("%s: error = %v", token, err)
		}
		if len(got) != 1 || got[0].Units != 5 {
			t.Errorf("%s: pedazos = %+v", token, got)
		}
	}

	// Si la conexión queda colgada, se corta sola y sin error para volver a abrirla.
	defer func(d time.Duration) { streamIdle = d }(streamIdle)
	streamIdle = 100 * time.Millisecond
	got = nil
	if err := yt.StreamChat(context.Background(), "v1", "chat1", "colgado", collect); err != nil || len(got) != 2 {
		t.Errorf("colgado: error = %v, pedazos = %+v", err, got)
	}
}

// TestWorkerStreamsChat corre el worker contra una API falsa: el chat del
// primer vivo llega por streaming y el del segundo, que no lo permite, por
// lecturas comunes.
func TestWorkerStreamsChat(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	stop := make(chan struct{}) // el worker deja el streaming abierto: así se corta al terminar
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch r.URL.Path {
		case "/channels":
			w.Write([]byte(`{"items":[{"snippet":{"title":"Radio Test"},"contentDetails":{"relatedPlaylists":{"uploads":"UU1"}}}]}`))
		case "/playlistItems":
			if q.Get("playlistId") == "UULV1" {
				w.Write([]byte(`{"items":[{"contentDetails":{"videoId":"s1"}},{"contentDetails":{"videoId":"p1"}}]}`))
				return
			}
			w.Write([]byte(`{"items":[]}`))
		case "/videos":
			live := func(id, title, chat string) string {
				return `{"id":"` + id + `","snippet":{"title":"` + title + `","channelTitle":"Radio Test","publishedAt":"` + now +
					`","liveBroadcastContent":"live"},"statistics":{},"liveStreamingDetails":{"activeLiveChatId":"` + chat + `"}}`
			}
			w.Write([]byte(`{"items":[` + live("s1", "LA MAÑANA | EN VIVO", "cs") + "," + live("p1", "LA TARDE | EN VIVO", "cp") + `]}`))
		case "/liveChat/messages/stream":
			if q.Get("liveChatId") != "cs" {
				w.WriteHeader(404)
				return
			}
			w.Write([]byte("[" + streamItem("t1", "s-1", "en tiempo real")))
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-stop:
			}
		case "/liveChat/messages":
			w.Write([]byte(`{"pollingIntervalMillis":1,"items":[{"id":"p-1","snippet":{"displayMessage":"leído en turno","publishedAt":"` + now + `"},"authorDetails":{"displayName":"Ana"}}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	defer close(stop)
	st, err := OpenStore(t.TempDir() + "/datos.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	yt := NewYouTube("k")
	yt.BaseURL = srv.URL
	go RunWorker(yt, st, WorkerConfig{
		Channels: []string{"C"}, Videos: 5, VideosEvery: time.Hour, CommentsEvery: time.Hour,
		ChatEvery: time.Millisecond, ChatPeak: time.Millisecond, Budget: 9000,
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s, p := st.LastChat("s1", 5), st.LastChat("p1", 5)
		if len(s) == 1 && len(p) == 1 {
			if s[0].Program != "LA MAÑANA" || p[0].Text != "leído en turno" {
				t.Fatalf("mensajes: %+v %+v", s, p)
			}
			// 4 de canal y videos + 5 del streaming + 5 del streaming rechazado + 5 por cada lectura común.
			if u := st.Units(); u < 19 || (u-14)%5 != 0 {
				t.Errorf("unidades = %d", u)
			}
			if note := st.Status().ChatNote; !strings.Contains(note, "tiempo real") || !strings.Contains(note, "Sin tiempo real en 1 vivo") {
				t.Errorf("nota del chat = %q", note)
			}
			info := st.Status().ChatInfo
			if !info["s1"].Open || !info["s1"].Stream || info["p1"].Stream || info["p1"].Got.IsZero() {
				t.Errorf("estado del chat = %+v", info)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no llegaron los mensajes: streaming %+v, lecturas %+v", st.LastChat("s1", 5), st.LastChat("p1", 5))
}
