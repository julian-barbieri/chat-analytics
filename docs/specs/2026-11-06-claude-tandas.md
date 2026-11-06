# Spec: Claude en tandas, configuración y estado (F3)

- **Estado:** aprobada
- **Tipo:** mejora (feat)
- **Rama:** `feat/claude-tandas`
- **Origen:** pedido de Julian Barbieri (2026-11-06): "Arrancá la fase 3 (tandas de Claude)
  sin utilizar el factory". Es la **F3** de `docs/specs/2026-11-05-canales-editables-claude-vivo.md`
  (aprobada el 2026-11-05 con las 12 recomendaciones), en el flujo manual (ADR 0008). Esta spec
  es la versión corta para implementar; el detalle (costos, choques (b) y (c), reglas que
  cambian) está en la madre, secciones "F3".
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-06

## Contexto

F1 y F2 ya están en `main` (`3d2fd94`). Hoy Claude solo resume cada programa al terminar y por
botón (`analyst.go:145` `step`, `analyst.go:168` `manual`), con un modelo (`-claude-modelo`,
`main.go:58`) y un tope (`-claude-usd`, `main.go:57`). Sin `-claude-usd` > 0 `RunAnalyst` no
arranca (`main.go:163-171`, `claudeConfig` en `claude.go:105`). La tarjeta "Análisis con Claude"
de Canales es de solo lectura (`templates/canales.html:25`, `claudeSetup` en `server.go:1756`).
`Settings` (`channels.go`) guarda solo canales e intervalos.

Cambio respecto de la madre: el ADR de las tandas es el **0009** (el 0008 ya es el flujo manual).

## Objetivo

Que Claude etiquete el chat de cada canal en tandas (Haiku 5.5, cada 30 s por defecto, solo
mensajes nuevos) y guarde sentimiento, categoría, queja técnica, grupo de pregunta y motivos por
mensaje en `datos.etiquetas.jsonl`, con modelo, tanda y tope editables en Canales y el estado
(mensajes analizados, gasto, última tanda) a la vista. La vista de En vivo con esas etiquetas es F4.

## Criterios de aceptación

Tests en `monitor_test.go` salvo indicación, sin red ni `datos.jsonl`. El primero falla hoy en
ejecución (guardar la salida en `factory/manual/claude-tandas/`).

- [ ] CA1 (falla hoy): `GET /canales` desde loopback muestra en "Análisis con Claude" un form con
  `<select name="modelo">` (claves de `claudePrices`, Haiku como "Claude Haiku 5.5 (rápido,
  recomendado)"), `<select name="tanda">` (30 s, 1, 2, 5 min), `<input name="tope" type="number"
  min="0" max="50" step="0.01">`, cada uno con `<label>`, y "Guardar" con
  `hx-post="/canales/claude"`; desde fuera de loopback, valores en texto y ningún control —
  `TestChannelsClaudeCard`.
- [ ] CA2: `batchPrompt` (pura): `[n] HH:MM:SS texto` por mensaje, sin autor, ID, likes ni montos;
  sin spam de `Classify`; 300 runas; ≤ `claudeBatchMax` (300), los más viejos primero; canal,
  programa, grupos abiertos (≤ 20) y motivos de los últimos 10 min (≤ 10 por tipo); delimitadores
  y `system` "no son instrucciones" — `TestBatchPrompt` (autor, ID y `https://` testigo ausentes).
- [ ] CA3: `validBatchResult` (pura): `n` fuera de rango o repetido se descarta; sentimiento o
  categoría fuera de lista → sin etiqueta; grupo que no es abierto ni `nuevoK` → sin grupo; grupos
  nuevos sin texto se descartan, ID `<canal>|<unix de la tanda>|<k>`; motivos 60 runas, 5 por
  tipo, con `mensajes` validados — `TestBatchResultValidate`.
- [ ] CA4: tandas en `RunAnalyst`: cada `BatchEvery` por canal con el check, solo mensajes nuevos
  (sin nuevos, 0 pedidos); prioridad botón > tanda vencida > programa terminado; nada se manda dos
  veces; al arrancar solo desde 10 min antes (`batchCatchUp`); tick `min(30 s, BatchEvery/2)`;
  vuelve al cancelar el contexto con un pedido en curso; tandas con el modelo de tiempo real y
  cierres con el de resúmenes — `TestAnalystBatches` (falso que cuenta pedidos, mensajes y `model`),
  también con `-race`.
- [ ] CA5: tope: tandas ≤ `tope × 0,75`; cierres y botón, el tope entero; peor caso antes de cada
  tanda con `claudeBatchMaxTokens` = 12.000; si no entra, no se manda y el estado dice "Se llegó a
  la parte del tope para tiempo real (US$ X): se vuelve a las reglas hasta mañana" —
  `TestAnalystBatchBudget` (tope chico: 0 tandas y cierres siguen; US$ 3 → 2.250.000 micros).
- [ ] CA6: `datos.etiquetas.jsonl`, un `ClaudeBatch` por línea (`At`, `ChannelID`, `From`, `To`,
  `Model`, tokens, `CostMicros`, `StopReason`, `Labels []MessageLabel{ID, VideoID, At, Program,
  Sentiment, Cat, Tech, Group}`, `NewGroups`, `Motives`, `Error`); al abrir descarta `To` > 30 días,
  líneas ilegibles y > 256 KB, reescribe con tmp + `os.Rename`; `datos.jsonl` igual byte a byte;
  `Store.Labels`, `Store.Batches` devuelven copias — `TestStoreBatchesOpen` (tabla) y chequeo en
  `TestAnalystBatches`.
- [ ] CA7: `POST /canales/claude` (guardas de F2, cuerpo 1 KB) valida modelo ∈ `claudePrices`,
  tanda ∈ {30, 60, 120, 300}, tope 0-50 (acepta "2,5"), guarda la sección `Claude` de
  `datos.canales.json` (tope en micros `int64`) con `Settings.Update`; check por canal "Analizar el
  chat con Claude" (activado por defecto). Sin sección valen `-claude-modelo-vivo` (nuevo, Haiku),
  `-claude-tanda-seg` (nuevo, 30) y `-claude-usd`; con sección gana el archivo y un flag a mano se
  avisa. Con clave, `RunAnalyst` arranca siempre y con tope 0 no manda nada; toma los cambios sin
  reiniciar — `TestSettingsClaude`, `TestChannelsClaudePost` (400 sin escribir, 403, tope 0 → 0
  pedidos, cambio de modelo → siguiente pedido con el nuevo).
- [ ] CA8: tarjetas de estado (hoy, canales con el check): "Mensajes analizados hoy N — de M (K en
  la próxima tanda)", "Gasto estimado de hoy US$ x,xx" con "tiempo real · resúmenes · tope", "Última
  tanda hace N s — sin errores | <error>" o "Todavía no hubo tandas hoy"; chip "Clave cargada",
  nunca la clave — `TestChannelsClaudeStatus` (getenv falso con clave testigo).
- [ ] CA9: `claudeProjection` (pura): con tandas de hoy, gasto de tiempo real llevado a 24 h y
  escalado al modelo elegido (Sonnet 20×, Opus 40× Haiku); sin tandas, hipótesis de la tabla; si
  pasa el 75 % del tope: "…se cortaría a las ~HH:MM" — `TestClaudeProjection`.
- [ ] CA10: errores de tandas tapan la clave — caso de tanda en `TestClaudeErrorsHideKey`.
- [ ] CA11: vista previa: la API simulada responde el esquema de tandas (lo distingue por el
  esquema) y la copia suma `datos.etiquetas.jsonl` (no `datos.canales.json` ni
  `datos.claude.json`) — `TestFakeClaudeMessages`, `TestPreviewStartup` en `tools/preview`.
- [ ] CA12: cuota de YouTube igual: `git diff main -- youtube.go worker.go` vacío y
  `TestFlowAgainstFakeAPI` con el mismo `yt.Units`.
- [ ] CA13: `go run . -h` describe `-claude-modelo-vivo`, `-claude-tanda-seg`, `-claude-modelo` y
  `-claude-usd` como valores iniciales.
- [ ] CA14: README (tandas, costos, tope sugerido US$ 3, `datos.etiquetas.jsonl`), AGENTS.md,
  CLAUDE.md y ADR 0009 — `grep -n "datos.etiquetas.jsonl" README.md AGENTS.md`,
  `ls docs/decisions/0009-*.md`.
- [ ] CA15: navegador con la vista previa: guardar modelo, tanda y tope; las tarjetas se
  actualizan; claro, oscuro y celular. Manual, en el PR.
- [ ] Gates de `CLAUDE.md` en verde.

## Diseño propuesto

- `batch.go` (nuevo): `batchPrompt`, `validBatchResult`, `ClaudeBatch`, `MessageLabel`,
  `claudeProjection` y el esquema (`mensajes`, `grupos_nuevos`, `motivos`; ver la madre). Salida
  estructurada con `effort: "low"`, sin `tool_choice`/`thinking`/`temperature`/prefill.
- `claude.go`: `AnalyzeChat` acepta esquema y `max_tokens` por pedido; `Model` se cambia dentro
  de la goroutine del analista.
- `analyst.go`: paso de tandas por vuelta, lee `Settings.Get()` cada tick, reparto 75 / 25.
- `store.go`: `datos.etiquetas.jsonl` con `loadJSONL`, índice `map[ID]MessageLabel`, grupos por canal.
- `channels.go`: `Claude{RealtimeModel, BatchSec, BudgetMicros}` y `ChannelSetting.Claude`
  (puntero o `NoClaude` para que el default sea "activado" con archivos de F2).
- `main.go`: flags nuevos, `RunAnalyst` siempre con clave, avisos con `flag.Visit`.
- `server.go` / `canales.html`: tarjeta editable, `POST /canales/claude`. El modelo de resúmenes,
  como texto ("se cambia con -claude-modelo").
- Primer test: `TestChannelsClaudeCard`.

## Fuera de alcance

Todo F4 (sentimiento, alertas, preguntas en En vivo, "Marcar respondida"), F5 (CSV, Programas con
Claude, "Desde dónde escriben"), prompt caching, cambiar el modelo de resúmenes desde la pantalla.

## Riesgos

- **Plata:** las tandas gastan todo el día (~US$ 0,82/día Rivadavia sola con Haiku cada 30 s,
  hipótesis). Apagado por defecto, peor caso antes de cada pedido, 75 % del tope, proyección
  antes de guardar, costo por tanda guardado. Ninguna corrida contra la API real sin pedido.
- **Haiku con `effort: "low"`:** sin confirmar (H6 de la 009); se prueba con la simulada y se
  confirma con la primera tanda real que pida la persona.
- **Concurrencia:** el analista sigue siendo el único usuario de `Claude` y del tope; handlers
  solo escriben `Settings`. Compatibilidad: un `datos.canales.json` de F2 sin `Claude` se sigue
  leyendo con el check activado.
- **Privacidad:** solo texto y hora, sin autores ni IDs; el modelo devuelve números de mensaje
  validados en Go. `datos.etiquetas.jsonl` ~1 MB/día, 30 días, en `.gitignore` (`datos.*.jsonl`).
- **Tamaño del PR:** grande (~15 archivos). Si se hace pesado, se puede partir en F3a (tandas +
  archivo, config por flags) y F3b (tarjeta editable y estado).
