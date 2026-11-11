# Spec: comparar la participación de un programa con sus emisiones anteriores

- **Estado:** implementada
- **Tipo:** mejora
- **Rama:** `feat/programas-comparar-emisiones`
- **Origen:** pedido del usuario (captura de "Participación durante el programa" de NADIE NOS PARA, Rock And Pop)
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-10

## Contexto

En Programas, el detalle de un tramo grafica el % de participación cada 5 min
(`programParticipation`, `programs.go:616`, con `participationChart`, `server.go:1995`).
La única referencia es la línea punteada "Típica", que es la mediana de las franjas
**de ese mismo tramo** (`ParticipationRange`, `analyze.go:208`). No se puede saber si
el programa anduvo mejor o peor que otras veces. Ya existen las emisiones del mismo
programa de los últimos 28 días, con sus mensajes y espectadores
(`programHistory` → `programEmissions` → `fillEmissions`, `programs.go:520-610`), que
hoy se usan solo para la fidelización.

## Objetivo

Que el gráfico del detalle se pueda comparar, a pedido, con las emisiones anteriores
del programa, y que arriba haya una línea que ubique a esta emisión entre las de las
últimas 4 semanas. Todo con datos ya guardados: sin cuota de YouTube y sin Claude.

## Criterios de aceptación

- [x] CA1: arriba del gráfico hay un selector "Comparar con": *Ninguna · Emisión
  anterior · Mismo día la semana pasada · Lo normal (4 semanas)*, con botones de 44 px
  como mínimo. Viaja en el parámetro `comparar` (`ninguna|anterior|semana|normal`, lista
  cerrada) de `/programas` y `/parcial/programas/detalle`, y se conserva al cambiar de
  tramo y en el refresco de 60 s. Por defecto, `ninguna`: el gráfico queda igual que hoy.
  Un valor fuera de la lista se toma como `ninguna` y nunca se refleja en el HTML.
  — `TestCompareParam`, `TestProgramDetailCompare`
- [x] CA2: con `normal`, detrás de las barras va una banda (del percentil 25 al 75) y
  una línea con la mediana del % de las emisiones anteriores, franja por franja,
  **alineadas por hora del reloj** (la franja 11:15 contra las 11:15 de las otras). Solo
  cuentan las emisiones anteriores al día del tramo y del mismo tipo de día: de lunes a
  viernes si el tramo es de lunes a viernes, y de fin de semana si es de fin de semana.
  Una franja con menos de 3 emisiones con dato (`minEmissions`) queda sin banda. Si
  ninguna franja llega a 3, el botón está deshabilitado con el motivo ("hacen falta 3
  emisiones anteriores"). La banda reemplaza a la línea "Típica" y el texto de abajo lo
  explica. — `TestNormalBand`
- [x] CA3: con `normal`, una barra que pasa el percentil 75 de su franja lleva una marca
  propia (clase CSS, además del color del top 5) y su `<title>` dice "por encima de lo
  normal (lo normal: x % a y %)". — `TestNormalBand`, `TestProgramDetailCompare`
- [x] CA4: con `anterior` (la emisión más reciente antes del día del tramo, de cualquier
  día) y con `semana` (la del mismo día 7 días antes), se dibuja una línea escalonada con
  el % de esa emisión por hora del reloj. La línea se corta donde no hay dato. La escala
  del gráfico es la mayor entre las barras y lo comparado, para que nada quede afuera. La
  leyenda dice de qué día es ("martes 03/11"). Si no hay emisión para comparar, el botón
  está deshabilitado con el motivo ("no hubo emisión el martes 03/11"). — `TestCompareLine`
- [x] CA5: arriba del gráfico va una línea de resumen, con cualquier valor del selector:
  participación de la emisión (mediana del % de sus franjas, el mismo número que hoy es
  "Típica"), su puesto entre las emisiones de 4 semanas ("3.ª de 12") y el pico de
  espectadores contra el récord de esas emisiones ("pico 16.468 mirando · récord 18.200,
  el 28/10"). Con menos de 3 emisiones no hay puesto, y sin dato de espectadores no se
  muestra la línea. — `TestEmissionRank`
- [x] CA6: los colores nuevos (banda, mediana, línea comparada, marca) son tokens
  definidos en los dos temas, y `aria-label` del SVG suma lo comparado.
  — `TestTemplateTokens`, `TestProgramDetailCompare`
- [x] CA7: el detalle no pide más al `Store` que hoy: las emisiones se arman una sola vez
  (las de `programHistory`) y sirven para la fidelización y para la comparación.
  — revisión del diff
- [ ] CA8: vista previa con datos: Programas en claro, oscuro y ancho de celular, con los
  cuatro valores del selector.
- [x] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

- `loyalty.go` (funciones puras): `EmissionSlots(e Emission) []Slot` (un
  `ParticipationRange` por tramo de la emisión, con sus vivos) y `clockKey(t)` (la hora
  y el minuto local de la franja). Además `NormalBand(prev []Emission, ref time.Time)
  map[clockKey]Band{P25, Med, P75, N}`, que aplica el filtro de tipo de día,
  `CompareEmission(ems, ref, modo) (*Emission, motivo)` y `EmissionRank(ems, cur)`.
- `programs.go`: `programDetail` arma `ems` una vez y se lo pasa a `programHistory` y a
  `programParticipation`. Esta última recibe el modo y la banda o la emisión comparada.
  `compareParam(q)` valida contra la lista cerrada.
- `server.go`: `participationChart` recibe un `*Compare` opcional (nil = igual que hoy)
  con una función de franja a valor. Agrega `Band` (un `path` de polígono con escalones)
  y `CmpLine` (un `path` escalonado que se corta donde no hay dato, como `viewersLine`)
  y la clase `sobre` en las barras. En vivo no cambia (le pasa `nil`).
- `templates/programas.html`: el selector como links `hx-get` a
  `/parcial/programas/detalle` con `tramo`, `dia` y `comparar`, más `hx-push-url`, y la
  línea de resumen. `part-chart` dibuja `Band` y `CmpLine` si vienen.
- **Orden del eje:** se mantiene lo más nuevo a la izquierda, como En vivo, porque el
  gráfico (`part-chart`) es el mismo. Darlo vuelta solo en Programas confundiría entre
  pantallas. Si se quiere de izquierda a derecha, es otra tarea para las dos pantallas.
- Alternativa descartada: alinear por minutos desde el inicio. El inicio del tramo se
  corre según cuándo cambia el título, y para una radio se lee mejor la hora.

## Fuera de alcance

- Gráficos chicos por emisión y comparar la curva de oyentes.
- Cambiar el orden del eje o el gráfico de En vivo.
- Guardar algo nuevo: no hay archivos ni campos nuevos.

## Riesgos

- **Cuota / Claude:** ninguno. Solo se leen datos guardados.
- **Performance:** son unas 20 emisiones × unas 40 franjas por cada pedido del detalle.
  Se reusan las emisiones que ya arma `programHistory`, sin lecturas nuevas del `Store`
  (CA7).
- **Retención:** la ventana de 28 días entra en los 30 de retención. Lo que se borró no
  se compara.
- **Inyección por parámetro:** `comparar` es una lista cerrada y no se refleja si no está
  en la lista (CA1).
- **Lectura:** el gráfico ya tiene barras, top 5 y la línea de oyentes. Por eso la
  comparación viene apagada por defecto y se ve de a una por vez.
