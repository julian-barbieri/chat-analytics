# Spec: tablero de la software factory

- **Estado:** aprobada
- **Tipo:** mejora
- **Rama:** `feat/tablero-factory`
- **Origen:** pedido de Julian Barbieri: una software factory como la de
  [zazencodes](https://github.com/zazencodes/zazencodes-season-3/tree/main/src/software-factory-claude-code)
  con un dashboard para ver qué hacen los agentes y en qué estado está cada tarea, con
  el stack del proyecto (Go stdlib + htmx).
- **Aprobada por / fecha:** Julian Barbieri, 2026-10-30 (aprobó en la conversación la
  propuesta de las fases 1 a 3; esta spec la detalla).

## Contexto

Ya existen los agentes (`triage`, `implementer`, `tester`, `reviewer`), el skill
`/fix-bug`, el CI y los hooks de guarda. Pero el avance de una tarea solo se ve leyendo
la conversación de Claude Code: no hay estado por tarea ni registro de lo que hace cada
agente, y no se puede retomar una tarea desde otra sesión.

La documentación de hooks de Claude Code (https://code.claude.com/docs/en/hooks.md)
confirma que `PreToolUse` y `PostToolUse` también se disparan para las herramientas de
los subagentes y traen `agent_id` y `agent_type`; que existen `SubagentStart` y
`SubagentStop` (este con `last_assistant_message`), y que un hook `command` acepta
`"async": true` para no frenar al agente. No dice si `session_id` de un subagente es el
de la sesión que lo lanzó: se verifica a mano (CA9).

## Objetivo

Cada tarea de la factory tiene su estado en `factory/jobs/<id>/job.json`, cada
movimiento de los agentes queda en `factory/events/`, y un tablero local en Go + htmx
muestra las dos cosas casi en tiempo real, sin escribir nada.

## Criterios de aceptación

- [x] CA1: el hook `.claude/hooks/log-activity.sh` agrega **una línea JSON por evento**
  en `factory/events/AAAA-MM-DD.jsonl` con `at`, `evento`, `sesion`, `agent_id`,
  `agente`, `tool`, `resumen` y `detalle` (los vacíos se omiten). El resumen es solo el
  comando recortado a 120 caracteres en una línea, la ruta relativa al repo, el patrón
  buscado o el agente lanzado: nunca contenidos de archivos ni prompts. La API key se
  redacta en `resumen` y `detalle`, sea `YT_API_KEY=<valor>` (con o sin comillas),
  `?key=`/`&key=` en una URL o una clave `AIza…` suelta. — `TestLogActivityHook`
- [x] CA2: el hook borra los archivos de eventos de más de 30 días. —
  `TestLogActivityHook`
- [x] CA3: `.claude/settings.json` registra el hook como `async` en `PreToolUse`,
  `PostToolUseFailure`, `SubagentStart`, `SubagentStop` y `Stop`, sin tocar los hooks de
  guarda. `factory/jobs/` y `factory/events/` están en `.gitignore`. — lectura del
  diff + `jq . .claude/settings.json`
- [x] CA4: el skill `/factory` documenta el esquema de `job.json`, las etapas
  (`triage`, `esperando-aprobacion`, `implementacion`, `tests`, `revision`, `gates`,
  `pr-abierto`, `necesita-persona`, `descartada`), que solo el orquestador escribe
  `job.json` (archivo temporal + `mv`), y los comandos `/factory <tarea>`,
  `/factory aprobar <id>`, `/factory rehacer <id> <nota>` y `/factory estado`.
  `/fix-bug` queda como atajo de `/factory` con tipo `fix`. — lectura de
  `.claude/skills/factory/SKILL.md`
- [x] CA5: el tablero (`go run ./tools/factory-board`) carga las tareas; un `job.json`
  inválido o con una etapa desconocida se muestra como aviso, sin romper el resto. —
  `TestLoadJobs`
- [x] CA6: cada evento se asigna a la tarea que su sesión tocó por última vez (una ruta
  `factory/jobs/<id>/` en el resumen); el agente activo de una sesión es el último
  `SubagentStart` sin su `SubagentStop`. — `TestAssignEvents`
- [x] CA7: `GET /`, `GET /parcial/tablero`, `GET /parcial/actividad` y
  `GET /tarea/{id}` responden 200, también cuando la spec no está en la rama actual; un
  `id` que no cumple `^\d{3}-[a-z0-9-]+$`, o un `archivo` que no está en la carpeta de
  la tarea, da 404 (sin path traversal); un `POST` da 405; el texto de los agentes sale
  escapado. — `TestRoutes`
- [x] CA8: el tablero escucha en `localhost:8090` por defecto, solo lee archivos y
  responde 403 a un `Host` que no sea local (DNS rebinding). — `TestLocalOnly` y
  lectura de `tools/factory-board/main.go`
- [ ] CA9: en una sesión nueva de Claude Code, lanzar un subagente deja eventos con su
  `agent_id`/`agente` en `factory/events/`, y se anota si el `session_id` es el de la
  sesión principal. — verificación manual, resultado en el PR
- [x] CA10: el tablero se ve bien en modo claro, oscuro y en ancho de celular. —
  navegador, con datos armados a mano
- [ ] CA11: gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

- **Fase 1 — actividad.** `log-activity.sh` (bash + `jq`, como los otros hooks) arma la
  línea con `jq -c` y la agrega con un solo `printf >>` (líneas cortas: append atómico
  entre hooks en paralelo). Archivo por día para borrar lo viejo con
  `find -mtime +30 -delete`.
- **Fase 2 — estado.** Skill `.claude/skills/factory/SKILL.md` que generaliza
  `/fix-bug`: crea `factory/jobs/<NNN>-<slug>/` con `mkdir` (falla si existe), escribe
  `job.json` en cada cambio de etapa y guarda el resultado de cada agente en la carpeta
  (`triage.md`, `implementacion-<n>.md`, `tests-<n>.md`, `revision-<n>.md`, `gates-<n>.md`)
  para que el tablero lo muestre. `pr-template.md` se mueve al skill nuevo.
- **Fase 3 — tablero.** `tools/factory-board/`, `package main` propio dentro del mismo
  módulo (sin `require`), para no meter herramientas de desarrollo en el binario de
  producción ni en el `.exe`. `html/template` embebido, htmx servido desde
  `static/htmx.min.js` del repo con `http.ServeFile` y ruta fija (`//go:embed` no sube
  de directorio y así no se duplica el vendorizado). Polling de htmx cada 2 s. Los eventos se releen
  solo si cambió el tamaño o la fecha del archivo, y solo los de hoy y ayer.

Descartado: servir el tablero desde el binario de `yt-monitor` (mezcla herramientas
internas con el producto); que los agentes anoten su `agent_id` en `job.json` (la
herramienta Agent lo devuelve recién al terminar, así que no sirve para el vivo).

## Fuera de alcance

- Fase 4: worktrees por tarea, `factory/backlog.md` con `/factory next` y revisores en
  paralelo (`reviewer-codigo`, `-seguridad`, `-dominio`, `-ui`).
- Botones en el tablero que ejecuten comandos o aprueben tareas.
- Leer el estado de PRs o CI de GitHub desde el tablero.

## Riesgos

- **Privacidad:** los resúmenes podrían mostrar datos si un agente nombrara datos
  reales en un comando. Mitigación: solo resúmenes cortos, nada de `tool_input`
  completo, `.env` ya bloqueado por los guardas, archivos en `.gitignore` y borrados a
  los 30 días.
- **Cuota de YouTube:** ninguno; nada de esto toca la API.
- **Hooks lentos:** `async: true`, así que no frenan a los agentes.
- **`session_id` de subagentes:** si no es el de la sesión principal, los eventos del
  subagente no se asignan a la tarea por sesión; aparecen igual en el feed general.
  Se verifica con CA9.
- **Windows:** el tablero es Go puro y compila en Windows; el hook necesita bash y
  `jq`, como los hooks que ya existen.
