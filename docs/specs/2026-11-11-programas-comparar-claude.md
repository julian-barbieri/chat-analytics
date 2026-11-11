# Spec: Programas: comparar que se vea, barras de ancho fijo y preguntas y categorías de Claude

- **Estado:** aprobada
- **Tipo:** bug
- **Rama:** `fix/programas-comparar-claude`
- **Origen:** mensaje del usuario con capturas (2026-11-11): los botones de "Comparar con" no hacen nada, la barra de un programa recién empezado ocupa todo el ancho y Preguntas / Temas salen "por reglas".
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-11

## Contexto

En el detalle de Programas:

1. **"Comparar con" no cambia nada.** Si las franjas del tramo no tienen dato de espectadores (`ParticipationRange` devuelve `ok=false`, `analyze.go:265`), el gráfico va por personas y `participationChartCmp` solo dibuja banda y línea con `ok` (`server.go:2071`). `compareSetup` (`programs.go:554`) igual deja los botones habilitados: se elige "Lo normal" o "Emisión anterior", la página se recarga y el gráfico queda igual.
2. **Barras estiradas.** El ancho del SVG es `len(slots)` (`server.go:2011`) con `preserveAspectRatio="none"`: un programa al aire con 1 franja es una barra de todo el ancho; con 3, tres barras gigantes.
3. **Preguntas por reglas.** La tarjeta usa `Analyze(msgs).Questions` (`programs.go:507`) salvo que haya resumen final de Claude, que existe solo cuando el programa terminó. Las tandas ya agrupan preguntas (`questionGroups`, `batch.go:758`) y En vivo las usa (`questionsData`, `server.go:1469`). La columna Preguntas de la lista cuenta con `Classify` (`programs.go:215`).
4. **Temas por reglas.** Igual: `bars(an.Topics)` mientras no hay resumen. Las tandas no sacan temas, pero sí categoría por mensaje (`MessageLabel.Cat`, `catRowsWith`, ADR 0010).

**Reproducción:** en la vista previa, un programa al aire de una sola franja sin dato de espectadores (captura del usuario). Se cubre con tests nuevos (abajo).

## Objetivo

Que "Comparar con" solo ofrezca lo que se puede dibujar, que cada franja de 5 min tenga el mismo ancho sin importar cuánto lleva el programa, y que Preguntas y Categorías salgan de Claude cuando está prendido.

## Criterios de aceptación

- [ ] CA1: con el gráfico por personas (sin %), "Emisión anterior", "Mismo día la semana pasada" y "Lo normal" quedan deshabilitados y el motivo dice que falta el dato de cuántos miraban; "Ninguna" queda marcada aunque venga `?comparar=normal`. — `TestCompareSetup` (caso sin %) y `TestProgramsPage`.
- [ ] CA2: con % y un modo con datos, el gráfico trae banda (o línea) como hoy. — `TestCompareSetup` existente sigue verde.
- [ ] CA3: el eje del gráfico de un tramo al aire cubre al menos su largo esperado: la mediana de duración de las emisiones anteriores del programa, o 1 h si no hay; nunca menos de lo transcurrido. Un tramo de 1 franja con 1 h esperada tiene `W` = 12 y la barra ocupa 1/12. Un tramo cerrado usa su largo real (sin cambios). — `TestProgramParticipationAxis` (nuevo).
- [ ] CA4: con Claude activo (`claudeActive`) y etiquetas en el tramo, Preguntas muestra los grupos de las tandas (texto y cantidad, como En vivo, sin "Marcar respondida") con la marca Claude; con resumen final y sin grupos, las preguntas del resumen; reglas solo con Claude apagado o sin etiquetas, con el chip "por reglas". — `TestProgramDetailClaudeQuestions` (nuevo).
- [ ] CA5: con Claude activo y etiquetas, la tarjeta de la izquierda es "Categorías" con `catRowsWith` (Claude) y los mensajes sin etiquetar contados aparte; con resumen final, los temas del resumen como hoy; sin Claude, temas por reglas. — `TestProgramDetailClaudeCats` (nuevo).
- [ ] CA6: la columna Preguntas de la lista cuenta mensajes con `Cat == pregunta` de las etiquetas cuando Claude está activo y hay etiquetas del tramo; si no, por reglas, y el `title`/nota dice cuál. — `TestProgramRows` (caso nuevo).
- [ ] CA7: navegador con la vista previa: Programas en claro, oscuro y ancho de celular.
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

- `compareSetup` recibe si hay % (`ok` de la participación): sin %, todos los modos menos "Ninguna" con `Off` y un solo motivo ("hace falta el dato de cuántos miraban"). Para eso `programParticipation` calcula las franjas antes de armar los botones (se separa el cálculo de las franjas del armado del gráfico).
- Eje: `programParticipation` pasa a `ParticipationRange` un `to` = `max(now, start + esperado)` solo para el **eje**: se agregan franjas vacías al final hasta el largo esperado (sin barra, sin rótulo de "ahora" adelante). Largo esperado: mediana de `End-Start` de las emisiones anteriores (`ems`, sin la de hoy), mínimo 1 h. Lo más nuevo sigue a la izquierda: las franjas vacías futuras quedan a la izquierda de la actual y los ticks marcan el fin esperado.
- `ProgramDetail` suma `QG []QGroupRow`, `QByClaude bool`, `Cats []CatRow`, `CatsPending int`. `programDetail` pide `s.st.Labels(messageIDs(msgs))` una vez (ya lo hace `programSentiment`: se reutiliza) y los grupos con el mismo camino que `questionsData`.
- `programRows`: con Claude activo pide `Labels` de los mensajes de chat de los tramos mostrados (un solo pedido) y cuenta `Cat == CatPregunta`; un tramo sin ninguna etiqueta cuenta por reglas.
- Plantilla `programas.html`: tarjetas con `ia`/chip según de dónde salió.

## Fuera de alcance

- Pedirle temas a Claude en las tandas (más gasto; el usuario eligió Categorías).
- Cambios en En vivo, en el CSV (la categoría del CSV sigue siendo la de reglas) o en `Classify`.
- Marcar preguntas respondidas desde Programas.

## Riesgos

- **Costo de Claude / cuota:** ninguno; solo se lee lo ya guardado.
- **Rendimiento:** con "7 días", `Labels` de todos los mensajes de chat mostrados es un mapa grande por refresco (60 s solo con "Hoy"). Se pide una vez por request con el índice por ID del `Store`; se mide en un test con muchos mensajes si hace falta.
- **Concurrencia:** solo lecturas con copias (`Labels`, `Groups`), como En vivo.
