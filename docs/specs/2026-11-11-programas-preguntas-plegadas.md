# Spec: Preguntas de Programas con "Ver N más"

- **Estado:** aprobada
- **Tipo:** mejora
- **Rama:** `feat/programas-preguntas-plegadas`
- **Origen:** pedido del usuario: "en Programas, en Preguntas, hay veces que se ven muchas preguntas y queda muy larga la sección".
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-11

## Contexto

En el detalle de Programas, con tandas de Claude, la tarjeta Preguntas lista **todos**
los grupos de preguntas del tramo (`programClaudeCards`, `programs.go:770-775`; plantilla
`templates/programas.html:116`). Un programa de varias horas arma decenas de grupos, muchos
de un solo mensaje, y la tarjeta tapa el resto del detalle.

En vivo ya lo resolvió (`questionsData`, `server.go:1465-1500`; `templates/partials.html:342-347`):
los `qgTop` (5) primeros grupos a la vista y el resto en un `<details>` "Ver N más"
(sección `maspreguntas` de `abrir`, que sigue abierta con el refresco).

Las otras dos listas de Preguntas de Programas ya tienen tope: por reglas, 8
(`analyze.go:88`); las del resumen de Claude, 8 (`cqFolds`).

## Objetivo

Que la tarjeta Preguntas de Programas muestre como mucho 5 grupos de las tandas y el resto
plegado en "Ver N más", con el estado abierto conservado como en En vivo.

## Criterios de aceptación

- [ ] CA1: con más de 5 grupos, se ven los 5 primeros (mismo orden de `questionGroups`)
  fuera del plegado y el resto dentro de un `<details>` cerrado con "Ver N más" (N = grupos
  restantes) — `TestProgramQuestionsMore`.
- [ ] CA2: con `abrir=maspreguntas` el `<details>` sale `open` y su `<summary>` tiene
  `id="pf-maspreguntas"` y un `hx-get` que lo cierra (sin `maspreguntas` en `abrir`) —
  `TestProgramQuestionsMore`.
- [ ] CA3: los grupos plegados de 2 o más mensajes siguen desplegándose (`qg6`…`qg30`,
  hoy en `programFoldLists`); un `qgN` abierto dentro del plegado se ve con sus mensajes
  si además está `maspreguntas` — `TestProgramQuestionsMore`.
- [ ] CA4: con 5 grupos o menos no aparece "Ver" ni el `<details>` extra; los tests
  existentes (`TestProgramDetailClaudeQuestions`, `TestProgramFolds`) siguen en verde.
- [ ] CA5: `abrir=maspreguntas` está en la lista cerrada `programFolds`; un valor que no
  está no se refleja (ya cubierto por `readDetail`, se suma al test).
- [ ] CA6: vista previa: Programas con un tramo de muchas preguntas, claro/oscuro y ancho
  de celular; abrir "Ver N más", esperar el refresco de 60 s (con Hoy) y que siga abierto.
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

- `programchat.go`: sumar `"maspreguntas"` a `programFolds`.
- `programs.go`: en `ProgramDetail`, `QGroups` pasa a ser los primeros `qgTop` y un nuevo
  `QRest []QGRow` el resto, con `QMore Fold` (`q.fold("maspreguntas")`). El corte se hace en
  `programFoldLists` después de ponerle `Key`/`Fold` por índice global (así `qg6` sigue
  siendo el sexto), o se recorre la concatenación. Reusa `qgTop` de `server.go`.
- `templates/programas.html`: sacar el `<li>` de un grupo a un `define` (`prog-qg`) y usarlo
  en las dos listas; debajo, `{{if .QRest}}<details class="plegable"…>` con
  `{{template "pf-summary"}}`-equivalente para `maspreguntas` ("Ver N más").
- Test primero: `TestProgramQuestionsMore` con un tramo de 7 grupos en `claudeProgramServer`
  (o etiquetas armadas en el test), cerrado y con `abrir=maspreguntas,qg6`.

## Fuera de alcance

- Lista por reglas y preguntas del resumen de Claude (ya cortadas en 8).
- En vivo, el orden de los grupos y marcar respondidas.

## Riesgos

- Ninguno de cuota, datos ni concurrencia: solo arma la vista con lo que el detalle ya leyó.
- `abrir` sigue siendo lista cerrada: no se refleja nada fuera de `programFolds`.
