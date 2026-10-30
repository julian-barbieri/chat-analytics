// Command factory-board es el tablero de la software factory (ADR 0002 y 0003): muestra
// las tareas de factory/jobs/ y lo que hacen los agentes según factory/events/. Lee
// archivos y consulta con gh (solo lectura) si los PR abiertos se mergearon; nunca
// escribe. Escucha en localhost: no tiene autenticación.
package main

import (
	"embed"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"
)

//go:embed templates/*.html
var templatesFS embed.FS

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"hora":  func(t time.Time) string { return t.Local().Format("15:04:05") },
	"fecha": fecha,
	"hace":  ago,
}).ParseFS(templatesFS, "templates/*.html"))

func main() {
	addr := flag.String("addr", "localhost:8090", "dirección del tablero (dejalo en localhost: no tiene autenticación)")
	repo := flag.String("repo", ".", "raíz del repo, donde están factory/ y static/htmx.min.js")
	flag.Parse()
	if _, err := os.Stat(filepath.Join(*repo, "static", "htmx.min.js")); err != nil {
		log.Fatalf("no encuentro static/htmx.min.js en %q: corré el tablero desde la raíz del repo o pasá -repo", *repo)
	}
	log.Printf("Tablero de la factory en http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, NewBoard(*repo).Routes()))
}

func (b *Board) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", b.page)
	mux.HandleFunc("GET /parcial/tablero", b.boardPartial)
	mux.HandleFunc("GET /parcial/actividad", b.activityPartial)
	mux.HandleFunc("GET /tarea/{id}", b.jobPage)
	// Solo htmx, con ruta fija: servir una carpeta del repo podría exponer otros archivos.
	mux.HandleFunc("GET /static/htmx.min.js", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(b.root, "static", "htmx.min.js"))
	})
	return localOnly(mux)
}

// localOnly rechaza pedidos con un Host que no sea local. Sin autenticación, esto evita
// que una página de internet lea el tablero con DNS rebinding.
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			http.Error(w, "el tablero solo responde en localhost", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
	}
}

func (b *Board) page(w http.ResponseWriter, r *http.Request) {
	render(w, "page.html", nil)
}

// Card es una tarea tal como se muestra en el tablero.
type Card struct {
	Job
	Stage    Stage
	Live     bool // un agente se movió hace poco
	Active   []string
	Last     *Event
	Reviews  []Review
	PRNote   string // aviso sobre el estado del PR en GitHub
	MergedAt string
}

type Review struct{ Name, Verdict, Class string }

type Column struct {
	Stage
	Cards []Card
}

// liveWindow: si no hubo eventos en este tiempo, la tarea no se marca "en curso".
const liveWindow = 2 * time.Minute

func (b *Board) card(j Job, acts map[string]*Activity, prs map[string]prEntry, now time.Time) Card {
	c := Card{Job: j}
	c.Stage, _ = stageOf(j.Etapa)
	applyPR(&c, prs)
	if a := acts[j.ID]; a != nil {
		c.Active, c.Last = a.Active, a.Last
	}
	c.Live = c.Stage.Live && (len(c.Active) > 0 || c.Last != nil && now.Sub(c.Last.At) < liveWindow)
	names := make([]string, 0, len(j.Revisiones))
	for n := range j.Revisiones {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		c.Reviews = append(c.Reviews, Review{Name: n, Verdict: j.Revisiones[n], Class: verdictClass(j.Revisiones[n])})
	}
	return c
}

func verdictClass(v string) string {
	switch v {
	case "APROBADO", "APROBADO CON CAMBIOS MENORES":
		return "ok"
	case "CAMBIOS REQUERIDOS":
		return "crit"
	}
	return "pend"
}

// boardStages son las columnas: las de job.json más "Mergeado" después de "PR abierto".
func boardStages() []Stage {
	i := slices.IndexFunc(stages, func(s Stage) bool { return s.Key == "pr-abierto" })
	return slices.Insert(slices.Clone(stages), i+1, mergedStage)
}

// prURLs junta los PR de las tareas en pr-abierto, las únicas que se consultan.
func prURLs(jobs []Job) []string {
	var urls []string
	for _, j := range jobs {
		if j.Err == "" && j.Etapa == "pr-abierto" {
			urls = append(urls, j.PR)
		}
	}
	return urls
}

// columns arma el tablero. Cada tarjeta va en la columna de su Stage, que puede ser la
// derivada "Mergeado" aunque job.json diga pr-abierto.
func (b *Board) columns(now time.Time) ([]Column, []Job) {
	acts := activities(b.Events(), now)
	all := boardStages()
	cols := make([]Column, len(all))
	for i, s := range all {
		cols[i].Stage = s
	}
	jobs := b.Jobs()
	prs := b.prStates(prURLs(jobs), now)
	var broken []Job
	for _, j := range jobs {
		if j.Err != "" {
			broken = append(broken, j)
			continue
		}
		c := b.card(j, acts, prs, now)
		i := slices.IndexFunc(all, func(s Stage) bool { return s.Key == c.Stage.Key })
		cols[i].Cards = append(cols[i].Cards, c)
	}
	// Las descartadas solo ocupan lugar si hay alguna; "Mergeado" se ve siempre.
	if last := len(cols) - 1; len(cols[last].Cards) == 0 {
		cols = cols[:last]
	}
	return cols, broken
}

func (b *Board) boardPartial(w http.ResponseWriter, r *http.Request) {
	cols, broken := b.columns(b.now())
	render(w, "tablero", map[string]any{"Columns": cols, "Broken": broken})
}

const maxFeed = 80

func (b *Board) activityPartial(w http.ResponseWriter, r *http.Request) {
	job := r.URL.Query().Get("tarea")
	if job != "" && !idRe.MatchString(job) {
		http.NotFound(w, r)
		return
	}
	evs := b.Events()
	var feed []Event
	for i := len(evs) - 1; i >= 0 && len(feed) < maxFeed; i-- {
		if job == "" || evs[i].Tarea == job {
			feed = append(feed, evs[i])
		}
	}
	render(w, "actividad", map[string]any{"Events": feed, "Tarea": job})
}

func (b *Board) jobPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idRe.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	if info, err := os.Stat(filepath.Join(b.jobsDir(), id)); err != nil || !info.IsDir() {
		http.NotFound(w, r)
		return
	}
	j := b.readJob(id)
	files := b.Files(j)
	name := r.URL.Query().Get("archivo")
	var content string
	switch {
	case name != "":
		// Solo nombres de la lista: nada de rutas armadas con lo que manda el navegador.
		ok := slices.Contains(files, name)
		if ok {
			content, ok = b.ReadFile(j, name)
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
	case len(files) > 0:
		// Por defecto el primero; si no se puede leer, la página sale igual.
		name = files[0]
		content, _ = b.ReadFile(j, name)
	}
	now := b.now()
	var prs map[string]prEntry
	if j.Err == "" {
		prs = b.prStates(prURLs([]Job{j}), now)
	}
	render(w, "tarea.html", map[string]any{
		"Card":    b.card(j, activities(b.Events(), now), prs, now),
		"Files":   files,
		"Archivo": name,
		"Content": content,
	})
}

// fecha muestra una fecha de job.json (RFC 3339, como la da date -Iseconds) en hora
// local; si no se entiende, la deja como vino.
func fecha(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.Local().Format("02/01 15:04")
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("hace %d s", int(max(d, 0).Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("hace %d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("hace %d h", int(d.Hours()))
	}
	return t.Local().Format("02/01 15:04")
}
