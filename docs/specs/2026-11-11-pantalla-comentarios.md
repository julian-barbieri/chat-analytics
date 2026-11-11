# Spec: pantalla Comentarios

- **Estado:** borrador
- **Tipo:** mejora
- **Rama:** `feat/pantalla-comentarios`
- **Origen:** pedido del usuario (2026-11-11); diseño de referencia en https://claude.ai/artifact/SkDNihHmNVFNcyQjDtwM3t. Decisión: cubrir las subidas de **todos** los canales configurados.
- **Aprobada por / fecha:** —

## Contexto

Los comentarios de videos ya se guardan (`Source: "comentario"`), pero solo se muestran mezclados con el chat. Hoy:

- Solo se bajan los de las últimas `-videos` (15) subidas del **canal principal**: `ChannelVideos` (`youtube.go:163`) pide `RecentVideoIDs` solo de `channels[0]` y de los demás canales descarta todo lo que no sea vivo (`youtube.go:205`).
- La lista de videos vive solo en memoria (`Store.SetVideos`, `store.go:436`) y se pisa en cada revisión: un video que sale de las últimas 15 pierde título, canal, vistas y fecha, aunque sus comentarios sigan en `datos.jsonl` por 30 días.
- La cuota ya está justa: con un canal extra que abre un vivo por programa el README calcula ~8.160 por día, por encima de las 7.500 hasta las que se lee el chat.

## Objetivo

Una cuarta pantalla, **Comentarios** (`GET /comentarios`, entre Programas y Canales), que analiza con reglas los comentarios de los videos grabados de todos los canales configurados. Las subidas de los canales extra se revisan con poca frecuencia, para que el chat no pierda cuota.

## Criterios de aceptación

- [ ] CA1: `ChannelVideos` devuelve también las últimas `-videos` subidas de cada canal extra cuando se le pide (`uploadsAll`). Con canales extra en la revisión normal sigue el comportamiento de hoy. — `TestChannelVideosExtraUploads`
- [ ] CA2: el worker revisa las subidas de los canales extra cada `extraUploadsEvery` (30 min) y no en cada revisión de 3 min. Los comentarios de esos videos se piden solo si cambió `commentCount` (`knownComments`). — `TestWorkerExtraUploadsEvery` (cuenta pedidos a `playlistItems` contra la API falsa)
- [ ] CA3: `datos.videos.jsonl` guarda el último dato visto de cada video (ID, título, canal, publicación, vistas, comentarios, si fue vivo). Retención de 30 días desde la última vez que se vio, tmp + `os.Rename` al abrir, nunca dentro de `datos.jsonl`. — `TestVideoArchiveRetention`
- [ ] CA4: `CommentsSummary` (función pura, `comments.go`) da, para un canal (o todos), un período (7 d, 30 d, desde la publicación) y un tipo (grabados / vivos terminados): comentarios, personas distintas, videos con comentarios, preguntas con `Replies == 0`, % de quejas, comentarios por día con los días de publicación, categorías por `Classify`, `TopWords`, `TopAuthors`, filas de videos (vistas, comentarios guardados, del período, c/1.000 vistas, preguntas) y destacados (más likes, más respuestas, quejas). — `TestCommentsSummary` (tabla de casos)
- [ ] CA5: `CommentsVideo` arma el detalle: curva por franja de horas desde la publicación con la mediana del canal, categorías y lista con `buscar`, `cat`, `orden` (`likes|respuestas|recientes|viejos`), `sinresp` y `pag` (de a 50), con listas cerradas para todos los valores. — `TestCommentsVideo`
- [ ] CA6: la pestaña aparece en las cuatro pantallas en el orden En vivo, Programas, Comentarios, Canales. `GET /comentarios` y `GET /comentarios?video=ID` responden 200 con datos y con el store vacío, y un parámetro fuera de la lista no se refleja en el HTML. — `TestCommentsPage`
- [ ] CA7: `GET /export.csv?video=ID` baja solo los comentarios de ese video con `csvHeader`. — `TestExportVideo`
- [ ] CA8: `EstimateQuota` y la barra de Canales suman las subidas de los canales extra (48 por canal por día) y una estimación de sus comentarios. — `TestEstimateQuota` actualizado
- [ ] CA9: `tools/preview` copia `datos.videos.jsonl`. — test de `tools/preview`
- [ ] CA10: README (cuota y pantalla) y `AGENTS.md` (mapa, rutas y datos) actualizados. Verificado en el navegador con la vista previa: claro, oscuro y ancho de celular.
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

- `youtube.go`: un parámetro en `ChannelVideos` para incluir las subidas de los canales extra (1 unidad por canal + `videos` de a 50).
- `worker.go`: `nextExtraUploads`. En la revisión de las 3 min se pasa `uploadsAll` solo cuando venció. Entre medio se conservan los grabados extra de la última vez (los que no son vivo), para que la lista no los pierda.
- `store.go`: `VideoArchive` en `datos.videos.jsonl` con `AddVideos` (al revisar) y `Videos()` (copia), y `readJSONL` / `loadJSONL` como `datos.vivos.jsonl`.
- `comments.go` (nuevo): `CommentsSummary` y `CommentsVideo`, solo funciones puras sobre los mensajes con `Source == "comentario"`. "Vivo terminado" = `StartedAt` no es cero.
- `server.go` / `templates/comentarios.html`: la pantalla con parciales htmx (`/parcial/comentarios`, `/parcial/comentarios/video`), refresco de 60 s, filtros como links con `aria-pressed` y el CSS existente (`.card`, `.tiles`, `.cstack`, `.crow`, `.tablewrap`, `.feed`). `layout.html` suma la pestaña y el título.
- Sin JS propio y sin colores nuevos fuera de los tokens.

## Fuera de alcance

- Tono / sentimiento y resumen de Claude de los comentarios (las tarjetas con **IA** del diseño): otra tarea.
- Shorts (necesita la duración del video) y saber si **el canal** respondió (`comments.list`, más cuota).
- Respuestas a comentarios: solo se guardan los comentarios principales del hilo.
- Bajar comentarios de más de 48 h hacia atrás al agregar un canal.

## Riesgos

- **Cuota:** por cada canal extra se suman ~48 de `playlistItems`, hasta ~48 de `videos` si se pasa de 50 IDs, y los comentarios (1 por página, solo videos con contador nuevo; estimado 100–300 por canal por día). Con el ejemplo del README (8.160) el chat se pausa todavía antes. La barra de Canales lo tiene que mostrar antes de guardar. Se mitiga con el intervalo de 30 min y `knownComments`. Recalcular las cifras del README.
- **Privacidad:** `datos.videos.jsonl` son datos de la API y llevan retención de 30 días, `.gitignore` (`datos.*.jsonl`) y nunca van en tests. Los autores se muestran como hoy en el resto del dashboard; no se manda nada afuera.
- **Rendimiento:** la pantalla recorre todos los mensajes de 30 días. Se filtran los comentarios una vez por pedido; el chat no se toca y no se usa `Snapshot` en el refresco (se agrega `Store.Comments()`, una copia solo de los comentarios).
- **Compatibilidad:** `Message` y `datos.jsonl` no cambian. Si `datos.videos.jsonl` no existe, la pantalla funciona con los videos de la lista actual.
