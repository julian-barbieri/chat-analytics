package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Revisión 1 de la vista previa: carpeta privada, estado como lock desde que existe la
// copia, -cortar durante la compilación y nada de matar PIDs que no son del dashboard.
// Datos sintéticos siempre y nada en la carpeta temporal ni en el cache reales.

// helperProcessIn es helperProcess con el directorio de trabajo en dir ("" = el del test).
func helperProcessIn(t *testing.T, dir string) (<-chan error, int) {
	t.Helper()
	helper := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	helper.Env = append(os.Environ(), "PREVIEW_HELPER=1")
	helper.Dir = dir
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	waited, exited := make(chan error, 1), make(chan struct{})
	go func() {
		waited <- helper.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		helper.Process.Kill()
		<-exited
	})
	return waited, helper.Process.Pid
}

// newCopy crea una carpeta de copia de vista previa con un datos.jsonl sintético.
func newCopy(t *testing.T, tmp string) string {
	t.Helper()
	dir, err := os.MkdirTemp(tmp, dirPrefix+"*")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "datos.jsonl"), "copia sintética\n")
	return dir
}

func putState(t *testing.T, tmp string, st previewState) string {
	t.Helper()
	b, _ := json.Marshal(st)
	path := filepath.Join(tmp, stateName)
	writeFile(t, path, string(b))
	return path
}

// stillAlive falla si el proceso del helper terminó (alguien lo mató).
func stillAlive(t *testing.T, waited <-chan error, pid int) {
	t.Helper()
	select {
	case err := <-waited:
		t.Errorf("el proceso %d terminó: %v", pid, err)
	case <-time.After(300 * time.Millisecond):
	}
}

// fakeGo pone primero en el PATH un go falso que corre script con sh (SLEEP se cambia
// por la ruta de sleep): sirve para simular una compilación lenta.
func fakeGo(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("el go falso es un script de sh")
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("falta sleep para el go falso")
	}
	dir := t.TempDir()
	body := "#!/bin/sh\n" + strings.ReplaceAll(script, "SLEEP", sleep) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// TestPreviewDefaultTmp: la copia y el estado van en una carpeta privada del cache del
// usuario (0700), no en la carpeta temporal compartida; si ya existía con otros permisos
// se corrigen, y un enlace en su lugar no se acepta.
func TestPreviewDefaultTmp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("LocalAppData", filepath.Join(home, "appdata"))
	cache, err := os.UserCacheDir()
	if err != nil || !strings.HasPrefix(cache, home) {
		t.Fatalf("UserCacheDir = %q, %v: no quedó dentro del HOME de prueba", cache, err)
	}
	want := filepath.Join(cache, "yt-monitor-vista-previa")
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(want, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := defaultTmp()
	if err != nil || got != want {
		t.Fatalf("defaultTmp = %q, %v; quiero %q", got, err, want)
	}
	info, err := os.Lstat(want)
	if err != nil || !info.IsDir() {
		t.Fatalf("%s: %v", want, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Errorf("permisos de %s = %v, quiero 0700", want, info.Mode().Perm())
	}

	t.Run("no existía", func(t *testing.T) {
		t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "nuevo", "cache"))
		t.Setenv("LocalAppData", filepath.Join(home, "nuevo", "appdata"))
		cache, _ := os.UserCacheDir()
		got, err := defaultTmp()
		if want := filepath.Join(cache, "yt-monitor-vista-previa"); err != nil || got != want {
			t.Fatalf("defaultTmp = %q, %v; quiero %q", got, err, want)
		}
		if info, err := os.Lstat(got); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o700) {
			t.Errorf("carpeta nueva %s: %v, %v", got, info.Mode(), err)
		}
	})

	t.Run("enlace en su lugar", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("enlaces simbólicos")
		}
		outside := t.TempDir()
		if err := os.Chmod(outside, 0o755); err != nil {
			t.Fatal(err)
		}
		cache := filepath.Join(home, "otro-cache")
		t.Setenv("XDG_CACHE_HOME", cache)
		if err := os.MkdirAll(cache, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(cache, "yt-monitor-vista-previa")); err != nil {
			t.Skip(err)
		}
		if got, err := defaultTmp(); err == nil {
			t.Errorf("defaultTmp = %q con un enlace en lugar de la carpeta; quiero error", got)
		}
		if info, _ := os.Stat(outside); info.Mode().Perm() != 0o755 {
			t.Errorf("cambió los permisos del destino del enlace: %v", info.Mode().Perm())
		}
	})
}

// TestStopCutFileNoFollow: -cortar crea el aviso sin seguir enlaces: si en la copia ya
// hay un "cortar" que apunta a otro archivo, ese archivo no se toca.
func TestStopCutFileNoFollow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("enlaces simbólicos")
	}
	tmp, outside := t.TempDir(), t.TempDir()
	victim := filepath.Join(outside, "victima")
	writeFile(t, victim, "no tocar")
	dir := newCopy(t, tmp)
	if err := os.Symlink(victim, filepath.Join(dir, stopName)); err != nil {
		t.Skip(err)
	}
	_, pid := helperProcessIn(t, dir)
	putState(t, tmp, previewState{Dir: dir, Addr: "localhost:8081", Child: pid})
	if _, err := stop(config{tmp: tmp, stopWait: 300 * time.Millisecond}); err != nil {
		t.Logf("stop: %v", err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "no tocar" {
		t.Errorf("el aviso de corte siguió el enlace y cambió %s: %q", victim, b)
	}
}

// TestWriteStateNoFollow: el temporal del estado no tiene nombre fijo ni sigue enlaces:
// un enlace plantado con el nombre de antes no cambia el archivo al que apunta.
func TestWriteStateNoFollow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("enlaces simbólicos")
	}
	tmp, outside := t.TempDir(), t.TempDir()
	victim := filepath.Join(outside, "victima")
	writeFile(t, victim, "no tocar")
	if err := os.Symlink(victim, filepath.Join(tmp, stateName+".tmp")); err != nil {
		t.Skip(err)
	}
	path := filepath.Join(tmp, stateName)
	want := previewState{Dir: filepath.Join(tmp, dirPrefix+"x"), Addr: "localhost:8081", Child: 1234, Self: 4321}
	if err := writeState(path, want); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "no tocar" {
		t.Errorf("writeState escribió en el destino del enlace: %q", b)
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		t.Errorf("el estado no quedó como archivo común: %v, %v", info, err)
	}
	if got, ok := readState(path); !ok || got != want {
		t.Errorf("readState = %+v, %v; quiero %+v", got, ok, want)
	}
}

// TestPreviewSweepOrphans: al arrancar se barren las copias huérfanas de la carpeta
// privada (por ejemplo, de una máquina que se apagó con la vista previa abierta) aunque
// no haya archivo de estado, sin tocar lo que no sea una copia. El aviso "Borré…" sale
// solo si de verdad se borró algo.
func TestPreviewSweepOrphans(t *testing.T) {
	t.Run("copias huérfanas sin estado", func(t *testing.T) {
		tmp := t.TempDir()
		a, b := newCopy(t, tmp), newCopy(t, tmp)
		writeFile(t, filepath.Join(tmp, "otra-cosa"), "x")
		writeFile(t, filepath.Join(tmp, "yt-monitor-vista-previaX", "datos.jsonl"), "x")
		out := &lockedBuffer{}
		p, err := prepare(config{Repo: repoRoot(t), Datos: filepath.Join(tmp, "nada.jsonl"), Addr: freeAddr(t), tmp: tmp, out: out})
		if err != nil {
			t.Fatal(err)
		}
		defer p.cleanup()
		for _, d := range []string{a, b} {
			if _, err := os.Stat(d); !os.IsNotExist(err) {
				t.Errorf("no barrió la copia huérfana %s", d)
			}
		}
		for _, k := range []string{"otra-cosa", filepath.Join("yt-monitor-vista-previaX", "datos.jsonl"), filepath.Base(p.dir)} {
			if _, err := os.Stat(filepath.Join(tmp, k)); err != nil {
				t.Errorf("borró %s: %v", k, err)
			}
		}
		if !strings.Contains(out.String(), "Borré") {
			t.Errorf("no avisó que borró copias viejas: %s", out)
		}
	})

	t.Run("estado que apunta afuera no avisa", func(t *testing.T) {
		tmp, outside := t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(outside, "importante"), "x")
		staleState(t, tmp, outside, 0)
		out := &lockedBuffer{}
		p, err := prepare(config{Repo: repoRoot(t), Datos: filepath.Join(tmp, "nada.jsonl"), Addr: freeAddr(t), tmp: tmp, out: out})
		if err != nil {
			t.Fatal(err)
		}
		defer p.cleanup()
		if strings.Contains(out.String(), "Borré") {
			t.Errorf("avisó que borró una copia sin haber borrado nada: %s", out)
		}
		if _, err := os.Stat(filepath.Join(outside, "importante")); err != nil {
			t.Errorf("borró afuera: %v", err)
		}
	})
}

// TestPreviewCutDuringBuild: -cortar encuentra la vista previa mientras compila (el
// estado se toma apenas existe la copia) y la corta sin esperar a que termine go build,
// aunque go deje un hijo con la salida abierta (WaitDelay). No queda nada.
func TestPreviewCutDuringBuild(t *testing.T) {
	checkNoRealTmpLeftovers(t)
	repo := slowRepo(t)
	const build = 4 * time.Second
	// El sleep en segundo plano es un nieto que se queda con la salida de go abierta.
	fakeGo(t, "SLEEP 4 & wait")
	start := time.Now()
	// Al final se espera a que termine ese nieto, para no dejar procesos.
	defer func() { time.Sleep(time.Until(start.Add(build + 300*time.Millisecond))) }()
	tmp := t.TempDir()
	done := make(chan error, 1)
	go func() {
		done <- run(context.Background(), config{Repo: repo, Datos: filepath.Join(tmp, "nada.jsonl"), Addr: freeAddr(t), tmp: tmp, out: io.Discard})
	}()
	// Que ya esté compilando: la copia existe hace un rato.
	deadline := time.Now().Add(10 * time.Second)
	for len(entries(t, tmp)) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	cutAt := time.Now()
	msg, err := stop(config{tmp: tmp, stopWait: 15 * time.Second})
	if err != nil || msg != "Vista previa cortada y copia temporal borrada" {
		t.Errorf("stop durante la compilación = %q, %v", msg, err)
	}
	if err := waitRun(t, done, 10*time.Second, "-cortar"); err != nil {
		t.Errorf("run terminó con error: %v", err)
	}
	took := time.Since(cutAt)
	t.Logf("de -cortar a terminar: %s (WaitDelay de 2 s por el nieto de go)", took.Round(100*time.Millisecond))
	if took > build-500*time.Millisecond {
		t.Errorf("cortar tardó %s: esperó a que terminara la compilación", took.Round(100*time.Millisecond))
	}
	if got := entries(t, tmp); len(got) != 0 {
		t.Errorf("en la carpeta quedó %v", got)
	}
}

// TestPreviewConcurrentRuns: dos vistas previas casi a la vez. Una sola toma el estado;
// la otra sale con "ya hay una vista previa corriendo en <addr>" sin dejar nada suyo ni
// tocar lo de la primera, y la primera se puede cortar con -cortar (está compilando).
func TestPreviewConcurrentRuns(t *testing.T) {
	checkNoRealTmpLeftovers(t)
	repo := slowRepo(t)
	fakeGo(t, "exec SLEEP 5")
	tmp := t.TempDir()
	type result struct {
		addr string
		err  error
	}
	results := make(chan result, 2)
	addrs := []string{freeAddr(t), freeAddr(t)}
	ctx, cancel := context.WithCancel(context.Background())
	pending := len(addrs)
	// Si el test corta antes, las dos terminan antes del cleanup de t.TempDir().
	t.Cleanup(func() {
		cancel()
		for ; pending > 0; pending-- {
			select {
			case <-results:
			case <-time.After(30 * time.Second):
				t.Errorf("una vista previa no terminó al cancelar")
				return
			}
		}
	})
	for _, a := range addrs {
		go func() {
			results <- result{a, run(ctx, config{Repo: repo, Datos: filepath.Join(tmp, "nada.jsonl"), Addr: a, tmp: tmp, out: io.Discard})}
		}()
	}
	var first result
	select {
	case first = <-results:
		pending--
	case <-time.After(4 * time.Second):
		t.Fatal("ninguna de las dos salió: las dos siguen corriendo")
	}
	other := addrs[0]
	if first.addr == addrs[0] {
		other = addrs[1]
	}
	want := "ya hay una vista previa corriendo en " + other + "; cortala con -cortar"
	if first.err == nil || first.err.Error() != want {
		t.Errorf("la que salió primero (%s): err = %v; quiero %q", first.addr, first.err, want)
	}
	st, ok := readState(filepath.Join(tmp, stateName))
	if !ok || st.Addr != other || st.Self != os.Getpid() || st.Child != 0 {
		t.Errorf("estado = %+v (%v); quiero el de %s compilando (Self propio, Child 0)", st, ok, other)
	}
	if got := entries(t, tmp); len(got) != 2 {
		t.Errorf("en la carpeta hay %v; quiero solo la copia y el estado de la que corre", got)
	}
	msg, err := stop(config{tmp: tmp, stopWait: 15 * time.Second})
	if err != nil || msg != "Vista previa cortada y copia temporal borrada" {
		t.Errorf("stop = %q, %v", msg, err)
	}
	select {
	case r := <-results:
		pending--
		if r.err != nil {
			t.Errorf("la que corría (%s) terminó con error: %v", r.addr, r.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("la que corría no terminó después de -cortar")
	}
	if got := entries(t, tmp); len(got) != 0 {
		t.Errorf("en la carpeta quedó %v", got)
	}
}

// TestStopReusedPID: si el PID del estado es de un proceso que no es el dashboard (su
// directorio de trabajo no es la copia, como un PID reasignado), -cortar no le manda
// nada, no espera el plazo y limpia la copia; y la próxima vista previa no queda
// bloqueada por ese estado.
func TestStopReusedPID(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("verificar el dueño de un PID solo se puede en Linux (/proc)")
	}
	t.Run("-cortar", func(t *testing.T) {
		tmp := t.TempDir()
		dir := newCopy(t, tmp)
		waited, pid := helperProcessIn(t, "")
		state := putState(t, tmp, previewState{Dir: dir, Addr: "localhost:8081", Child: pid, Self: deadPID(t)})
		start := time.Now()
		msg, err := stop(config{tmp: tmp, stopWait: 3 * time.Second})
		took := time.Since(start)
		if want := "La vista previa ya no estaba corriendo: borré su copia temporal"; err != nil || msg != want {
			t.Errorf("stop = %q, %v; quiero %q", msg, err, want)
		}
		if took > 2*time.Second {
			t.Errorf("stop tardó %s: esperó el plazo con un estado muerto", took.Round(100*time.Millisecond))
		}
		stillAlive(t, waited, pid)
		for _, p := range []string{dir, state} {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Errorf("quedó %s", p)
			}
		}
	})

	t.Run("la próxima vista previa", func(t *testing.T) {
		tmp := t.TempDir()
		dir := newCopy(t, tmp)
		waited, pid := helperProcessIn(t, "")
		putState(t, tmp, previewState{Dir: dir, Addr: "localhost:9999", Child: pid, Self: pid})
		p, err := prepare(config{Repo: repoRoot(t), Datos: filepath.Join(tmp, "nada.jsonl"), Addr: freeAddr(t), tmp: tmp, out: io.Discard})
		if err != nil {
			t.Fatalf("un PID reasignado no tiene que bloquear la vista previa: %v", err)
		}
		defer p.cleanup()
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("no borró la copia vieja %s", dir)
		}
		stillAlive(t, waited, pid)
	})
}

// TestCreateStateExclusive: el lock se crea una sola vez; el segundo intento falla con
// "ya existe", no cambia el primero y no deja temporales.
func TestCreateStateExclusive(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, stateName)
	first := previewState{Dir: filepath.Join(tmp, dirPrefix+"a"), Addr: "localhost:8081", Self: 100}
	if err := createState(path, first); err != nil {
		t.Fatal(err)
	}
	err := createState(path, previewState{Dir: filepath.Join(tmp, dirPrefix+"b"), Addr: "localhost:8082", Self: 200})
	if !errors.Is(err, fs.ErrExist) {
		t.Errorf("segundo createState = %v; quiero que ya exista", err)
	}
	if got, ok := readState(path); !ok || got != first {
		t.Errorf("el estado cambió: %+v", got)
	}
	if got := strings.Join(entries(t, tmp), ","); got != stateName {
		t.Errorf("en la carpeta quedó %s, quiero solo %s", got, stateName)
	}
	if info, err := os.Stat(path); err == nil && runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("permisos del estado %v, quiero 0600", info.Mode().Perm())
	}
}

// TestSweepSkipsLinksAndFiles: el barrido solo borra carpetas de verdad; un archivo o un
// enlace con el prefijo se dejan donde están (y el destino del enlace no se toca).
func TestSweepSkipsLinksAndFiles(t *testing.T) {
	tmp, outside := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(outside, "importante"), "x")
	writeFile(t, filepath.Join(tmp, dirPrefix+"archivo"), "x")
	linked := filepath.Join(tmp, dirPrefix+"enlace")
	if err := os.Symlink(outside, linked); err != nil {
		t.Skip(err)
	}
	orphan := newCopy(t, tmp)
	if n := sweep(tmp, filepath.Join(tmp, dirPrefix+"propia")); n != 1 {
		t.Errorf("sweep borró %d, quiero 1 (solo la carpeta huérfana)", n)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("no barrió %s", orphan)
	}
	for _, p := range []string{filepath.Join(tmp, dirPrefix+"archivo"), filepath.Join(outside, "importante")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("borró %s: %v", p, err)
		}
	}
	if _, err := os.Lstat(linked); err != nil {
		t.Errorf("borró el enlace %s: %v", linked, err)
	}
}

// TestStopDoesNotKillUnverifiedChild: si la vista previa misma sigue viva (la carpeta
// está en uso por su PID) pero el PID del dashboard del estado no usa la copia (PID
// reasignado), -cortar no mata a ese proceso ajeno.
func TestStopDoesNotKillUnverifiedChild(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("verificar el dueño de un PID solo se puede en Linux (/proc)")
	}
	tmp := t.TempDir()
	dir := newCopy(t, tmp)
	_, self := helperProcessIn(t, dir) // vive y tiene la copia como directorio de trabajo
	stranger, child := helperProcessIn(t, "")
	putState(t, tmp, previewState{Dir: dir, Addr: "localhost:8081", Child: child, Self: self})
	_, err := stop(config{tmp: tmp, stopWait: 400 * time.Millisecond})
	if err == nil {
		t.Errorf("stop sin error; quiero que avise que no cortó nada")
	}
	stillAlive(t, stranger, child)
}
