# Spec: mostrar la hora de Argentina aunque la PC esté en UTC

- **Estado:** implementada
- **Tipo:** bug
- **Rama:** `fix/zona-horaria-argentina`
- **Origen:** pedido del usuario: "se está tomando un horario distinto al de Argentina (parece estar 3 horas por delante)"
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-02 (pedido directo)

## Contexto

Todas las horas visibles (dashboard, informe, CSV, logs) y el horario pico del worker
(`WorkerConfig.chatInterval`) usan `time.Local`, que sale de la zona del sistema. La PC
donde corre está en WSL con `/etc/localtime -> Etc/UTC` y sin `TZ`, así que todo se ve
3 horas adelantado respecto de Argentina (UTC-3) y el horario pico `7-20` en realidad
iba de 4 a 17 h de Argentina.

## Objetivo

Que el binario use la hora de Argentina por defecto, sin depender de la zona del sistema
(Linux, WSL o Windows), y que se pueda cambiar si hiciera falta.

## Criterios de aceptación

- [x] CA1: con `time.Local` en UTC, `setZone(defaultZone)` hace que 13:08 UTC se
  muestre como 10:08 — `TestSetZone` (falla antes del arreglo: no existe `setZone`;
  salida guardada).
- [x] CA2: el horario pico se evalúa en hora argentina: 21:30 UTC (18:30) queda dentro
  de `7-20` — `TestSetZone`.
- [x] CA3: `-zona ""` deja la zona del sistema y una zona inexistente es un error —
  `TestSetZone` y `go run . -zona Marte/X` termina con `-zona: unknown time zone Marte/X`.
- [x] CA4: README (opciones de ambos modos) y AGENTS.md documentan `-zona`.
- [x] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

- `main.go`: flag `-zona` (por defecto `America/Argentina/Buenos_Aires`) y `setZone`,
  que fija `time.Local` justo después de `flag.Parse`, antes de arrancar cualquier
  goroutine (así no hay carrera con quien lee `time.Local`). La base de zonas ya viene
  embebida con `time/tzdata`, así que anda igual en Windows.
- No se tocan los `t.Local()` existentes: al cambiar `time.Local` quedan bien todos.
- La cuota sigue con su propia zona del Pacífico (`store.go: pacific`), que no depende
  de `time.Local`.

## Fuera de alcance

- Datos ya guardados: se guardan con su instante (`time.Time` con zona), así que se
  muestran bien sin migrar nada.

## Riesgos

- Cuota: ninguno de consumo total; el horario pico pasa a caer en el horario real de la
  radio (7 a 20 de Argentina), que es lo que suponían las cuentas del README.
