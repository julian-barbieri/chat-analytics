# CLAUDE.md

Memoria del proyecto para agentes. Las reglas de dominio (cuota de YouTube, vivos,
privacidad, clasificador, concurrencia) están en `AGENTS.md` y se importan acá:

@AGENTS.md

## Propósito

`yt-monitor`: bot que **no escribe en YouTube** y monitorea comentarios y chat en vivo de
canales de YouTube (piloto de Alphamedia con Radio Rivadavia). Dos modos con el mismo
binario: informe puntual (`go run .`) y continuo con dashboard htmx de cinco pantallas:
En vivo, Programas, Comentarios, Sin vivo y Canales (`-dashboard :8080`).
No publica, no modera, no usa OAuth. El dashboard escribe en cuatro puntos, solo desde la PC
del monitor (`sameOrigin` + `localRequest`): la configuración de canales e intervalos de la
pantalla Canales, incluida la tarjeta de Claude (`datos.canales.json`, ADR 0005 y 0009), pedir un análisis a Claude (cola),
marcar una pregunta agrupada como respondida en En vivo (`datos.respondidas.jsonl`, una marca local)
y marcar una recomendación como Hecha / No sirve (`datos.recmarcas.jsonl`, ADR 0011).
Si se activa (`CLAUDE_API_KEY` y un tope > 0, de `-claude-usd` o de Canales), el dashboard
manda el **texto** de los mensajes de chat (sin autores) a la API de Anthropic para resumir
cada programa (ADR 0006), etiquetar el chat en tandas (ADR 0009) y redactar las recomendaciones por rol (ADR 0011). Sigue sin escribir en YouTube.

## Comandos exactos

Todos verificados en Go 1.26 (el mínimo es el de `go.mod`). Ninguno toca la red de
YouTube ni necesita API key.

| Para qué | Comando |
|---|---|
| Build | `go build ./...` |
| Build Windows (incluye el tablero) | `GOOS=windows GOARCH=amd64 go build ./...` |
| Formato (debe no listar nada) | `gofmt -l .` |
| Typecheck + análisis estático | `go vet ./...` |
| Lint | `go run honnef.co/go/tools/cmd/staticcheck@2026.2.2 ./...` |
| Tests | `go test -count=1 ./...` |
| Tests con detector de carreras | `go test -race -count=1 ./...` |
| Detectar tests intermitentes | `go test -count=5 ./...` |
| Un test | `go test -count=1 -run '^TestNombre$' ./...` |
| Vulnerabilidades | `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...` |
| Gates rápidos (formato, vet, tests) | `scripts/gates.sh` |
| Todos los gates, con tabla para el PR | `scripts/gates.sh completo` |

`govulncheck` reporta vulnerabilidades de la **stdlib del Go instalado**: si falla
solo por eso, el arreglo es actualizar Go, no el código. Decilo en el PR.

Prohibido sin pedido explícito del usuario: `go run .` contra la API real (gasta cuota
compartida, escribe `informes/` y `datos.jsonl`), cualquier corrida con `-claude-usd` > 0
(o con un tope guardado en Canales) contra la API real de Anthropic (gasta plata y manda
mensajes de usuarios; las tandas gastan todo el día) y cualquier
`-dashboard` que quede corriendo, salvo la vista previa del paso 7b de `/factory` o la de
`/implementar` (`go run ./tools/preview`), que la persona pide (correr `/implementar` ya es el pedido), usa una copia temporal y una API de
YouTube y de Claude simulada (saca las dos claves del entorno) y se corta con `-cortar`
antes de terminar. Para probar a mano: `YT_BASE_URL=http://localhost:9999 YT_API_KEY=x go run . ...`
(con Claude, también `CLAUDE_BASE_URL=http://localhost:9998 CLAUDE_API_KEY=x`; las dos
`*_BASE_URL` existen solo para pruebas).

## Estructura

```
*.go                 paquete main en la raíz (mapa por archivo en AGENTS.md)
monitor_test.go      todos los tests; arman sus propios datos y API falsa (httptest)
templates/, static/  embebidos con //go:embed (htmx vendorizado: no editarlo)
docs/specs/          una spec por tarea (plantilla: SPEC_TEMPLATE.md)
docs/decisions/      ADRs numerados (plantilla: ADR_TEMPLATE.md)
.claude/agents/      agentes especializados: triage, implementer, tester, reviewer
.claude/skills/      procedimientos repetibles (/factory, /fix-bug; flujo manual: /idea, /implementar, /revisar, /push)
scripts/gates.sh     gates locales: rápido o completo (tabla en Markdown para el PR)
.github/workflows/   CI (desactivado en GitHub; los gates se corren en local)
tools/factory-board/ tablero de la factory (package main aparte, no va en el binario)
tools/preview/       vista previa del dashboard de una rama con datos copiados y APIs simuladas (ADR 0003, 0006)
factory/             estado (jobs/), actividad (events/) y evidencia del flujo manual (manual/); en .gitignore; el tablero puede borrar con el tacho las tareas en `pr-abierto`
```

## Convenciones

- Go solo con biblioteca estándar: `go.mod` sin `require`. Nada de dependencias nuevas.
  Las herramientas (`staticcheck`, `govulncheck`) se corren con `go run pkg@versión`.
- Comentarios, logs, errores, textos de UI, specs y ADRs **en español rioplatense**;
  identificadores de Go en inglés. Detalle de estilo en `AGENTS.md` → "Estilo".
- Código corto, funciones chicas, comentarios que explican el *por qué*.
- Tests: tabla de casos cuando aplica, sin red, sin `datos.jsonl` como fixture. Si un
  test arranca goroutines (por ejemplo `RunWorker`), debe dejarlas terminadas antes de
  que corra el cleanup de `t.TempDir()`.

## Git

- **Nunca commitear ni pushear a `main`**. Una rama por tarea, desde `main` actualizado:
  `fix/<slug>`, `feat/<slug>`, `chore/<slug>`, `docs/<slug>`, `test/<slug>`.
- Commits en Conventional Commits, en español e imperativo:
  `fix: cortar el worker al terminar el test`. Cuerpo opcional con el *por qué*.
- Todo cambio entra por PR a `main` con la spec enlazada y la evidencia de tests.
- Nada de `push --force`, `reset --hard` sobre trabajo ajeno ni reescribir `main`.

## Qué NO tocar

- `.env` (tiene `YT_API_KEY` y `CLAUDE_API_KEY`): no leer, no imprimir, no editar, no commitear.
- `datos.jsonl`, `datos.*.json`, `datos.*.jsonl` (por ejemplo `datos.espectadores.jsonl`), `informes/`: datos reales de usuarios y de la API. No editar, no
  usar en tests, no subir. Están en `.gitignore`.
- `static/htmx.min.js` (vendorizado), la retención de 30 días de `store.go`, el default
  `localhost` del dashboard.
- Fuera de alcance sin consultar: escribir en YouTube/OAuth, base de datos, Docker,
  frontend con build, autenticación o exponer el dashboard, `search.list` (100 unidades).

## Definición de "terminado"

Una tarea está terminada solo si se cumple **todo**:

1. Hay una spec en `docs/specs/` con criterios de aceptación verificables, y cada
   criterio tiene un test o una verificación que lo cubre.
2. Para bugs: existe un test de regresión que falló antes del arreglo (se guardó la
   salida) y pasa después.
3. `gofmt -l .` vacío, `go vet ./...` y `staticcheck` limpios, build de Linux y de
   Windows OK.
4. `go test -race -count=1 ./...` y `go test -count=5 ./...` en verde.
5. Lo pedido en "Antes de dar algo por terminado" de `AGENTS.md` (README si cambian
   flags/cuota, navegador si cambió el dashboard, `TestClassify` si cambió el clasificador).
6. Con `/factory`: revisión del agente `reviewer` sin hallazgos bloqueantes pendientes.
   Con el flujo manual, `/revisar` es recomendado y no obligatorio: si no se corrió, el
   PR lo dice (ADR 0008).
7. PR abierto contra `main` con resumen, link a la spec y evidencia de tests (salida de
   los gates locales de los puntos 3 y 4: el CI de GitHub está desactivado).

## Quality gates automáticos

- El CI de GitHub (`.github/workflows/ci.yml`) está **desactivado** a pedido del usuario,
  para mergear a `main` sin esperar checks. Los gates de la tabla de comandos se corren
  en local (`scripts/gates.sh completo`: paso 6 de `/factory` o `/push`) y su salida va en el PR. Para volver a prenderlo:
  `gh workflow enable CI`.
- Hooks de `.claude/settings.json`: después de editar un `.go`, `gofmt -w` y `go vet`
  (si vet falla, el error vuelve al agente). Antes de cada comando se bloquean `rm -rf`,
  `git push` forzado o a `main`, `git commit` en `main` (la rama de la carpeta donde
  corre el comando: en un worktree, la de la tarea), `git reset --hard`,
  `git clean -f` y cualquier mención de `.env`; Read/Edit/Write de `.env` también.
  Si un hook te bloquea, no lo esquives: cambiá el enfoque o preguntá.
- `log-activity.sh` (async, no bloquea) registra cada herramienta y cada subagente en
  `factory/events/AAAA-MM-DD.jsonl`, solo con resúmenes cortos; borra lo de más de 30
  días.

## Flujo manual (el de todos los días)

Para validar rápido, la persona corre cada paso y mira el resultado antes del siguiente
(ADR 0008). Todo pasa en la sesión principal, sin subagentes salvo `/revisar`:

- `/idea <tarea>`: rama desde `origin/main` y spec corta en `docs/specs/` (borrador
  commiteado). No escribe código.
- `/implementar [nota]`: correrlo aprueba la spec. TDD (rojo, código, verde) y
  `scripts/gates.sh`. No commitea el código. Levanta sola la vista previa en
  `http://localhost:8081` (si el cambio llega al binario) con una lista de qué probar,
  y la reinicia en cada pasada. Con una nota o hallazgos, corrige solo eso.
- `/revisar [foco]` (opcional): el agente `reviewer` lee el diff; la persona elige qué
  se arregla y se arregla en la misma sesión.
- `/push`: corta la vista previa, `scripts/gates.sh completo` una sola vez, commit, push y PR.

La evidencia (rojo, verde, revisión, gates) queda en `factory/manual/<slug>/`, en
`.gitignore`; estas tareas no aparecen en el tablero. Para algo largo que pueda correr
solo, `/factory`.

## Flujo de la software factory

- Cualquier tarea: `/factory <tarea>` (triage → spec aprobada por una persona →
  implementer → tester → reviewer → gates → PR). Bugs con stack trace o test que
  falla: `/fix-bug <descripción o traza>`, que es lo mismo con tipo `fix`. Retomar:
  `/factory aprobar <id>`, `/factory rehacer <id> <nota>`; ver todo: `/factory estado`.
- Estado de cada tarea en `factory/jobs/<id>/job.json` (lo escribe solo el
  orquestador) y actividad de los agentes en `factory/events/` (hook async
  `log-activity.sh`). Ver ADR 0002.
- Tablero: `go run ./tools/factory-board` y abrir `http://localhost:8090`. Lo levanta
  una persona; los agentes no lo dejan corriendo. Una tarea en `pr-abierto` pasa sola a
  la columna "Mergeado" cuando su PR entra a `main`: el tablero lo consulta con `gh`
  (solo lectura, cada 2 min) y no escribe `job.json` (ADR 0003). Desde el tablero una
  persona puede borrar una tarea en `pr-abierto` (también si ya figura en "Mergeado")
  con el tacho; no se puede deshacer y no toca el PR, la rama ni la spec (ADR 0004).
- Vista previa del PR (paso 7b de `/factory`, a pedido de la persona y solo si el cambio
  toca el dashboard o lo que muestra): `go run ./tools/preview -datos <checkout
  principal>/datos.jsonl` levanta la rama en `http://localhost:8081` con una copia
  temporal de los datos (`datos.jsonl`, `datos.cuota.json`, `datos.espectadores.jsonl`,
  `datos.vivos.jsonl`, `datos.videos.jsonl`, `datos.likes.jsonl`, `datos.sinvivo.jsonl`, `datos.analisis.jsonl`, `datos.etiquetas.jsonl` y `datos.recomendaciones.jsonl`; no `datos.claude.json` ni
  `datos.canales.json`) y una API simulada; `go run ./tools/preview -cortar` la corta y
  borra la copia. Siempre se corta antes de cerrar la tarea.
- Modelo y esfuerzo fijos en el frontmatter de cada agente (no heredan la sesión):
  triage, implementer y reviewer en `claude-opus-5-5` con esfuerzo `high`; tester en
  `claude-sonnet-5-5` con `medium`, porque sobre todo corre comandos y suma tests
  acotados. La orquestación y los gates usan el modelo de la sesión.
- Decisiones de diseño que no son obvias desde el código: un ADR en `docs/decisions/`.
- Si un agente no puede verificar un comando, pregunta; no inventa.
