# Spec: Rankings con barras, segmentos en cinta y preguntas dentro de Categorías

- **Estado:** aprobada
- **Tipo:** mejora
- **Rama:** `feat/rankings-segmentos-preguntas`
- **Origen:** pedido del usuario con capturas (2026-11-12), puntos 2, 3 y 5; maquetas
  en https://claude.ai/artifact/EgkYGxJybkC2KWBcTGv4Vd (opciones recomendadas).
  Tanda 2 de 3.
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-12

## Contexto

- **Rankings (2):** en Comentarios, "De qué hablan" son 16 chips con un número y
  "Quiénes más comentan" es una tabla Persona / Comentarios / Videos
  (`templates/comentarios.html:72-80`, datos en `comments.go:430-441`, `AuthorRow` en
  `comments.go:244`). Los chips no muestran la diferencia entre 42 y 21, y la columna
  Videos esconde si alguien es fiel (comenta en muchos videos) o discute en un solo
  hilo. En En vivo, "Quiénes más escriben" ya tiene barras (`partials.html:221`), y
  no hay otras tablas de este tipo.
- **Segmentos (3):** "Segmentos por asistencia" de Programas son dos barras al 100 %
  (Personas y Mensajes, `segmentBars` en `loyalty.go:739`, plantilla `segbars` en
  `programas.html:168`). Lo importante, que pocos escriben casi todo, cuesta verlo.
- **Preguntas (5):** en el detalle de Programas, "Categorías" y "Preguntas" son dos
  tarjetas (`programas.html:107-135`), aunque las preguntas ya son un sector del
  semicírculo (`programGauge`, `programs.go:775`; detalle en `catDetail`,
  `catgauge.go:147`).

## Objetivo

Que los rankings de Comentarios se lean de un vistazo, que Segmentos muestre el salto
de personas a mensajes, y que en Programas las preguntas agrupadas vivan dentro de
Categorías.

## Criterios de aceptación

- [ ] CA1: "De qué hablan" de Comentarios muestra las 10 palabras más repetidas como
  barras (`.wlist` / `.wrow` / `.wbar`, las mismas de En vivo), cada una con su
  número y la barra relativa a la primera. — `TestCommentsSummary` y test de render.
- [ ] CA2: "Quiénes más comentan" pasa a ser su propia tarjeta al lado de "De qué
  hablan" (`grid2`): un ranking de 8 con barras cuyo largo es el total de
  comentarios, la parte fuerte son los videos distintos y la suave el resto; inicial
  en un círculo con color estable por nombre (de `--series-1..5`); `<title>` y
  `aria-label` con "N comentarios en M videos"; leyenda de las dos partes. — test de
  la función que arma las filas (tabla: 9 en 9, 5 en 1, 0 videos) y de render.
- [ ] CA3: en Programas, "Segmentos por asistencia" reemplaza las dos barras por una
  cinta en SVG armada en Go: a la izquierda la columna Personas, a la derecha
  Mensajes, y una cinta por segmento que une su tramo en una con su tramo en la otra,
  con los % rotulados y `<title>` por tramo. Segmento vacío sin tramo; sin
  participantes, sin gráfico. La tabla de abajo queda igual. — `TestSegmentBars`
  adaptado (o `TestSegmentFlow`), con un caso de segmento vacío.
- [ ] CA4: en el detalle de Programas no está más la tarjeta "Preguntas"; la de
  Categorías se llama "Categorías y preguntas" y, con el sector **Pregunta** elegido,
  su detalle muestra los grupos de preguntas de las tandas (los `qgTop` primeros y
  "Ver N más" plegado en `maspreguntas`, como hoy) en lugar de "Últimos mensajes".
  — test de `programDetail` con etiquetas.
- [ ] CA5: sin grupos de las tandas, el sector Pregunta muestra las del resumen de
  Claude (`prog-items`) y, si tampoco hay, las preguntas por reglas como hoy. — test
  con los tres casos.
- [ ] CA6: en Programas, sin una categoría elegida en `abrir`, el semicírculo elige
  **Pregunta** si hay preguntas (si no, la más grande sin Otros, como hoy). En vivo y
  Comentarios no cambian. — test de `programGauge`.
- [ ] CA7: `TestTemplateTokens` en verde y vista previa de Comentarios y Programas en
  claro, oscuro y celular.
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

- `comments.go`: `Words` con `TopWords(period, 10)` y su ancho; `AuthorRow` suma
  `W`, `WVideos` (anchos en %) e `Initial`/`Hue`; función pura `authorHue(name)`.
  Plantilla con dos `section.card` en `grid2`.
- `loyalty.go`: `segmentFlow(segs)` devuelve columnas y cintas (paths con curvas)
  para un SVG de `viewBox` fijo; reemplaza `segmentBars` y la plantilla `segbars`.
  Colores con las clases de los segmentos que ya existen (`.sw nucleo`, etc.).
- `programs.go` / `catgauge.go`: `programGauge` pone el defecto en Pregunta; la
  plantilla de Programas, con el sector Pregunta elegido, dibuja dentro del detalle
  los `QGroups` / `QRest` que ya arma `programFoldLists` (`prog-qg`) en vez de los
  últimos mensajes. `cgauge` gana un bloque opcional para eso, así En vivo y
  Comentarios no cambian. Se borra la tarjeta Preguntas de `programas.html`.

## Fuera de alcance

- Marcar preguntas como respondidas en Programas (no existe hoy).
- Cambios en En vivo ("Preguntas para producción" sigue aparte) y en el CSV.
- Lo de la tanda 1 (menú, likes, "De qué se habla") y tanda 3.

## Riesgos

- **Conflictos con la tanda 1:** las dos tocan `partials.html` y el CSS de
  `layout.html`; la que entre segunda se rebasa sobre `main`.
- **Nombres de usuarios:** la inicial y el color salen del nombre visible que ya se
  muestra; nada nuevo se guarda ni se manda afuera.
- Sin cambios de cuota, de `datos.jsonl` ni de concurrencia.
