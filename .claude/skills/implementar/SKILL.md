---
name: implementar
description: Segundo paso del flujo manual de yt-monitor. Implementa la spec de la rama actual con TDD (test en rojo, código, verde) en esta sesión y corre los gates rápidos. Correrlo aprueba la spec.
argument-hint: "[ruta de la spec] [nota o hallazgos a corregir]"
---

# /implementar

Paso 2 del flujo manual (`/idea` → `/implementar` → `/revisar` → `/push`). Trabajás
**en esta sesión, sin subagentes**. Que la persona corra este comando es la aprobación
de la spec.

Argumentos:

$ARGUMENTS

## Pasos

1. Ubicá la spec: la ruta de los argumentos o, si no hay, el `docs/specs/*.md` que la
   rama agregó contra `origin/main`
   (`git diff --name-only origin/main...HEAD -- docs/specs/`). Si no hay ninguna o hay
   varias, preguntá. Si la rama es `main`, pará.
2. Si la spec dice `Estado: borrador`, poné `Estado: aprobada` y
   `Aprobada por / fecha: <nombre de git>, <hoy>` y commiteala
   (`docs: aprobar spec de <slug>`).
3. Carpeta de evidencia (en `.gitignore`):
   `mkdir -p factory/manual/<slug>`, con `<slug>` la rama sin el prefijo `<tipo>/`.
4. **Rojo:** escribí primero el test de los criterios (en `monitor_test.go` o el
   `_test.go` que corresponda, con tabla de casos si aplica, sin red ni `datos.jsonl`).
   Corrélo y guardá la salida: `go test -count=1 -run '^TestX$' ./... 2>&1 | tee
   factory/manual/<slug>/rojo.txt`. Tiene que fallar por lo que dice la spec, no por
   no compilar; si no falla, el test no prueba el cambio: arreglalo antes de seguir.
5. **Código:** el cambio mínimo de la spec, respetando `AGENTS.md` (cuota con `spend`,
   concurrencia, retención, español rioplatense, tokens CSS).
6. **Verde:** el mismo test, con la salida en `factory/manual/<slug>/verde.txt`.
7. `scripts/gates.sh` (rápido: formato, vet y tests). Si falla, arreglalo acá. La suite
   pesada (`-race`, `-count=5`, lint, Windows) corre una sola vez en `/push`.
8. Lo que pida "Antes de dar algo por terminado" de `AGENTS.md`: casos en
   `TestClassify` si tocaste el clasificador, el servidor falso de
   `TestFlowAgainstFakeAPI` si tocaste el cliente, `README.md` si cambian flags, cuota o
   comportamiento visible.
9. Si es la segunda pasada (vienen hallazgos de `/revisar` o una nota), corregí solo eso
   y repetí 6 a 8.

**No commitees** el código: la persona lo valida en el diff y `/push` hace el commit.

## Al terminar

Mostrá en pocas líneas: qué archivos cambiaste y por qué, el rojo y el verde (una línea
de cada uno) y la tabla de gates rápidos. Si el diff toca el dashboard o lo que muestra
(`main.go`, `server.go`, `programs.go`, `templates/`, `analyze.go`, `classify.go`,
`store.go`, `worker.go`, `youtube.go`), ofrecé la vista previa tal como dice el paso 7b
de `.claude/skills/factory/SKILL.md` (con la misma regla de cortarla siempre). Cerrá
diciendo que siga con `/revisar` o directo con `/push`.
