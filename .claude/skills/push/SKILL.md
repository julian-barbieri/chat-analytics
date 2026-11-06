---
name: push
description: Último paso del flujo manual de yt-monitor. Corre los gates completos una sola vez, commitea, pushea la rama y abre el PR contra main con la spec y la evidencia.
argument-hint: "[título del PR opcional]"
---

# /push

Último paso del flujo manual (`/idea` → `/implementar` → `/revisar` → `/push`). En
esta sesión, sin subagentes.

Argumentos:

$ARGUMENTS

## Pasos

1. La rama no puede ser `main`. Ubicá la spec y `factory/manual/<slug>/` como en
   `/implementar`. Cortá la vista previa si quedó corriendo (`go run ./tools/preview
   -cortar`, y confirmá que el comando de fondo terminó): los gates compilan y corren
   tests, y la persona ya validó.
2. Revisá `git status --porcelain` y el diff contra `origin/main`: que no entren `.env`,
   `datos.jsonl`, `datos.*`, `informes/`, `factory/` ni binarios (`yt-monitor`,
   `yt-monitor.exe`). Si hay algo raro, pará y preguntá.
3. Gates completos, una sola vez:

   ```bash
   scripts/gates.sh completo | tee factory/manual/<slug>/gates.md
   ```

   Si alguno da ❌, pará: mostrá la falla y proponé el arreglo (o que corra
   `/implementar <nota>`). `⚠️ revisar` en Vulnerabilidades: mirá la salida; si es solo
   la stdlib del Go instalado, se aclara en el PR (CLAUDE.md); si es otra cosa, pará.
4. En la spec: `Estado: implementada` y marcá los criterios cumplidos. Lo que quede sin
   marcar se explica en el PR.
5. Commit en Conventional Commits, en español e imperativo, con el *por qué* en el
   cuerpo y las líneas de atribución que pida la sesión. Push de la rama:

   ```bash
   git add -A
   git commit -m "<tipo>: <qué cambia>" -m "<motivo y solución en 2-3 líneas>"
   git push -u origin <tipo>/<slug>
   ```

6. Si la rama ya tiene un PR abierto (`gh pr view --json url -q .url`), el push lo
   actualizó: no abras otro. Si no, escribí el cuerpo en un archivo temporal (`mktemp`)
   siguiendo `.claude/skills/factory/pr-template.md`, con la evidencia de
   `factory/manual/<slug>/` (rojo, verde, `gates.md`). Si no se corrió `/revisar`, en
   "Revisión" poné "No se corrió (flujo manual)"; si se corrió, el veredicto y los
   hallazgos abiertos. Abrilo:

   ```bash
   gh pr create --base main --head <tipo>/<slug> --title "<tipo>: <qué cambia>" --body-file <archivo>
   ```

7. Antes de cerrar, confirmá que no quedó ninguna vista previa corriendo.

Respondé con el link al PR, los gates en una línea y los hallazgos abiertos. No
mergees: lo decide la persona. Para ver el cambio en su dashboard real tiene que
actualizar `main` y reiniciarlo ella.
