# Spec: Programas: barras de ancho fijo y programa partido por un corte en una sola fila

- **Estado:** borrador
- **Tipo:** bug
- **Rama:** `fix/programas-barras-tramos-unidos`
- **Origen:** mensaje del usuario con capturas (2026-11-11).
- **Aprobada por / fecha:** —

## Contexto

1. **Barras anchas.** El SVG de "Participación durante el programa" usa `viewBox` con
   `W` = franjas (+ `pad`, `programs.go:886`) y `preserveAspectRatio="none"`
   (`templates/partials.html:302`): el ancho de cada barra es el ancho de la tarjeta
   dividido por la cantidad de franjas. Con 5 franjas y 1 de `pad` (captura: 09:45 a
   10:15) cada barra ocupa ~1/6 de la tarjeta; con 3 h, 1/36. El eje de la 038 iguala
   el ancho solo dentro de un mismo programa, no entre programas.
2. **Programa partido.** `ProgramSegments` (`analyze.go:506`) corta un tramo cuando hay
   un hueco de más de `segmentGap` (7 min) y `programSegs` (`programs.go:58`) hace una
   fila por tramo. Si la transmisión se cae y vuelve (mismo vivo o vivo nuevo), el
   mismo programa sale en dos filas: "ESTA MAÑANA con NACHO ORTELLI" 09:24–09:32 y
   09:47–10:14; "NADIE NOS PARA" 09:24–09:32 y 09:47–al aire.

## Objetivo

Que cada franja de 5 min mida lo mismo en cualquier programa, y que un programa cortado
por una caída se vea como uno solo: una fila y un detalle con todos sus pedazos.

## Criterios de aceptación

- [ ] CA1: en Programas el gráfico tiene un ancho fijo por franja (`28px` por posición,
  contando el `pad`): el contenedor lleva `style="width: N×28px"` con `max-width: 100%`
  (en pantallas angostas o programas muy largos se achica). En vivo no cambia. —
  `TestProgramParticipationAxis` (caso: 6 posiciones → 168 px; 36 → 1008 px) y
  `TestProgramsPage` (el `style` sale en el HTML).
- [ ] CA2: `programSegs` junta en un solo `ProgramSeg` los tramos seguidos del mismo
  canal, mismo programa (sin distinguir mayúsculas) y mismo día local, cuando entre
  ellos solo hay "sin vivo" (ningún otro programa). `Parts` guarda los pedazos;
  `Seg` va del inicio del primero al fin del último, con el estado y el `VideoID` del
  último. — `TestProgramSegs` (casos: corte de 15 min en el mismo vivo; corte con vivo
  nuevo; otro programa en el medio → no se juntan; distinto día → no se juntan).
- [ ] CA3: la clave de la fila es la del primer pedazo (no cambia cuando aparece otro);
  `?tramo=` con la clave de cualquier pedazo elige la fila unida. — `TestProgramSegs` y
  `TestProgramsPage`.
- [ ] CA4: la fila suma mensajes, Super Chats y preguntas de todos los pedazos y cuenta
  personas sin repetir; el horario es `09:24–10:14` (o `09:24–` al aire) con la marca
  "1 corte". — `TestProgramRows` (caso nuevo).
- [ ] CA5: el detalle y el CSV del programa usan los mensajes de todos los pedazos (sin
  duplicar los que caen en los 5 min de margen de dos pedazos); el gráfico va del
  inicio al fin con las franjas del corte vacías y el aria/la línea dicen "se cortó de
  09:32 a 09:47". — `TestProgramDetailCut` (nuevo) y `TestExportProgram`.
- [ ] CA6: la historia y la comparación siguen contando una emisión por día (ya lo
  hacía `programEmissions`, que ahora recibe todos los pedazos). — tests existentes
  de `programEmissions` en verde.
- [ ] CA7: navegador con la vista previa: Programas en claro, oscuro y ancho de celular.
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

- `ProgramSeg` suma `Parts []Segment` (uno solo si no hubo corte). Después de armar los
  tramos de un canal, `programSegs` recorre en orden de inicio y une con el anterior si
  cumple CA2. `programRows` indexa los pedazos (no las filas) y suma en la fila dueña;
  personas en un mapa por fila.
- `segmentMessages` se aplica a cada pedazo y se deduplica por `Message.ID`
  (`programMessages`); lo usan `programsData`, `findProgram` y el CSV.
- `programSlots` pasa a `ParticipationRange` todos los videos de los pedazos y los
  espectadores de cada uno; el corte queda como franjas sin barra.
- Resumen de Claude: los análisis siguen siendo por pedazo (el analista no cambia). La
  fila muestra el del último pedazo que tenga uno, y el detalle dice qué horario cubre.
- Ancho: `programParticipation` agrega `Px` = `(W+pad)×28`; `programas.html` envuelve
  `part-chart` en un `div` con ese `width` y `max-width: 100%` (CSS en `layout.html`).

## Fuera de alcance

- Que Claude haga un solo resumen del programa unido (cambia cuándo y cuánto pide).
- Juntar tramos en En vivo ("Hoy en el canal", "Programa en curso").
- Juntar pedazos separados por otro programa (un programa que sale dos veces por día).

## Riesgos

- **Cuota / Claude:** ninguno; solo se reordena lo ya guardado.
- **Claves guardadas:** los análisis siguen con la clave de cada pedazo; un link viejo
  con la clave del segundo pedazo sigue andando (CA3).
- **Rendimiento:** la unión es lineal en los tramos; `programRows` sigue en una pasada.
