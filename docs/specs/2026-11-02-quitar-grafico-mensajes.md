# Spec: quitar el gráfico de mensajes cada 5 minutos

- **Estado:** implementada
- **Tipo:** mejora
- **Rama:** `feat/quitar-grafico-mensajes`
- **Origen:** pedido del usuario: "Mensajes cada 5 minutos (últimas 3 horas)... quiero que me elimines esta sección y su respectivo gráfico"
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-02 (pedido directo)

## Contexto

El resumen del dashboard (`GET /parcial/resumen`) mostraba, al lado de "Categorías de
hoy", un gráfico de barras con los mensajes de cada 5 minutos de las últimas 3 horas
(`activity` en `server.go`, bloque en `templates/partials.html`). El gráfico de
participación del vivo elegido ya muestra la actividad por franja de 5 minutos, así que
el usuario no lo necesita.

## Objetivo

Que el resumen no muestre más esa sección ni su gráfico, sin dejar código ni CSS sin uso.

## Criterios de aceptación

- [x] CA1: `/parcial/resumen` no contiene "Mensajes cada 5 minutos" y sigue mostrando
  "Categorías de hoy" — se verifica con `TestSummaryWithoutActivityChart` (falla con el
  template anterior).
- [x] CA2: no queda la función `activity` ni la regla CSS `svg text`, que solo usaba ese
  gráfico — se verifica con `go vet ./...`, `staticcheck` y `grep -n "activity(\|svg text" server.go templates/page.html` sin resultados.
- [x] CA3: README y AGENTS.md no mencionan más el gráfico — `grep -n "mensajes cada 5 minutos" README.md` y `grep -n "gráficos de actividad" AGENTS.md` sin resultados.
- [x] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

- `templates/partials.html`: borrar la sección del gráfico. "Categorías de hoy" queda
  como tarjeta sola a ancho completo, fuera de `.grid2` (si no, ocuparía 2/3 con un
  hueco al costado).
- `templates/page.html`: borrar `svg text` (no queda ningún `<text>` en los templates) y
  sumar `.mb` para conservar el margen de abajo que daba `.grid2`. `.bar` y `.hit` quedan:
  los usa el gráfico de participación.
- `server.go`: borrar `activity` y la clave `Activity` del resumen. `Bar` queda (la usa
  `participationChart`).

## Fuera de alcance

- El gráfico de participación del vivo y cualquier otro bloque del resumen.

## Riesgos

- Ninguno de cuota, datos ni concurrencia: es solo presentación y no cambia lo que se
  guarda ni lo que se consulta a YouTube.
