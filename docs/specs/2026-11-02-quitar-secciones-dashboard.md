# Spec: quitar cuatro secciones del dashboard

- **Estado:** implementada
- **Tipo:** mejora
- **Rama:** `feat/quitar-secciones-dashboard`
- **Origen:** pedido del usuario: "elimina las secciones «Chat por programa (24 h)», «Últimos mensajes», «4. Spam e insultos repetidos» y «Videos recientes»"
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-02 (pedido directo)

## Contexto

El dashboard mostraba, debajo del vivo elegido, el feed "Últimos mensajes" (con filtros
de fuente, categoría y buscador, `GET /parcial/feed`) y una columna (`GET /parcial/extra`)
con palabras más repetidas, chat por programa, quiénes más escriben y videos recientes.
En el análisis del vivo, la tarjeta 4 listaba a quienes repetían spam o insultos
(`Analysis.Offenders`). El usuario no las usa.

## Objetivo

Que el dashboard no muestre más esas cuatro secciones, sin dejar código, rutas ni CSS
sin uso, y que las tarjetas del análisis queden numeradas sin saltos.

## Criterios de aceptación

- [x] CA1: `GET /` no tiene "Últimos mensajes" ni el formulario de filtros, y
  `GET /parcial/feed` da 404 — `TestDashboardWithoutRemovedSections`.
- [x] CA2: `GET /parcial/extra` no tiene "Chat por programa" ni "Videos recientes" y
  conserva "Palabras más repetidas" y "Quiénes más escriben" — mismo test.
- [x] CA3: `GET /parcial/vivo/analisis` no tiene "Spam e insultos repetidos"; los filtros
  pasan a ser la tarjeta 4 y los momentos, la 5 — mismo test.
- [x] CA4: no quedan `Offender`, `Analysis.Offenders`, el handler `feed`, `catOptions`
  ni las reglas CSS `.filters`, `select, input[type=search]`, `.scroll` y `.fill.crit` —
  `go vet`, `staticcheck` y `grep -n "Offender\|catOptions\|parcial/feed\|\.filters\|\.scroll\|fill crit\|fill.crit" *.go templates/*` sin resultados.
- [x] CA5: README y AGENTS.md describen el dashboard sin esas secciones.
- [x] CA6: verificado en el navegador con la vista previa (datos locales): modo claro y
  oscuro a 1.163 px y celular a 390 px, sin desbordes ni huecos en la grilla.
- [x] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

- `templates/page.html`: sacar el feed y su formulario. `#extra` pasa a ser la grilla
  (`.grid2`, ahora `1fr 1fr`, una columna debajo de 600 px) con sus dos tarjetas.
- `templates/partials.html`: borrar los bloques `feed`, "Chat por programa", "Videos
  recientes" y la tarjeta 4 del análisis; renumerar filtros (4) y momentos (5).
- `server.go`: borrar la ruta y el handler `feed`, `catOptions` y las claves que ya no
  usa ningún template (`Cats`, `Programs`, `Videos`, `Channel` de `extra`, `Offenders`).
  `FeedRow` queda como `ChatRow` (sin `Video`) porque lo usa el chat en vivo.
- `analyze.go`: borrar `Offender` y su conteo. Filtros, candidatos y links siguen igual.
- `TopPrograms` se queda: lo usa el informe Markdown (`report`).
- `.vgrid` pasa de columnas de 300 px a 400 px: con 3 columnas la tarjeta de filtros
  quedaba sola con dos huecos; con 400 px y el ancho máximo de `main` entran 2 por fila
  y las 4 tarjetas quedan en 2x2 (una columna en celular).

## Fuera de alcance

- El informe Markdown y el CSV (siguen teniendo chat por programa y el resto).
- El clasificador y las listas de spam e insultos.

## Riesgos

- Ninguno de cuota, datos ni concurrencia: es solo presentación; no cambia lo que se
  consulta a YouTube ni lo que se guarda.
