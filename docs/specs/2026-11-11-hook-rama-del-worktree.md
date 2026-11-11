# Spec: el hook mira la rama de la carpeta donde corre el comando

- **Estado:** implementada
- **Tipo:** bug
- **Rama:** `chore/hook-rama-del-worktree`
- **Origen:** sesión del 2026-11-11 (fix/youtube-conexion-muerta): el hook no dejaba
  commitear desde un worktree.
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-11

## Contexto

`guard-bash.sh` decide si un `git commit` o un `git push` sin argumentos es "en main" con la
rama de `$CLAUDE_PROJECT_DIR` (`.claude/hooks/guard-bash.sh:19`), que es el checkout principal.
Si otra sesión deja ese checkout en `main`, una sesión que trabaja en un worktree de una rama
de tarea no puede commitear: el hook la bloquea como "commit en main" aunque el commit vaya a
la rama de la tarea.

**Causa raíz:** el hook usa la carpeta del proyecto y no la del comando. Claude Code le pasa
la carpeta del comando en el campo `cwd` de la entrada del hook.

## Objetivo

El hook decide con la rama de la carpeta donde corre el comando; sigue bloqueando los commits
y los push sin argumentos estando en `main`.

## Criterios de aceptación

- [x] CA1: `git commit` con `cwd` en una carpeta en `main` → bloqueado (salida 2).
- [x] CA2: `git commit` y `git push` con `cwd` en un worktree de una rama de tarea → permitido (0).
- [x] CA3: `git push origin main`, `git push` sin argumentos en `main` y `rm -rf` siguen bloqueados.
- [x] CA4: sin `cwd` en la entrada, vuelve a `$CLAUDE_PROJECT_DIR` como antes (en `main` → 2).

Se verifica pasándole al hook la entrada JSON a mano (salidas en el PR).

## Diseño

Leer la entrada una vez (`input=$(cat)`), sacar `command` y `cwd` con `jq` y usar
`git -C "${cwd:-${CLAUDE_PROJECT_DIR:-.}}" branch --show-current`. Nota en `CLAUDE.md`.

## Fuera de alcance

Detectar un `cd <otra carpeta>` o `git -C <otra carpeta>` dentro del comando: el hook mira la
carpeta donde arranca el comando, como hasta ahora miraba la del proyecto.

## Riesgos

- Un comando que hace `cd` al checkout principal en `main` y commitea desde una sesión en un
  worktree no se bloquea. Antes tampoco se cubría el caso inverso (una sesión en el checkout
  principal que entra a un worktree). Es un guardarraíl, no un control de acceso.
