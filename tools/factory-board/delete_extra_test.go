package main

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Casos extra del borrado (ADR 0004): concurrencia, eventos, etapas y error de rename.

// TestDeleteConcurrent: varios POST a la misma tarea a la vez (con -race). delMu los
// serializa: uno borra (200) y los otros ya no encuentran la carpeta (404).
func TestDeleteConcurrent(t *testing.T) {
	root := delRepo(t)
	_, srv := delServer(t, root, nil)
	codes := make([]int, 4)
	errs := make([]error, len(codes))
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		// Sin post(): su t.Fatal no se puede llamar desde otra goroutine.
		go func() {
			defer wg.Done()
			req, err := http.NewRequest("POST", srv.URL+"/tarea/003-c/eliminar", nil)
			if err != nil {
				errs[i] = err
				return
			}
			req.Header.Set("HX-Request", "true")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				errs[i] = err
				return
			}
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			codes[i] = res.StatusCode
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	ok, notFound := 0, 0
	for _, c := range codes {
		switch c {
		case 200:
			ok++
		case 404:
			notFound++
		default:
			t.Errorf("código inesperado %d (todos: %v)", c, codes)
		}
	}
	if ok != 1 || notFound != len(codes)-1 {
		t.Errorf("quiero un 200 y el resto 404, hubo %v", codes)
	}
	if exists(root, "factory/jobs/003-c") || exists(root, "factory/jobs/.borrando-003-c") {
		t.Error("quedó la carpeta o su .borrando")
	}
	if !exists(root, "factory/jobs/004-d/job.json") {
		t.Error("se borró otra tarea")
	}
}

// TestDeleteKeepsEvents: borrar no toca factory/events/ (CA1), ni siquiera sus fechas.
func TestDeleteKeepsEvents(t *testing.T) {
	root := delRepo(t)
	_, srv := delServer(t, root, nil)
	before := snapTree(t, root)
	if code, body := post(t, srv, "/tarea/003-c/eliminar", "", htmxHdr); code != 200 {
		t.Fatalf("POST = %d: %s", code, body)
	}
	after := snapTree(t, root)
	n := 0
	for p, s := range before {
		if !strings.Contains(filepath.ToSlash(p), "factory/events") {
			continue
		}
		n++
		a, ok := after[p]
		if !ok || string(a.data) != string(s.data) || !a.mod.Equal(s.mod) {
			t.Errorf("cambió %s", p)
		}
	}
	if n == 0 {
		t.Fatal("el test no vio ningún archivo de eventos")
	}
}

// TestTrashButtonOnlyPR: ninguna etapa distinta de pr-abierto tiene el tacho, en ninguna
// columna (CA6), y la de pr-abierto sí.
func TestTrashButtonOnlyPR(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "static/htmx.min.js", "/* htmx */")
	for i, s := range stages {
		id := "00" + string(rune('1'+i)) + "-x"
		job := `{"etapa":"` + s.Key + `"}`
		if s.Key == "pr-abierto" {
			job = prJob(id, "https://github.com/a/b/pull/1")
		}
		writeFile(t, root, "factory/jobs/"+id+"/job.json", job)
	}
	body := get(t, testBoard(t, root).Routes(), "/parcial/tablero")
	if n := strings.Count(body, `hx-post="/tarea/`); n != 1 {
		t.Errorf("hay %d botones, quiero 1 (solo pr-abierto):\n%s", n, body)
	}
	if !regexp.MustCompile(`hx-post="/tarea/00\d-x/eliminar"`).MatchString(body) {
		t.Error("falta el botón de la tarea en pr-abierto")
	}
}

// TestDeleteRenameFails: si os.Rename falla se responde 409 y no cambia nada (CA2/5).
// Se provoca sin hacks sacando el permiso de escritura de factory/jobs/.
func TestDeleteRenameFails(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("chmod no impide renombrar en Windows ni siendo root")
	}
	root := delRepo(t)
	jobs := filepath.Join(root, "factory/jobs")
	_, srv := delServer(t, root, nil)
	before := snapTree(t, root)
	if err := os.Chmod(jobs, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(jobs, 0o755) })
	code, body := post(t, srv, "/tarea/003-c/eliminar", "", htmxHdr)
	os.Chmod(jobs, 0o755)
	notice, _, ok := splitNotice(body)
	if code != 409 || !ok || !strings.Contains(notice, "No se borró la tarea 003-c") {
		t.Errorf("POST = %d, quiero 409 con aviso: %s", code, body)
	}
	// El aviso trae el motivo del sistema operativo, sin la ruta que lo tapaba al recortar.
	if !strings.Contains(notice, "permission denied") || strings.Contains(notice, "rename") || strings.Contains(notice, root) {
		t.Errorf("quiero solo el motivo del rename en el aviso: %q", notice)
	}
	if !strings.Contains(body, `href="/tarea/003-c"`) {
		t.Error("la tarea tiene que seguir en el tablero")
	}
	sameTree(t, root, before, snapTree(t, root))
}
