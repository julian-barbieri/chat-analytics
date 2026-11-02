package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Borrado de tareas en pr-abierto desde el tablero (ADR 0004). Cada caso arma su propio
// factory/ en una carpeta temporal; nada toca el factory/ real.

// delNow es la hora fija de estos tests: la ventana de actividad se mide contra ella.
var delNow = time.Date(2026, 11, 2, 15, 0, 0, 0, time.UTC)

func evLine(t *testing.T, at time.Time, sesion, agentID, evento, resumen string) string {
	t.Helper()
	data, err := json.Marshal(map[string]string{
		"at": at.Format(time.RFC3339), "evento": evento, "sesion": sesion,
		"agent_id": agentID, "agente": map[bool]string{true: "reviewer"}[agentID != ""],
		"tool": "Write", "resumen": resumen,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

// delRepo: 003-c y 004-d en pr-abierto, 001-a esperando aprobación, y eventos viejos
// (hace una hora) de 003-c y 004-d.
func delRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "static/htmx.min.js", "/* htmx */")
	writeFile(t, root, "factory/jobs/001-a/job.json", `{"etapa":"esperando-aprobacion"}`)
	writeFile(t, root, "factory/jobs/003-c/job.json", prJob("003-c", "https://github.com/a/b/pull/3"))
	writeFile(t, root, "factory/jobs/003-c/triage.md", "causa raíz")
	writeFile(t, root, "factory/jobs/004-d/job.json", prJob("004-d", "https://github.com/a/b/pull/4"))
	writeFile(t, root, "factory/events/2026-11-02.jsonl",
		evLine(t, delNow.Add(-time.Hour), "s1", "", "PreToolUse", "factory/jobs/003-c/job.json.tmp")+
			evLine(t, delNow.Add(-time.Hour), "s2", "", "PreToolUse", "factory/jobs/004-d/job.json.tmp"))
	return root
}

func delServer(t *testing.T, root string, removeAll func(string) error) (*Board, *httptest.Server) {
	t.Helper()
	b := testBoard(t, root)
	b.now = func() time.Time { return delNow }
	if removeAll != nil {
		b.removeAll = removeAll
	}
	srv := httptest.NewServer(b.Routes())
	t.Cleanup(srv.Close)
	return b, srv
}

// htmxHdr son los headers que manda htmx desde la página del tablero.
var htmxHdr = map[string]string{"HX-Request": "true"}

// oobRe: el aviso viaja en un bloque fuera de banda que htmx pone en #aviso, fuera de
// #tablero, porque #tablero se reemplaza cada 2 s y el aviso desaparecería.
var oobRe = regexp.MustCompile(`(?s)<div id="aviso" hx-swap-oob="innerHTML">(.*?)</div>`)

// splitNotice separa la respuesta del POST en el aviso fuera de banda y el tablero. ok
// es false si no vino el bloque o si vino más de uno.
func splitNotice(body string) (notice, board string, ok bool) {
	m := oobRe.FindAllStringSubmatch(body, -1)
	if len(m) != 1 {
		return "", body, false
	}
	return m[0][1], oobRe.ReplaceAllString(body, ""), true
}

func post(t *testing.T, srv *httptest.Server, path, host string, hdr map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest("POST", srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(body)
}

func exists(root, path string) bool {
	_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path)))
	return err == nil
}

// sameTree compara dos fotos de snapTree salvo lo que esté en skip (y lo de adentro).
func sameTree(t *testing.T, root string, before, after map[string]fileSnap, skip ...string) {
	t.Helper()
	skipped := func(p string) bool {
		for _, s := range skip {
			s = filepath.Join(root, filepath.FromSlash(s))
			if p == s || strings.HasPrefix(p, s+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	for p, s := range before {
		if skipped(p) {
			continue
		}
		a, ok := after[p]
		switch {
		case !ok:
			t.Errorf("desapareció %s", p)
		case string(a.data) != string(s.data) || !a.mod.Equal(s.mod) || a.mode != s.mode:
			t.Errorf("%s cambió", p)
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok && !skipped(p) {
			t.Errorf("apareció %s", p)
		}
	}
}

// TestDeleteJob: CA1 a CA5 y CA9.
func TestDeleteJob(t *testing.T) {
	t.Run("feliz", func(t *testing.T) {
		root := delRepo(t)
		_, srv := delServer(t, root, nil)
		before := snapTree(t, root)
		code, body := post(t, srv, "/tarea/003-c/eliminar", "", htmxHdr)
		if code != 200 {
			t.Fatalf("POST = %d, quiero 200: %s", code, body)
		}
		if strings.Contains(body, `href="/tarea/003-c"`) || !strings.Contains(body, `href="/tarea/004-d"`) {
			t.Errorf("el tablero devuelto tiene que traer 004-d y no 003-c:\n%s", body)
		}
		// El aviso de un borrado anterior se reemplaza por la confirmación.
		if notice, _, ok := splitNotice(body); !ok || !strings.Contains(notice, `<p class="note ok">Se borró la tarea 003-c.</p>`) {
			t.Errorf("quiero la confirmación en el bloque fuera de banda:\n%s", body)
		}
		for _, p := range []string{"factory/jobs/003-c", "factory/jobs/.borrando-003-c"} {
			if exists(root, p) {
				t.Errorf("quedó %s", p)
			}
		}
		// Solo cambian la carpeta borrada y la fecha de factory/jobs (el rename la toca).
		sameTree(t, root, before, snapTree(t, root), "factory/jobs/003-c", "factory/jobs")
		if !exists(root, "factory/jobs/004-d/job.json") || !exists(root, "factory/jobs/001-a/job.json") {
			t.Error("se borró otra tarea")
		}

		// CA9: el feed sigue mostrando los eventos de 003-c, pero sin link (daría 404).
		res, err := http.Get(srv.URL + "/parcial/actividad")
		if err != nil {
			t.Fatal(err)
		}
		feed, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if strings.Contains(string(feed), `href="/tarea/003-c"`) || !strings.Contains(string(feed), "003-c") {
			t.Errorf("el feed tiene que nombrar 003-c sin link:\n%s", feed)
		}
		if !strings.Contains(string(feed), `href="/tarea/004-d"`) {
			t.Errorf("004-d sigue existiendo y tiene que tener link:\n%s", feed)
		}
	})

	t.Run("mergeado", func(t *testing.T) {
		root := delRepo(t)
		b, srv := delServer(t, root, nil)
		b.prQuery = func(ctx context.Context, url string) (PRStatus, error) {
			return PRStatus{State: "MERGED", MergedAt: "2026-11-01T10:00:00Z"}, nil
		}
		b.checkPRs(context.Background(), []string{"https://github.com/a/b/pull/3"}, delNow)
		board := get(t, b.Routes(), "/parcial/tablero")
		_, merged, _ := strings.Cut(board, `aria-label="Mergeado"`)
		merged, _, _ = strings.Cut(merged, "</section>")
		if !strings.Contains(merged, `hx-post="/tarea/003-c/eliminar"`) {
			t.Fatalf("003-c tiene que estar en Mergeado con el tacho:\n%s", merged)
		}
		if code, body := post(t, srv, "/tarea/003-c/eliminar", "", htmxHdr); code != 200 || exists(root, "factory/jobs/003-c") {
			t.Errorf("POST = %d, quiero 200 y la carpeta borrada: %s", code, body)
		}
	})

	// CA2: 409 con el motivo y nada cambia.
	conflicts := []struct {
		name, id, why string
		setup         func(root string)
	}{
		{"esperando aprobación", "001-a", "solo se borran tareas con el PR abierto", nil},
		{"descartada", "002-b", "solo se borran tareas con el PR abierto", func(root string) {
			writeFile(t, root, "factory/jobs/002-b/job.json", `{"etapa":"descartada"}`)
		}},
		{"sin job.json", "005-e", "todavía no tiene job.json", func(root string) {
			os.MkdirAll(filepath.Join(root, "factory/jobs/005-e"), 0o755)
		}},
		{"job.json roto", "006-f", "job.json inválido", func(root string) {
			writeFile(t, root, "factory/jobs/006-f/job.json", `{"etapa": `)
		}},
		{"etapa desconocida", "007-g", "etapa desconocida", func(root string) {
			writeFile(t, root, "factory/jobs/007-g/job.json", `{"etapa":"mergeado","pr":"https://github.com/a/b/pull/7"}`)
		}},
		{"actividad reciente", "003-c", "esperá a que termine", func(root string) {
			writeFile(t, root, "factory/events/2026-11-02.jsonl",
				evLine(t, delNow.Add(-30*time.Second), "s1", "", "PreToolUse", "factory/jobs/003-c/job.json"))
		}},
		{"subagente activo", "003-c", "esperá a que termine", func(root string) {
			writeFile(t, root, "factory/events/2026-11-02.jsonl",
				evLine(t, delNow.Add(-15*time.Minute), "s1", "", "PreToolUse", "factory/jobs/003-c/job.json")+
					evLine(t, delNow.Add(-10*time.Minute), "s1", "x1", "SubagentStart", ""))
		}},
	}
	for _, c := range conflicts {
		t.Run("409 "+c.name, func(t *testing.T) {
			root := delRepo(t)
			if c.setup != nil {
				c.setup(root)
			}
			_, srv := delServer(t, root, nil)
			before := snapTree(t, root)
			code, body := post(t, srv, "/tarea/"+c.id+"/eliminar", "", htmxHdr)
			if code != 409 {
				t.Fatalf("POST = %d, quiero 409: %s", code, body)
			}
			notice, board, ok := splitNotice(body)
			if !ok || !regexp.MustCompile(`<p class="note">No se borró la tarea `+c.id+`:[^<]*`+regexp.QuoteMeta(c.why)).MatchString(notice) {
				t.Errorf("falta el aviso %q en el bloque fuera de banda:\n%s", c.why, body)
			}
			if strings.Contains(board, "No se borró") {
				t.Errorf("el aviso no tiene que ir dentro de #tablero:\n%s", board)
			}
			if !strings.Contains(board, `class="board"`) {
				t.Errorf("el 409 tiene que traer el tablero:\n%s", body)
			}
			sameTree(t, root, before, snapTree(t, root))
		})
	}

	// CA3: pedidos de otros sitios u otros hosts.
	forbidden := []struct {
		name, host string
		hdr        map[string]string
	}{
		{"sin HX-Request", "", nil},
		{"HX-Request falso", "", map[string]string{"HX-Request": "1"}},
		{"Origin ajeno", "", map[string]string{"HX-Request": "true", "Origin": "http://evil.example.com"}},
		{"Origin null", "", map[string]string{"HX-Request": "true", "Origin": "null"}},
		{"Sec-Fetch-Site cross-site", "", map[string]string{"HX-Request": "true", "Sec-Fetch-Site": "cross-site"}},
		{"Sec-Fetch-Site same-site", "", map[string]string{"HX-Request": "true", "Sec-Fetch-Site": "same-site"}},
		{"Host ajeno", "evil.example.com", htmxHdr},
	}
	for _, c := range forbidden {
		t.Run("403 "+c.name, func(t *testing.T) {
			root := delRepo(t)
			_, srv := delServer(t, root, nil)
			before := snapTree(t, root)
			if code, body := post(t, srv, "/tarea/003-c/eliminar", c.host, c.hdr); code != 403 {
				t.Errorf("POST = %d, quiero 403: %s", code, body)
			}
			sameTree(t, root, before, snapTree(t, root))
		})
	}

	t.Run("mismo origen", func(t *testing.T) {
		root := delRepo(t)
		_, srv := delServer(t, root, nil)
		hdr := map[string]string{"HX-Request": "true", "Origin": srv.URL, "Sec-Fetch-Site": "same-origin"}
		if code, body := post(t, srv, "/tarea/003-c/eliminar", "", hdr); code != 200 || exists(root, "factory/jobs/003-c") {
			t.Errorf("POST = %d, quiero 200 y la carpeta borrada: %s", code, body)
		}
	})

	// CA4: IDs que no son tareas.
	t.Run("404", func(t *testing.T) {
		root := delRepo(t)
		writeFile(t, root, "factory/jobs/.borrando-003-c/job.json", prJob("003-c", "https://github.com/a/b/pull/3"))
		writeFile(t, root, "factory/jobs/008-archivo", prJob("008-archivo", ""))
		_, srv := delServer(t, root, func(string) error {
			t.Errorf("no se tiene que barrer en un 404")
			return errors.New("no se tiene que barrer en un 404")
		})
		before := snapTree(t, root)
		for _, id := range []string{"..%2F..%2F.env", ".borrando-003-c", "001-A", "1-a", "999-nada", "008-archivo"} {
			if code, body := post(t, srv, "/tarea/"+id+"/eliminar", "", htmxHdr); code != 404 {
				t.Errorf("POST %s = %d, quiero 404: %s", id, code, body)
			}
		}
		sameTree(t, root, before, snapTree(t, root))
	})

	t.Run("enlaces simbólicos", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("los enlaces simbólicos piden permisos especiales en Windows")
		}
		root := delRepo(t)
		writeFile(t, root, "afuera/job.json", prJob("009-enlace", "https://github.com/a/b/pull/9"))
		writeFile(t, root, "secreto.txt", "no me borres")
		if err := os.Symlink(filepath.Join(root, "afuera"), filepath.Join(root, "factory/jobs/009-enlace")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(root, "secreto.txt"), filepath.Join(root, "factory/jobs/003-c/fuera")); err != nil {
			t.Fatal(err)
		}
		_, srv := delServer(t, root, nil)
		if code, body := post(t, srv, "/tarea/009-enlace/eliminar", "", htmxHdr); code != 404 {
			t.Errorf("tarea que es un enlace = %d, quiero 404: %s", code, body)
		}
		if !exists(root, "afuera/job.json") || !exists(root, "factory/jobs/009-enlace") {
			t.Error("se tocó el enlace o su destino")
		}
		if code, body := post(t, srv, "/tarea/003-c/eliminar", "", htmxHdr); code != 200 {
			t.Fatalf("POST 003-c = %d, quiero 200: %s", code, body)
		}
		if data, err := os.ReadFile(filepath.Join(root, "secreto.txt")); err != nil || string(data) != "no me borres" {
			t.Errorf("se tocó el destino del enlace de adentro: %q, %v", data, err)
		}
	})

	// CA5: el borrado falla después del rename y el próximo POST lo termina.
	t.Run("borrado a medias", func(t *testing.T) {
		root := delRepo(t)
		var calls atomic.Int32
		_, srv := delServer(t, root, func(p string) error {
			if calls.Add(1) == 1 {
				return errors.New("archivo abierto en otro programa")
			}
			return os.RemoveAll(p)
		})
		code, body := post(t, srv, "/tarea/003-c/eliminar", "", htmxHdr)
		if code != 200 {
			t.Fatalf("POST = %d, quiero 200: %s", code, body)
		}
		notice, board, ok := splitNotice(body)
		if !ok || !regexp.MustCompile(`<p class="note">[^<]*factory/jobs/\.borrando-003-c/`).MatchString(notice) || strings.Contains(board, `href="/tarea/003-c"`) {
			t.Errorf("quiero el aviso con factory/jobs/.borrando-003-c/ fuera de #tablero y sin la tarjeta:\n%s", body)
		}
		if strings.Contains(board, "borrando") {
			t.Errorf("el aviso no tiene que ir dentro de #tablero:\n%s", board)
		}
		if exists(root, "factory/jobs/003-c") || !exists(root, "factory/jobs/.borrando-003-c/triage.md") {
			t.Fatal("quiero 003-c fuera del tablero y lo que quedó en .borrando-003-c")
		}
		if code, body := post(t, srv, "/tarea/004-d/eliminar", "", htmxHdr); code != 200 || strings.Contains(body, "borrando") {
			t.Fatalf("POST 004-d = %d, quiero 200 sin aviso de borrado a medias: %s", code, body)
		}
		for _, p := range []string{"factory/jobs/.borrando-003-c", "factory/jobs/004-d", "factory/jobs/.borrando-004-d"} {
			if exists(root, p) {
				t.Errorf("quedó %s", p)
			}
		}
	})

	t.Run("borrado anterior imborrable", func(t *testing.T) {
		root := delRepo(t)
		writeFile(t, root, "factory/jobs/.borrando-003-c/viejo.md", "x")
		_, srv := delServer(t, root, func(p string) error {
			if strings.HasSuffix(p, ".borrando-003-c") {
				return errors.New("acceso denegado")
			}
			return os.RemoveAll(p)
		})
		before := snapTree(t, root)
		code, body := post(t, srv, "/tarea/003-c/eliminar", "", htmxHdr)
		if notice, _, ok := splitNotice(body); code != 409 || !ok || !strings.Contains(notice, "factory/jobs/.borrando-003-c/") {
			t.Errorf("POST = %d, quiero 409 con el aviso fuera de #tablero: %s", code, body)
		}
		sameTree(t, root, before, snapTree(t, root))
	})
}

// TestTrashButton: CA6. El tacho está solo en las tarjetas en pr-abierto.
func TestTrashButton(t *testing.T) {
	body := get(t, testBoard(t, fakeRepo(t)).Routes(), "/parcial/tablero")
	cards := map[string]string{}
	for _, part := range strings.Split(body, "<article")[1:] {
		if m := regexp.MustCompile(`href="/tarea/(\d{3}-[a-z0-9-]+)"`).FindStringSubmatch(part); m != nil {
			cards[m[1]] = part
		}
	}
	want := []string{
		// Con id, htmx 2 le devuelve el foco al botón después de cada refresco del tablero.
		`<button type="button" id="borrar-003-c" class="trash"`,
		`hx-post="/tarea/003-c/eliminar"`,
		`hx-target="#tablero"`,
		`hx-confirm="¿Borrar la tarea 003-c? Se borra su carpeta factory/jobs/003-c/ con todo lo que tiene y NO se puede deshacer. El PR, la rama y la spec no se tocan."`,
		`aria-label="Borrar la tarea 003-c"`,
		`title="Borrar tarea (no se puede deshacer)"`,
		`<svg`, `aria-hidden="true"`, `focusable="false"`, `stroke="currentColor"`,
	}
	for _, w := range want {
		if !strings.Contains(cards["003-c"], w) {
			t.Errorf("la tarjeta de 003-c no tiene %q:\n%s", w, cards["003-c"])
		}
	}
	for _, id := range []string{"001-a", "002-b"} {
		if c, ok := cards[id]; !ok || strings.Contains(c, "hx-post") || strings.Contains(c, "<svg") {
			t.Errorf("la tarjeta de %s no tiene que tener el tacho (¿está?: %v):\n%s", id, ok, c)
		}
	}
	// Las tareas rotas no son tarjetas y tampoco lo tienen.
	root := t.TempDir()
	writeFile(t, root, "static/htmx.min.js", "/* htmx */")
	writeFile(t, root, "factory/jobs/001-roto/job.json", `{"etapa":"pr-abierto",`)
	if b := get(t, testBoard(t, root).Routes(), "/parcial/tablero"); strings.Contains(b, "hx-post") {
		t.Errorf("una tarea rota no tiene que tener el tacho:\n%s", b)
	}
}

// TestDeleteNotice: el aviso del borrado vive en #aviso, fuera de #tablero (que htmx
// reemplaza cada 2 s), y es una región viva. Los GET del tablero no lo tocan, así el
// aviso queda hasta el próximo borrado.
func TestDeleteNotice(t *testing.T) {
	h := testBoard(t, fakeRepo(t)).Routes()
	page := get(t, h, "/")
	const notice = `<div id="aviso" role="status" aria-live="polite"></div>`
	i, j := strings.Index(page, notice), strings.Index(page, `<div id="tablero"`)
	if i < 0 || j < 0 || i > j {
		t.Errorf("quiero %s antes de #tablero (y no adentro):\n%s", notice, page)
	}
	if b := get(t, h, "/parcial/tablero"); strings.Contains(b, "hx-swap-oob") || strings.Contains(b, `id="aviso"`) {
		t.Errorf("el refresco del tablero no tiene que tocar el aviso:\n%s", b)
	}
}

// TestHtmxConfig: CA7. El meta es JSON válido, igual al default de htmx 2.0.4 más el
// 409 con swap antes de [45]..
func TestHtmxConfig(t *testing.T) {
	page := get(t, testBoard(t, fakeRepo(t)).Routes(), "/")
	m := regexp.MustCompile(`<meta name="htmx-config" content='([^']*)'>`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("falta el meta htmx-config:\n%s", page)
	}
	var cfg struct {
		ResponseHandling []struct {
			Code  string `json:"code"`
			Swap  bool   `json:"swap"`
			Error bool   `json:"error"`
		} `json:"responseHandling"`
	}
	if err := json.Unmarshal([]byte(m[1]), &cfg); err != nil {
		t.Fatalf("el content del meta no es JSON válido: %v\n%s", err, m[1])
	}
	type rule = struct {
		Code  string `json:"code"`
		Swap  bool   `json:"swap"`
		Error bool   `json:"error"`
	}
	want := []rule{{"204", false, false}, {"[23]..", true, false}, {"409", true, true}, {"[45]..", false, true}}
	if len(cfg.ResponseHandling) != len(want) {
		t.Fatalf("responseHandling = %+v, quiero %+v", cfg.ResponseHandling, want)
	}
	for i, w := range want {
		if cfg.ResponseHandling[i] != w {
			t.Errorf("regla %d = %+v, quiero %+v", i, cfg.ResponseHandling[i], w)
		}
	}
}
