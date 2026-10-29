# Spec: <título corto>

<!--
Copiá este archivo como docs/specs/AAAA-MM-DD-<slug>.md. Lo escribe el agente triage;
una persona lo aprueba antes de implementar. Todo en español. Borrá los comentarios.
-->

- **Estado:** borrador | aprobada | implementada | descartada
- **Tipo:** bug | mejora | tarea
- **Rama:** `fix/<slug>`
- **Origen:** <ticket, mensaje del usuario, stack trace, salida de CI>
- **Aprobada por / fecha:** <nombre, AAAA-MM-DD>

## Contexto

<!-- Qué pasa hoy. Para bugs: síntoma, cómo se reproduce (comando exacto), salida
observada (traza recortada) y frecuencia. Causa raíz con archivo:línea. -->

**Reproducción:**

```bash
go test -count=1 -run '^TestX$' ./...
```

```
<salida observada>
```

**Causa raíz:** <explicación con `archivo.go:línea`>

## Objetivo

<!-- Una o dos oraciones: qué tiene que ser verdad cuando esto esté hecho. -->

## Criterios de aceptación

<!-- Verificables por un comando o un test. Nada de "funciona mejor". -->

- [ ] CA1: <condición> — se verifica con `<comando o TestNombre>`
- [ ] CA2: …
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

<!-- Cambio mínimo: archivos y funciones a tocar, test de regresión a escribir primero.
Si hay alternativas razonables, una línea por cada una y por qué no. -->

## Fuera de alcance

<!-- Lo que NO se toca en esta tarea aunque se haya visto. -->

## Riesgos

<!-- Cuota de YouTube, compatibilidad de datos.jsonl, concurrencia, Windows,
privacidad. Para cada uno: impacto y cómo se mitiga o verifica. -->
