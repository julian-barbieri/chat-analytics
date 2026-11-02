# Spec: quitar el aviso de contador oculto de la tarjeta de momentos

- **Estado:** implementada
- **Tipo:** mejora
- **Rama:** `chore/quitar-aviso-contador-oculto`
- **Origen:** pedido del usuario: "elimina el comentario que dice «YouTube no informa cuántos miran este vivo (el canal oculta el contador): sin ese dato no se puede calcular el % de participación.»"
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-02 (pedido directo)

## Contexto

La tarjeta "Momentos con más participación" mostraba ese aviso (`div.note`) cuando el
vivo no tiene `Concurrent`. Repite lo que ya dice la línea "Sin dato de cuántos miraban:
el gráfico muestra personas que escribieron y no hay ranking por %".

Además, `main` no compilaba los tests desde el merge de `feat/panel-oyentes` (bc9e6d9):

```bash
go vet ./...
```

```
vet: ./monitor_test.go:2346:10: undefined: section5
```

**Causa raíz:** al resolver el merge quedaron el título de la tarjeta en "6." y el helper
de los tests como `section6`, mientras que `feat/quitar-secciones-dashboard` (5176d47) ya
los había pasado a "5." y `section5`, y otros tests llaman a `section5`.

## Objetivo

Que la tarjeta no muestre el aviso y que `main` vuelva a compilar y pasar los tests, con
la tarjeta numerada "5." como el resto del análisis.

## Criterios de aceptación

- [x] CA1: `GET /parcial/vivo/analisis` de un vivo sin espectadores no tiene "YouTube no
  informa cuántos miran este vivo" — `TestLiveParticipationView` (caso v2 lo pide ausente).
- [x] CA2: la tarjeta se titula "5. Momentos con más participación" y el helper de los
  tests es `section5` — `go vet ./...` limpio y `TestLiveParticipationView`,
  `TestParticipationViewersView` en verde.
- [x] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

- `templates/partials.html`: borrar la línea del `div.note` y volver el título a "5.".
- `monitor_test.go`: mover el texto del aviso a los que no deben aparecer en v2 y
  renombrar `section6` a `section5`.

## Fuera de alcance

- El README y las specs anteriores: el README ya describe el caso sin dato como "el
  gráfico muestra personas y no hay ranking", que sigue siendo cierto.

## Riesgos

- Ninguno de cuota, datos ni concurrencia: solo cambia un texto del template.
