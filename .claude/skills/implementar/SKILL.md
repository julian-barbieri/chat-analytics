---
name: implementar
description: Segundo paso del flujo manual de yt-monitor. Implementa la spec de la rama actual con TDD (test en rojo, código, verde) en esta sesión, corre los gates rápidos y levanta la vista previa en localhost:8081 para validar a mano. Correrlo aprueba la spec.
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
10. **Vista previa para validar a mano**, sin preguntar: correr `/implementar` ya es el
    pedido de la persona (ADR 0008). Ver abajo.

**No commitees** el código: la persona lo valida en el diff y `/push` hace el commit.

## Vista previa

Se levanta siempre que el diff toque Go del paquete `main` o `templates/` (todo lo que
termina en el binario). Si solo tocó docs, `tools/` o tests, salteala y decí por qué.

1. La vista previa compila la rama **una vez al arrancar**: si ya había una de una
   pasada anterior, cortala primero para que muestre el código nuevo:

   ```bash
   go run ./tools/preview -cortar
   ```

2. Levantala en segundo plano (con `run_in_background`), desde la rama de la tarea. La
   primera entrada de `git worktree list` es el checkout principal, donde está
   `datos.jsonl` aunque la sesión corra en un worktree:

   ```bash
   go run ./tools/preview -datos "$(git worktree list --porcelain | sed -n '1s/^worktree //p')/datos.jsonl"
   ```

   Esperá la línea "Vista previa en http://…". Si sale "ya hay algo escuchando", no la
   fuerces: decíselo a la persona (puede ser su dashboard real en ese puerto).
3. Usa una copia temporal de los datos y una API de YouTube y de Claude simulada: no
   gasta cuota, no manda nada a Claude y no toca `datos.jsonl`. Tampoco llegan mensajes
   nuevos, así que lo que dependa del chat en vivo (worker, streaming) se ve solo con lo
   que ya estaba guardado: decilo si es el caso.
4. Queda corriendo mientras la persona prueba y se corta en `/push` (o antes, si cambia
   de rama, pasa a otra tarea o lo pide). Si al cortar `-cortar` dice "No hay ninguna
   vista previa corriendo" pero el comando de fondo sigue vivo, esperá unos segundos y
   repetilo hasta que termine. No borres nada a mano.

## Al terminar

Mostrá en pocas líneas:

- Qué archivos cambiaste y por qué, el rojo y el verde (una línea de cada uno) y la
  tabla de gates rápidos.
- El link de la vista previa y **una lista corta de qué probar ahí**, concreta para esta
  tarea: pantalla (`/`, `/programas`, `/canales`), qué tocar y qué debería verse. Por
  ejemplo: "Abrí /canales, agregá @rockandpop y fijate que la barra de cuota suba antes
  de guardar". Recordale mirar modo claro y oscuro y el ancho de celular si cambió algo
  visible.

Cerrá diciendo que, si algo no es lo que buscaba, lo diga en el chat o corra
`/implementar <nota>` (la vista previa se reinicia con el cambio), y si está bien, siga
con `/revisar` o directo con `/push`.
