# Spec: Programas: preguntas sin etiquetar, franjas futuras y largo esperado

- **Estado:** implementada
- **Tipo:** bug
- **Rama:** `fix/programas-revision-preguntas-eje`
- **Origen:** revisión del PR #38 (`factory/manual/programas-comparar-claude/revision-1.md`, hallazgos importantes 1 a 3); la persona pidió arreglar los tres.
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-11

## Contexto

1. **Preguntas de la lista:** en un tramo con alguna etiqueta, `programRows` cuenta solo las preguntas etiquetadas por Claude (`programs.go:231-235`). Los mensajes sin etiquetar (por ejemplo, de antes de reiniciar el monitor o después de que las tandas llegan al tope) no cuentan, aunque las reglas los marquen como pregunta. Categorías sí los cuenta (`catOf`), así que los números no coinciden.
2. **Franjas futuras:** `programSlots` completa el eje con `Slot` vacíos (`programs.go:794-799`). El gráfico les pone `<title>` ("0 mensajes · 0 personas"), `viewersLine` las cuenta como "sin dato" en el aria y la línea típica se estira sobre ellas, como si esos minutos ya hubieran pasado.
3. **Largo esperado:** `expectedLength` usa `Emission.span()` (`loyalty.go:34`), del primer tramo del día al último. Un programa que sale dos veces por día "dura" 12 h y las barras quedan finitas.

## Objetivo

Que la columna Preguntas cuente como Categorías, que el futuro del eje se vea como espacio vacío y no como franjas sin chat, y que el largo esperado sea el de una sola salida del programa.

## Criterios de aceptación

- [x] CA1: en un tramo con etiquetas, Preguntas cuenta los etiquetados con `Cat == pregunta` más los no etiquetados que las reglas marcan como pregunta (el criterio de `catOf`). — `TestProgramRowsClaude`.
- [x] CA2: el gráfico de un tramo al aire dibuja solo las franjas reales (un `<title>` y un `rect.hit` por franja real). El espacio hasta el fin esperado queda a la izquierda con `viewBox="-P 0 W+P 100"`, sin barras, sin línea típica y sin contar en el aria de oyentes. Un rótulo marca el fin esperado en el borde. — `TestProgramParticipationAxis`.
- [x] CA3: el largo esperado es la mediana, entre las emisiones anteriores, del bloque seguido de tramos (huecos ≤ `segmentGap`) que empieza a la hora del reloj más cercana. Un programa que sale a las 01:50 (1 h) y a las 11:50 (1 h 30) da 1 h 30 para la salida de las 11:50. — `TestProgramParticipationAxis` (caso nuevo).
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde; vista previa de Programas en claro, oscuro y celular.

## Diseño propuesto

- `programRows`: por mensaje, `claudeQ[i]` suma `l.Cat == CatPregunta` si tiene etiqueta y, si no, `ask` por reglas.
- `programSlots` devuelve las franjas reales y cuántas faltan (`pad`). `programParticipation` arma el gráfico solo con las reales y después le corre el origen (`X0 = -pad`, `VW = W + pad`), recalcula la posición de marcas y rótulos y agrega el del fin esperado. `stepPath` y `viewersLine` no cambian (el viewBox corrido los deja en su lugar). La plantilla `part-chart` usa `X0`/`VW` si están; los demás gráficos no cambian.
- `expectedLength`: bloques por emisión (`runs`), el de inicio más cercano a la hora del reloj de `start`.

## Fuera de alcance

Los hallazgos menores 4 a 6 de la revisión (doble `Labels`, tests de ramas del resumen, marca de "ahora").

## Riesgos

Sin cuota, sin Claude, sin datos nuevos guardados. Solo cambia cómo se dibuja y cuenta lo que ya está.
