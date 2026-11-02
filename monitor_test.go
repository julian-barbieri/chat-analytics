package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"
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
	started := time.Now().Add(-70 * time.Hour).UTC().Truncate(time.Second)
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
				"v2": `{"id":"v2","snippet":{"title":"🔴RADIO TEST en VIVO | LA MAÑANA","channelTitle":"Radio Test","publishedAt":"` + old + `","liveBroadcastContent":"live"},"statistics":{"viewCount":"100","commentCount":"0"},"liveStreamingDetails":{"activeLiveChatId":"chat1","concurrentViewers":"40","actualStartTime":"` + started.Format(time.RFC3339) + `"}}`,
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
	// El inicio del vivo viene en la misma parte que ya se pide: no cuesta cuota.
	if !vids[0].StartedAt.Equal(started) {
		t.Errorf("StartedAt = %v, quería %v", vids[0].StartedAt, started)
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

// startWorker lanza RunWorker y devuelve cómo pararlo y esperar a que vuelva.
// Va en un defer y no en t.Cleanup: t.Cleanup correría después de los defer
// que cierran el servidor falso o restauran variables globales, con el worker
// todavía vivo.
func startWorker(t *testing.T, yt *YouTube, st *Store, cfg WorkerConfig) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunWorker(ctx, yt, st, cfg)
		close(done)
	}()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("RunWorker no volvió 5 s después de cancelar")
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
	// Se para antes de srv.Close y de restaurar realTimeGap (los defer corren al revés).
	stopWorker := startWorker(t, yt, st, WorkerConfig{
		Channels: []string{"C"}, Videos: 5, Extra: []string{"r1"}, VideosEvery: time.Hour, CommentsEvery: time.Hour,
		ChatEvery: time.Hour, ChatPeak: time.Hour, Budget: 9000, RealTime: map[string]bool{"r1": true},
	})
	defer stopWorker()
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
	stop := make(chan struct{}) // por si el worker no corta el streaming: así srv.Close no se cuelga
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
	// Se para antes de close(stop) y de srv.Close (los defer corren al revés).
	stopWorker := startWorker(t, yt, st, WorkerConfig{
		Channels: []string{"C"}, Videos: 5, VideosEvery: time.Hour, CommentsEvery: time.Hour,
		ChatEvery: time.Millisecond, ChatPeak: time.Millisecond, Budget: 9000,
	})
	defer stopWorker()

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

// TestWorkerStops: al cancelar el contexto, RunWorker corta sus streaming y
// vuelve, y después no escribe nada más en el directorio de datos.
func TestWorkerStops(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	stop := make(chan struct{}) // si el arreglo no anda, igual se corta el streaming para que srv.Close no se cuelgue
	clientGone := make(chan struct{})
	var gone sync.Once
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
				gone.Do(func() { close(clientGone) }) // la cortó el worker
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
	dir := t.TempDir()
	st, err := OpenStore(dir + "/datos.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	yt := NewYouTube("k")
	yt.BaseURL = srv.URL
	ctx, cancel := context.WithCancel(context.Background())
	done := runWorker(ctx, yt, st, WorkerConfig{
		Channels: []string{"C"}, Videos: 5, VideosEvery: time.Hour, CommentsEvery: time.Hour,
		ChatEvery: time.Millisecond, ChatPeak: time.Millisecond, Budget: 9000,
	})
	defer func() { // si falla antes, que no quede vivo escribiendo en el TempDir
		cancel()
		returned(done, 5*time.Second)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for len(st.LastChat("s1", 5)) == 0 || len(st.LastChat("p1", 5)) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no llegaron los mensajes: streaming %+v, lecturas %+v", st.LastChat("s1", 5), st.LastChat("p1", 5))
		}
		time.Sleep(50 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("RunWorker no volvió 3 s después de cancelar")
	}
	select {
	case <-clientGone:
	case <-time.After(time.Second):
		t.Fatal("el worker volvió sin cortar la conexión de streaming")
	}

	// Más de un tick del worker: si quedó algo corriendo, cambia la cuota o algún archivo.
	state := func() string {
		files, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		s := fmt.Sprintf("unidades=%d", st.Units())
		for _, f := range files {
			info, err := f.Info()
			if err != nil {
				t.Fatal(err)
			}
			s += fmt.Sprintf(" %s:%d:%s", f.Name(), info.Size(), info.ModTime().Format(time.RFC3339Nano))
		}
		return s
	}
	before := state()
	time.Sleep(1500 * time.Millisecond)
	if after := state(); after != before {
		t.Errorf("el worker siguió escribiendo después de volver:\nantes:   %s\ndespués: %s", before, after)
	}
}

// runWorker lanza RunWorker con ctx y devuelve un canal que se cierra cuando vuelve.
func runWorker(ctx context.Context, yt *YouTube, st *Store, cfg WorkerConfig) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		RunWorker(ctx, yt, st, cfg)
		close(done)
	}()
	return done
}

// returned dice si done se cerró dentro de d.
func returned(done <-chan struct{}, d time.Duration) bool {
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// TestWorkerStopsWithoutStreams: sin ningún streaming abierto (no hay vivos o
// el único chat quedó bloqueado), cancelar hace volver a RunWorker enseguida:
// no hay ningún done que esperar.
func TestWorkerStopsWithoutStreams(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	cases := map[string]struct {
		videos string
		ready  func(Snapshot) bool
	}{
		"sin vivos": {
			videos: `{"items":[]}`,
			ready:  func(s Snapshot) bool { return s.ChatNote == "No hay un vivo en este momento." },
		},
		"chat sin streaming y bloqueado": {
			videos: `{"items":[{"id":"p1","snippet":{"title":"LA TARDE | EN VIVO","channelTitle":"Radio Test","publishedAt":"` + now +
				`","liveBroadcastContent":"live"},"statistics":{},"liveStreamingDetails":{"activeLiveChatId":"cp"}}]}`,
			ready: func(s Snapshot) bool { return s.ChatInfo["p1"].Blocked },
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/channels":
					w.Write([]byte(`{"items":[{"snippet":{"title":"Radio Test"},"contentDetails":{"relatedPlaylists":{"uploads":"UU1"}}}]}`))
				case "/playlistItems":
					w.Write([]byte(`{"items":[]}`))
				case "/videos":
					w.Write([]byte(tc.videos))
				case "/liveChat/messages/stream":
					w.WriteHeader(404) // sin streaming: pasa a lecturas comunes
				case "/liveChat/messages":
					w.WriteHeader(403) // y tampoco deja leer: queda bloqueado
					w.Write([]byte(`{"error":{"message":"forbidden"}}`))
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
			ctx, cancel := context.WithCancel(context.Background())
			done := runWorker(ctx, yt, st, WorkerConfig{
				Channels: []string{"C"}, Videos: 5, Extra: []string{"p1"}, VideosEvery: time.Hour, CommentsEvery: time.Hour,
				ChatEvery: time.Millisecond, ChatPeak: time.Millisecond, Budget: 9000,
			})
			defer func() { // si falla antes, que no quede vivo
				cancel()
				returned(done, 5*time.Second)
			}()

			deadline := time.Now().Add(5 * time.Second)
			for !tc.ready(st.Status()) {
				if time.Now().After(deadline) {
					t.Fatalf("el worker no llegó al estado esperado: %+v", st.Status())
				}
				time.Sleep(20 * time.Millisecond)
			}
			cancel()
			if !returned(done, time.Second) {
				t.Fatal("RunWorker no volvió 1 s después de cancelar sin streaming abiertos")
			}
		})
	}
}

// TestWorkerStopsBeforeStart: con el contexto ya cancelado, RunWorker hace a lo
// sumo una vuelta y vuelve; el streaming que abre en esa vuelta nace cancelado.
func TestWorkerStopsBeforeStart(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/channels":
			w.Write([]byte(`{"items":[{"snippet":{"title":"Radio Test"},"contentDetails":{"relatedPlaylists":{"uploads":"UU1"}}}]}`))
		case "/playlistItems":
			w.Write([]byte(`{"items":[]}`))
		case "/videos":
			w.Write([]byte(`{"items":[{"id":"s1","snippet":{"title":"LA MAÑANA | EN VIVO","channelTitle":"Radio Test","publishedAt":"` + now +
				`","liveBroadcastContent":"live"},"statistics":{},"liveStreamingDetails":{"activeLiveChatId":"cs"}}]}`))
		case "/liveChat/messages/stream":
			w.Write([]byte("[" + streamItem("t1", "s-1", "hola")))
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-stop:
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	closeStop := sync.OnceFunc(func() { close(stop) })
	defer closeStop()
	st, err := OpenStore(t.TempDir() + "/datos.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	yt := NewYouTube("k")
	yt.BaseURL = srv.URL
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := runWorker(ctx, yt, st, WorkerConfig{
		Channels: []string{"C"}, Videos: 5, Extra: []string{"s1"}, VideosEvery: time.Hour, CommentsEvery: time.Hour,
		ChatEvery: time.Millisecond, ChatPeak: time.Millisecond, Budget: 9000,
	})
	if !returned(done, 3*time.Second) {
		closeStop() // destraba el handler por si el streaming quedó abierto
		returned(done, 5*time.Second)
		t.Fatal("RunWorker no volvió con el contexto ya cancelado")
	}
}

// TestWorkerStopsAfterLiveEnds: un vivo termina, el worker corta su streaming y
// recibe su done (que llega a un estado ya olvidado) antes de que se cancele
// ctx. El contador de streaming no puede quedar desbalanceado: RunWorker vuelve.
func TestWorkerStopsAfterLiveEnds(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	var ended atomic.Bool
	stop := make(chan struct{})
	clientGone := make(chan struct{})
	var gone sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/channels":
			w.Write([]byte(`{"items":[{"snippet":{"title":"Radio Test"},"contentDetails":{"relatedPlaylists":{"uploads":"UU1"}}}]}`))
		case "/playlistItems":
			w.Write([]byte(`{"items":[]}`))
		case "/videos":
			if ended.Load() {
				w.Write([]byte(`{"items":[]}`))
				return
			}
			w.Write([]byte(`{"items":[{"id":"s1","snippet":{"title":"LA MAÑANA | EN VIVO","channelTitle":"Radio Test","publishedAt":"` + now +
				`","liveBroadcastContent":"live"},"statistics":{},"liveStreamingDetails":{"activeLiveChatId":"cs"}}]}`))
		case "/liveChat/messages/stream":
			if ended.Load() {
				w.WriteHeader(404)
				return
			}
			w.Write([]byte("[" + streamItem("t1", "s-1", "hola")))
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				gone.Do(func() { close(clientGone) })
			case <-stop:
			}
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
	ctx, cancel := context.WithCancel(context.Background())
	done := runWorker(ctx, yt, st, WorkerConfig{
		Channels: []string{"C"}, Videos: 5, Extra: []string{"s1"}, VideosEvery: 10 * time.Millisecond, CommentsEvery: time.Hour,
		ChatEvery: time.Millisecond, ChatPeak: time.Millisecond, Budget: 9000,
	})
	defer func() { // si falla antes, que no quede vivo
		cancel()
		returned(done, 5*time.Second)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for len(st.LastChat("s1", 5)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no llegó el mensaje del streaming")
		}
		time.Sleep(20 * time.Millisecond)
	}
	ended.Store(true) // en la próxima revisión de videos el vivo ya no está
	select {
	case <-clientGone:
	case <-time.After(5 * time.Second):
		t.Fatal("el worker no cortó el streaming del vivo que terminó")
	}
	// Margen para que el done del vivo olvidado pase por el select principal
	// (no por el drenaje del cancel), que es el caso que se quiere cubrir.
	time.Sleep(300 * time.Millisecond)
	if s := st.Status(); s.ChatNote != "No hay un vivo en este momento." {
		t.Fatalf("el worker no olvidó el vivo: %q", s.ChatNote)
	}

	cancel()
	if !returned(done, 3*time.Second) {
		t.Fatal("RunWorker no volvió: el done del vivo que terminó desbalanceó el contador de streaming")
	}
}

// TestParticipation: franjas de 5 minutos del chat de un vivo con mensajes,
// personas, espectadores y participación.
func TestParticipation(t *testing.T) {
	now := time.Date(2026, 10, 30, 15, 2, 0, 0, time.UTC)
	at := func(h, m, s int) time.Time { return time.Date(2026, 10, 30, h, m, s, 0, time.UTC) }
	chat := func(id, author string, t time.Time) Message {
		return Message{ID: id, Source: "chat", VideoID: "v", Author: author, Text: "hola", At: t}
	}
	msgs := []Message{
		chat("1", "Ana", at(14, 5, 0)), // justo en el borde: franja 14:05
		chat("2", "Ana", at(14, 7, 0)), // mismo autor: una persona
		chat("3", "Beto", at(14, 9, 59)),
		chat("4", "Caro", at(14, 9, 0)),
		chat("5", "", at(14, 8, 0)), // sin autor: cuenta como mensaje, no como persona
		chat("6", "Gabi", at(14, 4, 59)),
		{ID: "7", Source: "comentario", VideoID: "v", Author: "Dani", At: at(14, 6, 0)},
		{ID: "8", Source: "chat", VideoID: "otro", Author: "Eva", At: at(14, 6, 0)},
		chat("9", "Fede", now.Add(-25*time.Hour)), // fuera de la ventana
	}
	viewers := []ViewerSample{
		{"v", at(14, 6, 0), 100}, {"v", at(14, 8, 0), 200},
		{"v", at(14, 1, 0), 0}, // 0 es "sin dato"
		{"otro", at(14, 6, 0), 9999},
		{"v", at(14, 11, 0), 50}, // franja sin mensajes pero con espectadores: 0 %
	}
	slots, typical, ok := Participation(msgs, viewers, "v", now)
	if len(slots) != 288 {
		t.Fatalf("franjas = %d, quería 288", len(slots))
	}
	if first, last := slots[0].Start, slots[287].Start; !first.Equal(at(15, 5, 0).Add(-24*time.Hour)) || !last.Equal(at(15, 0, 0)) {
		t.Fatalf("ventana de %v a %v", first, last)
	}
	byStart := map[time.Time]Slot{}
	total := 0
	for _, s := range slots {
		byStart[s.Start] = s
		total += s.N
	}
	if total != 6 {
		t.Errorf("mensajes en la ventana = %d, quería 6 (sin comentarios, otros vivos ni lo de afuera)", total)
	}
	cases := []struct {
		start      time.Time
		n, authors int
		viewers    int64
		pct        float64
		why        string
	}{
		{at(14, 5, 0), 5, 3, 150, 2.0, "promedio de 100 y 200; 3 personas de 150"},
		{at(14, 0, 0), 1, 1, 0, 0, "solo una muestra con 0: sin dato"},
		{at(14, 10, 0), 0, 0, 50, 0, "con espectadores y nadie escribió"},
		{at(13, 0, 0), 0, 0, 0, 0, "vacía"},
	}
	for _, c := range cases {
		s := byStart[c.start]
		if s.N != c.n || s.Authors != c.authors || s.Viewers != c.viewers || s.Pct != c.pct || len(s.Msgs) != c.n {
			t.Errorf("franja %s (%s) = N %d, personas %d, espectadores %d, %% %v; quería %d, %d, %d, %v",
				c.start.Format("15:04"), c.why, s.N, s.Authors, s.Viewers, s.Pct, c.n, c.authors, c.viewers, c.pct)
		}
	}
	// Mediana de las franjas con espectadores (2 % y 0 %), incluida la de 0 personas.
	if !ok || typical != 1.0 {
		t.Errorf("participación típica = %v (%v), quería 1", typical, ok)
	}
	if _, _, ok := Participation(msgs, nil, "v", now); ok {
		t.Errorf("sin muestras de espectadores la participación típica debería ser sin dato")
	}
	three := append(viewers, ViewerSample{"v", at(14, 21, 0), 10})
	if _, typical, ok := Participation(msgs, three, "v", now); !ok || typical != 0 {
		t.Errorf("mediana de 0, 0 y 2 = %v (%v), quería 0", typical, ok)
	}
}

func TestReelLink(t *testing.T) {
	now := time.Date(2026, 10, 30, 15, 2, 0, 0, time.UTC)
	const far = "Fuera de lo que se puede retroceder en YouTube: usá la grabación propia"
	const live = "https://www.youtube.com/watch?v=abc"
	cases := []struct {
		name      string
		status    string
		started   time.Time
		slot      time.Time
		href, has string
	}{
		{"vivo corto", "none", now.Add(-3 * time.Hour), now.Add(-62 * time.Minute), "https://www.youtube.com/watch?v=abc&t=7080s", ""},
		{"vivo de 12 h justas", "none", now.Add(-12 * time.Hour), now.Add(-12 * time.Hour), "https://www.youtube.com/watch?v=abc&t=0s", ""},
		{"franja antes del inicio", "none", now.Add(-3 * time.Hour), now.Add(-3*time.Hour - 2*time.Minute), "", "Retrocedé ~3 h 2 min en el reproductor del vivo"},
		{"vivo de días, franja reciente", "none", now.Add(-5 * 24 * time.Hour), now.Add(-127 * time.Minute), "", "Retrocedé ~2 h 7 min en el reproductor del vivo"},
		{"vivo de días, franja vieja", "none", now.Add(-5 * 24 * time.Hour), now.Add(-13 * time.Hour), "", far},
		{"sin inicio, franja reciente", "none", time.Time{}, now.Add(-62 * time.Minute), "", "Retrocedé ~1 h 2 min en el reproductor del vivo"},
		{"sin inicio, franja vieja", "none", time.Time{}, now.Add(-13 * time.Hour), "", far},
		// En un vivo en curso YouTube ignora t= y abre en el directo: se linkea
		// el vivo y se dice cuánto retroceder.
		{"en curso, vivo corto", "live", now.Add(-3 * time.Hour), now.Add(-62 * time.Minute), live, "Abrí el vivo y retrocedé ~1 h 2 min en el reproductor"},
		{"en curso, vivo de 12 h justas", "live", now.Add(-12 * time.Hour), now.Add(-12 * time.Hour), live, "Abrí el vivo y retrocedé ~12 h 0 min en el reproductor"},
		{"en curso, vivo de días", "live", now.Add(-5 * 24 * time.Hour), now.Add(-127 * time.Minute), live, "Abrí el vivo y retrocedé ~2 h 7 min en el reproductor"},
		{"en curso, sin inicio", "live", time.Time{}, now.Add(-62 * time.Minute), live, "Abrí el vivo y retrocedé ~1 h 2 min en el reproductor"},
		{"en curso, franja vieja", "live", now.Add(-5 * 24 * time.Hour), now.Add(-12*time.Hour - time.Nanosecond), "", far},
		// Solo "live" lleva el link sin t=.
		{"programado", "upcoming", time.Time{}, now.Add(-62 * time.Minute), "", "Retrocedé ~1 h 2 min en el reproductor del vivo"},
	}
	for _, c := range cases {
		href, hint := reelLink(Video{ID: "abc", LiveStatus: c.status, StartedAt: c.started}, c.slot, now)
		if href != c.href || (c.has != "" && hint != c.has) || hint == "" {
			t.Errorf("%s: reelLink = %q, %q; quería %q, %q", c.name, href, hint, c.href, c.has)
		}
	}
}

func TestStoreViewers(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/datos.jsonl"
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	st.Add([]Message{{ID: "a", Source: "chat", VideoID: "v", Author: "Ana", Text: "hola", At: now}})
	st.AddViewers(now, []Video{
		{ID: "v", LiveStatus: "live", Concurrent: 120},
		{ID: "z", LiveStatus: "live"},                 // el canal oculta el contador
		{ID: "n", LiveStatus: "none", Concurrent: 50}, // no es un vivo
	})
	st.AddViewers(now.Add(-40*24*time.Hour), []Video{{ID: "v", LiveStatus: "live", Concurrent: 7}}) // vencida
	if got := st.Viewers("v", time.Time{}); len(got) != 2 {
		t.Fatalf("en memoria = %+v, quería 2 muestras", got)
	}
	got := st.Viewers("v", now.Add(-time.Hour))
	if len(got) != 1 || got[0].VideoID != "v" || got[0].Viewers != 120 || !got[0].At.Equal(now) {
		t.Fatalf("Viewers = %+v", got)
	}
	got[0].Viewers = 1
	if again := st.Viewers("v", now.Add(-time.Hour)); again[0].Viewers != 120 {
		t.Errorf("Viewers no devuelve una copia: %+v", again)
	}
	if z, n := st.Viewers("z", time.Time{}), st.Viewers("n", time.Time{}); len(z)+len(n) != 0 {
		t.Errorf("guardó videos sin espectadores o que no son vivos: %+v %+v", z, n)
	}

	vpath := dir + "/datos.espectadores.jsonl"
	b, err := os.ReadFile(vpath)
	if err != nil || strings.Count(string(b), "\n") != 2 {
		t.Fatalf("archivo de espectadores: %v\n%s", err, b)
	}
	f, err := os.OpenFile(vpath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("esto no es json\n")
	f.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	st2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st2.Viewers("v", time.Time{}); len(got) != 1 || got[0].Viewers != 120 || !got[0].At.Equal(now) {
		t.Errorf("después de reabrir = %+v, quería solo la muestra de 120", got)
	}
	b, _ = os.ReadFile(vpath)
	if strings.Count(string(b), "\n") != 1 || strings.Contains(string(b), "no es json") {
		t.Errorf("el archivo no se reescribió sin lo vencido ni lo ilegible:\n%s", b)
	}
	if _, err := os.Stat(vpath + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("quedó el archivo temporal: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) || len(st2.Snapshot().Msgs) != 1 {
		t.Errorf("datos.jsonl cambió:\nantes %s\ndespués %s", before, after)
	}
}

// TestWorkerRecordsViewers: cada revisión de videos guarda los espectadores
// de los vivos sin pedirle nada nuevo a la API.
func TestWorkerRecordsViewers(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	allowed := map[string]bool{"/channels": true, "/playlistItems": true, "/videos": true, "/commentThreads": true,
		"/liveChat/messages": true, "/liveChat/messages/stream": true}
	var mu sync.Mutex
	var unknown []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if !allowed[r.URL.Path] {
			unknown = append(unknown, r.URL.Path)
		}
		mu.Unlock()
		switch r.URL.Path {
		case "/channels":
			w.Write([]byte(`{"items":[{"snippet":{"title":"Radio Test"},"contentDetails":{"relatedPlaylists":{"uploads":"UU1"}}}]}`))
		case "/playlistItems":
			w.Write([]byte(`{"items":[]}`))
		case "/videos":
			// Sin activeLiveChatId: así el worker no abre el chat y la prueba no depende de él.
			w.Write([]byte(`{"items":[{"id":"w1","snippet":{"title":"LA MAÑANA | EN VIVO","channelTitle":"Radio Test","publishedAt":"` + now +
				`","liveBroadcastContent":"live"},"statistics":{},"liveStreamingDetails":{"concurrentViewers":"321"}}]}`))
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
	stopWorker := startWorker(t, yt, st, WorkerConfig{
		Channels: []string{"C"}, Videos: 5, Extra: []string{"w1"}, VideosEvery: time.Hour, CommentsEvery: time.Hour,
		ChatEvery: time.Hour, ChatPeak: time.Hour, Budget: 9000,
	})
	defer stopWorker()

	deadline := time.Now().Add(5 * time.Second)
	for len(st.Viewers("w1", time.Time{})) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := st.Viewers("w1", time.Time{}); len(got) != 1 || got[0].Viewers != 321 {
		t.Errorf("muestras = %+v, quería una de 321", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(unknown) > 0 {
		t.Errorf("el worker pidió rutas nuevas: %v", unknown)
	}
}

// TestParticipationBorders: bordes de la ventana de 24 h y del cambio de
// franja, redondeo del promedio de espectadores y franjas en hora local.
func TestParticipationBorders(t *testing.T) {
	now := time.Date(2026, 10, 30, 15, 2, 0, 0, time.UTC)
	end := time.Date(2026, 10, 30, 15, 5, 0, 0, time.UTC) // fin de la franja que contiene now
	start := end.Add(-24 * time.Hour)
	at := func(h, m, s, ns int) time.Time { return time.Date(2026, 10, 30, h, m, s, ns, time.UTC) }
	var msgs []Message
	add := func(id string, t time.Time) {
		msgs = append(msgs, Message{ID: id, Source: "chat", VideoID: "v", Author: id, Text: "hola", At: t})
	}
	add("inicio", start)                      // primera franja
	add("antes", start.Add(-time.Nanosecond)) // fuera de la ventana
	add("t", at(14, 5, 0, 0))                 // franja 14:05
	add("casi", at(14, 9, 59, 999999999))     // sigue en 14:05
	add("t+5", at(14, 10, 0, 0))              // ya es la franja 14:10
	add("now", now)                           // última franja
	add("fin-1ns", end.Add(-time.Nanosecond))
	add("fin", end) // franja futura: fuera
	add("futuro", now.Add(time.Hour))
	viewers := []ViewerSample{
		{"v", at(14, 10, 0, 0), 30},              // borde: va a 14:10, no a 14:05
		{"v", at(14, 5, 0, 0), 100},              // borde: va a 14:05
		{"v", at(14, 6, 0, 0), 100},              // 100, 100 y 101 dan 100
		{"v", at(14, 7, 0, 0), 101},              //
		{"v", start.Add(-time.Nanosecond), 9999}, // fuera de la ventana
		{"v", end, 9999},                         // franja futura
		{"v", at(13, 0, 0, 0), 101},              // 101 y 102 dan 102 (redondeo hacia arriba)
		{"v", at(13, 1, 0, 0), 102},
	}
	slots, _, _ := Participation(msgs, viewers, "v", now)
	byStart := map[time.Time]Slot{}
	total := 0
	for _, s := range slots {
		byStart[s.Start] = s
		total += s.N
		if s.Viewers == 9999 {
			t.Errorf("se contó una muestra de fuera de la ventana en %v", s.Start)
		}
	}
	if total != 6 {
		t.Errorf("mensajes en la ventana = %d, quería 6 (inicio, t, casi, t+5, now, fin-1ns)", total)
	}
	cases := []struct {
		start   time.Time
		n       int
		viewers int64
	}{
		{start, 1, 0},
		{at(14, 5, 0, 0), 2, 100},
		{at(14, 10, 0, 0), 1, 30},
		{at(15, 0, 0, 0), 2, 0},
		{at(13, 0, 0, 0), 0, 102},
	}
	for _, c := range cases {
		if s := byStart[c.start]; s.N != c.n || s.Viewers != c.viewers {
			t.Errorf("franja %v: N %d, espectadores %d; quería %d, %d", c.start, s.N, s.Viewers, c.n, c.viewers)
		}
	}

	// Con now en hora argentina las franjas siguen alineadas a 5 minutos locales.
	art := time.FixedZone("ART", -3*3600)
	slots, _, _ = Participation(nil, nil, "v", now.In(art))
	for _, s := range slots {
		if s.Start.In(art).Minute()%5 != 0 || s.Start.Second() != 0 {
			t.Fatalf("franja desalineada en hora local: %v", s.Start.In(art))
		}
	}
	if !slots[len(slots)-1].Start.Equal(at(15, 0, 0, 0)) {
		t.Errorf("la última franja debería contener now: %v", slots[len(slots)-1].Start)
	}
}

// TestReelLinkBorders: franja que contiene el inicio, vivo sin inicio y vivo
// de más de 12 h, en los bordes de la ventana de DVR.
func TestReelLinkBorders(t *testing.T) {
	now := time.Date(2026, 10, 30, 15, 2, 0, 0, time.UTC)
	slot := func(h, m int) time.Time { return time.Date(2026, 10, 30, h, m, 0, 0, time.UTC) }
	cases := []struct {
		name          string
		started, from time.Time
		href, hint    string
	}{
		// El vivo arrancó a las 12:02: la franja de las 12:00 lo contiene pero empieza antes.
		{"franja que contiene el inicio", slot(12, 2), slot(12, 0), "", "Retrocedé ~3 h 2 min en el reproductor del vivo"},
		{"primera franja entera después del inicio", slot(12, 2), slot(12, 5), "https://www.youtube.com/watch?v=abc&t=180s", "Ver en YouTube desde ese momento"},
		{"vivo de 12 h y 1 s", now.Add(-12*time.Hour - time.Second), slot(14, 0), "", "Retrocedé ~1 h 2 min en el reproductor del vivo"},
		{"franja de 12 h justas", time.Time{}, now.Add(-12 * time.Hour), "", "Retrocedé ~12 h 0 min en el reproductor del vivo"},
		{"franja de 12 h y 1 ns", time.Time{}, now.Add(-12*time.Hour - time.Nanosecond), "", "Fuera de lo que se puede retroceder en YouTube: usá la grabación propia"},
		{"franja actual sin inicio", time.Time{}, slot(15, 0), "", "Retrocedé ~0 h 2 min en el reproductor del vivo"},
		{"franja actual de vivo corto", slot(14, 0), slot(15, 0), "https://www.youtube.com/watch?v=abc&t=3600s", "Ver en YouTube desde ese momento"},
	}
	for _, c := range cases {
		href, hint := reelLink(Video{ID: "abc", StartedAt: c.started}, c.from, now)
		if href != c.href || hint != c.hint {
			t.Errorf("%s: reelLink = %q, %q; quería %q, %q", c.name, href, hint, c.href, c.hint)
		}
	}
}

// TestStoreViewersOpen: archivo de espectadores que ya existe con líneas
// ilegibles, vacías y vencidas al abrir; datos.jsonl no se toca y lo nuevo se
// agrega al final sin pisar lo cargado.
func TestStoreViewersOpen(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/datos.jsonl"
	vpath := dir + "/datos.espectadores.jsonl"
	now := time.Now().Truncate(time.Second)
	line := func(v ViewerSample) string {
		return fmt.Sprintf(`{"VideoID":%q,"At":%q,"Viewers":%d}`, v.VideoID, v.At.Format(time.RFC3339Nano), v.Viewers)
	}
	content := strings.Join([]string{
		line(ViewerSample{"v", now.Add(-time.Hour), 10}),
		"",
		"basura {",
		`{"VideoID":"v","At":"no es fecha","Viewers":3}`,
		"null", // se decodifica sin error pero sin fecha: vencida
		line(ViewerSample{"v", now.Add(-31 * 24 * time.Hour), 99}),            // vencida
		line(ViewerSample{"v", now.Add(-29 * 24 * time.Hour), 20}),            // todavía vale
		`{"VideoID":"v","At":"` + now.Format(time.RFC3339) + `","Viewers":30`, // cortada a la mitad
	}, "\n") + "\n"
	if err := os.WriteFile(vpath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// Sin datos.jsonl: OpenStore no tiene que crearlo por cargar los espectadores.
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("OpenStore creó datos.jsonl sin mensajes: %v", err)
	}
	got := st.Viewers("v", time.Time{})
	if len(got) != 2 || got[0].Viewers != 10 || got[1].Viewers != 20 {
		t.Fatalf("muestras cargadas = %+v, quería 10 y 20", got)
	}
	b, _ := os.ReadFile(vpath)
	if strings.Count(string(b), "\n") != 2 || strings.Contains(string(b), "basura") || strings.Contains(string(b), `"Viewers":99`) {
		t.Errorf("archivo reescrito:\n%s", b)
	}

	// Con mensajes guardados: reabrir no cambia datos.jsonl y AddViewers agrega al final.
	st.Add([]Message{
		{ID: "a", Source: "chat", VideoID: "v", Author: "Ana", Text: "hola", At: now},
		{ID: "b", Source: "comentario", VideoID: "x", Author: "Beto", Text: "chau", At: now.Add(-time.Minute)},
	})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	st2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	st2.AddViewers(now, []Video{{ID: "v", LiveStatus: "live", Concurrent: 40}, {ID: "u", LiveStatus: "upcoming", Concurrent: 5}})
	st3, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st3.Viewers("v", time.Time{}); len(got) != 3 || got[2].Viewers != 40 {
		t.Errorf("después de AddViewers y reabrir = %+v, quería 10, 20 y 40", got)
	}
	if got := st3.Viewers("u", time.Time{}); len(got) != 0 {
		t.Errorf("guardó un vivo programado: %+v", got)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) || len(st3.Snapshot().Msgs) != 2 {
		t.Errorf("datos.jsonl cambió:\nantes %s\ndespués %s", before, after)
	}
	if got := st3.Viewers("v", now); len(got) != 1 {
		t.Errorf("Viewers con since = now debería incluir la muestra de now: %+v", got)
	}
}

// TestStoreViewersLongLine: una línea dañada de más de 64 KB en el archivo de
// espectadores (por ejemplo, ceros de un corte de luz) se saltea sola, sin
// perder las muestras que vienen después; y si la lectura falla por E/S, el
// archivo no se reescribe con lo leído a medias.
func TestStoreViewersLongLine(t *testing.T) {
	dir := t.TempDir()
	vpath := dir + "/datos.espectadores.jsonl"
	at := time.Now().Add(-time.Hour)
	line := func(n int64) string {
		return fmt.Sprintf(`{"VideoID":"v","At":%q,"Viewers":%d}`, at.Format(time.RFC3339Nano), n)
	}
	// La última sin salto de línea: también se tiene que leer.
	content := line(10) + "\n" + strings.Repeat("\x00", 70*1024) + "\n" + line(20) + "\n" + line(30)
	if err := os.WriteFile(vpath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := OpenStore(dir + "/datos.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	got := st.Viewers("v", time.Time{})
	if len(got) != 3 || got[0].Viewers != 10 || got[1].Viewers != 20 || got[2].Viewers != 30 {
		t.Errorf("muestras = %+v, quería 10, 20 y 30 (se saltea solo la línea dañada)", got)
	}
	b, _ := os.ReadFile(vpath)
	for _, want := range []string{`"Viewers":10`, `"Viewers":20`, `"Viewers":30`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("el archivo reescrito perdió %s:\n%s", want, b)
		}
	}
	if strings.Count(string(b), "\n") != 3 || strings.Contains(string(b), "\x00") {
		t.Errorf("el archivo reescrito tiene que quedar con las 3 muestras válidas:\n%q", b)
	}

	// Error de E/S al leer (acá, el archivo es una carpeta): OpenStore avisa y
	// no intenta reescribir (no deja el .tmp de rewriteJSONL).
	dir2 := t.TempDir()
	if err := os.Mkdir(dir2+"/datos.espectadores.jsonl", 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(dir2 + "/datos.jsonl"); err == nil {
		t.Errorf("OpenStore no avisó el error de lectura de espectadores")
	}
	if _, err := os.Stat(dir2 + "/datos.espectadores.jsonl.tmp"); !os.IsNotExist(err) {
		t.Errorf("se intentó reescribir el archivo de espectadores después de un error de lectura: %v", err)
	}
}

// TestVideosStartedAt: la decodificación de videos con y sin actualStartTime
// (vivo programado, video común, vivo con fracciones de segundo), sin costo extra.
func TestVideosStartedAt(t *testing.T) {
	var parts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts = append(parts, r.URL.Query().Get("part"))
		w.Write([]byte(`{"items":[
			{"id":"up","snippet":{"title":"Mañana","liveBroadcastContent":"upcoming"},"statistics":{},
			 "liveStreamingDetails":{"scheduledStartTime":"2026-10-31T12:00:00Z","activeLiveChatId":"c1"}},
			{"id":"vod","snippet":{"title":"Nota","liveBroadcastContent":"none"},"statistics":{"viewCount":"3"}},
			{"id":"live","snippet":{"title":"En vivo","liveBroadcastContent":"live"},"statistics":{},
			 "liveStreamingDetails":{"actualStartTime":"2026-10-30T09:15:30.250Z","activeLiveChatId":"c2"}}
		]}`))
	}))
	defer srv.Close()
	yt := NewYouTube("k")
	yt.BaseURL = srv.URL
	vids, err := yt.Videos([]string{"up", "vod", "live"})
	if err != nil || len(vids) != 3 {
		t.Fatalf("Videos: %v %+v", err, vids)
	}
	if !vids[0].StartedAt.IsZero() || vids[0].LiveChatID != "c1" || vids[0].Concurrent != 0 {
		t.Errorf("vivo programado: %+v", vids[0])
	}
	if !vids[1].StartedAt.IsZero() {
		t.Errorf("video sin liveStreamingDetails: %+v", vids[1])
	}
	if want := time.Date(2026, 10, 30, 9, 15, 30, 250e6, time.UTC); !vids[2].StartedAt.Equal(want) || vids[2].Concurrent != 0 {
		t.Errorf("vivo sin concurrentViewers: StartedAt %v, Concurrent %d", vids[2].StartedAt, vids[2].Concurrent)
	}
	if yt.Units != 1 || len(parts) != 1 || parts[0] != "snippet,statistics,liveStreamingDetails" {
		t.Errorf("costo %d, pedidos %v", yt.Units, parts)
	}
}

// TestReadViewersBorders: bordes del lector de espectadores con buffer de
// 64 KB: líneas justo en el límite con y sin salto final, varias largas
// seguidas, archivo vacío o hecho de una sola línea larga y CRLF de Windows.
// Cada caso se lee también de a un byte, de a mitades y con el EOF junto a los
// últimos datos, para no depender de cómo el sistema entregue las lecturas.
func TestReadViewersBorders(t *testing.T) {
	now := time.Now()
	cutoff := now.Add(-retention)
	valid := func(n int) string {
		return fmt.Sprintf(`{"VideoID":"v","At":%q,"Viewers":%d}`, now.Add(-time.Hour).Format(time.RFC3339Nano), n)
	}
	// padded: muestra válida rellena con espacios (JSON los admite) hasta size bytes.
	padded := func(n, size int) string {
		v := valid(n)
		return v + strings.Repeat(" ", size-len(v))
	}
	junk := func(size int) string { return strings.Repeat("\x00", size) }
	const buf = maxViewerLine
	old := fmt.Sprintf(`{"VideoID":"v","At":%q,"Viewers":99}`, now.Add(-31*24*time.Hour).Format(time.RFC3339Nano))
	cases := []struct {
		name string
		in   string
		want []int
	}{
		{"vacío", "", nil},
		{"solo saltos", "\n\n\r\n", nil},
		{"una línea larga sin salto", junk(70 * 1024), nil},
		{"una línea larga con salto", junk(70*1024) + "\n", nil},
		{"justo el buffer sin salto", junk(buf), nil},
		{"justo el buffer con salto", junk(buf) + "\n" + valid(1) + "\n", []int{1}},
		{"dos buffers justos con salto", junk(2*buf) + "\n" + valid(1), []int{1}},
		{"buffer menos uno con salto entra", padded(1, buf-1) + "\n" + valid(2) + "\n", []int{1, 2}},
		{"buffer más salto se descarta", padded(1, buf) + "\n" + valid(2) + "\n", []int{2}},
		{"buffer menos uno sin salto al final", valid(1) + "\n" + padded(2, buf-1), []int{1, 2}},
		// Basura y no una muestra rellena: con exactamente 64 KB al final sin
		// salto, si el EOF llega junto con los datos la línea se lee igual.
		{"buffer justo sin salto al final", valid(1) + "\n" + junk(buf), []int{1}},
		{"varias largas seguidas", valid(1) + "\n" + junk(70*1024) + "\n" + junk(buf) + "\n" + junk(3*buf+7) + "\n" + valid(2) + "\n", []int{1, 2}},
		{"larga pegada a una válida sin salto", junk(70*1024) + "\n" + valid(1), []int{1}},
		{"válida y después larga sin salto", valid(1) + "\n" + junk(70*1024), []int{1}},
		{"CRLF", valid(1) + "\r\n" + valid(2) + "\r\n", []int{1, 2}},
		{"CRLF con línea larga", valid(1) + "\r\n" + junk(70*1024) + "\r\n" + valid(2) + "\r\n", []int{1, 2}},
		{"CRLF sin salto final", valid(1) + "\r\n" + valid(2) + "\r", []int{1, 2}},
		{"vencida", old + "\n" + valid(1) + "\n", []int{1}},
	}
	readers := []struct {
		name string
		wrap func(io.Reader) io.Reader
	}{
		{"directo", func(r io.Reader) io.Reader { return r }},
		{"de a un byte", iotest.OneByteReader},
		{"de a mitades", iotest.HalfReader},
		{"EOF con datos", iotest.DataErrReader},
	}
	for _, c := range cases {
		for _, rd := range readers {
			got, err := readViewers(rd.wrap(strings.NewReader(c.in)), cutoff)
			if err != nil {
				t.Errorf("%s (%s): error %v", c.name, rd.name, err)
				continue
			}
			var ns []int
			for _, v := range got {
				ns = append(ns, int(v.Viewers))
			}
			if fmt.Sprint(ns) != fmt.Sprint(c.want) {
				t.Errorf("%s (%s): muestras %v, quería %v", c.name, rd.name, ns, c.want)
			}
		}
	}

	// Un error de lectura que no es EOF no devuelve lo leído a medias, ni
	// cuando cae en medio de una línea larga.
	boom := errors.New("disco roto")
	for name, r := range map[string]io.Reader{
		"después de una línea":      io.MultiReader(strings.NewReader(valid(1)+"\n"), iotest.ErrReader(boom)),
		"en medio de una larga":     io.MultiReader(strings.NewReader(valid(1)+"\n"+junk(70*1024)), iotest.ErrReader(boom)),
		"al principio":              iotest.ErrReader(boom),
		"timeout en la 2.ª lectura": iotest.TimeoutReader(strings.NewReader(valid(1) + "\n" + junk(buf) + "\n" + valid(2))),
	} {
		got, err := readViewers(r, cutoff)
		if err == nil || got != nil {
			t.Errorf("%s: readViewers = %v, %v; quería nil y el error", name, got, err)
		}
		if name != "timeout en la 2.ª lectura" && !errors.Is(err, boom) {
			t.Errorf("%s: el error no es el de lectura: %v", name, err)
		}
	}
}

// momentSlots arma franjas separadas por step con {personas, mirando} cada
// una (mirando 0 = sin dato) y el % calculado como en Participation. Cada
// persona manda un mensaje.
func momentSlots(step time.Duration, specs ...[2]int64) []Slot {
	base := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	slots := make([]Slot, len(specs))
	for i, sp := range specs {
		s := Slot{Start: base.Add(time.Duration(i) * step), N: int(sp[0]), Authors: int(sp[0]), Viewers: sp[1]}
		if s.Viewers > 0 {
			s.Pct = float64(s.Authors) / float64(s.Viewers) * 100
		}
		for j := range s.Authors {
			s.Msgs = append(s.Msgs, Message{ID: fmt.Sprint(i, "-", j), Source: "chat", VideoID: "v",
				Author: fmt.Sprint("p", j), Text: "mensaje", At: s.Start.Add(time.Duration(j) * time.Second)})
		}
		slots[i] = s
	}
	return slots
}

// starts devuelve el índice de franja de cada momento, para comparar el orden.
func starts(slots []Slot, ms []Moment) string {
	var out []int
	for _, m := range ms {
		for i, s := range slots {
			if s.Start.Equal(m.Start) {
				out = append(out, i)
			}
		}
	}
	return fmt.Sprint(out)
}

// TestTopMoments: las franjas con más % de participación, con el mínimo de
// personas, los desempates, el programa, los temas y los ejemplos.
func TestTopMoments(t *testing.T) {
	const sep = 15 * time.Minute // franjas que no se pisan por la separación
	cases := []struct {
		name  string
		slots []Slot
		want  string // índices de franja en el orden devuelto
	}{
		{"sin franjas", nil, "[]"},
		{"sin dato de espectadores", momentSlots(sep, [2]int64{6, 0}, [2]int64{10, 0}), "[]"},
		{"orden por %, sin las de 2 personas ni las sin dato",
			momentSlots(sep, [2]int64{10, 1000}, [2]int64{5, 100}, [2]int64{3, 50}, [2]int64{2, 10}, [2]int64{6, 0}), "[2 1 0]"},
		{"mínimo justo: 3 entra, 2 no", momentSlots(sep, [2]int64{3, 100}, [2]int64{2, 10}), "[0]"},
		{"empate de %: más personas primero", momentSlots(sep, [2]int64{4, 200}, [2]int64{6, 300}), "[1 0]"},
		{"empate total: la más reciente primero", momentSlots(sep, [2]int64{4, 200}, [2]int64{4, 200}, [2]int64{4, 200}), "[2 1 0]"},
	}
	for _, c := range cases {
		ms := TopMoments(c.slots, nil)
		if got := starts(c.slots, ms); got != c.want {
			t.Errorf("%s: franjas %s, quería %s", c.name, got, c.want)
		}
		for i, m := range ms {
			if m.Rank != i+1 {
				t.Errorf("%s: Rank del %d.º = %d", c.name, i+1, m.Rank)
			}
			if m.Pct != float64(m.Authors)/float64(m.Viewers)*100 || m.Viewers == 0 || m.Authors < 3 {
				t.Errorf("%s: el %% no cierra: %d personas, %d mirando, %v %%", c.name, m.Authors, m.Viewers, m.Pct)
			}
		}
	}
	// Vivo sin mensajes, pasando por Participation como en el servidor.
	slots, _, _ := Participation(nil, []ViewerSample{{"v", time.Now(), 50}}, "v", time.Now())
	if ms := TopMoments(slots, nil); len(ms) != 0 {
		t.Errorf("vivo sin mensajes: %+v", ms)
	}

	// Sin tema, los ejemplos son los primeros mensajes de la franja.
	slots = momentSlots(sep, [2]int64{6, 100})
	if ms := TopMoments(slots, nil); len(ms) != 1 || len(ms[0].Examples) != 2 ||
		ms[0].Examples[0].ID != "0-0" || ms[0].Examples[1].ID != "0-1" {
		t.Errorf("sin tema, los ejemplos son los primeros: %+v", ms)
	}

	// Programa más frecuente, temas y ejemplos sin spam ni insultos que tengan el tema principal.
	slots = momentSlots(sep, [2]int64{1, 100}, [2]int64{0, 100})
	texts := []struct{ program, text string }{
		{"LA TARDE", "qué lindo día hoy"},
		{"LA TARDE", "entrá a www.ejemplo.com por el dólar blue"},
		{"", "sos un pelotudo con el dólar blue"},
		{"LA TARDE", "el dólar blue sube otra vez"},
		{"", "dólar blue récord"},
		{"", "y el dólar blue mañana"},
	}
	for j, x := range texts {
		slots[1].Msgs = append(slots[1].Msgs, Message{ID: fmt.Sprint("t", j), Source: "chat", VideoID: "v", Program: x.program,
			Author: fmt.Sprint("q", j), Text: x.text, At: slots[1].Start.Add(time.Duration(j) * time.Second)})
	}
	slots[1].N, slots[1].Authors = len(texts), len(texts)
	slots[1].Pct = float64(len(texts)) / 100 * 100
	ms := TopMoments(slots, map[string]string{"v": "🔴RADIO TEST en VIVO | LA MAÑANA"})
	if len(ms) != 1 {
		t.Fatalf("momento con textos: %+v", ms)
	}
	m := ms[0]
	if m.Program != "LA TARDE" || len(m.Topics) == 0 || m.Topics[0].Key != "dólar blue" || len(m.Topics) > 3 {
		t.Errorf("programa %q, temas %v", m.Program, m.Topics)
	}
	if len(m.Examples) != 2 || m.Examples[0].ID != "t3" || m.Examples[1].ID != "t4" {
		t.Errorf("ejemplos = %+v", m.Examples)
	}
}

// TestTopMomentsBorders: separación de 15 minutos entre inicios (hacia
// adelante y hacia atrás), tope de 5 y ejemplos ordenados por hora.
func TestTopMomentsBorders(t *testing.T) {
	step := 5 * time.Minute
	// La mejor es la 3; la 1, 2, 4 y 5 están a 5 o 10 minutos de ella y no
	// entran aunque tengan más % que la 6 y la 0, que están a 15 justos.
	slots := momentSlots(step, [2]int64{50, 1000}, [2]int64{95, 1000}, [2]int64{85, 1000}, [2]int64{100, 1000},
		[2]int64{90, 1000}, [2]int64{80, 1000}, [2]int64{70, 1000})
	if got := starts(slots, TopMoments(slots, nil)); got != "[3 6 0]" {
		t.Errorf("separación: franjas %s, quería [3 6 0]", got)
	}

	// 7 elegibles separadas: las 5 de más %, en orden.
	slots = momentSlots(15*time.Minute, [2]int64{3, 100}, [2]int64{9, 100}, [2]int64{4, 100}, [2]int64{8, 100},
		[2]int64{5, 100}, [2]int64{7, 100}, [2]int64{6, 100})
	ms := TopMoments(slots, nil)
	if got := starts(slots, ms); got != "[1 3 5 6 4]" {
		t.Errorf("tope de 5: franjas %s, quería [1 3 5 6 4]", got)
	}
	var ranks []int
	for _, m := range ms {
		ranks = append(ranks, m.Rank)
	}
	if fmt.Sprint(ranks) != "[1 2 3 4 5]" {
		t.Errorf("Rank = %v", ranks)
	}

	// 2 elegibles: devuelve 2.
	slots = momentSlots(15*time.Minute, [2]int64{3, 100}, [2]int64{1, 100}, [2]int64{4, 100}, [2]int64{6, 0})
	if got := starts(slots, TopMoments(slots, nil)); got != "[2 0]" {
		t.Errorf("2 elegibles: franjas %s", got)
	}

	// Elegibles pegadas, todas a menos de 15 minutos de la mejor: una sola.
	slots = momentSlots(step, [2]int64{5, 100}, [2]int64{6, 100}, [2]int64{9, 100}, [2]int64{7, 100}, [2]int64{8, 100})
	if got := starts(slots, TopMoments(slots, nil)); got != "[2]" {
		t.Errorf("pegadas: franjas %s, quería [2]", got)
	}
	// Con una sexta pegada, esa ya está a 15 minutos de la mejor y entra.
	slots = momentSlots(step, [2]int64{5, 100}, [2]int64{6, 100}, [2]int64{9, 100}, [2]int64{7, 100}, [2]int64{8, 100}, [2]int64{4, 100})
	if got := starts(slots, TopMoments(slots, nil)); got != "[2 5]" {
		t.Errorf("seis pegadas: franjas %s, quería [2 5]", got)
	}

	// Ejemplos: ordenados por hora aunque la franja venga desordenada, sin spam
	// ni insultos aunque no haya tema.
	slots = momentSlots(step, [2]int64{0, 100})
	for j, x := range []string{"sos un pelotudo", "hola", "buenas tardes", "www.spam.com"} {
		slots[0].Msgs = append(slots[0].Msgs, Message{ID: fmt.Sprint("e", j), Source: "chat", VideoID: "v",
			Author: fmt.Sprint("e", j), Text: x, At: slots[0].Start.Add(time.Duration(3-j) * time.Second)})
	}
	slots[0].N, slots[0].Authors, slots[0].Pct = 4, 4, 4
	ms = TopMoments(slots, nil)
	if len(ms) != 1 {
		t.Fatalf("momento con ejemplos: %+v", ms)
	}
	var ids []string
	for _, e := range ms[0].Examples {
		ids = append(ids, e.ID)
		if c := Classify(e.Text); c == CatSpam || c == CatInsulto {
			t.Errorf("ejemplo de %s: %q", c, e.Text)
		}
	}
	if fmt.Sprint(ids) != "[e2 e1]" {
		t.Errorf("ejemplos = %v, quería [e2 e1] (los primeros por hora sin spam ni insultos)", ids)
	}
}

// TestParticipationChart: el gráfico de % (o de personas si no hay ningún
// dato de espectadores), con alturas, clases, títulos, marcas, ticks, línea
// de la típica, rótulo y aria-label.
func TestParticipationChart(t *testing.T) {
	start := time.Date(2026, 10, 29, 15, 5, 0, 0, time.UTC) // ventana de un now fijo a las 15:02
	hm := func(i int) string { return start.Add(time.Duration(i) * slotStep).Local().Format("15:04") }
	// 288 franjas vacías; set pone {mensajes y personas, mirando} en la franja i.
	empty := func() []Slot {
		out := make([]Slot, slotCount)
		for i := range out {
			out[i].Start = start.Add(time.Duration(i) * slotStep)
		}
		return out
	}
	set := func(slots []Slot, i, people int, viewers int64) {
		s := &slots[i]
		s.N, s.Authors, s.Viewers = people, people, viewers
		if viewers > 0 {
			s.Pct = float64(people) / float64(viewers) * 100
		}
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

	t.Run("modo %", func(t *testing.T) {
		slots := empty()
		set(slots, 10, 2, 10)   // 20 %, pero 2 personas: sin barra
		set(slots, 20, 6, 0)    // sin dato: sin barra
		set(slots, 100, 5, 100) // 5 %: la más alta
		set(slots, 200, 3, 150) // 2 %
		set(slots, 250, 0, 80)  // con dato y nadie escribió
		moments := TopMoments(slots, nil)
		c := participationChart(slots, moments, 1.0, true, nil)
		if c["W"] != slotCount {
			t.Fatalf("W = %v, quería %d", c["W"], slotCount)
		}
		bars := c["Bars"].([]Bar)
		if len(bars) != slotCount {
			t.Fatalf("barras = %d", len(bars))
		}
		for i, b := range bars {
			if !near(b.X, float64(i)+0.1) || !near(b.W, 0.8) || !near(b.Y, 100-b.H) {
				t.Errorf("barra %d: X %v, W %v, Y %v, H %v", i, b.X, b.W, b.Y, b.H)
			}
		}
		heights := map[int]float64{10: 0, 20: 0, 100: 100, 200: 40, 250: 0, 0: 0}
		for i, h := range heights {
			if !near(bars[i].H, h) {
				t.Errorf("alto de la franja %d = %v, quería %v", i, bars[i].H, h)
			}
		}
		for i, b := range bars {
			if top := i == 100 || i == 200; (b.Class == "top") != top {
				t.Errorf("franja %d: clase %q", i, b.Class)
			}
		}
		titles := map[int]string{
			10:  " · 2 mensajes · 2 personas · menos de 3 personas: no entra en el ranking",
			20:  " · 6 mensajes · 6 personas · sin dato de espectadores",
			100: " · 5 mensajes · 5 personas · 100 mirando (5,0 %) · N.º 1 de los 5 con más participación",
			200: " · 3 mensajes · 3 personas · 150 mirando (2,0 %) · N.º 2 de los 5 con más participación",
			250: " · 0 mensajes · 0 personas · menos de 3 personas: no entra en el ranking",
			0:   " · 0 mensajes · 0 personas · sin dato de espectadores",
		}
		for i, want := range titles {
			if want = hm(i) + " a " + hm(i+1) + want; bars[i].Label != want {
				t.Errorf("título de la franja %d = %q, quería %q", i, bars[i].Label, want)
			}
		}
		marks := c["Marks"].([]Mark)
		if len(marks) != 2 || marks[0].Rank != 1 || !near(marks[0].Left, 100.5/288*100) ||
			marks[1].Rank != 2 || !near(marks[1].Left, 200.5/288*100) {
			t.Errorf("marcas = %+v", marks)
		}
		ticks := c["Ticks"].([]Tick)
		if len(ticks) != 9 || !near(ticks[0].Left, 0) || !near(ticks[1].Left, 12.5) || !near(ticks[8].Left, 100) ||
			ticks[0].Label != hm(0) || ticks[1].Label != hm(36) || ticks[8].Label != hm(288) {
			t.Errorf("ticks = %+v", ticks)
		}
		if y, ok := c["TypicalY"].(float64); !ok || !near(y, 80) {
			t.Errorf("TypicalY = %v, quería 80 (típica 1 %% con escala 5 %%)", c["TypicalY"])
		}
		if c["Max"] != "máx. 5,0 %" {
			t.Errorf("rótulo = %v", c["Max"])
		}
		want := "Participación del chat cada 5 minutos en las últimas 24 horas (personas que escribieron sobre los que miraban); la más alta: 5,0 % a las " + hm(100) + " (5 personas de 100 mirando)"
		if c["Aria"] != want {
			t.Errorf("aria = %q, quería %q", c["Aria"], want)
		}
	})

	t.Run("típica mayor que la más alta y alto mínimo", func(t *testing.T) {
		slots := empty()
		set(slots, 5, 4, 100)     // 4 %
		set(slots, 50, 3, 100000) // 0,003 %: alto mínimo de 1
		c := participationChart(slots, TopMoments(slots, nil), 8, true, nil)
		bars := c["Bars"].([]Bar)
		if !near(bars[5].H, 50) || !near(bars[50].H, 1) {
			t.Errorf("altos = %v y %v, quería 50 y 1 (escala de la típica)", bars[5].H, bars[50].H)
		}
		if y := c["TypicalY"].(float64); !near(y, 0) || c["Max"] != "máx. 8,0 %" {
			t.Errorf("TypicalY %v, rótulo %v", y, c["Max"])
		}
	})

	t.Run("con dato pero sin elegibles", func(t *testing.T) {
		slots := empty()
		set(slots, 5, 2, 10)
		c := participationChart(slots, TopMoments(slots, nil), 20, true, nil)
		aria := c["Aria"].(string)
		if !strings.HasSuffix(aria, "; todavía sin franjas con dato de espectadores y al menos 3 personas") {
			t.Errorf("aria = %q", aria)
		}
		if len(c["Marks"].([]Mark)) != 0 || c["Bars"].([]Bar)[5].H != 0 {
			t.Errorf("sin elegibles no hay marcas ni barras: %+v", c["Marks"])
		}
	})

	t.Run("modo personas", func(t *testing.T) {
		slots := empty()
		set(slots, 7, 3, 0)
		set(slots, 30, 6, 0)
		set(slots, 40, 6, 0) // empate: la primera
		c := participationChart(slots, TopMoments(slots, nil), 0, false, nil)
		bars := c["Bars"].([]Bar)
		if !near(bars[7].H, 50) || !near(bars[30].H, 100) || bars[0].H != 0 {
			t.Errorf("altos = %v, %v, %v", bars[7].H, bars[30].H, bars[0].H)
		}
		for i, b := range bars {
			if b.Class != "" {
				t.Errorf("franja %d con clase %q en modo personas", i, b.Class)
			}
		}
		if _, has := c["TypicalY"]; has {
			t.Errorf("modo personas sin línea de típica")
		}
		if m, _ := c["Marks"].([]Mark); len(m) != 0 {
			t.Errorf("modo personas sin marcas: %+v", m)
		}
		if c["Max"] != "máx. 6 personas" {
			t.Errorf("rótulo = %v", c["Max"])
		}
		want := "Personas que escribieron en el chat cada 5 minutos en las últimas 24 horas; máximo de 6 personas a las " + hm(30)
		if c["Aria"] != want {
			t.Errorf("aria = %q, quería %q", c["Aria"], want)
		}
		c = participationChart(empty(), nil, 0, false, nil)
		if aria := c["Aria"].(string); !strings.HasSuffix(aria, "; todavía sin mensajes") {
			t.Errorf("aria sin mensajes = %q", aria)
		}
		if c = participationChart(nil, nil, 0, false, nil); !strings.HasSuffix(c["Aria"].(string), "; todavía sin mensajes") {
			t.Errorf("sin franjas: %q", c["Aria"])
		}
	})
}

// section6 recorta la tarjeta 6 del parcial de análisis.
func section6(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, "6. Momentos con más participación")
	if i < 0 {
		t.Fatalf("no está la tarjeta 6")
	}
	j := strings.Index(body[i:], "</section>")
	if j < 0 {
		t.Fatalf("la tarjeta 6 no cierra")
	}
	return body[i : i+j]
}

// TestLiveParticipationView: la tarjeta de los 5 momentos con más
// participación del vivo elegido, con sus avisos y los mensajes E0, E1 y E2.
func TestLiveParticipationView(t *testing.T) {
	st, err := OpenStore(t.TempDir() + "/datos.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	st.SetVideos("Radio Test", []Video{
		{ID: "v1", Title: "🔴RADIO TEST en VIVO | LA MAÑANA", Channel: "Radio Test", LiveStatus: "live", Concurrent: 150, StartedAt: now.Add(-2 * time.Hour)},
		{ID: "v2", Title: "Oculto en VIVO", Channel: "Oculto", LiveStatus: "live"},
		{ID: "v3", Title: "Nuevo en VIVO", Channel: "Nuevo", LiveStatus: "live", Concurrent: 80},
		{ID: "v4", Title: "Tranqui en VIVO", Channel: "Tranqui", LiveStatus: "live", Concurrent: 40},
		{ID: "v5", Title: "Lleno en VIVO", Channel: "Lleno", LiveStatus: "live", Concurrent: 100},
	})
	peak := now.Truncate(5 * time.Minute).Add(-30 * time.Minute)
	var msgs []Message
	for _, id := range []string{"v1", "v2", "v3"} {
		for k := 1; k <= 6; k++ { // una persona por franja antes del pico
			msgs = append(msgs, Message{ID: fmt.Sprint(id, "-b", k), Source: "chat", VideoID: id, Author: "Base", Text: "buenas",
				Program: "LA MAÑANA", At: peak.Add(-time.Duration(k)*5*time.Minute + time.Second)})
		}
		for j, a := range []string{"<script>x</script>", "Ana", "Beto", "Caro", "Dani", "Eva"} {
			msgs = append(msgs, Message{ID: fmt.Sprint(id, "-p", j), Source: "chat", VideoID: id, Author: a, Text: "el dólar blue sube",
				Program: "LA MAÑANA", At: peak.Add(time.Duration(j+1) * time.Second)})
		}
	}
	// v4: con dato de espectadores pero solo 2 personas. v5: 5 franjas
	// separadas por 15 minutos con 3 personas o más y dato de espectadores.
	msgs = append(msgs,
		Message{ID: "v4-a", Source: "chat", VideoID: "v4", Author: "Ana", Text: "buenas", At: peak.Add(time.Second)},
		Message{ID: "v4-b", Source: "chat", VideoID: "v4", Author: "Beto", Text: "hola", At: peak.Add(2 * time.Second)})
	st.AddViewers(peak.Add(time.Minute), []Video{{ID: "v4", LiveStatus: "live", Concurrent: 40}})
	for k := range 5 {
		at := peak.Add(-time.Duration(k) * 15 * time.Minute)
		for j := range 3 + k {
			msgs = append(msgs, Message{ID: fmt.Sprint("v5-", k, "-", j), Source: "chat", VideoID: "v5", Author: fmt.Sprint("a", j),
				Text: "hola", At: at.Add(time.Duration(j+1) * time.Second)})
		}
		st.AddViewers(at.Add(time.Minute), []Video{{ID: "v5", LiveStatus: "live", Concurrent: 100}})
	}
	st.Add(msgs)
	st.AddViewers(peak.Add(time.Minute), []Video{{ID: "v1", LiveStatus: "live", Concurrent: 150}})
	h := (&Server{st: st, budget: 9000}).Routes()
	get := func(url string) string {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		return section6(t, w.Body.String())
	}
	const (
		e0 = "Sin registro de cuántos miraban en las últimas 24 h: todavía no se puede armar el ranking por %."
		e1 = "Ninguna franja con dato de espectadores tuvo al menos 3 personas escribiendo."
		e2 = " de 5: el resto de las franjas no llega a 3 personas, no tiene dato de espectadores o está a menos de 15 minutos de una elegida."
	)
	hidden := "YouTube no informa cuántos miran este vivo"
	// "veces lo " + "normal" partido para que el grep de CA10 no encuentre el propio test.
	gone := []string{"veces lo " + "normal", "pico: desde", `class="umbral"`, "Todavía no hubo picos", `class="scroll"`, "overflow-x", "<text", "<script>x</script>"}
	check := func(name, body string, want, not []string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: falta %q", name, w)
			}
		}
		for _, n := range append(not, gone...) {
			if strings.Contains(body, n) {
				t.Errorf("%s: no debería tener %q", name, n)
			}
		}
	}

	body := get("/parcial/vivo/analisis?video=v1")
	hm := peak.Local().Format("15:04")
	span := hm + " a " + peak.Add(5*time.Minute).Local().Format("15:04")
	check("v1", body, []string{
		`preserveAspectRatio="none"`, `role="img"`,
		"la más alta: 4,0 % a las " + hm + " (6 personas de 150 mirando)",
		"<title>" + span + " · LA MAÑANA · 6 mensajes · 6 personas · 150 mirando (4,0 %) · N.º 1 de los 5 con más participación</title>",
		"sin dato de espectadores", `class="bar top"`, `class="pmarks"`, `>1</span>`, `class="pticks"`,
		"Participación típica del vivo: 4,0 %", "150 mirando ahora", "máx. 4,0 %",
		"<th>Momento</th>", "<td class=\"n\">150</td>", "<td class=\"n\">6</td>", "4,0 %",
		peak.Local().Format("02/01 15:04") + " a " + peak.Add(5*time.Minute).Local().Format("15:04"), "LA MAÑANA",
		`<tr class="det"><td colspan="4">`, "Para el reel:", `target="_blank" rel="noopener"`,
		// v1 está en curso: YouTube ignora t=, así que se linkea el vivo y se dice cuánto retroceder.
		`href="https://www.youtube.com/watch?v=v1"`, "Abrí el vivo y retrocedé ~",
		"&lt;script&gt;x&lt;/script&gt;", "dólar blue", "Solo 1" + e2,
	}, []string{hidden, e0, e1, "watch?v=v1&amp;t="})
	if n := strings.Count(body, "<th"); n != 4 {
		t.Errorf("v1: la tabla tiene %d columnas, quería 4", n)
	}
	// El chat de v1 empezó hace menos de 3 h: el gráfico muestra el mínimo de 3 h.
	if n := strings.Count(body, "<g><title>"); n != minChartSlots {
		t.Errorf("v1: %d franjas con <title>, quería %d", n, minChartSlots)
	}

	// El canal oculta el contador: gráfico de personas, aviso de YouTube y E0.
	check("v2", get("/parcial/vivo/analisis?video=v2"), []string{
		hidden, e0, "máx. 6 personas", "Personas que escribieron en el chat cada 5 minutos desde las ", "; máximo de 6 personas a las " + hm,
	}, []string{"<table", `class="bar top"`, e1, e2})
	// Tiene espectadores pero no hay muestras de esas franjas.
	check("v3", get("/parcial/vivo/analisis?video=v3"), []string{
		"80 mirando ahora", "quedan fuera del ranking y de la participación típica", "sin dato de espectadores", e0,
	}, []string{hidden, "<table", e1, e2})
	// Con dato pero nadie llega a 3 personas.
	check("v4", get("/parcial/vivo/analisis?video=v4"), []string{
		e1, "40 mirando ahora", "todavía sin franjas con dato de espectadores y al menos 3 personas",
	}, []string{hidden, "<table", e0, e2})
	// Los 5: sin mensaje de que faltan.
	body = get("/parcial/vivo/analisis?video=v5")
	check("v5", body, []string{"7,0 %", "3,0 %"}, []string{hidden, e0, e1, e2})
	if n := strings.Count(body, `<tr class="det">`); n != 5 {
		t.Errorf("v5: %d momentos, quería 5", n)
	}
}

// TestTopMomentsEdges: bordes del ranking que no cubren TestTopMoments ni
// TestTopMomentsBorders.
func TestTopMomentsEdges(t *testing.T) {
	step := 5 * time.Minute
	sep := 15 * time.Minute
	z := [2]int64{0, 0}
	cases := []struct {
		name  string
		slots []Slot
		want  string
	}{
		// La de 6 personas es la más vieja: gana por personas y no por reciente.
		{"empate de %: más personas aunque sea más vieja", momentSlots(sep, [2]int64{6, 300}, [2]int64{4, 200}), "[0 1]"},
		{"empate de % y personas: la más reciente aunque esté antes en la lista",
			momentSlots(sep, [2]int64{4, 200}, [2]int64{4, 200}), "[1 0]"},
		// Empate total entre pegadas: gana la más reciente y bloquea a las otras.
		{"empate total pegadas", momentSlots(step, [2]int64{4, 200}, [2]int64{4, 200}, [2]int64{4, 200}), "[2]"},
		// La 8 (8 %) está a 40 min de la mejor pero a 10 de la segunda (6): queda
		// afuera aunque supera a la 3 (7 %), que está a 15 justos de las dos.
		{"bloqueada por la segunda elegida",
			momentSlots(step, [2]int64{10, 100}, z, z, [2]int64{7, 100}, z, z, [2]int64{9, 100}, z, [2]int64{8, 100}), "[0 6 3]"},
		// Más personas que espectadores informados (cache de YouTube): entra con más de 100 %.
		{"% mayor a 100", momentSlots(sep, [2]int64{3, 100}, [2]int64{10, 5}), "[1 0]"},
		{"E1: con dato y nadie llega a 3", momentSlots(sep, [2]int64{2, 10}, [2]int64{1, 5}, [2]int64{0, 50}), "[]"},
		{"E2: 4 elegibles", momentSlots(sep, [2]int64{3, 100}, [2]int64{4, 100}, [2]int64{5, 100}, [2]int64{6, 100}, [2]int64{9, 0}), "[3 2 1 0]"},
		{"Viewers 0 con muchas personas", momentSlots(sep, [2]int64{500, 0}, [2]int64{3, 1000}), "[1]"},
	}
	for _, c := range cases {
		ms := TopMoments(c.slots, nil)
		if got := starts(c.slots, ms); got != c.want {
			t.Errorf("%s: franjas %s, quería %s", c.name, got, c.want)
		}
		for i, m := range ms {
			if m.Rank != i+1 {
				t.Errorf("%s: Rank del %d.º = %d", c.name, i+1, m.Rank)
			}
			for _, o := range ms[:i] {
				if d := m.Start.Sub(o.Start); d > -sep && d < sep {
					t.Errorf("%s: %v y %v a menos de 15 minutos", c.name, m.Start, o.Start)
				}
			}
		}
	}
	if ms := TopMoments(momentSlots(sep, [2]int64{3, 100}, [2]int64{10, 5}), nil); len(ms) != 2 || ms[0].Pct != 200 || ms[0].Viewers != 5 {
		t.Errorf("%% mayor a 100: %+v", ms)
	}

	// Ventana de 24 h sin nada que entre, pasando por Participation: mensajes de
	// hace 25 h (fuera) y muestras de espectadores sin chat en la ventana.
	now := time.Date(2026, 10, 30, 15, 2, 0, 0, time.UTC)
	var old []Message
	for j := range 5 {
		old = append(old, Message{ID: fmt.Sprint("o", j), Source: "chat", VideoID: "v", Author: fmt.Sprint("a", j), Text: "hola", At: now.Add(-25 * time.Hour)})
	}
	slots, _, ok := Participation(old, []ViewerSample{{"v", now.Add(-25 * time.Hour), 10}, {"v", now.Add(-time.Hour), 10}}, "v", now)
	if ms := TopMoments(slots, nil); !ok || len(ms) != 0 {
		t.Errorf("ventana vacía: ok %v, momentos %+v", ok, ms)
	}

	// TopMoments no reordena los mensajes de las franjas que recibe (el
	// servidor le pasa franjas que después usa el gráfico).
	slots = momentSlots(sep, [2]int64{4, 100})
	slots[0].Msgs[0], slots[0].Msgs[3] = slots[0].Msgs[3], slots[0].Msgs[0]
	before := fmt.Sprint(slots[0].Msgs)
	if ms := TopMoments(slots, nil); len(ms) != 1 || ms[0].Examples[0].ID != "0-0" {
		t.Errorf("ejemplos por hora: %+v", ms)
	}
	if fmt.Sprint(slots[0].Msgs) != before {
		t.Errorf("TopMoments modificó los mensajes de la franja")
	}
}

// TestParticipationChartEdges: % mayor a 100, primera y última franja del
// top, valores dentro de 0-100, aria coherente con el N.º 1 en empates y modo
// personas aunque lleguen momentos.
func TestParticipationChartEdges(t *testing.T) {
	start := time.Date(2026, 10, 29, 15, 5, 0, 0, time.UTC)
	hm := func(i int) string { return start.Add(time.Duration(i) * slotStep).Local().Format("15:04") }
	empty := func() []Slot {
		out := make([]Slot, slotCount)
		for i := range out {
			out[i].Start = start.Add(time.Duration(i) * slotStep)
		}
		return out
	}
	set := func(slots []Slot, i, people int, viewers int64) {
		s := &slots[i]
		s.N, s.Authors, s.Viewers = people, people, viewers
		if viewers > 0 {
			s.Pct = float64(people) / float64(viewers) * 100
		}
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	inRange := func(t *testing.T, c map[string]any) {
		t.Helper()
		for i, b := range c["Bars"].([]Bar) {
			for _, v := range []float64{b.X, b.Y, b.W, b.H} {
				if math.IsNaN(v) || math.IsInf(v, 0) {
					t.Errorf("barra %d con %v", i, v)
				}
			}
			if b.H < 0 || b.H > 100 || b.Y < 0 || b.Y > 100 || b.X+b.W > float64(slotCount) {
				t.Errorf("barra %d fuera del viewBox: %+v", i, b)
			}
		}
		for _, m := range c["Marks"].([]Mark) {
			if m.Left <= 0 || m.Left >= 100 {
				t.Errorf("marca fuera de 0-100: %+v", m)
			}
		}
		for _, k := range c["Ticks"].([]Tick) {
			if k.Left < 0 || k.Left > 100 {
				t.Errorf("tick fuera de 0-100: %+v", k)
			}
		}
		if y, ok := c["TypicalY"].(float64); ok && (math.IsNaN(y) || y < 0 || y > 100) {
			t.Errorf("TypicalY = %v", y)
		}
	}

	t.Run("primera y última franja, % mayor a 100 y miles", func(t *testing.T) {
		slots := empty()
		set(slots, 0, 10, 5)      // 200 %
		set(slots, 287, 3, 1573)  // 0,19 %
		set(slots, 144, 40, 1000) // 4 %
		ms := TopMoments(slots, nil)
		c := participationChart(slots, ms, 1, true, nil)
		inRange(t, c)
		bars := c["Bars"].([]Bar)
		if !near(bars[0].H, 100) || !near(bars[144].H, 2) || !near(bars[287].H, 1) {
			t.Errorf("altos = %v, %v, %v", bars[0].H, bars[144].H, bars[287].H)
		}
		marks := c["Marks"].([]Mark)
		if len(marks) != 3 || !near(marks[0].Left, 0.5/288*100) || marks[0].Rank != 1 ||
			!near(marks[2].Left, 287.5/288*100) || marks[2].Rank != 3 {
			t.Errorf("marcas = %+v", marks)
		}
		if c["Max"] != "máx. 200,0 %" {
			t.Errorf("rótulo = %v", c["Max"])
		}
		if want := hm(287) + " a " + hm(288) + " · 3 mensajes · 3 personas · 1.573 mirando (0,2 %) · N.º 3 de los 5 con más participación"; bars[287].Label != want {
			t.Errorf("título = %q, quería %q", bars[287].Label, want)
		}
		if want := "la más alta: 200,0 % a las " + hm(0) + " (10 personas de 5 mirando)"; !strings.HasSuffix(c["Aria"].(string), want) {
			t.Errorf("aria = %q", c["Aria"])
		}
	})

	t.Run("empate: el aria nombra al N.º 1", func(t *testing.T) {
		slots := empty()
		set(slots, 50, 6, 300) // 2 %, 6 personas: N.º 1
		set(slots, 90, 4, 200) // 2 %, 4 personas, más reciente
		set(slots, 120, 6, 300)
		ms := TopMoments(slots, nil)
		c := participationChart(slots, ms, 1, true, nil)
		if len(ms) != 3 || !ms[0].Start.Equal(slots[120].Start) {
			t.Fatalf("N.º 1 = %+v", ms)
		}
		if want := "la más alta: 2,0 % a las " + hm(120) + " (6 personas de 300 mirando)"; !strings.HasSuffix(c["Aria"].(string), want) {
			t.Errorf("aria = %q, quería sufijo %q", c["Aria"], want)
		}
	})

	t.Run("con dato, sin elegibles y típica 0", func(t *testing.T) {
		slots := empty()
		set(slots, 3, 0, 100)
		c := participationChart(slots, nil, 0, true, nil)
		inRange(t, c)
		if c["TypicalY"] != 100.0 {
			t.Errorf("TypicalY = %v, quería 100 (línea en la base)", c["TypicalY"])
		}
	})

	t.Run("modo personas ignora los momentos", func(t *testing.T) {
		slots := empty()
		set(slots, 0, 4, 100)
		set(slots, 287, 2, 0)
		ms := TopMoments(slots, nil)
		c := participationChart(slots, ms, 0, false, nil)
		inRange(t, c)
		for i, b := range c["Bars"].([]Bar) {
			if b.Class != "" || strings.Contains(b.Label, "N.º") {
				t.Errorf("franja %d en modo personas: %+v", i, b)
			}
		}
		if len(c["Marks"].([]Mark)) != 0 {
			t.Errorf("marcas en modo personas: %+v", c["Marks"])
		}
		if !near(c["Bars"].([]Bar)[287].H, 50) {
			t.Errorf("alto en modo personas = %v", c["Bars"].([]Bar)[287].H)
		}
	})

	t.Run("programa del título del vivo", func(t *testing.T) {
		slots := empty()
		set(slots, 9, 3, 100)
		for j := range 3 {
			slots[9].Msgs = append(slots[9].Msgs, Message{ID: fmt.Sprint(j), Source: "chat", VideoID: "v", Author: fmt.Sprint(j), At: slots[9].Start})
		}
		c := participationChart(slots, nil, 0, true, map[string]string{"v": "Radio en VIVO | <script>x</script>"})
		if l := c["Bars"].([]Bar)[9].Label; !strings.Contains(l, " · <script>x</script> · 3 mensajes") {
			t.Errorf("título = %q (el escapado lo hace el template)", l)
		}
	})
}

// TestLiveParticipationViewEdges: escapado de programas y textos, posiciones
// de marcas y horas que pasan el escapador de CSS, filas de detalle con
// colspan, sin scroll en la tarjeta ni textos de "pico", E2 con 4 y % mayor a 100.
func TestLiveParticipationViewEdges(t *testing.T) {
	st, err := OpenStore(t.TempDir() + "/datos.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	st.SetVideos("Radio Test", []Video{
		{ID: "x1", Title: "Radio Test en VIVO | <script>alert(1)</script>", Channel: "Radio Test", LiveStatus: "live", Concurrent: 5, StartedAt: now.Add(-48 * time.Hour)},
		{ID: "x2", Title: "Cuatro en VIVO", Channel: "Cuatro", LiveStatus: "live", Concurrent: 100},
		{ID: "x3", Title: "Vacío en VIVO", Channel: "Vacío", LiveStatus: "live", Concurrent: 100},
	})
	slot := now.Truncate(5 * time.Minute).Add(-30 * time.Minute)
	var msgs []Message
	// x1: 10 personas con 5 mirando (200 %), programa tomado del título del vivo
	// y un ejemplo con HTML.
	for j := range 10 {
		msgs = append(msgs, Message{ID: fmt.Sprint("x1-", j), Source: "chat", VideoID: "x1", Author: fmt.Sprint("p", j),
			Text: `<img src=x onerror=alert(2)> hola`, At: slot.Add(time.Duration(j+1) * time.Second)})
	}
	st.AddViewers(slot.Add(time.Minute), []Video{{ID: "x1", LiveStatus: "live", Concurrent: 5}})
	// x2: 4 franjas elegibles separadas por 15 minutos.
	for k := range 4 {
		at := slot.Add(-time.Duration(k) * 15 * time.Minute)
		for j := range 3 + k {
			msgs = append(msgs, Message{ID: fmt.Sprint("x2-", k, "-", j), Source: "chat", VideoID: "x2", Author: fmt.Sprint("a", j),
				Program: "<b>PROG</b>", Text: "hola", At: at.Add(time.Duration(j+1) * time.Second)})
		}
		st.AddViewers(at.Add(time.Minute), []Video{{ID: "x2", LiveStatus: "live", Concurrent: 100}})
	}
	// x3: muestras de espectadores y ningún mensaje en las últimas 24 h (uno de hace 25 h).
	msgs = append(msgs, Message{ID: "x3-old", Source: "chat", VideoID: "x3", Author: "Viejo", Text: "hola", At: now.Add(-25 * time.Hour)})
	st.AddViewers(slot.Add(time.Minute), []Video{{ID: "x3", LiveStatus: "live", Concurrent: 100}})
	st.Add(msgs)

	h := (&Server{st: st, budget: 9000}).Routes()
	get := func(url string) string {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		return w.Body.String()
	}
	common := func(name, body string) {
		t.Helper()
		if strings.Contains(strings.ToLower(body), "pico") {
			t.Errorf("%s: queda un texto de pico", name)
		}
		for _, bad := range []string{`class="scroll"`, "overflow-x", "ZgotmplZ", "<script>", "<img", "<b>"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: no debería tener %q", name, bad)
			}
		}
		// Cada fila principal tiene 4 celdas y su fila de detalle ocupa las 4.
		mains := strings.Split(body, `<tr class="main">`)[1:]
		if n := strings.Count(body, `<tr class="det"><td colspan="4">`); n != len(mains) {
			t.Errorf("%s: %d filas de detalle con colspan=4 para %d momentos", name, n, len(mains))
		}
		for i, row := range mains {
			row = row[:strings.Index(row, "</tr>")]
			if n := strings.Count(row, "<td"); n != 4 {
				t.Errorf("%s: la fila %d tiene %d celdas", name, i+1, n)
			}
		}
		if n := strings.Count(body, "colspan="); n != len(mains) {
			t.Errorf("%s: %d colspan para %d momentos", name, n, len(mains))
		}
	}

	body := section6(t, get("/parcial/vivo/analisis?video=x1"))
	common("x1", body)
	for _, want := range []string{
		"&lt;script&gt;alert(1)&lt;/script&gt;</div>",           // programa en la tabla
		"· &lt;script&gt;alert(1)&lt;/script&gt; · 10 mensajes", // programa en el <title>
		"&lt;img src=x onerror=alert(2)&gt; hola",               // ejemplo
		"200,0 %", "máx. 200,0 %", "<td class=\"n\">5</td>", "<td class=\"n\">10</td>",
		`style="left: 0.00%"`, `style="left: 100.00%"`, "Solo 1 de 5",
		// Vivo de 48 h en curso y momento de hace 30 min: link al vivo, sin t=, y cuánto retroceder.
		`href="https://www.youtube.com/watch?v=x1"`, "Abrí el vivo y retrocedé ~0 h 3",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("x1: falta %q", want)
		}
	}
	if strings.Contains(body, "watch?v=x1&amp;t=") {
		t.Errorf("x1: un vivo en curso no lleva t= (YouTube lo ignora)")
	}

	body = section6(t, get("/parcial/vivo/analisis?video=x2"))
	common("x2", body)
	if !strings.Contains(body, "Solo 4 de 5: el resto de las franjas") || strings.Count(body, `<tr class="main">`) != 4 {
		t.Errorf("x2: E2 con 4 momentos")
	}
	if !strings.Contains(body, "&lt;b&gt;PROG&lt;/b&gt;") {
		t.Errorf("x2: programa sin escapar o ausente")
	}
	// Las 4 marcas, en orden de %, cada una dentro de 0-100.
	marks := body[strings.Index(body, `class="pmarks"`):]
	marks = marks[:strings.Index(marks, "</div>")]
	if n := strings.Count(marks, "<span"); n != 4 {
		t.Errorf("x2: %d marcas, quería 4", n)
	}
	for _, part := range strings.Split(marks, `left: `)[1:] {
		var left float64
		if _, err := fmt.Sscanf(part, "%f%%", &left); err != nil || left <= 0 || left >= 100 {
			t.Errorf("x2: marca en %q (%v)", part[:min(len(part), 12)], err)
		}
	}

	body = section6(t, get("/parcial/vivo/analisis?video=x3"))
	common("x3", body)
	for _, want := range []string{"Ninguna franja con dato de espectadores tuvo al menos 3 personas escribiendo.",
		"todavía sin franjas con dato de espectadores y al menos 3 personas"} {
		if !strings.Contains(body, want) {
			t.Errorf("x3: falta %q", want)
		}
	}
	if strings.Contains(body, "<table") || strings.Contains(body, `class="bar top"`) {
		t.Errorf("x3: ventana vacía con tabla o top")
	}

	// CSS de la tarjeta (embebido en la página): sin scroll horizontal.
	page := get("/")
	for _, sel := range []string{".pchart", ".pmax", ".pmarks", ".pticks", ".tops"} {
		for _, line := range strings.Split(page, "\n") {
			if strings.Contains(line, sel) && (strings.Contains(line, "overflow-x") || strings.Contains(line, "overflow: auto") || strings.Contains(line, "overflow: scroll")) {
				t.Errorf("CSS de %s con scroll: %s", sel, line)
			}
		}
	}
	for _, want := range []string{"minmax(min(300px, 100%), 1fr)", "height: 240px", "height: 180px", ".bar.top { fill: var(--warn-text); }",
		"vector-effect: non-scaling-stroke", "overflow-wrap: anywhere", ".pticks span:nth-child(even) { display: none; }"} {
		if !strings.Contains(page, want) {
			t.Errorf("CSS: falta %q", want)
		}
	}
	for _, gone := range []string{".bar.pico", ".umbral"} {
		if strings.Contains(page, gone) {
			t.Errorf("CSS: queda %q", gone)
		}
	}
}

// TestChartWindow: el gráfico muestra las últimas 6 o 24 h, sin el tramo vacío
// del principio y con al menos 3 h.
func TestChartWindow(t *testing.T) {
	cases := []struct {
		name       string
		hours      int
		data       []int // franjas con chat (N) o con dato de espectadores (V)
		viewers    bool
		wantFirst  int
		wantLength int
	}{
		{"24 h sin datos: mínimo de 3 h", 24, nil, false, 252, 36},
		{"24 h con datos recientes: mínimo de 3 h", 24, []int{270, 280}, false, 252, 36},
		{"24 h desde la primera con chat", 24, []int{100, 200}, false, 100, 188},
		{"24 h con datos desde el principio", 24, []int{0}, false, 0, 288},
		{"un dato de espectadores también cuenta", 24, []int{120}, true, 120, 168},
		{"6 h con datos viejos: las 6 h enteras", 6, []int{0, 250}, false, 216, 72},
		{"6 h con datos desde la mitad", 6, []int{250}, false, 250, 38},
		{"6 h con datos recientes: mínimo de 3 h", 6, []int{280}, false, 252, 36},
	}
	start := time.Date(2026, 10, 29, 15, 5, 0, 0, time.UTC)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			slots := make([]Slot, slotCount)
			for i := range slots {
				slots[i].Start = start.Add(time.Duration(i) * slotStep)
			}
			for _, i := range c.data {
				if c.viewers {
					slots[i].Viewers = 100
				} else {
					slots[i].N = 1
				}
			}
			got := chartWindow(slots, c.hours)
			if len(got) != c.wantLength || !got[0].Start.Equal(slots[c.wantFirst].Start) || !got[len(got)-1].Start.Equal(slots[287].Start) {
				t.Errorf("ventana de %d franjas desde %v, quería %d desde la %d", len(got), got[0].Start, c.wantLength, c.wantFirst)
			}
		})
	}
	if got := chartWindow(make([]Slot, 10), 6); len(got) != 10 {
		t.Errorf("con menos franjas que el mínimo devuelve todas: %d", len(got))
	}
}

// TestParticipationChartWindow: rótulos de hora y marcas de un gráfico de
// menos de 24 h.
func TestParticipationChartWindow(t *testing.T) {
	start := time.Date(2026, 10, 29, 15, 5, 0, 0, time.UTC)
	hm := func(i int) string { return start.Add(time.Duration(i) * slotStep).Local().Format("15:04") }
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	for _, c := range []struct {
		n, step int
	}{{36, 6}, {38, 6}, {48, 6}, {72, 12}, {96, 12}, {188, 24}, {288, 36}} {
		slots := make([]Slot, c.n)
		for i := range slots {
			slots[i].Start = start.Add(time.Duration(i) * slotStep)
		}
		slots[c.n-1].N, slots[c.n-1].Authors, slots[c.n-1].Viewers, slots[c.n-1].Pct = 3, 3, 100, 3
		ch := participationChart(slots, TopMoments(slots, nil), 1, true, nil)
		ticks := ch["Ticks"].([]Tick)
		if len(ticks) != c.n/c.step+1 || len(ticks) > 9 || ticks[1].Label != hm(c.step) || !near(ticks[1].Left, float64(c.step)/float64(c.n)*100) {
			t.Errorf("%d franjas: ticks = %+v, quería uno cada %d", c.n, ticks, c.step)
		}
		// Solo el rótulo que cae en el borde derecho se alinea a la derecha.
		for _, k := range ticks {
			if k.End != near(k.Left, 100) {
				t.Errorf("%d franjas: tick %+v con End mal puesto", c.n, k)
			}
		}
		if last := ticks[len(ticks)-1]; last.End != (c.n%c.step == 0) {
			t.Errorf("%d franjas: último tick %+v", c.n, last)
		}
		if ch["W"] != c.n || len(ch["Bars"].([]Bar)) != c.n {
			t.Errorf("%d franjas: W = %v", c.n, ch["W"])
		}
		marks := ch["Marks"].([]Mark)
		if len(marks) != 1 || !near(marks[0].Left, (float64(c.n)-0.5)/float64(c.n)*100) {
			t.Errorf("%d franjas: marcas = %+v", c.n, marks)
		}
		aria := ch["Aria"].(string)
		if want := "cada 5 minutos desde las " + hm(0) + " ("; c.n < slotCount && !strings.Contains(aria, want) {
			t.Errorf("%d franjas: aria = %q, quería %q", c.n, aria, want)
		}
		if c.n == slotCount && !strings.Contains(aria, "en las últimas 24 horas") {
			t.Errorf("24 h: aria = %q", aria)
		}
	}
}

// TestParticipationWindowView: botones de 6 y 24 h, el aviso de los momentos
// que quedan fuera del gráfico y la ventana elegida en el refresco.
func TestParticipationWindowView(t *testing.T) {
	st, err := OpenStore(t.TempDir() + "/datos.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	st.SetVideos("Radio Test", []Video{{ID: "w1", Title: "RADIO en VIVO | TARDE", Channel: "Radio", LiveStatus: "live", Concurrent: 300}})
	old := now.Truncate(slotStep).Add(-10 * time.Hour)
	recent := now.Truncate(slotStep).Add(-30 * time.Minute)
	var msgs []Message
	for j := range 3 {
		msgs = append(msgs,
			Message{ID: fmt.Sprint("o", j), Source: "chat", VideoID: "w1", Author: fmt.Sprint("a", j), Text: "hola", At: old.Add(time.Second)},
			Message{ID: fmt.Sprint("r", j), Source: "chat", VideoID: "w1", Author: fmt.Sprint("a", j), Text: "hola", At: recent.Add(time.Second)})
	}
	st.Add(msgs)
	st.AddViewers(old.Add(time.Minute), []Video{{ID: "w1", LiveStatus: "live", Concurrent: 100}})    // 3 %: N.º 1
	st.AddViewers(recent.Add(time.Minute), []Video{{ID: "w1", LiveStatus: "live", Concurrent: 300}}) // 1 %: N.º 2
	h := (&Server{st: st, budget: 9000}).Routes()
	get := func(url string) string {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		return w.Body.String()
	}
	pressed := func(hours int, on bool) string {
		return fmt.Sprintf(`id="pwin-%d" class="vbtn" aria-pressed="%v"`, hours, on)
	}
	note := "fuera de este gráfico: N.º 1 del top. Tocá «Últimas 24 h» para verlo."
	// La hora del aviso es el inicio de la ventana: puede correrse una franja si
	// entre el test y el handler se pasó a la siguiente.
	cut := now.Truncate(slotStep).Add(slotStep - 6*time.Hour)
	before := func(body string) bool {
		for _, at := range []time.Time{cut, cut.Add(slotStep)} {
			if strings.Contains(body, "Antes de las "+at.Local().Format("15:04")+", "+note) {
				return true
			}
		}
		return false
	}

	for _, q := range []string{"", "&horas=6", "&horas=12", "&horas=x"} {
		body := get("/parcial/vivo/analisis?video=w1" + q)
		for _, want := range []string{`<div id="vivo-analisis" hx-get="/parcial/vivo/analisis?video=w1&horas=6" hx-trigger="every 30s" hx-swap="outerHTML">`,
			pressed(6, true), pressed(24, false), `hx-get="/parcial/vivo/analisis?video=w1&horas=24" hx-target="#vivo-analisis" hx-swap="outerHTML"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%q: falta %q", q, want)
			}
		}
		if !before(body) {
			t.Errorf("%q: falta el aviso %q", q, note)
		}
		if n := strings.Count(section6(t, body), "<g><title>"); n != 72 {
			t.Errorf("%q: %d franjas, quería 72 (6 h)", q, n)
		}
	}

	body := get("/parcial/vivo/analisis?video=w1&horas=24")
	for _, want := range []string{`hx-get="/parcial/vivo/analisis?video=w1&horas=24" hx-trigger="every 30s"`, pressed(24, true), pressed(6, false)} {
		if !strings.Contains(body, want) {
			t.Errorf("24 h: falta %q", want)
		}
	}
	if strings.Contains(body, note) {
		t.Errorf("24 h: no debería avisar momentos fuera del gráfico")
	}
	// Desde la franja del chat viejo (10 h) hasta ahora: 121 franjas, o una más
	// si entre el test y el handler se pasó a la franja siguiente.
	if n := strings.Count(section6(t, body), "<g><title>"); n != 121 && n != 122 {
		t.Errorf("24 h: %d franjas, quería 121", n)
	}

	// La sección del vivo pide el análisis con un lugar que se reemplaza entero
	// (si fuera innerHTML quedarían dos #vivo-analisis anidados) y que reintenta
	// si falla la primera carga.
	if body := get("/parcial/vivo?video=w1"); !strings.Contains(body, `<div id="vivo-analisis" hx-get="/parcial/vivo/analisis?video=w1" hx-trigger="load, every 30s" hx-swap="outerHTML"></div>`) {
		t.Errorf("/parcial/vivo sin el lugar del análisis: %q", body)
	}

	// Un vivo que ya no está sigue con su wrapper para volver a pedirse.
	body = get("/parcial/vivo/analisis?video=nada&horas=24")
	if !strings.Contains(body, `hx-get="/parcial/vivo/analisis?video=nada&horas=24"`) || !strings.Contains(body, "Este vivo ya no está en la lista de videos.") {
		t.Errorf("vivo que ya no está: %q", body)
	}
}
