# Spec: eliminar del tablero una tarea con el PR abierto (botón de tacho)

- **Estado:** borrador
- **Tipo:** mejora
- **Rama:** `feat/borrar-tarea-pr-abierto`
- **Origen:** pedido de Julian Barbieri por `/factory` (tarea `004-borrar-tarea-pr-abierto`),
  textual: "Para el dashboard del factory me gustaria que se pueda eliminar la tarea
  manualmente una vez que ya se encuentre dentro de la columna "PR abierto". El boton
  podria ser un logo de un tacho de basura."
- **Aprobada por / fecha:** <pendiente>

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
  cumple `idRe` (`^\d{3}-[a-z0-9-]+$`, `board.go:53`). No hay forma de sacar una tarea del
  tablero salvo borrar la carpeta a mano. Las tareas en `pr-abierto` quedan para siempre:
  las mergeadas pasan a la columna derivada "Mergeado" (ADR 0003), que en `job.json`
  sigue siendo `pr-abierto`.
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
  `pr-abierto` (`SKILL.md:271-280`).
- `.gitignore` ignora `factory/jobs/` y `factory/events/`, no todo `factory/`.

**Motivo:** las tareas terminadas (PR abierto o ya mergeado) se acumulan en el tablero y
la persona quiere sacarlas con un botón, sin ir a la terminal.

## Objetivo

Cada tarjeta cuya etapa en `job.json` es `pr-abierto` tiene un botón con un tacho de
basura que, después de confirmar, saca la tarea del tablero moviendo su carpeta de
`factory/jobs/<id>/` a `factory/archivo/<id>/`. Es la única escritura del tablero, solo
por `POST` desde la propia página, solo en `localhost`, y no toca `job.json`, los
eventos, la spec, la rama ni el PR.

## Decisión: qué es "eliminar"

**Archivar (mover la carpeta), no borrar.** `os.Rename(factory/jobs/<id>,
factory/archivo/<id>)`:

- **Reversible:** si fue un error, `mv factory/archivo/<id> factory/jobs/` la devuelve
  tal cual (con `job.json`, `triage.md`, revisiones y gates).
- **Atómico** en el mismo volumen (Linux y Windows): no queda una tarea a medio borrar
  que el tablero muestre rota, y no hace falta un borrado recursivo (`os.RemoveAll`),
  que es justo lo que el hook `guard-bash.sh` bloquea para los agentes (`rm -rf`).
- **No toca `job.json`:** el orquestador sigue siendo el único que lo escribe (ADR 0002);
  el tablero solo cambia dónde está la carpeta.
- **No reutiliza números:** el skill pasa a contar también `factory/archivo/` al elegir
  el `NNN` siguiente, así un ID archivado no se repite.
- `factory/archivo/` va afuera de `factory/jobs/`: `Jobs()` y el glob
  `factory/jobs/*/job.json` de `/factory estado` no lo ven sin cambiar nada.

La pregunta abierta 1 plantea la alternativa de borrar de verdad.

**Eventos:** no se tocan. Los archivos del día son de todas las tareas, el hook los
agrega en paralelo y reescribirlos para sacar líneas perdería eventos de otras sesiones.
Se van solos a los 30 días. Lo que cambia es el feed: el ID de una tarea que ya no está
en `factory/jobs/` se muestra como texto, sin link (que daría 404).

**Spec, rama y PR:** no se tocan. La spec ya está commiteada; el PR sigue en GitHub,
abierto o mergeado. Archivar es solo sacar la tarjeta del tablero.

## Criterios de aceptación

- [ ] CA1: `POST /tarea/{id}/eliminar` con `HX-Request: true`, `Host` local y una tarea
  cuyo `job.json` es válido y tiene `etapa: pr-abierto` responde 200 con el parcial del
  tablero (sin la tarjeta), y después `factory/jobs/<id>/` ya no existe y
  `factory/archivo/<id>/` tiene los mismos archivos con los mismos bytes (incluido
  `job.json`). Vale también para una tarea que el tablero muestra en "Mergeado". —
  `TestArchiveJob` (caso feliz y caso mergeado con `prQuery` falso)
- [ ] CA2: no archiva si la etapa no es `pr-abierto` (por ejemplo
  `esperando-aprobacion`, `descartada`), si `job.json` falta, está roto o tiene una
  etapa desconocida, si ya existe `factory/archivo/<id>`, o si la tarea tuvo actividad
  hace menos de `liveWindow` (2 min) o tiene un subagente activo según `activities`.
  En todos esos casos responde **409** con el parcial del tablero más un aviso
  `role="alert"` que dice por qué, y ningún archivo de `factory/` cambia. —
  `TestArchiveJob` (un caso por motivo, comparando con `snapTree` antes y después)
- [ ] CA3: protección contra pedidos de otros sitios (CSRF) y de otros hosts: responde
  **403** y no mueve nada si falta `HX-Request: true`, si hay `Origin` y su host no es
  igual a `r.Host`, si hay `Sec-Fetch-Site` distinto de `same-origin`, o si el `Host` no
  es local (`localOnly`). Un `Origin` igual al host local con `Sec-Fetch-Site:
  same-origin` pasa. — `TestArchiveJob` (casos 403) y `TestLocalOnly` extendido con el
  `POST`
- [ ] CA4: un `id` que no cumple `idRe` (por ejemplo `..%2F..%2F.env`, `001-A`,
  `1-a`) o que no existe en `factory/jobs/` da **404**; si `factory/jobs/<id>` es un
  enlace simbólico (aunque apunte a una carpeta) o un archivo, también 404 y el destino
  del enlace queda intacto; si `factory/archivo` existe pero es un enlace o un archivo,
  409. `GET /tarea/{id}/eliminar` da 405. — `TestArchiveJob` (los casos de enlaces se
  saltean en Windows con `t.Skip`) y `TestRoutes` (405)
- [ ] CA5: solo las tarjetas con `Etapa == "pr-abierto"` (columnas "PR abierto" y
  "Mergeado") tienen el botón: un `<button type="button">` con
  `hx-post="/tarea/<id>/eliminar"`, `hx-target="#tablero"`, `hx-confirm` en español
  ("¿Eliminar <id> del tablero? La carpeta pasa a factory/archivo/ y se puede
  recuperar. El PR, la rama y la spec no se tocan."), `aria-label="Eliminar la tarea
  <id> del tablero"` y `title="Eliminar del tablero"`, con un SVG inline de tacho
  (`aria-hidden="true"`, `focusable="false"`, trazo `currentColor`). Las tarjetas en
  otras etapas y las tareas rotas no lo tienen. — `TestRoutes` (busca `hx-post`,
  `hx-confirm`, `aria-label` y el `<svg` en `003-c`, y su ausencia en `001-a` y `002-b`)
- [ ] CA6: `templates/page.html` declara `<meta name="htmx-config">` con
  `responseHandling` igual al default de htmx 2.0.4 más `409` con `swap: true` antes de
  `[45]..`, para que el aviso de CA2 se vea. El JSON del meta es válido. — `TestRoutes`
  (`GET /` contiene el meta) y un test que extrae el `content` y lo pasa por
  `json.Unmarshal`
- [ ] CA7: el botón usa solo tokens que ya existen en los dos temas: color
  `--text-muted` en reposo y `--crit-text` sobre `--crit-bg` en `:hover` y
  `:focus-visible`, con contorno `--series-1` en `:focus-visible`; área táctil de al
  menos 28×28 px; no se agregan colores sueltos. — lectura del diff de `page.html` y
  verificación en el navegador en modo claro, oscuro y ancho de celular, con un
  `factory/` armado a mano en una carpeta temporal (`-repo`), anotada en el PR
- [ ] CA8: en `/parcial/actividad`, un evento cuya tarea ya no está en `factory/jobs/`
  muestra el ID como texto y no como `href="/tarea/<id>"`; las tareas que siguen
  existiendo mantienen el link. — `TestArchiveJob` (pide el feed después de archivar) o
  caso nuevo en `TestRoutes`
- [ ] CA9: los `GET` siguen sin escribir nada: `TestBoardWritesNothing` pasa sin cambios
  en lo que comprueba. — `go test -count=1 -run '^TestBoardWritesNothing$' ./tools/factory-board/`
- [ ] CA10: el tablero sigue escuchando en `localhost:8090` por defecto y sin
  autenticación nueva; cada archivado queda en el log del proceso
  (`tarea <id> archivada en factory/archivo/`). — lectura de `main.go` en el diff
- [ ] CA11: documentación: ADR 0004 nuevo (`docs/decisions/0004-tablero-archiva-tareas.md`,
  estado `propuesta`) que dice qué escribe el tablero y por qué; la línea de estado de
  ADR 0002 y la de ADR 0003 lo referencian; comentarios de `main.go:1-4`, `board.go:59`
  y `board.go:108-109` actualizados; `.gitignore` suma `factory/archivo/`; el skill
  `/factory` cuenta `factory/archivo/` al numerar (paso 0.3), y en "Retomar una tarea"
  dice que si la carpeta está en `factory/archivo/` se avise que se archivó desde el
  tablero y cómo devolverla. — lectura del diff y
  `git check-ignore factory/archivo/001-x/job.json` (sale con 0)
- [ ] CA12: gates de `CLAUDE.md` → "Definición de terminado" en verde (`gofmt -l .`
  vacío, `go vet ./...`, `staticcheck`, build Linux y Windows,
  `go test -race -count=1 ./...`, `go test -count=5 ./...`), revisión del `reviewer`
  sin bloqueantes y PR con la spec enlazada.

## Diseño propuesto

Tests primero (hoy fallan: la ruta no existe y `POST` da 405, el botón y el meta no
están). Todo en `tools/factory-board/`; nada del binario de `yt-monitor` cambia.

### Tests

1. `TestArchiveJob` (archivo nuevo `archive_test.go` o en `board_test.go`), tabla de
   casos sobre `httptest.NewServer(testBoard(t, root).Routes())`, cada uno con su propio
   `t.TempDir()`:
   - feliz (`pr-abierto`, `HX-Request`, sin `Origin`) → 200, carpeta movida, bytes
     iguales, body sin el ID en una tarjeta;
   - mergeado (`prQuery` falso que dice `MERGED`, `checkPRs` antes) → 200;
   - etapas que no son `pr-abierto`, `job.json` faltante/roto/etapa rara, destino
     existente, actividad reciente (evento con `factory/jobs/<id>/` hace 30 s) y
     subagente activo (`SubagentStart` sin `SubagentStop`) → 409, aviso, `snapTree` igual;
   - sin `HX-Request`, `Origin: http://evil.example.com`, `Sec-Fetch-Site: cross-site`,
     `Host: evil.example.com` → 403, `snapTree` igual;
   - `Origin` igual al host del servidor + `Sec-Fetch-Site: same-origin` → 200;
   - IDs inválidos o inexistentes, carpeta que es enlace simbólico, `factory/archivo`
     como archivo → 404/409 según CA4;
   - después del caso feliz, `GET /parcial/actividad` no tiene `href="/tarea/<id>"`.
   Usar `b.now` fijo para la ventana de actividad; si algún caso dispara la ronda de
   `gh`, `t.Cleanup(b.prWG.Wait)` como en los tests existentes.
2. `TestRoutes`: casos nuevos para el botón (CA5), el meta (CA6) y
   `GET /tarea/003-c/eliminar` → 405.
3. `TestLocalOnly`: sumar el `POST` con `Host` ajeno → 403.

### Código

1. **Ruta** en `Routes()` (`main.go`): `mux.HandleFunc("POST /tarea/{id}/eliminar",
   b.archiveJob)`, dentro de `localOnly` como todo lo demás.
2. **`sameOrigin(r) bool`** (`main.go`): `r.Header.Get("HX-Request") == "true"`; si hay
   `Origin`, `url.Parse` y `u.Host == r.Host`; si hay `Sec-Fetch-Site`, que sea
   `same-origin`. Por qué alcanza: un formulario de otro sitio no puede mandar headers
   propios, y un `fetch` con `HX-Request` dispara un preflight CORS que el tablero no
   contesta (no hay ruta `OPTIONS` ni headers CORS). No se usa
   `http.CrossOriginProtection` porque es de Go 1.25 y `go.mod` dice `go 1.24.7`
   (`go vet` lo marcaría con `stdversion`).
3. **`(b *Board) Archive(id string, now time.Time) error`** (`board.go` o archivo nuevo
   `archive.go`), con un `archMu sync.Mutex` propio para que dos pedidos no se pisen:
   1. `idRe`; `os.Lstat(factory/jobs/<id>)` tiene que ser carpeta real
      (`Mode().IsDir()`, no enlace). Si no: error "no existe" → 404.
   2. `readJob(id)`: `Err == ""` y `Etapa == "pr-abierto"`; si no → 409 con el motivo
      ("está en <etapa>, solo se eliminan tareas con el PR abierto", o el `Err`).
   3. `activities(b.Events(), now)[id]`: si hay `Active` o `Last` más nuevo que
      `liveWindow` → 409 ("un agente la tocó hace <hace>: esperá a que termine").
   4. `os.MkdirAll(factory/archivo, 0o755)` y `os.Lstat` del resultado: tiene que ser
      carpeta real, si no → 409.
   5. `os.Lstat(factory/archivo/<id>)`: si existe → 409 ("ya hay una tarea archivada
      con ese ID"). Hace falta porque en Linux `rename(2)` reemplaza una carpeta destino
      vacía.
   6. `os.Rename`. Si falla (en Windows, por ejemplo, un archivo abierto en otro
      programa) → 409 con el error recortado con `cut`.
   Errores tipados o sentinela (`errNoJob`, y uno con mensaje para 409) para que el
   handler elija el código sin comparar textos.
4. **Handler `archiveJob`** (`main.go`): `sameOrigin` → 403 en texto plano; `Archive` →
   404 en texto plano; 409 o 200 → `render(w, "tablero", ...)` con lo mismo que
   `boardPartial` más `"Aviso"` (texto del motivo, o vacío). Escribe el status con
   `w.WriteHeader(409)` antes de renderizar. `log.Printf` en el éxito.
5. **Template `tablero`** (`partials.html`): `{{with .Aviso}}<p class="note"
   role="alert">No se eliminó: {{.}}</p>{{end}}` arriba de los rotos.
6. **Template `card`** (`partials.html:15-20`): dentro de `.top`, al final,
   `{{if eq .Etapa "pr-abierto"}}<button type="button" class="trash" …>{{template
   "tacho"}}</button>{{end}}` con los atributos de CA5. `{{define "tacho"}}` con un SVG
   de 16×16 (`viewBox="0 0 24 24"`, `fill="none"`, `stroke="currentColor"`,
   `stroke-width="2"`, tapa + cuerpo + dos líneas), `aria-hidden="true"` y
   `focusable="false"`: el nombre accesible lo da el `aria-label` del botón y el
   `title` hace de tooltip. El ID sale del `job` y cumple `idRe`; igual
   `html/template` escapa los atributos.
7. **CSS** (`page.html`): `.job .top .trash { margin-left: auto; … }` con los tokens de
   CA7, `background: none`, `border: 1px solid transparent`, `border-radius: 6px`,
   `min-width/min-height: 28px`, `cursor: pointer`. Sin colores nuevos (los cuatro
   tokens ya están en claro y oscuro).
8. **Meta de htmx** (`page.html`, en `head`):
   `<meta name="htmx-config" content='{"responseHandling":[{"code":"204","swap":false},{"code":"[23]..","swap":true},{"code":"409","swap":true,"error":true},{"code":"[45]..","swap":false,"error":true}]}'>`.
   Es configuración declarativa de htmx, no JS propio. Solo cambia algo para el 409:
   los `GET` del tablero nunca lo devuelven.
9. **Feed** (`activityPartial` y template `actividad`): pasar al template el conjunto de
   IDs que existen en `factory/jobs/` (un `os.ReadDir` por pedido, filtrado con
   `idRe`) y poner el link solo si `.Tarea` está en el conjunto.
10. **Docs:** ADR 0004 (contexto: tareas que se acumulan; decisión: única escritura del
    tablero, archivar por `POST` con las guardas de arriba, `job.json` sigue siendo solo
    del orquestador; consecuencias y alternativas descartadas); línea de estado de ADR
    0002 ("modificada por ADR 0004: el tablero puede archivar tareas en `pr-abierto`") y
    de ADR 0003; comentarios de `main.go:1-4`, `board.go:59`, `board.go:108-109`;
    `.gitignore`; `SKILL.md` paso 0.3 y "Retomar una tarea" (CA11).

### Alternativas descartadas

- **Borrar de verdad (`os.RemoveAll`)**: irreversible para un clic en una página que se
  refresca sola; borrado recursivo en Go cuando el proyecto bloquea `rm -rf` a los
  agentes; y el siguiente `/factory` reusaría el `NNN`. Queda como pregunta abierta.
- **Nueva etapa `archivada` en `job.json`**: obligaría al tablero a escribir `job.json`,
  que ADR 0002 reserva al orquestador, y a manejar escrituras concurrentes con él.
- **Archivar dentro de `factory/jobs/` (por ejemplo `factory/jobs/_archivo/`)**: evita
  tocar `.gitignore`, pero mezcla lo archivado con lo vivo y confunde la numeración y los
  `ls` del skill.
- **Que el botón diga a la persona que corra un comando de `/factory`**: no es lo que
  pidió (un botón que elimine).
- **Confirmación con un `<dialog>` o una segunda pantalla**: necesita JS propio o más
  rutas; `hx-confirm` ya viene en htmx.
- **`http.CrossOriginProtection`**: Go 1.25, por encima del `go 1.24.7` de `go.mod`.
- **Responder 200 con el aviso en vez de 409**: evita el meta de htmx, pero un rechazo
  con 200 confunde a los tests y a quien mire el log.
- **Sacar las líneas de la tarea de `factory/events/`**: reescribir archivos que el hook
  agrega en paralelo pierde eventos de otras tareas; vencen solos a los 30 días.

## Fuera de alcance

- Cerrar el PR, borrar la rama o el worktree, o tocar la spec: el botón no hace nada
  en git ni en GitHub.
- Eliminar tareas en otras etapas (triage, implementación, necesita persona,
  descartadas, etc.). Se puede pedir aparte.
- El botón en la página de detalle `/tarea/{id}`: solo va en la tarjeta del tablero.
- Ver, buscar o restaurar tareas archivadas desde el tablero (se restauran con `mv`).
- Borrado automático de `factory/archivo/` por antigüedad.
- `/factory estado` no lista las archivadas (no hace falta cambiar nada).
- Autenticación del tablero o escuchar fuera de `localhost`.
- `CLAUDE.md` (línea `factory/ estado (jobs/) y actividad (events/)`): no se toca salvo
  que la persona lo pida (pregunta 3).
- Nada del binario de `yt-monitor`, del dashboard de producción ni de la cuota.

## Riesgos

- **Pedidos de otros sitios (CSRF):** hoy no hay riesgo porque todo es `GET`; con un
  `POST` que mueve archivos, una página de internet podría mandar un formulario a
  `localhost:8090` (el `Host` sería local y `localOnly` lo dejaría pasar). Mitigación:
  `HX-Request` obligatorio + `Origin`/`Sec-Fetch-Site`, verificado en CA3. Impacto
  acotado igual: solo mueve tareas en `pr-abierto`, de forma reversible.
- **Carrera con el orquestador:** si una sesión hace `/factory rehacer` sobre la tarea
  justo cuando se archiva, su `mv job.json.tmp job.json` falla o recrea la carpeta solo
  con `job.json`. Mitigación: no se archiva con actividad en los últimos 2 min ni con un
  subagente activo (CA2), y el skill avisa si encuentra la tarea en `factory/archivo/`.
  Queda una ventana chica entre la comprobación y el `os.Rename`; se acepta porque la
  acción la hace una persona que sabe que la tarea terminó.
- **Refresco cada 2 s:** un `GET /parcial/tablero` que salió antes del `POST` puede
  llegar después y mostrar la tarjeta un ciclo más (hasta 2 s). Cosmético; si molesta en
  la prueba del navegador, probar `hx-sync="#tablero:replace"` en el botón. Mientras el
  `confirm()` está abierto el navegador no procesa el polling, así que la tarjeta no
  desaparece debajo del diálogo.
- **Parpadeo de "todavía no tiene job.json":** si un `GET` lista la carpeta justo antes
  del `os.Rename` y la lee después, esa respuesta muestra la tarea como rota. Dura un
  ciclo; no se mitiga.
- **Windows:** `os.Rename` de una carpeta falla si otro programa tiene un archivo
  abierto adentro; se informa con el aviso (409) y no queda nada a medias. Los tests de
  enlaces simbólicos se saltean en Windows. No se prueba en Windows de verdad (CI compila
  para Windows).
- **Privacidad y retención:** `factory/archivo/` tiene lo mismo que `factory/jobs/`
  (resultados de agentes, sin datos de `datos.jsonl` por regla del skill) y queda en
  `.gitignore`. No crece la exposición; sí se acumula sin límite (pregunta 2).
- **Cuota de YouTube y `datos.jsonl`:** ninguno; el tablero no toca la API ni los datos.
- **Concurrencia:** `archMu` serializa los archivados; la lectura de eventos usa su
  propio `mu` y la ronda de `gh` el suyo, así que no se traban. Se cubre con
  `go test -race`.

## Preguntas abiertas

1. **¿Archivar o borrar de verdad?** La spec propone mover a `factory/archivo/<id>/`
   (reversible, atómico, no reusa números). Si preferís borrado definitivo, el diseño
   cambia a `os.RemoveAll` después de las mismas guardas, sin `factory/archivo/` ni
   cambios de numeración, y sin forma de deshacerlo.
2. **¿Limpieza de `factory/archivo/`?** Recomendación: ninguna automática por ahora (son
   archivos chicos y la idea es poder volver atrás). Alternativa: borrar lo archivado
   hace más de 30 días al arrancar el tablero, como los eventos.
3. **¿Actualizar `CLAUDE.md`** para que la línea de `factory/` diga también `archivo/`?
   Recomendación: sí, una palabra; queda fuera hasta que lo confirmes.
4. **¿También en "Mergeado"?** El pedido dice la columna "PR abierto". La spec pone el
   botón en toda tarjeta con `etapa: pr-abierto` en `job.json`, que incluye la columna
   "Mergeado" (es donde más se acumulan). Si lo querés solo en "PR abierto" (sin
   mergear), el botón y la guarda pasan a usar la columna calculada (`Card.Stage.Key`).
