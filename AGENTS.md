# AGENTS.md

Guía para agentes de código (Claude Code, Codex, Cursor, etc.) que trabajen en este repo. Para el uso como persona (API key, cómo correrlo, cron), ver [README.md](README.md).

## Qué es

`yt-monitor` (módulo `github.com/alphamedia/yt-monitor`): bot de **solo lectura** que monitorea comentarios y chat en vivo de canales de YouTube con una API key de la YouTube Data API v3. Es el primer bot del piloto de Alphamedia con **Radio Rivadavia** (canal `UCLAj5TzCrNbqe3LosSKHLcQ`, valor por defecto de `-canal`). `-canal` acepta varios canales: el primero es el principal y de los demás solo se siguen los vivos.

Dos modos, mismo binario:

- **Informe puntual** (`go run .`): baja videos recientes y vivos en curso, comentarios de las últimas N horas y opcionalmente N minutos de chat; escribe `informes/informe_<AAAA-MM-DD_HHMM>.md` y `informes/mensajes_<...>.csv` y termina.
- **Continuo con dashboard** (`go run . -dashboard :8080`): un worker consulta YouTube en bucle, persiste en `datos.jsonl` y un servidor HTTP sirve un dashboard con htmx que se refresca solo.

El bot **no publica, no modera y no usa OAuth**. Cualquier cambio que requiera escribir en YouTube (responder, borrar, moderar) es un cambio de alcance: consultarlo antes.

## Stack y restricciones

- Go puro, **solo biblioteca estándar**. `go.mod` no tiene `require` y debe seguir así; no agregar dependencias sin pedirlo.
- `go 1.24.7` en `go.mod` (se usa `min`/`max` built-in, patrones de ruta `GET /{$}` de `net/http` 1.22+, `http.FileServerFS`).
- Templates y JS se embeben con `//go:embed`: el binario es autocontenido. Al agregar archivos en `templates/` o `static/` revisar que las directivas `//go:embed` de `server.go` los cubran (`static/` solo embebe `htmx.min.js`).
- Frontend: `html/template` + htmx 2.0.4 vendorizado en `static/htmx.min.js` (no editarlo; no cargar nada de CDNs). CSS inline en `templates/page.html`, sin frameworks ni JS propio.
- Plataformas objetivo: Linux y **Windows** (se compila `yt-monitor.exe` para una PC de la oficina). Por eso `main.go` importa `time/tzdata`; no sacarlo.

## Mapa del código

Todo está en el paquete `main`, en la raíz:

| Archivo | Responsabilidad |
|---|---|
| `main.go` | Flags, carga de `.env` (`loadDotEnv`), modo informe (`report`, `writeCSV`), columnas del CSV (`csvHeader`, `csvRow`, `programOf`), arranque del modo dashboard. |
| `youtube.go` | Cliente mínimo de la API (`YouTube`): `UploadsPlaylist` (ID o @handle), `RecentVideoIDs`, `ChannelVideos`, `Videos`, `Comments`, `LiveChatPage`, `LiveChats`, `StreamChat` (chat en tiempo real). Tipos `Video`, `Message` y `ChatBatch`. Helpers `TagProgram`, `ProgramName`, `ParseVideoID`, `ParseChannel`. Cuenta cuota en `YouTube.Units`. |
| `classify.go` | `Classify` por reglas (listas de palabras y regex), `TopWords`, `TopAuthors`, `TopPrograms`, stopwords en español. |
| `analyze.go` | `Analyze`: los 5 análisis por vivo del dashboard (temas con `TopTopics`, preguntas, quejas técnicas con `TechIssue`, spam e insultos repetidos, filtros para YouTube Studio). `Participation` (franjas de 5 min de las últimas 24 h con mensajes, personas por `Author`, espectadores y %) y `PeakMoments` (momentos pico para reels: al menos `minPersonasPico` personas y `factorPico` veces la mediana; los `maxMomentos` más fuertes). Solo reglas. |
| `worker.go` | `RunWorker`: bucle infinito (tick de 1 s) que agenda videos / comentarios / chat y respeta el tope de cuota. Abre una goroutine de streaming por vivo (`StreamChat`) que le manda `chatEvent` por un canal; si el streaming no anda, lee el chat por turnos como antes. `nextChat` elige a qué vivo le toca la próxima conexión o lectura. |
| `store.go` | `Store`: estado en memoria con `sync.RWMutex`, deduplicación por `Message.ID`, persistencia JSONL, retención de 30 días, contador de cuota diario (persistido en `datos.cuota.json`), serie de espectadores por vivo (`ViewerSample`, `AddViewers`, `Viewers`; persistida en `datos.espectadores.jsonl`). |
| `server.go` | Rutas HTTP, parciales htmx, gráficos de actividad y de participación (SVG armado en Go, `participationChart`), `reelLink` (cómo ubicar un momento en YouTube según `dvrWindow`), export CSV, helper `miles`. |
| `templates/page.html` | Página principal + CSS (tokens de color claro/oscuro). |
| `templates/partials.html` | Bloques `resumen`, `feed`, `extra` que pide htmx. |
| `monitor_test.go` | Tests: clasificador, flujo completo contra una API falsa (`httptest`), dedup y retención del store. |

Rutas del dashboard: `GET /` (`?vivo=ID` elige el vivo), `GET /parcial/resumen` (cada 5 s), `GET /parcial/vivo` (sección del vivo elegido, se pide al cargar y al tocar un botón), `GET /parcial/vivo/chat` (cada 2 s; usa `Store.Status` y `Store.LastChat` para no copiar todo el histórico), `GET /parcial/vivo/analisis` (cada 30 s), `GET /parcial/feed` (cada 5 s, filtros `fuente`, `cat`, `q`), `GET /parcial/extra` (cada 30 s), `GET /export.csv`, `GET /static/`.

Botones de vivos: elegir un vivo **solo cambia lo que se ve**; el worker sigue leyendo todos los chats por turnos (lo pidió así el usuario para no perder mensajes ni datos de los análisis). No conectar la selección con el worker sin consultarlo. Si el vivo pedido ya no está en curso, `/parcial/vivo/chat` responde la sección entera con `HX-Retarget: #vivo` para pasar al primero en vivo. Las etiquetas salen de `LiveLabel`.

## Comandos

```bash
go build ./...        # compilar
go vet ./...          # análisis estático
gofmt -l .            # debe no listar nada
go test ./...         # tests (no tocan la red ni necesitan API key)
go run . -h           # ver flags
```

Compilar para Windows: `GOOS=windows GOARCH=amd64 go build -o yt-monitor.exe .`

Para probar contra una API simulada sin gastar cuota: `YT_BASE_URL=http://localhost:9999 YT_API_KEY=x go run . ...` (`YT_BASE_URL` existe solo para pruebas).

**No correr `go run .` contra la API real sin que el usuario lo pida**: gasta cuota diaria compartida y escribe en `informes/`. El modo `-dashboard` además escribe en `datos.jsonl` y queda corriendo para siempre.

## Antes de dar algo por terminado

1. `gofmt -l .` vacío, `go vet ./...` limpio y `go test ./...` en verde.
2. Si se tocó el clasificador, agregar casos a `TestClassify`.
3. Si se tocó el cliente o se agregó un endpoint, extender el servidor falso de `TestFlowAgainstFakeAPI` y, si cambia el costo, el chequeo de `yt.Units`.
4. Si se tocó el dashboard, verificar en el navegador con datos (el `datos.jsonl` local sirve) en modo claro y oscuro y en ancho de celular.
5. Si cambian flags, comportamiento visible o cuota, actualizar `README.md`.

## Reglas del dominio (importantes)

### Cuota de la API

- 10.000 unidades por día por proyecto de Google Cloud; se reinicia a medianoche **del Pacífico** (`store.go: pacific`, `rollDay`). En Argentina eso es 04:00 o 05:00.
- Costos usados en el código (pasados a `get` como `cost`): `channels` 1, `playlistItems` 1, `videos` 1 cada 50 IDs, `commentThreads` 1 por página, `liveChat/messages` 5 por lectura, `liveChat/messages/stream` 5 por conexión (la cuenta `StreamChat` con `ChatBatch.Units`, no con `get`). Cada revisión de videos del worker gasta 3 (subidas + vivos + `videos`) más 1 por cada canal extra (solo su lista de vivos). Si se agrega un endpoint, poner su costo real según la documentación de YouTube (por ejemplo, `search.list` cuesta **100**: evitarlo).
- `YouTube.Units` se incrementa solo cuando hubo respuesta HTTP. En el worker, todo llamado a la API debe ir envuelto en `spend(...)` para que se sume a `Store.AddUnits`. La excepción es `StreamChat`, que corre en otra goroutine: no toca `Units` y avisa la cuota en el primer `ChatBatch` (5 al haber respuesta HTTP), que el worker suma al procesarlo.
- El worker deja de leer el chat al llegar a `Budget - reserveForComments` (1.500 de reserva) y deja de consultar todo al llegar a `Budget` (`-cuota`, 9.000 por defecto). El chat se lee cada `-chat-seg-pico` (60 s) dentro de `-pico` (7-20, hora local) y cada `-chat-seg` (180 s) fuera (`WorkerConfig.chatInterval`). Con el vivo de 24 horas entra en el día: ~1.440 de videos + ~1.000 de comentarios + ~5.000 de chat (3.900 + 1.100). Cada canal extra suma ~480 de videos; el chat cuesta lo mismo con uno o varios vivos porque se leen por turnos. Si se pasa, el chat se pausa al final del día del Pacífico, que en Argentina es la madrugada.
- Los comentarios solo se piden para videos cuyo `commentCount` cambió (`knownComments`). Mantener ese tipo de ahorro en cualquier cambio.
- Si un cambio altera el consumo, recalcular y actualizar las cifras de cuota del README.

### Vivos y programas

- Radio Rivadavia tiene **un solo vivo de 24 horas** que empezó hace días y queda fuera de los últimos `-videos` subidos. `ChannelVideos` lo encuentra con la lista automática de vivos del canal: el ID de la lista de subidas con `UU` cambiado por `UULV`. No está documentada; si falla, se sigue sin ella, y `-vivos` permite agregar videos a mano.
- No usar `search.list` con `eventType=live` para detectar vivos: cuesta 100 unidades.
- El título del vivo cambia con cada programa. Los mensajes de **chat** guardan el programa en `Message.Program` al recibirse (`TagProgram`, con `ProgramName`, que saca el canal del título: adelante como en Rivadavia, "… en VIVO | PROGRAMA", o atrás como en Rock & Pop, "🔴 [EN VIVO] PROGRAMA | ROCK & POP", comparando con `Video.Channel`; un "| EN VIVO" final, como en Luzu TV, se descarta). Si se suma un canal con otro formato, agregar el caso a `TestProgramName`. Los comentarios no tienen programa. `programOf` cae al título actual del vivo para mensajes de chat guardados antes de que existiera el campo.
- En el dashboard el chat llega **en tiempo real** por streaming (`StreamChat`): una conexión abierta por vivo, 5 unidades cada vez que se abre. Lo pidió el usuario para ver los mensajes sin demora. El intervalo (`chatInterval`) es el mínimo entre dos conexiones o lecturas cualesquiera, así que si YouTube corta seguido las conexiones el gasto no supera al de las lecturas por turnos. Si el streaming de un chat da 403 (que no sea de cuota) o 404, se marca `noStream` y ese chat vuelve a leerse por turnos con `LiveChatPage`; si ahí también da 403, queda `blocked`.
- Las lecturas comunes (`LiveChatPage`) van **por turnos** (`nextChat`: el que hace más tiempo que no se lee). `LiveChats` hace lo mismo en el modo informe. No hacer lecturas comunes en paralelo: duplica la cuota y el cliente no es concurrente.
- De los canales extra solo se guardan los vivos en curso o programados (`ChannelVideos` descarta el resto) para no gastar cuota en comentarios de sus videos viejos.

### Chat en vivo

- Respetar `pollingIntervalMillis` que devuelve YouTube (el worker usa `max(wait, cfg.ChatEvery)`; `LiveChat` usa al menos 10 s).
- Puede que YouTube rechace leer el chat con API key sola (403/401). En ese caso el worker marca `blocked` en el `chatState` de ese vivo y no insiste; si el error menciona `quota`, sí reintenta. No convertir eso en reintentos agresivos.
- El token de página del chat es por vivo (`chats`, un `chatState` por ID); cuando un vivo termina se cancela su streaming y se olvida su estado. El streaming se retoma con el último `nextPageToken` recibido, así que no se pierden mensajes al reconectar. Si no llega nada en `streamIdle` (5 min), `StreamChat` corta la conexión para reabrirla.

### Datos y privacidad

- Políticas de YouTube: **no guardar datos de la API más de 30 días** (`retention` en `store.go`). `OpenStore` descarta lo vencido y reescribe el archivo. No alargar esa retención ni agregar otros almacenamientos que la salteen.
- `datos.espectadores.jsonl` guarda cuántos miraban cada vivo en cada revisión de videos (`ViewerSample`). Son datos de la API: misma retención de 30 días al abrir (`loadViewers`, tmp + `os.Rename`) y nunca dentro de `datos.jsonl`, para no cambiar el formato de `Message`. Está en `.gitignore` (`datos.*.jsonl`).
- `datos.jsonl` e `informes/` contienen nombres de usuarios y mensajes reales: son datos generados, no código. No editarlos a mano, no usarlos como fixtures de tests (los tests arman sus propios datos) y no subirlos a ningún lado.
- `.env` tiene `YT_API_KEY`. **No leer, imprimir ni commitear su contenido.** La clave nunca debe aparecer en logs, errores ni en el dashboard: `YouTube.get` la reemplaza por `***` en errores de red; mantener ese cuidado en cualquier código nuevo que formatee URLs o errores.
- El dashboard no tiene autenticación. Por defecto escucha en `localhost`; no cambiar ese default ni sugerir exponerlo a internet.

### Formato de `Message` y CSV

- `datos.jsonl`: un `Message` por línea, con los nombres de campo de Go (`ID`, `Source`, `VideoID`, `Author`, `Text`, `At`, `Likes`, `Replies`, `Program`). Cambiar nombres o tipos de campos rompe la carga de datos ya guardados; si hace falta, que sea compatible hacia atrás.
- `Source` vale `"comentario"` o `"chat"` (en español; el filtro del dashboard y los informes dependen de esos valores).
- La categoría **no se guarda**: se calcula con `Classify` cada vez que se lee. Cambiar el clasificador re-clasifica todo el histórico, lo cual es intencional.
- El CSV (informe y `GET /export.csv`) se arma con `csvHeader` y `csvRow` en `main.go`: `fuente, fecha, video, programa, autor, categoria, likes, respuestas, texto`. Está pensado para subirlo a Claude.

### Clasificador

- Orden de prioridad en `Classify`: spam (links, teléfonos, palabras de spam) > insulto > pregunta > queja > elogio > mayúsculas sostenidas (insulto) > otro. El orden importa: un mensaje con link y signo de pregunta es spam.
- Es un primer filtro barato y deliberadamente simple; el análisis fino lo hace Claude sobre el CSV. Lo mismo vale para `analyze.go`: las frases de `techIssues` se buscan como frases enteras sin tildes (`phraseText`); no agregar palabras sueltas que en una radio de noticias suelen ser tema ("audio", "corte", "retraso"). Cubrir los casos en `TestTechIssue` y `TestAnalyze`. No meter llamadas a LLMs ni dependencias de NLP acá sin pedirlo.
- Las listas comparan contra el texto en minúsculas con `strings.Contains`, así que una palabra corta puede matchear dentro de otra (por ejemplo `"lag"`). Al agregar términos, pensar en falsos positivos en español rioplatense y cubrirlos en `TestClassify`.
- Las constantes `Cat*` son también las claves de CSS (`.badge.spam`, etc.) y los valores del filtro `cat`. Si se agrega una categoría, sumarla a `categories` y `catLabels` en `server.go`, al bucle de `report` en `main.go` y, si corresponde, al CSS.

## Concurrencia

- Hay una sola goroutine que hace las llamadas comunes a YouTube (`RunWorker`); el cliente `YouTube` **no es seguro para uso concurrente** (`Units` sin lock). No llamarlo desde handlers HTTP. Las goroutines de streaming solo usan `StreamChat`, que lee `Key`, `BaseURL` y `Stream` sin modificarlos, y no tocan `chats` ni `lives`: todo lo que reciben se lo pasan al worker por el canal `events`.
- Los handlers solo leen el estado vía `Store.Snapshot()`, `Store.Status()`, `Store.LastChat()` o `Store.Viewers()`, que devuelven copias. No exponer los slices internos del `Store` ni modificar el `Store` desde el servidor.
- `Store.Add` agrega al JSONL en modo append; la compactación (`rewrite`) solo ocurre al abrir. Si se agrega limpieza periódica, hacerla bajo el lock y con el patrón archivo temporal + `os.Rename`.

## Estilo

- **Todo en español**: comentarios, mensajes de log, errores al usuario, textos del dashboard, nombres de flags (`-horas`, `-cuota`) y de categorías. Identificadores de Go en inglés como hasta ahora (`Store`, `Classify`, `RunWorker`). Español rioplatense con voseo en textos para el usuario ("Abrí", "ponela").
- Código corto y directo, como el existente: funciones chicas, structs anónimos para decodificar JSON de la API, `map[string]any` para datos de templates, comentarios breves que explican el *por qué* (cuota, políticas de YouTube), no el *qué*.
- Errores: en el modo informe, los fatales usan `check(err)`; los recuperables (video con comentarios desactivados, chat rechazado) se loguean como `aviso:` y se sigue. En el worker, nunca terminar el proceso: loguear, guardar en `SetError`/`SetChatNotes` y reintentar más tarde.
- Fechas visibles en hora local (`t.Local()`), formato `2006-01-02 15:04` en CSV/informes y `02/01 15:04` en el dashboard. Números con separador de miles `.` vía `miles`.
- Dashboard: usar los tokens CSS existentes (`--surface-*`, `--text-*`, `--series-1`, etc.) y definir cualquier color nuevo en ambos temas. Mantener los atributos de accesibilidad (`aria-label`, `role="img"`, `<title>` en barras). `html/template` ya escapa: no usar `template.HTML` con texto de usuarios.

## Fuera de alcance sin consultar

- Escribir en YouTube o pedir OAuth (respuestas, moderación, borrado de mensajes).
- Agregar base de datos, dependencias externas, Docker o un frontend con build.
- Autenticación del dashboard o exponerlo fuera de la red local.
- Integraciones del piloto aún no decididas (Nightbot, borradores de respuesta con aprobación): ver "Próximos pasos del piloto" en el README.
