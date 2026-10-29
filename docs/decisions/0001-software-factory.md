# ADR 0001: Flujo de software factory con agentes de Claude Code

- **Estado:** aceptada
- **Fecha:** 2026-10-29

## Contexto

Queremos que tareas repetibles (empezando por bugs con stack trace o tests que fallan)
las resuelvan agentes de forma auditable. El proyecto es chico, Go solo con stdlib, y
no tenía git, CI ni chequeos automáticos más allá de `gofmt`, `go vet` y `go test`.

## Decisión

- Memoria en el repo: `CLAUDE.md` (comandos, definición de terminado) que importa
  `AGENTS.md` (reglas de dominio); una spec por tarea en `docs/specs/`; ADRs acá.
- Agentes de Claude Code en `.claude/agents/` con herramientas mínimas: `triage`
  (escribe la spec), `implementer` (test que falla primero), `tester` (suite y
  regresiones), `reviewer` (solo lectura). Un skill `/fix-bug` los orquesta; una
  persona aprueba la spec antes de implementar.
- Una rama por tarea y PR a `main`; nunca commit directo a `main`.
- Quality gates: GitHub Actions (gofmt, vet, staticcheck, build Linux/Windows, tests con
  `-race`, govulncheck) y hooks de Claude Code (formatear al editar, bloquear comandos
  destructivos y el acceso a `.env`).
- `staticcheck` y `govulncheck` se corren con `go run pkg@versión` fijada, para no
  agregar `require` a `go.mod`.

## Consecuencias

- Cada cambio deja rastro: spec, commits, evidencia de tests y revisión en el PR.
- Más pasos para cambios triviales; para esos alcanza una rama y un PR sin spec.
- Las herramientas de lint se bajan la primera vez (necesita red en CI y en local).
- Descartado: frameworks de orquestación externos (más dependencias y superficie).
