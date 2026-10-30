package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Stage es una columna del tablero. El orden es el del flujo de /factory.
type Stage struct {
	Key, Label string
	Live       bool // un agente trabaja en esta etapa
	Human      bool // le toca a una persona
}

var stages = []Stage{
	{Key: "triage", Label: "Triage", Live: true},
	{Key: "esperando-aprobacion", Label: "Esperando aprobación", Human: true},
	{Key: "implementacion", Label: "Implementación", Live: true},
	{Key: "tests", Label: "Tests", Live: true},
	{Key: "revision", Label: "Revisión", Live: true},
	{Key: "gates", Label: "Gates", Live: true},
	{Key: "pr-abierto", Label: "PR abierto"},
	{Key: "necesita-persona", Label: "Necesita persona", Human: true},
	{Key: "descartada", Label: "Descartadas"},
}

func stageOf(key string) (Stage, bool) {
	for _, s := range stages {
		if s.Key == key {
			return s, true
		}
	}
	return Stage{}, false
}

var (
	idRe      = regexp.MustCompile(`^\d{3}-[a-z0-9-]+$`)
	jobPathRe = regexp.MustCompile(`factory/jobs/(\d{3}-[a-z0-9-]+)/`)
	specRe    = regexp.MustCompile(`^docs/specs/[A-Za-z0-9._-]+\.md$`)
	dayFileRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\.jsonl$`)
)

// Job es factory/jobs/<id>/job.json. Lo escribe solo el orquestador (/factory); acá
// solo se lee. Las fechas quedan como texto para que una mal escrita no tire la tarea.
type Job struct {
	ID, Tipo, Tarea, Etapa, Rama, Spec, PR, Motivo string
	Vuelta                                         int
	Revisiones                                     map[string]string
	Actualizada                                    string
	Historial                                      []struct{ Etapa, At string }

	Err string `json:"-"` // por qué no se pudo leer bien
}

// Event es una línea de factory/events/AAAA-MM-DD.jsonl (la escribe log-activity.sh).
type Event struct {
	At      time.Time `json:"at"`
	Evento  string    `json:"evento"`
	Sesion  string    `json:"sesion"`
	AgentID string    `json:"agent_id"`
	Agente  string    `json:"agente"`
	Tool    string    `json:"tool"`
	Resumen string    `json:"resumen"`
	Detalle string    `json:"detalle"`

	Tarea string `json:"-"` // la asigna assignJobs
}

// Who es quién hizo el movimiento: el subagente o la sesión principal.
func (e Event) Who() string {
	if e.Agente != "" {
		return e.Agente
	}
	return "orquestador"
}

// Action resume el evento en una o dos palabras.
func (e Event) Action() string {
	switch e.Evento {
	case "SubagentStart":
		return "arrancó"
	case "SubagentStop":
		return "terminó"
	case "PostToolUseFailure":
		return "falló " + e.Tool
	case "Stop":
		return "en pausa"
	}
	return e.Tool
}

// Board lee factory/ del repo. No escribe nada.
type Board struct {
	root string
	now  func() time.Time

	mu    sync.Mutex
	cache map[string]dayFile
}

type dayFile struct {
	size   int64
	mod    time.Time
	events []Event
}

func NewBoard(root string) *Board {
	return &Board{root: root, now: time.Now, cache: map[string]dayFile{}}
}

func (b *Board) jobsDir() string   { return filepath.Join(b.root, "factory", "jobs") }
func (b *Board) eventsDir() string { return filepath.Join(b.root, "factory", "events") }

// Jobs devuelve las tareas ordenadas por ID. Una carpeta con job.json roto o con una
// etapa desconocida igual aparece, con Err, para que se vea el problema.
func (b *Board) Jobs() []Job {
	entries, _ := os.ReadDir(b.jobsDir())
	var jobs []Job
	for _, e := range entries {
		if e.IsDir() && idRe.MatchString(e.Name()) {
			jobs = append(jobs, b.readJob(e.Name()))
		}
	}
	return jobs
}

func (b *Board) readJob(id string) Job {
	j := Job{ID: id}
	data, err := os.ReadFile(filepath.Join(b.jobsDir(), id, "job.json"))
	switch {
	case os.IsNotExist(err):
		j.Err = "todavía no tiene job.json"
		return j
	case err != nil:
		j.Err = "no se pudo leer job.json: " + err.Error()
		return j
	}
	if err := json.Unmarshal(data, &j); err != nil {
		return Job{ID: id, Err: "job.json inválido: " + err.Error()}
	}
	j.ID = id // manda la carpeta, por si el archivo dice otra cosa
	if _, ok := stageOf(j.Etapa); !ok {
		j.Err = fmt.Sprintf("etapa desconocida: %q", j.Etapa)
	}
	return j
}

// Files lista los archivos que se pueden ver de una tarea: la spec (si la ruta es
// válida y está en la rama actual: se commitea en la rama de la tarea) y lo que haya
// en su carpeta. Solo estos nombres se aceptan en ?archivo=.
func (b *Board) Files(j Job) []string {
	var files []string
	if specRe.MatchString(j.Spec) {
		if info, err := os.Stat(filepath.Join(b.root, filepath.FromSlash(j.Spec))); err == nil && info.Mode().IsRegular() {
			files = append(files, "spec")
		}
	}
	entries, _ := os.ReadDir(filepath.Join(b.jobsDir(), j.ID))
	for _, e := range entries {
		n := e.Name()
		if e.Type().IsRegular() && (strings.HasSuffix(n, ".md") || n == "job.json") {
			files = append(files, n)
		}
	}
	return files
}

const maxFileBytes = 256 << 10

// ReadFile devuelve el contenido de uno de los nombres de Files (recortado).
func (b *Board) ReadFile(j Job, name string) (string, bool) {
	path := filepath.Join(b.jobsDir(), j.ID, name)
	if name == "spec" {
		path = filepath.Join(b.root, filepath.FromSlash(j.Spec))
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return "", false
	}
	if len(data) > maxFileBytes {
		return string(data[:maxFileBytes]) + "\n… (recortado)", true
	}
	return string(data), true
}

// Events devuelve los eventos de hoy y ayer en orden, con la tarea asignada. Con dos
// archivos alcanza para seguir lo que pasa y no se relee un mes entero cada 2 s.
func (b *Board) Events() []Event {
	entries, _ := os.ReadDir(b.eventsDir())
	var names []string
	for _, e := range entries {
		if dayFileRe.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) > 2 {
		names = names[len(names)-2:]
	}

	b.mu.Lock()
	var evs []Event
	for _, n := range names {
		evs = append(evs, b.dayEvents(n)...)
	}
	// Los días que ya no se muestran salen del caché, para que no crezca sin límite.
	for n := range b.cache {
		if !slices.Contains(names, n) {
			delete(b.cache, n)
		}
	}
	b.mu.Unlock()

	sort.SliceStable(evs, func(i, k int) bool { return evs[i].At.Before(evs[k].At) })
	assignJobs(evs)
	return evs
}

// dayEvents relee un archivo solo si cambió. Se llama con b.mu tomado.
func (b *Board) dayEvents(name string) []Event {
	path := filepath.Join(b.eventsDir(), name)
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if c, ok := b.cache[name]; ok && c.size == info.Size() && c.mod.Equal(info.ModTime()) {
		return c.events
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var evs []Event
	// bufio.Reader y no Scanner: una línea demasiado larga no corta el resto del día.
	rd := bufio.NewReader(f)
	for {
		line, err := rd.ReadBytes('\n')
		var e Event
		// Una línea rota (por ejemplo, a medio escribir) se saltea.
		if json.Unmarshal(line, &e) == nil && !e.At.IsZero() {
			evs = append(evs, e)
		}
		if err != nil {
			break
		}
	}
	b.cache[name] = dayFile{size: info.Size(), mod: info.ModTime(), events: evs}
	return evs
}

// assignJobs marca cada evento con la última tarea que tocó su sesión: el orquestador
// escribe factory/jobs/<id>/job.json en cada cambio de etapa, así que eso alcanza para
// saber en qué está cada sesión sin que los agentes tengan que anotarlo.
func assignJobs(evs []Event) {
	current := map[string]string{}
	for i := range evs {
		e := &evs[i]
		if e.Sesion == "" {
			continue
		}
		if m := jobPathRe.FindStringSubmatch(e.Resumen); m != nil {
			current[e.Sesion] = m[1]
		}
		e.Tarea = current[e.Sesion]
	}
}

// staleAgent: un subagente sin movimientos en este tiempo se da por terminado aunque no
// haya llegado su SubagentStop (sesión cerrada a la fuerza, por ejemplo). Holgado
// porque solo se registra el inicio de cada herramienta y un go test puede tardar.
const staleAgent = 30 * time.Minute

// Activity es lo que se sabe de una tarea por sus eventos.
type Activity struct {
	Active []string // subagentes trabajando ahora
	Last   *Event   // último movimiento
}

// activities agrupa los eventos por tarea. Un subagente está activo desde su
// SubagentStart hasta su SubagentStop.
func activities(evs []Event, now time.Time) map[string]*Activity {
	type agent struct {
		name, job string
		last      time.Time
	}
	running := map[string]*agent{}
	var order []string
	acts := map[string]*Activity{}
	for i := range evs {
		e := &evs[i]
		if e.Tarea != "" {
			a := acts[e.Tarea]
			if a == nil {
				a = &Activity{}
				acts[e.Tarea] = a
			}
			a.Last = e
		}
		if e.AgentID == "" {
			continue
		}
		switch e.Evento {
		case "SubagentStart":
			running[e.AgentID] = &agent{name: e.Who(), job: e.Tarea, last: e.At}
			order = append(order, e.AgentID)
		case "SubagentStop":
			delete(running, e.AgentID)
		default:
			if r := running[e.AgentID]; r != nil {
				r.last = e.At
				if r.job == "" {
					r.job = e.Tarea
				}
			}
		}
	}
	for _, id := range order {
		r := running[id]
		if r == nil || r.job == "" || now.Sub(r.last) > staleAgent {
			continue
		}
		a := acts[r.job]
		a.Active = append(a.Active, r.name)
		delete(running, id) // un mismo agent_id no se lista dos veces
	}
	return acts
}
