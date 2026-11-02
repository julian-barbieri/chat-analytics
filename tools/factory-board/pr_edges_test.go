package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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

// Bordes de la consulta de PR del tablero (CA1 a CA4): URLs raras, estados que no
// esperamos, gh que se cuelga y pedidos htmx concurrentes. Nada llama al gh real.

// TestPRURLRejected: CA2 con más URLs raras. Ninguna llega a la consulta y la tarjeta
// queda en "PR abierto" con el aviso; la única válida sí se consulta.
func TestPRURLRejected(t *testing.T) {
	bad := []string{
		"",
		"javascript:alert(1)",
		"--web",
		"-R x",
		"-R otro/repo",
		"https://github.com/a/b/pull/7?x",
		"https://github.com/a/b/pull/1?x=y",
		"https://github.com/a/b/pull/1#discussion",
		"https://github.com/a/b/pull/1/files",
		"https://github.com/a/b/pull/1 --web",
		"https://github.com/a/b/pull/1\n--web",
		"https://github.com/a/b/pull/1\n",
		" https://github.com/a/b/pull/1",
		"https://github.com/a/b/pull/abc",
		"https://github.com/a/b/pull/",
		"https://github.com/a/b/issues/1",
		"https://github.com/a/pull/1",
		"http://github.com/a/b/pull/1",
		"HTTPS://github.com/a/b/pull/1",
		"https://gitlab.com/a/b/pull/1",
		"https://github.com.evil.com/a/b/pull/1",
		"https://evil.com/github.com/a/b/pull/1",
		"https://user@github.com/a/b/pull/1",
		"https://github.com:443/a/b/pull/1",
		"https://github.com/a b/c/pull/1",
		"https://github.com/a/b/pull/1;rm",
		"file:///etc/passwd",
		"data:text/html,<script>alert(1)</script>",
	}
	const good = "https://github.com/julian-barbieri/chat-analytics/pull/7"

	root := t.TempDir()
	writeFile(t, root, "static/htmx.min.js", "/* htmx */")
	for i, u := range append(bad, good) {
		id := fmt.Sprintf("%03d-pr", i+1)
		writeFile(t, root, "factory/jobs/"+id+"/job.json", prJob(id, u))
	}
	var mu sync.Mutex
	calls := map[string]int{}
	b := NewBoard(root)
	b.prQuery = func(ctx context.Context, url string) (PRStatus, error) {
		mu.Lock()
		defer mu.Unlock()
		calls[url]++
		return PRStatus{State: "OPEN"}, nil
	}
	t.Cleanup(b.prWG.Wait)
	now := time.Date(2026, 10, 30, 20, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return now }

	// Las URL también pasan por checkPRs directo: tiene que filtrarlas igual.
	b.checkPRs(context.Background(), bad, now)
	cols, broken := b.columns(now)
	get(t, b.Routes(), "/parcial/tablero")
	for i := range bad {
		get(t, b.Routes(), fmt.Sprintf("/tarea/%03d-pr", i+1))
	}
	b.prWG.Wait()
	if len(broken) != 0 {
		t.Fatalf("tareas con error: %+v", broken)
	}
	cards := map[string]Card{}
	where := map[string]string{}
	for _, c := range cols {
		for _, card := range c.Cards {
			cards[card.PR], where[card.PR] = card, c.Key
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, u := range bad {
		if prURLRe.MatchString(u) {
			t.Errorf("%q pasa la validación", u)
		}
		if calls[u] != 0 {
			t.Errorf("%q: %d consultas, quiero 0", u, calls[u])
		}
		if where[u] != "pr-abierto" || cards[u].PRNote != "PR sin URL de GitHub válida" {
			t.Errorf("%q: columna %q aviso %q", u, where[u], cards[u].PRNote)
		}
	}
	if calls[good] != 1 {
		t.Errorf("la URL válida se consultó %d veces, quiero 1", calls[good])
	}
	if len(calls) != 1 {
		t.Errorf("se consultaron URLs inesperadas: %v", calls)
	}
}

// TestPRUnexpectedState: un estado que no esperamos (o en minúsculas) nunca se toma como
// mergeado ni congela la caché; un MERGED sin fecha o con fecha rara no rompe el
// template, que escapa lo que venga de gh.
func TestPRUnexpectedState(t *testing.T) {
	urls := map[string]string{
		"001-draft":    "https://github.com/a/b/pull/1",
		"002-minus":    "https://github.com/a/b/pull/2",
		"003-sinfecha": "https://github.com/a/b/pull/3",
		"004-html":     "https://github.com/a/b/pull/4",
	}
	resp := map[string]PRStatus{
		urls["001-draft"]:    {State: "DRAFT"},
		urls["002-minus"]:    {State: "merged", MergedAt: "2026-10-30T19:26:22Z"},
		urls["003-sinfecha"]: {State: "MERGED"},
		urls["004-html"]:     {State: "MERGED", MergedAt: "<script>alert(1)</script>"},
	}
	root := t.TempDir()
	writeFile(t, root, "static/htmx.min.js", "/* htmx */")
	for id, u := range urls {
		writeFile(t, root, "factory/jobs/"+id+"/job.json", prJob(id, u))
	}
	var mu sync.Mutex
	calls := map[string]int{}
	b := NewBoard(root)
	b.prQuery = func(ctx context.Context, url string) (PRStatus, error) {
		mu.Lock()
		defer mu.Unlock()
		calls[url]++
		return resp[url], nil
	}
	t.Cleanup(b.prWG.Wait)
	t0 := time.Date(2026, 10, 30, 20, 0, 0, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(t0.UnixNano())
	b.now = func() time.Time { return time.Unix(0, clock.Load()) }
	b.checkPRs(context.Background(), []string{urls["001-draft"], urls["002-minus"], urls["003-sinfecha"], urls["004-html"]}, t0)

	cols, _ := b.columns(t0)
	where := map[string]string{}
	cards := map[string]Card{}
	for _, c := range cols {
		for _, card := range c.Cards {
			where[card.ID], cards[card.ID] = c.Key, card
		}
	}
	want := map[string]string{"001-draft": "pr-abierto", "002-minus": "pr-abierto", "003-sinfecha": "mergeado", "004-html": "mergeado"}
	for id, col := range want {
		if where[id] != col {
			t.Errorf("%s: columna %q, quiero %q", id, where[id], col)
		}
	}
	// Un estado desconocido no se calla: la tarjeta avisa cuál vino de GitHub.
	notes := map[string]string{
		"001-draft":    "Estado desconocido en GitHub: DRAFT",
		"002-minus":    "Estado desconocido en GitHub: merged",
		"003-sinfecha": "",
		"004-html":     "",
	}
	for id, note := range notes {
		if got := cards[id].PRNote; got != note {
			t.Errorf("%s: aviso %q, quiero %q", id, got, note)
		}
	}
	body := get(t, b.Routes(), "/parcial/tablero")
	if !strings.Contains(body, "Estado desconocido en GitHub: DRAFT") {
		t.Error("el tablero no muestra el aviso de estado desconocido")
	}
	if strings.Contains(body, "<script>alert") {
		t.Error("el MergedAt que vino de gh se mostró sin escapar")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("el MergedAt raro no se ve escapado")
	}

	// Vencido prEvery: los dos que no son MERGED se vuelven a consultar.
	clock.Store(t0.Add(prEvery).UnixNano())
	get(t, b.Routes(), "/parcial/tablero")
	b.prWG.Wait()
	mu.Lock()
	defer mu.Unlock()
	for id, n := range map[string]int{"001-draft": 2, "002-minus": 2, "003-sinfecha": 1, "004-html": 1} {
		if calls[urls[id]] != n {
			t.Errorf("%s: %d consultas, quiero %d", id, calls[urls[id]], n)
		}
	}
}

// TestPRConcurrentRequests: CA3 con pedidos htmx concurrentes (tablero y página de
// tarea) mientras la ronda de fondo está trabada y el reloj pasa prEvery: nunca hay dos
// rondas a la vez y cada PR se consulta una vez por ronda. Corre con -race.
func TestPRConcurrentRequests(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "static/htmx.min.js", "/* htmx */")
	const n = 6
	var urls []string
	for i := range n {
		u := fmt.Sprintf("https://github.com/a/b/pull/%d", i+1)
		urls = append(urls, u)
		id := fmt.Sprintf("%03d-pr", i+1)
		writeFile(t, root, "factory/jobs/"+id+"/job.json", prJob(id, u))
	}
	var (
		mu       sync.Mutex
		calls    = map[string]int{}
		inFlight atomic.Int32
		maxIn    atomic.Int32
		release  = make(chan struct{})
		started  = make(chan struct{}, 1)
	)
	b := NewBoard(root)
	b.prQuery = func(ctx context.Context, url string) (PRStatus, error) {
		k := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := maxIn.Load()
			if k <= m || maxIn.CompareAndSwap(m, k) {
				break
			}
		}
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		mu.Lock()
		calls[url]++
		mu.Unlock()
		return PRStatus{State: "OPEN"}, nil
	}
	t.Cleanup(b.prWG.Wait)
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	t0 := time.Date(2026, 10, 30, 20, 0, 0, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(t0.UnixNano())
	b.now = func() time.Time { return time.Unix(0, clock.Load()) }
	h := b.Routes()

	// Desde goroutines no se puede usar t.Fatal (get): se cuentan las respuestas malas.
	var bad atomic.Int32
	hammer := func() {
		var wg sync.WaitGroup
		for g := range 32 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range 10 {
					path := "/parcial/tablero"
					if (g+i)%3 == 0 {
						path = fmt.Sprintf("/tarea/%03d-pr", (g+i)%n+1)
					}
					req := httptest.NewRequest("GET", path, nil)
					req.Host = "localhost:8090"
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, req)
					if rec.Code != 200 {
						bad.Add(1)
					}
				}
			}()
		}
		wg.Wait()
	}

	snapshot := func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]int{}
		for k, v := range calls {
			out[k] = v
		}
		return out
	}

	// Ronda 1: la lanza el primer pedido que llegue. Si es una página de tarea, solo
	// incluye ese PR; si es el tablero, todos. Queda trabada.
	hammer()
	<-started
	// La ronda sigue trabada y el reloj ya pasó prEvery: los pedidos no lanzan otra.
	clock.Store(t0.Add(3 * prEvery).UnixNano())
	hammer()
	once.Do(func() { close(release) })
	b.prWG.Wait()
	r1 := snapshot()
	if len(r1) == 0 {
		t.Fatal("no hubo ninguna consulta")
	}
	for u, c := range r1 {
		if c != 1 {
			t.Errorf("ronda 1: %s consultado %d veces, quiero 1", u, c)
		}
	}

	// A 3*prEvery los de la ronda 1 (guardados con la hora t0) vencieron y los que no
	// entraron nunca se consultaron: las rondas siguientes los consultan una vez cada
	// uno y después todo queda fresco.
	for range 5 {
		get(t, h, "/parcial/tablero")
		b.prWG.Wait()
	}
	hammer()
	b.prWG.Wait()
	final := snapshot()
	for _, u := range urls {
		if want := 1 + r1[u]; final[u] != want {
			t.Errorf("%s consultado %d veces, quiero %d", u, final[u], want)
		}
	}
	if n := bad.Load(); n != 0 {
		t.Errorf("%d pedidos no dieron 200", n)
	}
	if m := maxIn.Load(); m != 1 {
		t.Errorf("hubo %d consultas en vuelo a la vez, quiero 1", m)
	}
}

type fileSnap struct {
	data []byte
	mod  time.Time
	mode fs.FileMode
}

func snapTree(t *testing.T, root string) map[string]fileSnap {
	t.Helper()
	out := map[string]fileSnap{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		s := fileSnap{mod: info.ModTime(), mode: info.Mode()}
		if !d.IsDir() {
			if s.data, err = os.ReadFile(p); err != nil {
				return err
			}
		}
		out[p] = s
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestBoardWritesNothing: CA1 ("job.json queda byte por byte igual") llevado a todo el
// árbol: después de rondas de gh con todos los estados, pasar a Mergeado y páginas de
// tarea, ningún archivo cambió de bytes, fecha ni permisos y no apareció ninguno nuevo.
func TestBoardWritesNothing(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "static/htmx.min.js", "/* htmx */")
	states := []string{"MERGED", "OPEN", "CLOSED", "", "RARO"}
	for i, s := range states {
		id := fmt.Sprintf("%03d-pr", i+1)
		writeFile(t, root, "factory/jobs/"+id+"/job.json", prJob(id, fmt.Sprintf("https://github.com/a/b/pull/%d", i+1)))
		writeFile(t, root, "factory/jobs/"+id+"/triage.md", "estado "+s)
	}
	writeFile(t, root, "factory/events/2026-10-30.jsonl", `{"at":"2026-10-30T20:00:00Z","evento":"PreToolUse","sesion":"s","tool":"Bash","resumen":"x"}`+"\n")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		return os.Chtimes(p, old, old)
	})
	before := snapTree(t, root)

	b := NewBoard(root)
	b.prQuery = func(ctx context.Context, url string) (PRStatus, error) {
		var i int
		fmt.Sscanf(url, "https://github.com/a/b/pull/%d", &i)
		if s := states[i-1]; s != "" {
			return PRStatus{State: s, MergedAt: "2026-10-30T19:26:22Z"}, nil
		}
		return PRStatus{}, errors.New("sin red")
	}
	t.Cleanup(b.prWG.Wait)
	t0 := time.Date(2026, 10, 30, 20, 0, 0, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(t0.UnixNano())
	b.now = func() time.Time { return time.Unix(0, clock.Load()) }
	h := b.Routes()
	for round := range 3 {
		clock.Store(t0.Add(time.Duration(round) * prEvery).UnixNano())
		get(t, h, "/parcial/tablero")
		b.prWG.Wait()
		get(t, h, "/parcial/tablero")
		for i := range states {
			get(t, h, fmt.Sprintf("/tarea/%03d-pr", i+1))
			get(t, h, fmt.Sprintf("/tarea/%03d-pr?archivo=triage.md", i+1))
		}
		get(t, h, "/parcial/actividad")
		b.prWG.Wait()
	}
	if !strings.Contains(get(t, h, "/tarea/001-pr"), `<span class="badge">Mergeado</span>`) {
		t.Error("001 no quedó en Mergeado")
	}

	after := snapTree(t, root)
	for p, s := range before {
		a, ok := after[p]
		switch {
		case !ok:
			t.Errorf("desapareció %s", p)
		case string(a.data) != string(s.data) || !a.mod.Equal(s.mod) || a.mode != s.mode:
			t.Errorf("%s cambió (fecha %s → %s, permisos %v → %v)", p, s.mod, a.mod, s.mode, a.mode)
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			t.Errorf("el tablero creó %s", p)
		}
	}
}

// fakeGh escribe un gh falso en dir que hace lo que diga GH_FALSO.
func fakeGh(t *testing.T, cases string) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("falta bash para el gh falso")
	}
	dir := t.TempDir()
	writeFile(t, dir, "gh", "#!"+bash+"\ncase \"$GH_FALSO\" in\n"+cases+"\nesac\n")
	if err := os.Chmod(filepath.Join(dir, "gh"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestGhPRStatusOutputs: CA4 con salidas raras de gh: JSON inválido, vacío, null, con
// basura atrás o estado vacío dan error; un estado desconocido se devuelve tal cual (y
// TestPRUnexpectedState muestra que no se toma como mergeado); un error sin stderr usa
// el del proceso; el recorte a 120 cuenta caracteres, no bytes.
func TestGhPRStatusOutputs(t *testing.T) {
	long := strings.Repeat("ñ", 200)
	dir := fakeGh(t, `
  vacio) ;;
  null) echo null ;;
  lista) echo '[]' ;;
  basura-atras) echo '{"state":"MERGED"} y algo más' ;;
  sin-estado) echo '{"state":"","mergedAt":null}' ;;
  numero) echo '{"state":7}' ;;
  raro) echo '{"state":"RARO"}' ;;
  callado) exit 3 ;;
  largo) echo "`+long+`" >&2; exit 1 ;;`)
	t.Setenv("PATH", dir)
	const url = "https://github.com/a/b/pull/7"

	for _, c := range []string{"vacio", "null", "lista", "basura-atras", "sin-estado", "numero"} {
		t.Setenv("GH_FALSO", c)
		st, err := ghPRStatus(context.Background(), url)
		if err == nil || err.Error() != "respuesta inesperada de gh" {
			t.Errorf("%s: %+v, %v; quiero \"respuesta inesperada de gh\"", c, st, err)
		}
	}

	t.Setenv("GH_FALSO", "raro")
	if st, err := ghPRStatus(context.Background(), url); err != nil || st.State != "RARO" {
		t.Errorf("raro: %+v, %v", st, err)
	}

	t.Setenv("GH_FALSO", "callado")
	if _, err := ghPRStatus(context.Background(), url); err == nil || err.Error() != "exit status 3" {
		t.Errorf("callado: quiero el error del proceso, tengo %v", err)
	}

	t.Setenv("GH_FALSO", "largo")
	_, err := ghPRStatus(context.Background(), url)
	if err == nil || err.Error() != strings.Repeat("ñ", 120)+"…" {
		t.Errorf("largo: quiero 120 ñ y …, tengo %q", err)
	}
}

// TestGhPRStatusHungChild: gh que se cuelga dejando un nieto con stdout y stderr
// abiertos. Al vencer el plazo, WaitDelay corta la espera: la consulta no queda
// trabada hasta que el nieto termine (y la ronda de fondo no se congela).
func TestGhPRStatusHungChild(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("falta sleep para el gh falso")
	}
	// Con WaitDelay de 1 s la consulta vuelve en ~1,2 s; si esperara al nieto, en 2 s.
	const grandchild = 2 * time.Second
	dir := fakeGh(t, `  colgado) "`+sleep+`" `+fmt.Sprint(int(grandchild.Seconds()))+` & wait ;;`)
	t.Setenv("PATH", dir)
	t.Setenv("GH_FALSO", "colgado")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = ghPRStatus(ctx, "https://github.com/a/b/pull/7")
	took := time.Since(start)
	// El nieto sigue vivo hasta que termina solo: se espera para no dejar procesos.
	defer time.Sleep(grandchild - took + 500*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "no respondió") {
		t.Errorf("quiero error de plazo, tengo %v", err)
	}
	if took > grandchild-300*time.Millisecond {
		t.Errorf("tardó %s: esperó al nieto en vez de cortar con WaitDelay", took)
	}
}
