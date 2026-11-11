# Spec: Mapa por emisión con dos decimales bajo el 1 %

- **Estado:** implementada
- **Tipo:** mejora
- **Rama:** `feat/mapa-dos-decimales`
- **Origen:** pedido del usuario: en "Mapa por emisión · % participación cada 30 min" muchas
  celdas dicen 0,1 con distinto tono de color; seguramente tienen otro valor.
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-11

## Contexto

`EmissionMap` (`loyalty.go`) escribe cada celda con `decimal` (un decimal), pero la
participación de un programa suele estar entre 0,0x % y 0,1x %. El color (`heatClass`) usa el
valor real, así que dos celdas "0,1" pueden tener tonos distintos. `pctFine` (`server.go`) ya
resolvía lo mismo para otros textos con dos decimales bajo el 1 %.

## Objetivo

Que las celdas del mapa (emisiones y mediana) muestren dos decimales cuando el valor está por
debajo de 1, sin cambiar los colores ni los valores de 1 o más.

## Criterios de aceptación

- [x] CA1: una celda de 0,12 % se lee "0,12" y una de 0,08 % "0,08"; la mediana entre las dos,
  "0,10" — `TestEmissionMapFine`.
- [x] CA2: los valores de 1 o más siguen con un decimal ("2,0", "4,0") y las clases no cambian
  — `TestEmissionMap`.
- [x] CA3: `pctFine` sigue igual ("0,04 %") — test existente.

## Fuera de alcance

Cambiar la escala de colores o el título de la tarjeta.
