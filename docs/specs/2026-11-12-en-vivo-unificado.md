# Spec: En vivo sin selector programa/día y resúmenes de ayer que faltan

- **Estado:** borrador
- **Tipo:** mejora
- **Rama:** `feat/en-vivo-unificado`
- **Origen:** pedido del usuario (2026-11-12), puntos 6 y 4; maquetas en
  https://claude.ai/artifact/EgkYGxJybkC2KWBcTGv4Vd (opción A del punto 6). Tanda 3 de 3.
- **Aprobada por / fecha:** —

## Contexto

- **Programa / día (6):** En vivo tiene el selector "Programa en curso" / "Todo el día
  del canal" (`templates/partials.html:80-86`, parámetro `ver` en `server.go:417`,
  alcance en `channelScope`, `server.go:1145`). Las dos vistas repiten las mismas
  tarjetas con números parecidos; el día entero ya lo cubren Programas (Hoy), la tira
  "Hoy en el canal" (`dayStrip`, `server.go:1782`) y Momentos, que siempre usa 24 h.
- **Resumen perdido (4):** Tardes Bestiales del 11/11 (16:04 a 18:19, con un corte a
  las 16:19) muestra temas "por reglas". El primer pedazo se resumió con error ("Muy
  pocos mensajes"); el pedazo grande terminó a las 18:19, cuando el monitor se apagó, y
  nunca se resumió: `pendingSegments` descarta lo que terminó antes de las 00:00 de hoy
  (`analyst.go:456`, `sg.End.Before(today)`), así que al día siguiente ya no se pide.
  Verificado en `datos.analisis.jsonl`: no hay análisis después de las 16:26 del 11/11.

## Objetivo

En vivo muestra siempre lo del programa al aire, con el total del día como contexto,
sin selector. Un programa de ayer que terminó sin resumen (por ejemplo, porque el
monitor estaba apagado) se resume cuando el monitor vuelve a andar.

## Criterios de aceptación

- [ ] CA1: En vivo no tiene más los botones "Programa en curso" / "Todo el día del
  canal". Con vivo al aire el alcance es el programa en curso; sin vivo, lo de hoy
  del canal (como hoy con "Esperando el próximo vivo"). Un `ver=` de un link viejo se
  ignora sin error ni reflejo en el HTML. — test de `channelScope` y de render.
- [ ] CA2: con programa al aire, las tarjetas Mensajes y Personas (y Likes, si hay
  dato) suman abajo el total del día del canal ("4.910 en el día"); la comparación
  con lo típico (`Trend`) sigue. — test de `liveTiles` con un día de dos programas.
- [ ] CA3: en la tira "Hoy en el canal", cada programa terminado es un link a su
  detalle en Programas (`/programas?canal=…&tramo=<segmentKey>`); el que está al aire
  y los ratos sin vivo no. — test de `dayStrip`.
- [ ] CA4: la tarjeta de Claude de En vivo muestra siempre el programa en curso (se va
  el caso "Resúmenes de Claude" de `ver=dia`, `server.go:2512`); sin vivo, el link a
  Programas de hoy como ahora. — test de `claudeView`.
- [ ] CA5: el analista también pide el resumen de los programas que terminaron **ayer**
  (desde las 00:00 de ayer) y no tienen análisis final, después de los de hoy y con el
  mismo tope del día; nunca más viejos. — `TestPendingSegments` (o el test del
  analista) con un tramo de ayer sin análisis, uno de ayer con análisis y uno de
  anteayer.
- [ ] CA6: la clave del tramo de ayer que resume el analista es la misma que usa
  Programas con `dia=ayer`, así el detalle encuentra el resumen. — test que compara
  `segmentKey` del analista con la de `programSegs` para un programa de ayer.
- [ ] CA7: README y AGENTS.md dicen que ya no hay selector en En vivo y que los
  programas de ayer sin resumen se resumen al volver a andar (puede gastar del tope de
  hoy). Vista previa de En vivo con y sin vivo, claro, oscuro y celular.
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

- `server.go`: `channelScope` decide el alcance solo por si hay vivo (se va `ver`
  como entrada; `liveScope.ver` queda interno para lo que ya lo usa). Con programa,
  cuenta también mensajes y personas del día (ya recorre los vivos del día) y se los
  pasa a `liveTiles`. `dayStrip` arma el `Href` de cada bloque terminado. Se borran
  el selector de `partials.html` y el `ver` de `vivo-q` y `analysisQuery`.
- `analyst.go`: `pendingSegments` arma los tramos de ayer con `from` = 00:00 de ayer
  (como Programas) y los de hoy como ahora; acepta los terminados desde las 00:00 de
  ayer. `done` se arma con los análisis desde esa hora. El orden (más nuevos primero)
  deja los de hoy adelante.

## Fuera de alcance

- Un resumen de "todo el día" agregado (sentimiento o preguntas del día entero): si
  se extraña, va en Programas en otra tarea.
- Temas de Claude durante el programa sin apretar el botón (tandas, ADR 0009).
- Reintentar los pedazos que ya tienen un análisis con error.

## Riesgos

- **Gasto de Claude:** al volver a andar puede pedir varios resúmenes de ayer de
  golpe; salen uno por vez (`analystTick`), cada uno chequea el tope del día
  (`claudeWorstCase`) y se cortan al llegar. Lo de hoy tiene prioridad.
- **Privacidad:** se manda lo mismo que hoy (texto sin autores, sin spam); solo cambia
  cuándo.
- **Conflictos con las tandas 1 y 2:** las tres tocan `partials.html` y
  `server.go`; la que entre después se rebasa sobre `main`.
- Sin cambios de cuota de YouTube ni de `datos.jsonl`.
