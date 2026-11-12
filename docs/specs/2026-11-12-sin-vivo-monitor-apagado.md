# Spec: Sin vivo distingue monitor apagado de corte

- **Estado:** implementada
- **Tipo:** mejora
- **Rama:** `feat/sin-vivo-monitor-apagado`
- **Origen:** pedido del usuario con captura de Sin vivo (2026-11-12)
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-12

## Contexto

Sin vivo arma los ratos con `ProgramSegments` sobre `datos.vivos.jsonl`
(`nolive.go:72`), que solo tiene muestras **cuando hay vivo** (`store.go:389`,
`AddLives` descarta lo que no es `live`). Sin muestras no se sabe si el canal no
tenía vivo o si el monitor estaba apagado, y por eso:

- Un hueco de más de 2 h se pinta gris y no cuenta (`maxCut`, `nolive.go:15`),
  aunque el monitor haya estado andando y el canal sin vivo.
- Un hueco corto no aparece: dentro del mismo vivo hace falta más de `segmentGap`
  (7 min) y entre vivos más de `newLiveGap` (4 min) para que sea un rato sin vivo
  (`analyze.go:537-542`). Un corte de 1 a 3 min queda pintado como al aire.

## Objetivo

Cada rato del pasado se pinta según lo que vio el monitor: **al aire** (azul),
**corte** (rojo: el monitor revisó el canal y no encontró vivo, sin mínimo ni
máximo de duración) o **monitor apagado** (gris: no hubo revisiones; no es
alerta y no cuenta en los números).

## Criterios de aceptación

- [x] CA1: el worker guarda una revisión sin vivo (`NoLiveCheck{At, ChannelID}`)
  por cada canal resuelto que una revisión completa o una búsqueda rápida
  encontró sin vivo en curso, en `datos.sinvivo.jsonl`; con vivo no guarda nada
  nuevo (ya está `datos.vivos.jsonl`). Sin cuota extra. — `TestFlowAgainstFakeAPI`
  (o un test del worker con la API falsa) cuenta las líneas y `yt.Units` no cambia.
- [x] CA2: retención de 30 días al abrir (tmp + `os.Rename`), como `datos.vivos.jsonl`.
  — test del store con una muestra vencida.
- [x] CA3: un rato entre dos muestras de vivo con al menos una revisión sin vivo en
  el medio es **corte**, aunque dure 1 min y aunque pase de 2 h. — `TestNoLiveCuts`
  (tabla: 1 min, 3 min, 5 h).
- [x] CA4: un rato sin ninguna muestra (ni de vivo ni sin vivo) de más de
  `segmentGap` es **monitor apagado**: gris, no figura en Cortes ni en los números.
  Si un corte lo toca, el corte va desde la última evidencia antes hasta la primera
  después que lo prueben (no se estira sobre lo apagado). — `TestNoLiveCuts`.
- [x] CA5: "Al aire" = aire ÷ (aire + cortes), sin lo apagado; los textos de las
  tarjetas y la leyenda dicen "monitor apagado" en vez de "más de 2 h", y se va
  `maxCut` / la clase `largo`. — test de `noLiveData` y vista previa.
- [x] CA6: un corte de 1 min se ve en la tira (`.np.corte` ya tiene `min-width: 3px`).
  — vista previa, claro/oscuro y celular.
- [x] CA7: la vista previa copia `datos.sinvivo.jsonl`. — test de `tools/preview`.
- [x] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

- `store.go`: `NoLiveCheck`, `AddNoLive(at, channelIDs)` (append, como `AddLives`),
  `NoLiveChecks(since)` (copia), carga con `loadJSONL` y la misma retención.
- `worker.go`: después de `trackChannels` en la revisión completa, los canales
  resueltos sin vivo; en la búsqueda rápida, el canal si `!live`. Solo si el pedido
  anduvo (un error no prueba nada: ese rato queda gris).
- `nolive.go`: `NoLiveCuts` recibe también las revisiones sin vivo. Los ratos al aire
  salen de `ProgramSegments` como hoy; el resto se recorre por evidencias ordenadas
  (fin/inicio de tramos al aire y revisiones sin vivo): entre dos evidencias
  separadas por más de `segmentGap` es apagado; una serie de revisiones sin vivo es
  corte, desde el fin del tramo anterior (si está a ≤ `segmentGap`) hasta el inicio
  del siguiente (o ahora, si sigue). Clases: `aire`, `corte`, `apagado`.
- `nolivepage.go`, `templates/sinvivo.html`, `layout.html`: textos, leyenda, clase
  `.np.apagado` (mismo token gris que hoy `largo`). Lo futuro de hoy sigue en blanco.
- `tools/preview`, README y AGENTS.md (mapa, datos y privacidad): el archivo nuevo.

De paso, para que los gates queden en verde: `sentimentChart` (`batch.go`) ponía los
rótulos por hora con `Truncate(time.Hour)`, que cuenta en UTC; con una zona no corrida
en horas enteras (la de `noonLocal`) caían a mitad de hora y `TestSentimentChartWide`
fallaba también en `main`. Ahora se alinean a la hora en punto local.

Alternativa descartada: guardar una muestra por revisión aunque haya vivo (más
simple de leer, ~3× más líneas sin dato nuevo).

## Fuera de alcance

Otras pantallas que usan `ProgramSegments` (En vivo, Programas, Canales) siguen igual.
No se avisa en En vivo ni se notifica nada.

## Riesgos

- **Historia:** antes de este cambio no hay revisiones sin vivo, así que los días
  anteriores van a verse grises fuera de los vivos (no se puede saber si el monitor
  andaba). Se completa solo en 30 días.
- **Falsos cortes:** si la lista de vivos `UULV` falla en silencio (`ChannelVideos`
  sigue sin ella), la revisión diría "sin vivo". Hoy pasa igual con la tarjeta Canal;
  se acepta.
- **Tope de cuota:** con `Budget` alcanzado no hay revisiones: se ve gris, no rojo.
- **Datos:** archivo nuevo de la API (canal y hora), en `.gitignore` (`datos.*.jsonl`),
  30 días, nunca dentro de `datos.jsonl`. ~500 líneas por canal por día sin vivo.
- **Concurrencia:** lo escribe solo el worker; los handlers leen copias.
