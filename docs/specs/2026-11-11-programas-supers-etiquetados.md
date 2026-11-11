# Spec: Super Chats del programa con mensaje, autor y tags

- **Estado:** borrador
- **Tipo:** feature
- **Rama:** `feat/programas-supers-etiquetados`
- **Origen:** pedido del usuario (2026-11-11): "en la sección de Super Chats del programa agregá el mensaje y el usuario y cómo fue categorizado (si es pregunta, etc.), etiquetado igual que el resto de los mensajes del chat del programa".
- **Aprobada por / fecha:**

## Contexto

En el detalle de Programas, "Super Chats del programa" (`templates/programas.html:129`)
muestra solo la tabla por moneda (`programs.go:712`). No se ve quién pagó, qué escribió
ni de qué se trata, aunque el "Chat del programa" ya etiqueta cada mensaje
(`programChat`, `programchat.go:152`): categoría (`catOf`, ADR 0010), sentimiento y
grupo de preguntas de las tandas y temas del resumen.

## Objetivo

Debajo de la tabla por moneda, la lista de los Super Chats y Super Stickers del tramo en
orden de hora, cada uno con hora, autor, tipo y monto, texto y los mismos tags que en
"Chat del programa".

## Criterios de aceptación

- [ ] CA1: `programSupers` devuelve solo los mensajes con `Super`, en orden de hora, con
  `Kind` ("Super Chat" / "Super Sticker"), `Amount` (`AmountText`) y los mismos `Cat`,
  `Sent`, `Group` y `Topics` que la fila de `programChat` del mismo mensaje; un Super
  Sticker sin texto muestra "Super Sticker: <nombre>"; sin etiquetas, la categoría es
  por reglas y sin sentimiento — `TestProgramSupers` (nuevo).
- [ ] CA2: el detalle renderizado tiene, dentro de `#supers`, el autor, el texto, el
  monto y el badge de categoría de cada pago; sin pagos sigue "Sin Super Chats ni Super
  Stickers." — `TestProgramSupers` (render con `programas.html`).
- [ ] CA3: los tests de `programChat` siguen pasando sin cambios.
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

- Sacar de `programChat` el armado de una fila a `chatLine` (mismo resultado) y sumar a
  `ChatLine` `Kind` y `Amount` (vacíos en mensajes comunes).
- `programSupers(msgs, labels, groups, topics) []ChatLine` reusa `chatLine`.
- `ProgramDetail.SuperMsgs` (reemplaza `SupersMsg`, que no se usaba); se arma después de
  las etiquetas, con las mismas `labels`, `groups` y `topics` que el chat.
- Plantilla: lista `qlist chatl` con el mismo markup de tags del chat, más un badge
  `super` con tipo y monto. Sin colores nuevos.

## Fuera de alcance

- La tarjeta de Super Chats de En vivo (`superView`).
- Filtros o paginado en la lista: son pocos pagos por programa.
- El CSV (ya tiene autor, texto, categoría y monto).

## Riesgos

- Privacidad: muestra autores y textos que ya se ven en "Chat del programa"; nada sale
  del dashboard. `html/template` escapa el texto.
