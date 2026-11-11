package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestVideosLikesAndShorts (CA3, CA6): Videos lee likeCount sin cambiar el
// costo y ChannelShorts pide la lista automática UUSH del canal (1 unidad).
func TestVideosLikesAndShorts(t *testing.T) {
	var lists []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch r.URL.Path {
		case "/playlistItems":
			lists = append(lists, q.Get("playlistId"))
			w.Write([]byte(`{"items":[{"contentDetails":{"videoId":"s1"}},{"contentDetails":{"videoId":"s2"}}]}`))
		case "/videos":
			w.Write([]byte(`{"items":[{"id":"v1","snippet":{"title":"Uno","publishedAt":"2026-11-01T10:00:00Z","liveBroadcastContent":"none"},"statistics":{"viewCount":"100","likeCount":"7","commentCount":"2"}}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	yt := NewYouTube("k")
	yt.BaseURL = srv.URL
	vids, err := yt.Videos([]string{"v1"})
	if err != nil || len(vids) != 1 || vids[0].Likes != 7 || yt.Units != 1 {
		t.Fatalf("Videos = %v %+v, %d unidades", err, vids, yt.Units)
	}
	ids, err := yt.ChannelShorts("UUabc")
	if err != nil || strings.Join(ids, ",") != "s1,s2" || yt.Units != 2 || len(lists) != 1 || lists[0] != "UUSHabc" {
		t.Errorf("ChannelShorts = %v %v, %d unidades, listas %v", err, ids, yt.Units, lists)
	}
	if r := videoRecord(Video{ID: "v1", Likes: 7, Short: true}, time.Now()); r.Likes != 7 || !r.Short {
		t.Errorf("videoRecord = %+v", r)
	}
}

// TestLikesStore (CA7): la serie de likes guarda un vivo en cada revisión si
// cambió y un video o short a lo sumo cada likeSaveEvery; al reabrir aplica
// la retención de 30 días y deja el archivo sin lo vencido.
func TestLikesStore(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/datos.jsonl"
	old := time.Now().Add(-31 * 24 * time.Hour).UTC().Format(time.RFC3339)
	os.WriteFile(dir+"/datos.likes.jsonl", []byte(`{"VideoID":"viejo","At":"`+old+`","Likes":3}`+"\nbasura\n"), 0o644)
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().Add(-2 * time.Hour)
	live := func(n int64) Video { return Video{ID: "vivo", LiveStatus: "live", Likes: n} }
	vid := func(n int64) Video { return Video{ID: "g", LiveStatus: "none", Likes: n} }
	steps := []struct {
		at   time.Duration
		vids []Video
	}{
		{0, []Video{live(10), vid(100), {ID: "oculto", LiveStatus: "none"}}}, // primera muestra de cada uno; sin likes, nada
		{3 * time.Minute, []Video{live(10), vid(105)}},                       // vivo igual: no; video antes de 30 min: no
		{6 * time.Minute, []Video{live(14), vid(100)}},                       // vivo cambió: sí
		{31 * time.Minute, []Video{live(14), vid(100)}},                      // video igual a lo guardado: no
		{40 * time.Minute, []Video{live(20), vid(120)}},                      // los dos
		{45 * time.Minute, []Video{vid(130)}},                                // video antes de 30 min desde la última: no
	}
	for _, s := range steps {
		st.AddLikes(t0.Add(s.at), s.vids)
	}
	got := func(s *Store, id string) []int64 {
		var out []int64
		for _, l := range s.Likes(id, t0.Add(-time.Hour)) {
			out = append(out, l.Likes)
		}
		return out
	}
	if g := got(st, "vivo"); len(g) != 3 || g[0] != 10 || g[1] != 14 || g[2] != 20 {
		t.Errorf("vivo = %v, quería [10 14 20]", g)
	}
	if g := got(st, "g"); len(g) != 2 || g[0] != 100 || g[1] != 120 {
		t.Errorf("video = %v, quería [100 120]", g)
	}
	if g := got(st, "oculto"); len(g) != 0 {
		t.Errorf("sin likes = %v", g)
	}
	if all := st.LikesSince(t0.Add(30 * time.Minute)); len(all["vivo"]) != 1 || len(all["g"]) != 1 {
		t.Errorf("LikesSince = %+v", all)
	}
	st2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if g := got(st2, "vivo"); len(g) != 3 {
		t.Errorf("al reabrir = %v", g)
	}
	// Después de reabrir recuerda la última muestra: un video igual no se repite.
	st2.AddLikes(t0.Add(3*time.Hour), []Video{vid(120)})
	if g := got(st2, "g"); len(g) != 2 {
		t.Errorf("repetido al reabrir = %v", g)
	}
	b, _ := os.ReadFile(dir + "/datos.likes.jsonl")
	if strings.Contains(string(b), "viejo") || strings.Contains(string(b), "basura") {
		t.Errorf("archivo sin compactar:\n%s", b)
	}
}

// TestStoreMissingVideos (CA5): los videos con comentarios guardados que no
// están en el archivo, ordenados y sin repetir; un short sigue siéndolo.
func TestStoreMissingVideos(t *testing.T) {
	st, err := OpenStore(t.TempDir() + "/datos.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	st.AddVideos(now, []Video{{ID: "a", LiveStatus: "none", Short: true}})
	st.Add([]Message{
		{ID: "1", Source: "comentario", VideoID: "zz", At: now},
		{ID: "2", Source: "comentario", VideoID: "a", At: now},
		{ID: "3", Source: "comentario", VideoID: "bb", At: now},
		{ID: "4", Source: "comentario", VideoID: "zz", At: now},
		{ID: "5", Source: "chat", VideoID: "vivo", At: now},
	})
	if got := st.MissingVideos(); strings.Join(got, ",") != "bb,zz" {
		t.Errorf("MissingVideos = %v", got)
	}
	// Una revisión que no sabe que es short no le saca la marca.
	st.AddVideos(now, []Video{{ID: "a", LiveStatus: "none"}})
	if r := st.VideoArchive(); len(r) != 1 || !r[0].Short {
		t.Errorf("archivo = %+v", r)
	}
}

// TestWorkerShortsAndMissing (CA3, CA5): el worker pide los shorts de cada
// canal y los videos que faltan cada shortsEvery, con spend; un faltante que
// YouTube no devuelve no se vuelve a pedir; los shorts quedan marcados.
func TestWorkerShortsAndMissing(t *testing.T) {
	defer func(k, e, f time.Duration) { workerTick, shortsEvery, shortsFirst = k, e, f }(workerTick, shortsEvery, shortsFirst)
	workerTick, shortsEvery, shortsFirst = 2*time.Millisecond, time.Hour, 0
	var (
		mu      sync.Mutex
		shorts  int
		missing []string
		reviews int
		units   int // lo que cobraría YouTube: 1 por pedido en estas rutas
	)
	now := time.Now().UTC().Format(time.RFC3339)
	video := func(id, title string) string {
		return `{"id":"` + id + `","snippet":{"title":"` + title + `","channelId":"UCA","channelTitle":"Radio A","publishedAt":"` + now +
			`","liveBroadcastContent":"none"},"statistics":{"viewCount":"10","likeCount":"4","commentCount":"0"}}`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		mu.Lock()
		defer mu.Unlock()
		units++
		switch r.URL.Path {
		case "/channels":
			w.Write([]byte(`{"items":[{"id":"UCA","snippet":{"title":"Radio A"},"contentDetails":{"relatedPlaylists":{"uploads":"UUA"}}}]}`))
		case "/playlistItems":
			switch q.Get("playlistId") {
			case "UUSHA":
				shorts++
				w.Write([]byte(`{"items":[{"contentDetails":{"videoId":"s1"}}]}`))
			case "UUA":
				reviews++
				w.Write([]byte(`{"items":[{"contentDetails":{"videoId":"s1"}}]}`))
			default:
				w.Write([]byte(`{"items":[]}`))
			}
		case "/videos":
			var out []string
			for _, id := range strings.Split(q.Get("id"), ",") {
				switch id {
				case "s1":
					out = append(out, video("s1", "Un short"))
				case "zz":
					missing = append(missing, id)
					out = append(out, video("zz", "Video viejo"))
				case "borrado":
					missing = append(missing, id)
				}
			}
			w.Write([]byte(`{"items":[` + strings.Join(out, ",") + `]}`))
		default:
			w.Write([]byte(`{"items":[]}`))
		}
	}))
	defer srv.Close()
	st, err := OpenStore(t.TempDir() + "/datos.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	st.Add([]Message{
		{ID: "1", Source: "comentario", VideoID: "zz", Text: "hola", At: at},
		{ID: "2", Source: "comentario", VideoID: "borrado", Text: "hola", At: at},
	})
	yt := NewYouTube("k")
	yt.BaseURL = srv.URL
	ctx, cancel := context.WithCancel(context.Background())
	done := runWorker(ctx, yt, st, WorkerConfig{Channels: []string{"UCA"}, Videos: 5, VideosEvery: 10 * time.Millisecond,
		CommentsEvery: time.Hour, ChatEvery: time.Hour, ChatPeak: time.Hour, Budget: 100000})
	get := func() (int, int, []string) {
		mu.Lock()
		defer mu.Unlock()
		return shorts, reviews, append([]string(nil), missing...)
	}
	waitFor(t, 5*time.Second, "cinco revisiones de videos", func() bool { _, r, _ := get(); return r >= 5 })
	cancel()
	<-done
	s, _, m := get()
	mu.Lock()
	want := units
	mu.Unlock()
	// Todo pasa por spend: lo que suma el Store es lo que se pidió.
	if got := st.Units(); got != want {
		t.Errorf("unidades en el Store = %d, pedidos = %d", got, want)
	}
	if s != 1 {
		t.Errorf("lista de shorts pedida %d veces, quería 1 (una por hora)", s)
	}
	if strings.Join(m, ",") != "borrado,zz" {
		t.Errorf("faltantes pedidos %v, quería una vez cada uno", m)
	}
	byID := map[string]VideoRecord{}
	for _, r := range st.VideoArchive() {
		byID[r.ID] = r
	}
	if r := byID["zz"]; r.Title != "Video viejo" || r.Likes != 4 {
		t.Errorf("faltante completado = %+v", r)
	}
	if r := byID["s1"]; !r.Short || r.Likes != 4 {
		t.Errorf("short = %+v", r)
	}
	if l := st.Likes("s1", at.Add(-time.Hour)); len(l) != 1 || l[0].Likes != 4 {
		t.Errorf("likes del short = %+v", l)
	}
}

// TestProgramLikes (CA10): los likes de un tramo son la diferencia del
// contador del vivo entre su primera y su última muestra; con una sola, no
// hay dato. Los likes c/1.000 espectadores usan el promedio del tramo.
func TestProgramLikes(t *testing.T) {
	t0 := dayStart(time.Now()).Add(8 * time.Hour)
	s := func(min int, n int64) LikeSample {
		return LikeSample{VideoID: "v", At: t0.Add(time.Duration(min) * time.Minute), Likes: n}
	}
	likes := []LikeSample{s(-10, 90), s(0, 100), s(30, 130), s(60, 160), s(90, 175), s(120, 200)}
	views := []ViewerSample{{VideoID: "v", At: t0, Viewers: 1000}, {VideoID: "v", At: t0.Add(time.Hour), Viewers: 3000}}
	cases := []struct {
		name     string
		sg       Segment
		likes    int64
		ok       bool
		per1000  string
		viewsAvg int64
	}{
		{"terminado", Segment{VideoID: "v", Start: t0, End: t0.Add(time.Hour), State: SegEnded}, 60, true, "30,0", 2000},
		{"al aire", Segment{VideoID: "v", Start: t0.Add(time.Hour), State: SegLive}, 40, true, "13,3", 3000},
		// Un vivo guarda una muestra solo cuando cambia: la base es la última
		// hasta el inicio (175, a las 9:30), así que alcanza una dentro del tramo.
		{"una sola muestra", Segment{VideoID: "v", Start: t0.Add(115 * time.Minute), End: t0.Add(125 * time.Minute), State: SegEnded}, 25, true, "—", 0},
		{"sin muestras en el tramo", Segment{VideoID: "v", Start: t0.Add(130 * time.Minute), End: t0.Add(140 * time.Minute), State: SegEnded}, 0, false, "", 0},
		{"otro vivo", Segment{VideoID: "otro", Start: t0, End: t0.Add(time.Hour), State: SegEnded}, 0, false, "", 0},
	}
	for _, c := range cases {
		n, ok := segmentLikes(likes, c.sg)
		if n != c.likes || ok != c.ok {
			t.Errorf("%s: %d %v, quería %d %v", c.name, n, ok, c.likes, c.ok)
		}
		if ok {
			avg := avgViewers(views, c.sg)
			if avg != c.viewsAvg || per1000(n, avg) != c.per1000 {
				t.Errorf("%s: promedio %d, c/1.000 %q", c.name, avg, per1000(n, avg))
			}
		}
	}

	// Las filas suman sus pedazos; sin muestras en el tramo, no hay dato.
	cut := []ProgramRow{
		{ProgramSeg: ProgramSeg{Parts: []Segment{cases[0].sg, cases[1].sg}}},
		{ProgramSeg: ProgramSeg{Seg: cases[3].sg}},
	}
	programLikes(cut, map[string][]LikeSample{"v": likes}, map[string][]ViewerSample{"v": views})
	if r := cut[0]; !r.HasLikes || r.Likes != 100 || r.LikesPer1000 != "40,0" {
		t.Errorf("fila con dos pedazos = %v %d %q", r.HasLikes, r.Likes, r.LikesPer1000)
	}
	if r := cut[1]; r.HasLikes || r.LikesPer1000 != "—" {
		t.Errorf("fila sin dato = %v %q", r.HasLikes, r.LikesPer1000)
	}

	// El gráfico del día: likes ganados cada 30 min, con la clase del programa.
	rows := []ProgramRow{
		{ProgramSeg: ProgramSeg{Seg: Segment{VideoID: "v", Program: "A", Start: t0, End: t0.Add(time.Hour), State: SegEnded}}},
		{ProgramSeg: ProgramSeg{Seg: Segment{VideoID: "v", Program: "B", Start: t0.Add(time.Hour), State: SegLive}}},
	}
	c := likesDayChart(rows, map[string][]LikeSample{"v": likes}, dayStart(t0), t0.Add(2*time.Hour))
	if len(c.Bars) != 48 {
		t.Fatalf("barras = %d", len(c.Bars))
	}
	// 08:00 (16): +10; 08:30: +30; 09:00: +30; 09:30: +15; 10:00 (20): +25.
	want := map[int]int{16: 10, 17: 30, 18: 30, 19: 15, 20: 25}
	for i, b := range c.Bars {
		if b.N != want[i] {
			t.Errorf("barra %d = %d, quería %d", i, b.N, want[i])
		}
	}
	if c.Bars[17].Class != "s1" || c.Bars[19].Class != "s2" {
		t.Errorf("clases = %q %q", c.Bars[17].Class, c.Bars[19].Class)
	}
	// El color no es el único dato: cada barra dice de qué programa es.
	if !strings.Contains(c.Bars[17].Label, "· A:") || !strings.Contains(c.Bars[19].Label, "· B:") {
		t.Errorf("rótulos = %q, %q", c.Bars[17].Label, c.Bars[19].Label)
	}
}

// TestReviewShortsOnlyNew (CA3): la lista de shorts de un canal se pide solo
// si tiene videos que todavía no se clasificaron; los vivos no cuentan.
func TestReviewShortsOnlyNew(t *testing.T) {
	var lists []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lists = append(lists, r.URL.Query().Get("playlistId"))
		w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()
	yt := NewYouTube("k")
	yt.BaseURL = srv.URL
	st, err := OpenStore(t.TempDir() + "/datos.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	spend := func(f func() error) error { return f() }
	infos := []ChannelInfo{{ID: "UCA", Uploads: "UUA"}, {ID: "UCB", Uploads: "UUB"}}
	shorts, classified, asked := map[string]bool{}, map[string]bool{}, map[string]bool{}
	st.SetVideos("Radio A", []Video{{ID: "a1", ChannelID: "UCA", LiveStatus: "none"}, {ID: "b1", ChannelID: "UCB", LiveStatus: "live"}})
	reviewShorts(yt, st, spend, infos, shorts, classified, asked)
	reviewShorts(yt, st, spend, infos, shorts, classified, asked)
	if strings.Join(lists, ",") != "UUSHA" {
		t.Fatalf("listas = %v, quería solo UUSHA una vez", lists)
	}
	st.SetVideos("Radio A", []Video{{ID: "a1", ChannelID: "UCA", LiveStatus: "none"}, {ID: "a2", ChannelID: "UCA", LiveStatus: "none"}})
	reviewShorts(yt, st, spend, infos, shorts, classified, asked)
	if strings.Join(lists, ",") != "UUSHA,UUSHA" {
		t.Errorf("con un video nuevo = %v", lists)
	}
}

// TestReviewShortsNoList (CA3): si YouTube dice que la lista de shorts no
// existe (un canal sin shorts), no se vuelve a pedir por los mismos videos;
// con otro error, sí.
func TestReviewShortsNoList(t *testing.T) {
	for _, c := range []struct {
		name, body string
		code, want int
	}{
		{"404", `{"error":{"code":404,"message":"playlistNotFound"}}`, 404, 1},
		{"500", `{"error":{"code":500,"message":"backendError"}}`, 500, 2},
	} {
		n := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n++
			w.WriteHeader(c.code)
			w.Write([]byte(c.body))
		}))
		yt := NewYouTube("k")
		yt.BaseURL = srv.URL
		st, err := OpenStore(t.TempDir() + "/datos.jsonl")
		if err != nil {
			t.Fatal(err)
		}
		st.SetVideos("Radio A", []Video{{ID: "a1", ChannelID: "UCA", LiveStatus: "none"}})
		spend := func(f func() error) error { return f() }
		shorts, classified, asked := map[string]bool{}, map[string]bool{}, map[string]bool{}
		for range 2 {
			reviewShorts(yt, st, spend, []ChannelInfo{{ID: "UCA", Uploads: "UUA"}}, shorts, classified, asked)
		}
		srv.Close()
		if n != c.want {
			t.Errorf("%s: lista pedida %d veces, quería %d", c.name, n, c.want)
		}
	}
}
