# ADR 0003: El tablero consulta GitHub con gh y la vista previa del PR es una herramienta aparte

- **Estado:** propuesta
- **Fecha:** 2026-10-30
- **Complementa:** ADR 0002

## Contexto

Con el ADR 0002 el tablero solo lee `factory/` y nunca usa la red. Pero `job.json` deja
de cambiar cuando el orquestador abre el PR, así que una tarea mergeada queda para
siempre en "PR abierto": cuando se mergea, el orquestador ya no está corriendo. Además,
la persona quiere ver el cambio del PR en su dashboard antes de mergearlo, sin gastar
cuota de YouTube ni tocar `datos.jsonl`, y sin que nadie deje un `-dashboard` corriendo
contra la API real. Restricciones: solo stdlib, Windows, retención de 30 días, nada
expuesto fuera de `localhost`, y el hook que bloquea `rm -rf`.

Spec: `docs/specs/2026-10-30-tablero-mergeado-vista-previa.md`.

## Decisión

- **El tablero lee archivos y además consulta GitHub**, solo para las tareas en
  `pr-abierto`: `gh pr view <url> --json state,mergedAt` (solo lectura, con timeout, sin
  prompts). Si el PR está `MERGED`, la tarjeta pasa a la columna derivada "Mergeado",
  que **no existe en `job.json`** (el tablero la calcula; `readJob` la rechaza). `CLOSED`
  y los errores quedan en "PR abierto" con un aviso. El tablero sigue sin escribir nada.
- **Caché en memoria con refresco de fondo**: cada PR se consulta como mucho cada 2 min
  (`prEvery`), un mergeado nunca más, y hay una sola ronda en vuelo; ningún pedido htmx
  espera a `gh`. La URL se valida antes de ejecutar `gh`, para que un `pr` con forma de
  flag no le llegue.
- **Vista previa del PR en `tools/preview/`**, otro `package main` (no entra en el
  binario): compila el binario de la rama, lo corre con `-dashboard` en
  `localhost:8081` (solo loopback), con el directorio de trabajo en una copia temporal
  `0700` de `datos.jsonl`, `datos.cuota.json` y `datos.espectadores.jsonl`, y con una
  API de YouTube simulada que arma los vivos a partir de la copia. El entorno del hijo
  no hereda `YT_API_KEY` ni `YT_BASE_URL`. La copia se borra al cortar (señal, aviso de
  corte con `-cortar` o fin del hijo), y `-cortar` limpia por su cuenta si la vista
  previa ya no corre. La ofrece el orquestador en el paso 7b de `/factory`, solo si el
  cambio toca el dashboard o lo que muestra, y la corta antes de cerrar.
- **Copias y estado en una carpeta privada**, `os.UserCacheDir()/yt-monitor-vista-previa/`
  (`0700`), y no en la temporal del sistema: ahí cualquier usuario de la máquina podría
  plantar un enlace como aviso de corte, un estado con un PID ajeno o el temporal del
  estado (revisión 1). Es portable sin build tags y permite barrer al arrancar todas las
  copias huérfanas. El aviso de corte se crea con `O_EXCL` y el temporal del estado con
  `os.CreateTemp`: ninguno sigue enlaces.
- **El archivo de estado es también el lock**: se crea en exclusiva (temporal +
  `os.Link`) apenas existe la copia, con `Child` en 0 hasta que arranca el dashboard.
  Así `-cortar` ve a la vista previa también mientras compila (el build mira el aviso y
  se cancela) y de dos que arrancan a la vez solo una sigue.
- **Nunca se mata un PID sin comprobar que es el dashboard**: en Linux un PID del estado
  cuenta solo si su `cwd`, su `exe` o un archivo abierto están dentro de la copia (la
  vista previa la deja abierta para eso); con esa misma prueba se decide si un estado es
  de una vista previa viva. En Windows no hay `/proc` y `FindProcess` no distingue un PID
  reasignado: `-cortar` nunca mata de respaldo, informa el PID y solo borra la copia si
  se deja borrar.

## Consecuencias

- La tarjeta pasa sola a "Mergeado" en unos 2 minutos. Con N PRs abiertos hay como mucho
  N llamadas a `gh` cada 2 minutos; `gh` usa la sesión de la persona y no gasta cuota de
  YouTube. Sin `gh` o sin sesión, la tarjeta queda en "PR abierto" con el aviso.
- Al reiniciar el tablero se vuelve a consultar todo una vez (la caché no va a disco).
- La vista previa es una **copia de datos reales**: es la única permitida, vive en la
  carpeta privada del cache del usuario, con permisos `0700`, y el dashboard le aplica
  la misma retención de 30 días al abrirla. Caso no cubierto: si la máquina se apaga con
  la vista previa abierta, la copia queda en esa carpeta hasta la próxima vista previa,
  que barre las huérfanas al arrancar (el sistema no la limpia solo, como haría con
  `/tmp`).
- Si `-cortar` no puede comprobar el PID (Windows), la persona puede tener que cerrar el
  dashboard a mano; el mensaje le dice cuál.
- La vista previa es una foto: no llegan mensajes nuevos. Los videos no se guardan, así
  que los títulos de los vivos los inventa la API simulada con el último programa.
- No se probó en Windows de verdad (compila, usa `Kill` + `Wait` antes de borrar).
- Descartado: que el orquestador escriba `etapa: mergeado` (no está corriendo cuando se
  mergea); llamar a `gh` en cada pedido (30 por minuto por PR); `gh pr list` (depende
  del clone y del límite de resultados); la API REST con `net/http` (token a mano); un
  flag `-datos` en `yt-monitor` (cambia el producto sin necesidad); un script de shell
  (no anda en Windows y limpiar necesitaría `rm -rf`); servir la vista previa desde el
  tablero (mezcla el tablero con correr binarios de ramas).
