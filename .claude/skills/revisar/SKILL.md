---
name: revisar
description: Paso opcional del flujo manual de yt-monitor. Pasa el diff de la rama al agente reviewer (ojos que no escribieron el código), muestra los hallazgos y corrige los que la persona elija en esta sesión.
argument-hint: "[foco opcional: archivo, riesgo o pregunta]"
---

# /revisar

Paso opcional del flujo manual (`/idea` → `/implementar` → `/revisar` → `/push`). Es
el único que usa un subagente: el valor está en una lectura que no viene de quien
escribió el código.

Foco pedido (puede estar vacío):

$ARGUMENTS

## Pasos

1. Ubicá la spec como en `/implementar` y la carpeta `factory/manual/<slug>/`.
2. Armá el diff contra `origin/main`, con lo no commiteado:

   ```bash
   git fetch origin
   git add -A
   git diff --cached origin/main
   ```

   Si aparecen `.env`, `datos.jsonl`, `datos.*`, `informes/`, `factory/` o binarios,
   sacalos del índice y avisá antes de seguir.
3. Lanzá el agente `reviewer` con la ruta de la spec, el foco pedido si hay y el **diff
   completo pegado en el prompt** (no tiene Bash). Guardá su respuesta tal cual en
   `factory/manual/<slug>/revision-<n>.md` (`n` = siguiente número libre).
4. Mostrá a la persona el veredicto y los hallazgos en una lista corta, numerados, con
   `archivo.go:línea`. Para cada uno decí si lo ves real y cuánto cuesta arreglarlo. No
   corrijas nada todavía.
5. Cuando la persona elija cuáles arreglar, corregilos acá mismo (no relances el
   reviewer ni `/implementar` entero), corré `scripts/gates.sh` y mostrá qué cambió.
   Lo que quede abierto va en el PR (lo toma `/push` de `revision-<n>.md`).

Cerrá diciendo que siga con `/push`.
