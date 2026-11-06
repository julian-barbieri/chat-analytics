# Spec: gráficos con lo último a la izquierda, alertas técnicas y nuevo orden de En vivo

- **Estado:** borrador
- **Tipo:** mejora
- **Rama:** `feat/orden-graficos-alertas-tecnicas`
- **Origen:** mensaje del usuario con tres capturas (Sentimiento del programa, Participación, Alertas)
- **Aprobada por / fecha:** —

## Contexto

1. Programas → "Sentimiento del programa" lista una fila por franja de 5 min
   (`programSentiment`, `programs.go:523`; plantilla `templates/programas.html:94`) pero no
   dice cómo va el programa en total.
2. Los gráficos van de lo más viejo (izquierda) a lo más nuevo (derecha):
   `participationChart` (`server.go:1595`, En vivo y Programas), `viewersLine`
   (`server.go:1553`), `sentimentChart` y `programStrip` (`batch.go:480`, `batch.go:~545`).
   Las filas de "Sentimiento del programa" van de la más vieja arriba a la más nueva abajo.
3. `Alerts` (`batch.go:684`) salta por quejas técnicas **o** por ≥ 5 % de insultos en 10 min;
   la persona quiere solo problemas técnicos de sonido o transmisión (la etiqueta `tecnica`
   de Claude ya es "se queja de audio, cortes o la transmisión", `batch.go:99`).
4. Orden de En vivo (`templates/partials.html`): tiles → Alertas → Resumen de Claude →
   Sentimiento → Preguntas agrupadas + Categorías → Momentos → **Chat** → Super Chats →
   Palabras y autores (al final, en `#vivo-abajo`).
5. La tarjeta "Preguntas" de arriba cuenta con `Classify` (`liveTiles`, `server.go:792`),
   mientras "Preguntas agrupadas" cuenta con los grupos de Claude (`questionGroups`,
   `batch.go:762`): dan números distintos.

## Objetivo

Lo más reciente se lee primero (a la izquierda o arriba) en todos los gráficos, las alertas
son solo técnicas, En vivo queda en el orden pedido y "Preguntas" dice lo mismo que
"Preguntas agrupadas".

## Criterios de aceptación

- [ ] CA1: "Sentimiento del programa" muestra arriba de las franjas una fila "Promedio" con
  el % positivo / neutro / negativo de todos los mensajes etiquetados del tramo (suma de
  conteos, no promedio de %), y las franjas van de la más nueva arriba a la más vieja abajo
  — `TestProgramSentimentAverage`.
- [ ] CA2: en el gráfico de participación (En vivo y Programas) la franja más nueva está en
  `x` más chico; las marcas de ranking, la línea de oyentes, los cortes de programa, la tira
  de programas y las horas también se espejan (la hora de "ahora" queda a la izquierda) —
  `TestParticipationChartNewestLeft`.
- [ ] CA3: lo mismo para "Cómo fue cambiando · cada 5 min" de En vivo (`sentimentChart`) —
  `TestSentimentChart` ajustado.
- [ ] CA4: `Alerts` no tiene alerta de insultos ni los menciona en la calma ni en la nota;
  con 30 % de insultos y 0 quejas técnicas no hay alerta; con 15 quejas técnicas en 5 min
  sí. La tarjeta dice "Claude" (no "Claude y reglas") y el umbral solo de técnicas —
  `TestAlerts` ajustado.
- [ ] CA5: en En vivo el orden es: tiles → Resumen de Claude → Alertas → Sentimiento →
  Momentos → Preguntas agrupadas + Categorías → Palabras y autores → Chat en vivo → Super
  Chats — test de plantilla que busca los `id` en ese orden en la respuesta de
  `/parcial/vivo/analisis` + la página.
- [ ] CA6: con Claude activo y etiquetas en el alcance, el tile "Preguntas" vale la misma
  suma que el encabezado de "Preguntas agrupadas" y dice "agrupadas por Claude"; sin
  etiquetas sigue por reglas y dice "por reglas" — `TestLiveTiles` / test del handler.
- [ ] CA7: gates (`scripts/gates.sh`) y vista previa en claro/oscuro y celular.

## Diseño

- Espejar en Go, no en CSS: un helper `mirrorX(x, w, total)` (`total - x - w`) aplicado a
  barras, líneas, marcas y tira; ticks con `Left = 100 - Left` y la clase `end` pasa a la
  de la izquierda (CSS: la primera alineada a la izquierda, la última a la derecha, como
  hoy). El `path` de oyentes se arma recorriendo las franjas al revés. `max.`/escalas no
  cambian.
- Programas: `programSentiment` suma `Pos/Neu/Neg` de las `SentSlot` del tramo en una
  `SentRow` `Avg` aparte (con `Low` si el total < `minChartMsgs`) e invierte `SentRows`.
- `Alerts`: se borran `insults`, `insultAlertPct`, `insultWindow` (la ventana del conteo
  pasa a 5 min) y los textos de insultos. El texto de umbrales de la plantilla queda solo
  con las 15 quejas técnicas.
- Orden: Claude arriba de Alertas; Momentos arriba del `grid2` de Preguntas/Categorías;
  `Monitor` pasa del `#vivo-abajo` al final de `#vivo-analisis` (justo antes del chat).
  Super Chats queda abajo del chat.
- Tile: `liveTiles` recibe el conteo de grupos (`-1` = sin Claude, por reglas);
  `channelAnalysis` lo saca de `questionsData`/`questionGroups` (misma llamada, sin
  recorrer de nuevo).

## Fuera de alcance

- La tira "Hoy en el canal" (ya va con lo actual primero) y las tablas.
- Cambiar el prompt o el modelo de las tandas; Programas → "Preguntas" (sigue con el
  resumen de Claude o reglas).

## Riesgos

- Ninguno de cuota, `datos.jsonl`, concurrencia ni privacidad: solo vista. Los insultos
  siguen en Categorías y en las etiquetas; solo deja de haber alerta.
