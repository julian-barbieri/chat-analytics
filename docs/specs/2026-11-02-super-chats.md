# Spec: capturar Super Chats y Super Stickers del chat en vivo

- **Estado:** borrador
- **Tipo:** mejora
- **Rama:** `feat/super-chats`
- **Origen:** pedido de Julian Barbieri (2026-11-02): "Capturar los Super Chats y Super
  Stickers del chat en vivo: decodificar snippet.type y superChatDetails/superStickerDetails
  (amountMicros, currency, amountDisplayString, tier, comentario) en chatPage de youtube.go,
  que hoy los descarta y los guarda como chat común. Guardarlos de forma compatible hacia
  atrás con datos.jsonl […], sin cuota extra […]. Mostrarlos en el dashboard y en el CSV con
  totales por moneda (sin convertir) y por programa. Es el monto bruto que pagó cada
  persona, no el ingreso neto del canal (eso requiere OAuth, fuera de alcance)."
- **Aprobada por / fecha:** —

## Contexto

Hoy el chat se decodifica en un solo lugar, que usan la lectura común, el streaming y el
modo informe:

- `youtube.go:425` `chatPage`: de cada item solo decodifica `id`,
  `snippet.displayMessage`, `snippet.publishedAt` y `authorDetails.displayName`. No lee
  `snippet.type` ni `snippet.superChatDetails` ni `snippet.superStickerDetails`.
- `youtube.go:444-456` `chatPage.messages`: descarta todo item con `displayMessage` vacío
  (`youtube.go:447`) y arma un `Message` común con `Source: "chat"` y
  `Text: displayMessage`.
- `youtube.go:473` (`LiveChatPage`), `youtube.go:547` (`StreamChat`) y, a través de
  `LiveChatPage`, `LiveChats` (modo informe) pasan por `chatPage.messages`.
- `youtube.go:459` `chatParams` ya pide `part=snippet,authorDetails`; los detalles de Super
  Chat y Super Sticker son parte de `snippet` (ver supuestos), así que **ya llegan en las
  respuestas que se pagan hoy** y se tiran al decodificar.
- `youtube.go:228-238` `Message`: sin etiquetas JSON; `datos.jsonl` usa los nombres de
  campo de Go. El worker no mira el contenido: `worker.go:182-184` y `worker.go:389-390`
  hacen `TagProgram` + `st.Add`, así que cualquier campo nuevo de `Message` se guarda y
  se le anota el programa sin tocar el worker.
- `main.go:227-234` `csvHeader`/`csvRow`: 9 columnas, las usan el informe
  (`writeCSV`, `main.go:207`) y `GET /export.csv` (`server.go`, `export`).
- Dashboard: el análisis del vivo elegido (`server.go:301` `liveAnalysis`, ventana de
  24 h con `sinceFor`) tiene las tarjetas 1 a 5 en `templates/partials.html:82-177`; el
  chat del vivo (`templates/partials.html:55-80`) muestra un badge por categoría.

Consecuencia hoy: un Super Chat con comentario queda como chat común sin monto; un Super
Chat sin comentario o un Super Sticker se guarda como chat común si `displayMessage` trae
algo y **se pierde** si viene vacío. No hay forma de saber cuánto se pagó ni en qué
programa.

**Clasificador:** con el texto actual no hay evidencia de que un Super Chat caiga en
spam por el monto. `classify.go:22` (`reLink`) no matchea códigos de moneda ni montos;
`classify.go:23` (`rePhone`, `\+?\d[\d\s-]{8,}\d`) pide 10 o más caracteres seguidos entre
dígitos, espacios ASCII y guiones, y un monto con separadores `.` o `,` ("ARS 10.000,00",
"US$ 1,000.00") lo corta. El único caso teórico es un monto con miles separados por
espacio ASCII ("10 000 000"); verificado leyendo el regex, no corriéndolo. Con el diseño
de abajo el monto **nunca** entra en `Message.Text`, así que `Classify` no se toca.

**Reproducción:** no aplica (mejora). Línea de base verificada en esta rama con Go 1.26.8:
`go build ./...`, `go vet ./...` y
`go test -count=1 -run '^(TestStreamChat|TestFlowAgainstFakeAPI|TestClassify|TestStoreDedupAndRetention|TestLiveChatSelection)$' ./...`
en verde.

**Causa raíz:** no aplica (no es un bug). El descarte está en `youtube.go:425-456`.

## Objetivo

Que cada Super Chat y Super Sticker que llegue por `liveChat/messages` o por el streaming
se guarde en `datos.jsonl` con su tipo, monto, moneda, texto de monto de YouTube, nivel y
(si es sticker) su descripción, sin cambiar el formato de los mensajes comunes ni gastar
cuota extra; y que el dashboard (por vivo elegido, últimas 24 h) y el CSV muestren los
montos brutos con totales por moneda, sin convertir, y por programa.

## Criterios de aceptación

Tests nuevos en `monitor_test.go`, escritos **antes** del código. Para que el primero
falle hoy en ejecución y no solo por compilación, empezar `TestChatPageSuper` afirmando
solo con campos que ya existen (que un Super Sticker con `displayMessage` vacío aparece en
el resultado: hoy se descarta) y guardar esa salida para el PR; después sumar las
afirmaciones sobre los campos nuevos.

- [ ] CA1 (decodificación): `chatPage.messages` sobre una respuesta con estos items
  devuelve, en orden:
  1. `textMessageEvent` "Grande Nelson" → `Message` igual al de hoy (`Super == ""`,
     monto, moneda, nivel y sticker en cero, `Text == "Grande Nelson"`).
  2. `superChatEvent` con `amountMicros: "5000000"`, `currency: "ARS"`,
     `amountDisplayString: "ARS 5,00"`, `tier: 2`, `userComment: "Gracias Nelson"` →
     `Super == "superchat"`, `AmountMicros == 5000000`, `Currency == "ARS"`,
     `AmountText == "ARS 5,00"`, `Tier == 2`, `Text == "Gracias Nelson"`.
  3. `superChatEvent` sin `userComment` y con `displayMessage` no vacío → se guarda con
     `Text == ""` y los datos del pago.
  4. `superStickerEvent` con `displayMessage` vacío y
     `superStickerMetadata.altText: "Perrito aplaudiendo"` → **no se descarta**:
     `Super == "supersticker"`, `Sticker == "Perrito aplaudiendo"`, `Text == ""` y los
     datos del pago.
  5. `superChatEvent` con `amountMicros` como número JSON (`5000000`, sin comillas) →
     `AmountMicros == 5000000`.
  6. `superChatEvent` con `amountMicros: "basura"` → el item se guarda con
     `AmountMicros == 0` y `AmountText` intacto, y **el resto de la página se decodifica
     igual** (un campo raro no tira la página entera).
  7. `textMessageEvent` (u otro tipo, por ejemplo `messageDeletedEvent`) con
     `displayMessage` vacío → se descarta, como hoy.

  Además `Classify(m.Text)` del item 2 es `elogio`. — se verifica con
  `go test -count=1 -run '^TestChatPageSuper$' ./...`.
- [ ] CA2 (mismos endpoints, sin cuota extra): el servidor falso de
  `TestFlowAgainstFakeAPI` devuelve además un `superChatEvent` en `/liveChat/messages`;
  `LiveChats` lo entrega con `Super == "superchat"`, monto y `Program` anotado, y el
  chequeo existente `yt.Units == 2+1+2+1+1+2*5` no cambia. En `TestStreamChat` un pedazo
  del streaming trae un `superStickerEvent` con `displayMessage` vacío y llega en
  `ChatBatch.Msgs` con sus datos; `yt.Units` sigue en 0 y el primer `ChatBatch.Units` en
  5. — se verifica con
  `go test -count=1 -run '^(TestFlowAgainstFakeAPI|TestStreamChat)$' ./...`.
- [ ] CA3 (compatibilidad de `datos.jsonl`): en `TestStoreSuper`, con un `Store` en
  `t.TempDir()`:
  - `json.Marshal` de un `Message` común produce exactamente las claves de hoy (`ID`,
    `Source`, `VideoID`, `Author`, `Text`, `At`, `Likes`, `Replies`, `Program`) y ninguna
    de las nuevas (`omitempty`);
  - una línea escrita con el formato viejo (literal en el test) se carga con los campos
    nuevos en cero;
  - un Super Chat y un Super Sticker agregados con `Add` vuelven idénticos después de
    `OpenStore` (ida y vuelta por el archivo);
  - una línea nueva con pago se decodifica sin error en un struct con solo los campos
    viejos (copia local del `Message` de hoy en el test): un binario anterior la sigue
    leyendo;
  - un mensaje con pago que tiene el mismo `ID` que uno común ya guardado no se re-agrega
    (la dedup por `ID` no cambia).

  — se verifica con `go test -count=1 -run '^TestStoreSuper$' ./...` y con
  `TestStoreDedupAndRetention` sin cambios.
- [ ] CA4 (CSV): `csvHeader` tiene 15 columnas; las 9 primeras son exactamente
  `fuente, fecha, video, programa, autor, categoria, likes, respuestas, texto` y las 6
  nuevas van **al final**: `pago, monto, moneda, monto_youtube, nivel, sticker`. Para un
  mensaje común las 6 nuevas son `""`. Para un Super Chat de 5.500.000 micros en ARS,
  nivel 2: `superchat, 5.5, ARS, <amountDisplayString>, 2, ""`. Para 1.000.000.000 micros:
  `monto == "1000"`; para 1.990.000: `"1.99"` (punto decimal, sin separador de miles, sin
  ceros de más, exacto: se arma con enteros, no con `float64`). Para un sticker:
  `supersticker` y `sticker` con el `altText`. La columna `categoria` sale de
  `Classify(m.Text)` como hoy. — se verifica con
  `go test -count=1 -run '^TestCSVSuper$' ./...` (llama a `csvRow` y a `GET /export.csv`
  con `httptest`).
- [ ] CA5 (totales): una función pura `Supers(msgs []Message, titles map[string]string)`
  en `analyze.go` devuelve, sobre los mensajes con `Super != ""`:
  - `ByCurrency`: por moneda, cantidad y suma de `AmountMicros` como `int64`, **sin
    convertir** (ARS y USD quedan en filas separadas); orden: más cantidad primero, después
    código de moneda. Moneda vacía cuenta aparte con la clave `""`.
  - `ByProgram`: por programa (`programOf`) y moneda, cantidad y suma; orden: programa con
    más pagos primero (empate: nombre), y dentro del programa, moneda.
  - `Last`: los 10 más nuevos, del más nuevo al más viejo.

  Los mensajes comunes no cuentan. Caso de tabla: en "LA MAÑANA", 2 en ARS (5.000.000 y
  2.500.000 micros) y 1 en USD (1.990.000); 1 en ARS (1.000.000) sin `Program` guardado
  en un vivo titulado "🔴RADIO TEST en VIVO | LA TARDE" (cae a `programOf`) →
  `ByCurrency == [{ARS, 3, 8500000}, {USD, 1, 1990000}]` y
  `ByProgram == [{LA MAÑANA, ARS, 2, 7500000}, {LA MAÑANA, USD, 1, 1990000},
  {LA TARDE, ARS, 1, 1000000}]`; con 12 pagos, `Last` tiene 10. — se verifica con
  `go test -count=1 -run '^TestSupers$' ./...`.
- [ ] CA6 (tarjeta del dashboard): `GET /parcial/vivo/analisis?video=<vivo>` con un
  `Store` armado en el test incluye la tarjeta **"6. Super Chats y Super Stickers"** con:
  la tabla por moneda (`Moneda`, `Cantidad`, `Total`), la tabla por programa (`Programa`,
  `Moneda`, `Cantidad`, `Total`), la lista de los últimos (hora `02/01 15:04`, autor, tipo,
  `AmountText` tal como lo manda YouTube y el comentario o "Super Sticker: <altText>") y la
  nota "Montos brutos que pagó cada persona, en su moneda y sin convertir: no es lo que
  cobra el canal." Los totales se escriben con `miles` y dos decimales con coma
  (8.500.000.000 micros → `8.500,00`). Solo cuenta el vivo elegido y las últimas 24 h (un
  pago de otro vivo o de hace 25 h no aparece). Sin pagos en la ventana, la tarjeta dice
  "Sin Super Chats ni Super Stickers en las últimas 24 h." Un comentario con `<b>` sale
  escapado. — se verifica con `go test -count=1 -run '^TestLiveAnalysisSuper$' ./...`.
- [ ] CA7 (feed del chat): en `GET /parcial/vivo/chat?video=<vivo>`, la fila de un Super
  Chat lleva, además del badge de categoría, un badge `class="badge super"` con
  "Super Chat · <AmountText>"; la de un Super Sticker sin texto muestra
  "Super Sticker · <AmountText>" y "Super Sticker: <altText>" en lugar del texto vacío. Las
  filas comunes no cambian. — se verifica con
  `go test -count=1 -run '^TestLiveChatSuper$' ./...`.
- [ ] CA8 (sin ejemplos vacíos en momentos): `newMoment` (`analyze.go:379`) no elige como
  ejemplo un mensaje con `Text` vacío (Super Sticker o Super Chat sin comentario), aunque
  la franja no tenga temas. Los stickers sí cuentan en mensajes y personas de la franja
  (es participación). — se verifica con un caso nuevo en
  `go test -count=1 -run '^TestTopMomentsEdges$' ./...`.
- [ ] CA9 (cuota sin cambios): no hay llamadas nuevas a la API ni cambios de costo:
  `git diff main --stat` no lista `worker.go`, `chatParams` y los `cost` pasados a `get`
  no cambian (`git diff main -- youtube.go` solo toca `Message`, `chatPage` y
  `chatPage.messages` más el helper de enteros), y los chequeos de unidades de
  `TestFlowAgainstFakeAPI`, `TestStreamChat` y `TestWorkerStreamsChat` pasan sin cambiar
  sus números. Las cifras de cuota del README no cambian. — se verifica con
  `go test -count=1 -run '^(TestFlowAgainstFakeAPI|TestStreamChat|TestWorkerStreamsChat)$' ./...`
  y revisando `git diff main -- youtube.go worker.go` en el PR.
- [ ] CA10 (README): `README.md` describe la tarjeta nueva en la lista de secciones del
  análisis del vivo, las 6 columnas nuevas del CSV (al final, vacías en mensajes comunes),
  que el monto es bruto y sin convertir, y que los pagos se cuentan desde que se actualiza
  el monitor. — se verifica con `grep -n 'monto_youtube' README.md` y
  `grep -n -i 'super chat' README.md`.
- [ ] CA11 (navegador, manual): la tarjeta y los badges se ven bien en modo claro y oscuro
  y en ancho de celular (≤ 600 px), con los colores nuevos definidos en ambos temas. Lo
  verifica la persona (ver pregunta abierta 4 sobre con qué datos).
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde: `gofmt -l .` vacío,
  `go vet ./...`, `staticcheck`, `go build ./...` y
  `GOOS=windows GOARCH=amd64 go build ./...`, `go test -race -count=1 ./...` y
  `go test -count=5 ./...`.

## Diseño propuesto

### Almacenamiento: campos opcionales en `Message` (recomendado)

Agregar a `Message` (`youtube.go:228`) campos planos con `json:",omitempty"` (la clave
sigue siendo el nombre de Go, como pide `AGENTS.md`):

```go
Super        string `json:",omitempty"` // "superchat", "supersticker" o "" (mensaje común)
AmountMicros int64  `json:",omitempty"` // monto bruto en millonésimas de la moneda
Currency     string `json:",omitempty"` // código ISO 4217, por ejemplo "ARS"
AmountText   string `json:",omitempty"` // amountDisplayString, como lo muestra YouTube
Tier         int    `json:",omitempty"`
Sticker      string `json:",omitempty"` // superStickerMetadata.altText
```

Con constantes `SuperChat = "superchat"` y `SuperSticker = "supersticker"`. `Source`
sigue siendo `"chat"`.

Por qué esta opción:

- **Compatible en los dos sentidos:** las líneas viejas se cargan con ceros; las líneas
  comunes nuevas son byte a byte iguales a las de hoy (`omitempty`); un binario viejo
  ignora las claves que no conoce (`encoding/json` descarta campos desconocidos).
- **Retención y dedup gratis:** viven en `datos.jsonl`, así que les aplica la retención de
  30 días de `OpenStore` y la dedup por `ID` sin código nuevo; un archivo aparte
  necesitaría su propia carga, retención con tmp + `os.Rename` y un join por `ID` para el
  CSV y el feed.
- **Sin tocar el worker ni la concurrencia:** `TagProgram` y `st.Add` ya manejan cualquier
  `Message`; los campos son valores (sin punteros), así que las copias de `Snapshot` y
  `LastChat` siguen siendo independientes.
- **Costo:** solo las líneas con pago crecen (unos 100 bytes); son pocas.

Descartadas:

- *Archivo aparte `datos.superchats.jsonl` con retención propia:* más código (carga,
  retención, join, otra superficie para la regla de privacidad) para el mismo resultado.
- *Puntero a un struct `*SuperDetails`:* `omitempty` funciona igual, pero comparte memoria
  entre las copias de `Snapshot`; los campos planos son más simples.
- *Guardar el monto en `Text`:* rompe temas, palabras y clasificación.

**Datos ya guardados:** los Super Chats que entraron como chat común siguen así (sin
monto) y no se "re-capturan": tienen el mismo `ID`, `Store.Add` los descarta por
`seen[m.ID]` (`store.go:182`) y además el token de página solo avanza. Los Super Stickers
con `displayMessage` vacío nunca se guardaron. Por eso la tarjeta y el README aclaran que
los pagos se cuentan desde que se actualizó el monitor.

### Decodificación (`youtube.go`)

1. En `chatPage` (`youtube.go:425`) sumar a `Snippet`:
   `Type string` (`json:"type"`), `SuperChatDetails` (`amountMicros`, `currency`,
   `amountDisplayString`, `userComment`, `tier`) y `SuperStickerDetails` (`amountMicros`,
   `currency`, `amountDisplayString`, `tier`, `superStickerMetadata.altText`).
   `amountMicros` y `tier` se decodifican como `json.RawMessage` y se convierten con un
   helper chico (por ejemplo `apiInt(raw) int64`, que acepta `"5000000"` y `5000000` y
   devuelve 0 si no puede), para que un valor inesperado no haga fallar el `Unmarshal` de
   la página entera (en el streaming eso cortaría la conexión y perdería el pedazo).
2. En `chatPage.messages` (`youtube.go:444`):
   - `superChatEvent` → `Super = SuperChat`, datos de `superChatDetails`,
     `Text = userComment` (puede ser `""`).
   - `superStickerEvent` → `Super = SuperSticker`, datos de `superStickerDetails`,
     `Sticker = altText`, `Text = ""`.
   - Cualquier otro tipo (o sin `type`, como en los tests de hoy) → igual que hoy,
     incluido descartar si `displayMessage` está vacío.
   - Los dos tipos pagos **nunca** se descartan por texto vacío.

   Por qué `Text` sale de `userComment` y no de `displayMessage`: `userComment` está
   documentado como el comentario de la persona; `displayMessage` es lo que arma YouTube
   para mostrar y podría incluir el monto o el nombre (no verificado, ver supuestos). Así
   el monto nunca contamina temas, palabras, preguntas ni `Classify`, y no hace falta tocar
   el clasificador.

### Formato de montos

- Totales: siempre en `int64` de micros, por moneda; nunca se suman monedas distintas ni
  se convierte.
- CSV `monto` (`main.go`): decimal exacto con punto, sin separador de miles ni ceros de
  más, armado con enteros (`micros / 1_000_000` y el resto con 6 dígitos sin ceros
  finales). `""` si no hay pago.
- Dashboard (`server.go`): helper `money(micros int64) string` → parte entera con `miles`
  y dos decimales con coma (centavos truncados: `micros / 10_000`), por ejemplo
  `8.500,00`. El `amountDisplayString` se muestra tal cual en la lista y el feed.

### CSV (`main.go:227-234`)

Agregar al final de `csvHeader`: `pago, monto, moneda, monto_youtube, nivel, sticker`, y
en `csvRow` los valores (vacíos en mensajes comunes; `nivel` vacío si es 0). Van al final
para que quien ya lee el CSV por posición (o lo sube a Claude) siga teniendo las 9
columnas en el mismo lugar. Lo usan sin cambios `writeCSV` (informe) y `export`.

### Totales (`analyze.go`)

`Supers(msgs []Message, titles map[string]string) SuperSummary` con `ByCurrency`,
`ByProgram` y `Last` (ver CA5). Solo reglas, sin estado, como el resto de `analyze.go`.
En `newMoment` (`analyze.go:390-401`) saltear mensajes con
`strings.TrimSpace(msg.Text) == ""` al elegir ejemplos (CA8).

### Dashboard (`server.go`, `templates/partials.html`, `templates/page.html`)

- `liveAnalysis` (`server.go:301`) ya tiene `msgs` del vivo elegido en 24 h y los títulos:
  agregar `"Super": superView(Supers(msgs, titles))` con los textos ya formateados.
- Tarjeta nueva **"6. Super Chats y Super Stickers"** en `vivo-analisis`, después de la 5:
  tabla por moneda, tabla por programa, lista de los últimos 10 (`qlist`) y la nota de
  monto bruto. Tablas con `<th>`, la sección con `aria-label`. Que entre en ancho de
  celular sin scroll horizontal.
- Feed (`vivo-chat`, `templates/partials.html:69-77`): badge `super` con el tipo y
  `AmountText`; si `Text` está vacío y es sticker, mostrar "Super Sticker: <Sticker>".
- CSS: `.badge.super` con un par de tokens nuevos (por ejemplo `--paid-bg` y
  `--paid-text`) definidos en **los dos** temas de `page.html`. `super` no choca con
  ninguna constante `Cat*`. Sin `template.HTML` para comentarios ni nombres.
- No se toca `Store`: `LastChat` ya devuelve los pagos porque son `Source == "chat"`.

### Modo informe

`LiveChats` pasa por la misma decodificación, así que el CSV del informe trae las columnas
nuevas sin más. El Markdown del informe no cambia (ver pregunta abierta 2).

### Orden sugerido

1. `TestChatPageSuper` (falla hoy: el sticker vacío se descarta) → decodificación.
2. `TestStoreSuper` y `TestCSVSuper` → campos y CSV.
3. `TestSupers` y el caso de `TestTopMomentsEdges` → `analyze.go`.
4. `TestLiveAnalysisSuper` y `TestLiveChatSuper` → templates y CSS.
5. Extender `TestFlowAgainstFakeAPI` y `TestStreamChat`; README.

## Fuera de alcance

- Ingreso neto del canal, reparto de YouTube, conversión de monedas o tipo de cambio
  (requiere OAuth o fuentes externas).
- Otros eventos del chat (`newSponsorEvent`, `memberMilestoneChatEvent`,
  `membershipGiftingEvent`, `giftMembershipReceivedEvent`, `pollEvent`,
  `messageDeletedEvent`, `userBannedEvent`, etc.): siguen como hoy (se guardan como chat
  común si traen `displayMessage`, se descartan si no).
- Recuperar los Super Chats ya guardados como chat común o los stickers ya perdidos.
- Totales globales de todos los vivos o de más de 24 h en el dashboard (el CSV ya trae todo
  el histórico de 30 días).
- Cambios en `Classify`, en el worker, en `Store`, en la cuota o en `tools/preview`.
- El desfasaje que ya existe entre la numeración de tarjetas del README (incluye
  "usuarios que mandaron spam o insultos") y la del template: se ajusta solo lo necesario
  para sumar la tarjeta nueva.

## Riesgos

- **Cuota:** ninguno esperado. Los detalles llegan en `snippet`, que ya se pide
  (`youtube.go:459`); no hay requests nuevos. Se verifica con CA2 y CA9.
- **Compatibilidad de `datos.jsonl`:** campos nuevos con `omitempty` y nombres de Go;
  líneas comunes idénticas a las de hoy; un binario anterior lee las líneas nuevas
  ignorando las claves extra (pierde el monto, no el mensaje). Se verifica con CA3.
- **Decodificación frágil:** si `amountMicros` o `tier` vinieran con otro tipo, un
  `Unmarshal` estricto tiraría la página entera (o cortaría el streaming). Mitigación:
  `json.RawMessage` + helper tolerante (CA1 casos 5 y 6).
- **Privacidad:** el CSV y el dashboard muestran cuánto pagó cada persona con su nombre.
  Es información pública del chat (YouTube la muestra a todos) y vive en `datos.jsonl` con
  la misma retención de 30 días; no se crea otro almacenamiento. Conviene aclararlo en el
  README junto a la advertencia de no subir datos.
- **Interpretación del monto:** alguien podría leer los totales como ingreso. Mitigación:
  nota fija en la tarjeta y en el README ("monto bruto, sin convertir, no es lo que cobra
  el canal") y nunca sumar monedas distintas.
- **Concurrencia:** sin cambios; campos por valor, handlers leen copias. `go test -race`
  en los gates.
- **Windows:** sin código dependiente de plataforma; build de Windows en los gates.
- **Análisis existentes:** los stickers cuentan en participación y en el total de mensajes
  del vivo (intencional: es participación); no aportan palabras. CA8 evita ejemplos vacíos
  en momentos.

## Supuestos no verificados (sin red)

Basados en la documentación de YouTube Data API v3 (recurso `liveChatMessages`), no
confirmados contra una respuesta real porque no se puede usar la API ni `datos.jsonl`:

1. `snippet.type` vale `superChatEvent` / `superStickerEvent` para estos pagos, y
   `superChatDetails` / `superStickerDetails` vienen dentro de `snippet` con
   `part=snippet,authorDetails` (sin pedir otro `part`).
2. `liveChat/messages/stream` devuelve el mismo recurso con los mismos campos que
   `liveChat/messages`.
3. `amountMicros` llega como string (convención de la API para enteros de 64 bits) y
   `tier` como número; el diseño tolera ambas formas igual.
4. El contenido de `displayMessage` en eventos pagos (si trae el monto, el nombre o solo el
   comentario, y si viene vacío en stickers) es desconocido; el diseño no depende de eso.
5. Que los canales del piloto (Radio Rivadavia y extras) tengan Super Chat activado: si no
   lo tienen, la tarjeta va a decir siempre "Sin Super Chats…".

Se confirma con la primera captura real: que la persona (no un agente) revise una línea
con `"Super":"superchat"` en su `datos.jsonl` después de unos días de uso.

## Preguntas abiertas

1. **Texto del Super Chat:** la spec usa `userComment` como `Text` (el monto nunca entra en
   el texto). ¿Preferís conservar `displayMessage` como hasta ahora, a costa de que pueda
   traer el monto o el nombre?
2. **Informe Markdown:** ¿sumar una sección "Super Chats" con totales por moneda y por
   programa al `.md` del modo informe? La spec solo cambia su CSV.
3. **Tarjeta vacía:** la spec la muestra siempre con "Sin Super Chats ni Super Stickers en
   las últimas 24 h." (como las demás tarjetas). ¿O preferís ocultarla cuando no hay
   pagos, para no sumar espacio en canales sin Super Chat?
4. **Verificación en el navegador (CA11):** `tools/preview` no simula chat y el
   `datos.jsonl` local todavía no tiene pagos. ¿Alcanza con verificar después de que haya
   capturas reales, o querés que se sume (en otra tarea) un modo de datos sintéticos a la
   vista previa?
