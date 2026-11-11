# Spec: largo esperado de un programa al aire con pocas emisiones

- **Estado:** borrador
- **Tipo:** bug
- **Rama:** `fix/programas-largo-esperado`
- **Origen:** captura del usuario en Programas (2026-11-11): "COBERTURA ESPECIAL DE RADIO RIVADAVIA" al aire con el gráfico apretado a la derecha y el eje hasta las 19:40.
- **Aprobada por / fecha:** —

## Contexto

Mientras un programa está al aire, Programas reserva a la izquierda del gráfico de
participación franjas vacías hasta su largo esperado (`programs.go:940-945`). Ese largo
es la mediana de las emisiones anteriores (`expectedLength`, `programs.go:955`), aunque
haya una sola. "COBERTURA ESPECIAL…" tiene una sola emisión previa en `datos.vivos.jsonl`
(08/11 17:14 → 09/11 02:33, ~9 h 20 min). Hoy arrancó a las 10:15, así que el eje llega
a las 19:40: a las 11:45 las barras ocupan ~1/6 del ancho y el resto es hueco. Con
programas especiales o nuevos, una sola emisión rara define todo el eje.

**Causa raíz:** `expectedLength` (`programs.go:972-976`) usa la mediana con cualquier
cantidad de emisiones, desde 1. "Lo normal (4 semanas)" ya pide `minEmissions` (3,
`loyalty.go:362`) para no comparar contra una sola emisión.

## Objetivo

Que el gráfico de un programa al aire solo reserve espacio hasta un fin esperado si hay
al menos `minEmissions` emisiones anteriores; con menos, se usa el mínimo de 1 h
(`minProgramAxis`), como un programa sin historia.

## Criterios de aceptación

- [ ] CA1: con 1 o 2 emisiones anteriores (de cualquier largo), `expectedLength` devuelve
  `minProgramAxis` — `TestExpectedLength` (nuevo, tabla: 0, 1, 2 y 3 emisiones; con 3,
  la mediana como hoy).
- [ ] CA2: en el detalle de un programa al aire hace 2 h con una sola emisión anterior de
  9 h, el gráfico no tiene franjas vacías (`viewBox="0 0 25 100"`, sin rótulo del fin
  esperado) — caso nuevo en `TestProgramParticipationAxis`.
- [ ] CA3: los casos actuales de `TestProgramParticipationAxis` (3 días de historia)
  siguen pasando sin cambios.
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

En `expectedLength`: `if len(spans) < minEmissions { return minProgramAxis }` en lugar de
`len(spans) == 0`. Actualizar el comentario de la función. Tests primero: la tabla de
`TestExpectedLength` y el caso "al aire con una sola emisión" en
`TestProgramParticipationAxis`, que fallan con el código de hoy.

Alternativa descartada: ponerle un techo fijo (por ejemplo, 4 h) al largo esperado, que
cortaría también a los programas largos de verdad que salen todos los días.

## Fuera de alcance

- Separar días hábiles de fines de semana en el largo esperado (lo hace "Lo normal",
  pero acá no hace falta).
- Cambiar la dirección del gráfico (lo más nuevo sigue a la izquierda) o el ancho fijo
  por franja.
- En vivo, que no reserva espacio.

## Riesgos

- Ninguno de cuota, datos, concurrencia ni privacidad: es una función pura del dashboard,
  sin llamadas a la API. Cambia lo que se ve en Programas: se verifica con la vista previa.
