---
name: fix-bug
description: Arregla un bug de yt-monitor de punta a punta (triage → spec aprobada → implementer → tester → reviewer → PR) en una rama nueva. Usalo cuando haya un stack trace, un test que falla o un bug reproducible.
argument-hint: <descripción del bug, stack trace o test que falla>
---

# /fix-bug

Orquestás el arreglo de un bug con los agentes de `.claude/agents/`. Vos (la sesión
principal) manejás git, la aprobación humana y el PR; los agentes hacen el trabajo. Todo
queda registrado en la spec, los commits y el PR.

Cada agente se lanza con la herramienta Agent y `subagent_type` igual a su nombre
(`triage`, `implementer`, `tester`, `reviewer`). No conocen esta conversación: pasales
todo lo que necesitan (reporte, ruta de la spec, hallazgos, diff) en el prompt. Los
agentes no pueden lanzar otros agentes, así que el orden lo llevás vos.

Bug reportado:

$ARGUMENTS

Si no hay descripción, pedila y pará.

## 0. Preparación

1. `git status --porcelain` tiene que salir vacío. Si hay cambios, pará y preguntá.
2. Actualizá main y abrí la rama de la tarea (nunca trabajes sobre `main`):

   ```bash
   git fetch origin
   git switch main
   git pull --ff-only
   git switch -c fix/<slug>
   ```

   `<slug>`: 2 a 5 palabras en minúscula con guiones, describiendo el bug.

## 1. Triage

Lanzá el agente `triage` con el reporte completo (texto y traza tal como llegaron), el
slug y la fecha de hoy. Pedile la spec en `docs/specs/<AAAA-MM-DD>-<slug>.md`.

Cuando vuelva, leé la spec. Si no reprodujo el bug o la causa es solo una hipótesis,
decíselo a la persona tal cual.

## 2. Aprobación de la spec (gate humano)

Mostrá a la persona: causa raíz, criterios de aceptación, diseño propuesto, fuera de
alcance y riesgos, con el link a la spec. Preguntá si la aprueba. **No sigas sin un sí
explícito.** Si pide cambios, editá la spec (o volvé a lanzar `triage`) y preguntá de
nuevo.

Con el sí: en la spec poné `Estado: aprobada` y `Aprobada por / fecha` con lo que dijo
la persona, y commiteá solo la spec:

```bash
git add docs/specs/<archivo>.md
git commit -m "docs: spec de <slug>"
```

## 3. Implementación (TDD)

Lanzá `implementer` con la ruta de la spec. Su resultado tiene que traer la salida del
test en **rojo** antes del arreglo y en **verde** después. Si falta el rojo, pedíselo de
nuevo: sin esa evidencia no hay PR.

## 4. Tests

Lanzá `tester` con la ruta de la spec. Si encuentra un bug en código de producción o una
falla, volvé al paso 3 pasándole el hallazgo a `implementer`.

## 5. Revisión

Armá el diff contra main, incluido lo no commiteado:

```bash
git add -A
git diff --cached main
```

Revisá que en el diff no aparezcan `.env`, `datos.jsonl`, `informes/` ni binarios. Lanzá
`reviewer` con la ruta de la spec y el **diff completo pegado en el prompt** (no tiene
Bash).

- `CAMBIOS REQUERIDOS` o algún BLOQUEANTE: volvé al paso 3 con los hallazgos y después
  repetí 4 y 5. Máximo **2 vueltas**; si siguen los bloqueantes, pará y mostrale a la
  persona dónde se trabó.
- IMPORTANTE: arreglalo en la misma vuelta si es chico; si no, listalo en el PR.
- MENOR: listalo en el PR.

## 6. Gates finales (los corrés vos)

No te quedes con lo que dicen los agentes: corré y guardá la salida recortada de cada uno.

```bash
gofmt -l .
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@2026.2.2 ./...
go build ./...
GOOS=windows GOARCH=amd64 go build -o /dev/null .
go test -race -count=1 ./...
go test -count=5 ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```

Todos tienen que pasar, salvo `govulncheck` cuando falla solo por la stdlib del Go
instalado: en ese caso se aclara en el PR (ver `CLAUDE.md`).

## 7. Commit y PR

1. Commit en Conventional Commits, en español, con el *por qué* en el cuerpo y las líneas
   de atribución que pida la sesión:

   ```bash
   git commit -m "fix: <qué se arregla>" -m "<causa y solución en 2-3 líneas>"
   git push -u origin fix/<slug>
   ```

2. Escribí el cuerpo del PR en un archivo temporal (`mktemp`) siguiendo
   [pr-template.md](pr-template.md) y abrilo:

   ```bash
   gh pr create --base main --head fix/<slug> --title "fix: <qué se arregla>" --body-file <archivo>
   ```

3. En la spec, si quedó algo sin marcar, decí por qué en el PR.

## 8. Cierre

Respondé con el link al PR, la causa raíz en una línea, los gates y los hallazgos que
quedaron abiertos. No mergees: el merge lo decide una persona después de que el CI pase.
