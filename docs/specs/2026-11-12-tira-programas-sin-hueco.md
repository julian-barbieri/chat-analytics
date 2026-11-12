# Spec: la tira "Hoy en el canal" sin huecos falsos entre programas del mismo vivo

- **Estado:** aprobada
- **Tipo:** bug
- **Rama:** `fix/tira-programas-sin-hueco`
- **Origen:** mensaje del usuario (captura de En vivo: "EL PASE ENTR… 11:59 a 12:32 terminó" y "CRISTINA SIN… 12:35 a ahora al aire")
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-12

## Contexto

En la tira "Hoy en el canal" de En vivo, un programa termina en su última
muestra y el siguiente empieza en su primera. Las muestras son cada ~3 min, así
que entre dos programas del **mismo vivo** queda siempre un hueco de hasta 3 min
que parece un corte. Caso real del 12/11: vivo `mgCIneeiU74` de Radio
Rivadavia, muestras a las 12:32 ("EL PASE ENTRE GONZALO BAÑEZ y CRISTINA PÉREZ")
y 12:35 ("CRISTINA SIN VUELTAS"), con chat a las 12:33 y 12:34: no hubo corte.

**Causa raíz:** `dayStrip` (`server.go:1810-1817`) usa `sg.End` (la última
muestra del tramo, `analyze.go:526`) para el texto y el ancho del bloque.

## Objetivo

En la tira, un programa que terminó y al que le sigue otro programa del mismo
vivo sin hueco real termina donde empieza el siguiente ("11:59 a 12:35").

## Criterios de aceptación

- [ ] CA1: tramo terminado del vivo `a` hasta 12:32 y siguiente tramo del vivo
  `a` desde 12:35 → el bloque anterior dice "… a 12:35" y su ancho llega hasta
  12:35 — `TestDayStripSameLiveNoGap`.
- [ ] CA2: si entre los dos tramos del mismo vivo pasó más de `segmentGap`
  (7 min), o lo que sigue es otro vivo o un "sin vivo", el bloque termina en
  su última muestra como hoy — `TestDayStripSameLiveNoGap` (casos de tabla).
- [ ] CA3: el link del bloque a Programas (`tramo=`) sigue siendo el mismo —
  `TestDayStripSameLiveNoGap` comprueba `Href`.
- [ ] CA4: `ProgramSegments`, `segmentKey`, Programas y Sin vivo no cambian —
  `TestProgramSegments` y los tests de Sin vivo siguen en verde sin tocarlos.
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde; vista previa
  de En vivo en claro, oscuro y celular.

## Diseño propuesto

Solo en `dayStrip`: para un tramo `SegEnded`, buscar el siguiente tramo (j > i)
del mismo `VideoID`; si existe, no es `SegNone` y empieza a ≤ `segmentGap` de
`sg.End`, usar su `Start` como fin visible (texto y `W`). La búsqueda de la
clave (`keys`) sigue usando `sg.End` original. Un helper chico
(`stripEnd(segs, i)`) para que `dayStrip` no crezca.

Alternativa descartada: cambiar `End` en `ProgramSegments`. Lo usan los
mensajes de cada programa, el analista y Programas; movería mensajes de un
programa a otro y cambiaría lo que ya se analizó.

## Fuera de alcance

- Las horas de la lista de Programas y del detalle (siguen con la última muestra).
- Sin vivo (`nolive.go`): ya trata ese rato como al aire.
- El cartel de continuidad (`ContinuityBanner`).

## Riesgos

- Cuota, `datos.jsonl`, concurrencia, privacidad: ninguno (solo cambia cómo se
  dibuja un dato que ya está en memoria).
- Dos vivos a la vez del mismo canal: se busca el siguiente tramo del **mismo**
  vivo, no el siguiente de la lista; lo cubre un caso de CA2.
