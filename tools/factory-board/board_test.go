package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFile crea path (y sus carpetas) dentro de root.
func writeFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadJobs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "factory/jobs/001-ok/job.json", `{"id":"001-ok","tipo":"fix","etapa":"revision","vuelta":1,"revisiones":{"reviewer":"pendiente"}}`)
	writeFile(t, root, "factory/jobs/002-roto/job.json", `{"etapa": `)
	writeFile(t, root, "factory/jobs/003-rara/job.json", `{"etapa":"mergeada"}`)
	writeFile(t, root, "factory/jobs/004-otro-id/job.json", `{"id":"999-mentira","etapa":"triage"}`)
	os.MkdirAll(filepath.Join(root, "factory/jobs/005-vacia"), 0o755)
	// "mergeado" la calcula el tablero con gh: escrita en job.json es un error.
	writeFile(t, root, "factory/jobs/006-mergeado/job.json", `{"etapa":"mergeado"}`)
	os.MkdirAll(filepath.Join(root, "factory/jobs/no-es-tarea"), 0o755)

	jobs := NewBoard(root).Jobs()
	want := []struct{ id, etapa, err string }{
		{"001-ok", "revision", ""},
		{"002-roto", "", "job.json inválido"},
		{"003-rara", "mergeada", "etapa desconocida"},
		{"004-otro-id", "triage", ""},
		{"005-vacia", "", "todavía no tiene job.json"},
		{"006-mergeado", "mergeado", "etapa desconocida"},
	}
	if len(jobs) != len(want) {
		t.Fatalf("tareas = %d, quiero %d: %+v", len(jobs), len(want), jobs)
	}
	for i, w := range want {
		j := jobs[i]
		if j.ID != w.id || j.Etapa != w.etapa || !strings.Contains(j.Err, w.err) || (w.err == "") != (j.Err == "") {
			t.Errorf("tarea %d = {%s %s %q}, quiero {%s %s %q}", i, j.ID, j.Etapa, j.Err, w.id, w.etapa, w.err)
		}
	}
	if jobs[0].Vuelta != 1 || jobs[0].Revisiones["reviewer"] != "pendiente" {
		t.Errorf("no se leyeron vuelta y revisiones: %+v", jobs[0])
	}
	if _, ok := stageOf(mergedStage.Key); ok || !mergedStage.Derived {
		t.Errorf("la etapa %q tiene que ser derivada y no aceptarse en job.json", mergedStage.Key)
	}
}

func TestAssignEvents(t *testing.T) {
	t0 := time.Date(2026, 10, 30, 15, 0, 0, 0, time.UTC)
	at := func(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }
	evs := []Event{
		{At: at(0), Evento: "PreToolUse", Sesion: "s1", Tool: "Bash", Resumen: "git status"},
		{At: at(1), Evento: "PreToolUse", Sesion: "s1", Tool: "Write", Resumen: "factory/jobs/001-a/job.json.tmp"},
		{At: at(2), Evento: "SubagentStart", Sesion: "s1", AgentID: "x1", Agente: "triage"},
		{At: at(3), Evento: "PreToolUse", Sesion: "s1", AgentID: "x1", Agente: "triage", Tool: "Read", Resumen: "worker.go"},
		{At: at(3), Evento: "PreToolUse", Sesion: "s2", Tool: "Bash", Resumen: "mv factory/jobs/002-b/job.json.tmp factory/jobs/002-b/job.json"},
		{At: at(4), Evento: "SubagentStart", Sesion: "s2", AgentID: "y1", Agente: "reviewer"},
		{At: at(5), Evento: "SubagentStop", Sesion: "s2", AgentID: "y1", Agente: "reviewer", Detalle: "APROBADO"},
		{At: at(6), Evento: "PreToolUse", Tool: "Bash", Resumen: "sin sesión"},
		// La sesión 1 pasa a otra tarea: lo que sigue ya no es de 001-a.
		{At: at(7), Evento: "PreToolUse", Sesion: "s1", Tool: "Write", Resumen: "factory/jobs/003-c/job.json.tmp"},
	}
	assignJobs(evs)
	wantJobs := []string{"", "001-a", "001-a", "001-a", "002-b", "002-b", "002-b", "", "003-c"}
	for i, w := range wantJobs {
		if evs[i].Tarea != w {
			t.Errorf("evento %d (%s %s): tarea %q, quiero %q", i, evs[i].Evento, evs[i].Resumen, evs[i].Tarea, w)
		}
	}

	acts := activities(evs, at(8))
	if a := acts["001-a"]; a == nil || len(a.Active) != 1 || a.Active[0] != "triage" || a.Last.Resumen != "worker.go" {
		t.Errorf("001-a: quiero triage activo y último evento worker.go, tengo %+v", a)
	}
	if a := acts["002-b"]; a == nil || len(a.Active) != 0 || a.Last.Evento != "SubagentStop" {
		t.Errorf("002-b: el reviewer terminó, no tiene que estar activo: %+v", a)
	}
	// Sin movimientos por más de staleAgent, el triage se da por terminado.
	if a := activities(evs, at(3).Add(staleAgent+time.Minute))["001-a"]; len(a.Active) != 0 {
		t.Errorf("agente viejo sigue activo: %+v", a.Active)
	}
}

// fakeRepo arma un repo con dos tareas y eventos de hoy, sin tocar factory/ real.
func fakeRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "static/htmx.min.js", "/* htmx */")
	writeFile(t, root, ".env", "YT_API_KEY=no-se-tiene-que-ver")
	writeFile(t, root, "docs/specs/2026-10-30-a.md", "# Spec de prueba")
	writeFile(t, root, "factory/jobs/001-a/job.json", `{"id":"001-a","tipo":"fix","tarea":"<script>alert(1)</script>","etapa":"esperando-aprobacion","spec":"docs/specs/2026-10-30-a.md","pr":"javascript:alert(1)"}`)
	writeFile(t, root, "factory/jobs/001-a/triage.md", "Causa raíz: worker.go:10")
	writeFile(t, root, "factory/jobs/002-b/job.json", `{"etapa":"descartada","spec":"../../.env"}`)
	// La spec se commitea en la rama de la tarea: desde otra rama no está.
	writeFile(t, root, "factory/jobs/003-c/job.json", `{"etapa":"pr-abierto","spec":"docs/specs/2026-10-30-otra-rama.md"}`)
	day := time.Now().Format("2006-01-02")
	writeFile(t, root, "factory/events/"+day+".jsonl", strings.Join([]string{
		`{"at":"` + time.Now().UTC().Format(time.RFC3339) + `","evento":"PreToolUse","sesion":"s1","tool":"Write","resumen":"factory/jobs/001-a/job.json.tmp"}`,
		`{"at":"` + time.Now().UTC().Format(time.RFC3339) + `","evento":"PreToolUse","sesion":"s1","agente":"triage","agent_id":"x","tool":"Bash","resumen":"go test <b>"}`,
		`línea rota`,
	}, "\n")+"\n")
	return root
}

func TestRoutes(t *testing.T) {
	srv := httptest.NewServer(testBoard(t, fakeRepo(t)).Routes())
	defer srv.Close()

	cases := []struct {
		method, path string
		code         int
		has, hasNot  []string
	}{
		{"GET", "/", 200, []string{`hx-get="/parcial/tablero"`, `hx-get="/parcial/actividad"`}, nil},
		{"GET", "/parcial/tablero", 200,
			[]string{"001-a", "Esperando aprobación", "/factory aprobar 001-a", "&lt;script&gt;", "Descartadas", "002-b", `aria-label="Mergeado"`, "PR sin URL de GitHub válida"},
			[]string{"<script>alert", "javascript:alert"}},
		{"GET", "/parcial/actividad", 200, []string{"triage", "go test &lt;b&gt;", `href="/tarea/001-a"`}, []string{"<b>"}},
		{"GET", "/parcial/actividad?tarea=001-a", 200, []string{"go test"}, nil},
		{"GET", "/parcial/actividad?tarea=../x", 404, nil, nil},
		{"GET", "/tarea/001-a", 200, []string{"Spec de prueba", "?archivo=triage.md", "?archivo=job.json"}, nil},
		{"GET", "/tarea/001-a?archivo=triage.md", 200, []string{"worker.go:10"}, nil},
		// La spec de 002-b apunta afuera de docs/specs: no se ofrece ni se lee.
		{"GET", "/tarea/002-b", 200, []string{"job.json"}, []string{"no-se-tiene-que-ver", "?archivo=spec"}},
		{"GET", "/tarea/002-b?archivo=spec", 404, nil, nil},
		{"GET", "/tarea/003-c", 200, []string{"job.json", "pr-abierto"}, []string{"?archivo=spec"}},
		{"GET", "/tarea/003-c?archivo=spec", 404, nil, nil},
		{"GET", "/tarea/001-a?archivo=../../../.env", 404, nil, nil},
		{"GET", "/tarea/001-a?archivo=..%2F..%2F..%2F.env", 404, nil, nil},
		{"GET", "/tarea/..%2F..%2F.env", 404, nil, nil},
		{"GET", "/tarea/999-nada", 404, nil, nil},
		{"GET", "/static/htmx.min.js", 200, []string{"htmx"}, nil},
		{"GET", "/static/../.env", 404, nil, []string{"no-se-tiene-que-ver"}},
		{"POST", "/", 405, nil, nil},
		{"POST", "/parcial/tablero", 405, nil, nil},
	}
	for _, c := range cases {
		req, _ := http.NewRequest(c.method, srv.URL+c.path, nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != c.code {
			t.Errorf("%s %s = %d, quiero %d", c.method, c.path, res.StatusCode, c.code)
			continue
		}
		for _, s := range c.has {
			if !strings.Contains(string(body), s) {
				t.Errorf("%s %s: falta %q", c.method, c.path, s)
			}
		}
		for _, s := range c.hasNot {
			if strings.Contains(string(body), s) {
				t.Errorf("%s %s: no tiene que aparecer %q", c.method, c.path, s)
			}
		}
	}
}

// TestLocalOnly: sin autenticación, un Host ajeno (DNS rebinding) no puede leer nada
// aunque el pedido llegue a localhost.
func TestLocalOnly(t *testing.T) {
	srv := httptest.NewServer(testBoard(t, fakeRepo(t)).Routes())
	defer srv.Close()
	for host, code := range map[string]int{"evil.example.com": 403, "evil.example.com:8090": 403, "localhost:8090": 200, "127.0.0.1": 200, "[::1]:8090": 200} {
		req, _ := http.NewRequest("GET", srv.URL+"/parcial/tablero", nil)
		req.Host = host
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != code || (code == 403 && strings.Contains(string(body), "001-a")) {
			t.Errorf("Host %s = %d, quiero %d", host, res.StatusCode, code)
		}
	}
}

// TestLogActivityHook corre el hook real con eventos de ejemplo y lee lo que escribió
// con el mismo código del tablero, así el formato de los dos no se separa.
func TestLogActivityHook(t *testing.T) {
	for _, bin := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("falta %s para correr el hook", bin)
		}
	}
	hook, err := filepath.Abs("../../.claude/hooks/log-activity.sh")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	old := filepath.Join(root, "factory/events/2026-01-01.jsonl")
	writeFile(t, root, "factory/events/2026-01-01.jsonl", "{}\n")
	viejo := time.Now().Add(-40 * 24 * time.Hour)
	if err := os.Chtimes(old, viejo, viejo); err != nil {
		t.Fatal(err)
	}

	inputs := []string{
		`{"hook_event_name":"PreToolUse","session_id":"s1","agent_id":"a1","agent_type":"implementer","tool_name":"Bash","tool_input":{"command":"YT_API_KEY=secreta go test\n-run '^TestX$' ./... ` + strings.Repeat("x", 200) + `"}}`,
		`{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Write","tool_input":{"file_path":"` + root + `/factory/jobs/001-a/job.json.tmp","content":"CONTENIDO-PRIVADO"}}`,
		`{"hook_event_name":"SubagentStop","session_id":"s1","agent_id":"a1","agent_type":"reviewer","last_assistant_message":"\n\nVEREDICTO: APROBADO\nresto del informe"}`,
		`{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Agent","tool_input":{"subagent_type":"tester","description":"Correr la suite","prompt":"PROMPT-PRIVADO"}}`,
		`{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Grep","tool_input":{"pattern":"func main","path":"` + root + `/tools"}}`,
		`{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Bash","tool_input":{"command":"curl \"https://www.googleapis.com/youtube/v3/videos?id=x&key=secreta2&part=snippet\""}}`,
		`{"hook_event_name":"PostToolUseFailure","session_id":"s1","tool_name":"Bash","error":"GET https://x?key=AIzaSyA1234567890abcdefghijklmnopqrstuv: 403"}`,
		`{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Bash","tool_input":{"command":"YT_API_KEY=\"clave con espacios\" go run ."}}`,
		`{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Bash","tool_input":{"command":"echo AIzaSyB1234567890abcdefghijklmnopqrstuv"}}`,
		`esto no es JSON`,
	}
	for _, in := range inputs {
		cmd := exec.Command("bash", hook)
		cmd.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+root)
		cmd.Stdin = strings.NewReader(in)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("el hook tiene que salir con 0 siempre: %v\n%s", err, out)
		}
	}

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("el archivo de hace 40 días no se borró (err=%v)", err)
	}
	evs := NewBoard(root).Events()
	if len(evs) != 9 {
		t.Fatalf("eventos = %d, quiero 9: %+v", len(evs), evs)
	}
	if evs[7].Resumen != "YT_API_KEY=*** go run ." || evs[8].Resumen != "echo ***" {
		t.Errorf("clave con comillas o suelta sin redactar: %q, %q", evs[7].Resumen, evs[8].Resumen)
	}
	bash, write, stop := evs[0], evs[1], evs[2]
	if evs[3].Resumen != "tester: Correr la suite" {
		t.Errorf("Agent: quiero tipo y descripción, tengo %q", evs[3].Resumen)
	}
	if evs[4].Resumen != "func main en tools" {
		t.Errorf("Grep: quiero patrón y ruta relativa, tengo %q", evs[4].Resumen)
	}
	if !strings.Contains(evs[5].Resumen, "&key=***&part") {
		t.Errorf("la clave en la URL no se redactó: %q", evs[5].Resumen)
	}
	if d := evs[6].Detalle; !strings.HasPrefix(d, "GET https://x?key=***") || strings.Contains(d, "AIza") {
		t.Errorf("la clave en el detalle de la falla no se redactó: %q", evs[6].Detalle)
	}
	if bash.Agente != "implementer" || bash.AgentID != "a1" || bash.Tool != "Bash" {
		t.Errorf("faltan datos del agente: %+v", bash)
	}
	if strings.Contains(bash.Resumen, "secreta") || !strings.HasPrefix(bash.Resumen, "YT_API_KEY=*** go test -run") {
		t.Errorf("resumen de Bash sin redactar o con saltos de línea: %q", bash.Resumen)
	}
	if n := len([]rune(bash.Resumen)); n > 121 {
		t.Errorf("resumen de %d caracteres, quiero 120 + …", n)
	}
	if write.Resumen != "factory/jobs/001-a/job.json.tmp" || write.Tarea != "001-a" {
		t.Errorf("Write: quiero ruta relativa y tarea 001-a, tengo %+v", write)
	}
	if stop.Detalle != "VEREDICTO: APROBADO" || stop.Tarea != "001-a" {
		t.Errorf("SubagentStop: quiero el veredicto en detalle, tengo %+v", stop)
	}
	data, _ := os.ReadFile(filepath.Join(root, "factory/events", time.Now().Format("2006-01-02")+".jsonl"))
	for _, s := range []string{"CONTENIDO-PRIVADO", "PROMPT-PRIVADO", "resto del informe", "secreta", "AIza", "con espacios"} {
		if strings.Contains(string(data), s) {
			t.Errorf("el hook guardó %q, que no tenía que guardar:\n%s", s, data)
		}
	}
}
