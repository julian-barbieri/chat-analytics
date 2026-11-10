# Spec: En vivo — Momentos de todo el canal, Categorías con Claude y mensajes de cada motivo

- **Estado:** implementada
- **Tipo:** mejora (con un bug)
- **Rama:** `feat/en-vivo-momentos-categorias`
- **Origen:** pedido del usuario con capturas de En vivo (2026-11-10)
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-10

## Contexto

1. **Bug, Momentos.** Con "Programa en curso", "Momentos con más participación (para reels)"
   solo muestra el programa al aire, aunque se elija "Últimas 6 h" o "Últimas 24 h".
   **Causa:** `channelScope` (`server.go:969`) recorta los mensajes, los espectadores y los
   vivos (`ls.msgs`, `ls.viewers`, `ls.pids`) al tramo del programa, y `analysisData`
   (`server.go:883`) se los pasa tal cual a `participationView`. Con "Todo el día" también se
   corta en las 00:00 (`from := dayStart(now)`), así que "Últimas 24 h" no cubre 24 h.
2. **Categorías por reglas.** La tarjeta usa `catRows` (`server.go:163`, `Classify`), aunque las
   tandas ya traen una categoría validada por mensaje (`MessageLabel.Cat`, `batch.go:209`).
3. **Motivos sin detalle.** "Por qué hay mensajes negativos" y "Qué está gustando" muestran
   2 citas fijas por motivo (`motiveQuotes`, `batch.go:300`). No hay forma de ver el resto.

## Objetivo

Momentos cubre las últimas 6 o 24 h del canal con cualquier alcance. Categorías usa lo que
dijo Claude cuando hay etiquetas. Cada motivo despliega todos sus mensajes con una flecha,
cerrada por defecto.

## Criterios de aceptación

- [x] CA1: con "Programa en curso", el gráfico y el ranking de Momentos incluyen franjas de un
  programa anterior del mismo canal (otro título y otro vivo de hoy o de ayer, dentro de 24 h).
  Nunca incluyen mensajes ni espectadores de otro canal. — `TestMomentsWholeChannel`
- [x] CA2: con "Todo el día del canal", Momentos también cubre 24 h (incluye franjas de antes
  de las 00:00). — `TestMomentsWholeChannel` (caso día)
- [x] CA3: con Claude prendido y etiquetas, Categorías cuenta cada mensaje etiquetado con su
  `Cat` de Claude. Los que `Classify` marca como spam se cuentan como spam, porque no se le
  mandan a Claude. Los no etiquetados se cuentan por reglas. La nota dice "Por Claude" y "N
  todavía sin etiquetar (por reglas)" cuando N > 0. Los % siguen sumando 100. —
  `TestCatRowsClaude`
- [x] CA4: sin Claude, o con Claude y sin ninguna etiqueta en el alcance, Categorías queda
  igual que hoy (texto "Por reglas simples…"). — `TestCatRowsShare` sin cambios y
  `TestCatRowsClaude` (caso apagado)
- [x] CA5: el CSV (`csvRow`) y las "Preguntas" de reglas siguen con `Classify`. —
  `TestFlowAgainstFakeAPI` y los tests de CSV sin cambios
- [x] CA6: cada motivo (no "Otros") lleva un `<details>` cerrado por defecto con la cantidad
  ("Ver 12 mensajes") y, adentro, todos sus mensajes del alcance sin autor, sin repetidos y
  los más nuevos primero. Como mucho son `motiveListMax` (100); si hay más, se avisa "y N más".
  Sin abrir no se ve ninguna cita. — `TestMotives` (ampliado) y `TestSentimentFold`
- [x] CA7: el estado abierto sobrevive al refresco de 30 s. Las claves `neg1…neg3` y
  `pos1…pos3` se suman a `foldSections` y viajan en `abrir`, como `preguntas`. Un valor que
  no esté en la lista no se refleja. — `TestFoldParam` (casos nuevos) y `TestSentimentFold`
- [x] CA8: `AGENTS.md` (regla del clasificador y mapa de `server.go`) y un ADR 0010 dicen
  que Categorías usa Claude cuando hay etiquetas y que el CSV sigue por reglas.
- [x] CA10 (pedido durante /implementar): cada momento de "Momentos con más participación"
  muestra a la vista solo el horario y el título (más Mirando, Personas y %). Una flecha
  (`<details>` cerrado por defecto) despliega el link del reel, los temas y todos los
  mensajes de la franja, en orden, sin los vacíos ni el spam, como mucho `momentListMax`
  (100) con "y N más". Sigue abierta tras el refresco con `mom1…mom5` en `abrir`. —
  `TestMomentFold`, `TestLiveParticipationView*`
- [ ] CA9: navegador con la vista previa: En vivo en claro, oscuro y ancho de celular.
- [x] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

- `liveScope` suma `partMsgs`, `partViewers` y `partIDs`: los vivos del canal de las últimas
  24 h (los de `snap.Videos` y `s.st.Lives(now-24h)` con ese `ChannelID`). Se arman igual en
  los dos alcances. `analysisData` recibe esos datos solo para `participationView`; el resto
  de las tarjetas sigue con `ls.msgs`. El vivo suelto (`server.go:782`) no cambia.
- `catRows(msgs, labels)`: si `labels` está vacío, se comporta como hoy. Si no, usa `Cat`
  cuando hay etiqueta, spam por reglas primero y `Classify` para el resto, y devuelve
  cuántos quedaron sin etiquetar. El ejemplo sigue siendo el último mensaje de la categoría.
  `channelAnalysis` ya tiene `labels`, así que no hay lecturas nuevas del `Store`.
- `MotiveRow` cambia `Quotes` por `Msgs []string` (hasta `motiveListMax`), `More int` y
  `Fold Fold`. La plantilla `motivos` usa `<details>`/`<summary>` con `hx-get`, igual que
  "Ver N más" de Preguntas. La clave es la posición (`neg1` = primer motivo negativo).

## Fuera de alcance

- Resumen de Claude que se escribe a medida que llega (streaming): otra spec.
- Cambiar el prompt o el esquema de las tandas, y Momentos en Programas o en el vivo suelto.

## Riesgos

- **Performance:** Momentos ahora recorre 24 h de mensajes del canal en cada refresco de
  30 s; antes recorría el tramo. Es el mismo orden que "Todo el día". Se filtra con
  `MessagesOf` y los IDs del canal, sin `Snapshot` extra.
- **Plegado por posición:** si cambia el orden de los motivos entre refrescos, el abierto
  puede pasar a ser otro motivo. Se acepta: con una clave por texto se rompería la lista
  cerrada de `abrir`.
- **Privacidad:** se muestran más mensajes del chat, pero sin autor y solo en el dashboard
  local. No se manda nada nuevo a Claude ni a YouTube y no cambia la cuota.
