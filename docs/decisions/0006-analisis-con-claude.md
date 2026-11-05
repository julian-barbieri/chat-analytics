# ADR 0006: El monitor manda el texto del chat de cada programa a la API de Claude, apagado por defecto y con tope de gasto

- **Estado:** propuesta
- **Fecha:** 2026-11-05
- **Modifica:** ADR 0003 (la vista previa también simula la API de Claude y saca sus claves del entorno)
- **Spec:** `docs/specs/2026-11-05-analisis-claude.md` (etapa A)

## Contexto

El análisis fino del chat (resumen, sentimiento, preguntas agrupadas) se hacía a mano:
alguien descargaba el CSV y lo subía a un chat de Claude con el pedido del README. Las
reglas del repo lo impedían dentro del monitor: AGENTS.md ("No meter llamadas a LLMs ni
dependencias de NLP acá sin pedirlo") y el README ("no lo subas a ningún lado que no sea
el chat con Claude"). La persona pidió que el monitor lo haga solo por canal y por
programa, y confirmó que mandar el texto del chat a Anthropic es compatible con las
políticas de YouTube y con los términos de la cuenta de Alphamedia. Restricciones: solo
biblioteca estándar, Windows, un tope de gasto que nunca se pase, la clave nunca en logs
ni en el dashboard, datos de la API con retención de 30 días.

## Decisión

- **Opt-in:** apagado por defecto; se activa con `-claude-usd` > 0 (tope diario en
  dólares, día local) y `CLAUDE_API_KEY`. Sin eso, todo sigue como antes y la tarjeta 7
  dice que está apagado.
- **Goroutine propia** (`RunAnalyst`, `analyst.go`), la única que usa el cliente `Claude`
  (`claude.go`, `net/http`, sin SDK). El worker de YouTube no cambia.
- **Cuándo:** un pedido por programa de cada canal de `-canal` cuando termina (al
  arrancar, los de hoy que ya terminaron), y uno parcial del programa al aire con el botón
  "Analizar ahora", como mucho cada 10 min por canal. El botón solo encola, detrás de
  `sameOrigin` y de `Host` / conexión de loopback.
- **Minimización:** solo el texto y la hora de los mensajes de chat, sin autores, IDs,
  likes ni montos, sin el spam (links y teléfonos), cada texto cortado en 300 runas y
  como mucho 1.500 por pedido. Las cantidades de mensajes y personas se cuentan en Go.
- **Salida estructurada** con `output_config.format` (JSON Schema) y `effort: "low"`; el
  resultado se valida en Go (números de mensaje en rango, listas y textos cortados).
- **Tope local con peor caso:** antes de cada pedido, lo gastado más el costo de la
  salida completa (`claudeMaxTokens`) no puede pasar el tope; el costo real se calcula con
  el `model` de la respuesta y se guarda en `datos.claude.json`.
- **Archivos aparte con retención:** `datos.analisis.jsonl` (textos del modelo e IDs de
  los mensajes de ejemplo) con la misma retención de 30 días y tmp + `os.Rename`; nada
  cambia en `datos.jsonl` ni en el CSV.
- **Vista previa:** `tools/preview` saca `CLAUDE_API_KEY`, `CLAUDE_BASE_URL` y
  `ANTHROPIC_API_KEY` del entorno del hijo, pone una clave falsa y su API simulada
  atiende `POST /v1/messages` con un análisis fijo: los mensajes copiados nunca salen de
  la PC.

## Consecuencias

- Los datos salen de la PC todos los días, a un tercero, si se activa: por eso apagado por
  defecto, minimizado y con la misma retención. Queda a confirmar si la cuenta tiene
  retención cero en Anthropic.
- Gasta plata: ~US$ 1,33 por día con Sonnet 5.5 según la hipótesis de volumen (se mide
  la primera semana con los tokens que guarda cada análisis); tope sugerido US$ 2.
- El resumen puede equivocarse o ser manipulado por un mensaje (prompt injection): sin
  herramientas, con esquema, validación, salida escapada y la aclaración en la tarjeta.
- Una goroutine más escribe el `Store` (solo análisis, gasto y estado, bajo el lock).
- Descartadas: herramienta forzada (`tool_choice`, da 400 en los 5.5), `output_format`
  (deprecado), apagar el thinking (da 400), fallback del servidor ante refusal (no está
  claro cómo se factura), llamar a Claude desde el worker (frena el tick y el chat) o
  desde el handler (gasto doble, tope en dos lugares), análisis cada N minutos (reenvía
  lo mismo), mandar autores (no hace falta), SDK (dependencia), Batches API y prompt
  caching (ahorro chico, más complejidad), guardar la categoría de Claude en `Message`
  (cambia `datos.jsonl`), meterlo en `classify.go` / `analyze.go` (siguen siendo reglas).
