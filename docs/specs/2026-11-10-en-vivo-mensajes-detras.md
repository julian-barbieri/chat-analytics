# Spec: En vivo, ver los mensajes detrás de cada dato

- **Estado:** borrador
- **Tipo:** mejora
- **Rama:** `feat/en-vivo-mensajes-detras`
- **Origen:** pedido del usuario con capturas de En vivo (2026-11-10)
- **Aprobada por / fecha:** —

## Contexto

En En vivo se ven conteos de temas, categorías, grupos de preguntas y sentimiento, pero no
los mensajes que los forman:

- Temas del resumen de Claude: `claude-block` (`templates/partials.html:252`) muestra
  `texto · N mensajes · M personas` sin nada más. `validClaudeResult` guarda solo 3 IDs por
  grupo (`claude.go:571`, `ClaudeGroup.Examples`), así que hoy no se pueden listar todos.
- Categorías (`partials.html:329`) muestra un ejemplo por fila y no lleva el chip "Claude"
  aunque use las tandas. El pie dice "de del programa en curso": `CatOf` ya trae el "del".
- "Preguntas agrupadas" (`#preguntas`, `partials.html:308`) muestra el texto del grupo y
  la cantidad, sin las preguntas originales (sí están: `MessageLabel.Group`).
- "Cómo fue cambiando" (`partials.html:125`) es un SVG con un `<title>` por barra; no se
  puede ver qué mensajes hay detrás de una franja.
- La franja de estado muestra "Chat en tiempo real: 0 de 2 vivo(s) conectados ahora."
  (`worker.go:662`), que no le sirve a quien mira.

## Objetivo

Desde cada dato de En vivo se pueden ver los mensajes que lo forman, sin JS propio y con el
estado plegado sobreviviendo al refresco de 30 s.

## Criterios de aceptación

- [ ] CA1: `validClaudeResult` guarda los IDs de todos los mensajes válidos de cada grupo
  en un campo nuevo `IDs` (con `omitempty`); `Examples` sigue con los 3 primeros. Un
  análisis guardado sin `IDs` se carga y usa `Examples`. — `TestValidClaudeResult`
  (caso nuevo) y `TestClaudeAnalysisCompat`.
- [ ] CA2: cada tema del resumen es un `<details>` (claves `tema1`–`tema5` en
  `foldSections`) que muestra hasta 20 mensajes del tema (autor, hora, texto) y "y N más".
  — `TestClaudeTopicsFold`.
- [ ] CA3: la tarjeta Categorías lleva `<span class="ia">Claude</span>` en el título solo si
  `CatClaude`; el pie dice "del programa en curso" (sin "de del"). — `TestCategoriesCard`.
- [ ] CA4: cada fila de Categorías con mensajes es plegable (claves `cat-<Cat*>`) y muestra
  hasta 20 mensajes de esa categoría (la misma cuenta que la fila: Claude o reglas) y
  "y N más". — `TestCategoriesFold`.
- [ ] CA5: cada grupo de "Preguntas agrupadas" es plegable (claves `qg1`–`qg30`, por
  posición en la lista ordenada) y muestra hasta 20 preguntas del grupo, la más nueva
  primero. — `TestQuestionGroupsFold`.
- [ ] CA6: la franja de estado ya no dice "Chat en tiempo real: … conectados ahora"; las
  notas de lectura común, pausa y tope siguen. — `TestFlowAgainstFakeAPI` (chequeo de
  `ChatNote`).
- [ ] CA7: cada barra con color de "Cómo fue cambiando" es un `<a>` con `aria-label`
  (hora, %, mensajes) y `hx-get` del análisis con `franja=<unix>`. Con `franja`, debajo del
  gráfico aparece "Mensajes de las HH:MM a HH:MM · <programa>" con tres `<details>`:
  Negativos (abierto), Neutros y Positivos, hasta 20 mensajes cada uno y "y N más"; la barra
  queda marcada (clase `sel`) y hay un botón "Cerrar" que pide el análisis sin `franja`. —
  `TestSentimentSlotDetail`.
- [ ] CA8: `franja` se acepta solo si es un inicio de franja de 5 min dentro de la ventana
  del gráfico; cualquier otro valor se ignora y no se refleja en el HTML. El refresco de
  30 s conserva la franja (input oculto `.vq`). — `TestSentimentSlotParam` (tabla).
- [ ] CA9: los colores nuevos (barra marcada) están en los dos temas. — `TestTemplateTokens`.
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde y vista previa revisada en
  las tres pantallas, claro y oscuro y ancho de celular.

## Diseño propuesto

- `claude.go`: `ClaudeGroup.IDs []string`; `fetchClaude` (`server.go:1419`) pide también
  los IDs de los temas. Los mensajes se buscan entre los guardados, como los ejemplos.
- Plegado: mismo mecanismo que motivos (`motiveFolds`, `foldState`): `<summary>` con
  `hx-get` y la sección cambiada en `abrir`. Las claves nuevas van a `foldSections`, que
  sigue siendo una lista cerrada. Tope compartido `maxFoldMsgs = 20`.
- Categorías: `catRowsWith` / `catRows` devuelven también los mensajes de cada fila (los
  más nuevos primero, cortados al tope), y el chip y el pie salen de `CatClaude` / `CatOf`.
- Preguntas: `questionGroups` junta los mensajes de cada grupo al contar.
- Sentimiento, opción A (recomendada): parámetro `franja` validado contra la ventana de
  `sentimentChart`, mensajes de la franja con `Store.MessagesOf` de los vivos del canal y
  sus `Labels` (solo los de esa franja, no las tandas de 7 días). Alternativas, si la
  persona las prefiere: **B** las mismas tres listas pero como pestañas Neg/Neu/Pos (una
  visible por vez, otro parámetro más); **C** sin clic en el gráfico, una lista de franjas
  plegables debajo (más larga: hasta 288 franjas en "Todo el día").
- `worker.go:662`: borrar la parte de `streaming`.

## Fuera de alcance

Temas y quejas en Programas, cambiar cómo Claude arma los temas, marcar respondida desde la
tarjeta, el CSV.

## Riesgos

- `datos.analisis.jsonl`: el campo nuevo es opcional; las líneas viejas siguen cargando.
  Crece unos KB por análisis (IDs, no textos); mismos 30 días.
- Performance: el detalle de franja lee solo los mensajes del canal de esos 5 min; las
  listas plegadas cortan a 20 mensajes y el HTML no crece con el día.
- Privacidad: se muestran autores y textos que ya están en el dashboard; nada nuevo sale a
  Claude ni a YouTube. Sin cambios de cuota.
- `abrir` y `franja` vienen del usuario: lista cerrada y validación contra la ventana; nada
  de `template.HTML`.
