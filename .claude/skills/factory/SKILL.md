---
name: factory
description: Lleva una tarea de yt-monitor (bug, mejora o tarea) de punta a punta con los agentes (triage → spec aprobada → implementer → tester → reviewer → gates → PR) y deja su estado en factory/jobs/ para el tablero. También aprueba, rehace o lista tareas.
argument-hint: <tarea> | aprobar <id> | rehacer <id> <nota> | estado
---

# /factory

Orquestás una tarea con los agentes de `.claude/agents/`. Vos (la sesión principal)
manejás git, la aprobación humana, el PR y **el estado de la tarea**; los agentes hacen
el trabajo. Una persona sigue todo en el tablero (`go run ./tools/factory-board`), que
lee lo que escribís en `factory/jobs/` y la actividad que dejan los hooks en
`factory/events/` (ADR 0002).

Cada agente se lanza con la herramienta Agent y `subagent_type` igual a su nombre
(`triage`, `implementer`, `tester`, `reviewer`). No conocen esta conversación: pasales
todo lo que necesitan (tarea, ruta de la spec, hallazgos, diff) en el prompt. Los
agentes no pueden lanzar otros agentes, así que el orden lo llevás vos.

Pedido:

$ARGUMENTS

Según cómo empieza:

- `aprobar <id>`: andá a **Retomar una tarea** y seguí en el paso 3.
- `rehacer <id> <nota>`: andá a **Retomar una tarea** y seguí en el paso 3 con la nota.
- `estado`: listá las tareas con
  `jq -r '[.id, .etapa, .vuelta, .rama, .pr, .motivo] | @tsv' factory/jobs/*/job.json`
  y pará. Con el glob y no leyendo cada `job.json` por separado, así el tablero no
  asocia esta sesión a una tarea.
- Cualquier otra cosa es una tarea nueva: seguí en el paso 0. Si está vacío, pedila y
  pará.

## Estado de la tarea: `factory/jobs/<id>/job.json`

Sos **el único** que escribe este archivo. Reescribilo entero en cada cambio de etapa:
primero con Write en `factory/jobs/<id>/job.json.tmp` y después
`mv factory/jobs/<id>/job.json.tmp factory/jobs/<id>/job.json`, así el tablero nunca lee
un archivo a medias. La hora sale de `date -Iseconds`.

```json
{
  "id": "003-chat-duplicado",
  "tipo": "fix",
  "tarea": "El chat muestra mensajes repetidos al reconectar",
  "etapa": "revision",
  "vuelta": 1,
  "rama": "fix/chat-duplicado",
  "spec": "docs/specs/2026-10-30-chat-duplicado.md",
  "revisiones": {"reviewer": "pendiente"},
  "pr": "",
  "motivo": "",
  "actualizada": "2026-10-30T15:02:11-03:00",
  "historial": [{"etapa": "triage", "at": "2026-10-30T14:50:02-03:00"}]
}
```

- `tipo`: `fix`, `feat`, `chore`, `docs` o `test` (es también el prefijo de la rama).
- `etapa`, en este orden: `triage`, `esperando-aprobacion`, `implementacion`, `tests`,
  `revision`, `gates`, `pr-abierto`. Fuera del camino feliz: `necesita-persona` (con
  `motivo`) y `descartada`. El tablero marca como error cualquier otro valor.
- `vuelta`: vuelta de revisión actual, 0 antes de la primera.
- `revisiones`: veredicto de cada revisor en la vuelta actual: `pendiente`, `APROBADO`,
  `APROBADO CON CAMBIOS MENORES` o `CAMBIOS REQUERIDOS`.
- `historial`: agregá `{etapa, at}` cada vez que cambia la etapa. No borres entradas.

Guardá también el resultado de cada agente, tal como volvió, en la carpeta de la tarea:
`triage.md`, `implementacion-<n>.md`, `tests-<n>.md`, `revision-<n>.md`, y la salida de
los gates en `gates-<n>.md`. `n` es el número de pasada por el paso 3: arranca en 1, sube
cada vez que volvés a implementar y **nunca se reinicia** (tampoco con `rehacer`), así
no se pisa nada; mirá el más alto que exista. Si hay una nota de `rehacer`, va en
`rehacer-<k>.md`. El tablero muestra esos archivos; no pongas ahí la API key ni datos de
`datos.jsonl`.

## 0. Preparación

1. `git status --porcelain` tiene que salir vacío. Si hay cambios, pará y preguntá.
   `factory/` está en `.gitignore`, así que el estado no cuenta.
2. Elegí `tipo` y un `<slug>` de 2 a 5 palabras en minúscula con guiones.
3. Creá la tarea: `mkdir -p factory/jobs` y después `mkdir factory/jobs/<NNN>-<slug>`,
   con `NNN` el número siguiente al más alto que exista (tres dígitos, desde `001`). Si
   el `mkdir` falla porque otra sesión lo tomó, probá con el número siguiente.
4. Escribí `job.json` con `etapa: triage`, `vuelta: 0` y `rama: <tipo>/<slug>`.
5. Abrí la rama desde el `main` actualizado (nunca trabajes sobre `main`). Desde
   `origin/main` y sin `git switch main`, que falla si `main` está abierto en otro
   worktree:

   ```bash
   git fetch origin
   git switch -c <tipo>/<slug> origin/main
   ```

   Si la sesión corre en un worktree, `factory/` queda dentro de ese worktree: el
   tablero se levanta con `-repo <ruta del worktree>`.

## 1. Triage

Lanzá `triage` con la tarea completa (texto y traza tal como llegaron), el tipo, el slug
y la fecha de hoy. Pedile la spec en `docs/specs/<AAAA-MM-DD>-<slug>.md`. Guardá su
resultado en `triage.md` y anotá `spec` en `job.json`.

Commiteá la spec en borrador enseguida, en la rama de la tarea. Así el checkout queda
limpio mientras se espera la aprobación: se puede arrancar otra tarea o aprobar desde
otra sesión, y la spec no se cuela en el diff de otra rama.

```bash
git add docs/specs/<archivo>.md
git commit -m "docs: spec de <slug> (borrador)"
```

Si no reprodujo el bug o la causa es solo una hipótesis, decíselo a la persona tal cual.

## 2. Aprobación de la spec (gate humano)

Pasá a `esperando-aprobacion`. Mostrá a la persona: causa raíz o motivo, criterios de
aceptación, diseño propuesto, fuera de alcance y riesgos, con el link a la spec.
Preguntá si la aprueba. **No sigas sin un sí explícito.** Puede contestar acá o más
tarde, desde cualquier sesión, con `/factory aprobar <id>`.

- Si pide cambios, editá la spec (o volvé a lanzar `triage`), commiteala
  (`docs: ajustar spec de <slug>`) para que el checkout quede limpio y preguntá de nuevo.
- Si la descarta, pasá a `descartada` con el `motivo` y pará.

Con el sí: en la spec poné `Estado: aprobada` y `Aprobada por / fecha` con lo que dijo
la persona, y commiteá solo la spec:

```bash
git add docs/specs/<archivo>.md
git commit -m "docs: aprobar spec de <slug>"
```

## 3. Implementación (TDD)

Pasá a `implementacion`. Lanzá `implementer` con la ruta de la spec (y, si venís de
`rehacer` o de una revisión, la nota o los hallazgos). Guardá el resultado en
`implementacion-<n>.md`. Tiene que traer la salida del test en **rojo** antes del cambio
y en **verde** después. Si falta el rojo, pedíselo de nuevo: sin esa evidencia no hay PR.

## 4. Tests

Pasá a `tests`. Lanzá `tester` con la ruta de la spec y guardá `tests-<n>.md`. Si
encuentra un bug en código de producción o una falla, volvé al paso 3 con el hallazgo.

## 5. Revisión

Pasá a `revision`, sumá 1 a `vuelta` y poné `revisiones.reviewer` en `pendiente`. Armá el
diff contra `origin/main` (el `main` local puede estar atrasado), incluido lo no
commiteado:

```bash
git add -A
git diff --cached origin/main
```

Revisá que en el diff no aparezcan `.env`, `datos.jsonl`, `informes/`, `factory/` ni
binarios. Lanzá `reviewer` con la ruta de la spec y el **diff completo pegado en el
prompt** (no tiene Bash). Guardá `revision-<n>.md` y anotá el veredicto en
`revisiones.reviewer`.

- `CAMBIOS REQUERIDOS` o algún BLOQUEANTE: volvé al paso 3 con los hallazgos y después
  repetí 4 y 5. Máximo **2 vueltas**; si siguen los bloqueantes, pasá a
  `necesita-persona` con el `motivo` y mostrale a la persona dónde se trabó.
- IMPORTANTE: arreglalo en la misma vuelta si es chico; si no, listalo en el PR.
- MENOR: listalo en el PR.

## 6. Gates finales (los corrés vos)

Pasá a `gates`. No te quedes con lo que dicen los agentes: corré y guardá en
`gates-<n>.md` la salida recortada de cada uno.

```bash
gofmt -l .
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@2026.2.2 ./...
go build ./...
GOOS=windows GOARCH=amd64 go build ./...
go test -race -count=1 ./...
go test -count=5 ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```

Todos tienen que pasar, salvo `govulncheck` cuando falla solo por la stdlib del Go
instalado: en ese caso se aclara en el PR (ver `CLAUDE.md`). Si falla otro, volvé al
paso 3.

## 7. Commit y PR

1. Commit en Conventional Commits, en español, con el *por qué* en el cuerpo y las líneas
   de atribución que pida la sesión:

   ```bash
   git commit -m "<tipo>: <qué cambia>" -m "<causa o motivo y solución en 2-3 líneas>"
   git push -u origin <tipo>/<slug>
   ```

2. Escribí el cuerpo del PR en un archivo temporal (`mktemp`) siguiendo
   [pr-template.md](pr-template.md) y abrilo:

   ```bash
   gh pr create --base main --head <tipo>/<slug> --title "<tipo>: <qué cambia>" --body-file <archivo>
   ```

3. Pasá a `pr-abierto` con la URL en `pr`. En la spec, si quedó algo sin marcar, decí
   por qué en el PR.

## 8. Cierre

Respondé con el link al PR, la causa raíz o el motivo en una línea, los gates y los
hallazgos que quedaron abiertos. No mergees: el merge lo decide una persona después de
que el CI pase.

## Retomar una tarea

1. Leé `factory/jobs/<id>/job.json`. Si no existe, listá las tareas y pará.
2. `git status --porcelain` vacío; si no, pará y preguntá. Después `git switch <rama>`.
3. Según el comando:
   - `aprobar`: solo si la etapa es `esperando-aprobacion`. Hacé lo del paso 2 con el
     sí (marcar y commitear la spec) y seguí en el paso 3.
   - `rehacer`: solo si la etapa es `necesita-persona` o `pr-abierto`. Guardá la nota en
     `rehacer-<k>.md` (con `k` el siguiente), poné `vuelta: 0` (vuelven a quedar 2
     vueltas de revisión; `n` sigue contando) y seguí en el paso 3 pasándole la nota a
     `implementer`. Si ya había PR, el push del paso 7 lo actualiza: no abras otro.
   - En otra etapa, decí en cuál está y pará.
