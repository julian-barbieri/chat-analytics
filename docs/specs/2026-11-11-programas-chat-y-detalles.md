# Spec: chat completo, franjas clickeables y temas/preguntas desplegables en Programas

- **Estado:** aprobada
- **Tipo:** mejora
- **Rama:** `feat/programas-chat-y-detalles`
- **Origen:** pedido del usuario (2026-11-11)
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-11

## Contexto

En el detalle de Programas (`templates/programas.html:46`, `programDetail` en
`programs.go:610`) no hay forma de leer el chat del programa ni de ver qué mensajes hay
detrás de un número:

- Las barras de "Participación durante el programa" usan `part-chart`
  (`templates/partials.html:299`), que no tiene links.
- Las filas de "Sentimiento del programa" (`sfila`, `programas.html:162`) tampoco. En
  En vivo, "Sentimiento del chat" sí: cada barra pide su franja (`slotLinks`,
  `server.go:1355`) y abajo aparece "Mensajes de las HH:MM a HH:MM" con pestañas
  Negativos / Neutros / Positivos y "Cerrar".
- "Temas de conversación" (Claude) son chips sin mensajes; las "Preguntas" (grupos de
  las tandas o del resumen) no se despliegan. En En vivo sí (`topicFolds`,
  `server.go:2457`; `qgrupo`, `partials.html:327`).

## Objetivo

Que en el detalle de un programa se pueda (1) leer todo su chat, plegado por defecto, con
buscador y filtros y cada mensaje con hora y tags; (2) tocar una franja de Participación
o de Sentimiento y ver sus mensajes como en En vivo; (3) desplegar los mensajes de cada
tema y cada grupo de preguntas.

## Criterios de aceptación

- [ ] CA1: al final del detalle hay "Chat del programa (N mensajes)" en un `<details>`
  cerrado por defecto. Abierto, lista los mensajes de chat del tramo en orden de hora
  (`HH:MM`, autor, texto) con tags: categoría (la de Claude si hay etiqueta y Claude
  está prendido, si no la de reglas; ADR 0010), sentimiento (solo con etiqueta), "Pregunta"
  (con el texto del grupo en `title` si tiene grupo) y tema (los temas del resumen de
  Claude que incluyen ese mensaje en sus `IDs`). — `TestProgramChatRows` (tabla).
- [ ] CA2: filtros que se combinan: texto (en texto y autor, sin distinguir mayúsculas
  ni tildes, hasta 100 runas), categoría, sentimiento (incluye "sin etiquetar") y tema.
  Valores fuera de la lista cerrada se ignoran y no se reflejan en el HTML; el texto sí,
  escapado. Muestra "N de M mensajes" y pagina de a 200 con "Ver 200 más". —
  `TestProgramChatFilter` y `TestProgramChatParams`.
- [ ] CA3: lo abierto del chat y sus filtros viajan en la URL (`programsQuery`), así el
  refresco de 60 s de "Hoy" no lo cierra ni borra la búsqueda; el campo de texto lleva
  `hx-preserve` para no perder lo que se está escribiendo. — `TestProgramsQueryChat`.
- [ ] CA4: cada barra de Participación (no las franjas vacías reservadas hasta el fin
  esperado) y cada fila de Sentimiento del programa (no el Promedio) es un link con
  `aria-label` "… Ver los mensajes". Al tocarla aparece debajo de ese gráfico
  "Mensajes de las HH:MM a HH:MM" con "Cerrar" y pestañas Todos / Negativos / Neutros /
  Positivos con cantidades (sin etiquetas, solo Todos). Desde Participación abre en
  Todos; desde Sentimiento, en Negativos (como En vivo). La franja seleccionada se marca.
  — `TestProgramSlotDetail`.
- [ ] CA5: `franja` solo vale si es el inicio de una franja de 5 min dentro del tramo;
  si no, no hay detalle. — `TestProgramSlotDetail` (casos inválidos).
- [ ] CA6: en Temas de conversación (Claude) y en Preguntas (grupos de las tandas o del
  resumen), cada ítem con más de 1 mensaje se despliega con una flecha y muestra sus
  mensajes (`fold-msgs`: autor, hora, texto, con tope y "y N más"); con 1 mensaje queda
  como hoy. El estado abierto sobrevive al refresco. — `TestProgramFolds`.
- [ ] CA7: vista previa: las tres pantallas en claro y oscuro y a ancho de celular; el
  chat de un programa largo abre sin trabarse.
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

- `programsQuery` suma `abrir` (lista cerrada: `chat`, `tema1…tema5`, `preg1…preg30`),
  `franja`, `sent` (`todos|neg|neu|pos`), `desde` (`part|sent`), `buscar`, `cat`,
  `senti`, `tema`, `pag`. `values()` los agrega solo si son válidos.
- Chat: función pura `programChat(msgs, labels, topics, claudeOn, filtro) ChatView` en
  `programs.go` (filas, totales, opciones de los selects). Se arma solo con `abrir=chat`,
  con los `msgs` y `labels` que `programDetail` ya tiene: sin lecturas extra del `Store`.
  Template `programa-chat` con un `<form>` htmx (`hx-get` al parcial, `submit` y
  `change`), sin JS propio.
- Franjas: reusar `SlotDetail` / `SlotTab` y `fold-msgs` de En vivo. `part-chart` recibe
  un `Href` opcional por barra (En vivo sigue sin link en Momentos); `sfila` pasa a ser
  `<a>` cuando tiene `Href`. Los mensajes de la franja salen de los `msgs` del tramo.
- Temas y preguntas: como `topicFolds` / `qgrupo`, con `hx-get` en el `<summary>` hacia
  `/parcial/programas` y `hx-target="#programas"`.
- Tests primero: los de los CA, sobre funciones puras y `GET /parcial/programas/detalle`
  con `httptest`.

## Fuera de alcance

- En vivo (salvo el `Href` opcional en `part-chart`), el CSV, "Marcar respondida",
  pedir análisis a Claude o etiquetar mensajes que no tienen etiqueta.
- Tema por mensaje fuera de los `IDs` que dio Claude en el resumen (los mensajes de un
  programa sin resumen no llevan tag de tema).

## Riesgos

- Cuota, Claude, `datos.jsonl`: ninguno; solo lectura de lo que ya se carga.
- Performance: un programa de 24 h puede tener miles de mensajes. Se pagina de a 200 y
  el chat solo se arma abierto.
- Privacidad: muestra autores, como el resto del dashboard (local, sin exponer). El
  texto buscado se escapa con `html/template`.
- Refresco de 60 s mientras se escribe: mitigado con `hx-preserve` y el estado en la URL.
