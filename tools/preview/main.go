// Command preview levanta una vista previa del dashboard de yt-monitor de la rama que
// está en el checkout (ADR 0003): compila el binario, lo corre en localhost:8081 con una
// copia temporal de los datos y una API de YouTube simulada, y borra la copia al
// cortarla. No gasta cuota ni toca datos.jsonl. Lo usa el paso 7b de /factory.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// config es lo que se arma con los flags. Los campos en minúscula los cambian los tests.
type config struct {
	Repo, Datos, Addr string
	tmp               string        // dónde van las copias y el archivo de estado (defaultTmp)
	out               io.Writer     // log de la vista previa y salida del dashboard
	stopWait          time.Duration // cuánto espera -cortar antes de limpiar por su cuenta
	startWait         time.Duration // cuánto se espera a que el dashboard escuche (0 = 30 s)
	ready             func(*running)
}

// running es la vista previa ya levantada (para los tests).
type running struct {
	dir, addr string
	cmd       *exec.Cmd
	fake      *fakeAPI
}

func main() {
	repo := flag.String("repo", ".", "raíz del repo de yt-monitor con la rama que querés ver (tiene que tener go.mod y main.go)")
	datos := flag.String("datos", "datos.jsonl", "ruta al datos.jsonl real; también se copian datos.cuota.json y datos.espectadores.jsonl de la misma carpeta")
	addr := flag.String("addr", "localhost:8081", "dirección de la vista previa: solo localhost, 127.0.0.1 o ::1")
	cut := flag.Bool("cortar", false, "corta la vista previa que esté corriendo y borra su copia temporal")
	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), `Uso:
  go run ./tools/preview [-repo .] [-datos datos.jsonl] [-addr localhost:8081]
  go run ./tools/preview -cortar

Levanta el dashboard de la rama actual con una copia temporal de los datos y una API
simulada: no gasta cuota ni toca los archivos reales. Cortala siempre con -cortar.

Opciones:
`)
		flag.PrintDefaults()
	}
	flag.Parse()
	log.SetFlags(0)

	tmp, err := defaultTmp()
	if err != nil {
		log.Fatalf("error: no pude preparar la carpeta de la vista previa: %v", err)
	}
	cfg := config{Repo: abs(*repo), Datos: abs(*datos), Addr: *addr, tmp: tmp, out: os.Stderr, stopWait: 15 * time.Second}
	if *cut {
		msg, err := stop(cfg)
		if err != nil {
			log.Fatalf("error: %v", err)
		}
		fmt.Println(msg)
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err = run(ctx, cfg)
	cancel()
	if err != nil {
		log.Fatalf("error: %v", err)
	}
}

func abs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// run arma la copia, compila y levanta el dashboard, y espera hasta que se cancele ctx,
// aparezca el aviso de corte o el dashboard termine solo. En todos los casos mata al
// dashboard, cierra la API simulada y borra la copia y el archivo de estado.
func run(ctx context.Context, cfg config) error {
	cfg.out = &syncWriter{w: cfg.out}
	p, err := prepare(cfg)
	if err != nil {
		return err
	}
	defer p.cleanup()
	lg := p.log

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: p.fake, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	defer srv.Close()

	bin := filepath.Join(p.dir, "yt-monitor-vista-previa")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	stopFile := filepath.Join(p.dir, stopName)
	lg.Printf("Compilando el dashboard de %s…", cfg.Repo)
	if out, err := build(ctx, cfg.Repo, bin, stopFile); err != nil {
		if ctx.Err() != nil || stopRequested(stopFile) {
			lg.Printf("Cortando la vista previa…")
			return nil
		}
		return fmt.Errorf("no pude compilar la rama: %v\n%s", err, out)
	}

	args := []string{"-dashboard", p.addr}
	if p.budget > 0 {
		args = append(args, "-cuota", strconv.Itoa(p.budget))
	}
	cmd := exec.Command(bin, args...)
	// Con el directorio de trabajo en la copia, el dashboard usa esos datos y no
	// encuentra el archivo de la clave del repo.
	cmd.Dir = p.dir
	cmd.Env = childEnv(os.Environ(), "http://"+ln.Addr().String())
	cmd.Stdout, cmd.Stderr = cfg.out, cfg.out
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("no pude arrancar el dashboard: %v", err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		close(done)
	}()
	// Kill y no una señal de interrupción, que no anda en Windows; y Wait antes de
	// borrar, porque en Windows no se puede borrar el .exe de un proceso vivo.
	defer func() {
		cmd.Process.Kill()
		<-done
		if n := p.fake.badKeys.Load(); n > 0 {
			lg.Printf("aviso: la API simulada recibió %d pedidos con otra clave y los rechazó.", n)
		}
	}()

	// El PID del dashboard se anota apenas arranca y no cuando ya escucha: es el que
	// -cortar puede matar de respaldo si la vista previa no responde.
	if err := p.setChild(cmd.Process.Pid); err != nil {
		return err
	}
	startWait := cfg.startWait
	if startWait <= 0 {
		startWait = 30 * time.Second
	}
	// En todos los casos de error vuelve sin esperar al hijo: el defer lo mata y limpia.
	switch waitListening(ctx, p.addr, done, stopFile, startWait) {
	case startCut:
		lg.Printf("Cortando la vista previa…")
		return nil
	case startExited:
		return fmt.Errorf("el dashboard de la vista previa no arrancó: %v", waitErr)
	case startTimeout:
		return fmt.Errorf("el dashboard de la vista previa no empezó a escuchar en %s después de %s", p.addr, startWait)
	}
	lg.Printf("Vista previa en http://%s (datos copiados a las %s; no llegan mensajes nuevos). Cortala con: go run ./tools/preview -cortar",
		p.addr, time.Now().Format("15:04"))
	if cfg.ready != nil {
		cfg.ready(&running{dir: p.dir, addr: p.addr, cmd: cmd, fake: p.fake})
	}

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			lg.Printf("Cortando la vista previa…")
			return nil
		case <-done:
			return fmt.Errorf("el dashboard de la vista previa terminó solo: %v", waitErr)
		case <-tick.C:
			if stopRequested(stopFile) {
				lg.Printf("Llegó el aviso de corte: cortando la vista previa…")
				return nil
			}
		}
	}
}

// Cómo terminó la espera del arranque.
const (
	startListening = iota
	startCut       // contexto cancelado o aviso de corte de -cortar
	startExited    // el dashboard terminó solo
	startTimeout   // vivo pero sin escuchar al vencer el plazo
)

// waitListening espera a que el dashboard acepte conexiones, así el link que se le
// pasa a la persona ya anda. Mientras tanto atiende la cancelación y el aviso de corte.
func waitListening(ctx context.Context, addr string, done <-chan struct{}, stopFile string, wait time.Duration) int {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			c.Close()
			return startListening
		}
		if stopRequested(stopFile) {
			return startCut
		}
		select {
		case <-ctx.Done():
			return startCut
		case <-done:
			return startExited
		case <-time.After(100 * time.Millisecond):
		}
	}
	return startTimeout
}

// build compila la rama en bin. Mientras compila mira el aviso de corte: -cortar ya ve
// a la vista previa (el estado existe desde que hay copia) y no tiene que esperar a go.
func build(ctx context.Context, repo, bin, stopFile string) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if stopRequested(stopFile) {
					cancel()
					return
				}
			}
		}
	}()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, ".")
	cmd.Dir = repo
	// Si go deja un hijo con la salida abierta al cortarlo, que no quede esperándolo.
	cmd.WaitDelay = 2 * time.Second
	return cmd.CombinedOutput()
}

// stopRequested: hay algo con el nombre del aviso de corte en la copia. Lstat y no Stat,
// así un enlace cuenta como aviso sin seguirlo.
func stopRequested(stopFile string) bool {
	_, err := os.Lstat(stopFile)
	return err == nil
}

// setChild anota el PID del dashboard en el estado, que sigue siendo de esta copia
// (cualquier otra vista previa sale al ver el lock).
func (p *prepared) setChild(pid int) error {
	path := filepath.Join(p.tmp, stateName)
	st, ok := readState(path)
	if !ok || filepath.Clean(st.Dir) != filepath.Clean(p.dir) || st.Self != os.Getpid() {
		return fmt.Errorf("el archivo de estado %s ya no es el de esta vista previa: la corto", path)
	}
	st.Child = pid
	if err := writeState(path, st); err != nil {
		return fmt.Errorf("no pude escribir %s: %v", path, err)
	}
	return nil
}

// childEnv saca YT_API_KEY y YT_BASE_URL heredados y pone la clave falsa y la API
// simulada: la vista previa nunca puede llegar a YouTube.
func childEnv(env []string, base string) []string {
	out := make([]string, 0, len(env)+2)
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if strings.EqualFold(k, "YT_API_KEY") || strings.EqualFold(k, "YT_BASE_URL") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "YT_API_KEY="+fakeKey, "YT_BASE_URL="+base)
}

// stop corta la vista previa en curso. No depende de señales: deja el aviso de corte en
// la copia y espera a que la vista previa limpie. Si ya no estaba corriendo, limpia sin
// esperar. Si no limpia a tiempo, mata al dashboard solo si puede comprobar que ese PID
// es el dashboard de la copia (Linux); si no, no mata a nadie y lo informa.
func stop(cfg config) (string, error) {
	const cut = "Vista previa cortada y copia temporal borrada"
	statePath := filepath.Join(cfg.tmp, stateName)
	st, ok := readState(statePath)
	if !ok {
		return "No hay ninguna vista previa corriendo", nil
	}
	if !st.valid(cfg.tmp) {
		// No es de una vista previa: ni señales ni borrados.
		return "", fmt.Errorf("%s no es de una vista previa (carpeta o PID inválidos): no corté nada ni lo toqué", statePath)
	}
	if !st.alive() {
		return cleanStale(statePath, st, "La vista previa ya no estaba corriendo: borré su copia temporal")
	}
	// O_EXCL: si ya hay algo con ese nombre (un aviso anterior o un enlace) no se sigue
	// ni se trunca; la vista previa lo ve igual con Lstat.
	if f, err := os.OpenFile(filepath.Join(st.Dir, stopName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err == nil {
		f.Close()
	}
	deadline := time.Now().Add(cfg.stopWait)
	for time.Now().Before(deadline) {
		if _, err := os.Lstat(statePath); errors.Is(err, os.ErrNotExist) {
			return cut, nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Respaldo: el estado sigue después del aviso. Se relee porque Child pudo pasar de 0
	// al PID del dashboard mientras se esperaba.
	if now, ok := readState(statePath); !ok {
		return cut, nil
	} else if now.valid(cfg.tmp) && filepath.Clean(now.Dir) == filepath.Clean(st.Dir) {
		st = now
	}
	if !st.alive() {
		return cleanStale(statePath, st, "La vista previa terminó sin limpiar: borré su copia temporal")
	}
	if st.Child > 1 && st.Child != os.Getpid() {
		owns, known := ownsDir(st.Child, st.Dir)
		if !known {
			// En Windows (y fuera de Linux) no se puede saber si el PID se reasignó: no se
			// mata. Si la copia se deja borrar, nadie la estaba usando (en Windows no se
			// puede borrar el .exe de un proceso vivo), así un PID reasignado no traba todo.
			if removeAll(st.Dir) == nil {
				os.Remove(statePath)
				return fmt.Sprintf("La vista previa no respondía y su copia no estaba en uso: la borré. No corté el PID %d porque no puedo comprobar que sea su dashboard", st.Child), nil
			}
			return "", fmt.Errorf("la vista previa no respondió al aviso en %s y no puedo comprobar que el PID %d sea su dashboard: no lo corté. Si sigue abierto, cerralo a mano y volvé a correr -cortar", cfg.stopWait, st.Child)
		}
		if owns {
			if proc, err := os.FindProcess(st.Child); err == nil {
				proc.Kill()
			}
			if err := removeAll(st.Dir); err != nil {
				return "", fmt.Errorf("no pude borrar la copia %s: %v", st.Dir, err)
			}
			os.Remove(statePath)
			return fmt.Sprintf("La vista previa no respondía: corté el dashboard (PID %d). %s", st.Child, cut), nil
		}
	}
	// Sigue viva la vista previa misma (por ejemplo, compilando): no se mata a nadie.
	return "", fmt.Errorf("la vista previa (PID %d) no respondió al aviso en %s: no corté nada. Volvé a probar -cortar en un rato o cortala con Ctrl+C", st.Self, cfg.stopWait)
}

// cleanStale borra la copia y el estado de una vista previa que ya no corre.
func cleanStale(statePath string, st previewState, msg string) (string, error) {
	if err := removeAll(st.Dir); err != nil {
		return "", fmt.Errorf("no pude borrar la copia %s: %v", st.Dir, err)
	}
	os.Remove(statePath)
	return msg, nil
}

// syncWriter junta el log propio y la salida del dashboard, que llegan de goroutines
// distintas.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// processAlive dice si pid es de un proceso vivo (que no sea este). En Unix la señal 0
// solo comprueba que exista; EPERM es que existe y es de otro usuario. En Windows
// FindProcess ya falla si no existe, pero no distingue un PID reasignado a otro proceso:
// por eso fuera de Linux -cortar nunca mata de respaldo (ver stop y ownsDir).
func processAlive(pid int) bool {
	if pid <= 1 || pid == os.Getpid() {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	defer p.Release()
	if runtime.GOOS == "windows" {
		return true
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
