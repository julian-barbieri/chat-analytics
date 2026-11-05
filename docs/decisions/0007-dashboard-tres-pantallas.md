# ADR 0007: El dashboard pasa a tres pantallas con rutas propias

- **Estado:** propuesta
- **Fecha:** 2026-11-05
- **Modifica:** ADR 0003 (la vista previa también copia `datos.vivos.jsonl` y
  `datos.analisis.jsonl`) y lo dicho en el ADR 0006 sobre no copiar `datos.analisis.jsonl`
- **Spec:** `docs/specs/2026-11-05-rediseno-tres-pantallas.md` (etapa 1)

## Contexto

El dashboard era una sola página armada por acumulación: resumen global, chat por canal,
tarjetas numeradas del 1 al 7 y, abajo, palabras y autores. La persona pidió la estructura
y la estética de un diseño de referencia con tres secciones (En vivo, Programas de hoy,
Canales) que se vean igual, y poder revisar los programas ya terminados. Restricciones:
solo stdlib, `html/template` + htmx vendorizado, sin JS propio ni build, Windows, la
retención de 30 días y que los handlers nunca llamen a YouTube ni a Claude.

## Decisión

- **Tres rutas:** `GET /` (En vivo), `GET /programas` (Programas de hoy, `?dia=hoy|ayer|7d`,
  `?canal=`, `?tramo=`) y `GET /canales` (solo lectura en esta etapa), con el mismo
  encabezado y pestañas (`templates/layout.html`) y links comunes entre ellas. htmx solo
  para los parciales. `?canal=` y `?vivo=` siguen andando.
- **Qué se mudó:** los botones de canal, el cartel de esperando, la tira "Hoy en el canal"
  y el cartel de continuidad a la tarjeta Canal (`/parcial/vivo/canal`, 5 s); el chat a su
  tarjeta (sigue siendo el que detecta el cambio de vivo con `HX-Retarget: #vivo`); palabras,
  autores y categorías globales al bloque "Todo el monitor · últimas 24 h"; el resumen de
  Claude de cada programa terminado a Programas; la cuota usada hoy y el estado de Claude
  a Canales. El análisis de abajo (Super Chats, filtros, todo el monitor) llega con el de
  arriba como `hx-swap-oob` en `#vivo-abajo`, así el chat queda en el medio sin JS.
- **Qué se quitó:** la lista de vivos en curso y los tiles globales del resumen (los
  reemplazan los botones, la tarjeta Canal y cinco tiles por alcance), `/parcial/extra`
  (404) y los números de los títulos.
- **Programas sin archivo nuevo:** los tramos salen de `datos.vivos.jsonl`
  (`ProgramSegments`, por día desde las 00:00 − 12 h, la misma `segmentKey` de la 009) y
  los resúmenes de `datos.analisis.jsonl`. Los handlers usan `Store.Lives` y
  `Store.MessagesOf` (no `Snapshot`) y cada mensaje va a su tramo con búsqueda binaria.
- **Tokens nuevos**, en los dos temas: `--link`, `--link-hover`, `--sent-*`,
  `--series-2..5`, `--ia-*`, `--field-border`, `--on-accent`; `--text-muted` pasa a
  #6b6a65 por contraste. Los `style="…"` solo llevan valores calculados
  (`TestTemplateTokens`).
- **Vista previa:** copia también `datos.vivos.jsonl` y `datos.analisis.jsonl` (con los
  nombres que deriva `OpenStore`) a la misma carpeta privada `0700`, que se borra al cortar
  y que barre la limpieza de huérfanas; no copia `datos.claude.json` (el tope arranca en 0)
  ni `datos.canales.json`.

## Consecuencias

- Cada pantalla se puede recargar y compartir por su URL; el botón "atrás" anda sin JS.
- En vivo pide cuatro parciales (2, 5, 5 y 30 s) por pestaña abierta; el de 30 s sigue con
  `Snapshot()` como antes. Programas con "7 días" recorre ~70.000 mensajes en una pasada.
- La vista previa tiene más datos de la API y del modelo fuera del archivo original, con
  las mismas reglas que el resto de la copia.
- Descartadas: pestañas dentro de una sola página con htmx (pierde la URL y el "atrás");
  recalcular Programas con `Snapshot()` (copia 30 días en cada pedido); guardar los
  programas en un archivo nuevo (duplica datos de la API con otra retención); mover la
  detección de cambio de vivo a la tarjeta Canal (cambia un contrato probado sin ganancia).
