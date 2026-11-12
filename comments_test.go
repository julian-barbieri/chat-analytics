package main

import (
	"context"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestChannelVideosExtraUploads (CA1): con all, ChannelVideos suma las
// últimas subidas de los canales extra (1 unidad por canal); sin all, de los
// extra siguen quedando solo los vivos, como antes.
func TestChannelVideosExtraUploads(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	var lists []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch r.URL.Path {
		case "/playlistItems":
			lists = append(lists, q.Get("playlistId"))
			ids := map[string][]string{"UU1": {"v1"}, "UULV2": {"v3"}, "UU2": {"v5"}}[q.Get("playlistId")]
			var out []string
			for _, id := range ids {
				out = append(out, `{"contentDetails":{"videoId":"`+id+`"}}`)
			}
			w.Write([]byte(`{"items":[` + strings.Join(out, ",") + `]}`))
		case "/videos":
			items := map[string]string{
				"v1": `{"id":"v1","snippet":{"title":"Entrevista","channelId":"C","channelTitle":"Radio","publishedAt":"` + now + `","liveBroadcastContent":"none"},"statistics":{"viewCount":"10","commentCount":"2"}}`,
				"v3": `{"id":"v3","snippet":{"title":"Vivo de Otra","channelId":"UC2","channelTitle":"Otra","publishedAt":"` + now + `","liveBroadcastContent":"live"},"statistics":{}}`,
				"v5": `{"id":"v5","snippet":{"title":"Grabado de Otra","publishedAt":"` + now + `","liveBroadcastContent":"none"},"statistics":{"viewCount":"40","commentCount":"4"}}`,
			}
			var out []string
			for _, id := range strings.Split(q.Get("id"), ",") {
				if items[id] != "" {
					out = append(out, items[id])
				}
			}
			w.Write([]byte(`{"items":[` + strings.Join(out, ",") + `]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	yt := NewYouTube("k")
	yt.BaseURL = srv.URL
	chs := []ChannelInfo{{ID: "C", Title: "Radio", Uploads: "UU1"}, {ID: "UC2", Title: "Otra", Uploads: "UU2"}}

	vids, err := yt.ChannelVideos(chs, 5, nil, false)
	if err != nil || len(vids) != 2 || slicesHasID(vids, "v5") {
		t.Fatalf("sin all: %v %+v", err, vids)
	}
	before := yt.Units
	vids, err = yt.ChannelVideos(chs, 5, nil, true)
	if err != nil || !slicesHasID(vids, "v5") {
		t.Fatalf("con all: %v %+v", err, vids)
	}
	// La lista de subidas del canal extra cuesta 1 unidad más que sin all (4 + 1).
	if got := yt.Units - before; got != 5 {
		t.Errorf("unidades con all = %d, quería 5 (UULV1, UULV2, UU1, UU2 y videos)", got)
	}
	for _, v := range vids {
		if v.ID == "v5" && v.ChannelID != "UC2" {
			t.Errorf("v5 sin canal: %+v", v)
		}
	}
	if !strings.Contains(strings.Join(lists, ","), "UU2") {
		t.Errorf("no se pidieron las subidas del canal extra: %v", lists)
	}

	// Entre revisiones con all se conservan los grabados de los canales extra.
	kept := extraUploads(vids, chs)
	if len(kept) != 1 || kept[0].ID != "v5" {
		t.Fatalf("extraUploads = %+v", kept)
	}
	merged := withExtraUploads([]Video{{ID: "v1", ChannelID: "C"}}, kept, chs)
	if len(merged) != 2 || merged[1].ID != "v5" {
		t.Errorf("withExtraUploads = %+v", merged)
	}
	// Un canal quitado ya no aporta sus grabados.
	if got := withExtraUploads(nil, kept, chs[:1]); len(got) != 0 {
		t.Errorf("con el canal quitado = %+v", got)
	}
}

func slicesHasID(vids []Video, id string) bool {
	for _, v := range vids {
		if v.ID == id {
			return true
		}
	}
	return false
}

// TestWorkerExtraUploadsEvery (CA2): las subidas de los canales extra se
// piden cada extraUploadsEvery, no en cada revisión de videos.
func TestWorkerExtraUploadsEvery(t *testing.T) {
	defer func(k, e time.Duration) { workerTick, extraUploadsEvery = k, e }(workerTick, extraUploadsEvery)
	workerTick, extraUploadsEvery = 2*time.Millisecond, time.Hour
	f := newFakeChannelsAPI(t)
	defer f.srv.Close()
	st, err := OpenStore(t.TempDir() + "/datos.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	yt := NewYouTube("k")
	yt.BaseURL = f.srv.URL
	ctx, cancel := context.WithCancel(context.Background())
	done := runWorker(ctx, yt, st, WorkerConfig{Channels: []string{"UCA", "@b"}, Videos: 5, VideosEvery: 10 * time.Millisecond,
		CommentsEvery: time.Hour, ChatEvery: time.Hour, ChatPeak: time.Hour, Budget: 100000})
	count := func(id string) int {
		_, _, lists := f.get()
		n := 0
		for _, l := range lists {
			if l == id {
				n++
			}
		}
		return n
	}
	waitFor(t, 5*time.Second, "cinco revisiones de videos", func() bool { return count("UULVB") >= 5 })
	cancel()
	<-done
	if n := count("UUB"); n != 1 {
		t.Errorf("subidas del canal extra pedidas %d veces, quería 1 (una por hora)", n)
	}
	if n := count("UUA"); n < 5 {
		t.Errorf("subidas del principal pedidas %d veces, quería una por revisión", n)
	}
}

// TestWorkerExtraComments (CA2, segunda mitad): los comentarios de un grabado
// de un canal extra se piden una vez y no vuelven a pedirse entre revisiones
// de subidas, aunque su contador haya cambiado en YouTube: el worker conserva
// el dato de la última revisión con subidas (con extraUploadsEvery = 1 h, en
// todo el test hay una sola).
func TestWorkerExtraComments(t *testing.T) {
	defer func(k, e time.Duration) { workerTick, extraUploadsEvery = k, e }(workerTick, extraUploadsEvery)
	workerTick, extraUploadsEvery = 2*time.Millisecond, time.Hour
	var (
		mu               sync.Mutex
		count            = "3"
		threads, reviews int
	)
	now := time.Now().UTC().Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/channels":
			switch {
			case q.Get("id") == "UCA":
				w.Write([]byte(`{"items":[{"id":"UCA","snippet":{"title":"Radio A"},"contentDetails":{"relatedPlaylists":{"uploads":"UUA"}}}]}`))
			case q.Get("id") == "UCB":
				w.Write([]byte(`{"items":[{"id":"UCB","snippet":{"title":"Radio B"},"contentDetails":{"relatedPlaylists":{"uploads":"UUB"}}}]}`))
			default:
				w.Write([]byte(`{"items":[]}`))
			}
		case "/playlistItems":
			if q.Get("playlistId") == "UULVB" {
				reviews++
			}
			if q.Get("playlistId") == "UUB" {
				w.Write([]byte(`{"items":[{"contentDetails":{"videoId":"g1"}}]}`))
				return
			}
			w.Write([]byte(`{"items":[]}`))
		case "/videos":
			if strings.Contains(q.Get("id"), "g1") {
				w.Write([]byte(`{"items":[{"id":"g1","snippet":{"title":"Grabado de B","channelId":"UCB","channelTitle":"Radio B","publishedAt":"` + now +
					`","liveBroadcastContent":"none"},"statistics":{"viewCount":"10","commentCount":"` + count + `"}}]}`))
				return
			}
			w.Write([]byte(`{"items":[]}`))
		case "/commentThreads":
			if q.Get("videoId") == "g1" {
				threads++
			}
			w.Write([]byte(`{"items":[]}`))
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
	done := runWorker(ctx, yt, st, WorkerConfig{Channels: []string{"UCA", "UCB"}, Videos: 5, VideosEvery: 10 * time.Millisecond,
		CommentsEvery: 5 * time.Millisecond, ChatEvery: time.Hour, ChatPeak: time.Hour, Budget: 100000})
	get := func() (int, int) {
		mu.Lock()
		defer mu.Unlock()
		return threads, reviews
	}
	waitFor(t, 5*time.Second, "los comentarios del grabado extra", func() bool { n, _ := get(); return n >= 1 })
	// YouTube cambia el contador: sin una revisión de subidas, no se vuelven a pedir.
	mu.Lock()
	count = "9"
	from := reviews
	mu.Unlock()
	waitFor(t, 5*time.Second, "cinco revisiones más", func() bool { _, r := get(); return r >= from+5 })
	cancel()
	<-done
	if n, _ := get(); n != 1 {
		t.Errorf("comentarios del grabado extra pedidos %d veces, quería 1 (hasta la próxima revisión de subidas)", n)
	}
	if !slicesHasID(st.Status().Videos, "g1") {
		t.Error("el grabado extra no quedó en la lista entre revisiones de subidas")
	}
}

// TestVideoArchiveRetention (CA3): datos.videos.jsonl guarda el último dato de
// cada video, se compacta al abrir y descarta lo que no se vio en 30 días.
func TestVideoArchiveRetention(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/datos.jsonl"
	old := time.Now().Add(-31 * 24 * time.Hour)
	os.WriteFile(dir+"/datos.videos.jsonl", []byte(
		`{"Seen":"`+old.UTC().Format(time.RFC3339)+`","ID":"viejo","Title":"Viejo"}`+"\n"+
			"basura\n"), 0o644)
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	pub := now.Add(-48 * time.Hour)
	st.AddVideos(now.Add(-time.Hour), []Video{{ID: "a", Title: "Primero", ChannelID: "UC1", Channel: "Radio", PublishedAt: pub, Views: 10, Comments: 1, LiveStatus: "none"}})
	st.AddVideos(now, []Video{
		{ID: "a", Title: "Título nuevo", ChannelID: "UC1", Channel: "Radio", PublishedAt: pub, Views: 20, Comments: 3, LiveStatus: "none"},
		{ID: "b", Title: "Vivo", ChannelID: "UC1", Channel: "Radio", PublishedAt: pub, LiveStatus: "none", StartedAt: pub},
	})
	got := st.VideoArchive()
	if len(got) != 2 {
		t.Fatalf("archivo = %+v", got)
	}
	byID := map[string]VideoRecord{}
	for _, v := range got {
		byID[v.ID] = v
	}
	if a := byID["a"]; a.Title != "Título nuevo" || a.Views != 20 || a.Comments != 3 || a.Live {
		t.Errorf("a = %+v", a)
	}
	if !byID["b"].Live {
		t.Errorf("b tendría que figurar como vivo: %+v", byID["b"])
	}
	// Al reabrir queda una línea por video, sin el vencido ni la basura.
	st2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st2.VideoArchive(); len(got) != 2 {
		t.Errorf("al reabrir = %+v", got)
	}
	b, _ := os.ReadFile(dir + "/datos.videos.jsonl")
	if n := strings.Count(string(b), "\n"); n != 2 || strings.Contains(string(b), "viejo") || strings.Contains(string(b), "basura") {
		t.Errorf("archivo compactado:\n%s", b)
	}
	if _, err := os.Stat(dir + "/datos.videos.jsonl.tmp"); !os.IsNotExist(err) {
		t.Error("quedó el temporal")
	}
	// Comments copia solo los comentarios desde since.
	st2.Add([]Message{
		{ID: "c1", Source: "comentario", VideoID: "a", Text: "hola", At: now},
		{ID: "m1", Source: "chat", VideoID: "a", Text: "chat", At: now},
		{ID: "c0", Source: "comentario", VideoID: "a", Text: "viejo", At: now.Add(-10 * 24 * time.Hour)},
	})
	if cs := st2.Comments(now.Add(-24 * time.Hour)); len(cs) != 1 || cs[0].ID != "c1" {
		t.Errorf("Comments = %+v", cs)
	}
}

// commentsFixture arma dos canales con grabados, un short, un vivo terminado
// y comentarios repartidos en el tiempo.
func commentsFixture(now time.Time) ([]Message, []VideoRecord) {
	day := 24 * time.Hour
	vids := []VideoRecord{
		{ID: "g1", Title: "Entrevista al ministro", ChannelID: "UC1", Channel: "Radio", PublishedAt: now.Add(-3 * day), Views: 2000, Likes: 120, Comments: 6},
		{ID: "g2", Title: "Editorial", ChannelID: "UC1", Channel: "Radio", PublishedAt: now.Add(-20 * day), Views: 1000, Likes: 40, Comments: 2},
		{ID: "o1", Title: "Acústico", ChannelID: "UC2", Channel: "Otra", PublishedAt: now.Add(-2 * day), Views: 500, Comments: 1},
		{ID: "l1", Title: "Vivo de ayer", ChannelID: "UC1", Channel: "Radio", PublishedAt: now.Add(-day), Live: true, Comments: 1},
		{ID: "s1", Title: "Short del día", ChannelID: "UC1", Channel: "Radio", PublishedAt: now.Add(-day), Views: 4000, Likes: 200, Comments: 1, Short: true},
	}
	c := func(id, video, author, text string, ago time.Duration, likes, replies int64) Message {
		return Message{ID: id, Source: "comentario", VideoID: video, Author: author, Text: text, At: now.Add(-ago), Likes: likes, Replies: replies}
	}
	msgs := []Message{
		c("c1", "g1", "Ana", "¿Dónde está la entrevista completa?", 3*day-30*time.Minute, 58, 0),
		c("c2", "g1", "Beto", "No se escucha nada, arreglen el audio", 3*day-2*time.Hour, 20, 3),
		c("c3", "g1", "Ana", "Excelente entrevista, gracias", 2*day, 400, 30),
		c("c4", "g1", "Caro", "¿Cuándo repiten el programa?", day, 5, 2),
		c("c5", "g1", "Dani", "Saludos desde Rosario", 2*time.Hour, 1, 0),
		c("c6", "g2", "Ana", "Muy bueno el editorial", 15*day, 7, 0),
		c("c7", "g2", "Eli", "¿Y la parte de tarifas?", 19*day, 3, 0),
		c("c8", "o1", "Fede", "Qué buena sesión, excelente", day, 9, 1),
		c("c9", "l1", "Gabi", "¿Lo suben a Spotify?", 20*time.Hour, 2, 0),
		c("c10", "s1", "Iris", "Genial el cierre", 3*time.Hour, 4, 0),
		{ID: "m1", Source: "chat", VideoID: "l1", Author: "Hugo", Text: "chat del vivo", At: now.Add(-day)},
	}
	return msgs, vids
}

// TestCommentsSummary (CA4): los números, la serie por día, las filas y los
// destacados según canal, período y tipo de video.
func TestCommentsSummary(t *testing.T) {
	now := noonToday()
	msgs, vids := commentsFixture(now)
	cases := []struct {
		name                                    string
		q                                       CommentsQuery
		total, people, videos, open, days, rows int
		firstRow                                string
	}{
		// Videos de todos en 30 días: g1 (5), g2 (2), o1 (1); preguntas sin respuestas: c1, c7.
		{"30 días, videos", CommentsQuery{Periodo: "30d", Tipo: "videos"}, 8, 6, 3, 2, 30, 3, "g1"},
		{"7 días, videos", CommentsQuery{Periodo: "7d", Tipo: "videos"}, 6, 5, 2, 1, 7, 3, "g1"},
		{"un canal", CommentsQuery{Canal: "UC2", Periodo: "30d", Tipo: "videos"}, 1, 1, 1, 0, 30, 1, "o1"},
		{"shorts", CommentsQuery{Periodo: "30d", Tipo: "shorts"}, 1, 1, 1, 0, 30, 1, "s1"},
		// Todos: videos y shorts, nunca los vivos (c9 queda afuera).
		{"todos los tipos", CommentsQuery{Periodo: "30d", Tipo: "todos"}, 9, 7, 4, 2, 30, 4, "g1"},
	}
	for _, c := range cases {
		v := CommentsSummary(msgs, vids, nil, c.q, now)
		if v.Total != c.total || v.People != c.people || v.WithComments != c.videos || v.OpenQuestions != c.open ||
			len(v.Days) != c.days || len(v.Rows) != c.rows || (len(v.Rows) > 0 && v.Rows[0].ID != c.firstRow) {
			t.Errorf("%s: total %d personas %d videos %d preguntas %d días %d filas %d (primera %+v)", c.name,
				v.Total, v.People, v.WithComments, v.OpenQuestions, len(v.Days), len(v.Rows), v.Rows)
		}
	}
	v := CommentsSummary(msgs, vids, nil, CommentsQuery{Periodo: "30d", Tipo: "videos", Dest: "likes"}, now)
	// 1 queja (c2) de 8.
	if v.ComplaintPct != 12.5 {
		t.Errorf("quejas = %v", v.ComplaintPct)
	}
	// Las publicaciones se marcan en su día.
	marks := 0
	for _, d := range v.Days {
		marks += len(d.Pub)
	}
	if marks != 3 {
		t.Errorf("marcas de publicación = %d, quería 3", marks)
	}
	if v.Featured[0].ID != "c3" || v.Questions[0].ID != "c1" || v.Authors[0].Name != "Ana" || v.Authors[0].N != 3 || v.Authors[0].Videos != 2 {
		t.Errorf("destacado %+v, pregunta %+v, autor %+v", v.Featured[0], v.Questions[0], v.Authors[0])
	}
	row := v.Rows[0]
	if row.Stored != 5 || row.InPeriod != 5 || row.Per1000 != "3,0" || row.Questions != 2 || row.Title != "Entrevista al ministro" {
		t.Errorf("fila de g1 = %+v", row)
	}
	if len(v.Cats) == 0 || v.CatTotal != 8 {
		t.Errorf("categorías = %d sobre %+v", v.CatTotal, v.Cats)
	}
	if d := CommentsSummary(msgs, vids, nil, CommentsQuery{Periodo: "30d", Tipo: "videos", Dest: "respuestas"}, now); d.Featured[0].ID != "c3" || d.Featured[1].ID != "c2" {
		t.Errorf("más respuestas = %+v", d.Featured)
	}
	if d := CommentsSummary(msgs, vids, nil, CommentsQuery{Periodo: "30d", Tipo: "videos", Dest: "quejas"}, now); len(d.Featured) != 1 || d.Featured[0].ID != "c2" {
		t.Errorf("quejas = %+v", d.Featured)
	}
	if d := CommentsSummary(msgs, vids, nil, CommentsQuery{Periodo: "30d", Tipo: "videos", Orden: "fecha"}, now); d.Rows[0].ID != "o1" {
		t.Errorf("orden por fecha = %+v", d.Rows)
	}
	// Un comentario de un video que no está en el archivo cuenta en "todos los canales" como grabado.
	extra := append(msgs, Message{ID: "x1", Source: "comentario", VideoID: "zz", Author: "Iván", Text: "hola", At: now.Add(-time.Hour)})
	if d := CommentsSummary(extra, vids, nil, CommentsQuery{Periodo: "7d", Tipo: "videos"}, now); d.Total != 7 {
		t.Errorf("con video desconocido = %d", d.Total)
	}
	if d := CommentsSummary(nil, nil, nil, CommentsQuery{Periodo: "30d", Tipo: "videos"}, now); d.Total != 0 || len(d.Days) != 30 {
		t.Errorf("vacío = %+v", d)
	}
}

// TestCommentsVideo (CA5): el detalle de un video con la curva y los filtros de la lista.
func TestCommentsVideo(t *testing.T) {
	now := noonToday()
	msgs, vids := commentsFixture(now)
	d, ok := CommentsVideo(msgs, vids, nil, CommentsQuery{Video: "g1"}, now)
	if !ok || d.Video.Title != "Entrevista al ministro" || d.Total != 5 || len(d.List) != 5 || d.List[0].ID != "c3" {
		t.Fatalf("detalle = %v %+v", ok, d)
	}
	n := 0
	for _, b := range d.Curve {
		n += b.N
	}
	if n != 5 || d.Curve[0].N != 1 {
		t.Errorf("curva = %+v", d.Curve)
	}
	if d.HalfHours == "" {
		t.Error("falta en cuántas horas llegó la mitad")
	}
	cases := []struct {
		name  string
		q     CommentsQuery
		first string
		n     int
	}{
		{"recientes", CommentsQuery{Video: "g1", VOrden: "recientes"}, "c5", 5},
		{"viejos", CommentsQuery{Video: "g1", VOrden: "viejos"}, "c1", 5},
		{"respuestas", CommentsQuery{Video: "g1", VOrden: "respuestas"}, "c3", 5},
		{"buscar sin tildes", CommentsQuery{Video: "g1", Buscar: "DONDE"}, "c1", 1},
		{"categoría", CommentsQuery{Video: "g1", Cat: CatPregunta}, "c1", 2},
		{"sin respuestas", CommentsQuery{Video: "g1", SinResp: true, VOrden: "viejos"}, "c1", 2},
	}
	for _, c := range cases {
		d, _ := CommentsVideo(msgs, vids, nil, c.q, now)
		if d.Shown != c.n || len(d.List) == 0 || d.List[0].ID != c.first {
			t.Errorf("%s: %d mensajes, primero %+v", c.name, d.Shown, d.List)
		}
	}
	if _, ok := CommentsVideo(msgs, vids, nil, CommentsQuery{Video: "nada"}, now); ok {
		t.Error("un video sin datos no tendría que encontrarse")
	}
	// Páginas de commentsPageSize.
	var many []Message
	for i := range commentsPageSize + 3 {
		many = append(many, Message{ID: "p" + string(rune('a'+i%26)) + strings.Repeat("x", i/26), Source: "comentario", VideoID: "g1", Text: "ok", At: now.Add(-time.Duration(i) * time.Minute)})
	}
	if d, _ := CommentsVideo(many, vids, nil, CommentsQuery{Video: "g1", Pag: 2}, now); len(d.List) != 3 || d.Page != 2 || d.Pages != 2 {
		t.Errorf("página 2: %d mensajes, página %d de %d", len(d.List), d.Page, d.Pages)
	}
}

// TestCommentsQuery: los parámetros salen de listas cerradas.
func TestCommentsQuery(t *testing.T) {
	q := commentsQuery(map[string][]string{"periodo": {"<x>"}, "tipo": {"raro"}, "orden": {"x"}, "dest": {"y"}, "vorden": {"z"},
		"cat": {"nada"}, "pag": {"-3"}, "buscar": {strings.Repeat("a", 300)}, "sinresp": {"1"}})
	if q.Periodo != "30d" || q.Tipo != "todos" || q.Orden != "periodo" || q.Dest != "likes" || q.VOrden != "likes" ||
		q.Cat != "" || q.Pag != 1 || len([]rune(q.Buscar)) != 100 || !q.SinResp {
		t.Errorf("commentsQuery = %+v", q)
	}
	// Los tipos de antes caen al defecto: no hay más "vivos" ni "grabados".
	for _, tipo := range []string{"vivos", "grabados"} {
		if q := commentsQuery(map[string][]string{"tipo": {tipo}}); q.Tipo != "todos" {
			t.Errorf("tipo=%s = %q", tipo, q.Tipo)
		}
	}
	for _, o := range []string{"likes", "likesratio"} {
		if q := commentsQuery(map[string][]string{"orden": {o}}); q.Orden != o {
			t.Errorf("orden=%s = %q", o, q.Orden)
		}
	}
}

// TestCommentsPage (CA6, CA7): la pestaña en las cuatro pantallas, la
// pantalla con y sin datos, el detalle y el CSV de un video.
func TestCommentsPage(t *testing.T) {
	st, err := OpenStore(t.TempDir() + "/datos.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{st: st}
	h := srv.Routes()
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	if w := get("/comentarios"); w.Code != 200 || !strings.Contains(w.Body.String(), "Todavía no hay comentarios") {
		t.Errorf("vacío: %d\n%s", w.Code, w.Body.String())
	}
	if w := get("/comentarios?video=g1"); w.Code != 200 || !strings.Contains(w.Body.String(), "Ese video no tiene comentarios guardados") {
		t.Errorf("detalle vacío: %d", w.Code)
	}
	now := time.Now()
	msgs, vids := commentsFixture(now)
	st.Add(msgs)
	for _, v := range vids {
		st.AddVideos(now, []Video{{ID: v.ID, Title: v.Title, ChannelID: v.ChannelID, Channel: v.Channel, PublishedAt: v.PublishedAt,
			Views: v.Views, Likes: v.Likes, Short: v.Short, Comments: v.Comments, LiveStatus: "none", StartedAt: map[bool]time.Time{true: v.PublishedAt}[v.Live]}})
	}
	for _, path := range []string{"/", "/programas", "/canales", "/comentarios"} {
		body := get(path).Body.String()
		i, j, k := strings.Index(body, "<span>Programas</span></a>"), strings.Index(body, "<span>Comentarios y likes</span></a>"), strings.Index(body, "<span>Canales</span></a>")
		if i < 0 || j < i || k < j {
			t.Errorf("%s: las pestañas no están en orden (%d, %d, %d)", path, i, j, k)
		}
	}
	w := get("/comentarios?periodo=<script>&canal=%3Cb%3E")
	body := w.Body.String()
	for _, want := range []string{`<span>Comentarios y likes</span>`, `href="/comentarios" aria-current="page"`, "Entrevista al ministro", "Preguntas sin respuesta", "¿Dónde está la entrevista completa?", "Ana",
		"Comunidad", ">Shorts<", "Likes nuevos", "Videos y shorts"} {
		if !strings.Contains(body, want) {
			t.Errorf("falta %q", want)
		}
	}
	if strings.Contains(body, "<script>") || strings.Contains(body, "&lt;script&gt;") || strings.Contains(body, "&lt;b&gt;") {
		t.Error("se reflejó un parámetro fuera de la lista")
	}
	if w := get("/parcial/comentarios?periodo=7d"); w.Code != 200 || !strings.Contains(w.Body.String(), `id="comentarios"`) {
		t.Errorf("parcial: %d", w.Code)
	}
	w = get("/comentarios?video=g1&vorden=viejos")
	if body := w.Body.String(); w.Code != 200 || !strings.Contains(body, "Cómo llegaron los comentarios") || !strings.Contains(body, "Saludos desde Rosario") ||
		!strings.Contains(body, "/export.csv?video=g1") {
		t.Errorf("detalle: %d\n%s", w.Code, body)
	}
	if w := get("/comentarios?video=nada"); w.Code != 200 || !strings.Contains(w.Body.String(), "Ese video no tiene comentarios guardados") {
		t.Errorf("video inexistente: %d", w.Code)
	}

	// CA7: el CSV de un video.
	w = get("/export.csv?video=g1")
	rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if err != nil || len(rows) != 6 || strings.Join(rows[0], ",") != strings.Join(csvHeader, ",") ||
		!strings.Contains(w.Header().Get("Content-Disposition"), "comentarios_g1_") {
		t.Errorf("CSV: %v %d filas %q", err, len(rows), w.Header().Get("Content-Disposition"))
	}
	for _, r := range rows[1:] {
		if r[0] != "comentario" || r[2] != "Entrevista al ministro" {
			t.Errorf("fila de otro video o sin título: %v", r)
		}
	}
	if w := get("/export.csv?video=a%22b"); w.Code != http.StatusBadRequest {
		t.Errorf("ID inválido: %d", w.Code)
	}
}

// commentsLikes son las muestras de likes del fixture (ver TestCommentsLikes).
func commentsLikes(now time.Time) map[string][]LikeSample {
	day := 24 * time.Hour
	s := func(id string, ago time.Duration, n int64) LikeSample {
		return LikeSample{VideoID: id, At: now.Add(-ago), Likes: n}
	}
	return map[string][]LikeSample{
		"g1": {s("g1", 3*day-time.Hour, 50), s("g1", 2*day, 100), s("g1", time.Hour, 120)},
		"g2": {s("g2", 20*day-2*time.Hour, 10), s("g2", 10*day, 30), s("g2", day, 40)},
		"s1": {s("s1", day-time.Hour, 150), s("s1", 2*time.Hour, 200)},
	}
}

// TestCommentsLikes (CA8): likes del período, likes c/1.000 vistas, likes
// nuevos por día, la tarjeta Videos y shorts y los órdenes por likes.
func TestCommentsLikes(t *testing.T) {
	now := noonToday()
	msgs, vids := commentsFixture(now)
	likes := commentsLikes(now)
	v := CommentsSummary(msgs, vids, likes, CommentsQuery{Periodo: "30d", Tipo: "todos"}, now)
	rows := map[string]CommentVideoRow{}
	for _, r := range v.Rows {
		rows[r.ID] = r
	}
	// Publicados dentro de la ventana: todos sus likes; o1 no tiene dato.
	for id, want := range map[string]string{"g1": "120", "g2": "40", "s1": "200", "o1": "—"} {
		if got := rows[id].LikesP; got != want {
			t.Errorf("likes del período de %s = %q, quería %q", id, got, want)
		}
	}
	if r := rows["g1"]; r.Likes != 120 || r.LikesPer1000 != "60,0" || r.Short {
		t.Errorf("fila g1 = %+v", r)
	}
	if !rows["s1"].Short {
		t.Error("s1 tendría que figurar como short")
	}
	if v.LikesNew != 360 || v.LikesPer1000 != "51,4" {
		t.Errorf("likes nuevos %d, c/1.000 %q", v.LikesNew, v.LikesPer1000)
	}
	// Por día: lo que subió el contador; la primera muestra de un video
	// publicado dentro de la ventana cuenta entera.
	want := map[int]int64{9: 10, 19: 20, 26: 50, 27: 50, 28: 160, 29: 70}
	for i, d := range v.Days {
		if d.Likes != want[i] {
			t.Errorf("likes del día %d (%s) = %d, quería %d", i, commentDayLabel(d.Day), d.Likes, want[i])
		}
	}
	// 7 días: g2 se publicó antes; sus likes del período salen de la última muestra previa.
	v7 := CommentsSummary(msgs, vids, likes, CommentsQuery{Periodo: "7d", Tipo: "todos"}, now)
	for _, r := range v7.Rows {
		if r.ID == "g2" && r.LikesP != "10" {
			t.Errorf("g2 en 7 días = %q, quería 10", r.LikesP)
		}
	}
	if v7.LikesNew != 330 {
		t.Errorf("likes nuevos en 7 días = %d, quería 330", v7.LikesNew)
	}
	if len(v.Kinds) != 2 || v.Kinds[0].Name != "Video" || v.Kinds[0].N != 3 || v.Kinds[0].Likes != 160 || v.Kinds[0].Views != 3500 ||
		v.Kinds[1].Name != "Short" || v.Kinds[1].N != 1 || v.Kinds[1].Likes != 200 || v.Kinds[1].Per1000 != "50,0" {
		t.Errorf("videos y shorts = %+v", v.Kinds)
	}
	for orden, first := range map[string]string{"likes": "s1", "likesratio": "g1"} {
		if d := CommentsSummary(msgs, vids, likes, CommentsQuery{Periodo: "30d", Tipo: "todos", Orden: orden}, now); d.Rows[0].ID != first {
			t.Errorf("orden %s: primero %s, quería %s", orden, d.Rows[0].ID, first)
		}
	}
}

// TestCommentsVideoLikes (CA9): el detalle con los likes del video y su curva
// desde la publicación.
func TestCommentsVideoLikes(t *testing.T) {
	now := noonToday()
	msgs, vids := commentsFixture(now)
	d, ok := CommentsVideo(msgs, vids, commentsLikes(now)["g1"], CommentsQuery{Video: "g1"}, now)
	if !ok || d.Video.Likes != 120 || d.LikesPer1000 != "60,0" || !d.LikesFrom.IsZero() {
		t.Fatalf("detalle = %v %+v", ok, d)
	}
	// 50 a la hora de publicado (1–2 h), +50 al día (1–2 días), +20 a los 3 días menos 1 h (2–3 días).
	got := map[int]int{}
	for i, b := range d.LikeCurve {
		if b.N != 0 {
			got[i] = b.N
		}
	}
	if len(got) != 3 || got[1] != 50 || got[6] != 50 || got[7] != 20 {
		t.Errorf("curva de likes = %+v", d.LikeCurve)
	}
	// Si las muestras empiezan mucho después de publicado, la primera es la base.
	late := []LikeSample{{VideoID: "g2", At: now.Add(-10 * 24 * time.Hour), Likes: 30}, {VideoID: "g2", At: now.Add(-24 * time.Hour), Likes: 40}}
	d, _ = CommentsVideo(msgs, vids, late, CommentsQuery{Video: "g2"}, now)
	n := 0
	for _, b := range d.LikeCurve {
		n += b.N
	}
	if n != 10 || d.LikesFrom.IsZero() {
		t.Errorf("curva con base = %d desde %v", n, d.LikesFrom)
	}
}

// TestCommentsLikesFirstSample (CA8): si la primera muestra de un video
// publicado en la ventana llega días después (el primer día con la serie),
// sus likes van al día de la publicación y no se juntan en el de la muestra.
func TestCommentsLikesFirstSample(t *testing.T) {
	now := noonToday()
	msgs, vids := commentsFixture(now)
	likes := map[string][]LikeSample{"g1": {{VideoID: "g1", At: now.Add(-time.Hour), Likes: 120}}}
	v := CommentsSummary(msgs, vids, likes, CommentsQuery{Periodo: "30d", Tipo: "videos"}, now)
	// g1 se publicó hace 3 días (día 26).
	for i, d := range v.Days {
		want := int64(0)
		if i == 26 {
			want = 120
		}
		if d.Likes != want {
			t.Errorf("día %d = %d, quería %d", i, d.Likes, want)
		}
	}
	// g2 no tiene muestras pero sí contador (40): suma en el total, no en los días.
	if v.LikesNew != 160 {
		t.Errorf("likes nuevos = %d, quería 160", v.LikesNew)
	}
}
