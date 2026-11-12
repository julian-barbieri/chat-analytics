# Spec: Menú lateral, selector de likes y "De qué se habla" unificado

- **Estado:** implementada
- **Tipo:** mejora
- **Rama:** `feat/menu-likes-temas`
- **Origen:** pedido del usuario con capturas (2026-11-12), puntos 1, 7, 8, 9 y 10;
  maquetas en https://claude.ai/artifact/EgkYGxJybkC2KWBcTGv4Vd (opciones recomendadas).
  Tanda 1 de 3.
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-12

## Contexto

- **Menú (10):** las cinco pantallas se eligen con pestañas horizontales en el
  encabezado (`templates/layout.html:582`, CSS `.tabs` en `layout.html:126`).
- **Likes (1):** "Comentarios y likes por día" dibuja las barras de comentarios y la
  línea de likes en el mismo SVG, cada una con su escala (`commentspage.go:237`,
  `likesSteps` en `commentspage.go:246`; plantilla `comentarios.html:49`). Son dos
  ejes en uno: la altura de la línea contra las barras no significa nada, y no se
  puede sacar la línea.
- **De qué se habla (7, 8, 9):** En vivo muestra solo palabras sueltas
  (`TopWords`, `server.go:1039`; `monitorView`, `livecards.go:338`; plantilla
  `partials.html:209`). Programas muestra "Temas de conversación": los de Claude del
  resumen final o, si no hay, `TopTopics` por reglas (`programas.html:96-105`,
  `programs.go:742`), pero no las palabras. Son la misma pregunta con dos respuestas
  distintas.

## Objetivo

Navegar desde una columna a la izquierda; en Comentarios elegir qué serie ver; y que
En vivo y Programas tengan la misma tarjeta "De qué se habla": temas arriba y
palabras más repetidas abajo.

## Criterios de aceptación

- [x] CA1: las cinco pantallas tienen la navegación en una columna a la izquierda
  (`<header class="side">` con `<nav class="nav" aria-label="Secciones">`) con los cinco links, ícono SVG
  `aria-hidden` y texto, la actual con `aria-current="page"`; el logo, el título y
  "Descargar CSV para Claude" van en la columna. — `TestDashboardTabs` adaptado.
- [x] CA2: la franja de estado (`#estado`, refresco de 5 s) sigue en todas las
  pantallas, al pie de la columna. — `TestDashboardTabs`.
- [ ] CA3: debajo de 720 px la columna pasa a una barra fija abajo (5 ítems de al
  menos 44 px, respetando `env(safe-area-inset-bottom)`), y el contenido no queda
  tapado. Sin JS propio. — vista previa en ancho de celular.
- [x] CA4: "Comentarios y likes por día" tiene un selector "Ver" con **Comentarios**
  (por defecto), **Likes** y **Los dos**, como links `cfiltro` con `ver=` en la URL
  (lista cerrada en `CommentsQuery`; un valor fuera de la lista cae al defecto y no
  se refleja). — `TestCommentsQuery` (casos de `ver`).
- [x] CA5: con `ver=comentarios` el SVG no tiene la línea; con `ver=likes` las barras
  son los likes nuevos por día con su propia escala y su `<title>`; con `ver=dos`
  queda como hoy. La leyenda, el `aria-label` y el texto "cada serie con su escala"
  (solo en "Los dos") siguen a lo elegido. Sin likes guardados, el selector no
  aparece. — test de `dayChart` / `commentsData` con tabla de los tres valores.
- [x] CA6: En vivo y Programas muestran la misma tarjeta "De qué se habla" (una
  plantilla compartida): **Temas** arriba y **Palabras más repetidas** abajo, con ▲
  en las palabras en alza (`risingWords`, como hoy). — test de render en las dos
  pantallas.
- [x] CA7: los temas son los de Claude cuando hay un análisis del tramo (en
  Programas, el final de `finalAnalysis`; en En vivo con "Programa en curso", el
  último análisis de ese programa, parcial del botón o final, con su hora) y, si no,
  `TopTopics` por reglas con el chip "por reglas" (solo sus pares de palabras: una
  palabra sola ya está en la lista de abajo). Los temas de Claude se siguen
  desplegando con sus mensajes y en En vivo salen de la tarjeta de Claude (no se
  ven dos veces). Con "Todo el día del canal", por reglas. — test con y sin
  análisis en cada pantalla.
- [x] CA8: Programas suma las palabras más repetidas del tramo (`TopWords`,
  `wordsShown`); "Quiénes participan" de En vivo no cambia. — test de
  `programDetail`.
- [ ] CA9: `TestTemplateTokens` en verde (colores nuevos en los dos temas) y vista
  previa de las cinco pantallas en claro, oscuro y celular.
- [x] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

- `layout.html`: `<body>` con grilla `nav.side` + `main`; íconos SVG inline (trazo
  con `currentColor`); `@media (max-width: 720px)` pasa la columna a barra inferior
  y agrega `padding-bottom` a `main`. Se va `.tabs`.
- `comments.go`: `Ver` en `CommentsQuery` con `commentViews` (lista cerrada) y en
  `values()`; `commentspage.go`: `dayChart(days, ver)` arma barras de comentarios o
  de likes con `barChart` y la línea solo en "dos"; `Choice` para los botones.
- Tarjeta compartida `talk` en `partials.html` que reciben las dos pantallas; una
  función pura `talkView(topics, words, msgs, analysis, now)` en `livecards.go`
  reemplaza la parte de palabras de `monitorView`. En vivo lee `Store.Analyses`
  (copia) del canal para el tramo en curso, como ya hace `claudeInput`.
- No se agregan pedidos a Claude: los temas salen de análisis que ya existen.

Ajustes al implementar: la columna es un `<header>` (no un `<aside>`: es el
encabezado del sitio); por reglas, sacar de la lista de palabras las que ya eran un
tema dejaba afuera justo las más repetidas, así que los temas por reglas son solo los
pares de palabras; y los temas de Claude se mueven de su tarjeta a "De qué se habla"
para no mostrarlos dos veces.

## Fuera de alcance

- Temas de Claude **mientras** el programa está al aire sin apretar "Analizar ahora"
  (haría falta sumar temas a las tandas, ADR 0009): queda para otra tarea.
- La tarjeta de Claude de En vivo sigue con su resumen, preguntas y quejas (los
  temas pasan a "De qué se habla").
- Rankings con barras de Comentarios (tanda 2) y unificar programa/día (tanda 3).

## Riesgos

- **Layout:** el cambio de encabezado toca las cinco pantallas; el ancho útil baja
  ~200 px en escritorio. Gráficos con ancho fijo (`pfijo` de Programas) se revisan en
  la vista previa.
- **Links viejos:** `ver=` nuevo con defecto; URLs guardadas siguen andando.
- Sin cambios de cuota, de `datos.jsonl` ni de concurrencia (solo lecturas con copia).
