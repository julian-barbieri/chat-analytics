# Spec: columna "Mergeado" en el tablero y vista previa local del PR

- **Estado:** implementada
- **Tipo:** tarea
- **Rama:** `chore/tablero-mergeado-vista-previa`
- **Origen:** pedido de Julian Barbieri por `/factory` (textual): "1) Me gustaria que
  cuando se hace el merge el PR se pase a la siguiente seccion del tablero. 2) Cuando
  se hace el merge se deberia dejar correr la aplicacion y volver a correr para que se
  pueda visualizar el cambio o mostrar en el local rama que hizo el cambio asi lo puedo
  ver." Decisiones ya tomadas por la persona: columna "Mergeado" calculada por el
  tablero con `gh` sin escribir `job.json`; "vista previa del PR" que levanta el
  orquestador en otro puerto con una copia temporal de los datos y una API simulada.
- **Aprobada por / fecha:** Julian Barbieri, 2026-10-30 ("Aprobar las dos": con las recomendaciones de las preguntas abiertas tal cual)

## Contexto

No es un bug: no hay nada que reproducir. Lo que sigue es cómo está hoy, verificado
leyendo el código (Go 1.26.8; `go build ./...`, `go vet ./...` y `go test -count=1 ./...`
en verde en la rama antes de esta spec).

**Tablero (`tools/factory-board`)**

- Las columnas salen de `stages` (`tools/factory-board/board.go:25`); la última del
  camino feliz es `pr-abierto` (`board.go:32`). `readJob` marca como error cualquier
  etapa que no esté en esa lista (`board.go:153`).
- `Board` "lee factory/ del repo. No escribe nada" (`board.go:102`) y no hace ninguna
  llamada de red. `boardPartial` (`tools/factory-board/main.go:128`) reconstruye todo en
  cada pedido, y htmx lo pide **cada 2 s** (`templates/page.html:122`). Sin caché, una
  consulta a GitHub por pedido serían 30 por minuto por PR.
- `job.json` no cambia después de abrir el PR (el orquestador termina en el paso 8 del
  skill), así que hoy toda tarea mergeada queda para siempre en "PR abierto".
- La tarjeta (`card`, `main.go:100`, y el template `card` de `templates/partials.html`)
  ya muestra el link al PR; la página de la tarea (`templates/tarea.html`) reusa `card`
  para el badge de etapa.
- `gh` 2.46.0 está instalado en esta máquina (`/usr/bin/gh`). No se verificó contra
  GitHub (sin red en el triage) que `gh pr view <url> --json state,mergedAt` devuelva
  `state` en `OPEN`/`CLOSED`/`MERGED` y `mergedAt` en RFC 3339: es lo documentado por la
  CLI; el implementer lo confirma una vez contra el PR #7 (ya mergeado) antes de
  escribir el parser.

**Dashboard y datos (para la vista previa)**

- **No hay flag para la ruta de datos.** El modo dashboard abre `OpenStore("datos.jsonl")`
  relativo al directorio de trabajo (`main.go:108`) y deriva `datos.cuota.json` y
  `datos.espectadores.jsonl` del mismo nombre (`store.go:63-65`). También el archivo de
  la clave se carga relativo al directorio de trabajo (`loadDotEnv`, `main.go:97`) y no
  pisa variables ya definidas (`main.go:303`). Conclusión: correr el binario compilado de
  la rama **con el directorio de trabajo en la carpeta temporal** alcanza para que use la
  copia y no encuentre la clave; no hace falta tocar el producto.
- `-dashboard` solo agrega `localhost` si la dirección empieza con `:` (`main.go:118`);
  `0.0.0.0:8081` escucharía en toda la red. El que lance el binario tiene que validar.
- **Los videos no se persisten**: viven en memoria y los pone el worker con
  `SetVideos` después de consultar la API (`worker.go:238`, `worker.go:246`). Al arrancar
  con datos copiados, la lista de videos depende enteramente de lo que conteste la API.
- **Qué muestra el dashboard si la API falsa devuelve listas vacías** (leído en
  `server.go`): el resumen (totales, actividad, categorías), el feed, "extra" y el CSV
  salen de `Snapshot.Msgs`, que se carga de la copia, así que se ven. Pero la sección del
  vivo queda vacía: `pickLive` solo considera videos con `LiveStatus == "live"`
  (`server.go:227-228`) y `liveAnalysis` exige que el video esté en `snap.Videos`
  (`server.go:373-378`); el resumen tampoco lista vivos (`server.go:96-97`). Si además
  `channels` viene vacío, `UploadsPlaylist` falla con "no se encontró el canal"
  (`youtube.go:95`), el worker muestra el error y reintenta cada 30 s (`worker.go:277`).
  Es decir: con un falso vacío, justo lo que más se toca (el vivo, el chat, los análisis
  y la participación) no se vería.
- Lo que hace el worker según lo que conteste el falso:
  - Un vivo con `LiveStatus == "live"` pero **sin `activeLiveChatId`** se muestra en el
    dashboard (los botones solo miran `LiveStatus`) y el worker **no** lo agrega a
    `lives` ni abre streaming ni lecturas de chat (`worker.go:252`). Así no hay
    conexiones largas ni pedidos de chat al falso.
  - Con `commentCount` en `"0"` el worker no pide comentarios (`worker.go:288`).
  - Sin `concurrentViewers` (`Concurrent == 0`), `AddViewers` no agrega muestras
    (`store.go:202`): la serie de espectadores copiada queda como estaba.
  - Cada revisión de videos suma unidades con `AddUnits`, que reescribe
    `datos.cuota.json` **de la copia**. Si la copia de la cuota ya está en el tope
    (`-cuota`, 9.000), el worker no consulta videos y la vista previa queda sin vivos.
- Los títulos de los vivos no están en los datos guardados; sí el programa de cada
  mensaje de chat (`Message.Program`).

**Restricciones que pesan acá**

- CLAUDE.md y AGENTS.md prohíben dejar un `-dashboard` corriendo y dicen que los agentes
  no dejan el tablero corriendo; AGENTS.md prohíbe "otros almacenamientos que salteen" la
  retención de 30 días. La vista previa crea una copia de datos reales: tiene que ser
  efímera y borrarse siempre.
- El hook `guard-bash.sh` bloquea `rm -rf` y cualquier comando que mencione el archivo
  de la clave: la limpieza la tiene que hacer el propio programa (Go, `os.RemoveAll`), no
  un comando de shell del orquestador.
- Hay worktrees en `.claude/worktrees/`: si la sesión corre en uno, `datos.jsonl` no está
  en el directorio actual sino en el checkout principal.

## Objetivo

1. En el tablero, una tarea en `pr-abierto` cuyo PR fue mergeado aparece sola en una
   columna nueva "Mergeado", sin que nadie escriba `job.json`; si GitHub no responde, la
   tarea sigue en "PR abierto" con un aviso y el tablero no se rompe.
2. Al abrir un PR, el orquestador ofrece una vista previa del dashboard de esa rama en
   `localhost:8081` con una copia temporal de los datos y una API simulada; no gasta
   cuota, no toca los archivos reales y la copia desaparece al cortarla.

## Criterios de aceptación

Parte 1: columna "Mergeado" (`tools/factory-board`)

- [x] CA1: con la consulta del PR inyectada, una tarea en `pr-abierto` con URL de PR
  válida se ubica así: `MERGED` → columna "Mergeado" (inmediatamente después de "PR
  abierto") con la fecha del merge en `02/01 15:04`; `OPEN` → "PR abierto" sin aviso;
  `CLOSED` → "PR abierto" con el aviso "El PR se cerró sin merge"; error de la consulta
  → "PR abierto" con "No se pudo consultar GitHub: …"; todavía sin respuesta → "PR
  abierto" con "Consultando GitHub…"; cualquier otro estado (`DRAFT`, `merged` en
  minúsculas) → "PR abierto" con "Estado desconocido en GitHub: <estado>" (caso en
  `TestPRUnexpectedState`). En todos los casos `job.json` queda byte por byte
  igual. La página `/tarea/<id>` muestra la misma etapa ("Mergeado" en el badge) — se
  verifica con `go test -count=1 -run '^TestPRStatusColumns$' ./tools/factory-board/`.
- [x] CA2: si `pr` no es una URL de PR de GitHub (`^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/pull/[0-9]+$`),
  no se consulta nada (contador de llamadas en 0) y la tarjeta muestra "PR sin URL de
  GitHub válida". Casos: vacío, `javascript:alert(1)`, `--web`, `-R otro/repo`,
  `https://github.com/a/b/pull/1?x=y` — se verifica con
  `go test -count=1 -run '^TestPRStatusColumns$' ./tools/factory-board/` (subcasos).
- [x] CA3: caché: 50 pedidos a `/parcial/tablero` dentro de `prEvery` (2 min, reloj
  inyectado con `Board.now`) generan **una** consulta por PR; pasado `prEvery` se vuelve
  a consultar `OPEN`, `CLOSED` y los errores, nunca un `MERGED`; nunca hay más de una
  ronda de consultas en vuelo — se verifica con
  `go test -race -count=1 -run '^TestPRCache$' ./tools/factory-board/`.
- [x] CA4: la consulta real ejecuta `gh pr view <url> --json state,mergedAt` con timeout,
  `GH_PROMPT_DISABLED=1` y `GH_NO_UPDATE_NOTIFIER=1`; interpreta la salida JSON; si `gh`
  no está en el `PATH` devuelve un error que dice "falta la CLI gh"; si sale con error,
  devuelve la primera línea de stderr recortada a 120 caracteres — se verifica con
  `go test -count=1 -run '^TestGhPRStatus$' ./tools/factory-board/`, usando un `gh` falso
  (script en `t.TempDir()` puesto primero en `PATH` con `t.Setenv`; `t.Skip` si no hay
  `bash`, como `TestLogActivityHook`). Ningún test ejecuta el `gh` real.
- [x] CA5: `mergeado` no es una etapa válida en `job.json` (solo la calcula el tablero):
  `readJob` la sigue marcando "etapa desconocida" — caso nuevo en `TestLoadJobs`
  (`go test -count=1 -run '^TestLoadJobs$' ./tools/factory-board/`).
- [x] CA6: `/factory estado` muestra también el estado en GitHub de las tareas en
  `pr-abierto`, y `/factory rehacer` sobre una tarea cuyo PR ya está mergeado para y
  pide abrir una tarea nueva — se verifica con
  `grep -n 'gh pr view' .claude/skills/factory/SKILL.md` (aparece en `estado` y en
  "Retomar una tarea").

Parte 2: vista previa del PR (`tools/preview`)

- [x] CA7: existe `tools/preview`, `package main` aparte, solo stdlib, que compila en
  Linux y Windows; `go run ./tools/preview -h` lista en español `-repo`, `-datos`,
  `-addr` (por defecto `localhost:8081`) y `-cortar` — se verifica con
  `go run ./tools/preview -h` y `GOOS=windows GOARCH=amd64 go build ./...`.
- [x] CA8: `-addr` solo acepta `localhost`, `127.0.0.1` o `::1` (`:8081` se toma como
  `localhost:8081`); `0.0.0.0:8081`, `192.168.0.10:8081` o un nombre ajeno salen con
  error sin crear carpetas ni compilar nada — se verifica con
  `go test -count=1 -run '^TestPreviewAddr$' ./tools/preview/`.
- [x] CA9: la API simulada, armada a partir de una copia **sintética** (escrita por el
  test en `t.TempDir()`, nunca `datos.jsonl`), responde: `channels` → un canal
  "Vista previa" con lista de subidas; `playlistItems` de la lista `UULV…` → los IDs de
  videos con mensajes de chat, del más reciente al más viejo; de la lista de subidas →
  los IDs con solo comentarios (hasta `maxResults`); `videos` → `liveBroadcastContent:
  live` para los de chat y `none` para el resto, `activeLiveChatId` vacío,
  `commentCount: "0"`, sin `concurrentViewers`, `actualStartTime` = primer mensaje del
  video y un título que incluye el último `Program` guardado de ese video; cualquier
  otra ruta → 404 con `{"error":{"message":"vista previa: <ruta> no está simulado"}}` —
  se verifica con `go test -count=1 -run '^TestFakeAPI$' ./tools/preview/`.
- [x] CA10: de punta a punta: con una copia sintética (`datos.jsonl` con chat de dos vivos
  y comentarios, `datos.cuota.json` y `datos.espectadores.jsonl`) y `YT_API_KEY` del
  entorno puesta en un valor testigo, la vista previa compila el binario de la raíz,
  levanta el dashboard en un puerto libre de `127.0.0.1` y: `/parcial/vivo/chat?video=<id>`
  termina mostrando un texto sintético del chat de ese vivo (espera máxima 30 s);
  `/parcial/resumen` contiene "Vista previa"; la API simulada nunca recibe la clave
  testigo; al cancelar, el proceso hijo terminó, la carpeta de la copia y el archivo de
  estado ya no existen y los tres archivos de origen quedaron byte por byte iguales y con
  la misma fecha de modificación — se verifica con
  `go test -count=1 -run '^TestPreviewEndToEnd$' ./tools/preview/` (con `t.Skip` solo si no
  hay `go` en el `PATH`).
- [x] CA11: `-cortar` (la función que lo implementa) corta una vista previa en curso
  creando el aviso de corte en su carpeta (en exclusiva, sin seguir enlaces): la vista
  previa termina, mata al hijo, borra la copia y el archivo de estado, y `-cortar`
  informa "Vista previa cortada y copia temporal borrada". Si el estado es de una vista
  previa que ya no corre (ni el dashboard ni la vista previa usan la copia; en Linux se
  comprueba con `/proc/<pid>/cwd`, `exe` y los archivos abiertos, así un PID reasignado
  no cuenta), no manda el aviso, no espera ni mata a nadie: borra copia y estado y dice
  "La vista previa ya no estaba corriendo: borré su copia temporal". Si sigue corriendo
  y no limpia en 15 s, mata al dashboard solo si comprueba que ese PID es el dashboard de
  la copia (Linux); si no lo puede comprobar (Windows y otros) o el que sigue es la vista
  previa misma, no mata a nadie y lo informa con el PID. Sin vista previa, dice "No hay
  ninguna vista previa corriendo" y sale con 0. Si el archivo de estado no es de una
  vista previa (carpeta fuera de `yt-monitor-vista-previa-*` en la carpeta de copias,
  JSON roto, sin PIDs, PID 1 o propio), avisa que "no es de una vista previa" y no manda
  ninguna señal ni borra nada, ni siquiera el archivo. `-cortar` también corta una vista
  previa que todavía compila o arranca (el estado existe desde que hay copia) — se
  verifica con `go test -count=1 -run '^(TestPreviewStop|TestStopNeverTouchesOutside|TestPreviewSlowDashboardCancel|TestStopReusedPID|TestStopCutFileNoFollow|TestPreviewCutDuringBuild|TestStopDoesNotKillUnverifiedChild|TestPreviewStateValidAlive)$' ./tools/preview/`.
- [x] CA12: las copias y el archivo de estado van en una carpeta privada del usuario,
  `os.UserCacheDir()/yt-monitor-vista-previa/` (creada y corregida a `0700`; un enlace en
  su lugar se rechaza), no en la temporal compartida (`TestPreviewDefaultTmp`). Al
  arrancar, si el puerto está ocupado sale con "ya hay algo escuchando en
  <addr>: si es una vista previa vieja, cortala con -cortar" **sin tocar nada**; si está
  libre y quedó un archivo de estado de una corrida muerta (o de un PID reasignado), lo
  borra; si el estado es de una vista previa viva en otra dirección (su dashboard o su
  proceso siguen usando la copia), sale con "ya hay una vista previa corriendo en
  <addr>; cortala con -cortar" sin tocar nada (`TestPreviewSecondRunKeepsLiveCopy`,
  `TestPreviewStaleOrLiveState`). El estado se toma como lock en exclusiva apenas existe
  la copia (temporal + `os.Link`, con `Child` 0 hasta que arranca el dashboard): de dos
  vistas previas casi a la vez, la segunda sale con ese mismo error sin dejar nada y la
  primera se puede cortar (`TestPreviewConcurrentRuns`, `TestCreateStateExclusive`). Con
  el estado tomado, barre todas las copias huérfanas `yt-monitor-vista-previa-*` de la
  carpeta privada y avisa "Borré…" solo si borró alguna (`TestPreviewSweepOrphans`,
  `TestStaleCleanupOnlyPreviewFolders`). El temporal del estado tiene nombre al azar y no
  sigue enlaces (`TestWriteStateNoFollow`). Si
  el dashboard vive pero no escucha al vencer el plazo de arranque (30 s), sale con error
  y limpia sin esperar a que el hijo termine; Ctrl+C y `-cortar` funcionan durante esa
  espera (`TestPreviewSlowDashboardCancel`). Si `-datos` no existe, arranca con datos vacíos y lo avisa; copia
  solo los archivos que existan, en una carpeta con permisos `0700` (chequeado fuera de
  Windows) — se verifica con `go test -count=1 -run '^(TestPreviewStartup|TestPreviewDefaultTmp|TestPreviewConcurrentRuns|TestCreateStateExclusive|TestPreviewSweepOrphans|TestSweepSkipsLinksAndFiles|TestStaleCleanupOnlyPreviewFolders|TestWriteStateNoFollow|TestPreviewSlowDashboardCancel)$' ./tools/preview/`.
- [x] CA13: si la copia de `datos.cuota.json` ya está en o sobre el tope (9.000), la vista
  previa pasa `-cuota` = unidades copiadas + 1.000 y lo avisa, para que el worker siga
  consultando la API falsa y se vean los vivos — caso en `TestPreviewStartup`.
- [ ] CA14 (pendiente, la hace la persona): verificación a mano, una vez, por la persona: con el `datos.jsonl` local,
  `go run ./tools/preview -datos <checkout principal>/datos.jsonl`, abrir
  `http://localhost:8081`, ver los botones de vivos, el chat y los análisis en modo claro
  y oscuro; después `go run ./tools/preview -cortar`,
  `ls "${XDG_CACHE_HOME:-$HOME/.cache}/yt-monitor-vista-previa" | grep -c yt-monitor-vista-previa-`
  da `0` (no queda ninguna copia en la carpeta privada) y
  `ls "${TMPDIR:-/tmp}" | grep -c yt-monitor-vista-previa` da `0` (nada en la temporal
  compartida). La evidencia (salida
  de los dos comandos, sin datos de usuarios) va en el PR.

Documentación

- [x] CA15: hay un ADR nuevo `docs/decisions/0003-tablero-consulta-github-y-vista-previa.md`
  y ADR 0002 dice que lo complementa; `.claude/skills/factory/SKILL.md` tiene el paso de
  vista previa entre el 7 y el 8; CLAUDE.md y AGENTS.md mencionan la excepción de la
  vista previa y `tools/preview/` en la estructura — se verifica con
  `ls docs/decisions/0003-*.md`, `grep -n '0003' docs/decisions/0002-estado-en-archivos-y-tablero.md`,
  `grep -n 'tools/preview' CLAUDE.md AGENTS.md .claude/skills/factory/SKILL.md`.
- [x] Gates de `CLAUDE.md` → "Definición de terminado" en verde (`gofmt -l .` vacío,
  `go vet ./...`, `staticcheck`, build Linux y Windows, `go test -race -count=1 ./...`,
  `go test -count=5 ./...`, `govulncheck`). `govulncheck` falla solo por la stdlib de
  Go 1.26.8 instalado (arreglo: actualizar Go a 1.26.10); hay que aclararlo en el PR.

## Diseño propuesto

### Parte 1: estado del PR en el tablero

Tests primero: `TestPRStatusColumns`, `TestPRCache`, `TestGhPRStatus` y el caso nuevo de
`TestLoadJobs`. Hoy fallan por no compilar (no existen la consulta inyectable ni la
columna).

1. **Consulta inyectable** en `Board` (`board.go`):
   - Campo `prQuery func(ctx context.Context, url string) (PRStatus, error)`; `NewBoard`
     lo inicializa con `ghPRStatus`. `PRStatus{State, MergedAt string}`.
   - `ghPRStatus` (archivo nuevo `tools/factory-board/github.go`): valida la URL con
     `prURLRe` (CA2) **antes** de ejecutar, para que un `pr` como `--web` o `-R x/y` no
     llegue como flag a `gh`; corre
     `exec.CommandContext(ctx, "gh", "pr", "view", url, "--json", "state,mergedAt")` con
     timeout `prTimeout` (15 s) y `cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1",
     "GH_NO_UPDATE_NOTIFIER=1")`. `errors.Is(err, exec.ErrNotFound)` → "falta la CLI gh
     (instalala y corré gh auth login)". Otro error → primera línea de stderr, recortada.
     `os/exec` es stdlib.
2. **Caché con refresco en segundo plano** (mismo archivo):
   - `prs map[string]prEntry` con `{Status, Err, Checked}` bajo un mutex propio
     (`prMu`, separado del de eventos), y `prBusy bool`.
   - `prState(url, now)` devuelve lo que haya en caché; si alguna URL pedida está vencida
     (`now - Checked >= prEvery`, 2 min; `MERGED` no vence nunca) y no hay ronda en
     vuelo, lanza **una** goroutine que consulta las vencidas una por una y actualiza la
     caché. Así ningún pedido htmx espera a `gh` y con N PRs hay como mucho N llamadas
     cada 2 min (los mergeados, ninguna después de la primera).
   - `checkPRs(ctx, urls, now)` hace la ronda de forma sincrónica; la usa la goroutine y
     la usan los tests para dejar la caché lista sin carreras ni esperas. La goroutine no
     toca archivos, así que no choca con el cleanup de `t.TempDir()`.
   - Nada se escribe a disco: al reiniciar el tablero se vuelve a consultar todo una vez.
3. **Columna derivada**:
   - `Stage` suma `Derived bool`. `mergeado` ("Mergeado") va en una lista aparte, no en
     `stages`, así `readJob`/`stageOf` la siguen rechazando en `job.json` (CA5).
     `boardPartial` arma las columnas con `stages` más la derivada insertada después de
     `pr-abierto`.
   - `Card` suma `PRNote string` (aviso) y `MergedAt string`. `card()` consulta
     `prState` solo para `Etapa == "pr-abierto"`; si es `MERGED`, `c.Stage` pasa a la
     etapa derivada. `boardPartial` ubica la tarjeta por `c.Stage.Key` en vez de por
     `j.Etapa`, y `jobPage` hereda el badge porque usa `card()`.
   - Template `card`: si hay `MergedAt`, "Mergeado {{fecha .MergedAt}}"; si hay `PRNote`,
     `<p class="badge warn">`. Sin CSS nuevo (`.badge.warn` ya existe en los dos temas).
   - La columna "Mergeado" se muestra siempre (es el final del camino feliz), a diferencia
     de "Descartadas".
4. Comentarios de `main.go:1-3` y `board.go:102`: decir que el tablero lee archivos y
   consulta el estado de los PR con `gh`, y que nunca escribe.
5. **Skill** (`.claude/skills/factory/SKILL.md`):
   - `estado`: después del `jq`, un bucle que para cada tarea en `pr-abierto` con `pr`
     imprime `gh pr view "$u" --json state -q .state` (o "sin datos" si falla). No lee
     cada `job.json` por separado, así que no asocia la sesión a una tarea.
   - "Retomar una tarea" → `rehacer` en `pr-abierto`: si `gh` dice `MERGED`, parar y
     decir que el PR ya entró a `main` y que el ajuste va en una tarea nueva (un push a
     una rama mergeada no actualiza nada).
   - Paso 8: avisar que, cuando la persona mergee, la tarjeta pasa sola a "Mergeado" en
     unos 2 minutos, y que para ver el cambio en su dashboard real tiene que actualizar
     `main` y reiniciarlo ella (el orquestador no lo hace: sería un `-dashboard` contra la
     API real).

Alternativas descartadas (parte 1):

- Que el orquestador escriba `etapa: mergeado`: la persona decidió que sea automático y
  el orquestador no está corriendo cuando se mergea.
- Llamar a `gh` en cada pedido: 30 llamadas por minuto por PR y pedidos htmx lentos.
- Refresco sincrónico cada 2 min dentro del pedido: más simple, pero un pedido cada 2 min
  tarda N × la latencia de `gh` y se notan saltos en el tablero.
- `gh pr list --state all` (una sola llamada para todos): depende de estar en un clone con
  remoto de GitHub y del límite de resultados; `gh pr view <url>` no.
- API REST de GitHub con `net/http`: hay que manejar el token a mano; `gh` ya tiene la
  sesión de la persona.

### Parte 2: vista previa del PR (`tools/preview`)

Tests primero: `TestPreviewAddr`, `TestFakeAPI`, `TestPreviewStartup`,
`TestPreviewStop` y `TestPreviewEndToEnd`, todos con datos sintéticos en `t.TempDir()`.

`package main` nuevo en `tools/preview/` (mismo módulo, solo stdlib, no entra en el
binario de `yt-monitor`). Archivos sugeridos: `main.go` (flags, `run`, `stop`),
`fakeapi.go`, `copy.go`, `preview_test.go`.

1. **Flags**: `-repo` (por defecto `.`; tiene que tener `go.mod` y `main.go`), `-datos`
   (por defecto `datos.jsonl`; ruta al `datos.jsonl` real, del que se derivan
   `datos.cuota.json` y `datos.espectadores.jsonl` igual que `OpenStore`), `-addr` (por
   defecto `localhost:8081`, validado como en CA8) y `-cortar`.
2. **Arranque (`run(ctx, cfg)`)**, en este orden:
   1. Validar `-addr`. Probar que el puerto esté libre (`net.Listen` y cerrar); si no,
      salir sin tocar nada (CA12).
   2. Carpeta de copias: `os.UserCacheDir()/yt-monitor-vista-previa/` (`MkdirAll` y
      `Chmod` `0700`; si es un enlace, error), y no la temporal compartida, donde otro
      usuario de la máquina podría plantar enlaces o estados falsos (revisión 1). Si
      existe el archivo de estado (`<carpeta>/yt-monitor-vista-previa.json`) de una
      vista previa viva (su dashboard o ella usan la copia: en Linux, `/proc/<pid>/cwd`,
      `exe` o un archivo abierto dentro de la copia; en otros sistemas, que el PID
      exista), salir con "ya hay una vista previa corriendo". Si es de una corrida
      muerta, borrar el archivo (solo si no cambió desde que se leyó).
   3. `os.MkdirTemp(<carpeta>, "yt-monitor-vista-previa-*")` (permisos `0700`), dejarla
      abierta (es lo que prueba en Linux que la vista previa es dueña de ese estado) y
      **tomar el estado como lock** con `{dir, addr, Child: 0, Self: pidPropio}`:
      temporal con `os.CreateTemp` + `os.Link` al nombre final, que falla si ya existe
      (sale con "ya hay una vista previa corriendo…" sin dejar nada); el temporal se
      borra siempre. Con el lock tomado, barrer las demás carpetas
      `yt-monitor-vista-previa-*` de primer nivel (copias huérfanas; un enlace no se
      sigue) y avisar "Borré…" solo si borró alguna. Copiar los
      archivos de datos que existan (`io.Copy`; una última línea a medio escribir por el
      dashboard real la descarta `OpenStore` al leer). Nunca copiar nada más del repo.
   4. Si la cuota copiada está en el tope, calcular `-cuota` alto (CA13).
   5. Leer de la copia solo `ID`, `Source`, `VideoID`, `At` y `Program` de cada línea
      (struct propio: el falso nunca maneja textos ni autores) y armar la API simulada
      (CA9). Escucha en `127.0.0.1:0` con `net/http` (sin `httptest`, que es para tests).
   6. `go build -o <copia>/yt-monitor-vista-previa[.exe] .` con `Dir = -repo` y
      `WaitDelay` de 2 s: compila la rama que está en el checkout. Mientras compila, una
      goroutine mira el aviso `cortar` y cancela el build, así `-cortar` no espera a go.
   7. Lanzar el binario con `Dir = <copia>`, `-dashboard <addr>`, y un entorno sin
      `YT_API_KEY` ni `YT_BASE_URL` heredados, más `YT_API_KEY=vista-previa` y
      `YT_BASE_URL=http://127.0.0.1:<puerto del falso>`. Como el directorio de trabajo es
      la copia, el binario no encuentra el archivo de la clave del repo.
   8. Anotar `Child` = PID del hijo en el estado (temporal con nombre al azar +
      `os.Rename`) apenas arranca, esperar hasta 30 s a que escuche y loguear `Vista previa en http://<addr> (datos copiados a las HH:MM;
      no llegan mensajes nuevos). Cortala con: go run ./tools/preview -cortar`. Nada de
      mensajes, autores ni conteos por persona en el log: solo cuántos mensajes y vivos se
      copiaron.
3. **Corte**: `run` termina cuando (a) se cancela `ctx` (`signal.NotifyContext` con
   `os.Interrupt` y `syscall.SIGTERM`), (b) aparece el archivo `cortar` en la copia
   (se revisa cada 1 s) o (c) el hijo termina solo. En los tres: `Process.Kill` al hijo
   (no `Signal(os.Interrupt)`, que no anda en Windows), `Wait` (en Windows no se puede
   borrar la carpeta ni el `.exe` de un proceso vivo), cerrar el falso, `os.RemoveAll`
   de la copia y borrar el archivo de estado. Todo en un `defer` desde el paso 3.
4. **`-cortar` (`stop`)**: lee el archivo de estado; si no hay, "No hay ninguna vista
   previa corriendo". Si no es de una vista previa, no toca nada. Si ya no corre (ver
   paso 2.2), borra copia y estado sin esperar ni matar: "La vista previa ya no estaba
   corriendo: borré su copia temporal". Si corre, crea `<copia>/cortar` con `O_EXCL`
   (no sigue enlaces) y espera hasta 15 s a que desaparezca el archivo de estado. Si no
   desaparece, relee el estado y mata `Child` con `os.FindProcess` + `Kill` **solo si**
   en Linux comprueba que su `cwd`, `exe` o un archivo abierto está dentro de la copia;
   si no se puede comprobar (Windows, donde `FindProcess` no distingue un PID
   reasignado) no mata: borra la copia solo si se deja borrar (nadie la usa) y si no,
   informa el PID; si `Child` es 0 (compilando), no mata a nadie y lo informa. No
   depende de señales: no está verificado cómo corta Claude Code un comando en segundo
   plano (si manda SIGTERM al grupo, SIGKILL o nada a los nietos), y con `go run` hay
   tres procesos (go, la vista previa, el dashboard).
5. **API simulada (respuesta para que se vean los datos copiados)**:
   - `channels` (cualquier `id` o `forHandle`): `snippet.title = "Vista previa (datos
     copiados)"`, `relatedPlaylists.uploads = "UUvistaprevia"`. El encabezado del
     dashboard queda con ese nombre: nadie la confunde con el real.
   - `playlistItems`: `UULVvistaprevia` → vivos (videos con chat), más reciente primero,
     así el vivo por defecto es el más activo. `UUvistaprevia` → videos con solo
     comentarios, hasta `maxResults`.
   - `videos?id=…`: por ID pedido, como en CA9. Título:
     `"VISTA PREVIA en VIVO | <último Program>"` (o `"Video <ID>"` si no hay programa),
     `channelTitle: "Vista previa"`. Con eso `LiveLabel` da "VISTA PREVIA en VIVO" y,
     si hay dos vivos, se distinguen por el programa (`pickLive`).
   - Cualquier otra ruta (incluidos `liveChat/*` y `commentThreads`): 404. No deberían
     llegar, porque sin `activeLiveChatId` y con `commentCount` 0 el worker no los pide.
   - Resultado: el worker solo hace la revisión de videos cada 3 min (3 unidades de
     mentira en la copia de la cuota), los vivos de la copia aparecen "en vivo" con su
     chat guardado, los análisis y la participación de las últimas 24 h, y la serie de
     espectadores copiada. Limitación conocida: si el último mensaje copiado tiene más de
     24 h, los análisis del vivo salen vacíos (es lo mismo que haría el dashboard real).
6. **Skill, paso nuevo "7b. Vista previa (a pedido)"** entre el 7 y el 8:
   1. Solo si el cambio toca el dashboard o lo que muestra (`server.go`, `templates/`,
      `analyze.go`, `classify.go`, `store.go`, `worker.go`, `youtube.go`); para cambios
      de docs o de `tools/` se saltea. Preguntar: "¿Querés ver el cambio en una vista
      previa en http://localhost:8081? Usa una copia temporal de tus datos y una API
      simulada: no gasta cuota ni toca datos.jsonl." Sin un sí, seguir al paso 8.
   2. Con el sí, en segundo plano y desde la rama de la tarea:
      `go run ./tools/preview -datos "$(git worktree list --porcelain | sed -n '1s/^worktree //p')/datos.jsonl"`
      (la primera entrada de `git worktree list` es el checkout principal, donde está
      `datos.jsonl` aunque la sesión corra en un worktree). Esperar la línea "Vista
      previa en http://…" y pasarle el link a la persona.
   3. Cuando la persona diga que ya está, y **siempre** antes de cerrar el paso 8,
      cambiar de rama o pasar a otra tarea: `go run ./tools/preview -cortar` y mostrar su
      salida. Si la persona quiere seguir mirándola después del cierre, decirle que la
      corte ella con ese comando.
   4. No se anota en `job.json` (no hay etapa nueva).
7. **CLAUDE.md**: en "Prohibido sin pedido explícito…" agregar la excepción "salvo la
   vista previa del paso 7b de `/factory` (`go run ./tools/preview`), que la persona pide,
   usa una copia temporal y una API simulada y se corta con `-cortar` antes de terminar";
   `tools/preview/` en "Estructura"; en "Flujo de la software factory", la columna
   "Mergeado" y la vista previa. **AGENTS.md**: en "Comandos", cómo probar con
   `tools/preview`; en "Datos y privacidad", que la única copia permitida de los datos es
   la temporal de la vista previa (carpeta `0700`, se borra al cortar, `OpenStore`
   aplica la retención igual) y que nunca se usa en tests. README no cambia: no hay flags
   ni comportamiento nuevos del producto.
8. **ADR 0003** (`docs/decisions/0003-tablero-consulta-github-y-vista-previa.md`,
   `Estado: propuesta` hasta el merge): el tablero deja de ser "solo lee archivos": lee
   archivos y consulta GitHub con `gh` (solo lectura, con caché, sin escribir
   `job.json`); y la vista previa como herramienta aparte. En ADR 0002, en `Estado`:
   "aceptada; complementada por ADR 0003". Se elige un ADR nuevo y no editar el 0002
   porque cambia una decisión explícita (sin red) y deja rastro de por qué.

Alternativas descartadas (parte 2):

- Agregar un flag `-datos` a `yt-monitor`: cambia el producto (README, tests) sin
  necesidad; el directorio de trabajo ya alcanza.
- Pasar `-vivos <IDs>` al binario en vez de simular `playlistItems`: igual hace falta el
  falso de `channels` y `videos`, y el resultado es el mismo.
- Que el falso devuelva `activeLiveChatId` y responda el chat vacío: el worker abriría
  streaming contra el falso y el banner diría cosas de conexión que confunden.
- Script de shell: no anda en Windows y el hook bloquea `rm -rf`, que haría falta para
  limpiar.
- Servirlo desde `tools/factory-board`: mezcla el tablero con correr binarios de ramas.
- Correr `go run .` de la rama directamente: compila en un temporal de `go` que no
  controlamos y deja un proceso más entre la vista previa y el dashboard.

## Fuera de alcance

- Reiniciar automáticamente el dashboard real (`-dashboard :8080` contra la API real)
  después del merge: lo hace la persona; el paso 8 se lo recuerda.
- Escribir `job.json` desde el tablero o agregar la etapa `mergeado` al orquestador.
- Mostrar en el tablero que hay una vista previa corriendo, o lanzarla desde el tablero.
- Archivar o esconder tareas viejas de "Mergeado".
- Recibir mensajes nuevos en la vista previa (es una foto de los datos al copiarlos).
- Agregar el chequeo de `Host` (`localOnly`) al dashboard de `yt-monitor`: se vio que el
  tablero lo tiene y el dashboard no, pero es otro cambio.
- Cualquier cambio en `main.go`, `store.go`, `worker.go`, `server.go` o templates del
  producto.

## Riesgos

- **Privacidad (copia de datos reales).** Impacto: alto si la copia queda en una carpeta
  compartida. Mitigación: carpeta privada `0700` en el cache del usuario (no `/tmp`),
  copias `0700`, borrado en todo camino de salida, `-cortar` que no depende de señales y
  barrido de copias huérfanas al arrancar. Se verifica con CA10, CA11, CA12 y el `ls`
  de CA14. Queda un caso no cubierto: si la máquina se apaga con la vista previa abierta,
  la copia queda en la carpeta privada hasta la próxima vez que se arranque una vista
  previa; se documenta en el ADR.
- **Cuota y clave.** La vista previa no debe llegar a YouTube. Mitigación: entorno del
  hijo sin `YT_API_KEY`/`YT_BASE_URL` heredados, clave falsa, directorio de trabajo sin el
  archivo de la clave; CA10 comprueba que el falso nunca ve la clave testigo. `gh` en el
  tablero no gasta cuota de YouTube.
- **Exposición en la red.** El dashboard no tiene autenticación ni chequeo de `Host`.
  Mitigación: `-addr` restringido a loopback (CA8). El riesgo de DNS rebinding es el
  mismo que ya tiene el dashboard en `:8080` (fuera de alcance).
- **Archivos reales.** La vista previa solo lee `datos.*`; el worker escribe en la copia.
  CA10 compara bytes y fecha de modificación de los originales.
- **Tests que llaman a GitHub o tardan.** `TestRoutes`/`TestLocalOnly` usan `NewBoard`
  con `gh` real: hoy ninguna tarea de `fakeRepo` está en `pr-abierto` con URL válida, pero
  el implementer inyecta una consulta falsa en todos los tests de rutas para no depender
  de eso. `TestPreviewEndToEnd` compila el binario (con caché de `go`, unos segundos) y
  corre en `-count=5`; si resulta lento o inestable, se mide con
  `go test -count=5 -run '^TestPreviewEndToEnd$' ./tools/preview/` y se ajusta la espera,
  no se saltea.
- **Concurrencia.** La caché de PR y la goroutine de refresco van bajo `prMu`; CA3 corre
  con `-race`. La vista previa no comparte memoria con el dashboard (son procesos).
- **Windows.** `tools/preview` tiene que compilar (gate) y usa `Kill` + `Wait` antes de
  borrar. No se prueba en Windows de verdad: la vista previa la corre el orquestador en
  Linux/WSL.
- **`gh` sin sesión o sin instalar.** La tarjeta queda en "PR abierto" con el aviso y se
  reintenta cada 2 min, sin prompts (CA1, CA4).
- **PID reutilizado.** `-cortar` en el camino de respaldo mata `Child`; si el sistema ya
  lo reasignó a otro proceso, mataría a ese. Mitigación: en Linux solo se mata si el PID
  tiene el directorio de trabajo, el ejecutable o un archivo abierto dentro de la copia,
  y la misma comprobación decide si un estado es de una vista previa viva (un PID
  reasignado ya no bloquea). En Windows no se puede comprobar: nunca se mata de
  respaldo y se informa el PID; un PID reasignado puede hacer que la próxima vista
  previa diga que ya hay una corriendo: `-cortar`, a los 15 s y sin matar a nadie, borra
  la copia si se deja borrar (en Windows no se puede borrar el `.exe` de un proceso
  vivo, así que si se borra nadie la usaba) y destraba el estado. Esa rama no tiene test
  (solo corre fuera de Linux).

## Preguntas abiertas

Resueltas al aprobar (Julian Barbieri, 2026-10-30): se toman las recomendaciones tal
cual. 1: aviso y queda en "PR abierto". 2: se copia la cuota con el ajuste de CA13. 3:
solo si el diff toca el dashboard o lo que muestra. 4: sí, `rehacer` sobre un PR
mergeado para. 5: `TestPreviewEndToEnd` siempre activo (con `-count=5` tarda unos 8 s).

Verificado al implementar: `gh pr view https://github.com/julian-barbieri/chat-analytics/pull/7 --json state,mergedAt`
devolvió `{"mergedAt":"2026-10-30T19:26:22Z","state":"MERGED"}` (gh 2.46.0).

1. **PR cerrado sin merge:** ¿queda en "PR abierto" con el aviso (propuesta) o pasa a
   "Descartadas"? Recomiendo el aviso: cerrar puede ser para rehacer, y descartar lo
   decide la persona con `/factory`.
2. **Cuota en la vista previa:** pediste copiar `datos.cuota.json`. Lo copio, pero si ya
   está en el tope del día la vista previa sube `-cuota` (CA13) para no quedar sin vivos,
   y el cartel de cuota muestra ese tope alto. La alternativa es no copiarla (el cartel
   arranca de 0). Recomiendo copiarla con el ajuste de CA13.
3. **¿Cuándo ofrecer la vista previa?** Propuesta: solo si el diff toca archivos del
   dashboard o de lo que muestra (lista del paso 7b). Alternativa: ofrecerla siempre.
4. **`rehacer` con PR mergeado:** propongo que pare y pida una tarea nueva (CA6). Es un
   cambio chico al skill que no pediste; confirmá si lo querés en esta tarea.
5. **Tiempo de `-count=5`:** `TestPreviewEndToEnd` compila y levanta un proceso en cada
   corrida. Si preferís, puede saltearse con `testing.Short()` y correr completo en CI;
   recomiendo dejarlo siempre activo porque es el único que prueba que no se tocan los
   datos reales.
