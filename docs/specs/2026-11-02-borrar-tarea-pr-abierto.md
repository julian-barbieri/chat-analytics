# Spec: borrar desde el tablero una tarea con el PR abierto (botón de tacho)

- **Estado:** aprobada
- **Tipo:** mejora
- **Rama:** `feat/borrar-tarea-pr-abierto`
- **Origen:** pedido de Julian Barbieri por `/factory` (tarea `004-borrar-tarea-pr-abierto`),
  textual: "Para el dashboard del factory me gustaria que se pueda eliminar la tarea
  manualmente una vez que ya se encuentre dentro de la columna "PR abierto". El boton
  podria ser un logo de un tacho de basura." Decisiones de la persona sobre el primer
  borrador: borrar de verdad (no archivar); botón en toda tarjeta con `etapa:
  pr-abierto`, también las que figuran en "Mergeado"; sin limpieza automática; actualizar
  `CLAUDE.md`; que un borrado no haga reusar el número de tarea; ADR nuevo.
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-02 ("Sí, aprobada", con borrado definitivo, botón también en Mergeado y CLAUDE.md actualizado)

## Contexto

No es un bug: no hay nada que reproducir. Lo que sigue es cómo está hoy, verificado
leyendo el código en `origin/main` (`10d948d`) con Go 1.26.8; `go build ./...`,
`go vet ./...` y `go test -count=1 ./tools/factory-board/` en verde antes de esta spec.

**Reproducción (estado actual):**

```bash
go test -count=1 -run '^(TestRoutes|TestBoardWritesNothing)$' ./tools/factory-board/
```

```
ok  	github.com/alphamedia/yt-monitor/tools/factory-board
```

`TestRoutes` (`tools/factory-board/board_test.go:121`) exige 405 a todo `POST`, y
`TestBoardWritesNothing` (`tools/factory-board/pr_edges_test.go:366`) exige que el
tablero no cree, cambie ni borre ningún archivo.

**Cómo es el tablero hoy**

- Solo hay rutas `GET` (`tools/factory-board/main.go:41-52`), todas detrás de
  `localOnly` (`main.go:56-68`), que rechaza un `Host` que no sea local (DNS rebinding).
  No hay autenticación y el default es `localhost:8090` (`main.go:31`).
- El tablero es de solo lectura por diseño: `main.go:1-4` ("nunca escribe"),
  `board.go:59` ("`job.json` lo escribe solo el orquestador; acá solo se lee"),
  `board.go:108-109` ("No escribe nada"), ADR 0002 ("Solo lee") y ADR 0003 ("El tablero
  sigue sin escribir nada"). La spec del tablero dejó fuera de alcance "botones que
  ejecuten comandos" (`docs/specs/2026-10-30-tablero-factory.md`, "Fuera de alcance").
- `Jobs()` (`board.go:139-148`) lista solo las carpetas de `factory/jobs/` cuyo nombre
  cumple `idRe` (`^\d{3}-[a-z0-9-]+$`, `board.go:53`): una carpeta que empieza con punto
  ya se ignora. No hay forma de sacar una tarea del tablero salvo borrar la carpeta a
  mano. Las tareas en `pr-abierto` quedan para siempre: las mergeadas pasan a la columna
  derivada "Mergeado" (ADR 0003), que en `job.json` sigue siendo `pr-abierto`.
- La tarjeta es el template `card` (`templates/partials.html:13-33`). El tablero se
  reemplaza entero cada 2 s con `hx-get="/parcial/tablero"` (`templates/page.html`).
- htmx 2.0.4 vendorizado (`static/htmx.min.js`, servido con ruta fija en `main.go:48-50`).
  Ya trae `hx-confirm` (usa el `confirm()` del navegador), manda `HX-Request: true` en
  cada pedido y acepta `<meta name="htmx-config">` con `responseHandling` (default:
  `204` no reemplaza, `[23]..` sí, `[45]..` no). El tablero no tiene JS propio.
- Los eventos (`factory/events/AAAA-MM-DD.jsonl`) son de todas las tareas mezcladas; los
  escribe el hook async `log-activity.sh` en modo append y los borra a los 30 días.
  `assignJobs` (`board.go`) asigna cada evento a una tarea por la ruta
  `factory/jobs/<id>/` en el resumen. El feed enlaza cada evento a `/tarea/<id>`
  (`templates/partials.html:42`).
- El skill numera las tareas con "el número siguiente al más alto que exista" en
  `factory/jobs/` (`.claude/skills/factory/SKILL.md:93-95`), y `rehacer` acepta tareas en
  `pr-abierto` (`SKILL.md:271-280`). Si se borra la tarea de número más alto, la próxima
  tarea recibiría ese mismo `NNN`.

**Motivo:** las tareas terminadas (PR abierto o ya mergeado) se acumulan en el tablero y
la persona quiere sacarlas con un botón, sin ir a la terminal.

## Objetivo

Cada tarjeta cuya etapa en `job.json` es `pr-abierto` (columnas "PR abierto" y
"Mergeado") tiene un botón con un tacho de basura que, después de una confirmación que
dice que no se puede deshacer, borra la carpeta `factory/jobs/<id>/` entera. Es la única
escritura del tablero, solo por `POST` desde la propia página, solo en `localhost`, y no
toca los eventos, la spec, la rama ni el PR. Un número de tarea borrado no se vuelve a
usar mientras haya eventos que lo nombren.

## Decisión: cómo se borra

**Borrado definitivo en dos pasos: renombrar y después `os.RemoveAll`.**

1. `os.Rename(factory/jobs/<id>, factory/jobs/.borrando-<id>)`. Es atómico en el mismo
   volumen (Linux y Windows): la tarea desaparece del tablero de una vez, porque
   `.borrando-<id>` no cumple `idRe` y `Jobs()` ya lo ignora; `/tarea/<id>` da 404 y el
   glob `factory/jobs/*/job.json` de `/factory estado` no ve carpetas con punto. Si el
   rename falla, no cambió nada y se responde 409.
2. `os.RemoveAll(factory/jobs/.borrando-<id>)`. Si falla a mitad (en Windows, por
   ejemplo, un archivo abierto en un editor), lo que quede ya está fuera del tablero y
   no se ve como una tarea rota a medio borrar. Se avisa en la respuesta y en el log, y
   cada borrado siguiente reintenta primero los `factory/jobs/.borrando-*` que hayan
   quedado (barrido).

Por qué así y no `os.RemoveAll` directo: un `RemoveAll` que falla a la mitad deja
`factory/jobs/<id>/` sin `job.json` pero con otros archivos, y el tablero la mostraría
como "todavía no tiene job.json" para siempre. Por qué no a una carpeta temporal del
sistema: `os.Rename` entre volúmenes falla en Windows y en Linux si `/tmp` es otro
sistema de archivos; dentro de `factory/jobs/` siempre es el mismo volumen. El barrido va
en el `POST` y no en los `GET` para que los `GET` sigan sin escribir nada.

`os.RemoveAll` no sigue enlaces simbólicos de adentro: borra el enlace, no su destino. La
carpeta de la tarea en sí tiene que ser una carpeta real (no un enlace), así el rename
nunca mueve algo de afuera de `factory/jobs/`.

**Eventos:** no se tocan. Los archivos del día son de todas las tareas, el hook los
agrega en paralelo y reescribirlos para sacar líneas perdería eventos de otras sesiones.
Se van solos a los 30 días. Mientras tanto: el feed muestra el ID de una tarea borrada
como texto, sin link (daría 404), y el skill no reusa ese número (CA11).

**Spec, rama y PR:** no se tocan. La spec ya está commiteada; el PR sigue en GitHub,
abierto o mergeado. Borrar es sacar el estado de la factory, no deshacer el trabajo.

## Criterios de aceptación

- [ ] CA1: `POST /tarea/{id}/eliminar` con `HX-Request: true`, `Host` local y una tarea
  cuyo `job.json` es válido y tiene `etapa: pr-abierto` responde 200 con el parcial del
  tablero sin esa tarjeta, y después no existe ni `factory/jobs/<id>/` ni
  `factory/jobs/.borrando-<id>/`; las demás tareas y `factory/events/` quedan iguales.
  Vale también para una tarea que el tablero muestra en "Mergeado". — `TestDeleteJob`
  (caso feliz y caso mergeado con `prQuery` falso, comparando con `snapTree`)
- [ ] CA2: no borra si la etapa no es `pr-abierto` (por ejemplo `esperando-aprobacion`,
  `descartada`), si `job.json` falta, está roto o tiene una etapa desconocida, o si la
  tarea tuvo actividad hace menos de `liveWindow` (2 min) o tiene un subagente activo
  según `activities`. En todos esos casos responde **409** con el parcial del tablero
  más un aviso `role="alert"` que dice por qué, y ningún archivo de `factory/` cambia. —
  `TestDeleteJob` (un caso por motivo, con `snapTree` antes y después)
- [ ] CA3: protección contra pedidos de otros sitios (CSRF) y de otros hosts: responde
  **403** y no borra nada si falta `HX-Request: true`, si hay `Origin` y su host no es
  igual a `r.Host`, si hay `Sec-Fetch-Site` distinto de `same-origin`, o si el `Host` no
  es local (`localOnly`). Un `Origin` igual al host local con `Sec-Fetch-Site:
  same-origin` pasa. — `TestDeleteJob` (casos 403) y `TestLocalOnly` extendido con el
  `POST`
- [ ] CA4: un `id` que no cumple `idRe` (por ejemplo `..%2F..%2F.env`, `.borrando-003-c`,
  `001-A`, `1-a`) o que no existe en `factory/jobs/` da **404**; si `factory/jobs/<id>`
  es un enlace simbólico (aunque apunte a una carpeta con un `job.json` en
  `pr-abierto`) o un archivo, también 404 y el destino del enlace queda intacto. Un
  enlace simbólico **dentro** de la carpeta que apunta a un archivo de afuera se borra
  como enlace y el archivo de afuera queda intacto. `GET /tarea/{id}/eliminar` da 405. —
  `TestDeleteJob` (los casos de enlaces se saltean en Windows con `t.Skip`) y
  `TestRoutes` (405)
- [ ] CA5: si el borrado falla después del rename, la tarea ya no aparece, la respuesta
  es 200 con un aviso `role="alert"` que nombra `factory/jobs/.borrando-<id>/`, y el
  próximo `POST` de borrado (de cualquier tarea) reintenta y borra esa carpeta antes de
  seguir. Si ya existe `factory/jobs/.borrando-<id>` y el barrido no la pudo borrar, el
  pedido responde 409 sin tocar `factory/jobs/<id>/`. — `TestDeleteJob` con el borrado
  inyectable (campo `removeAll func(string) error` en `Board`, `os.RemoveAll` por
  defecto) que falla una vez
- [ ] CA6: solo las tarjetas con `Etapa == "pr-abierto"` (columnas "PR abierto" y
  "Mergeado") tienen el botón: un `<button type="button">` con
  `hx-post="/tarea/<id>/eliminar"`, `hx-target="#tablero"`, `hx-confirm` con el texto
  "¿Borrar la tarea <id>? Se borra su carpeta factory/jobs/<id>/ con todo lo que tiene y
  NO se puede deshacer. El PR, la rama y la spec no se tocan.",
  `aria-label="Borrar la tarea <id>"` y `title="Borrar tarea (no se puede deshacer)"`,
  con un SVG inline de tacho (`aria-hidden="true"`, `focusable="false"`, trazo
  `currentColor`). Las tarjetas en otras etapas y las tareas rotas no lo tienen. —
  `TestRoutes` (busca `hx-post`, `hx-confirm` con "no se puede deshacer", `aria-label` y
  el `<svg` en `003-c`, y su ausencia en `001-a` y `002-b`)
- [ ] CA7: `templates/page.html` declara `<meta name="htmx-config">` con
  `responseHandling` igual al default de htmx 2.0.4 más `409` con `swap: true` antes de
  `[45]..`, para que el aviso de CA2 se vea. El JSON del meta es válido. — `TestRoutes`
  (`GET /` contiene el meta) y un test que extrae el `content` y lo pasa por
  `json.Unmarshal`
- [ ] CA8: el botón usa solo tokens que ya existen en los dos temas: color
  `--text-muted` en reposo y `--crit-text` sobre `--crit-bg` en `:hover` y
  `:focus-visible`, con contorno `--series-1` en `:focus-visible`; área táctil de al
  menos 28×28 px; no se agregan colores sueltos. — lectura del diff de `page.html` y
  verificación en el navegador en modo claro, oscuro y ancho de celular, con un
  `factory/` armado a mano en una carpeta temporal (`-repo`), anotada en el PR
- [ ] CA9: en `/parcial/actividad`, un evento cuya tarea ya no está en `factory/jobs/`
  muestra el ID como texto y no como `href="/tarea/<id>"`; las tareas que siguen
  existiendo mantienen el link. — `TestDeleteJob` (pide el feed después de borrar) o
  caso nuevo en `TestRoutes`
- [ ] CA10: los `GET` siguen sin escribir nada: `TestBoardWritesNothing` pasa sin cambios
  en lo que comprueba, también con una carpeta `factory/jobs/.borrando-x/` presente (el
  barrido no corre en los `GET`). —
  `go test -count=1 -run '^TestBoardWritesNothing$' ./tools/factory-board/`
- [ ] CA11: el paso 0.3 del skill `/factory` elige `NNN` como el siguiente al más alto
  entre (a) las carpetas de `factory/jobs/`, incluidas las `.borrando-<id>`, y (b) los
  IDs que aparecen como `factory/jobs/<NNN>-` en `factory/events/*.jsonl`. El skill trae
  el comando exacto (ver Diseño). — verificación manual anotada en el PR: en una carpeta
  temporal con `factory/jobs/001-a/`, `factory/jobs/.borrando-002-b/` y un evento que
  nombra `factory/jobs/005-c/job.json`, el comando imprime `005` (el siguiente es 006);
  sin eventos, imprime `002`
- [ ] CA12: el tablero sigue escuchando en `localhost:8090` por defecto y sin
  autenticación nueva; cada borrado queda en el log del proceso
  (`tarea <id> borrada`, o el error si quedó algo en `.borrando-<id>`). — lectura de
  `main.go` en el diff
- [ ] CA13: documentación: ADR 0004 nuevo
  (`docs/decisions/0004-tablero-borra-tareas-pr-abierto.md`, estado `propuesta`) que dice
  que el tablero deja de ser de solo lectura, qué borra, con qué guardas y que **no se
  puede deshacer**; la línea de estado de ADR 0002 y la de ADR 0003 lo referencian;
  comentarios de `main.go:1-4`, `board.go:59` y `board.go:108-109` actualizados;
  `CLAUDE.md` dice en la línea de `factory/` de "Estructura" y en el punto "Tablero" del
  flujo que el tablero puede borrar tareas en `pr-abierto` con el tacho (ADR 0004); el
  skill `/factory` en "Retomar una tarea" dice que si no existe la carpeta puede haberse
  borrado desde el tablero. — lectura del diff
- [ ] CA14: gates de `CLAUDE.md` → "Definición de terminado" en verde (`gofmt -l .`
  vacío, `go vet ./...`, `staticcheck`, build Linux y Windows,
  `go test -race -count=1 ./...`, `go test -count=5 ./...`), revisión del `reviewer`
  sin bloqueantes y PR con la spec enlazada.

## Diseño propuesto

Tests primero (hoy fallan: la ruta no existe y `POST` da 405, el botón y el meta no
están). Todo en `tools/factory-board/` más docs y skill; nada del binario de
`yt-monitor` cambia.

### Tests

1. `TestDeleteJob` (archivo nuevo `delete_test.go` o en `board_test.go`), tabla de casos
   sobre `httptest.NewServer(testBoard(t, root).Routes())`, cada uno con su propio
   `t.TempDir()`:
   - feliz (`pr-abierto`, `HX-Request`, sin `Origin`) → 200, carpeta borrada, nada más
     cambió, body sin la tarjeta;
   - mergeado (`prQuery` falso que dice `MERGED`, `checkPRs` antes) → 200;
   - etapas que no son `pr-abierto`, `job.json` faltante/roto/etapa rara, actividad
     reciente (evento con `factory/jobs/<id>/` hace 30 s) y subagente activo
     (`SubagentStart` sin `SubagentStop`) → 409, aviso, `snapTree` igual;
   - sin `HX-Request`, `Origin: http://evil.example.com`, `Sec-Fetch-Site: cross-site`,
     `Host: evil.example.com` → 403, `snapTree` igual;
   - `Origin` igual al host del servidor + `Sec-Fetch-Site: same-origin` → 200;
   - IDs inválidos o inexistentes, carpeta que es enlace simbólico, enlace adentro que
     apunta afuera → según CA4;
   - `removeAll` que falla una vez → 200 con aviso y `.borrando-<id>` presente; el
     siguiente `POST` (otra tarea) la barre (CA5); `.borrando-<id>` imborrable → 409;
   - después del caso feliz, `GET /parcial/actividad` no tiene `href="/tarea/<id>"`.
   Usar `b.now` fijo para la ventana de actividad; si algún caso dispara la ronda de
   `gh`, `t.Cleanup(b.prWG.Wait)` como en los tests existentes.
2. `TestRoutes`: casos nuevos para el botón (CA6), el meta (CA7) y
   `GET /tarea/003-c/eliminar` → 405.
3. `TestLocalOnly`: sumar el `POST` con `Host` ajeno → 403.
4. `TestBoardWritesNothing`: sumar una `factory/jobs/.borrando-x/` al árbol inicial.

### Código

1. **Ruta** en `Routes()` (`main.go`): `mux.HandleFunc("POST /tarea/{id}/eliminar",
   b.deleteJob)`, dentro de `localOnly` como todo lo demás.
2. **`sameOrigin(r) bool`** (`main.go`): `r.Header.Get("HX-Request") == "true"`; si hay
   `Origin`, `url.Parse` y `u.Host == r.Host`; si hay `Sec-Fetch-Site`, que sea
   `same-origin`. Por qué alcanza: un formulario de otro sitio no puede mandar headers
   propios, y un `fetch` con `HX-Request` dispara un preflight CORS que el tablero no
   contesta (no hay ruta `OPTIONS` ni headers CORS). No se usa
   `http.CrossOriginProtection` porque es de Go 1.25 y `go.mod` dice `go 1.24.7`
   (`go vet` lo marcaría con `stdversion`).
3. **`(b *Board) Delete(id string, now time.Time) (aviso string, err error)`**
   (`board.go` o archivo nuevo `delete.go`), con un `delMu sync.Mutex` propio para que
   dos pedidos no se pisen, y un campo `removeAll func(string) error` (`os.RemoveAll` en
   `NewBoard`) para poder simular fallas:
   1. `idRe`; `os.Lstat(factory/jobs/<id>)` tiene que ser carpeta real
      (`Mode().IsDir()`, no enlace). Si no: `errNoJob` → 404.
   2. `readJob(id)`: `Err == ""` y `Etapa == "pr-abierto"`; si no → 409 con el motivo
      ("está en <etapa>: solo se borran tareas con el PR abierto", o el `Err`).
   3. `activities(b.Events(), now)[id]`: si hay `Active` o `Last` más nuevo que
      `liveWindow` → 409 ("un agente la tocó <hace>: esperá a que termine").
   4. Barrido: para cada entrada de `factory/jobs/` que empiece con `.borrando-` y sea
      carpeta real, `b.removeAll`; los errores se loguean. Si
      `factory/jobs/.borrando-<id>` sigue existiendo → 409 ("quedó un borrado anterior
      sin terminar en factory/jobs/.borrando-<id>/"). Hace falta porque en Linux
      `rename(2)` reemplaza una carpeta destino vacía.
   5. `os.Rename(<id>, .borrando-<id>)`. Si falla → 409 con el error recortado con `cut`.
   6. `b.removeAll(.borrando-<id>)`. Si falla → sin error, con `aviso` ("se sacó del
      tablero pero quedaron archivos en factory/jobs/.borrando-<id>/; se reintenta en el
      próximo borrado") y log.
   Errores sentinela (`errNoJob`) y un tipo con mensaje para el 409, así el handler elige
   el código sin comparar textos.
4. **Handler `deleteJob`** (`main.go`): `sameOrigin` → 403 en texto plano; `errNoJob` →
   404 en texto plano; 409 o 200 → `render(w, "tablero", ...)` con lo mismo que
   `boardPartial` más `"Aviso"`. `w.WriteHeader(409)` antes de renderizar en el rechazo.
   `log.Printf` en el éxito y en el aviso.
5. **Template `tablero`** (`partials.html`): `{{with .Aviso}}<p class="note"
   role="alert">{{.}}</p>{{end}}` arriba de los rotos (el texto ya dice si se borró o
   no).
6. **Template `card`** (`partials.html:15-20`): dentro de `.top`, al final,
   `{{if eq .Etapa "pr-abierto"}}<button type="button" class="trash" …>{{template
   "tacho"}}</button>{{end}}` con los atributos de CA6. `{{define "tacho"}}` con un SVG
   de 16×16 (`viewBox="0 0 24 24"`, `fill="none"`, `stroke="currentColor"`,
   `stroke-width="2"`, tapa + cuerpo + dos líneas), `aria-hidden="true"` y
   `focusable="false"`: el nombre accesible lo da el `aria-label` del botón y el `title`
   hace de tooltip. El ID cumple `idRe`; igual `html/template` escapa los atributos.
7. **CSS** (`page.html`): `.job .top .trash { margin-left: auto; … }` con los tokens de
   CA8, `background: none`, `border: 1px solid transparent`, `border-radius: 6px`,
   `min-width/min-height: 28px`, `cursor: pointer`. Sin colores nuevos (los cuatro
   tokens ya están en claro y oscuro).
8. **Meta de htmx** (`page.html`, en `head`):
   `<meta name="htmx-config" content='{"responseHandling":[{"code":"204","swap":false},{"code":"[23]..","swap":true},{"code":"409","swap":true,"error":true},{"code":"[45]..","swap":false,"error":true}]}'>`.
   Es configuración declarativa de htmx, no JS propio. Solo cambia algo para el 409:
   los `GET` del tablero nunca lo devuelven.
9. **Feed** (`activityPartial` y template `actividad`): pasar al template el conjunto de
   IDs que existen en `factory/jobs/` (un `os.ReadDir` por pedido, filtrado con `idRe`) y
   poner el link solo si `.Tarea` está en el conjunto.
10. **Skill** (`.claude/skills/factory/SKILL.md`):
    - Paso 0.3: `NNN` es el siguiente al más alto que dé este comando (vacío si no hay
      ninguno: arrancar en `001`):

      ```bash
      { ls -A factory/jobs 2>/dev/null | sed -nE 's/^(\.borrando-)?([0-9]{3})-.*/\2/p'
        cat factory/events/*.jsonl 2>/dev/null | grep -oE 'factory/jobs/[0-9]{3}-' | sed -E 's/.*jobs\/([0-9]{3})-/\1/'
      } | sort -n | tail -1
      ```

      con una línea de por qué: una tarea borrada desde el tablero deja eventos con su
      ID hasta 30 días, y si el número se reusara `assignJobs` los podría mezclar con la
      tarea nueva. Usa `cat` + `grep` (no `grep -r`) para no depender de flags de
      versiones distintas de `grep`.
    - "Retomar una tarea", paso 1: si `factory/jobs/<id>/job.json` no existe, avisar que
      puede haberse borrado desde el tablero, listar las tareas y parar.
11. **Docs:**
    - ADR 0004 (contexto: tareas terminadas que se acumulan; decisión: única escritura
      del tablero, borrado definitivo por `POST` con las guardas de arriba, rename +
      `RemoveAll` con barrido, `job.json` sigue siendo solo del orquestador, eventos sin
      tocar, numeración que cuenta los eventos; consecuencias: **no se puede deshacer**,
      la persona ve la advertencia en el `confirm`; alternativas descartadas).
    - Línea de estado de ADR 0002 ("modificada por ADR 0004: el tablero puede borrar
      tareas en `pr-abierto`") y de ADR 0003.
    - Comentarios de `main.go:1-4`, `board.go:59`, `board.go:108-109`.
    - `CLAUDE.md`: la línea `factory/ estado (jobs/) y actividad (events/) de la
      factory; en .gitignore` suma "el tablero puede borrar con el tacho las tareas en
      `pr-abierto`"; y en el punto "Tablero: …" del flujo, una oración: "Desde el
      tablero una persona puede borrar una tarea en `pr-abierto` (también si ya figura
      en "Mergeado") con el tacho; no se puede deshacer y no toca el PR, la rama ni la
      spec (ADR 0004)." Ese mismo punto hoy dice que el tablero "no escribe `job.json`",
      que sigue siendo cierto.

### Alternativas descartadas

- **Archivar en `factory/archivo/<id>/` (mover sin borrar)**: era la propuesta del primer
  borrador por ser reversible; la persona eligió borrar de verdad.
- **`os.RemoveAll` directo sobre `factory/jobs/<id>/`**: si falla a mitad deja una tarea
  rota visible en el tablero; con el rename previo la tarea desaparece de una vez.
- **Renombrar a una carpeta temporal del sistema**: `os.Rename` entre volúmenes falla;
  dentro de `factory/jobs/` siempre es el mismo volumen.
- **Barrer los `.borrando-*` al arrancar el tablero o en los `GET`**: los `GET` dejarían
  de ser de solo lectura (`TestBoardWritesNothing`); en el `POST` alcanza.
- **Nueva etapa `borrada` en `job.json`**: obligaría al tablero a escribir `job.json`,
  que ADR 0002 reserva al orquestador, y la tarjeta seguiría ocupando lugar.
- **Que el botón diga a la persona que corra un comando de `/factory`**: no es lo que
  pidió (un botón que elimine).
- **Confirmación con un `<dialog>` o una segunda pantalla**: necesita JS propio o más
  rutas; `hx-confirm` ya viene en htmx.
- **`http.CrossOriginProtection`**: Go 1.25, por encima del `go 1.24.7` de `go.mod`.
- **Responder 200 con el aviso en los rechazos en vez de 409**: evita el meta de htmx,
  pero un rechazo con 200 confunde a los tests y a quien mire el log.
- **Sacar las líneas de la tarea de `factory/events/`**: reescribir archivos que el hook
  agrega en paralelo pierde eventos de otras tareas; vencen solos a los 30 días.
- **Que el skill guarde el último número usado en un archivo (`factory/ultimo-id`)**:
  otro estado más para mantener y sincronizar entre sesiones; leer los eventos que ya
  existen alcanza para el riesgo real (30 días de eventos).

## Fuera de alcance

- Cerrar el PR, borrar la rama o el worktree, o tocar la spec: el botón no hace nada
  en git ni en GitHub.
- Borrar tareas en otras etapas (triage, implementación, necesita persona, descartadas,
  etc.). Se puede pedir aparte.
- El botón en la página de detalle `/tarea/{id}`: solo va en la tarjeta del tablero.
- Deshacer un borrado, papelera o archivo.
- Limpieza automática de tareas viejas.
- Autenticación del tablero o escuchar fuera de `localhost`.
- Nada del binario de `yt-monitor`, del dashboard de producción ni de la cuota.

## Riesgos

- **Borrado irreversible:** un clic de más borra resultados de agentes, revisiones y
  gates de la tarea. Mitigación: solo en `pr-abierto` (el trabajo ya está en el PR y la
  spec en git), `hx-confirm` que dice "NO se puede deshacer", y guardas de CA2. Se acepta
  por decisión de la persona.
- **Pedidos de otros sitios (CSRF):** con un `POST` que borra, una página de internet
  podría mandar un formulario a `localhost:8090` (el `Host` sería local y `localOnly` lo
  dejaría pasar). Mitigación: `HX-Request` obligatorio + `Origin`/`Sec-Fetch-Site`,
  verificado en CA3; además solo borra tareas en `pr-abierto` sin actividad reciente.
- **Reuso del número de tarea:** si se borra la tarea de número más alto, el paso 0.3
  le daría ese `NNN` a la próxima y los eventos viejos (hasta 30 días) quedarían en su
  feed y su actividad cuando el slug coincida, o confundirían igual con el mismo número.
  Mitigación: el skill cuenta también los IDs de `factory/events/` y las `.borrando-*`
  (CA11). Pasados 30 días los eventos ya no existen y el número puede volver a usarse
  sin mezclar nada.
- **Carrera con el orquestador:** si una sesión hace `/factory rehacer` sobre la tarea
  justo cuando se borra, su `mv job.json.tmp job.json` falla o la herramienta Write
  recrea la carpeta solo con `job.json.tmp`. Mitigación: no se borra con actividad en los
  últimos 2 min ni con un subagente activo (CA2), y el skill avisa si la carpeta no
  existe. Queda una ventana chica entre la comprobación y el rename; se acepta porque la
  acción la hace una persona que sabe que la tarea terminó.
- **Borrado a medias:** si `RemoveAll` falla, queda `factory/jobs/.borrando-<id>/` fuera
  del tablero hasta el próximo borrado, con aviso y log (CA5). Si nunca se vuelve a
  borrar nada, queda ahí; la persona la puede borrar a mano con lo que dice el aviso.
- **Refresco cada 2 s:** un `GET /parcial/tablero` que salió antes del `POST` puede
  llegar después y mostrar la tarjeta un ciclo más (hasta 2 s). Cosmético; si molesta en
  la prueba del navegador, probar `hx-sync="#tablero:replace"` en el botón. Mientras el
  `confirm()` está abierto el navegador no procesa el polling, así que la tarjeta no
  desaparece debajo del diálogo.
- **Windows:** `os.Rename` de una carpeta falla si otro programa tiene un archivo
  abierto adentro (409, nada cambió); `RemoveAll` puede fallar igual después del rename
  (CA5). Los tests de enlaces simbólicos se saltean en Windows. No se prueba en Windows de
  verdad (CI compila para Windows).
- **Privacidad:** borrar reduce lo guardado; no agrega exposición. Los eventos de la
  tarea siguen hasta los 30 días, como hoy.
- **Cuota de YouTube y `datos.jsonl`:** ninguno; el tablero no toca la API ni los datos.
- **Concurrencia:** `delMu` serializa los borrados; la lectura de eventos usa su propio
  `mu` y la ronda de `gh` el suyo, así que no se traban. Se cubre con `go test -race`.

## Preguntas abiertas

Ninguna: las del primer borrador las respondió la persona (borrado definitivo, botón
también en "Mergeado", sin limpieza automática, actualizar `CLAUDE.md`).
