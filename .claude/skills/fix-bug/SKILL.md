---
name: fix-bug
description: Arregla un bug de yt-monitor de punta a punta (triage → spec aprobada → implementer → tester → reviewer → PR) en una rama nueva. Usalo cuando haya un stack trace, un test que falla o un bug reproducible. Es un atajo de /factory con tipo fix.
argument-hint: <descripción del bug, stack trace o test que falla>
---

# /fix-bug

Es `/factory` con `tipo: fix`. Seguí [../factory/SKILL.md](../factory/SKILL.md) como
una tarea nueva (paso 0 en adelante), con esto como tarea:

$ARGUMENTS

Si no hay descripción, pedila y pará.
