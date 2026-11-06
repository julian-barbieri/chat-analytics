# Spec: En vivo con Claude: sentimiento, alertas y preguntas agrupadas (F4)

- **Estado:** borrador
- **Tipo:** mejora (feat)
- **Rama:** `feat/claude-en-vivo`
- **Origen:** pedido de Julian Barbieri (2026-11-06): "Arrancá la fase 4 (tandas de Claude) sin
  utilizar el factory". Es la **F4** de `docs/specs/2026-11-05-canales-editables-claude-vivo.md`
  (aprobada el 2026-11-05 con las 12 recomendaciones), en el flujo manual (ADR 0008). Esta es la
  versión corta para implementar; el detalle (imágenes de referencia, decisiones 5 a 8) está en la
  madre, "F4" y "Diseño de referencia".
- **Aprobada por / fecha:** —

## Contexto

F3 está en `main` (`ae1c89c`): las tandas guardan por mensaje `MessageLabel` (sentimiento,
categoría, `Tech`, `Group`; `batch.go:41`), grupos nuevos (`BatchGroup`, `batch.go:51`) y motivos
(`BatchMotive`, `batch.go:57`) en `datos.etiquetas.jsonl`, y el `Store` las expone con
`Batches(canal, desde)` (`store.go:552`) y `Labels(ids)` (`store.go:565`). Pero En vivo **no las
muestra**: `channelAnalysis` (`server.go:830`) arma `#quejas` y `#preguntas` por reglas
(`templates/partials.html:94`, `:109`), el sentimiento sigue siendo la barra `claude-sent` del
resumen (`partials.html:252`) y `claudeLine` (`server.go:920`) solo habla de resúmenes. `abrir` ya
acepta `alertas` (`server.go:356`).

## Objetivo

Que En vivo muestre, con las etiquetas de las tandas: "Sentimiento del chat" (tarjetas, cartel,
gráfico cada 5 min y motivos), alertas de insultos y quejas técnicas con sus mensajes, y
"Preguntas agrupadas para producción" ordenadas por cantidad con "Marcar respondida" persistida.

## Criterios de aceptación

Son CA4.1 a CA4.13 de la madre, **adoptados tal cual**; acá el resumen y su test. Tests en
`monitor_test.go`, sin red ni `datos.jsonl`; el primero falla hoy en ejecución (salida en
`factory/manual/claude-en-vivo/`).

- [ ] CA1 (falla hoy, = CA4.1): `id="sentimiento"` "Sentimiento del chat" con chip Claude; sin
  etiquetas "Todavía no hay mensajes analizados por Claude en este programa"; apagado "El
  sentimiento lo calcula Claude: está apagado (ver Canales)"; En vivo sin `class="csent"`
  (Programas sí) — `TestSentimentCard`.
- [ ] CA2 (= CA4.2): `sentimentSlots` (pura), franjas de 5 min, < 20 mensajes sin color, tarjetas
  "±N pts sobre lo típico", cartel "El negativo sube…" si X ≥ Y + 5 — `TestSentimentSlots`.
- [ ] CA3 (= CA4.3): lo típico: mismo canal y programa en 7 días (≥ 6 franjas), si no el tramo de
  hoy (≥ 6), si no "todavía sin historia para comparar" — `TestSentimentTypical`.
- [ ] CA4 (= CA4.4): SVG apilado al 100 % "Cómo fue cambiando · cada 5 min", líneas de cambio de
  programa, textos en HTML, `--sent-low` nuevo en los dos temas — `TestSentimentChart`,
  `TestTemplateTokens`.
- [ ] CA5 (= CA4.5): "Por qué hay mensajes negativos" / "Qué está gustando": motivos de 5 min
  juntados por texto normalizado, top 3 + "Otros", ≤ 2 citas por motivo del `Store` (sin autor) —
  `TestMotives`.
- [ ] CA6 (= CA4.6): `#alertas` reemplaza a `#quejas`: técnicas ≥ 15 en 5 min, insultos ≥ 5 % en
  10 min con ≥ 20 mensajes (Claude, o reglas sin etiqueta); "Ver los mensajes (N)" plegado
  (`abrir=alertas`), ≤ 10 con autor, hora y texto del `Store` — `TestAlerts` (bordes).
- [ ] CA7 (= CA4.7): `#preguntas` "Preguntas agrupadas para producción": "N · pregunta" por
  cantidad, "P preguntas · G grupos", respondidas al final con "Deshacer"; sin etiquetas, lista
  por reglas con "por reglas" — `TestQuestionGroups`.
- [ ] CA8 (= CA4.8): `POST /preguntas/respondida` (`grupo`, `deshacer`): 403 sin guardas, 1 KB,
  grupo inexistente 400; `Answers` (`answers.go`, mutex propio) agrega a
  `datos.respondidas.jsonl`; al abrir 30 días y tmp + `os.Rename`; botón 44 px solo en loopback —
  `TestAnswerPost`, `TestAnswersOpen`.
- [ ] CA9 (= CA4.9): orden `#canal`, selector, tiles, `#alertas`, `#claude-card`, `#sentimiento`,
  `#preguntas`, `#categorias`, `#momentos` (sin cambios), `#chat`, `#supers`, `#monitor` —
  `TestLiveScreenOrder`.
- [ ] CA10 (= CA4.10): `claudeLine`: "Claude (Haiku 5.5) · analizado hasta las HH:MM:SS · N s de
  demora · tandas cada 30 s" o el motivo (apagado, tope de tiempo real, canal sin el check) —
  `TestLiveAnalysisClaudeLine` extendido.
- [ ] CA11 (= CA4.11): sin copiar 7 días de etiquetas por pedido: `Store.SentSlots(canal, desde)`
  agregado por franja y programa — `BenchmarkSentimentView` (70.000 etiquetas, informativo).
- [ ] CA12 (= CA4.12): README, AGENTS.md, CLAUDE.md (tercer punto de escritura del dashboard,
  `Answers`, `datos.respondidas.jsonl`) y ADR 0009 ampliado —
  `grep -n "datos.respondidas.jsonl" README.md AGENTS.md CLAUDE.md`.
- [ ] CA13: cuota de YouTube igual (`git diff main -- youtube.go worker.go` vacío) y navegador con
  la vista previa: claro, oscuro, celular, alertas abiertas tras 30 s, marcar y deshacer. Manual.
- [ ] Gates de `CLAUDE.md` en verde.

## Diseño propuesto

- `batch.go`: `sentimentSlots`, `sentimentTypical`, `Motives`, `Alerts`, `questionGroups` (puras).
- `store.go`: `SentSlots` (contadores por canal, franja de 5 min y programa, armados en
  `indexLabels`) y un acceso a los grupos de un canal; nada de copiar `batches` enteras.
- `answers.go` (nuevo): `Answers`, `OpenAnswers`, `Mark`, `Answered`; vive en el `Server`.
- `server.go` / `partials.html`: `#alertas`, `#sentimiento`, `#preguntas` nuevas; `claude-block`
  sin `Sent` en En vivo; `claudeLine` con la última tanda; ruta `POST /preguntas/respondida`.
- `main.go`: abre `datos.respondidas.jsonl`. Primer test: `TestSentimentCard`.

## Fuera de alcance

F5 (CSV, categorías de Claude en el chat, "Desde dónde escriben", Programas con Claude), umbrales
configurables, cambiar el gráfico de participación y el panel de oyentes, copiar
`datos.respondidas.jsonl` en la vista previa (es lo que decidió la persona, no de la API).

## Riesgos

- **Sin gasto nuevo:** F4 solo lee lo que F3 ya guardó; no hay pedidos nuevos a Claude ni a YouTube.
- **Concurrencia:** `Answers` es el tercer escritor desde handlers (después de `Settings` y la cola
  del analista): mutex propio, fuera del `Store`, detrás de `sameOrigin` + `localRequest`.
- **Performance:** el parcial se refresca cada 30 s; con 7 días de etiquetas hay que agregar al
  guardar (`SentSlots`), no al leer.
- **Privacidad:** las alertas muestran autor y texto que ya están en el chat, plegados y solo en el
  dashboard local; nunca texto del modelo como si fuera de un usuario.
- **Tamaño:** muchos tests de texto de En vivo (`TestLiveScreenOrder`, `TestClaudeCard`,
  `TestDashboardWithoutRemovedSections`). Si se hace pesado: F4a (sentimiento + alertas) y F4b
  (preguntas + respondidas).
