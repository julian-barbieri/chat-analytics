# Spec: pestaña Recomendaciones en Programas (próximamente)

- **Estado:** implementada
- **Tipo:** mejora
- **Rama:** `feat/pestana-recomendaciones`
- **Origen:** pedido del usuario: "Dentro de programas quiero una solapa llamada
  Recomendaciones + el logo de un robot (recomendaciones con IA). Por ahora como futura
  implementación, pero quiero tener ya la solapa."
- **Aprobada por / fecha:** Julian Barbieri (pedido directo), 2026-11-13

## Contexto

El detalle de un programa en Programas tiene cinco pestañas (`programTabs` en
`programs.go`): Resumen, Conversación, Audiencia y fidelización, Chat del programa y
Super Chats. La idea es sumar recomendaciones hechas con IA para cada programa; todavía
no está definido qué recomiendan ni con qué datos, pero se quiere ver ya el lugar.

## Objetivo

Que el detalle de Programas tenga una sexta pestaña, "Recomendaciones", con un ícono de
robot que indica que es con IA, y que al abrirla diga que es una función que viene
pronto. No se manda nada a Claude ni se calcula nada nuevo.

## Criterios de aceptación

- [x] CA1: `programTabs` termina en `{"recomendaciones", "Recomendaciones"}`, a la
  derecha de Super Chats, y la pestaña lleva el ícono de robot (SVG con
  `aria-hidden="true"`, colores por tokens con `currentColor`) — `TestProgramRecsTab`.
- [x] CA2: `?pestana=recomendaciones` abre la pestaña (`aria-current="page"`) y muestra
  la tarjeta `id="recomendaciones"` con el cartel "Próximamente" y el texto de qué va a
  haber; no muestra el chat del programa ni otra pestaña — `TestProgramRecsTab`.
- [x] CA3: la pestaña viaja en la URL como las otras (`programsQuery`), y un valor que
  no está en la lista sigue sin reflejarse — `TestProgramRecsTab` y los tests de
  pestañas existentes.
- [x] CA4: el ícono no aparece en las pestañas de En vivo (comparten la plantilla
  `tabs`) — `TestProgramRecsTab`.
- [x] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

- `programs.go`: `programTabs` pasa a `[]struct{ Key, Label, Icon string }` y suma
  `{"recomendaciones", "Recomendaciones", "robot"}` al final; `TabLink` gana `Icon`.
- `templates/partials.html`: la plantilla `tabs` dibuja el robot si `Icon` es `robot`
  (SVG inline, clase `ico`, sin colores fijos).
- `templates/programas.html`: rama `{{else if eq .Tab "recomendaciones"}}` con una
  tarjeta "Recomendaciones con IA" y la etiqueta "Próximamente".
- `TestProgramSupersTab` se ajusta: Super Chats deja de ser la última y pasa a ser la
  anteúltima (sigue a la derecha de Chat del programa).

## Fuera de alcance

- Generar recomendaciones (prompt, modelo, costo, datos que se mandan a Claude): va en
  una spec propia cuando se defina.
- Pestaña equivalente en En vivo.

## Riesgos

- Ninguno de cuota ni de privacidad: no hay llamados nuevos a YouTube ni a Claude.
- Compatibilidad: un link viejo sin `pestana` o con otra pestaña sigue igual.
