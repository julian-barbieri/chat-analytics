package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Bordes de la vista previa (CA8, CA10, CA11 y CA12): direcciones que no son loopback,
// limpieza que solo toca carpetas de vistas previas, todos los caminos de corte y que no
// queden procesos ni archivos en la carpeta temporal real. Datos sintéticos siempre.

// TestPreviewAddrLoopbackOnly: CA8 con más formas de escribir una dirección. Todo lo
// aceptado es loopback; lo rechazado sale antes de crear nada.
func TestPreviewAddrLoopbackOnly(t *testing.T) {
	ok := map[string]string{
		":8081":           "localhost:8081",
		"localhost:8081":  "localhost:8081",
		"127.0.0.1:8081":  "127.0.0.1:8081",
		"[::1]:8081":      "[::1]:8081",
		"localhost:65535": "localhost:65535",
	}
	for in, want := range ok {
		got, err := normalizeAddr(in)
		if err != nil || got != want {
			t.Errorf("normalizeAddr(%q) = %q, %v; quiero %q", in, got, err, want)
			continue
		}
		host, _, _ := net.SplitHostPort(got)
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			t.Errorf("normalizeAddr(%q) aceptó %q, que no es loopback", in, got)
		}
	}

	bad := []string{
		"0.0.0.0:8081", "[::]:8081", "[0:0:0:0:0:0:0:0]:8081", "[::ffff:0.0.0.0]:8081",
		"0.0.0.0:0", "127.0.0.2:8081", "[::ffff:127.0.0.1]:8081", "192.168.0.10:8081",
		"10.0.0.1:8081", "evil.example.com:8081", "localhost.:8081", "localhost.evil.com:8081",
		" localhost:8081", "localhost :8081", "LOCALHOST:8081", "::1:8081", "[::1]", "[::1]:0",
		"localhost:65536", "localhost:-1", "localhost:8081/x", "127.0.0.1:8081:1", ":", "8081",
	}
	for _, in := range bad {
		if got, err := normalizeAddr(in); err == nil {
			t.Errorf("normalizeAddr(%q) = %q: quiero error", in, got)
		}
	}
	// run con cualquiera de esas sale con error de -addr sin crear nada en la carpeta
	// temporal (ni la copia ni el binario, que se compila adentro de la copia).
	for _, in := range []string{"[::]:8081", "0.0.0.0:0", "[::ffff:127.0.0.1]:8081", "127.0.0.2:8081"} {
		tmp := t.TempDir()
		err := run(context.Background(), config{Repo: repoRoot(t), Datos: filepath.Join(tmp, "nada.jsonl"), Addr: in, tmp: tmp, out: io.Discard})
		if err == nil || !strings.Contains(err.Error(), "-addr") {
			t.Errorf("run con -addr %s: quiero error de -addr, tengo %v", in, err)
		}
		if names := entries(t, tmp); len(names) != 0 {
			t.Errorf("run con -addr %s creó %v", in, names)
		}
	}
}

// TestValidDir: la limpieza (al arrancar y en -cortar) solo acepta carpetas
// yt-monitor-vista-previa-* que estén directamente en la carpeta temporal.
func TestValidDir(t *testing.T) {
	tmp := filepath.Join(string(filepath.Separator), "tmp", "x")
	cases := map[string]bool{
		filepath.Join(tmp, dirPrefix+"123"):                              true,
		filepath.Join(tmp, dirPrefix+"123") + string(filepath.Separator): true,
		filepath.Join(tmp, dirPrefix):                                    true, // nombre raro pero con el prefijo
		"":                                                               false,
		tmp:                                                              false,
		filepath.Dir(tmp):                                                false,
		string(filepath.Separator):                                       false,
		filepath.Join(tmp, stateName):                                    false,
		filepath.Join(tmp, "yt-monitor-vista-previa"): false,
		filepath.Join(tmp, "otra-cosa"):               false,
		filepath.Join(tmp, "sub", dirPrefix+"123"):    false,
		tmp + string(filepath.Separator) + dirPrefix + "1" + string(filepath.Separator) + ".." + string(filepath.Separator) + "..": false,
		tmp + string(filepath.Separator) + dirPrefix + ".." + string(filepath.Separator) + "..":                                    false,
		dirPrefix + "123": false, // relativa
		filepath.Join(filepath.Dir(tmp), dirPrefix+"123"): false,
		filepath.Join(tmp+"y", dirPrefix+"123"):           false,
	}
	for dir, want := range cases {
		if got := validDir(tmp, dir); got != want {
			t.Errorf("validDir(%q, %q) = %v, quiero %v", tmp, dir, got, want)
		}
	}
}

// staleState escribe un archivo de estado que apunta a dir.
func staleState(t *testing.T, tmp, dir string, child int) string {
	t.Helper()
	b, _ := json.Marshal(previewState{Dir: dir, Addr: "localhost:8081", Child: child})
	state := filepath.Join(tmp, stateName)
	writeFile(t, state, string(b))
	return state
}

// TestStaleCleanupOnlyPreviewFolders: CA12. Con el puerto libre, la limpieza de restos
// borra las carpetas de copias de primer nivel de la carpeta privada (la del estado viejo
// y cualquier otra huérfana con el prefijo); nunca la carpeta privada entera, nada de
// afuera, el destino de un enlace, carpetas anidadas ni nombres sin el guion del prefijo.
func TestStaleCleanupOnlyPreviewFolders(t *testing.T) {
	type tc struct {
		name string
		// dir arma el directorio del estado y devuelve qué tiene que seguir existiendo.
		dir func(t *testing.T, tmp, outside string) (string, []string)
	}
	cases := []tc{
		{"carpeta temporal entera", func(t *testing.T, tmp, outside string) (string, []string) {
			writeFile(t, filepath.Join(tmp, "otra-cosa", "x"), "x")
			return tmp, []string{filepath.Join(tmp, "otra-cosa", "x")}
		}},
		{"carpeta de afuera", func(t *testing.T, tmp, outside string) (string, []string) {
			return outside, []string{filepath.Join(outside, "importante")}
		}},
		{"escape con ..", func(t *testing.T, tmp, outside string) (string, []string) {
			rel, err := filepath.Rel(tmp, outside)
			if err != nil {
				t.Fatal(err)
			}
			return filepath.Join(tmp, dirPrefix+"x") + string(filepath.Separator) + ".." + string(filepath.Separator) + rel,
				[]string{filepath.Join(outside, "importante")}
		}},
		{"anidada", func(t *testing.T, tmp, outside string) (string, []string) {
			d := filepath.Join(tmp, "sub", dirPrefix+"x")
			writeFile(t, filepath.Join(d, "datos.jsonl"), "x")
			return d, []string{filepath.Join(d, "datos.jsonl")}
		}},
		{"sin el guion del prefijo", func(t *testing.T, tmp, outside string) (string, []string) {
			d := filepath.Join(tmp, "yt-monitor-vista-previaX")
			writeFile(t, filepath.Join(d, "datos.jsonl"), "x")
			return d, []string{filepath.Join(d, "datos.jsonl")}
		}},
		{"enlace con el prefijo que apunta afuera", func(t *testing.T, tmp, outside string) (string, []string) {
			if runtime.GOOS == "windows" {
				t.Skip("enlaces simbólicos")
			}
			link := filepath.Join(tmp, dirPrefix+"enlace")
			if err := os.Symlink(outside, link); err != nil {
				t.Skip(err)
			}
			return link, []string{filepath.Join(outside, "importante")}
		}},
		{"copia vieja de verdad", func(t *testing.T, tmp, outside string) (string, []string) {
			d, err := os.MkdirTemp(tmp, dirPrefix+"*")
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(d, "datos.jsonl"), "x")
			return d, nil
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tmp, outside := t.TempDir(), t.TempDir()
			writeFile(t, filepath.Join(outside, "importante"), "no borrar")
			// Otra copia huérfana que no es la del archivo de estado: también se barre.
			ajena := filepath.Join(tmp, dirPrefix+"ajena")
			writeFile(t, filepath.Join(ajena, "datos.jsonl"), "de otra corrida")
			dir, keep := c.dir(t, tmp, outside)
			state := staleState(t, tmp, dir, 0)

			p, err := prepare(config{Repo: repoRoot(t), Datos: filepath.Join(outside, "nada.jsonl"), Addr: freeAddr(t), tmp: tmp, out: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			defer p.cleanup()
			for _, k := range append(keep, filepath.Join(outside, "importante"), tmp) {
				if _, err := os.Stat(k); err != nil {
					t.Errorf("la limpieza borró %s: %v", k, err)
				}
			}
			if _, err := os.Stat(ajena); !os.IsNotExist(err) {
				t.Errorf("no barrió la copia huérfana %s", ajena)
			}
			// El estado que queda es el lock de la nueva, no el viejo.
			if st, ok := readState(state); !ok || st.Dir != p.dir {
				t.Errorf("el archivo de estado no es el de la vista previa nueva: %+v, %v", st, ok)
			}
			if c.name == "copia vieja de verdad" {
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Errorf("no borró la copia vieja %s", dir)
				}
			}
		})
	}
}

// TestStopNeverTouchesOutside: CA11 con archivos de estado raros: si no es de una vista
// previa (carpeta inválida, JSON roto, sin PIDs, PID 1 o propio), -cortar avisa, no le
// manda ninguna señal a nadie y no borra ni el archivo ni nada de afuera.
func TestStopNeverTouchesOutside(t *testing.T) {
	for _, c := range []struct {
		name string
		// state arma el archivo con el PID de un proceso de prueba que tiene que seguir vivo.
		state func(t *testing.T, tmp, outside string, pid int) string
	}{
		{"afuera", func(t *testing.T, tmp, outside string, pid int) string {
			b, _ := json.Marshal(previewState{Dir: outside, Child: pid})
			return string(b)
		}},
		{"carpeta temporal entera", func(t *testing.T, tmp, outside string, pid int) string {
			b, _ := json.Marshal(previewState{Dir: tmp, Child: pid})
			return string(b)
		}},
		{"sin carpeta", func(t *testing.T, tmp, outside string, pid int) string {
			b, _ := json.Marshal(previewState{Child: pid, Self: pid})
			return string(b)
		}},
		{"PID propio", func(t *testing.T, tmp, outside string, pid int) string {
			b, _ := json.Marshal(previewState{Dir: outside, Child: os.Getpid()})
			return string(b)
		}},
		{"sin PIDs con carpeta de vista previa", func(t *testing.T, tmp, outside string, pid int) string {
			b, _ := json.Marshal(previewState{Dir: filepath.Join(tmp, dirPrefix+"x")})
			return string(b)
		}},
		{"compilando con vista previa PID 1 con carpeta de vista previa", func(t *testing.T, tmp, outside string, pid int) string {
			b, _ := json.Marshal(previewState{Dir: filepath.Join(tmp, dirPrefix+"x"), Self: 1})
			return string(b)
		}},
		{"PID propio con carpeta de vista previa", func(t *testing.T, tmp, outside string, pid int) string {
			b, _ := json.Marshal(previewState{Dir: filepath.Join(tmp, dirPrefix+"x"), Child: os.Getpid(), Self: pid})
			return string(b)
		}},
		{"PID 1 con carpeta de vista previa", func(t *testing.T, tmp, outside string, pid int) string {
			b, _ := json.Marshal(previewState{Dir: filepath.Join(tmp, dirPrefix+"x"), Child: 1, Self: pid})
			return string(b)
		}},
		// JSON bien formado con un campo de otro tipo: Unmarshal llena Child y Dir antes
		// de fallar en Self, y readState tiene que descartarlo entero.
		{"tipo inválido con carpeta de vista previa", func(t *testing.T, tmp, outside string, pid int) string {
			b, _ := json.Marshal(map[string]any{"Dir": filepath.Join(tmp, dirPrefix+"x"), "Child": pid, "Self": "x"})
			return string(b)
		}},
		{"roto", func(t *testing.T, tmp, outside string, pid int) string { return "{no es json" }},
		{"roto con PID", func(t *testing.T, tmp, outside string, pid int) string {
			return `{"Dir":"` + filepath.ToSlash(outside) + `","Child":` + strconv.Itoa(pid) + `,`
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			tmp, outside := t.TempDir(), t.TempDir()
			writeFile(t, filepath.Join(outside, "importante"), "no borrar")
			writeFile(t, filepath.Join(tmp, "otra-cosa"), "no borrar")
			waited, pid := helperProcess(t)
			state := c.state(t, tmp, outside, pid)
			writeFile(t, filepath.Join(tmp, stateName), state)
			wantTmp := "otra-cosa," + stateName
			if strings.HasSuffix(c.name, "con carpeta de vista previa") {
				writeFile(t, filepath.Join(tmp, dirPrefix+"x", "datos.jsonl"), "x")
				wantTmp = "otra-cosa," + dirPrefix + "x," + stateName
			}

			msg, err := stop(config{tmp: tmp, stopWait: 300 * time.Millisecond})
			if err == nil || !strings.Contains(err.Error(), "no es de una vista previa") {
				t.Errorf("stop = %q, %v; quiero el aviso de que no es de una vista previa", msg, err)
			}
			if strings.Contains(msg, "corté") || strings.Contains(msg, "PID") {
				t.Errorf("stop dice que cortó algo: %q", msg)
			}
			select {
			case err := <-waited:
				t.Errorf("stop le mandó una señal al proceso %d del estado: %v", pid, err)
			case <-time.After(300 * time.Millisecond):
			}
			if got := strings.Join(entries(t, outside), ","); got != "importante" {
				t.Errorf("stop tocó la carpeta de afuera: %s", got)
			}
			if got := strings.Join(entries(t, tmp), ","); got != wantTmp {
				t.Errorf("en la carpeta temporal quedó %s, quiero %s", got, wantTmp)
			}
			if b, _ := os.ReadFile(filepath.Join(tmp, stateName)); string(b) != state {
				t.Errorf("stop cambió el archivo de estado: %q", b)
			}
		})
	}
}

// helperProcess arranca TestHelperProcess (un proceso que duerme) y lo mata al terminar
// el test. waited recibe el resultado de Wait si termina antes.
func helperProcess(t *testing.T) (<-chan error, int) {
	t.Helper()
	return helperProcessIn(t, "")
}

// procsUsing lista los procesos (Linux) cuyo ejecutable o directorio de trabajo está en
// dir: si quedara un dashboard vivo de la vista previa, aparecería acá.
func procsUsing(t *testing.T, dir string) []string {
	t.Helper()
	ps, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range ps {
		if _, err := strconv.Atoi(p.Name()); err != nil {
			continue
		}
		for _, f := range []string{"exe", "cwd"} {
			if l, err := os.Readlink(filepath.Join("/proc", p.Name(), f)); err == nil && strings.HasPrefix(l, dir) {
				out = append(out, p.Name()+" "+f+"="+l)
			}
		}
	}
	return out
}

// realTmpLeftovers lista restos de vista previa en la carpeta temporal real del sistema
// y en la carpeta privada del cache real (los tests usan t.TempDir(): ahí no tiene que
// aparecer nada nuevo).
func realTmpLeftovers(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	dirs := []string{os.TempDir()}
	if cache, err := os.UserCacheDir(); err == nil {
		dirs = append(dirs, filepath.Join(cache, baseName))
	}
	for _, d := range dirs {
		es, _ := os.ReadDir(d)
		for _, e := range es {
			if strings.HasPrefix(e.Name(), "yt-monitor-vista-previa") {
				out[filepath.Join(d, e.Name())] = true
			}
		}
	}
	return out
}

func checkNoRealTmpLeftovers(t *testing.T) {
	t.Helper()
	before := realTmpLeftovers(t)
	t.Cleanup(func() {
		for n := range realTmpLeftovers(t) {
			if !before[n] {
				t.Errorf("el test dejó %s", n)
			}
		}
	})
}

// TestPreviewCutPaths: CA10 por los otros dos caminos de corte (cancelar el contexto,
// como con Ctrl+C, y que el dashboard muera solo). En los dos: el hijo terminó, no
// queda ningún proceso usando la copia, la copia y el estado se borraron y el origen
// quedó igual. Mientras corre: el hijo tiene la clave falsa y no la del entorno, su
// directorio de trabajo es la copia y en la copia solo hay datos y el binario.
func TestPreviewCutPaths(t *testing.T) {
	checkNoRealTmpLeftovers(t)
	const witness = "clave-testigo-no-va"
	t.Setenv("YT_API_KEY", witness)
	t.Setenv("YT_BASE_URL", "http://127.0.0.1:1")

	check := func(t *testing.T, cfg config, r *running) {
		t.Helper()
		pid := strconv.Itoa(r.cmd.Process.Pid)
		if runtime.GOOS == "linux" {
			env, err := os.ReadFile(filepath.Join("/proc", pid, "environ"))
			if err != nil {
				t.Fatalf("no pude leer el entorno del dashboard: %v", err)
			}
			vars := strings.Split(string(env), "\x00")
			var keys []string
			for _, kv := range vars {
				if strings.HasPrefix(kv, "YT_") {
					keys = append(keys, kv)
				}
			}
			if strings.Contains(string(env), witness) {
				t.Errorf("el dashboard recibió la clave del entorno")
			}
			want := []string{"YT_API_KEY=" + fakeKey, "YT_BASE_URL=http://127.0.0.1:"}
			if len(keys) != 2 || keys[0] != want[0] || !strings.HasPrefix(keys[1], want[1]) {
				t.Errorf("variables YT_ del dashboard = %q, quiero %q y %q<puerto>", keys, want[0], want[1])
			}
			if cwd, err := os.Readlink(filepath.Join("/proc", pid, "cwd")); err != nil || cwd != r.dir {
				t.Errorf("directorio de trabajo del dashboard %q (%v), quiero %q", cwd, err, r.dir)
			}
		}
		for _, n := range entries(t, r.dir) {
			if !strings.HasPrefix(n, "datos.") && !strings.HasPrefix(n, "yt-monitor-vista-previa") {
				t.Errorf("en la copia hay %q: solo tendría que haber datos y el binario", n)
			}
		}
		if host, _, _ := net.SplitHostPort(r.addr); host != "127.0.0.1" {
			t.Errorf("el dashboard escucha en %s", r.addr)
		}
	}
	after := func(t *testing.T, cfg config, r *running, src string, stamps map[string]fileStamp) {
		t.Helper()
		if r.cmd.ProcessState == nil {
			t.Error("el proceso del dashboard no terminó")
		}
		for _, p := range []string{r.dir, filepath.Join(cfg.tmp, stateName)} {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Errorf("quedó %s después de cortar", p)
			}
		}
		if ps := procsUsing(t, r.dir); len(ps) > 0 {
			t.Errorf("quedaron procesos usando la copia: %v", ps)
		}
		if n := r.fake.badKeys.Load(); n != 0 {
			t.Errorf("la API simulada recibió %d pedidos con otra clave", n)
		}
		if got := entries(t, cfg.tmp); len(got) != 0 {
			t.Errorf("en la carpeta temporal quedó %v", got)
		}
		checkUntouched(t, src, stamps)
	}

	t.Run("cancelar el contexto", func(t *testing.T) {
		src, stamps := e2eData(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cfg, r, done := startPreviewCtx(t, ctx, src)
		check(t, cfg, r)
		waitFor(t, r.addr, "/parcial/resumen", "Vista previa", 30*time.Second)
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("run terminó con error: %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("run no terminó al cancelar el contexto")
		}
		after(t, cfg, r, src, stamps)
	})

	t.Run("el dashboard muere solo", func(t *testing.T) {
		src, stamps := e2eData(t)
		cfg, r, done := startPreview(t, src)
		check(t, cfg, r)
		if err := r.cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "terminó solo") {
				t.Errorf("run = %v, quiero que avise que el dashboard terminó solo", err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("run no terminó cuando murió el dashboard")
		}
		after(t, cfg, r, src, stamps)
	})
}

// startPreviewCtx es startPreview con un contexto que maneja el test.
func startPreviewCtx(t *testing.T, ctx context.Context, src string) (config, *running, <-chan error) {
	t.Helper()
	out := &lockedBuffer{}
	ready := make(chan *running, 1)
	cfg := config{
		Repo: repoRoot(t), Datos: filepath.Join(src, "datos.jsonl"), Addr: freeAddr(t),
		tmp: t.TempDir(), out: out, stopWait: 15 * time.Second,
		ready: func(r *running) { ready <- r },
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg) }()
	pub := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		pub <- <-done
		close(finished)
	}()
	// Antes del cleanup de t.TempDir(): la vista previa tiene que haber terminado.
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(60 * time.Second):
			t.Errorf("la vista previa no terminó al cancelar:\n%s", out)
		}
	})
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

// TestPreviewSecondRunKeepsLiveCopy: una segunda vista previa en otra dirección no puede
// borrar la copia ni el archivo de estado de una que sigue corriendo: sale con un error
// claro sin tocar nada, y -cortar sigue encontrando a la primera.
func TestPreviewSecondRunKeepsLiveCopy(t *testing.T) {
	src, _ := e2eData(t)
	cfg, r, done := startPreview(t, src)
	state := filepath.Join(cfg.tmp, stateName)
	before := strings.Join(entries(t, cfg.tmp), ",")

	p2, err := prepare(config{Repo: repoRoot(t), Datos: filepath.Join(src, "datos.jsonl"), Addr: freeAddr(t), tmp: cfg.tmp, out: io.Discard})
	if err == nil {
		defer p2.cleanup()
	}
	want := "ya hay una vista previa corriendo en " + r.addr + "; cortala con -cortar"
	if err == nil || err.Error() != want {
		t.Errorf("segunda vista previa: err = %v, quiero %q", err, want)
	}
	if got := strings.Join(entries(t, cfg.tmp), ","); got != before {
		t.Errorf("la segunda vista previa tocó la carpeta temporal: antes %s, después %s", before, got)
	}
	if _, err := os.Stat(r.dir); err != nil {
		t.Errorf("la segunda vista previa borró la copia de la que sigue corriendo: %v", err)
	}
	if st, ok := readState(state); !ok || st.Dir != r.dir {
		t.Errorf("la segunda vista previa borró o pisó el estado de la que sigue corriendo (ok=%v, dir=%q)", ok, st.Dir)
	}
	// Con el estado intacto, -cortar encuentra a la primera y la corta.
	cancelPreview(t, config{tmp: cfg.tmp, stopWait: 15 * time.Second}, done)
}

// slowRepo arma un "repo" cuyo dashboard vive pero nunca escucha (como uno que tarda en
// abrir un datos.jsonl grande).
func slowRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("falta go en el PATH para compilar el dashboard")
	}
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "go.mod"), "module lento\n\ngo 1.24\n")
	writeFile(t, filepath.Join(repo, "main.go"), "package main\n\nimport \"time\"\n\nfunc main() { time.Sleep(60 * time.Second) }\n")
	return repo
}

// startSlow corre run con el repo lento y espera a que aparezca el archivo de estado
// (el dashboard ya arrancó y todavía no escucha). Devuelve el PID del dashboard.
func startSlow(t *testing.T, ctx context.Context, tmp string, startWait time.Duration) (<-chan error, int) {
	t.Helper()
	repo := slowRepo(t)
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, config{Repo: repo, Datos: filepath.Join(tmp, "nada.jsonl"), Addr: freeAddr(t), tmp: tmp, out: io.Discard, startWait: startWait})
	}()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if st, ok := readState(filepath.Join(tmp, stateName)); ok && st.Child > 1 {
			return done, st.Child
		}
		select {
		case err := <-done:
			t.Fatalf("run terminó antes de escribir el estado: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatal("run no escribió el archivo de estado en 2 minutos")
	return nil, 0
}

// waitRun espera a que run termine en d; si no, falla y espera igual (el hijo lento
// termina solo) para no dejar goroutines ni procesos vivos.
func waitRun(t *testing.T, done <-chan error, d time.Duration, what string) error {
	t.Helper()
	start := time.Now()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Errorf("run no terminó %s después de %s", d, what)
		err := <-done
		t.Logf("run terminó %s después de %s", time.Since(start).Round(time.Second), what)
		return err
	}
}

// checkSlowCleaned: no quedó el dashboard lento, ni la copia ni el archivo de estado.
func checkSlowCleaned(t *testing.T, tmp string, child int) {
	t.Helper()
	if processAlive(child) {
		t.Errorf("el dashboard %d sigue vivo", child)
	}
	if got := entries(t, tmp); len(got) != 0 {
		t.Errorf("en la carpeta temporal quedó %v", got)
	}
}

// TestPreviewSlowDashboardCancel: si el dashboard vive pero no escucha, la vista previa
// igual se puede cortar mientras espera (con -cortar o cancelando el contexto, como con
// Ctrl+C) y, al vencer el plazo de arranque, sale con error y limpia: nunca se queda
// esperando a que el hijo termine solo.
func TestPreviewSlowDashboardCancel(t *testing.T) {
	checkNoRealTmpLeftovers(t)

	t.Run("-cortar durante el arranque", func(t *testing.T) {
		tmp := t.TempDir()
		done, child := startSlow(t, context.Background(), tmp, time.Minute)
		msg, err := stop(config{tmp: tmp, stopWait: 15 * time.Second})
		if err != nil || msg != "Vista previa cortada y copia temporal borrada" {
			t.Errorf("stop = %q, %v", msg, err)
		}
		if err := waitRun(t, done, 5*time.Second, "-cortar"); err != nil {
			t.Errorf("run terminó con error: %v", err)
		}
		checkSlowCleaned(t, tmp, child)
	})

	t.Run("cancelar durante el arranque", func(t *testing.T) {
		tmp := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done, child := startSlow(t, ctx, tmp, time.Minute)
		cancel()
		if err := waitRun(t, done, 5*time.Second, "cancelar el contexto"); err != nil {
			t.Errorf("run terminó con error: %v", err)
		}
		checkSlowCleaned(t, tmp, child)
	})

	t.Run("vence el plazo de arranque", func(t *testing.T) {
		tmp := t.TempDir()
		start := time.Now()
		done, child := startSlow(t, context.Background(), tmp, 2*time.Second)
		err := waitRun(t, done, 10*time.Second, "arrancar con plazo de 2 s")
		if err == nil || !strings.Contains(err.Error(), "no empezó a escuchar") {
			t.Errorf("run = %v, quiero que avise que el dashboard no empezó a escuchar", err)
		}
		t.Logf("run terminó %s después de arrancar", time.Since(start).Round(100*time.Millisecond))
		checkSlowCleaned(t, tmp, child)
	})
}

// deadPID devuelve el PID de un proceso que ya terminó y se esperó (no es zombi).
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

// procState devuelve la letra de estado de /proc/<pid>/stat (R, S, Z…) o "" si no está.
func procState(pid int) string {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return ""
	}
	// El nombre va entre paréntesis y puede tener espacios: el estado viene después del último.
	s := string(b)
	if i := strings.LastIndexByte(s, ')'); i >= 0 && i+2 < len(s) {
		return s[i+2 : i+3]
	}
	return ""
}

// TestPreviewStateValidAlive: bordes de valid y alive, que deciden si -cortar puede
// mandar señales y si la próxima vista previa toma el estado como resto o como una viva.
// PID 1, negativo y propio nunca son un hijo válido; Child 0 es válido solo con el PID de
// la vista previa (está compilando). En Linux un PID cuenta como vivo solo si usa la
// copia (directorio de trabajo, ejecutable o archivo abierto): uno reasignado, no.
func TestPreviewStateValidAlive(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, dirPrefix+"x")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	_, liveIn := helperProcessIn(t, dir)
	_, liveOut := helperProcess(t)
	dead := deadPID(t)
	const missing = 1 << 30 // por encima de pid_max en Linux
	linux := runtime.GOOS == "linux"
	for _, c := range []struct {
		name         string
		st           previewState
		valid, alive bool
	}{
		{"hijo vivo en la copia", previewState{Dir: dir, Child: liveIn}, true, true},
		{"hijo muerto y vista previa viva en la copia", previewState{Dir: dir, Child: dead, Self: liveIn}, true, true},
		{"hijo vivo en la copia y vista previa muerta", previewState{Dir: dir, Child: liveIn, Self: dead}, true, true},
		{"compilando con la vista previa viva en la copia", previewState{Dir: dir, Self: liveIn}, true, true},
		// Fuera de Linux no se puede saber: alcanza con que el PID exista.
		{"hijo vivo fuera de la copia (PID reasignado)", previewState{Dir: dir, Child: liveOut}, true, !linux},
		{"compilando con PID reasignado", previewState{Dir: dir, Self: liveOut}, true, !linux},
		{"compilando con la vista previa muerta", previewState{Dir: dir, Self: dead}, true, false},
		{"los dos muertos", previewState{Dir: dir, Child: dead, Self: dead}, true, false},
		{"los dos inexistentes", previewState{Dir: dir, Child: missing, Self: missing}, true, false},
		{"hijo PID 1", previewState{Dir: dir, Child: 1}, false, false},
		{"sin PIDs", previewState{Dir: dir}, false, false},
		{"compilando con vista previa PID 1", previewState{Dir: dir, Self: 1}, false, false},
		{"hijo PID negativo", previewState{Dir: dir, Child: -1, Self: -1}, false, false},
		{"hijo PID propio", previewState{Dir: dir, Child: os.Getpid()}, false, false},
		{"vista previa PID 1", previewState{Dir: dir, Child: dead, Self: 1}, true, false},
		{"vista previa PID propio sin la copia abierta", previewState{Dir: dir, Child: dead, Self: os.Getpid()}, true, false},
		{"carpeta afuera con hijo vivo en la copia", previewState{Dir: t.TempDir(), Child: liveIn}, false, !linux},
	} {
		if got := c.st.valid(tmp); got != c.valid {
			t.Errorf("%s: valid = %v, quiero %v", c.name, got, c.valid)
		}
		if got := c.st.alive(); got != c.alive {
			t.Errorf("%s: alive = %v, quiero %v", c.name, got, c.alive)
		}
	}

	// Un zombi es un hijo que su padre todavía no esperó. Para el dashboard eso solo pasa
	// con la vista previa (su padre) viva, que ya hace vivo al estado; contarlo como vivo
	// es lo seguro (la señal 0 lo encuentra) y deja de contar apenas se lo espera.
	t.Run("zombi", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("usa /proc")
		}
		cmd := exec.Command(os.Args[0], "-test.run=^$")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		pid := cmd.Process.Pid
		deadline := time.Now().Add(10 * time.Second)
		for procState(pid) != "Z" && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		state := procState(pid)
		zombie := processAlive(pid)
		cmd.Wait()
		if state != "Z" {
			t.Fatalf("el proceso %d no quedó zombi (estado %q)", pid, state)
		}
		if !zombie {
			t.Errorf("processAlive(zombi) = false: un hijo sin esperar cuenta como vivo")
		}
		if processAlive(pid) {
			t.Errorf("processAlive = true después de esperar al zombi %d", pid)
		}
	})
}

// TestPreviewStaleOrLiveState: la próxima vista previa, con el puerto libre, solo borra
// la copia del estado si ni el hijo ni la vista previa que lo escribió siguen usándola.
// Con cualquiera de los dos en la copia sale con el error de vista previa corriendo sin
// tocar nada; un PID vivo que no usa la copia (reasignado) no la bloquea (Linux).
func TestPreviewStaleOrLiveState(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("verificar el dueño de un PID solo se puede en Linux (/proc)")
	}
	_, other := helperProcess(t)
	dead := deadPID(t)
	const inCopy = -2 // se reemplaza por un proceso con el directorio de trabajo en la copia
	for _, c := range []struct {
		name        string
		child, self int
		running     bool
	}{
		{"hijo vivo en la copia y vista previa muerta", inCopy, dead, true},
		{"hijo muerto y vista previa viva en la copia", dead, inCopy, true},
		{"compilando con la vista previa viva en la copia", 0, inCopy, true},
		{"PID reasignado a otro proceso", other, other, false},
		{"los dos muertos", dead, dead, false},
		{"hijo muerto y vista previa sin PID", dead, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			tmp := t.TempDir()
			old, err := os.MkdirTemp(tmp, dirPrefix+"*")
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(old, "datos.jsonl"), "copia vieja\n")
			child, self := c.child, c.self
			if child == inCopy || self == inCopy {
				_, pid := helperProcessIn(t, old)
				if child == inCopy {
					child = pid
				} else {
					self = pid
				}
			}
			b, _ := json.Marshal(previewState{Dir: old, Addr: "localhost:9999", Child: child, Self: self})
			state := filepath.Join(tmp, stateName)
			writeFile(t, state, string(b))
			before := strings.Join(entries(t, tmp), ",")

			p, err := prepare(config{Repo: repoRoot(t), Datos: filepath.Join(tmp, "nada.jsonl"), Addr: freeAddr(t), tmp: tmp, out: io.Discard})
			if err == nil {
				defer p.cleanup()
			}
			if c.running {
				want := "ya hay una vista previa corriendo en localhost:9999; cortala con -cortar"
				if err == nil || err.Error() != want {
					t.Errorf("err = %v, quiero %q", err, want)
				}
				if got := strings.Join(entries(t, tmp), ","); got != before {
					t.Errorf("tocó la carpeta temporal: antes %s, después %s", before, got)
				}
				if got, _ := os.ReadFile(state); string(got) != string(b) {
					t.Errorf("cambió el archivo de estado: %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("con el estado muerto quiero que arranque: %v", err)
			}
			if _, err := os.Stat(old); !os.IsNotExist(err) {
				t.Errorf("no borró la copia vieja %s", old)
			}
			if st, ok := readState(state); !ok || st.Dir != p.dir {
				t.Errorf("el estado no es el lock de la nueva: %+v, %v", st, ok)
			}
		})
	}
}

// startFailRepo arma un "repo" que compila pero cuyo resultado no es un ejecutable (un
// paquete que no es main da un archivo ar): cmd.Start falla.
func startFailRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("falta go en el PATH para compilar el dashboard")
	}
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "go.mod"), "module noarranca\n\ngo 1.24\n")
	writeFile(t, filepath.Join(repo, "main.go"), "package noarranca\n\nfunc F() {}\n")
	return repo
}

// TestPreviewStartFailsNoLeftovers: si el dashboard no llega a arrancar, o no se puede
// tomar el archivo de estado, run sale con error, mata al hijo si lo hubo y no deja nada
// propio en la carpeta (ni la copia ni un estado a medias).
func TestPreviewStartFailsNoLeftovers(t *testing.T) {
	checkNoRealTmpLeftovers(t)

	t.Run("cmd.Start falla", func(t *testing.T) {
		tmp := t.TempDir()
		err := run(context.Background(), config{Repo: startFailRepo(t), Datos: filepath.Join(tmp, "nada.jsonl"), Addr: freeAddr(t), tmp: tmp, out: io.Discard, startWait: 5 * time.Second})
		if err == nil || !strings.Contains(err.Error(), "no pude arrancar el dashboard") {
			t.Errorf("run = %v, quiero que avise que no pudo arrancar el dashboard", err)
		}
		if got := entries(t, tmp); len(got) != 0 {
			t.Errorf("en la carpeta temporal quedó %v", got)
		}
	})

	t.Run("no se puede escribir el estado", func(t *testing.T) {
		tmp := t.TempDir()
		// Una carpeta con el nombre del estado: crearlo en exclusiva falla y no es el
		// estado de otra vista previa.
		if err := os.Mkdir(filepath.Join(tmp, stateName), 0o700); err != nil {
			t.Fatal(err)
		}
		err := run(context.Background(), config{Repo: slowRepo(t), Datos: filepath.Join(tmp, "nada.jsonl"), Addr: freeAddr(t), tmp: tmp, out: io.Discard, startWait: 5 * time.Second})
		if err == nil || !strings.Contains(err.Error(), "no pude escribir") {
			t.Errorf("run = %v, quiero que avise que no pudo escribir el estado", err)
		}
		if ps := procsUsing(t, tmp); len(ps) > 0 {
			t.Errorf("quedaron procesos usando la copia: %v", ps)
		}
		if got := strings.Join(entries(t, tmp), ","); got != stateName {
			t.Errorf("en la carpeta temporal quedó %s, quiero solo %s (la carpeta que ya estaba)", got, stateName)
		}
	})
}
