package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Borrado de tareas en pr-abierto: la única escritura del tablero (ADR 0004).

// trashPrefix: la carpeta se renombra a esto antes de borrarla. No cumple idRe, así que
// Jobs() y el glob de /factory estado ya la ignoran aunque el borrado quede a medias.
const trashPrefix = ".borrando-"

// errNoJob: el id no es una carpeta real de factory/jobs/ (404).
var errNoJob = errors.New("no existe la tarea")

// conflictError: no se borra por un motivo que se le muestra a la persona (409).
type conflictError struct{ msg string }

func (e *conflictError) Error() string { return e.msg }

func conflict(format string, a ...any) error { return &conflictError{fmt.Sprintf(format, a...)} }

// Delete borra factory/jobs/<id>/ si está en pr-abierto y nadie la está tocando. Devuelve
// un aviso si la tarea salió del tablero pero quedaron archivos sin borrar.
func (b *Board) Delete(id string, now time.Time) (string, error) {
	// idRe antes de armar cualquier ruta, como jobPage.
	if !idRe.MatchString(id) {
		return "", errNoJob
	}
	b.delMu.Lock()
	defer b.delMu.Unlock()
	dir := filepath.Join(b.jobsDir(), id)
	// Lstat y no Stat: si la tarea es un enlace, el rename movería algo de afuera.
	if info, err := os.Lstat(dir); err != nil || !info.Mode().IsDir() {
		return "", errNoJob
	}
	j := b.readJob(id)
	switch {
	case j.Err != "":
		return "", conflict("No se borró la tarea %s: %s.", id, j.Err)
	case j.Etapa != "pr-abierto":
		return "", conflict("No se borró la tarea %s: está en %s y solo se borran tareas con el PR abierto.", id, j.Etapa)
	}
	if a := activities(b.Events(), now)[id]; a != nil {
		switch {
		case len(a.Active) > 0:
			return "", conflict("No se borró la tarea %s: tiene un subagente trabajando (%s); esperá a que termine.", id, strings.Join(a.Active, ", "))
		case now.Sub(a.Last.At) < liveWindow:
			return "", conflict("No se borró la tarea %s: un agente la tocó %s; esperá a que termine.", id, agoAt(now, a.Last.At))
		}
	}
	b.sweep()
	trash := filepath.Join(b.jobsDir(), trashPrefix+id)
	// En Linux rename(2) reemplaza una carpeta destino vacía: si quedó una, no se pisa.
	if _, err := os.Lstat(trash); err == nil {
		return "", conflict("No se borró la tarea %s: quedó un borrado anterior sin terminar en factory/jobs/%s%s/; borrala a mano.", id, trashPrefix, id)
	}
	if err := os.Rename(dir, trash); err != nil {
		// Las dos rutas largas del LinkError tapaban el motivo: al aviso va solo el
		// motivo y al log, el error entero.
		log.Printf("tarea %s: no se pudo renombrar: %v", id, err)
		reason := err
		var le *os.LinkError
		if errors.As(err, &le) {
			reason = le.Err
		}
		return "", conflict("No se borró la tarea %s: %s.", id, cut(reason.Error(), 120))
	}
	if err := b.removeAll(trash); err != nil {
		log.Printf("tarea %s: quedaron archivos en %s: %v", id, trash, err)
		return fmt.Sprintf("La tarea %s se sacó del tablero pero quedaron archivos en factory/jobs/%s%s/; se reintenta en el próximo borrado.", id, trashPrefix, id), nil
	}
	return "", nil
}

// sweep reintenta los borrados que quedaron a medias. Va en el POST y no en los GET para
// que mirar el tablero siga sin escribir nada.
func (b *Board) sweep() {
	entries, _ := os.ReadDir(b.jobsDir())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), trashPrefix) && e.IsDir() {
			if err := b.removeAll(filepath.Join(b.jobsDir(), e.Name())); err != nil {
				log.Printf("no se pudo terminar de borrar factory/jobs/%s/: %v", e.Name(), err)
			}
		}
	}
}

// sameOrigin acepta solo pedidos de htmx desde la propia página. Un formulario de otro
// sitio no puede mandar HX-Request, y un fetch con ese header dispara un preflight CORS
// que el tablero no contesta. Origin y Sec-Fetch-Site cubren navegadores que los mandan.
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("HX-Request") != "true" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || u.Host != r.Host {
			return false
		}
	}
	if s := r.Header.Get("Sec-Fetch-Site"); s != "" && s != "same-origin" {
		return false
	}
	return true
}

func (b *Board) deleteJob(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "el borrado solo se acepta desde la página del tablero", http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	now := b.now()
	aviso, err := b.Delete(id, now)
	code, ok := http.StatusOK, false
	var c *conflictError
	switch {
	case errors.Is(err, errNoJob):
		http.NotFound(w, r)
		return
	case errors.As(err, &c):
		code, aviso = http.StatusConflict, c.msg
		log.Print(c.msg)
	case err != nil:
		// Delete solo devuelve los dos de arriba; por las dudas, no se responde 200.
		http.Error(w, "no se pudo borrar la tarea", http.StatusInternalServerError)
		return
	case aviso == "":
		log.Printf("tarea %s borrada", id)
		// La confirmación también reemplaza el aviso de un intento anterior.
		aviso, ok = fmt.Sprintf("Se borró la tarea %s.", id), true
	}
	cols, broken := b.columns(now)
	// El header va antes de WriteHeader: después ya no se puede cambiar.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	render(w, "eliminar", map[string]any{"Columns": cols, "Broken": broken, "Aviso": aviso, "OK": ok})
}
