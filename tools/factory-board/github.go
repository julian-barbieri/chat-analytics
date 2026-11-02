package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// PRStatus es lo que interesa de un PR en GitHub: OPEN, CLOSED o MERGED, y cuándo se
// mergeó (RFC 3339).
type PRStatus struct {
	State    string `json:"state"`
	MergedAt string `json:"mergedAt"`
}

// prEntry es una consulta guardada en la caché.
type prEntry struct {
	Status  PRStatus
	Err     string
	Checked time.Time
}

const (
	// prEvery: cada cuánto se vuelve a preguntar por un PR no mergeado. htmx pide el
	// tablero cada 2 s: sin esto serían 30 llamadas a gh por minuto por PR.
	prEvery   = 2 * time.Minute
	prTimeout = 15 * time.Second
)

// prURLRe: solo URLs de PR de GitHub. Se valida antes de llamar a gh para que un "pr"
// como "--web" o "-R otro/repo" no le llegue como flag.
var prURLRe = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/pull/[0-9]+$`)

// ghPRStatus consulta el PR con la CLI gh, que ya tiene la sesión de la persona. Solo
// lee: gh pr view no cambia nada en GitHub.
func ghPRStatus(ctx context.Context, url string) (PRStatus, error) {
	if !prURLRe.MatchString(url) {
		return PRStatus{}, errors.New("PR sin URL de GitHub válida")
	}
	ctx, cancel := context.WithTimeout(ctx, prTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", url, "--json", "state,mergedAt")
	// Sin prompts ni avisos de actualización: el tablero no tiene a quién preguntarle.
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1")
	// Si gh deja un hijo con stderr abierto, que Wait no espere para siempre.
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return PRStatus{}, errors.New("falta la CLI gh (instalala y corré gh auth login)")
	case ctx.Err() != nil:
		return PRStatus{}, fmt.Errorf("gh no respondió en %s", prTimeout)
	case err != nil:
		line, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n")
		if line == "" {
			line = err.Error()
		}
		return PRStatus{}, errors.New(cut(line, 120))
	}
	var st PRStatus
	if err := json.Unmarshal(stdout.Bytes(), &st); err != nil || st.State == "" {
		return PRStatus{}, errors.New("respuesta inesperada de gh")
	}
	return st, nil
}

func cut(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// prStates devuelve lo que haya en la caché para esas URLs y, si alguna venció y no
// hay otra ronda en vuelo, lanza una sola en segundo plano: ningún pedido htmx espera
// a gh. Un PR mergeado no vence nunca.
func (b *Board) prStates(urls []string, now time.Time) map[string]prEntry {
	b.prMu.Lock()
	defer b.prMu.Unlock()
	out := map[string]prEntry{}
	var stale []string
	for _, u := range urls {
		if !prURLRe.MatchString(u) {
			continue
		}
		e, ok := b.prs[u]
		if ok {
			out[u] = e
		}
		if !ok || e.Status.State != "MERGED" && now.Sub(e.Checked) >= prEvery {
			stale = append(stale, u)
		}
	}
	if len(stale) > 0 && !b.prBusy {
		b.prBusy = true
		b.prWG.Add(1)
		go func() {
			defer b.prWG.Done()
			b.checkPRs(context.Background(), stale, now)
			b.prMu.Lock()
			b.prBusy = false
			b.prMu.Unlock()
		}()
	}
	return out
}

// checkPRs consulta las URLs de a una y guarda el resultado. La usa la ronda de fondo;
// los tests la llaman directo para dejar la caché lista sin esperas.
func (b *Board) checkPRs(ctx context.Context, urls []string, now time.Time) {
	for _, u := range urls {
		if !prURLRe.MatchString(u) {
			continue
		}
		st, err := b.prQuery(ctx, u)
		e := prEntry{Status: st, Checked: now}
		if err != nil {
			e = prEntry{Err: err.Error(), Checked: now}
		}
		b.prMu.Lock()
		b.prs[u] = e
		b.prMu.Unlock()
	}
}

// applyPR ubica una tarea en pr-abierto según su PR: si se mergeó pasa a la columna
// derivada "Mergeado"; si no, queda donde está con un aviso cuando hace falta.
func applyPR(c *Card, prs map[string]prEntry) {
	if c.Etapa != "pr-abierto" {
		return
	}
	e, ok := prs[c.PR]
	switch {
	case !prURLRe.MatchString(c.PR):
		c.PRNote = "PR sin URL de GitHub válida"
	case !ok:
		c.PRNote = "Consultando GitHub…"
	case e.Err != "":
		c.PRNote = "No se pudo consultar GitHub: " + e.Err
	case e.Status.State == "MERGED":
		c.Stage, c.MergedAt = mergedStage, e.Status.MergedAt
	case e.Status.State == "CLOSED":
		c.PRNote = "El PR se cerró sin merge"
	case e.Status.State != "OPEN":
		// DRAFT, minúsculas o un cambio de formato de gh: que se vea en vez de pasar por
		// "PR abierto" sin aviso. El template escapa lo que venga.
		c.PRNote = "Estado desconocido en GitHub: " + cut(e.Status.State, 40)
	}
}
