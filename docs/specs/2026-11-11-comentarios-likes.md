# Spec: Comentarios y likes (shorts, likes guardados, videos sin nombre)

- **Estado:** implementada
- **Tipo:** mejora
- **Rama:** `feat/comentarios-likes`
- **Origen:** pedido del usuario y diseño https://claude.ai/artifact/YQMeAw32tiseUT5pJpMRoE
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-11

## Contexto

- «Desde la publicación» (`comments.go:43`) cuenta todos los comentarios guardados de los
  videos publicados en los últimos 30 días (`comments.go:264`, `comments.go:303`); el
  nombre confunde.
- El filtro de tipo es Grabados / Vivos / Todos (`comments.go:44`). Los vivos no tienen
  comentarios en estos canales y no hay forma de separar shorts de videos.
- Filas sin nombre ni datos: comentarios de videos que no están en el archivo
  (`comments.go:239`, `comments.go:418`), porque se guardaron antes de que existiera
  `datos.videos.jsonl` o el video salió de las últimas `-videos` subidas.
- Los likes no se guardan: `likeCount` ya viene en `statistics` (`youtube.go:283`) pero
  `videos` no lo decodifica (`youtube.go:272`).
- Las publicaciones de Comunidad no están en la YouTube Data API v3 (ni con OAuth).

## Objetivo

Comentarios pasa a llamarse «Comentarios y likes»: separa videos de shorts, no tiene filas
sin datos y muestra likes guardados; Programas muestra los likes del vivo por programa.

## Criterios de aceptación

- [x] CA1: el período `pub` se llama «Publicados en 30 días» y la pantalla dice desde qué
  fecha («publicados desde el 13/10…») — `TestCommentsPage` (texto y fecha).
- [x] CA2: `tipo=todos|videos|shorts` (por defecto `todos`); `tipo=vivos` o `grabados`
  cae al defecto. Los vivos no entran en ninguna opción. — `TestCommentsQuery`, `TestCommentsSummary`.
- [x] CA3: un video está en `UUSH…` del canal ⇒ `Short: true`. La lista de un canal se
  pide como mucho cada `shortsEvery` (30 min; la primera, `shortsFirst` después de
  arrancar) y solo si tiene videos sin clasificar, 1 unidad con `spend`; si falla, se
  conserva lo último. — `TestVideosLikesAndShorts`, `TestWorkerShortsAndMissing`,
  `TestReviewShortsOnlyNew`.
- [x] CA4: la pantalla avisa que las publicaciones de Comunidad no se leen con la API. —
  `TestCommentsPage`.
- [x] CA5: los IDs con comentarios guardados que no están en el archivo se piden una sola
  vez con `Videos` (1 unidad cada 50, con `spend`) y entran al archivo; uno que YouTube no
  devuelve no se vuelve a pedir hasta reiniciar. — test de worker con API falsa.
- [x] CA6: `Video.Likes` y `VideoRecord.Likes` desde `likeCount`, sin cambiar el costo. —
  `TestFlowAgainstFakeAPI`.
- [x] CA7: `datos.likes.jsonl` guarda `LikeSample{VideoID, At, Likes}`: en vivos, en cada
  revisión si cambió; en videos y shorts, si cambió y pasaron 30 min desde la última.
  Retención de 30 días al abrir (tmp + `os.Rename`). — `TestLikesStore` (dedup, intervalo,
  retención, reapertura).
- [x] CA8: resumen con tarjetas Likes nuevos y Likes c/1.000 vistas, puntos de likes por
  día en el gráfico, tarjeta «Videos y shorts» y columnas Likes / Likes del período /
  Likes c/1.000 en la tabla, con órdenes `likes` y `likesratio`. «Likes del período» =
  último valor − último valor antes del inicio (o 0 si se publicó dentro). —
  `TestCommentsSummary` (tabla de casos).
- [x] CA9: detalle de un video con tarjeta Likes y curva de likes acumulados desde la
  publicación junto a la de comentarios. — `TestCommentsVideo`.
- [x] CA10: Programas: por tramo, likes ganados = última muestra del tramo − última
  muestra hasta su inicio (un vivo guarda una muestra solo cuando el contador cambia; sin
  ninguna antes, la primera del tramo; vacío sin muestras dentro del tramo), likes c/1.000
  espectadores promedio y gráfico de likes cada 30 min del día con el color y el nombre
  del programa (el mismo color junto a su nombre en la tabla). — `TestProgramLikes`.
- [x] CA11: README (cuota de shorts y faltantes, `datos.likes.jsonl`), AGENTS.md (mapa,
  archivos de datos) y `tools/preview` copia `datos.likes.jsonl`.
- [x] Gates de `CLAUDE.md` → "Definición de terminado" en verde y navegador en claro,
  oscuro y celular.

## Diseño propuesto

- `youtube.go`: `likeCount` → `Video.Likes`; `ChannelShorts(uploads)` con
  `RecentVideoIDs("UUSH"+…, 50)`.
- `worker.go`: con `nextExtra` (cada 30 min), shorts de cada canal (`shortIDs` en memoria)
  y faltantes (`Store.MissingVideos()`, `asked` en memoria). `Short` se marca antes de
  `AddVideos`.
- `store.go`: `VideoRecord.Short`, `.Likes`; `AddLikes`, `Likes(id, from)` y `loadLikes`
  como la serie de espectadores; `MissingVideos` (copia).
- `comments.go` / `commentspage.go` / `templates/comentarios.html`: tipos y períodos
  nuevos, likes en vista y gráficos (`dayChart` suma una serie de puntos con su escala).
- `programs.go` / `templates/programas.html`: columna y gráfico de likes por tramo.
- Pestaña «Comentarios y likes» en `templates/layout.html`; la ruta sigue `/comentarios`.

## Fuera de alcance

Publicaciones de Comunidad (no hay API; scrapear no), likes de comentarios en el tiempo
(ya están en `Message.Likes`), dislikes, `search.list`, detectar shorts por duración.

## Riesgos

- **Cuota:** UUSH a lo sumo 48/día por canal, en general unas pocas (solo con videos
  nuevos; la barra de Canales no lo suma); faltantes 1 cada 50, una vez. Likes: 0. Actualizar
  README. `UUSH` no está documentada: si falla, todos quedan como «Video».
- **Datos:** `datos.likes.jsonl` es de la API: 30 días, `.gitignore` (`datos.*.jsonl`),
  nunca dentro de `datos.jsonl`. Campos nuevos con `omitempty` en `VideoRecord`.
- **Tamaño:** ~1 línea cada 3 min por vivo + ≤ 1 cada 30 min por video que cambia.
- **Revisión 1** (`/revisar`): se corrigieron los 11 hallazgos; CA10 cambió de «primera
  del tramo» a «última hasta el inicio» por cómo se guardan las muestras de un vivo.
- **Alcance:** es grande y va en un solo PR (lo decidió la persona). Para que se pueda
  revisar, un commit por parte: datos (CA3, CA5–CA7), Comentarios (CA1, CA2, CA4, CA8,
  CA9) y Programas (CA10).
