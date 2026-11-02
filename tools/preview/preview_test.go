package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Todos los datos de estos tests son sintéticos y viven en t.TempDir(): nunca se usa el
// datos.jsonl real.

// lockedBuffer junta el log de la vista previa; se escribe desde varias goroutines.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// msgLine arma una línea de datos.jsonl con los campos de Message.
func msgLine(id, source, video, text, program string, at time.Time) string {
	b, _ := json.Marshal(map[string]any{
		"ID": id, "Source": source, "VideoID": video, "Author": "Persona Sintética",
		"Text": text, "At": at, "Program": program,
	})
	return string(b) + "\n"
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func pacificToday() string {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		loc = time.UTC
	}
	return time.Now().In(loc).Format("2006-01-02")
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

// staleRun deja en tmp los restos de una vista previa que murió sin limpiar.
func staleRun(t *testing.T, tmp string) (dir, state string) {
	t.Helper()
	dir, err := os.MkdirTemp(tmp, dirPrefix+"*")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "datos.jsonl"), "copia vieja\n")
	state = filepath.Join(tmp, stateName)
	b, _ := json.Marshal(previewState{Dir: dir, Addr: "localhost:8081", Child: 999999999})
	writeFile(t, state, string(b))
	return dir, state
}

func TestPreviewAddr(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{":8081", "localhost:8081", true},
		{"localhost:8081", "localhost:8081", true},
		{"127.0.0.1:9000", "127.0.0.1:9000", true},
		{"[::1]:8081", "[::1]:8081", true},
		{"0.0.0.0:8081", "", false},
		{"192.168.0.10:8081", "", false},
		{"evil.example.com:8081", "", false},
		{"localhost.evil.com:8081", "", false},
		{":0", "", false},
		{"localhost", "", false},
		{"localhost:http", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, err := normalizeAddr(c.in)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("normalizeAddr(%q) = %q, %v; quiero %q, ok=%v", c.in, got, err, c.want, c.ok)
		}
	}

	// Con una dirección que no es local, run sale antes de crear carpetas o compilar.
	for _, bad := range []string{"0.0.0.0:8081", "192.168.0.10:8081", "evil.example.com:8081"} {
		tmp := t.TempDir()
		err := run(context.Background(), config{Repo: repoRoot(t), Datos: filepath.Join(tmp, "nada.jsonl"), Addr: bad, tmp: tmp, out: io.Discard})
		if err == nil || !strings.Contains(err.Error(), "-addr") {
			t.Errorf("run con -addr %s: quiero error de -addr, tengo %v", bad, err)
		}
		if names := entries(t, tmp); len(names) != 0 {
			t.Errorf("run con -addr %s creó %v", bad, names)
		}
	}
}

// fakeData escribe un datos.jsonl sintético: dos vivos con chat, videos con solo
// comentarios, una línea rota y un mensaje vencido.
func fakeData(t *testing.T, path string, now time.Time) {
	t.Helper()
	h := func(n int) time.Time { return now.Add(-time.Duration(n) * time.Hour) }
	writeFile(t, path, msgLine("c1", "chat", "vivoViejo11", "hola", "PROGRAMA A", h(3))+
		msgLine("c2", "chat", "vivoViejo11", "chau", "PROGRAMA B", h(2))+
		msgLine("c3", "chat", "vivoNuevo11", "buenas", "PROGRAMA C", h(1))+
		msgLine("k0", "comentario", "vivoNuevo11", "comentario en el vivo", "", h(7))+
		msgLine("k1", "comentario", "coment11111", "uno", "", h(5))+
		msgLine("k2", "comentario", "coment22222", "dos", "", h(4))+
		msgLine("k3", "comentario", "coment33333", "tres", "", h(6))+
		"{línea rota\n"+
		msgLine("v1", "chat", "vencido1111", "viejo", "PROGRAMA V", now.Add(-40*24*time.Hour)))
}

func TestFakeAPI(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	path := filepath.Join(t.TempDir(), "datos.jsonl")
	fakeData(t, path, now)
	fake, err := loadFake(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	call := func(path string) (int, string) {
		t.Helper()
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		res, err := http.Get(srv.URL + path + sep + "key=" + fakeKey)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}

	var ch struct {
		Items []struct {
			Snippet        struct{ Title string } `json:"snippet"`
			ContentDetails struct {
				RelatedPlaylists struct{ Uploads string } `json:"relatedPlaylists"`
			} `json:"contentDetails"`
		} `json:"items"`
	}
	for _, q := range []string{"/channels?part=snippet,contentDetails&id=UCLAj5TzCrNbqe3LosSKHLcQ", "/channels?part=snippet&forHandle=@otro"} {
		code, body := call(q)
		if code != 200 || json.Unmarshal([]byte(body), &ch) != nil || len(ch.Items) != 1 ||
			!strings.Contains(ch.Items[0].Snippet.Title, "Vista previa") || ch.Items[0].ContentDetails.RelatedPlaylists.Uploads != uploadsID {
			t.Errorf("%s = %d %s", q, code, body)
		}
	}

	playlist := func(q string) []string {
		t.Helper()
		var r struct {
			Items []struct {
				ContentDetails struct {
					VideoID string `json:"videoId"`
				} `json:"contentDetails"`
			} `json:"items"`
		}
		code, body := call(q)
		if code != 200 || json.Unmarshal([]byte(body), &r) != nil {
			t.Fatalf("%s = %d %s", q, code, body)
		}
		var ids []string
		for _, it := range r.Items {
			ids = append(ids, it.ContentDetails.VideoID)
		}
		return ids
	}
	if got := strings.Join(playlist("/playlistItems?part=contentDetails&playlistId=UULVvistaprevia&maxResults=10"), ","); got != "vivoNuevo11,vivoViejo11" {
		t.Errorf("vivos = %s, quiero vivoNuevo11,vivoViejo11 (más reciente primero)", got)
	}
	if got := strings.Join(playlist("/playlistItems?part=contentDetails&playlistId=UUvistaprevia&maxResults=2"), ","); got != "coment22222,coment11111" {
		t.Errorf("subidas = %s, quiero coment22222,coment11111 (hasta maxResults)", got)
	}
	if got := strings.Join(playlist("/playlistItems?part=contentDetails&playlistId=UUvistaprevia&maxResults=15"), ","); got != "coment22222,coment11111,coment33333" {
		t.Errorf("subidas = %s", got)
	}

	code, body := call("/videos?part=snippet,statistics,liveStreamingDetails&id=vivoViejo11,coment11111,noexiste111,vencido1111")
	var vs struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet struct {
				Title                string `json:"title"`
				ChannelTitle         string `json:"channelTitle"`
				LiveBroadcastContent string `json:"liveBroadcastContent"`
			} `json:"snippet"`
			Statistics struct {
				CommentCount string `json:"commentCount"`
			} `json:"statistics"`
			LiveStreamingDetails struct {
				ActiveLiveChatID string    `json:"activeLiveChatId"`
				ActualStartTime  time.Time `json:"actualStartTime"`
			} `json:"liveStreamingDetails"`
		} `json:"items"`
	}
	if code != 200 || json.Unmarshal([]byte(body), &vs) != nil || len(vs.Items) != 2 {
		t.Fatalf("videos = %d %s", code, body)
	}
	live, com := vs.Items[0], vs.Items[1]
	if live.ID != "vivoViejo11" || live.Snippet.LiveBroadcastContent != "live" || live.LiveStreamingDetails.ActiveLiveChatID != "" ||
		live.Statistics.CommentCount != "0" || !live.LiveStreamingDetails.ActualStartTime.Equal(now.Add(-3*time.Hour)) ||
		!strings.Contains(live.Snippet.Title, "PROGRAMA B") {
		t.Errorf("vivo: %+v", live)
	}
	if com.ID != "coment11111" || com.Snippet.LiveBroadcastContent != "none" || com.Statistics.CommentCount != "0" {
		t.Errorf("video con comentarios: %+v", com)
	}
	if strings.Contains(body, "concurrentViewers") {
		t.Errorf("videos no tiene que traer concurrentViewers: %s", body)
	}

	for _, p := range []string{"/liveChat/messages?liveChatId=x", "/commentThreads?videoId=x", "/search?q=x"} {
		code, body := call(p)
		route := strings.SplitN(p, "?", 2)[0]
		if code != 404 || body != `{"error":{"message":"vista previa: `+route+` no está simulado"}}`+"\n" {
			t.Errorf("%s = %d %q", p, code, body)
		}
	}
	if n := fake.badKeys.Load(); n != 0 {
		t.Errorf("claves distintas a la de la vista previa: %d", n)
	}
	res, err := http.Get(srv.URL + "/channels?id=x&key=otra")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 || fake.badKeys.Load() != 1 {
		t.Errorf("clave ajena: %d, badKeys=%d", res.StatusCode, fake.badKeys.Load())
	}
}

func TestPreviewStartup(t *testing.T) {
	repo := repoRoot(t)

	t.Run("puerto ocupado", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		tmp := t.TempDir()
		dir, state := staleRun(t, tmp)
		before := entries(t, tmp)
		addr := ln.Addr().String()
		err = run(context.Background(), config{Repo: repo, Datos: filepath.Join(t.TempDir(), "datos.jsonl"), Addr: addr, tmp: tmp, out: io.Discard})
		want := "ya hay algo escuchando en " + addr + ": si es una vista previa vieja, cortala con -cortar"
		if err == nil || err.Error() != want {
			t.Errorf("err = %v, quiero %q", err, want)
		}
		if got := entries(t, tmp); strings.Join(got, ",") != strings.Join(before, ",") {
			t.Errorf("tocó la carpeta temporal: antes %v, después %v", before, got)
		}
		for _, p := range []string{dir, state} {
			if _, err := os.Stat(p); err != nil {
				t.Errorf("borró %s con el puerto ocupado", p)
			}
		}
	})

	t.Run("restos de una corrida muerta y copia", func(t *testing.T) {
		tmp, src := t.TempDir(), t.TempDir()
		dir, state := staleRun(t, tmp)
		datos := filepath.Join(src, "datos.jsonl")
		fakeData(t, datos, time.Now())
		writeFile(t, filepath.Join(src, "datos.cuota.json"), `{"Day":"`+pacificToday()+`","Units":120}`)
		writeFile(t, filepath.Join(src, "datos.espectadores.jsonl"), `{"VideoID":"vivoViejo11","At":"2026-10-30T10:00:00Z","Viewers":5}`+"\n")
		writeFile(t, filepath.Join(src, "otro.txt"), "no se copia")
		out := &lockedBuffer{}
		p, err := prepare(config{Repo: repo, Datos: datos, Addr: freeAddr(t), tmp: tmp, out: out})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("no borró la copia vieja %s", dir)
		}
		if st, ok := readState(state); !ok || st.Dir != p.dir || st.Child != 0 || st.Self != os.Getpid() {
			t.Errorf("el estado no es el lock de la nueva (Child 0, Self propio): %+v, %v", st, ok)
		}
		info, err := os.Stat(p.dir)
		if err != nil || filepath.Dir(p.dir) != tmp || !strings.HasPrefix(filepath.Base(p.dir), dirPrefix) {
			t.Fatalf("carpeta de la copia %q: %v", p.dir, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
			t.Errorf("permisos de la copia %v, quiero 0700", info.Mode().Perm())
		}
		if got := strings.Join(entries(t, p.dir), ","); got != "datos.cuota.json,datos.espectadores.jsonl,datos.jsonl" {
			t.Errorf("copió %s", got)
		}
		for _, n := range []string{"datos.jsonl", "datos.cuota.json", "datos.espectadores.jsonl"} {
			a, _ := os.ReadFile(filepath.Join(src, n))
			b, _ := os.ReadFile(filepath.Join(p.dir, n))
			if !bytes.Equal(a, b) {
				t.Errorf("%s copiado distinto", n)
			}
		}
		if p.budget != 0 {
			t.Errorf("con 120 unidades no hay que tocar -cuota: %d", p.budget)
		}
		if strings.Contains(out.String(), "hola") || strings.Contains(out.String(), "Persona Sintética") {
			t.Errorf("el log muestra mensajes o autores: %s", out)
		}
		p.cleanup()
		if _, err := os.Stat(p.dir); !os.IsNotExist(err) {
			t.Errorf("cleanup no borró la copia")
		}
	})

	t.Run("resto que apunta afuera no se borra", func(t *testing.T) {
		tmp, outside := t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(outside, "importante"), "x")
		b, _ := json.Marshal(previewState{Dir: outside})
		writeFile(t, filepath.Join(tmp, stateName), string(b))
		p, err := prepare(config{Repo: repo, Datos: filepath.Join(tmp, "nada.jsonl"), Addr: freeAddr(t), tmp: tmp, out: io.Discard})
		if err != nil {
			t.Fatal(err)
		}
		defer p.cleanup()
		if _, err := os.Stat(filepath.Join(outside, "importante")); err != nil {
			t.Errorf("borró una carpeta fuera de la temporal: %v", err)
		}
	})

	t.Run("sin datos", func(t *testing.T) {
		tmp := t.TempDir()
		out := &lockedBuffer{}
		p, err := prepare(config{Repo: repo, Datos: filepath.Join(t.TempDir(), "datos.jsonl"), Addr: freeAddr(t), tmp: tmp, out: out})
		if err != nil {
			t.Fatal(err)
		}
		defer p.cleanup()
		if names := entries(t, p.dir); len(names) != 0 {
			t.Errorf("sin datos copió %v", names)
		}
		if !strings.Contains(out.String(), "aviso:") || !strings.Contains(out.String(), "sin datos") {
			t.Errorf("no avisó que arranca sin datos: %s", out)
		}
	})

	t.Run("solo datos.jsonl", func(t *testing.T) {
		src := t.TempDir()
		datos := filepath.Join(src, "datos.jsonl")
		fakeData(t, datos, time.Now())
		p, err := prepare(config{Repo: repo, Datos: datos, Addr: freeAddr(t), tmp: t.TempDir(), out: io.Discard})
		if err != nil {
			t.Fatal(err)
		}
		defer p.cleanup()
		if got := strings.Join(entries(t, p.dir), ","); got != "datos.jsonl" {
			t.Errorf("copió %s", got)
		}
	})

	t.Run("repo sin main.go", func(t *testing.T) {
		tmp := t.TempDir()
		_, err := prepare(config{Repo: t.TempDir(), Datos: "datos.jsonl", Addr: freeAddr(t), tmp: tmp, out: io.Discard})
		if err == nil || !strings.Contains(err.Error(), "-repo") {
			t.Errorf("quiero error de -repo, tengo %v", err)
		}
		if names := entries(t, tmp); len(names) != 0 {
			t.Errorf("creó %v", names)
		}
	})

	// CA13: con la cuota copiada en el tope, se sube -cuota para que se vean los vivos.
	for _, c := range []struct {
		name, day     string
		units, budget int
	}{
		{"cuota en el tope", pacificToday(), 9000, 10000},
		{"cuota sobre el tope", pacificToday(), 9500, 10500},
		{"cuota debajo del tope", pacificToday(), 8999, 0},
		{"cuota de otro día", "2020-01-01", 9500, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := t.TempDir()
			datos := filepath.Join(src, "datos.jsonl")
			fakeData(t, datos, time.Now())
			b, _ := json.Marshal(map[string]any{"Day": c.day, "Units": c.units})
			writeFile(t, filepath.Join(src, "datos.cuota.json"), string(b))
			out := &lockedBuffer{}
			p, err := prepare(config{Repo: repo, Datos: datos, Addr: freeAddr(t), tmp: t.TempDir(), out: out})
			if err != nil {
				t.Fatal(err)
			}
			defer p.cleanup()
			if p.budget != c.budget {
				t.Errorf("budget = %d, quiero %d", p.budget, c.budget)
			}
			if avisa := strings.Contains(out.String(), "-cuota"); avisa != (c.budget > 0) {
				t.Errorf("aviso de -cuota = %v, quiero %v: %s", avisa, c.budget > 0, out)
			}
		})
	}
}

// startPreview arranca una vista previa de verdad (compila el binario de la raíz) con
// la copia sintética de src y espera a que el dashboard escuche. Al terminar el test la
// corta y espera, para que el proceso hijo no quede vivo durante el cleanup.
func startPreview(t *testing.T, src string) (config, *running, <-chan error) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("falta go en el PATH para compilar el dashboard")
	}
	out := &lockedBuffer{}
	ready := make(chan *running, 1)
	cfg := config{
		Repo: repoRoot(t), Datos: filepath.Join(src, "datos.jsonl"), Addr: freeAddr(t),
		tmp: t.TempDir(), out: out, stopWait: 15 * time.Second,
		ready: func(r *running) { ready <- r },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg) }()
	result := make(chan error, 1)
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-result:
			result <- err
		case <-time.After(60 * time.Second):
			t.Errorf("la vista previa no terminó al cancelar:\n%s", out)
		}
	})
	// Reenvía el resultado a quien lo pida y al cleanup.
	pub := make(chan error, 1)
	go func() {
		err := <-done
		pub <- err
		result <- err
	}()
	select {
	case r := <-ready:
		return cfg, r, pub
	case err := <-pub:
		t.Fatalf("la vista previa terminó antes de arrancar: %v\n%s", err, out)
	case <-time.After(3 * time.Minute):
		t.Fatalf("la vista previa no arrancó en 3 minutos:\n%s", out)
	}
	return cfg, nil, nil
}

// e2eData arma la copia sintética de origen y devuelve un resumen de cada archivo.
func e2eData(t *testing.T) (string, map[string]fileStamp) {
	t.Helper()
	src := t.TempDir()
	now := time.Now()
	writeFile(t, filepath.Join(src, "datos.jsonl"),
		msgLine("c1", "chat", "vivoUno1111", "mensaje sintético del vivo uno", "PROGRAMA UNO", now.Add(-30*time.Minute))+
			msgLine("c2", "chat", "vivoDos2222", "mensaje sintético del vivo dos", "PROGRAMA DOS", now.Add(-20*time.Minute))+
			msgLine("k1", "comentario", "coment11111", "comentario sintético", "", now.Add(-2*time.Hour)))
	writeFile(t, filepath.Join(src, "datos.cuota.json"), `{"Day":"`+pacificToday()+`","Units":42}`)
	writeFile(t, filepath.Join(src, "datos.espectadores.jsonl"),
		`{"VideoID":"vivoUno1111","At":"`+now.Add(-time.Hour).UTC().Format(time.RFC3339)+`","Viewers":7}`+"\n")
	old := now.Add(-48 * time.Hour).Truncate(time.Second)
	stamps := map[string]fileStamp{}
	for _, n := range []string{"datos.jsonl", "datos.cuota.json", "datos.espectadores.jsonl"} {
		p := filepath.Join(src, n)
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
		stamps[n] = stampOf(t, p)
	}
	return src, stamps
}

type fileStamp struct {
	data []byte
	mod  time.Time
}

func stampOf(t *testing.T, path string) fileStamp {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fileStamp{data, info.ModTime()}
}

func checkUntouched(t *testing.T, src string, stamps map[string]fileStamp) {
	t.Helper()
	for n, s := range stamps {
		now := stampOf(t, filepath.Join(src, n))
		if !bytes.Equal(now.data, s.data) || !now.mod.Equal(s.mod) {
			t.Errorf("%s de origen cambió (fecha %s → %s)", n, s.mod, now.mod)
		}
	}
}

func fetch(addr, path string) (string, error) {
	res, err := http.Get("http://" + addr + path)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	return string(b), err
}

func waitFor(t *testing.T, addr, path, want string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	var last string
	for time.Now().Before(deadline) {
		body, err := fetch(addr, path)
		if err == nil && strings.Contains(body, want) {
			return
		}
		last = body
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("%s no mostró %q en %s; última respuesta:\n%.2000s", path, want, d, last)
}

// TestPreviewEndToEnd: CA10. Compila el dashboard de la raíz y lo levanta contra la API
// simulada con una copia sintética; al cortar no queda nada y el origen no cambió.
func TestPreviewEndToEnd(t *testing.T) {
	t.Setenv("YT_API_KEY", "clave-testigo-no-va")
	t.Setenv("YT_BASE_URL", "http://127.0.0.1:1")
	src, stamps := e2eData(t)
	cfg, r, done := startPreview(t, src)

	if host, _, _ := net.SplitHostPort(r.addr); host != "127.0.0.1" {
		t.Errorf("el dashboard escucha en %s", r.addr)
	}
	state := filepath.Join(cfg.tmp, stateName)
	if _, err := os.Stat(state); err != nil {
		t.Errorf("no está el archivo de estado: %v", err)
	}
	waitFor(t, r.addr, "/parcial/vivo/chat?video=vivoDos2222", "mensaje sintético del vivo dos", 30*time.Second)
	waitFor(t, r.addr, "/parcial/resumen", "Vista previa", 30*time.Second)

	cancelPreview(t, cfg, done)
	if r.cmd.ProcessState == nil {
		t.Error("el proceso del dashboard no terminó")
	}
	for _, p := range []string{r.dir, state} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("quedó %s después de cortar", p)
		}
	}
	if n := r.fake.badKeys.Load(); n != 0 {
		t.Errorf("la API simulada recibió %d pedidos con otra clave (¿la del entorno?)", n)
	}
	if r.fake.requests.Load() == 0 {
		t.Error("la API simulada no recibió ningún pedido")
	}
	checkUntouched(t, src, stamps)
}

// cancelPreview corta la vista previa con -cortar y espera a que run termine.
func cancelPreview(t *testing.T, cfg config, done <-chan error) {
	t.Helper()
	msg, err := stop(cfg)
	if err != nil || msg != "Vista previa cortada y copia temporal borrada" {
		t.Errorf("stop = %q, %v", msg, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run terminó con error: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("run no terminó después de -cortar")
	}
}

// TestHelperProcess hace de dashboard colgado para TestPreviewStop.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("PREVIEW_HELPER") != "1" {
		return
	}
	time.Sleep(60 * time.Second)
	os.Exit(0)
}

func TestPreviewStop(t *testing.T) {
	t.Run("sin vista previa", func(t *testing.T) {
		msg, err := stop(config{tmp: t.TempDir(), stopWait: time.Second})
		if err != nil || msg != "No hay ninguna vista previa corriendo" {
			t.Errorf("stop = %q, %v", msg, err)
		}
	})

	t.Run("en curso", func(t *testing.T) {
		src, stamps := e2eData(t)
		cfg, r, done := startPreview(t, src)
		cancelPreview(t, cfg, done)
		if r.cmd.ProcessState == nil {
			t.Error("el proceso del dashboard no terminó")
		}
		for _, p := range []string{r.dir, filepath.Join(cfg.tmp, stateName)} {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Errorf("quedó %s después de cortar", p)
			}
		}
		checkUntouched(t, src, stamps)
	})

	// Respaldo: un dashboard colgado (su directorio de trabajo es la copia, como el de
	// verdad) y nadie que limpie. Solo en Linux se puede comprobar que el PID es ese.
	t.Run("no responde", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("verificar el dueño de un PID solo se puede en Linux (/proc)")
		}
		tmp := t.TempDir()
		dir, err := os.MkdirTemp(tmp, dirPrefix+"*")
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "datos.jsonl"), "copia\n")
		waited, pid := helperProcessIn(t, dir)
		b, _ := json.Marshal(previewState{Dir: dir, Addr: "localhost:8081", Child: pid})
		writeFile(t, filepath.Join(tmp, stateName), string(b))

		msg, err := stop(config{tmp: tmp, stopWait: 500 * time.Millisecond})
		if err != nil || !strings.Contains(msg, "no respondía") || !strings.Contains(msg, "Vista previa cortada y copia temporal borrada") {
			t.Errorf("stop = %q, %v", msg, err)
		}
		select {
		case <-waited:
		case <-time.After(10 * time.Second):
			t.Error("stop no mató al proceso del dashboard colgado")
		}
		for _, p := range []string{dir, filepath.Join(tmp, stateName)} {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Errorf("quedó %s", p)
			}
		}
	})
}
