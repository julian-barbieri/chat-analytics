# ADR 0009: Claude etiqueta el chat en tandas

- **Estado:** aceptada (modifica ADR 0006)
- **Fecha:** 2026-11-06

## Contexto

ADR 0006 dejó a Claude resumiendo cada programa cuando termina y con el botón "Analizar
ahora", y descartó "un análisis cada N minutos" porque reenviaba los mismos mensajes. La
persona pidió sentimiento, alertas y preguntas agrupadas en tiempo real (spec
`docs/specs/2026-11-05-canales-editables-claude-vivo.md`, F3, y
`docs/specs/2026-11-06-claude-tandas.md`), con el modelo, las tandas y el tope editables
desde Canales. Restricciones: el tope diario no se puede pasar, se manda solo texto y hora,
el cliente `Claude` no es concurrente y `datos.jsonl` no cambia de formato.

## Decisión

- **Tandas solo con mensajes nuevos.** Cada `BatchEvery` (30 s por defecto), por canal con
  el check "Analizar el chat con Claude", `RunAnalyst` manda los mensajes de chat que todavía
  no se mandaron (a lo sumo 300, los más viejos primero). Sin mensajes nuevos no hay pedido.
  Al arrancar, solo los últimos 10 minutos. Claude devuelve por número de mensaje sentimiento,
  categoría, si es una queja técnica y el grupo de preguntas; Go valida todo y pone los IDs.
- **Dos modelos.** Tiempo real con `claude-haiku-5-5` por defecto (`-claude-modelo-vivo`,
  editable en Canales); resúmenes de cada programa con `-claude-modelo` (Sonnet), como antes.
  El modelo va en cada pedido: sigue habiendo un solo cliente.
- **Una sola goroutine y un solo tope.** Las tandas van en `RunAnalyst`, con prioridad botón
  > tanda vencida > programa terminado. Las tandas usan como mucho 3/4 del tope diario
  (chequeando su peor caso, con 12.000 tokens de salida, antes de mandar); los resúmenes y el
  botón pueden usar el tope entero.
- **Etiquetas aparte.** `datos.etiquetas.jsonl`, una tanda por línea con tokens, modelo y
  costo, 30 días desde el último mensaje de la tanda, tmp + `os.Rename` al abrir. Nunca en
  `datos.jsonl`. `Classify` sigue siendo la categoría del CSV.
- **Configuración editable con precedencia del archivo.** La sección `Claude` de
  `datos.canales.json` (modelo de tiempo real, tanda, tope en micro-dólares) y el campo
  `NoClaude` de cada canal se guardan con `POST /canales/claude`, detrás de `sameOrigin` y
  `localRequest`. Sin la sección valen `-claude-modelo-vivo`, `-claude-tanda-seg` y
  `-claude-usd`; con la sección gana el archivo y un flag pasado a mano se avisa.
- **Activación con clave y tope.** Con `CLAUDE_API_KEY`, `RunAnalyst` arranca siempre; con
  tope 0 no manda nada. Sigue apagado por defecto y solo se prende desde la PC del monitor.

## Consecuencias

- Hay etiquetas por mensaje casi en tiempo real por ~US$ 0,82 por día con Rivadavia sola
  (hipótesis); el gasto se mide con los tokens guardados en cada tanda.
- Un cierre de programa (decenas de segundos) atrasa una tanda: la siguiente trae más
  mensajes, no se pierde ninguno.
- Un mensaje que Claude no etiquetó no se vuelve a mandar mientras el monitor siga
  corriendo; después de reiniciar, sí (si es de los últimos 10 minutos).
- **Ampliación (F4, `docs/specs/2026-11-06-claude-en-vivo.md`):** En vivo muestra las
  etiquetas sin pedidos nuevos: sentimiento, alertas y preguntas agrupadas. "Marcar
  respondida" se guarda en un archivo aparte, `datos.respondidas.jsonl` (`Answers`, 30 días),
  y no en `datos.etiquetas.jsonl`, para no mezclar lo que escribe el modelo con lo que decide
  la persona. El `Store` suma el sentimiento por franja al guardar cada tanda (`SentSlots`)
  para que el refresco de 30 s no recorra 7 días de etiquetas.
- Descartadas: una goroutine aparte para las tandas (dos chequeando el mismo tope pueden
  pasarlo), dos clientes `Claude`, reenviar la ventana de 5 min en cada tanda, guardar la
  etiqueta en `Message` (cambia `datos.jsonl`) y prompt caching (centavos por día con Haiku).
