# ADR 0008: Flujo manual por pasos al lado de /factory

- **Estado:** aceptada
- **Fecha:** 2026-11-06

## Contexto

`/factory` lleva una tarea sola de punta a punta, pero tarda: sin contar la espera de la
aprobación, las tareas 008 a 012 llevaron entre 36 y 100 minutos. El tiempo se va en
tres cosas: cada agente (triage, implementer, tester, reviewer) arranca sin contexto y
vuelve a leer el código; una revisión con hallazgos repite implementar, tester y
reviewer (20 a 40 minutos por vuelta); y la suite pesada (`-race`, `-count=5`) corre en
el tester y otra vez en los gates. La persona quiere validar cada paso y avanzar más
rápido.

## Decisión

Sumamos un flujo manual de cuatro skills que la persona corre una por una: `/idea`,
`/implementar`, `/revisar` (opcional) y `/push`. Trabajan en la sesión principal, sin
subagentes, para que lo leído en un paso sirva en el siguiente. Solo `/revisar` usa el
agente `reviewer`, porque ahí el valor es una lectura que no viene de quien escribió el
código, y los hallazgos se arreglan en la misma sesión sin otra vuelta completa. El
tester deja de ser un paso: `/implementar` escribe y corre los tests (TDD). Los gates
viven en `scripts/gates.sh`: rápido mientras se trabaja y completo una sola vez en
`/push`. Al final de `/implementar` se levanta sola la vista previa (`tools/preview`,
ADR 0003) en `localhost:8081` con una lista de qué probar, para que la persona valide
que el cambio es lo que buscaba antes de seguir: correr `/implementar` cuenta como el
pedido que exige `CLAUDE.md`. Se reinicia en cada pasada (compila la rama al arrancar)
y `/push` la corta. La evidencia queda en `factory/manual/<slug>/`, en `.gitignore`.

`/factory` sigue igual, para tareas que puedan correr solas.

## Consecuencias

- Más rápido y con control de la persona en cada paso; a cambio, la persona orquesta.
- La revisión del `reviewer` pasa a ser recomendada en el flujo manual: si no se corrió,
  el PR lo dice. Cambia el punto 6 de "Definición de terminado" en `CLAUDE.md`.
- Las tareas manuales no aparecen en el tablero (no escriben `factory/jobs/`).
- Descartado: skills que lancen cada agente por separado (mismo tiempo por paso que
  hoy) y bajar el modelo de los agentes (ahorra poco: el costo está en el contexto
  que se pierde).
