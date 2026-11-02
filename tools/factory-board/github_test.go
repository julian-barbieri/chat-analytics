package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testBoard es NewBoard con una consulta de PR falsa: ningún test llama al gh real. Al
// terminar espera la ronda de consultas que haya quedado en vuelo.
func testBoard(t *testing.T, root string) *Board {
	t.Helper()
	b := NewBoard(root)
	b.prQuery = func(ctx context.Context, url string) (PRStatus, error) {
		return PRStatus{State: "OPEN"}, nil
	}
	t.Cleanup(b.prWG.Wait)
	return b
}

// get pide path directo al handler (sin red), con Host local.
func get(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Host = "localhost:8090"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET %s = %d", path, rec.Code)
	}
	return rec.Body.String()
}

// fakeGitHub responde según la URL y cuenta las consultas.
type fakeGitHub struct {
	mu    sync.Mutex
	calls map[string]int
	resp  map[string]PRStatus
	errs  map[string]error
}

func (f *fakeGitHub) query(ctx context.Context, url string) (PRStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[url]++
	if err := f.errs[url]; err != nil {
		return PRStatus{}, err
	}
	return f.resp[url], nil
}

func prJob(id, pr string) string {
	return fmt.Sprintf(`{"id":%q,"tipo":"chore","etapa":"pr-abierto","pr":%q}`, id, pr)
}

// TestPRStatusColumns: CA1 y CA2. La columna sale del estado del PR en GitHub y
// job.json no se toca.
func TestPRStatusColumns(t *testing.T) {
	const merged = "2026-10-30T19:26:22Z"
	urls := map[string]string{
		"001-mergeado":  "https://github.com/a/b/pull/1",
		"002-abierto":   "https://github.com/a/b/pull/2",
		"003-cerrado":   "https://github.com/a/b/pull/3",
		"004-error":     "https://github.com/a/b/pull/4",
		"005-esperando": "https://github.com/a/b/pull/5",
		"006-vacio":     "",
		"007-js":        "javascript:alert(1)",
		"008-flag":      "--web",
		"009-repo":      "-R otro/repo",
		"010-query":     "https://github.com/a/b/pull/1?x=y",
	}
	root := t.TempDir()
	writeFile(t, root, "static/htmx.min.js", "/* htmx */")
	before := map[string][]byte{}
	for id, u := range urls {
		writeFile(t, root, "factory/jobs/"+id+"/job.json", prJob(id, u))
		before[id], _ = os.ReadFile(filepath.Join(root, "factory/jobs", id, "job.json"))
	}

	gh := &fakeGitHub{calls: map[string]int{},
		resp: map[string]PRStatus{
			urls["001-mergeado"]:  {State: "MERGED", MergedAt: merged},
			urls["002-abierto"]:   {State: "OPEN"},
			urls["003-cerrado"]:   {State: "CLOSED"},
			urls["005-esperando"]: {State: "OPEN"},
		},
		errs: map[string]error{urls["004-error"]: errors.New("sin red")},
	}
	b := NewBoard(root)
	// La consulta de 005 queda trabada hasta el final: así sigue "sin respuesta" en
	// todos los pedidos del test, aunque la ronda de fondo ya haya arrancado.
	hold := make(chan struct{})
	b.prQuery = func(ctx context.Context, url string) (PRStatus, error) {
		if url == urls["005-esperando"] {
			<-hold
		}
		return gh.query(ctx, url)
	}
	t.Cleanup(b.prWG.Wait)
	t.Cleanup(func() {
		select {
		case <-hold:
		default:
			close(hold)
		}
	})
	now := time.Date(2026, 10, 30, 20, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return now }
	// La caché queda lista para todos menos 005, que sigue "sin respuesta".
	b.checkPRs(context.Background(), []string{urls["001-mergeado"], urls["002-abierto"], urls["003-cerrado"], urls["004-error"]}, now)

	cols, broken := b.columns(now)
	if len(broken) != 0 {
		t.Fatalf("tareas con error: %+v", broken)
	}
	var keys []string
	where := map[string]string{}
	cards := map[string]Card{}
	for _, c := range cols {
		keys = append(keys, c.Key)
		for _, card := range c.Cards {
			where[card.ID] = c.Key
			cards[card.ID] = card
		}
	}
	if i := strings.Index(strings.Join(keys, ","), "pr-abierto,mergeado,"); i < 0 {
		t.Errorf("columnas %v: quiero \"mergeado\" inmediatamente después de \"pr-abierto\"", keys)
	}
	body := get(t, b.Routes(), "/parcial/tablero")
	mergedLocal, _ := time.Parse(time.RFC3339, merged)

	cases := []struct {
		id, col, note, html string
	}{
		{"001-mergeado", "mergeado", "", "Mergeado " + mergedLocal.Local().Format("02/01 15:04")},
		{"002-abierto", "pr-abierto", "", ""},
		{"003-cerrado", "pr-abierto", "El PR se cerró sin merge", "El PR se cerró sin merge"},
		{"004-error", "pr-abierto", "No se pudo consultar GitHub: sin red", "No se pudo consultar GitHub: sin red"},
		{"005-esperando", "pr-abierto", "Consultando GitHub…", "Consultando GitHub…"},
		{"006-vacio", "pr-abierto", "PR sin URL de GitHub válida", "PR sin URL de GitHub válida"},
		{"007-js", "pr-abierto", "PR sin URL de GitHub válida", ""},
		{"008-flag", "pr-abierto", "PR sin URL de GitHub válida", ""},
		{"009-repo", "pr-abierto", "PR sin URL de GitHub válida", ""},
		{"010-query", "pr-abierto", "PR sin URL de GitHub válida", ""},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			if where[c.id] != c.col {
				t.Errorf("columna %q, quiero %q", where[c.id], c.col)
			}
			if got := cards[c.id].PRNote; got != c.note {
				t.Errorf("aviso %q, quiero %q", got, c.note)
			}
			if c.html != "" && !strings.Contains(body, c.html) {
				t.Errorf("el tablero no muestra %q", c.html)
			}
			after, _ := os.ReadFile(filepath.Join(root, "factory/jobs", c.id, "job.json"))
			if string(after) != string(before[c.id]) {
				t.Errorf("job.json cambió:\nantes %s\ndespués %s", before[c.id], after)
			}
		})
	}

	// La página de la tarea muestra la misma etapa.
	page := get(t, b.Routes(), "/tarea/001-mergeado")
	if !strings.Contains(page, `<span class="badge">Mergeado</span>`) {
		t.Errorf("/tarea/001-mergeado no muestra el badge Mergeado")
	}
	if page := get(t, b.Routes(), "/tarea/003-cerrado"); !strings.Contains(page, "PR abierto") || !strings.Contains(page, "El PR se cerró sin merge") {
		t.Errorf("/tarea/003-cerrado: quiero etapa PR abierto con el aviso")
	}

	// CA2: las URL inválidas no se consultan nunca, ni siquiera en la ronda de fondo.
	close(hold)
	b.prWG.Wait()
	for _, id := range []string{"006-vacio", "007-js", "008-flag", "009-repo", "010-query"} {
		if n := gh.calls[urls[id]]; n != 0 {
			t.Errorf("%s: %d consultas con una URL inválida", id, n)
		}
	}
	if gh.calls[urls["005-esperando"]] != 1 {
		t.Errorf("005: el pedido tenía que lanzar una consulta de fondo; hubo %d", gh.calls[urls["005-esperando"]])
	}
}

// TestPRCache: CA3. Muchos pedidos dentro de prEvery dan una consulta por PR; después
// se vuelve a consultar todo menos los mergeados; nunca hay dos rondas a la vez.
func TestPRCache(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "static/htmx.min.js", "/* htmx */")
	urls := []string{"https://github.com/a/b/pull/1", "https://github.com/a/b/pull/2", "https://github.com/a/b/pull/3", "https://github.com/a/b/pull/4"}
	states := map[string]string{urls[0]: "OPEN", urls[1]: "CLOSED", urls[2]: "MERGED"}
	for i, u := range urls {
		id := fmt.Sprintf("00%d-pr", i+1)
		writeFile(t, root, "factory/jobs/"+id+"/job.json", prJob(id, u))
	}

	var (
		mu       sync.Mutex
		calls    = map[string]int{}
		inFlight atomic.Int32
		maxIn    atomic.Int32
		release  = make(chan struct{})
	)
	b := NewBoard(root)
	b.prQuery = func(ctx context.Context, url string) (PRStatus, error) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := maxIn.Load()
			if n <= m || maxIn.CompareAndSwap(m, n) {
				break
			}
		}
		<-release
		mu.Lock()
		calls[url]++
		mu.Unlock()
		if states[url] == "" {
			return PRStatus{}, errors.New("sin red")
		}
		return PRStatus{State: states[url], MergedAt: "2026-10-30T19:26:22Z"}, nil
	}
	t.Cleanup(b.prWG.Wait)
	var clock atomic.Int64
	t0 := time.Date(2026, 10, 30, 20, 0, 0, 0, time.UTC)
	clock.Store(t0.UnixNano())
	b.now = func() time.Time { return time.Unix(0, clock.Load()) }
	h := b.Routes()
	count := func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]int{}
		for k, v := range calls {
			out[k] = v
		}
		return out
	}

	// Ronda 1: la consulta queda trabada mientras llegan los 50 pedidos.
	for range 50 {
		get(t, h, "/parcial/tablero")
	}
	close(release)
	b.prWG.Wait()
	for _, u := range urls {
		if c := count()[u]; c != 1 {
			t.Errorf("ronda 1: %s consultado %d veces, quiero 1", u, c)
		}
	}

	// Dentro de prEvery no se consulta nada más.
	clock.Store(t0.Add(prEvery - time.Second).UnixNano())
	for range 50 {
		get(t, h, "/parcial/tablero")
	}
	b.prWG.Wait()
	for _, u := range urls {
		if c := count()[u]; c != 1 {
			t.Errorf("dentro de prEvery: %s consultado %d veces, quiero 1", u, c)
		}
	}

	// Vencido prEvery: OPEN, CLOSED y el error se vuelven a consultar; MERGED no.
	clock.Store(t0.Add(prEvery).UnixNano())
	for range 50 {
		get(t, h, "/parcial/tablero")
	}
	b.prWG.Wait()
	want := map[string]int{urls[0]: 2, urls[1]: 2, urls[2]: 1, urls[3]: 2}
	for u, w := range want {
		if c := count()[u]; c != w {
			t.Errorf("después de prEvery: %s (%s) consultado %d veces, quiero %d", u, states[u], c, w)
		}
	}
	if m := maxIn.Load(); m != 1 {
		t.Errorf("hubo %d consultas en vuelo a la vez, quiero 1", m)
	}
}

// TestGhPRStatus: CA4, con un gh falso primero en el PATH. Nunca corre el gh real.
func TestGhPRStatus(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("falta bash para el gh falso")
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("falta sleep para el gh falso")
	}
	const url = "https://github.com/a/b/pull/7"
	dir := t.TempDir()
	log := filepath.Join(dir, "llamadas.txt")
	script := "#!" + bash + `
printf '%s|%s|%s\n' "$*" "$GH_PROMPT_DISABLED" "$GH_NO_UPDATE_NOTIFIER" >> "` + log + `"
case "$GH_FALSO" in
  merged) echo '{"mergedAt":"2026-10-30T19:26:22Z","state":"MERGED"}' ;;
  open) echo '{"mergedAt":null,"state":"OPEN"}' ;;
  basura) echo 'no es json' ;;
  lento) exec ` + "\"" + sleep + "\"" + ` 30 ;;
  *) printf 'error: %s\nsegunda línea\n' "` + strings.Repeat("x", 200) + `" >&2; exit 1 ;;
esac
`
	writeFile(t, dir, "gh", script)
	if err := os.Chmod(filepath.Join(dir, "gh"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	t.Setenv("GH_FALSO", "merged")
	st, err := ghPRStatus(context.Background(), url)
	if err != nil || st.State != "MERGED" || st.MergedAt != "2026-10-30T19:26:22Z" {
		t.Errorf("merged: %+v, %v", st, err)
	}
	data, _ := os.ReadFile(log)
	if got := strings.TrimSpace(string(data)); got != "pr view "+url+" --json state,mergedAt|1|1" {
		t.Errorf("gh se llamó con %q", got)
	}

	t.Setenv("GH_FALSO", "open")
	if st, err := ghPRStatus(context.Background(), url); err != nil || st.State != "OPEN" || st.MergedAt != "" {
		t.Errorf("open: %+v, %v", st, err)
	}

	t.Setenv("GH_FALSO", "basura")
	if _, err := ghPRStatus(context.Background(), url); err == nil {
		t.Error("salida que no es JSON: quiero error")
	}

	t.Setenv("GH_FALSO", "falla")
	_, err = ghPRStatus(context.Background(), url)
	if err == nil || !strings.HasPrefix(err.Error(), "error: xxx") || strings.Contains(err.Error(), "segunda") || len([]rune(err.Error())) > 121 {
		t.Errorf("falla: quiero la primera línea de stderr recortada a 120, tengo %q", err)
	}

	t.Setenv("GH_FALSO", "lento")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	start := time.Now()
	_, err = ghPRStatus(ctx, url)
	cancel()
	if err == nil || time.Since(start) > 10*time.Second {
		t.Errorf("lento: quiero error por timeout enseguida, tengo %v a los %s", err, time.Since(start))
	}

	// Una URL inválida no llega a gh: podría ser un flag como --web.
	os.Remove(log)
	for _, bad := range []string{"", "--web", "-R otro/repo", "https://github.com/a/b/pull/1?x=y"} {
		if _, err := ghPRStatus(context.Background(), bad); err == nil {
			t.Errorf("URL inválida %q: quiero error", bad)
		}
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Error("con URL inválida se ejecutó gh")
	}

	t.Setenv("PATH", t.TempDir())
	if _, err := ghPRStatus(context.Background(), url); err == nil || !strings.Contains(err.Error(), "falta la CLI gh") {
		t.Errorf("sin gh: quiero \"falta la CLI gh\", tengo %v", err)
	}
}
