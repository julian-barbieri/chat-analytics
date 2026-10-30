# ADR 0002: Estado de la factory en archivos y tablero aparte en Go + htmx

- **Estado:** aceptada
- **Fecha:** 2026-10-30

## Contexto

La factory del ADR 0001 funciona, pero el avance de cada tarea solo se ve en la
conversación de Claude Code: no hay forma de saber qué está haciendo cada agente, en qué
etapa está cada tarea ni de retomarla desde otra sesión. El proyecto es Go solo con
stdlib y htmx vendorizado; no queremos base de datos ni frontend con build.

## Decisión

- **Estado en archivos:** cada tarea tiene `factory/jobs/<NNN>-<slug>/job.json`. Lo
  escribe **solo** el orquestador (`/factory`), con archivo temporal + `mv`. Los
  resultados de los agentes se guardan como `.md` en la misma carpeta.
- **Actividad por hooks:** un hook `async` (`log-activity.sh`) agrega una línea por
  evento de herramienta o de subagente a `factory/events/AAAA-MM-DD.jsonl`. Solo
  resúmenes cortos; se borra a los 30 días.
- **Tablero aparte:** `tools/factory-board/`, un `package main` propio en el mismo
  módulo, con `html/template` y htmx. Solo lee, escucha en `localhost` y asigna cada
  evento a la tarea que su sesión tocó por última vez.
- Los dos directorios de `factory/` están en `.gitignore`: son datos de trabajo, no
  código. La spec sigue en `docs/specs/` y se commitea.

## Consecuencias

- Una persona ve en el tablero el estado y los movimientos de todas las tareas, y
  cualquier sesión puede retomar una tarea leyendo su `job.json`.
- El orquestador tiene más pasos (escribir el estado en cada etapa); si se olvida uno,
  el tablero muestra una etapa vieja, aunque la actividad sigue llegando por los hooks.
- El hook depende de bash y `jq`, como los guardas existentes.
- Descartado: servidor de Python + JS propio como en el original (otro stack);
  servirlo desde el binario de `yt-monitor` (mezcla herramientas internas con el
  producto); una base de datos o un servicio de eventos (más infraestructura).
