# CLAUDE.md

Memoria del proyecto para agentes. Las reglas de dominio (cuota de YouTube, vivos,
privacidad, clasificador, concurrencia) están en `AGENTS.md` y se importan acá:

@AGENTS.md

## Propósito

`yt-monitor`: bot de **solo lectura** que monitorea comentarios y chat en vivo de
canales de YouTube (piloto de Alphamedia con Radio Rivadavia). Dos modos con el mismo
binario: informe puntual (`go run .`) y continuo con dashboard htmx (`-dashboard :8080`).
No publica, no modera, no usa OAuth.

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

`govulncheck` reporta vulnerabilidades de la **stdlib del Go instalado**: si falla
solo por eso, el arreglo es actualizar Go, no el código. Decilo en el PR.

Prohibido sin pedido explícito del usuario: `go run .` contra la API real (gasta cuota
compartida, escribe `informes/` y `datos.jsonl`) y cualquier `-dashboard` que quede
corriendo. Para probar a mano: `YT_BASE_URL=http://localhost:9999 YT_API_KEY=x go run . ...`.

## Estructura

```
*.go                 paquete main en la raíz (mapa por archivo en AGENTS.md)
monitor_test.go      todos los tests; arman sus propios datos y API falsa (httptest)
templates/, static/  embebidos con //go:embed (htmx vendorizado: no editarlo)
docs/specs/          una spec por tarea (plantilla: SPEC_TEMPLATE.md)
docs/decisions/      ADRs numerados (plantilla: ADR_TEMPLATE.md)
.claude/agents/      agentes especializados: triage, implementer, tester, reviewer
.claude/skills/      procedimientos repetibles (/factory, /fix-bug)
.github/workflows/   CI: quality gates en cada PR
tools/factory-board/ tablero de la factory (package main aparte, no va en el binario)
factory/             estado (jobs/) y actividad (events/) de la factory; en .gitignore
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

- `.env` (tiene `YT_API_KEY`): no leer, no imprimir, no editar, no commitear.
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
6. Revisión del agente `reviewer` sin hallazgos bloqueantes pendientes.
7. PR abierto contra `main` con resumen, link a la spec y evidencia de tests; CI verde.

## Quality gates automáticos

- CI (`.github/workflows/ci.yml`) corre la tabla de comandos en cada PR y push a main.
- Hooks de `.claude/settings.json`: después de editar un `.go`, `gofmt -w` y `go vet`
  (si vet falla, el error vuelve al agente). Antes de cada comando se bloquean `rm -rf`,
  `git push` forzado o a `main`, `git commit` en `main`, `git reset --hard`,
  `git clean -f` y cualquier mención de `.env`; Read/Edit/Write de `.env` también.
  Si un hook te bloquea, no lo esquives: cambiá el enfoque o preguntá.
- `log-activity.sh` (async, no bloquea) registra cada herramienta y cada subagente en
  `factory/events/AAAA-MM-DD.jsonl`, solo con resúmenes cortos; borra lo de más de 30
  días.

## Flujo de la software factory

- Cualquier tarea: `/factory <tarea>` (triage → spec aprobada por una persona →
  implementer → tester → reviewer → gates → PR). Bugs con stack trace o test que
  falla: `/fix-bug <descripción o traza>`, que es lo mismo con tipo `fix`. Retomar:
  `/factory aprobar <id>`, `/factory rehacer <id> <nota>`; ver todo: `/factory estado`.
- Estado de cada tarea en `factory/jobs/<id>/job.json` (lo escribe solo el
  orquestador) y actividad de los agentes en `factory/events/` (hook async
  `log-activity.sh`). Ver ADR 0002.
- Tablero: `go run ./tools/factory-board` y abrir `http://localhost:8090`. Lo levanta
  una persona; los agentes no lo dejan corriendo.
- Modelo y esfuerzo fijos en el frontmatter de cada agente (no heredan la sesión):
  triage, implementer y reviewer en `claude-opus-5-5` con esfuerzo `high`; tester en
  `claude-sonnet-5-5` con `medium`, porque sobre todo corre comandos y suma tests
  acotados. La orquestación y los gates usan el modelo de la sesión.
- Decisiones de diseño que no son obvias desde el código: un ADR en `docs/decisions/`.
- Si un agente no puede verificar un comando, pregunta; no inventa.
