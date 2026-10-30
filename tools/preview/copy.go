package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // la cuota se cuenta por día del Pacífico, también en Windows
)

const (
	baseName      = "yt-monitor-vista-previa"      // carpeta privada en el cache del usuario
	dirPrefix     = "yt-monitor-vista-previa-"     // carpeta de cada copia, dentro de la privada
	stateName     = "yt-monitor-vista-previa.json" // archivo de estado, al lado
	stopName      = "cortar"                       // aviso de corte que deja -cortar en la copia
	defaultBudget = 9000                           // -cuota por defecto del dashboard
)

// defaultTmp es la carpeta de las copias y del archivo de estado: una propia en el cache
// del usuario con permisos 0700, y no la temporal compartida, donde otro usuario de la
// máquina podría plantar enlaces o estados falsos (ADR 0003).
func defaultTmp() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("no encontré la carpeta de cache del usuario: %v", err)
	}
	dir := filepath.Join(cache, baseName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// Un enlace en su lugar haría que el Chmod y las copias vayan a otro lado.
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("%s no es una carpeta común: no la uso", dir)
	}
	// MkdirAll no cambia los permisos de una que ya existía.
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// previewState es el archivo de estado: dónde está la copia y quién la usa, para que
// -cortar y la próxima vista previa sepan qué limpiar. También es el lock: se crea en
// exclusiva apenas existe la copia, con Child en 0 mientras compila.
type previewState struct {
	Dir, Addr   string
	Child, Self int // PID del dashboard (0 hasta que arranca) y de la vista previa
}

// prepared es la copia temporal lista para levantar el dashboard.
type prepared struct {
	dir, addr, tmp string
	budget         int // -cuota para el dashboard; 0 = el de siempre
	fake           *fakeAPI
	log            *log.Logger
	// hold mantiene abierta la carpeta de la copia: en Linux es lo que prueba que este
	// proceso es la vista previa de ese estado (ownsDir), aunque su PID se reasigne.
	hold *os.File
}

// normalizeAddr acepta solo direcciones de loopback: el dashboard no tiene
// autenticación y la copia tiene datos reales.
func normalizeAddr(s string) (string, error) {
	if strings.HasPrefix(s, ":") {
		s = "localhost" + s
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return "", fmt.Errorf("dirección inválida %q (usá localhost:8081)", s)
	}
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return "", fmt.Errorf("%q no es local: la vista previa solo escucha en localhost, 127.0.0.1 o ::1", s)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("puerto inválido en %q", s)
	}
	return net.JoinHostPort(host, port), nil
}

// prepare valida todo y arma la copia, en este orden: dirección, repo, puerto libre,
// otra vista previa viva, estado de una corrida muerta, carpeta 0700, estado tomado como
// lock, copias huérfanas, archivos de datos, cuota y API simulada. Si algo de lo primero
// falla, sale sin tocar nada.
func prepare(cfg config) (*prepared, error) {
	lg := log.New(cfg.out, "", 0)
	addr, err := normalizeAddr(cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("-addr: %v", err)
	}
	for _, f := range []string{"go.mod", "main.go"} {
		if _, err := os.Stat(filepath.Join(cfg.Repo, f)); err != nil {
			return nil, fmt.Errorf("-repo %q no parece la raíz de yt-monitor: falta %s", cfg.Repo, f)
		}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("ya hay algo escuchando en %s: si es una vista previa vieja, cortala con -cortar", addr)
	}
	ln.Close()

	// Una vista previa viva en otra dirección no ocupa este puerto: si se tomara su
	// estado como resto, se borraría su copia y -cortar ya no la encontraría.
	statePath := filepath.Join(cfg.tmp, stateName)
	if b, err := os.ReadFile(statePath); err == nil {
		if old := parseState(b); old.valid(cfg.tmp) && old.alive() {
			return nil, fmt.Errorf("ya hay una vista previa corriendo en %s; cortala con -cortar", old.Addr)
		}
		// Si no, es de una vista previa que murió sin limpiar (o no es de una vista
		// previa). Solo se borra si sigue igual: otra que arranca a la vez pudo haberlo
		// reemplazado recién por su lock. Su copia la barre sweep más abajo.
		if now, err := os.ReadFile(statePath); err == nil && bytes.Equal(now, b) {
			os.Remove(statePath)
		}
	}

	dir, err := os.MkdirTemp(cfg.tmp, dirPrefix+"*")
	if err != nil {
		return nil, err
	}
	p := &prepared{dir: dir, addr: addr, tmp: cfg.tmp, log: lg}
	ok := false
	defer func() {
		if !ok {
			p.cleanup()
		}
	}()
	// MkdirTemp ya la crea 0700; se repite por si el sistema hace otra cosa.
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	if p.hold, err = os.Open(dir); err != nil {
		return nil, err
	}
	// El estado se toma ya, antes de compilar: -cortar ve a la vista previa desde acá y
	// una segunda que arranque a la vez no puede tomarlo (crear en exclusiva).
	if err := createState(statePath, previewState{Dir: dir, Addr: addr, Self: os.Getpid()}); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("no pude escribir %s: %v", statePath, err)
		}
		if st, found := readState(statePath); found && st.valid(cfg.tmp) && st.alive() {
			return nil, fmt.Errorf("ya hay una vista previa corriendo en %s; cortala con -cortar", st.Addr)
		}
		return nil, fmt.Errorf("no pude escribir %s: ya existe (si otra vista previa está arrancando, probá de nuevo en unos segundos)", statePath)
	}
	// Con el estado tomado ninguna otra vista previa viva usa esta carpeta: todo lo que
	// tenga el prefijo es una copia huérfana (por ejemplo, de una máquina que se apagó).
	if n := sweep(cfg.tmp, dir); n == 1 {
		lg.Printf("Borré la copia de una vista previa anterior que no terminó bien.")
	} else if n > 1 {
		lg.Printf("Borré %d copias de vistas previas anteriores que no terminaron bien.", n)
	}

	// Mismos nombres que deriva OpenStore, así el dashboard los encuentra en su carpeta.
	base := strings.TrimSuffix(cfg.Datos, ".jsonl")
	for src, dst := range map[string]string{
		cfg.Datos:                    "datos.jsonl",
		base + ".cuota.json":         "datos.cuota.json",
		base + ".espectadores.jsonl": "datos.espectadores.jsonl",
	} {
		copied, err := copyFile(src, filepath.Join(dir, dst))
		if err != nil {
			return nil, fmt.Errorf("no pude copiar %s: %v", src, err)
		}
		if !copied && dst == "datos.jsonl" {
			lg.Printf("aviso: no existe %s: la vista previa arranca sin datos.", cfg.Datos)
		}
	}

	if units, ok := copiedUnits(filepath.Join(dir, "datos.cuota.json"), time.Now()); ok && units >= defaultBudget {
		// Con la cuota copiada en el tope el worker no consultaría videos y no se verían
		// los vivos. Son unidades de mentira: la API es simulada.
		p.budget = units + 1000
		lg.Printf("aviso: la cuota copiada ya está en el tope (%d de %d): la vista previa usa -cuota %d.", units, defaultBudget, p.budget)
	}

	if p.fake, err = loadFake(filepath.Join(dir, "datos.jsonl")); err != nil {
		return nil, fmt.Errorf("no pude leer la copia: %v", err)
	}
	// Solo conteos: nada de mensajes ni autores en el log.
	lg.Printf("Copié %d mensajes: %d vivos y %d videos con comentarios.", p.fake.msgs, len(p.fake.lives), len(p.fake.uploads))
	ok = true
	return p, nil
}

// cleanup borra la copia y el archivo de estado (si es el de esta copia).
func (p *prepared) cleanup() {
	// Primero se suelta la carpeta: en Windows no se puede borrar una abierta.
	if p.hold != nil {
		p.hold.Close()
	}
	removeAll(p.dir)
	statePath := filepath.Join(p.tmp, stateName)
	if st, ok := readState(statePath); ok && filepath.Clean(st.Dir) == filepath.Clean(p.dir) {
		os.Remove(statePath)
	}
}

// removeAll reintenta un poco: en Windows un proceso recién matado puede tener todavía
// abiertos sus archivos.
func removeAll(dir string) error {
	var err error
	for range 10 {
		if err = os.RemoveAll(dir); err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return err
}

// sweep borra las carpetas de copias que hay en tmp salvo keep y devuelve cuántas borró.
// Solo carpetas de verdad (un enlace con el prefijo no se sigue ni se borra) y solo las
// de primer nivel.
func sweep(tmp, keep string) int {
	es, _ := os.ReadDir(tmp)
	n := 0
	for _, e := range es {
		d := filepath.Join(tmp, e.Name())
		if !e.IsDir() || !strings.HasPrefix(e.Name(), dirPrefix) || d == filepath.Clean(keep) {
			continue
		}
		if os.RemoveAll(d) == nil {
			n++
		}
	}
	return n
}

// validDir: solo se borran carpetas de vistas previas dentro de la carpeta de copias,
// aunque el archivo de estado diga otra cosa.
func validDir(tmp, dir string) bool {
	return dir != "" && filepath.Dir(filepath.Clean(dir)) == filepath.Clean(tmp) &&
		strings.HasPrefix(filepath.Base(dir), dirPrefix)
}

func readState(path string) (previewState, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return previewState{}, false
	}
	return parseState(b), true
}

// parseState: uno roto igual cuenta como resto a limpiar, pero sin nada a medio
// decodificar.
func parseState(b []byte) previewState {
	var st previewState
	if json.Unmarshal(b, &st) != nil {
		return previewState{}
	}
	return st
}

// valid: el estado puede ser de una vista previa: carpeta de vista previa y, o el PID
// del dashboard (que no sea este proceso), o Child en 0 (todavía compilando) con el PID
// de la vista previa. Con uno inválido no se mata ni se borra nada.
func (st previewState) valid(tmp string) bool {
	if !validDir(tmp, st.Dir) {
		return false
	}
	if st.Child == 0 {
		return st.Self > 1
	}
	return st.Child > 1 && st.Child != os.Getpid()
}

// alive: sigue corriendo el dashboard o la vista previa que escribió el estado. En Linux
// cuenta solo si ese PID usa la copia (ownsDir), así un PID reasignado a otro proceso no
// la mantiene viva; en otros sistemas alcanza con que el PID exista.
func (st previewState) alive() bool {
	for _, pid := range []int{st.Child, st.Self} {
		if pid <= 1 {
			continue
		}
		if owns, known := ownsDir(pid, st.Dir); known {
			if owns {
				return true
			}
		} else if processAlive(pid) {
			return true
		}
	}
	return false
}

// ownsDir dice si el proceso pid tiene el directorio de trabajo, el ejecutable o algún
// archivo abierto dentro de dir: el dashboard corre ahí y la vista previa tiene la
// carpeta abierta (prepared.hold). Solo se puede saber en Linux, con /proc; en otros
// sistemas known es false.
func ownsDir(pid int, dir string) (owns, known bool) {
	if runtime.GOOS != "linux" {
		return false, false
	}
	if _, err := os.Stat("/proc/self/fd"); err != nil {
		return false, false
	}
	if pid <= 1 || dir == "" {
		return false, true
	}
	// /proc muestra rutas sin enlaces; la copia pudo haberse borrado ya, así que se
	// resuelve la carpeta que la contiene.
	dirs := []string{filepath.Clean(dir)}
	if parent, err := filepath.EvalSymlinks(filepath.Dir(dir)); err == nil {
		dirs = append(dirs, filepath.Join(parent, filepath.Base(dir)))
	}
	base := filepath.Join("/proc", strconv.Itoa(pid))
	links := []string{filepath.Join(base, "cwd"), filepath.Join(base, "exe")}
	if fds, err := os.ReadDir(filepath.Join(base, "fd")); err == nil {
		for _, f := range fds {
			links = append(links, filepath.Join(base, "fd", f.Name()))
		}
	}
	for _, l := range links {
		target, err := os.Readlink(l)
		if err != nil {
			continue
		}
		target = strings.TrimSuffix(target, " (deleted)")
		for _, d := range dirs {
			if target == d || strings.HasPrefix(target, d+string(filepath.Separator)) {
				return true, true
			}
		}
	}
	return false, true
}

// createState crea el estado en exclusiva (el lock): escribe un temporal y lo enlaza con
// el nombre final, que falla si ya existe. Nunca queda un estado a medias.
func createState(path string, st previewState) error {
	tmp, err := writeTemp(path, st)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	return os.Link(tmp, path)
}

// writeState reemplaza el estado (temporal + rename) para que -cortar nunca lea uno a
// medias.
func writeState(path string, st previewState) error {
	tmp, err := writeTemp(path, st)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// writeTemp escribe st en un temporal nuevo al lado de path. CreateTemp usa un nombre al
// azar y lo crea en exclusiva: no sigue un enlace plantado con un nombre fijo.
func writeTemp(path string, st previewState) (string, error) {
	b, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return "", err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// copyFile copia src si existe; el origen se abre solo para leer. Una última línea a
// medio escribir por el dashboard real la descarta OpenStore al leer la copia.
func copyFile(src, dst string) (bool, error) {
	in, err := os.Open(src)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false, err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return false, err
	}
	return true, out.Close()
}

// copiedUnits lee la cuota copiada. Solo cuenta si es del día del Pacífico de hoy: si
// no, el dashboard la pone en cero al arrancar (rollDay).
func copiedUnits(path string, now time.Time) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	var q struct {
		Day   string
		Units int
	}
	if json.Unmarshal(b, &q) != nil {
		return 0, false
	}
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		loc = time.UTC
	}
	return q.Units, q.Day == now.In(loc).Format("2006-01-02")
}
