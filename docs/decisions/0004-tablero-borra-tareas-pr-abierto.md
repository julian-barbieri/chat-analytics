# ADR 0004: El tablero puede borrar una tarea en pr-abierto con el tacho

- **Estado:** propuesta
- **Fecha:** 2026-11-02
- **Modifica:** ADR 0002 (el tablero deja de ser de solo lectura) y ADR 0003

## Contexto

Con los ADR 0002 y 0003 el tablero solo lee `factory/` y consulta GitHub. Las tareas
terminadas (PR abierto o ya mergeado) se acumulan en las columnas "PR abierto" y
"Mergeado", y la única forma de sacarlas es borrar la carpeta a mano. La persona pidió
un botón con un tacho de basura para borrarlas desde el tablero, sin ir a la terminal, y
eligió borrar de verdad (no archivar). Restricciones: solo stdlib, `go 1.24.7` en
`go.mod`, Windows, sin autenticación y solo en `localhost`, sin JS propio, y `job.json`
lo escribe solo el orquestador.

Spec: `docs/specs/2026-11-02-borrar-tarea-pr-abierto.md`.

## Decisión

- **El tablero deja de ser de solo lectura en un único punto:** `POST
  /tarea/{id}/eliminar` borra la carpeta `factory/jobs/<id>/` entera. Los `GET` siguen
  sin escribir nada y `job.json` sigue siendo solo del orquestador: el tablero lo borra
  junto con la carpeta, nunca lo edita.
- **Qué se borra:** solo tareas cuyo `job.json` es válido y dice `etapa: pr-abierto`
  (también las que el tablero muestra en "Mergeado"). Cualquier otra etapa, un
  `job.json` faltante, roto o con etapa desconocida, o una tarea con actividad en los
  últimos 2 min o con un subagente activo, responde 409 con el motivo y no toca nada.
- **Quién puede pedirlo:** solo `localhost` (`localOnly`), solo htmx desde la propia
  página (`HX-Request: true`, `Origin` igual al `Host` si viene, `Sec-Fetch-Site:
  same-origin` si viene); si no, 403. Un `id` que no cumple `idRe` o que no es una
  carpeta real (un enlace simbólico o un archivo) da 404.
- **Cómo se borra:** primero `os.Rename` a `factory/jobs/.borrando-<id>` (atómico en el
  mismo volumen; la tarea desaparece del tablero de una vez porque el nombre con punto
  no cumple `idRe`) y después `os.RemoveAll`. Si el borrado falla a mitad, se avisa y el
  próximo `POST` de borrado barre los `.borrando-*` que hayan quedado. El barrido va en
  el `POST`, no en los `GET`.
- **La persona confirma** con `hx-confirm`, que dice que **no se puede deshacer** y que
  el PR, la rama y la spec no se tocan. El 409 se muestra gracias a un
  `<meta name="htmx-config">` con `responseHandling` (configuración declarativa, no JS).
- **Eventos sin tocar:** `factory/events/` es de todas las tareas y lo agrega el hook en
  paralelo; se va solo a los 30 días. El feed muestra el ID de una tarea borrada como
  texto, sin link.
- **Numeración:** el skill `/factory` elige el `NNN` siguiente contando también las
  carpetas `.borrando-*` y los IDs que nombran los eventos, para que un número borrado
  no se reuse mientras haya eventos que lo mezclen con la tarea nueva.

## Consecuencias

- Se gana sacar del tablero las tareas terminadas con un clic.
- **No se puede deshacer:** se pierden los resultados de los agentes, las revisiones y
  los gates guardados en la carpeta. Se acepta porque el trabajo ya está en el PR y la
  spec en git, y la persona lo ve en el `confirm`.
- Queda una ventana chica entre las comprobaciones y el rename en la que el orquestador
  podría escribir la tarea; el skill avisa si al retomar la carpeta ya no existe.
- En Windows, un archivo abierto en otro programa hace fallar el rename (409, nada
  cambió) o el `RemoveAll` (queda `.borrando-<id>` fuera del tablero hasta el próximo
  borrado).
- Descartadas: archivar en `factory/archivo/` (la persona eligió borrar); `RemoveAll`
  directo (un fallo a mitad deja una tarea rota visible); renombrar a una carpeta
  temporal del sistema (`os.Rename` entre volúmenes falla); barrer en los `GET`
  (dejarían de ser de solo lectura); etapa `borrada` en `job.json` (el tablero tendría
  que escribirlo); `http.CrossOriginProtection` (Go 1.25); responder 200 en los rechazos;
  sacar líneas de `factory/events/` (perdería eventos de otras sesiones); guardar el
  último número en un archivo aparte.
