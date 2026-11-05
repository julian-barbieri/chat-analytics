# Spec: canales en continuo (vista por canal, intervalo de chat por canal y canales editables desde el dashboard)

- **Estado:** borrador
- **Tipo:** mejora
- **Rama:** `feat/canales-continuos` (si se aprueba en dos etapas: `feat/canales-continuos`
  para la etapa 1 y `feat/canales-editables` para la 2, cada una con su PR)
- **Origen:** pedido de Julian Barbieri (2026-11-05): "Necesito empezar a trabajar con 2
  canales de YouTube que salen en vivo. Yo los ingreso de manera manual dentro de la
  aplicación. Yo les indico cada cuánto tiempo debería actualizar el chat en vivo. Me
  gustaría que no se corte y que continúe analizando el vivo del programa siguiente y que
  no termine cuando finaliza un determinado programa en vivo, ya que cada canal tiene
  varios programas en vivo." Diseño de referencia: canvas con las pantallas 1 ("Canales")
  y 2 ("En vivo por canal"). Es la **parte 1 de 2**: la parte 2 (análisis con Claude) es
  otra tarea.
- **Aprobada por / fecha:** pendiente.

## Contexto

### Qué pasa hoy (verificado leyendo el código)

**Configuración de canales.** Solo por flags, al arrancar. `-canal` (`main.go:45`) se
parte por comas y cada parte pasa por `ParseChannel` (`main.go:79-92`), que es una función
pura sin red (`youtube.go:336-356`). El primero es el principal: de ese se piden las
subidas y su lista de vivos `UULV` (10); de los demás solo la lista `UULV` (5), y de esos
solo quedan los vivos en curso o programados (`youtube.go:148-166`). El intervalo del chat
es **global**: `-chat-seg-pico` (60 s), `-chat-seg` (180 s) y `-pico` (7-20)
(`main.go:52-54`), que el worker usa en `WorkerConfig.chatInterval` (`worker.go:38-46`).
`VideosEvery` está fijo en 3 min (`main.go:138`). Para cambiar algo hay que reiniciar.

**Turnos del chat.** `nextChat` (`worker.go:114-126`) elige, entre los vivos que se turnan,
el que hace más que no se lee y ya cumplió el `wait` que pidió YouTube
(`pollingIntervalMillis`). Entre dos conexiones o lecturas **cualesquiera** pasa como
mínimo `every` (`worker.go:366`, `lastChat` es uno solo para todos los vivos). Por eso hoy
el chat cuesta lo mismo con 1 o con 2 canales, y con 2 vivos cada uno se lee cada 2 min en
el pico y cada 6 min fuera (README:61).

**Qué pasa cuando termina un programa.**

- *Mismo vivo, otro título (Rivadavia, vivo de 24 h).* La revisión de videos (cada 3 min)
  actualiza `lives` con el título nuevo (`worker.go:249-262`). El `chatState` solo se
  rehace si cambió el `LiveChatID` (`worker.go:255`), así que **no se reconecta** y el
  token se conserva. Los mensajes se etiquetan con el programa del título vigente al
  recibirlos (`worker.go:182`, `worker.go:389`), con hasta 3 min de demora (README:63).
  **No se corta nada.**
- *Termina el vivo (Rock & Pop, un vivo por programa).* En la revisión siguiente el vivo
  ya no está en `lives`: se cancela su streaming y se olvida su estado
  (`worker.go:263-271`). Si es de un canal extra, además desaparece de `Store.videos`,
  porque `ChannelVideos` descarta los que no están en vivo ni programados
  (`youtube.go:163-166`).
- *Empieza el siguiente.* Aparece primero en la lista `UULV` del canal y se engancha solo
  en la próxima revisión de videos, **a lo sumo 3 min después** (más lo que tarde YouTube
  en sumarlo a esa lista, que no está documentada: hipótesis, ver Riesgos). Abre su
  `chatState` nuevo y su streaming.

**Conclusión: el worker no se corta; lo que "se corta" es el dashboard**, que está armado
por ID de video:

- Los botones son por vivo (`server.go:152-181` `pickLive`, `templates/partials.html:58`
  "Elegí qué vivo ver") y el link es `/?vivo=ID`.
- Si el vivo elegido terminó, `/parcial/vivo/chat` recarga la sección con **el primer vivo
  en curso de cualquier canal** (`server.go:201-207`): mirando Rock & Pop, al terminar el
  programa la vista salta a Rivadavia. Lo afirma hoy `TestLiveChatSelection`
  (`monitor_test.go:564-568`). Sin ningún vivo, se ve "No hay vivos en este momento"
  (`templates/partials.html:80`).
- El análisis va por un solo video (`server.go:304-338`: `sinceFor(snap.Msgs, id, 24 h)`
  en `server.go:325`; `Participation` filtra por un `videoID`, `analyze.go:266-283`). El
  vivo nuevo **arranca de cero** y el del programa anterior, si era de un canal extra, da
  "Este vivo ya no está en la lista de videos" (`server.go:310-322`,
  `templates/partials.html:197`). Los mensajes siguen en `datos.jsonl`; no se borran, pero
  no se pueden ver.
- No hay noción de canal en `Video`: `Channel` es el nombre (`channelTitle`), no el ID
  (`youtube.go:61-72`, `youtube.go:186-227` no decodifica `snippet.channelId`), y
  `UploadsPlaylist` no devuelve el ID del canal (`youtube.go:76-99`), así que un canal dado
  como `@handle` no se puede asociar a sus videos por ID.

**Reproducción:** no aplica (mejora). El comportamiento de hoy lo fijan tests existentes,
que pasan:

```bash
go test -count=1 -v -run '^(TestLiveChatSelection|TestWorkerStopsAfterLiveEnds|TestNextChat|TestChatInterval|TestWorkerStreamsChat)$' .
```

```
--- PASS: TestNextChat (0.00s)
--- PASS: TestChatInterval (0.00s)
--- PASS: TestLiveChatSelection (0.00s)
--- PASS: TestWorkerStreamsChat (1.03s)
--- PASS: TestWorkerStopsAfterLiveEnds (1.30s)
```

Línea de base en esta rama (Go 1.26.8, `5c5d535`): `go build ./...` OK, `go vet ./...` OK,
`gofmt -l .` vacío, `go test -count=1 ./...` en verde (raíz 8,6 s, `tools/factory-board`
3,4 s, `tools/preview` 19,9 s).

**Causa raíz:** no es un bug. La vista y el análisis se identifican por video
(`server.go:152-207`, `server.go:304-338`) y la configuración es por flags y global
(`main.go:45-54`, `worker.go:38-46`, `worker.go:366`).

## Objetivo

Que cada canal configurado se vea y se analice como una unidad continua: el botón es del
canal, el chat y el análisis siguen solos al programa siguiente (sea el mismo vivo con
otro título o un vivo nuevo) sin perder lo anterior del día; y que la persona agregue,
pause y quite canales y elija cada cuánto se lee el chat de cada uno desde el dashboard,
sin reiniciar, viendo antes cuánta cuota diaria va a gastar.

## Propuesta de etapas

El alcance no entra bien en un solo PR. Propongo dos, en este orden:

- **Etapa 1 — continuidad por canal (recomendada primero).** `Video.ChannelID`, serie de
  vivos por canal en `datos.vivos.jsonl`, búsqueda rápida del próximo vivo, botones por
  canal, cartel de continuidad, tira "Hoy en el canal" y selector "Programa en curso /
  Todo el día del canal". Los canales siguen saliendo de `-canal`. **No agrega ninguna
  ruta que escriba.**
- **Etapa 2 — canales editables.** `datos.canales.json`, intervalo de chat por canal,
  pausar / quitar / agregar desde la pantalla "Canales", estimación de cuota y las guardas
  de los `POST`.

Por qué la 1 primero: arregla lo que hoy "se corta" (la vista), no abre superficie de
escritura, y deja la base que necesita la 2 (el ID de canal de cada video y las horas en
vivo observadas para estimar la cuota). Mientras tanto los dos canales ya se pueden seguir
con `-canal UCLAj5TzCrNbqe3LosSKHLcQ,@<rock-and-pop>`. Si la persona prefiere al revés, la
2 funciona sola con la estimación en "sin historial: 24 h" hasta que exista la 1.

## Criterios de aceptación

Tests nuevos en `monitor_test.go`, escritos **antes** del código. Para cada etapa, el
primero de la lista debe fallar hoy en ejecución (no solo por compilación): guardar esa
salida para el PR.

### Etapa 1 — continuidad por canal

- [ ] CA1.1: `videos` decodifica `snippet.channelId` en `Video.ChannelID` y
  `UploadsPlaylist` (o una función nueva que la reemplace) devuelve también el ID del
  canal (`items[0].id`), también para `@handle`. El costo no cambia — se verifica con
  `TestFlowAgainstFakeAPI` extendido (el falso manda `channelId` e `id`; `yt.Units` sigue
  siendo `2+1+2+1+1+2*5`).
- [ ] CA1.2 (falla hoy): con dos canales, mirando el canal B cuyo vivo terminó y empezó otro
  vivo del mismo canal, `/parcial/vivo/chat?canal=<B>` muestra el vivo nuevo **sin**
  `HX-Retarget` a un vivo del canal A, y el feed incluye mensajes de los dos vivos de B y
  ninguno de A. Si B no tiene vivo, responde el estado "Esperando el próximo vivo" del
  canal B (sin saltar a A). `?vivo=ID` sigue andando: elige el canal de ese vivo — se
  verifica con `TestLiveChatByChannel` (y se actualiza `TestLiveChatSelection`, que hoy
  afirma el salto).
- [ ] CA1.3: el worker agrega una `LiveSample{At, VideoID, ChannelID, Channel, Title}` por
  vivo en curso en cada revisión de videos (y en cada búsqueda rápida) a
  `datos.vivos.jsonl`, nunca a `datos.jsonl`, sin pedidos extra a YouTube — se verifica con
  `TestWorkerRecordsLives` (cuenta pedidos del falso y lee el archivo).
- [ ] CA1.4: al abrir el `Store`, `datos.vivos.jsonl` descarta lo de más de 30 días, las
  líneas ilegibles y las demasiado largas, y se reescribe con temporal + `os.Rename`; si no
  existe, se arranca vacío — se verifica con `TestStoreLivesOpen` (tabla, como
  `TestStoreViewersOpen`).
- [ ] CA1.5: `ProgramSegments` (función pura) arma los programas del día de un canal a
  partir de las muestras: en un mismo vivo, un tramo nuevo cuando cambia
  `ProgramName(Title, Channel)`; un tramo nuevo por cada vivo; muestras repetidas (por
  ejemplo, tras reiniciar) se juntan; un hueco mayor que `segmentGap` (2 revisiones + 1 min)
  corta el tramo y deja un "sin vivo" — se verifica con `TestProgramSegments` (casos
  Rivadavia, Rock & Pop, reinicio, hueco, dos vivos del mismo canal a la vez, día local que
  empieza a las 00:00 con `-zona`).
- [ ] CA1.6: cuando un canal que tenía vivo se queda sin ninguno, el worker revisa **solo
  ese canal** (`UULV` + `videos`, 2 unidades sumadas con `spend`) cada `fastEvery` (1 min)
  durante `fastFor` (20 min) o hasta encontrar un vivo nuevo; al encontrarlo abre su chat
  sin esperar la revisión de 3 min. Pasados los 20 min no hay más revisiones rápidas. Con
  el tope de cuota (`Budget`) alcanzado no hay revisiones rápidas — se verifica con
  `TestWorkerFindsNextLive` (variables achicadas, falso que cuenta pedidos por
  `playlistId`, `st.Units()` con el delta esperado).
- [ ] CA1.7: cartel de continuidad (función pura sobre los tramos): mismo vivo con otro
  programa → "A las HH:MM cambió el programa y el chat siguió sin cortes"; vivo nuevo →
  "A las HH:MM empezó un vivo nuevo y el monitor lo enganchó solo. El vivo de <programa>
  terminó a las HH:MM." Solo aparece si el cambio fue hoy — se verifica con
  `TestContinuityBanner`.
- [ ] CA1.8: la tira "Hoy en el canal" muestra un bloque por tramo con estado "terminó",
  "al aire" o "sin vivo", con `aria-label` y `<title>` por bloque; el bloque actual sin
  vivo dice "buscando cada 1 min hasta HH:MM" o "buscando cada 3 min" — se verifica con
  `TestChannelDayStrip` (HTML del parcial).
- [ ] CA1.9: el selector "Programa en curso / Todo el día del canal" filtra todo el
  análisis de reglas (temas, preguntas, quejas técnicas, filtros, momentos para reels,
  Super Chats) y los números de la cabecera. "Todo el día" = mensajes de los vivos del
  canal desde las 00:00 locales; "Programa en curso" = mensajes del vivo actual con el
  programa actual desde el inicio del tramo menos 5 min. No entran mensajes de otro canal.
  La elección se conserva en los refrescos (va en el `hx-get`, como `horas`) — se verifica
  con `TestLiveAnalysisByChannel`.
- [ ] CA1.10: `Participation` acepta un conjunto de vivos; en una franja con dos vivos del
  canal, los espectadores son la suma de los promedios de cada vivo. Cada momento del top
  sabe de qué vivo es (el de más mensajes en la franja) y `reelLink` usa ese vivo; si ya no
  está en `Store.videos`, linkea el video sin `t=` y la pista dice la hora local — se
  verifica con `TestParticipationChannel` y casos nuevos en `TestReelLink`.
- [ ] CA1.11: vivos que no son de un canal de `-canal` (los de `-vivos`) siguen con un
  botón por vivo, como hoy — se verifica con un caso de `TestLiveChatByChannel`.
- [ ] CA1.12: el formato de `datos.jsonl` no cambia: `git diff main -- youtube.go` no toca
  el struct `Message`, y `TestStoreSuper` / `TestStoreDedupAndRetention` siguen en verde.
- [ ] CA1.13: README (sección 3: botones por canal, continuidad, tira, selector, búsqueda
  rápida y su cuota) y AGENTS.md (mapa, rutas, reglas nuevas de abajo) actualizados — se
  verifica con `git diff --stat main` y `grep -n "datos.vivos.jsonl" README.md AGENTS.md`.
- [ ] CA1.14: revisado en el navegador con la vista previa (`go run ./tools/preview`, paso
  7b) en claro, oscuro y ancho de celular (AGENTS.md, "Antes de dar algo por terminado",
  punto 4). Verificación manual, anotada en el PR.
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde (`gofmt -l .` vacío,
  `go vet ./...`, `staticcheck`, build Linux y Windows, `go test -race -count=1 ./...`,
  `go test -count=5 ./...`, reviewer sin bloqueantes, PR con CI verde).

### Etapa 2 — canales editables

- [ ] CA2.1 (falla hoy): `nextChat` por canal: cada canal tiene su intervalo (pico / fuera
  del pico) entre dos conexiones o lecturas **de sus vivos** (por turnos entre ellos); entre
  canales se hace a lo sumo una acción por tick; se respeta el `wait` de
  `pollingIntervalMillis` aunque el intervalo del canal sea menor; un canal en pausa no se
  elige — se verifica con `TestNextChatByChannel` (tabla con `now` fijo).
- [ ] CA2.2: con el worker andando, el canal A con intervalo de 50 ms abre al menos 3
  conexiones en 3,5 s y el canal B con 1 h abre 1; el falso nunca ve dos `liveChat/messages`
  (lecturas comunes) en curso a la vez — se verifica con `TestWorkerChatPerChannel`, también
  con `-race`.
- [ ] CA2.3: `LoadSettings`: sin `datos.canales.json`, arranca con `-canal` y con
  intervalos `-chat-seg-pico × N` y `-chat-seg × N` (N = cantidad de canales), así el gasto
  inicial es el de hoy; con el archivo, manda el archivo y si además se pasó `-canal` a mano
  se loguea un `aviso:`; un archivo ilegible frena el arranque con un error en español que
  dice qué hacer (no se pisa) — se verifica con `TestLoadSettings`.
- [ ] CA2.4: `Settings.Update` valida (al menos 1 canal, a lo sumo `maxChannels` = 5,
  `ParseChannel` no vacío, sin repetidos, intervalos dentro de los permitidos) y guarda con
  temporal + `os.Rename`; si no puede guardar, devuelve el error y queda la configuración
  anterior — se verifica con `TestSettingsUpdate`.
- [ ] CA2.5: el worker toma los cambios sin reiniciar: un canal agregado se resuelve en la
  goroutine del worker con `channels` (1 unidad, sumada a `st.Units()`) y entra en la
  revisión siguiente; uno quitado corta los streaming de sus vivos y deja de revisarse; uno
  en pausa no lee chat — se verifica con `TestWorkerAppliesSettings` (falso que cuenta
  pedidos por `liveChatId` y `forHandle`).
- [ ] CA2.6: los handlers nunca llaman a YouTube: `POST /canales/agregar` con un `@handle`
  responde sin que el falso reciba pedidos mientras corre el handler; el canal queda
  "resolviendo…" hasta que el worker lo resuelve o anota el error ("no se encontró el
  canal @x") — se verifica con `TestChannelsAddDoesNotCallAPI`.
- [ ] CA2.7: estimación de cuota (función pura `EstimateQuota`): con el ejemplo del diseño
  (2 canales, `-pico 7-20`, Rivadavia 2 min / 5 min y 24 h en vivo, Rock & Pop 3 min / 5 min
  y 18 h en vivo, cuota 9.000) da videos 1.920, comentarios 1.000, Rivadavia 2.610,
  Rock & Pop 1.600, total 7.130 y "Entra en el día, quedan 370"; con más chat da "No entra:
  faltan X unidades, subí algún intervalo"; un canal en pausa suma 0 de chat; sin horario
  pico todo cuenta como "fuera" — se verifica con `TestEstimateQuota`.
- [ ] CA2.8: `LiveHours` (función pura) saca de `datos.vivos.jsonl` las horas en vivo por
  día de un canal en los últimos 7 días, separadas en pico y fuera del pico; con menos de
  24 h de historial devuelve 24 h y la marca "sin historial" — se verifica con
  `TestLiveHours`.
- [ ] CA2.9: guardas de los `POST`: 403 y sin cambios en el archivo si falta
  `HX-Request: true`, si `Origin` no es el `Host`, si `Sec-Fetch-Site` no es `same-origin`,
  si el `Host` no es `localhost` / `127.0.0.1` / `::1` o si `RemoteAddr` no es loopback;
  cuerpo limitado con `http.MaxBytesReader`; el mismo pedido válido responde 200 — se
  verifica con `TestChannelsPostGuards` (tabla).
- [ ] CA2.10: `GET /canales` muestra una tarjeta por canal (nombre, handle, principal,
  estado EN VIVO / esperando, "Al aire: <programa> desde HH:MM", cómo sale, selects con el
  valor actual elegido, Pausar / Reanudar, Quitar con `hx-confirm`) y la barra de cuota con
  las marcas en `Budget - reserveForComments` y `Budget`; `GET /parcial/canales/cuota` con
  los valores del formulario recalcula sin escribir nada — se verifica con
  `TestChannelsPage`.
- [ ] CA2.11: `go run . -h` (no toca la API) describe `-canal`, `-chat-seg-pico` y
  `-chat-seg` como valores iniciales cuando no existe `datos.canales.json`.
- [ ] CA2.12: README (pantalla Canales, intervalos por canal, nuevas cifras de cuota,
  `datos.canales.json`), AGENTS.md y CLAUDE.md con los textos de "Reglas que cambian", y
  ADR 0005 — se verifica con `git diff --stat main`.
- [ ] CA2.13: revisado en el navegador con la vista previa en claro, oscuro y celular.
  Verificación manual, anotada en el PR.
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

### Etapa 1 — continuidad por canal

**`youtube.go`.** Agregar `ChannelID` a `Video` y `ChannelID string \`json:"channelId"\``
en el `snippet` que decodifica `videos` (`youtube.go:189-195`): viene en la misma parte
que ya se pide, sin costo. Decodificar `items[0].id` en `UploadsPlaylist` y devolverlo
(cambiar la firma o envolverlo en `ChannelInfo{ID, Title, Uploads}`). Nueva
`ChannelLives(upload string) ([]Video, error)`: `RecentVideoIDs("UULV"+…, 5)` + `Videos`,
2 unidades, solo los en curso o programados (la misma regla de `youtube.go:163-166`).

**`store.go`.** `LiveSample{At time.Time; VideoID, ChannelID, Channel, Title string}` en
`<base>.vivos.jsonl` (ya ignorado por `.gitignore:7`, `datos.*.jsonl`). Misma retención y
lectura que la serie de espectadores: generalizar `readViewers` (`store.go:128-150`) a
`readJSONL[T]` y reusar `rewriteJSONL` (`store.go:154-168`). `AddLives(at, vids)` en modo
append, bajo el lock; `Lives(since) []LiveSample` devuelve copia. Se guarda el título y no
el programa para que una mejora de `ProgramName` se aplique también a lo guardado (como
con `programOf`). Estado por canal en memoria, para la vista: `SetChannels([]ChannelStatus)`
con ID, nombre (lo devuelve `channels`; no se persiste), si está en vivo, desde cuándo
espera y hasta cuándo busca cada 1 min; va en `Status()` como copia.

Tamaño: 2 canales × 480 revisiones × 30 días ≈ 29.000 líneas de ~200 bytes ≈ 6 MB. Se
carga al abrir, como la serie de espectadores.

**`worker.go`.** En la revisión de videos (`worker.go:229-279`), después de
`st.AddViewers`, `st.AddLives(now, vids)`. Guardar los IDs de canal resueltos junto a
`uploads` (hoy un `[]string` posicional, `worker.go:134`). Al olvidar un vivo
(`worker.go:263-271`), si su canal no tiene otro vivo en curso, anotar `waitSince` y
`fastUntil = now + fastFor` para ese canal. En el bucle, para cada canal con
`now < fastUntil` y `now >= nextFast` (y `used < cfg.Budget`), `spend(ChannelLives)`; si
aparece un vivo en curso, sumarlo a `lives` y a la lista de videos del `Store`
(reemplazando los de ese canal), crear su `chatState` y cortar la búsqueda rápida. No
correr la búsqueda rápida en el mismo tick que una revisión completa. Variables
`fastEvery = time.Minute` y `fastFor = 20 * time.Minute` como `var` para que los tests las
achiquen (mismo patrón que `realTimeGap`, `worker.go:31`).

**`analyze.go`.** `ProgramSegments(samples []LiveSample, channelID string, from, now
time.Time) []Segment` (pura). `Participation` pasa de un `videoID` a un conjunto de IDs
(`analyze.go:266`), con la suma por vivo en las franjas compartidas. `Moment` suma
`VideoID`. Los demás análisis (`Analyze`, `Supers`, `TopMoments`) no cambian: reciben los
mensajes ya filtrados.

**`server.go` y templates.**

- `pickLive` pasa a `pickChannel(snap, canal, vivo)`: un botón por canal de `-canal` (con o
  sin vivo, en el orden de `-canal`) más uno por vivo suelto de `-vivos`. Etiqueta:
  "<nombre del canal> · en vivo" o "<nombre> · esperando el próximo vivo". `?canal=UC…` en
  el link (`hx-push-url`); `?vivo=ID` se traduce al canal de ese vivo.
- `/parcial/vivo/chat?canal=…`: feed con los últimos 60 mensajes de chat de los vivos del
  canal de hoy (actuales + los de `Store.Lives` desde las 00:00), con un
  `Store.LastChatOf(ids, n)` que reemplaza a `LastChat` (`store.go:333-343`). El
  `HX-Retarget` queda solo para cuando **cambia el vivo en curso del canal** (para recargar
  el análisis), nunca para pasar a otro canal. El cartel del chat (`chatBanner`) se arma con
  el vivo en curso del canal.
- `/parcial/vivo/analisis?canal=…&ver=programa|dia&horas=…`: filtra mensajes y muestras de
  espectadores según el selector y llama a lo mismo que hoy. Encabezado: "Análisis de
  <canal> · <programa en curso | hoy> · N mensajes".
- Parcial nuevo `vivo-tira` (o dentro de `vivo-analisis`, que se refresca cada 30 s) con el
  cartel de continuidad y la tira "Hoy en el canal". Colores con los tokens existentes
  (`--ok-*`, `--surface-*`, `--text-muted`) y nada de JS propio.
- La lista de vivos del resumen (`templates/partials.html:7-14`) queda como está.

**Test de regresión a escribir primero:** `TestLiveChatByChannel` (CA1.2). Hoy falla en
ejecución si se arma con lo que ya existe: dos vivos en `SetVideos` de canales distintos
(con `Channel` distinto) y pedir el chat del vivo terminado; hoy responde
`HX-Retarget: #vivo` con el vivo del otro canal (`server.go:201-207`).

**Alternativas descartadas (etapa 1):**

- Derivar los programas solo de `Message.Program` (sin archivo nuevo): no hay ID de canal
  de los vivos terminados después de reiniciar, los bordes quedan en el primer y último
  mensaje y un programa sin chat no aparece. Sirve como respaldo para días anteriores a
  esta etapa, no como fuente.
- Sumar `ChannelID` y `Title` a `ViewerSample` y guardar también las muestras con
  `Viewers` 0: cambia el significado de un archivo que ya existe ("sin dato" hoy es "no hay
  línea", `store.go:196-198`) y lo leen `Participation` y `viewersLine`.
- Agregar `ChannelID` a `Message`: suma un campo a cada línea de `datos.jsonl` (AGENTS.md,
  "Formato de Message") para un dato que se deduce del vivo.
- Mantener en `Store.videos` los vivos terminados de canales extra: el worker pediría sus
  comentarios (gasto que AGENTS.md pide evitar).

### Etapa 2 — canales editables

**Archivo.** `datos.canales.json` junto a `datos.jsonl` (misma base que `datos.cuota.json`,
`store.go:64-65`; ya ignorado por `.gitignore:6`, `datos.*.json`). Contenido: solo lo que
escribió la persona y sus elecciones, nada que venga de la API:

```json
{"Channels":[{"Input":"UCLAj5TzCrNbqe3LosSKHLcQ","ChatPeakSec":120,"ChatOffSec":300,"Paused":false},
             {"Input":"@<rock-and-pop>","ChatPeakSec":180,"ChatOffSec":300,"Paused":false}]}
```

El orden es el de `-canal`: el primero es el principal. El nombre y el ID resuelto se
guardan solo en memoria (`ChannelStatus`), así no hay datos de la API sin retención. Se
resuelven al arrancar, como hoy (`worker.go:232-241`, 1 unidad por canal).

**Mecanismo (nuevo `channels.go`).** `Settings` con `sync.Mutex`, la lista y un contador
`version`. `LoadSettings(path, fromFlags)`, `Get() ([]ChannelSetting, int)` (copia) y
`Update(f func([]ChannelSetting) ([]ChannelSetting, error)) error`, que valida, guarda con
temporal + `os.Rename` y recién ahí cambia la lista y suma `version`. `WorkerConfig` suma
`Settings *Settings`; si es `nil` (tests de hoy, modo informe), se usa `Channels`,
`ChatPeak` y `ChatEvery` como ahora. El worker compara `version` en cada tick (1 s): si
cambió, resuelve los canales nuevos con `spend` (es la única goroutine que usa el cliente),
corta los streaming de los quitados, y adelanta `nextVideos` a `now`. Los handlers solo
llaman a `Settings.Update` y leen `Settings.Get` y `Store.Status`; nunca a `YouTube` ni a
métodos que escriban en el `Store`.

**Intervalo por canal (`nextChat`).** Cada vivo pertenece a un grupo: el ID de su canal si
es uno configurado o `""` para los vivos sueltos de `-vivos` (usan `-chat-seg-pico` y
`-chat-seg` sin multiplicar). Cada grupo tiene `last` y su intervalo según
`chatInterval` del canal (el horario pico sigue siendo uno solo, `-pico`). Un vivo está
listo si no está bloqueado, no tiene el streaming abierto, su grupo no está en pausa,
`now - c.last >= c.wait` y `now - grupo.last >= intervalo del grupo`. Entre los listos se
elige el del grupo más atrasado y, dentro del grupo, el que hace más que no se lee (como
hoy). **Una sola acción por tick**: la lectura común (`LiveChatPage`) sigue corriendo
sincrónica en la goroutine del worker, así que nunca hay dos en paralelo; el streaming
sigue siendo una goroutine por vivo y no cambia. `-tiempo-real` queda como está (fuera de
los turnos). `lastChat` global y el `every` de `worker.go:366` desaparecen; `chatInfos`
calcula `Gap`, `Lives` y `Next` por grupo.

**Intervalos permitidos.** Pico: 30 s, 1, 1:30, 2, 3, 5 min. Fuera: 1, 2, 3, 5, 10 min (los
del diseño). Al migrar desde flags el valor puede no estar en la lista (por ejemplo,
`-chat-seg 180 × 2` = 6 min): se acepta cualquier múltiplo de 30 s entre 30 s y 10 min, y
el select agrega el valor actual si no es uno de los de la lista.

**Pausar / Quitar / Agregar.** Pausar: no se lee el chat de ese canal (se siguen revisando
sus vivos para no perder la continuidad; ver pregunta 3). Quitar: sale de la configuración;
sus datos quedan hasta que venza la retención. No se puede quitar el último canal. Si se
quita el principal, pasa a serlo el siguiente (cambia de qué canal se bajan comentarios).
Agregar: valida con `ParseChannel`, queda "resolviendo…" y lo resuelve el worker.

**Rutas.**

| Ruta | Qué hace |
|---|---|
| `GET /canales` | Pantalla "Canales" (navegación "En vivo / Canales") |
| `GET /parcial/canales` | Tarjetas + cuota (cada 5 s, como el resumen) |
| `GET /parcial/canales/cuota?…` | Recalcula la estimación con los valores del formulario, sin guardar (`hx-trigger="change"`) |
| `POST /canales` | "Guardar cambios": intervalos y pausas de todos |
| `POST /canales/agregar` | Agrega un canal al final |
| `POST /canales/quitar` | Quita un canal (`hx-confirm`) |

"Descartar" vuelve a pedir `GET /parcial/canales`. "Los cambios se aplican sin reiniciar el
monitor" es verdad a partir del tick siguiente.

**Seguridad de los `POST` (CSRF y DNS rebinding).** El dashboard no tiene usuario y se
puede abrir a la red con `-dashboard 0.0.0.0:8080` (README:114). Defensa con stdlib, la
misma que ya usa `tools/factory-board` (ADR 0004): `sameOrigin` (`HX-Request: true`;
`Origin` igual al `Host` si viene; `Sec-Fetch-Site: same-origin` si viene) más `Host` en
`localhost` / `127.0.0.1` / `::1` y `RemoteAddr` loopback, así solo se edita desde la PC
donde corre el monitor aunque escuche en la red. `http.MaxBytesReader` de 16 KB. No se usa
`http.CrossOriginProtection` porque es de Go 1.25 y `go.mod` dice `go 1.24.7`. Copiar
`sameOrigin` al paquete `main` (son paquetes distintos) con su test.

**Estimación de cuota (pura, en `channels.go` o `analyze.go`).**

```
videos    = revisionesPorDía × (3 + canalesExtra)        // 480 × 4 = 1.920 con 2 canales
comentarios = 1.000                                      // fijo: el techo que ya usa README/AGENTS
chat(c)   = 5 × (horasPico(c) × 3600 / intPico(c) + horasFuera(c) × 3600 / intFuera(c))
horasPico(c)  = min(horasEnVivo(c), largoPico)  (o lo observado en el pico, ver abajo)
horasFuera(c) = horasEnVivo(c) − horasPico(c)
total     = videos + comentarios + Σ chat(c)
entra     = total ≤ Budget − reserveForComments          // 7.500 con -cuota 9000
```

Comprobación con el diseño: Rivadavia 13 h × 30 + 11 h × 12 = 522 lecturas → 2.610;
Rock & Pop 13 h × 20 + 5 h × 12 = 320 → 1.600; 1.920 + 1.000 + 2.610 + 1.600 = 7.130;
quedan 7.500 − 7.130 = 370. Las horas en vivo salen de `datos.vivos.jsonl` (`LiveHours`):
franjas de 5 min con al menos una muestra del canal, de los últimos 7 días, separadas en
pico y fuera del pico, promedio por día. Sin 24 h de historial, se supone 24 h en vivo (el
peor caso) y la tarjeta dice "sin historial". La búsqueda rápida suma como mucho 40 por
cada vez que un canal se queda sin vivo; se estima con los cortes observados por día y se
muestra como parte de "videos".

**Test de regresión a escribir primero:** `TestNextChatByChannel` (CA2.1). Para que falle
en ejecución y no por compilación, el primer caso usa la firma de hoy: dos vivos de canales
distintos, uno leído hace 2 min y otro hace 1 min, y afirma que con intervalos por canal
(que hoy no existen) los dos están listos a la vez; hoy `RunWorker` respeta un solo
`lastChat`. Si no hay forma limpia sin la firma nueva, guardar el error de compilación y
anotarlo.

**Alternativas descartadas (etapa 2):**

- Mandar la configuración al worker por un canal de Go: el handler igual necesita leer la
  configuración vigente para mostrarla, y si el worker está en una lectura bloquearía o se
  perderían cambios. Con `Settings` + `version` el worker no espera a nadie.
- Que el handler resuelva el `@handle`: usaría el cliente desde otra goroutine (no es
  concurrente, `youtube.go:24`) y gastaría cuota fuera de `spend`.
- Un token CSRF por arranque: necesita meterlo en cada página y no agrega nada a
  `sameOrigin` + loopback para este caso.
- Seguir con un intervalo global y repartirlo por "peso" entre canales: no es lo que pidió
  la persona ("yo les indico cada cuánto").

### Reglas de AGENTS.md / CLAUDE.md que cambian (a confirmar por la persona)

1. **AGENTS.md:43, botones.** Hoy: "Botones de vivos: elegir un vivo solo cambia lo que se
   ve […] Si el vivo pedido ya no está en curso, `/parcial/vivo/chat` responde la sección
   entera con `HX-Retarget: #vivo` para pasar al primero en vivo." Propuesta: "Botones de
   canales: uno por canal de la configuración (y uno por vivo suelto de `-vivos`). Elegir un
   canal **solo cambia lo que se ve**; el worker sigue leyendo todos los chats. Si el vivo
   en curso del canal cambia, `/parcial/vivo/chat` recarga la sección del mismo canal con
   `HX-Retarget: #vivo`; nunca pasa a otro canal. Si el canal no tiene vivo, muestra
   'Esperando el próximo vivo' y lo del día."
2. **AGENTS.md:78 y :87-88, intervalo y turnos.** Hoy: el intervalo es "el mínimo entre dos
   conexiones o lecturas cualesquiera" y "el chat cuesta lo mismo con uno o varios vivos
   porque se leen por turnos". Propuesta: "Cada canal tiene su intervalo de chat (pico y
   fuera del pico) entre dos conexiones o lecturas de sus vivos; con varios vivos del mismo
   canal se turnan y ese canal cuesta lo mismo. Cada canal suma su propio chat a la cuota.
   Entre canales, una sola acción por tick: nunca dos lecturas comunes en paralelo. Los
   vivos de `-vivos` que no son de un canal configurado usan `-chat-seg-pico` / `-chat-seg`."
   Y recalcular las cifras de cuota con el ejemplo del diseño.
3. **AGENTS.md:124, concurrencia.** Agregar: "La configuración de canales (`Settings`,
   `datos.canales.json`) es lo único que el dashboard escribe. Los handlers la cambian solo
   con `Settings.Update`, detrás de `sameOrigin` y loopback; nunca llaman a YouTube ni
   escriben en el `Store`. El worker la lee en cada tick y resuelve los `@handle` (1 unidad
   con `spend`)."
4. **AGENTS.md:7 y CLAUDE.md:10, "solo lectura".** El bot sigue siendo de solo lectura con
   YouTube. Agregar: "El dashboard deja de ser de solo lectura en un único punto: la
   configuración de canales (ADR 0005)."
5. **AGENTS.md, "Vivos y programas".** Agregar la búsqueda rápida: "Cuando un canal se
   queda sin vivo, se revisa solo ese canal (`ChannelLives`, 2 unidades) cada 1 min durante
   20 min; después, cada 3 min con el resto."
6. **AGENTS.md, "Datos y privacidad".** Agregar: "`datos.vivos.jsonl` guarda una muestra
   por vivo en curso en cada revisión (ID, canal, título): datos de la API, retención de 30
   días al abrir, nunca dentro de `datos.jsonl`. `datos.canales.json` guarda solo lo que
   escribió la persona (canales e intervalos), nada de la API."
7. **AGENTS.md, mapa del código y rutas:** `channels.go`, las rutas nuevas, `Video.ChannelID`,
   `LiveSample`, `ProgramSegments`, `EstimateQuota`, `LiveHours`.
8. **README:** `-canal` y `-chat-seg*` pasan a ser valores iniciales; la cuota del chat pasa
   a ser por canal (la frase de README:61 "el gasto de cuota del chat no cambia con la
   cantidad de vivos" deja de valer entre canales).
9. **ADR 0005 (propuesto):** "La configuración de canales se edita desde el dashboard y el
   worker la toma en caliente": archivo, `Settings` + `version`, guardas de los `POST`,
   intervalo por canal en vez de global, y alternativas descartadas de arriba.

## Fuera de alcance

- **Parte 2 del pedido:** análisis con Claude (sentimiento, preguntas agrupadas,
  categorías por LLM, resúmenes) y todo lo marcado "Claude" en el diseño. Va en otra tarea.
- La sección "Programas de hoy" de la navegación del diseño, si es una pantalla aparte (ver
  pregunta 6); acá solo la tira "Hoy en el canal" dentro de la vista del canal.
- Cambiar el modo informe (`go run .`): sigue con `-canal` y sin `datos.canales.json`.
- Elegir el canal principal desde la pantalla (sale del orden), reordenar canales, editar
  `-pico`, `-cuota` o `-comentarios-min` desde el dashboard.
- Autenticación del dashboard, editar desde otras PCs de la red, `search.list`, OAuth.
- Detectar el fin de un vivo por el error del chat (`liveChatEnded`) para buscar antes:
  posible mejora después, con capturas reales del error.
- `tools/preview` con varios canales (ver pregunta 7).

## Riesgos

- **Cuota (etapa 2).** Con intervalos por canal el chat de cada canal suma aparte: con los
  defaults de hoy (60 s / 180 s) en cada uno de 2 canales el total pasaría de 9.000. Se
  mitiga arrancando con `-chat-seg* × N` (CA2.3, mismo gasto que hoy), con la estimación
  antes de guardar (CA2.7), con `maxChannels` y con el tope `Budget` que sigue igual. El
  intervalo mínimo de 30 s en el pico cuesta 600 por hora por canal: la tarjeta lo muestra.
- **Cuota (etapa 1).** La búsqueda rápida gasta a lo sumo 40 unidades por cada vez que un
  canal se queda sin vivo (20 × 2); con 6 programas por día en Rock & Pop, ≤ 240 por día, y
  mucho menos si el siguiente empieza enseguida. Se corta con el tope `Budget` (CA1.6). Se
  verifica con `st.Units()` en `TestWorkerFindsNextLive`.
- **Lista `UULV` no documentada** (`youtube.go:149-150`). Que un vivo nuevo aparezca en ella
  al minuto de empezar es una hipótesis; se confirma mirando el log del worker en los
  primeros días ("vivo nuevo de <canal> encontrado a los N min"). Si tarda más, la búsqueda
  rápida no empeora nada: gasta y engancha cuando aparece.
- **Demora en el cambio de programa.** Los tramos y el cartel usan la primera muestra con el
  título nuevo: hasta 3 min (o 1 min en búsqueda rápida) después del cambio real. El texto
  del cartel lo dice como hora aproximada.
- **Concurrencia.** `Settings` lo comparten handlers y worker: lock propio, copias en
  `Get`, y `go test -race` en CA2.2 / CA2.5. Los handlers siguen sin tocar el cliente ni
  escribir el `Store` (CA2.6). Los tests que arrancan `RunWorker` usan `startWorker` /
  `runWorker` y lo paran antes del cleanup de `t.TempDir()`.
- **CSRF / red local.** Cubierto por CA2.9. Quien abra el dashboard desde otra PC con
  `0.0.0.0` ve la pantalla pero no puede guardar (a confirmar, pregunta 4).
- **Compatibilidad de datos.** `datos.jsonl` y `datos.espectadores.jsonl` no cambian
  (CA1.12). Archivos nuevos con temporal + `os.Rename` (anda en Windows, ya se usa en
  `store.go:257-258`). Sin `datos.canales.json` todo arranca como hoy.
- **Privacidad.** `datos.vivos.jsonl` es dato de la API: retención de 30 días al abrir
  (CA1.4), ignorado por `.gitignore:7`. `datos.canales.json` no tiene datos de la API.
  Ninguno se usa como fixture.
- **Tamaño del cambio en el dashboard.** Cambiar `pickLive` y el análisis toca muchos tests
  de `server.go` (`TestLiveChatSelection`, `TestLiveAnalysisSuper`, `TestLiveChatSuper`,
  `TestLiveParticipationView*`). Se mitiga con la etapa 1 sin escrituras y con `?vivo=` que
  sigue andando.

## Preguntas abiertas para la persona que aprueba

1. **Orden de etapas:** ¿va primero la etapa 1 (continuidad por canal) como recomiendo, o la
   2 (canales editables)?
2. **"Todo el día del canal":** ¿desde las 00:00 de hoy en hora local (lo que propongo, igual
   que la tira) o las últimas 24 h como el análisis de hoy?
3. **Pausar:** ¿pausa solo el chat (lo que propongo: se sigue viendo si está en vivo y la
   tira sigue armándose) o todo el canal, también la revisión de sus vivos (ahorra 1 unidad
   por revisión, ~480 por día, pero se pierde la continuidad mientras está en pausa)?
4. **Editar desde otras PCs:** ¿alcanza con poder cambiar la configuración solo desde la PC
   donde corre el monitor (lo que propongo), o hace falta desde la red local? Lo segundo
   necesita alguna forma de autenticación, que está fuera de alcance.
5. **Valores iniciales:** si no existe `datos.canales.json`, ¿arranco con `-chat-seg* × N`
   (mismo gasto que hoy; con 2 canales: 2 min en el pico y 6 min fuera) o con los del
   diseño (2 min / 5 min)? Y si existe el archivo y se pasa `-canal` a mano, ¿gana el
   archivo con un aviso (lo que propongo) o gana `-canal`?
6. **"Programas de hoy"** en la navegación: ¿es otra pantalla (no está en las pantallas 1 y
   2 que vi) o alcanza con la tira dentro de cada canal?
7. **Vista previa:** para revisar esto en el navegador (paso 7b) la vista previa tendría que
   simular dos canales y copiar `datos.vivos.jsonl` (y no copiar `datos.canales.json`).
   ¿Lo sumo a cada etapa (toca `tools/preview` y el ADR 0003) o se revisa con la vista
   previa de un solo canal?
8. **"Cómo sale"** de cada tarjeta ("un vivo de 24 h" / "un vivo nuevo por programa"): ¿lo
   deduzco de lo observado (un vivo con varios programas en el día → 24 h; varios vivos →
   uno por programa), o lo elige la persona al agregar el canal?
9. **Reglas de AGENTS.md / CLAUDE.md** de la sección "Reglas que cambian": ¿aprobás los textos
   propuestos y el ADR 0005?
