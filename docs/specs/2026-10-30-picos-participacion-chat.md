# Spec: picos de participación del chat por franjas de 5 minutos (para reels)

- **Estado:** implementada
- **Tipo:** mejora
- **Rama:** `feat/picos-participacion-chat`
- **Origen:** pedido de Julian Barbieri (textual): "Necesito que se agregue un grafico o
  una seccion que me ayude a saber Qué momentos generan participación? Qué % de los que
  miran escriben en el chat? Esos picos de participacion, me podrian dar una ayuda para
  que se puedan generar reels de ese determinado momento espacio de tiempo. dentro del
  sistema se encuentra dividido por franjas de 5 minutos."
- **Aprobada por / fecha:** Julian Barbieri, 2026-10-30 ("Aprobar así": con las recomendaciones de las preguntas abiertas tal cual)

## Contexto

Es una mejora, no un bug: no hay nada que reproducir. Esto es lo que hay hoy, verificado
leyendo el código (rama `feat/picos-participacion-chat`, igual a `main` en `814f1cb`):

**Franjas de 5 minutos.** Están solo en `activity` (`server.go:143-179`): 36 franjas de
5 minutos (últimas 3 h) que terminan en `now.Truncate(5*time.Minute).Add(5*time.Minute)`
(`server.go:146`), y cada mensaje va a la franja `int(m.At.Sub(start)/step)`
(`server.go:153`). Cuenta **todos** los mensajes (comentarios y chat, de todos los
videos), no los de un vivo, y no cuenta autores. Se dibuja en el bloque `resumen`
(`templates/partials.html:27-43`) con SVG armado en Go, `role="img"`, `aria-label` y un
`<title>` por barra. No hay franjas en `analyze.go`.

**Espectadores.** `videos` ya pide `part=snippet,statistics,liveStreamingDetails`
(`youtube.go:204`) y ya lee `liveStreamingDetails.concurrentViewers` en
`Video.Concurrent` (`youtube.go:200`, `youtube.go:219`). El dato se pisa en cada revisión:
`Store.SetVideos` reemplaza la lista entera (`store.go:127-131`), no queda serie temporal.
El worker revisa videos cada `VideosEvery` = 3 minutos (`main.go:111`,
`worker.go:229-246`), así que cada franja de 5 minutos tiene 1 o 2 lecturas de
espectadores, sin pedir nada nuevo a la API. Según la documentación de YouTube,
`concurrentViewers` falta si el canal oculta el contador; `atoi("")` da 0
(`youtube.go:597-601`), así que 0 tiene que leerse como "sin dato".

**Inicio del vivo.** `actualStartTime` **no** se lee: el struct de `videos`
(`youtube.go:198-201`) solo trae `activeLiveChatId` y `concurrentViewers`. Viene en la
misma parte `liveStreamingDetails`, así que leerlo no cuesta cuota. `Video` no se
persiste (`Store.rewrite` y `Store.Add` solo escriben `Message`, `store.go:85-125`):
agregarle un campo no rompe datos guardados.

**Programa y autores.** Cada mensaje de chat guarda el programa al llegar
(`worker.go:182`, `worker.go:387`); `programOf` (`main.go:211-216`) cae al título actual
para mensajes viejos. Del autor solo se guarda el nombre visible
(`youtube.go:431-433`, `youtube.go:448`), no el ID del canal: "personas únicas" se cuentan
por `Message.Author`.

**Sección del vivo.** `liveAnalysis` (`server.go:365-398`) arma los 5 análisis del vivo
elegido con los mensajes de las últimas 24 h (`server.go:378`) y se refresca cada 30 s
(`templates/partials.html:101`). Hoy solo busca la etiqueta del vivo
(`server.go:368-373`), no el `Video` entero.

**Archivos de datos.** `.gitignore:6` ignora `datos.*.json`, pero **no**
`datos.*.jsonl`. Verificado:

```bash
git check-ignore -v datos.espectadores.jsonl datos.espectadores.json
```

```
.gitignore:6:datos.*.json	datos.espectadores.json
```

(`datos.espectadores.jsonl` no aparece: hoy se podría commitear por error.)

Línea de base: `go build ./...`, `go vet ./...` y `go test -count=1 ./...` en verde antes
del cambio.

**Causa raíz:** no aplica (mejora).

## Objetivo

En la sección del vivo elegido hay un gráfico de participación del chat cada 5 minutos
(últimas 24 h) y una lista de los momentos pico, con hora local de inicio y fin, programa,
mensajes, personas, % de los que miran que escribieron y cómo ubicar ese momento para
cortar un reel. Los espectadores se guardan como serie temporal por vivo con la misma
retención de 30 días, sin gastar cuota extra y sin tocar el formato de `datos.jsonl`.

## Definiciones (las usan los criterios)

- **Franja:** intervalo `[t, t+5min)` con `t = At.Truncate(5*time.Minute)`, igual que
  `activity`. Se muestra en hora local (`t.Local()`).
- **Ventana:** las 288 franjas de las últimas 24 h, la última es la que contiene `now`
  (`end = now.Truncate(5min).Add(5min)`, `start = end - 24h`). Misma ventana que el resto
  del análisis del vivo.
- **Mensajes de la franja:** mensajes con `Source == "chat"` y `VideoID` del vivo con `At`
  en la franja. Los comentarios no cuentan.
- **Personas de la franja:** cantidad de `Author` distintos (no vacíos) entre esos mensajes.
- **Espectadores de la franja:** promedio (redondeado al entero) de las muestras de ese
  vivo con `Viewers > 0` y `At` en la franja. Sin muestras: **sin dato** (0).
- **Participación de la franja (%):** `personas / espectadores * 100`, solo si hay
  espectadores; si no, sin dato. No se recorta a 100 %. Es una aproximación:
  `concurrentViewers` es "mirando ahora" y las personas se acumulan durante 5 minutos.
- **Participación típica del vivo:** mediana de la participación de las franjas de la
  ventana **con** dato de espectadores (incluye las de 0 personas). Sin ninguna: sin dato.
- **Base del vivo:** mediana de personas por franja entre las franjas de la ventana con
  al menos un mensaje.
- **Franja pico:** `personas >= max(minPersonasPico, factorPico * base)`, con
  `minPersonasPico = 3` y `factorPico = 2` (constantes con nombre; ver preguntas abiertas).
- **Momento pico:** franjas pico consecutivas agrupadas. Inicio = inicio de la primera,
  fin = fin de la última, **franja más fuerte** = la de más personas (desempate: más
  mensajes, después la más temprana). Se ordenan por personas de la franja más fuerte,
  después mensajes, después la más reciente. Se muestran los primeros `maxMomentos = 5`.

## Criterios de aceptación

- [x] CA1: `Participation` (función pura en `analyze.go`) arma las 288 franjas de la
  ventana para un vivo con mensajes, personas, espectadores y participación según las
  definiciones: el mensaje a las 14:05:00 cae en la franja 14:05; ignora comentarios,
  mensajes de otros vivos y mensajes fuera de la ventana; dos mensajes del mismo autor
  cuentan una persona; dos muestras (100 y 200) dan 150 espectadores; franja sin muestras
  o con `Viewers == 0` da sin dato y participación sin dato; 3 personas con 150
  espectadores dan 2,0 %. — se verifica con `go test -count=1 -run '^TestParticipation$' ./...`
  (tabla de casos con `now` fijo).
- [x] CA2: la participación típica es la mediana de las franjas con dato (incluye franjas
  con 0 personas) y es sin dato si no hay ninguna franja con espectadores. — se verifica
  con `go test -count=1 -run '^TestParticipation$' ./...`.
- [x] CA3: `PeakMoments` (función pura en `analyze.go`) aplica la base, el umbral
  `max(3, 2 × base)`, agrupa franjas pico consecutivas, elige la franja más fuerte, ordena
  y corta en 5. Casos mínimos: sin mensajes → ningún momento; actividad pareja (todas las
  franjas igual) → ningún momento; un pico de 1 franja; dos franjas pico seguidas → un
  momento de 10 minutos con la más fuerte bien elegida; 7 picos separados → 5 momentos en
  el orden definido; pico de 2 personas con base 0 → no es pico (mínimo 3). Cada momento
  trae el programa más frecuente del momento (con `programOf`), los temas
  (`TopTopics(msgs, 3)`) y hasta 2 mensajes de ejemplo que no sean spam ni insulto
  (`Classify`), los primeros del momento que contengan el tema principal, o los primeros
  si no hay tema. — se verifica con `go test -count=1 -run '^TestPeakMoments$' ./...`.
- [x] CA4: `reelLink` (función pura en `server.go`) devuelve cómo ubicar el momento:
  (a) vivo con `StartedAt` conocido, que empezó hace 12 h o menos y franja posterior al
  inicio → link `https://www.youtube.com/watch?v=<ID>&t=<segundos>s` con
  `segundos = inicio de la franja − StartedAt`; (b) vivo que empezó hace más de 12 h (el de
  24 h de Rivadavia) y franja de las últimas 12 h → sin link con `t`, texto "Retrocedé
  ~X h Y min en el reproductor del vivo"; (c) franja de hace más de 12 h → texto "Fuera de
  lo que se puede retroceder en YouTube: usá la grabación propia"; (d) `StartedAt` cero →
  como (b) o (c) según la antigüedad de la franja. — se verifica con
  `go test -count=1 -run '^TestReelLink$' ./...` (tabla con `now` fijo).
- [x] CA5: `Videos` lee `liveStreamingDetails.actualStartTime` en `Video.StartedAt` sin
  cambiar `part` ni el costo: en `TestFlowAgainstFakeAPI` el vivo falso trae
  `actualStartTime` y `concurrentViewers`, `vids[0].StartedAt` coincide y el chequeo de
  `yt.Units` (`2+1+2+1+1+2*5`) no cambia. — se verifica con
  `go test -count=1 -run '^TestFlowAgainstFakeAPI$' ./...`.
- [x] CA6: `Store.AddViewers(at, vids)` guarda una muestra `{VideoID, At, Viewers}` por
  cada video con `LiveStatus == "live"` y `Concurrent > 0` (ignora los demás), en memoria
  y en modo append en `<datos sin .jsonl>.espectadores.jsonl`. `OpenStore` la carga,
  descarta las muestras de más de `retention` (30 días) y las líneas que no se pueden
  leer, y reescribe el archivo con archivo temporal + `os.Rename`. `Store.Viewers(id,
  since)` devuelve una copia (modificarla no cambia el `Store`). `datos.jsonl` queda igual
  (mismos mensajes después de reabrir). Una línea larga (más de 64 KB) se saltea sin
  perder las siguientes y un error de lectura no reescribe el archivo. — se verifica con
  `go test -count=1 -run '^TestStoreViewers(Open|LongLine)?$' ./...`.
- [x] CA7: el worker llama a `AddViewers` en cada revisión de videos que sale bien, sin
  llamadas nuevas a la API: con una API falsa (`httptest`) que devuelve un vivo con
  `concurrentViewers`, después de la primera revisión `st.Viewers` tiene la muestra con
  ese valor y el servidor falso no recibió rutas distintas de las de hoy; el worker se
  para antes del cleanup de `t.TempDir()` (helper `startWorker`). — se verifica con
  `go test -count=1 -run '^TestWorkerRecordsViewers$' ./...`.
- [x] CA8: `GET /parcial/vivo/analisis?video=<ID>` muestra la sección "Momentos con más
  participación" del vivo elegido: SVG con `role="img"` y `aria-label` que nombra el pico,
  un `<title>` por barra con "HH:MM a HH:MM", programa, mensajes, personas y espectadores
  o "sin dato"; las barras de los momentos pico con la clase `pico`; la participación
  típica; y la tabla de momentos con inicio y fin (`02/01 15:04`), programa, mensajes,
  personas, % (o "sin dato") y el link o el texto de `reelLink`. Con datos armados en el
  test, un autor `<script>x</script>` sale escapado (`&lt;script&gt;`). Sin muestras de
  espectadores muestra "sin dato" y el aviso correspondiente (CA9). — se verifica con
  `go test -count=1 -run '^TestLiveParticipationView$' ./...`.
- [x] CA9: avisos sin dato: si el vivo tiene `Concurrent == 0` ahora, el texto dice que
  YouTube no informa cuántos miran este vivo; si tiene espectadores pero hay franjas sin
  muestra (por ejemplo, de antes de este cambio o de cuando se cortó la revisión de
  videos), esas franjas dicen "sin dato" y no entran en la participación típica. — se
  verifica con `go test -count=1 -run '^TestLiveParticipationView$' ./...`.
- [x] CA10: el archivo de espectadores no se puede commitear: `.gitignore` incluye
  `datos.*.jsonl`. — se verifica con `git check-ignore -q datos.espectadores.jsonl`
  (sale con código 0).
- [x] CA11: sin `template.HTML` con texto de usuarios ni JS propio ni recursos externos:
  `grep -n 'template.HTML' *.go` no lista nada y `grep -n '<script' templates/*.html`
  solo lista `/static/htmx.min.js`; `go.mod` sigue sin `require`.
- [x] CA12: cuota sin cambios: no hay endpoints nuevos ni cambios de `part` ni de `cost`
  (lo cubren CA5 y CA7); `git diff main -- youtube.go` solo agrega el campo
  `ActualStartTime` al struct de `videos` y `StartedAt` a `Video`.
- [x] CA13: el README (sección 3) menciona la sección nueva y el archivo
  `datos.espectadores.jsonl` con su retención de 30 días; `AGENTS.md` (mapa de `store.go`
  y `analyze.go`, "Datos y privacidad") y `CLAUDE.md` ("Qué NO tocar": `datos.*.jsonl`)
  quedan al día. Las cifras de cuota del README no cambian. — se verifica revisando
  `git diff main -- README.md AGENTS.md CLAUDE.md` en la revisión.
- [ ] CA14 (pendiente: manual, no la puede hacer el agente): verificación manual en el navegador (`AGENTS.md`, "Antes de dar algo por
  terminado", punto 4) con una API simulada (`YT_BASE_URL=http://localhost:9999
  YT_API_KEY=x`), en modo claro y oscuro y en ancho de celular: el gráfico se lee, las
  barras pico se distinguen en los dos temas y la tabla no rompe el ancho. Una persona
  prueba a mano si el link con `&t=` abre el momento correcto en un vivo de menos de 12 h
  y en su VOD; si no, se ajusta `reelLink` antes de mergear. La evidencia va en el PR.
- [x] Gates de `CLAUDE.md` → "Definición de terminado" en verde: `gofmt -l .` vacío,
  `go vet ./...`, `staticcheck` limpio, `go build ./...` y
  `GOOS=windows GOARCH=amd64 go build ./...` OK, `go test -race -count=1 ./...` y
  `go test -count=5 ./...` en verde.

## Diseño propuesto

Orden sugerido (tests primero): `TestParticipation`, `TestPeakMoments`, `TestReelLink`
(funciones puras), después `TestStoreViewers`, `TestFlowAgainstFakeAPI` (extensión),
`TestWorkerRecordsViewers` y por último `TestLiveParticipationView`.

### 1. Cliente (`youtube.go`), costo 0

- Agregar `ActualStartTime time.Time \`json:"actualStartTime"\`` al struct anónimo de
  `liveStreamingDetails` en `videos` (`youtube.go:198-201`) y `StartedAt time.Time` a
  `Video` (`youtube.go:60-70`). Mismo pedido, misma parte, mismo costo.

### 2. Serie de espectadores (`store.go`)

```go
// ViewerSample es cuántos miraban un vivo según la revisión de videos.
type ViewerSample struct {
	VideoID string
	At      time.Time
	Viewers int64
}
```

- Campos nuevos en `Store`: `viewersPath string` (`strings.TrimSuffix(path, ".jsonl") +
  ".espectadores.jsonl"`, mismo patrón que `quota`, `store.go:53`) y `viewers
  []ViewerSample`.
- `OpenStore`: después de los mensajes, lee el archivo de espectadores con el mismo
  esquema (scanner, descarta lo vencido y lo ilegible) y lo reescribe con tmp + rename.
  Generalizar `rewrite` (`store.go:85-99`) a un helper `rewriteJSONL(path, items)` que
  usen los dos.
- `AddViewers(at time.Time, vids []Video)`: bajo el lock, agrega en memoria y en append
  (igual que `Add`, `store.go:102-125`). Si no se puede abrir el archivo, sigue en memoria.
- `Viewers(videoID string, since time.Time) []ViewerSample`: copia filtrada, con `RLock`.
  No se agrega a `Snapshot` para no copiar la serie entera cada 2 s.
- Tamaño: ~480 muestras por vivo y por día; en 30 días, ~14.400 líneas por vivo (~1 MB).

### 3. Worker (`worker.go`)

- Una línea después de `st.SetVideos(...)` (`worker.go:246`):
  `st.AddViewers(time.Now(), vids)`. Sin llamadas nuevas a la API, en la misma goroutine.

### 4. Análisis (`analyze.go`)

```go
// Slot es una franja de 5 minutos del chat de un vivo.
type Slot struct {
	Start    time.Time
	Msgs     []Message // solo para los momentos; no se muestran todos
	N        int       // mensajes
	Authors  int       // personas distintas
	Viewers  int64     // promedio de las muestras; 0 = sin dato
	Pct      float64   // participación; vale solo si Viewers > 0
}

func Participation(msgs []Message, viewers []ViewerSample, videoID string, now time.Time) (slots []Slot, typical float64, ok bool)

type Moment struct {
	Start, End time.Time
	Best       Slot     // franja más fuerte
	N, Authors int      // totales del momento (personas distintas en todo el momento)
	Program    string
	Topics     []Count
	Examples   []Message
	Ratio      float64  // personas de la franja más fuerte / base ("3,2 veces lo normal")
}

func PeakMoments(slots []Slot, titles map[string]string) []Moment
```

- Las franjas se calculan igual que `activity` (`Truncate`, índice por división). No se
  toca `activity` del resumen: refactorizarlo para compartir código no hace falta acá.
- Constantes con nombre: `minPersonasPico = 3`, `factorPico = 2`, `maxMomentos = 5`.

### 5. Servidor y template

- `liveAnalysis` (`server.go:365-398`): guardar el `Video` encontrado (no solo la
  etiqueta), pedir `s.st.Viewers(id, now.Add(-24*time.Hour))`, llamar a `Participation`
  y `PeakMoments` con `sinceFor(snap.Msgs, id, ...)` (ya calculado) y pasar al template
  el gráfico y las filas. Todo con `now := time.Now()` una sola vez.
- `participationChart(slots, moments)`: arma las barras con el tipo `Bar` existente
  (`server.go:69-73`), alto = personas, `Label` para el `<title>`, una marca de clase para
  las franjas de momentos pico, una línea horizontal en el umbral ("2 veces lo normal") y
  ticks cada 3 h. Con 288 barras en un `viewBox` de 720, ancho de barra 2,5 y separación
  0,5 (no `bw - 2` como en `activity`, que daría ancho negativo).
- `reelLink(v Video, slotStart, now time.Time) (href, hint string)` con la constante
  `dvrWindow = 12 * time.Hour` (ver riesgos).
- `templates/partials.html`, bloque `vivo-analisis`: una tarjeta más dentro de `.vgrid`
  que ocupa todo el ancho (`grid-column: 1 / -1`), titulada "6. Momentos con más
  participación (para reels)": línea con la participación típica y los espectadores
  ahora, el SVG (mismas reglas que `templates/partials.html:30-41`) y una tabla
  `.scroll` con los momentos (inicio–fin, franja más fuerte, programa, mensajes, personas,
  espectadores, %, "X veces lo normal", temas, ejemplos, link o texto). Links con
  `target="_blank" rel="noopener"` como los existentes.
- `templates/page.html`: `.bar.pico { fill: var(--warn-text); }` (token ya definido en
  los dos temas y usado igual por `.fill.warn`, `page.html:114`), `.vgrid .full {
  grid-column: 1 / -1; }` y, si hace falta, estilo de la línea de umbral con `--text-muted`.
  Sin colores nuevos.

### 6. Datos y docs

- `.gitignore`: agregar `datos.*.jsonl` debajo de `datos.*.json`.
- README, AGENTS.md y CLAUDE.md según CA13.

### Alternativas descartadas

- **Guardar los espectadores dentro de `datos.jsonl`** (como `Message` con otro `Source`
  o campos nuevos): rompe la regla de no cambiar `Message` y mezclaría muestras con
  mensajes en el feed, el CSV y los conteos.
- **Pedir `videos` más seguido para tener más muestras:** cuesta cuota; con 3 minutos ya
  hay 1 o 2 muestras por franja.
- **Usar `Video.Views` (vistas totales) como denominador:** no dice cuántos miran en ese
  momento.
- **Calcular el pico por mensajes en vez de personas:** un solo usuario (o un spammer) que
  escribe mucho infla la franja; personas distintas es más robusto. Los mensajes se
  muestran igual y desempatan.
- **Sumar columnas al CSV actual** (`csvHeader`): cambia un formato que se usa con
  Claude; si hace falta exportar, un CSV aparte (pregunta abierta 4).
- **Calcular `t=` para el vivo de 24 h desde `actualStartTime`:** el vivo empezó hace
  días, así que el segundo cae muy fuera de lo que YouTube deja retroceder; por eso
  `reelLink` da el texto "retrocedé ~X" en ese caso.

## Fuera de alcance

- El gráfico "Mensajes cada 5 minutos" del resumen (`activity`): queda igual.
- Modo informe (`go run .`): no guarda espectadores ni calcula picos.
- Bajar o cortar video, generar el reel o integrarse con editores: solo se indica el
  momento.
- Limpieza periódica en memoria de mensajes o muestras de más de 30 días con el proceso
  andando: hoy tampoco se hace con los mensajes (`store.go:85`, solo al abrir).
- Comentarios del video del vivo: la participación es solo del chat.
- Cambiar el clasificador o las listas de `analyze.go`.
- Exportar los momentos pico a CSV, salvo que se decida en la pregunta abierta 4.

## Riesgos

- **Cuota:** impacto nulo esperado. `actualStartTime` y `concurrentViewers` vienen en la
  parte que ya se pide; no hay endpoints nuevos. Lo verifican CA5, CA7 y CA12. El README
  no cambia sus cifras de cuota.
- **Compatibilidad de `datos.jsonl`:** no se toca `Message` ni el archivo. El archivo
  nuevo es aparte y, si no existe (instalaciones viejas), se crea solo; si está dañado, se
  saltean las líneas ilegibles. Lo verifica CA6.
- **Retención y privacidad:** el archivo nuevo tiene datos de la API (espectadores por
  vivo), así que se aplica la misma retención de 30 días al abrir y tmp + rename (CA6).
  No tiene nombres de usuarios. Hoy no está en `.gitignore`: lo arregla CA10. Los
  ejemplos de mensajes y autores ya se muestran en el dashboard, que sigue en `localhost`.
- **Concurrencia:** `AddViewers` corre en la goroutine del worker bajo el lock del
  `Store`; los handlers solo leen copias con `Viewers`. `go test -race` lo cubre.
- **Calidad del dato:** `concurrentViewers` puede faltar (canal que oculta el contador) o
  venir redondeado o cacheado por YouTube; las personas se cuentan por nombre visible, que
  puede repetirse o cambiar. Es una aproximación, igual que el resto de `analyze.go`; la
  sección lo aclara en una línea.
- **DVR de YouTube (hipótesis, no verificada sin red):** según la ayuda de YouTube, en un
  vivo con DVR se puede retroceder hasta unas 12 h y los vivos de más de 12 h no quedan
  enteros como VOD. Tampoco está documentado cómo trata el reproductor `&t=` en un vivo en
  curso. Se confirma a mano en CA14; si el resultado es otro, se ajusta `dvrWindow` o se
  deja solo el texto "retrocedé ~X". Para el vivo de 24 h de Rivadavia lo útil va a ser la
  hora local y el programa, para cortar de la grabación propia de la radio.
- **Rendimiento:** `liveAnalysis` ya copia todos los mensajes cada 30 s; las franjas son
  una pasada más sobre los mensajes del vivo en 24 h. Despreciable.
- **Ancho de celular:** 288 barras en un SVG que escala quedan de ~1 px en 360 px de
  ancho; se lee como curva. Lo verifica CA14; si no alcanza, se puede achicar la ventana
  del gráfico a 12 h (pregunta abierta 2).
- **Windows:** `os.Rename` sobre un archivo existente funciona en Go en Windows (ya lo usa
  `saveUnits`); el build de Windows está en los gates.

## Preguntas abiertas (decisiones de producto)

Resueltas al aprobar ("Aprobar así", Julian Barbieri, 2026-10-30): se toman las
recomendaciones tal cual.

1. **Umbral y cantidad de picos:** resuelta. Franja pico = al menos 3 personas y el doble
   de la mediana de personas por franja con actividad; se muestran los 5 momentos más
   fuertes (`minPersonasPico = 3`, `factorPico = 2`, `maxMomentos = 5`).
2. **Ventana:** resuelta. 24 h (288 franjas de 5 minutos), como el resto del análisis del
   vivo.
3. **Criterio del pico:** resuelta. Personas distintas que escribieron (no mensajes ni %);
   los mensajes desempatan.
4. **Export:** resuelta. El CSV de momentos pico queda fuera de alcance (otra tarea).
5. **Spam en los picos:** resuelta. El spam cuenta para los picos; spam e insultos solo se
   excluyen de los mensajes de ejemplo.
6. **Link a YouTube:** resuelta. Se mantiene el link con `&t=` con la lógica de
   `reelLink` descripta arriba (a confirmar a mano en CA14).
