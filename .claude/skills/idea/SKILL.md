---
name: idea
description: Primer paso del flujo manual de yt-monitor. Convierte una tarea en una spec corta en docs/specs/, abre la rama y se la muestra a la persona para que la apruebe. Sigue /implementar.
argument-hint: <tarea, bug o stack trace>
---

# /idea

Flujo manual: `/idea` → `/implementar` → `/revisar` (opcional) → `/push`. La persona
corre cada paso y valida entre uno y otro. Trabajás **en esta sesión, sin subagentes**:
lo que leas acá sirve para `/implementar`.

Tarea:

$ARGUMENTS

Si está vacía, pedila y pará.

## Pasos

1. `git status --porcelain` tiene que salir vacío; si no, pará y preguntá (la rama nueva
   se llevaría esos cambios).
2. Elegí `tipo` (`fix`, `feat`, `chore`, `docs`, `test`) y un `<slug>` de 2 a 5 palabras.
   Abrí la rama sin pasar por `main`:

   ```bash
   git fetch origin
   git switch -c <tipo>/<slug> origin/main
   ```

3. Entendé el problema leyendo **solo lo necesario** (`AGENTS.md` ya está en contexto: no
   lo releas). Para un bug, reproducilo con un test o un comando; si no se pudo, decilo y
   marcá la causa como hipótesis.
4. Escribí `docs/specs/<AAAA-MM-DD>-<slug>.md` con la estructura de
   `docs/specs/SPEC_TEMPLATE.md`, pero **corta** (que entre en una pantalla):
   contexto y causa con `archivo.go:línea`, objetivo, criterios de aceptación
   verificables (cada uno con su test o comando), diseño en pocas líneas, fuera de
   alcance y solo los riesgos reales (cuota, `datos.jsonl`, concurrencia, Windows,
   privacidad). `Estado: borrador`.
5. Commiteá la spec para que el checkout quede limpio:

   ```bash
   git add docs/specs/<archivo>.md
   git commit -m "docs: spec de <slug> (borrador)"
   ```

6. Mostrá en el chat: causa o motivo, criterios, diseño y riesgos, con el link a la spec.
   Cerrá diciendo que si está bien corra `/implementar` (eso cuenta como aprobación), o
   que te diga qué cambiar. Si pide cambios, editá la spec, commiteala
   (`docs: ajustar spec de <slug>`) y volvé a mostrarla.

No escribas código de producción ni tests en este paso.
