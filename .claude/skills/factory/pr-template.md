# Plantilla del cuerpo del PR de /factory

Copiá desde `## Resumen` hasta el final y completá. Recortá las salidas a lo que
demuestra el punto (sin datos de usuarios ni la API key). En mejoras y tareas, "Causa
raíz" pasa a ser "Motivo" y el rojo es el test nuevo fallando antes de implementar.

---

## Resumen

<Qué estaba mal, en 1-2 oraciones, y qué cambia.>

- **Spec:** [docs/specs/<archivo>.md](<url del repo>/blob/<tipo>/<slug>/docs/specs/<archivo>.md)
  (la URL sale de `gh repo view --json url -q .url`)
- **Causa raíz:** <una línea con `archivo.go:línea`>

## Cambios

- `<archivo>`: <qué y por qué>

## Evidencia

**Test de regresión antes del arreglo (rojo):**

```
<salida de go test -run '^TestX$' ...>
```

**Después del arreglo (verde):**

```
<salida>
```

**Gates:**

| Gate | Comando | Resultado |
|---|---|---|
| Formato | `gofmt -l .` | ✅ |
| Vet | `go vet ./...` | ✅ |
| Lint | `staticcheck@2026.2.2 ./...` | ✅ |
| Build Linux / Windows | `go build ./...` / `GOOS=windows ...` | ✅ / ✅ |
| Tests con race | `go test -race -count=1 ./...` | ✅ |
| Intermitencias | `go test -count=5 ./...` | ✅ |
| Vulnerabilidades | `govulncheck@v1.9.0 ./...` | ✅ / ⚠️ solo stdlib del Go local |

## Revisión (agente reviewer)

**Veredicto:** <APROBADO | APROBADO CON CAMBIOS MENORES>

- <hallazgos IMPORTANTE/MENOR que quedan abiertos, o "Sin hallazgos pendientes">

## Criterios de aceptación

- [x] CA1 …

## Fuera de alcance

- <lo visto y no tocado>
