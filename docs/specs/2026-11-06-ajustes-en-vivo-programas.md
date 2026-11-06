# Spec: ajustes de En vivo y Programas

- **Estado:** borrador
- **Tipo:** mejora
- **Rama:** `feat/ajustes-en-vivo-programas`
- **Origen:** pedido del usuario (capturas de la tira "Hoy en el canal" y del gráfico de participación), 2026-11-06
- **Aprobada por / fecha:** —

## Contexto

- La tira "Hoy en el canal" va de la medianoche a ahora: el programa en curso queda a la derecha, fuera de la vista sin scrollear (`dayStrip`, `server.go:1333`).
- "Preguntas agrupadas para producción" muestra todos los grupos y un botón "Marcar respondida" por fila (`templates/partials.html:317-321`).
- La pestaña dice "Programas de hoy" (`templates/layout.html:7,325`) aunque la pantalla también muestra ayer y 7 días.
- El detalle de un programa no tiene gráfico de participación (`programDetail`, `programs.go:440`).
- "Todo el monitor · últimas 24 h" junta todos los canales de 24 h (`monitorView`, `server.go:774`, llamado en `server.go:751` y `:871`).
- Las alertas técnicas usan la etiqueta de Claude y, si el mensaje no la tiene, `TechIssue` por reglas (`Alerts`, `batch.go:674`).
- El gráfico de momentos no dice qué programa es cada tramo (sí lo hace "Sentimiento del chat" con `Strip`, `batch.go:505`) y los espectadores van en un panel aparte arriba (`viewersLine`, `server.go:1543`; plantilla `partials.html:180-186`).

## Objetivo

Que En vivo muestre primero lo que está pasando (programa en curso a la izquierda, datos del programa y no de 24 h) y que Programas tenga el gráfico de participación de cada programa.

## Criterios de aceptación

- [ ] CA1: en "Hoy en el canal" el primer bloque es el tramo actual (en vivo o "sin vivo" final) y los anteriores siguen hacia la derecha, del más nuevo al más viejo — `TestDayStrip` (orden de `Blocks`).
- [ ] CA2: con Claude, la tarjeta se titula "Preguntas agrupadas" y no tiene botones "Marcar respondida" ni "Deshacer" (sí sigue diciendo "Respondida" si había una marca guardada) — test de render de `vivo-preguntas`.
- [ ] CA3: con más de 5 grupos se ven los 5 primeros y el resto va dentro de un `<details>` ("Ver N más"); con 5 o menos no hay `<details>` — mismo test, casos 5 y 7 grupos.
- [ ] CA4: la pestaña y el `<title>` dicen "Programas" — test sobre `GET /programas`.
- [ ] CA5: el detalle de un programa (también de ayer o 7 días) muestra el gráfico de barras de participación de todo el tramo, franjas de 5 min entre el inicio y el fin del programa, con el top marcado — `TestProgramDetailParticipation`.
- [ ] CA6: el bloque "Todo el monitor · últimas 24 h" pasa a "Palabras y autores del programa": solo mensajes del programa en curso del canal elegido (el mismo filtro que "Programa en curso", `segmentMessages`); con "Todo el día del canal", los del día del canal — test de `channelAnalysis`.
- [ ] CA7: una queja técnica cuenta solo si el mensaje tiene etiqueta de Claude con `Tech`; un mensaje sin etiqueta no suma aunque `TechIssue` lo detecte. Si ningún mensaje de los últimos 5 min tiene etiqueta, la tarjeta lo dice ("Quejas técnicas: sin análisis de Claude") en vez de "0 quejas" — `TestAlerts` actualizado. Los insultos no cambian (Claude con caída a reglas).
- [ ] CA8: debajo de las barras de "Momentos con más participación" hay una franja con el programa de cada tramo, como `Strip` de "Sentimiento del chat" (incluida la lista accesible) — test de `participationChart`.
- [ ] CA9: la línea de espectadores va dentro del mismo SVG de las barras, con su escala propia (máximo a la derecha, "máx. N mirando"), y desaparece el panel `.poyentes` aparte; sin dato de espectadores no se dibuja — test de render.
- [ ] CA10: verificación en el navegador (vista previa) de En vivo y Programas en claro, oscuro y ancho de celular.
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

- `dayStrip`: invertir `blocks` al final; la tira deja de usar `X` en minutos para el orden (sigue el scroll horizontal; con el actual primero no hace falta scrollear).
- `vivo-preguntas`: partir `Rows` en `Top` (5) y `Rest` en `questionsData`; sacar los botones de la plantilla. La ruta `POST /preguntas/respondida` y `Answers` quedan (no se borran datos), solo no se ofrecen.
- Layout: "Programas de hoy" → "Programas" en pestaña y `<title>`; el encabezado de la lista (`programs.go:407`) sigue diciendo "Programas de hoy · …".
- `Participation` gana una variante con ventana `[from, to)` (hoy fija `now-24h`); `programDetail` la usa con el tramo y reusa `participationChart` + el template del gráfico extraído a un `define` común.
- `monitorView` recibe los mensajes del alcance (`ls.msgs`) en lugar de `snap.Msgs`; cambia el título y el subtítulo.
- `Alerts`: sacar la rama `!ok && TechIssue(...)`; contar cuántos mensajes de 5 min tienen etiqueta para el texto de "sin análisis".
- `participationChart`: armar `Strip` con los tramos del canal (`ProgramSegments`) igual que `sentimentChart`; reusar `SentProg` y el CSS `.sprogs`.
- Espectadores: el path de `viewersLine` se dibuja en el SVG de `.pchart` (misma `viewBox`, `vector-effect="non-scaling-stroke"`), con la etiqueta "máx. N mirando" en HTML a la derecha; se borra `.poyentes`.

## Fuera de alcance

- Borrar `POST /preguntas/respondida`, `Answers` o `datos.respondidas.jsonl`.
- Cambiar cómo Claude etiqueta (`batchPrompt`) o agregar pedidos nuevos a Claude: las alertas usan las etiquetas que ya llegan en las tandas.
- Cambiar los temas "por reglas" (`TopTopics`).

## Riesgos

- Alertas sin Claude: con Claude apagado o el canal sin el check, ya no hay alerta técnica. Se mitiga con el texto explícito de CA7; no cambia el gasto (las etiquetas ya se piden).
- Gráfico en Programas para 7 días: `Participation` por tramo lee mensajes y espectadores ya filtrados por el vivo; se mide que el detalle no recorra el histórico entero (usa `Store.MessagesOf` y `Store.Viewers`, no `Snapshot`).
- Sin cambios de cuota, de `datos.jsonl` ni de concurrencia.
