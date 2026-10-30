# Spec: los 5 momentos con más % de participación, gráfico legible y tabla sin scroll

- **Estado:** borrador
- **Tipo:** mejora
- **Rama:** `feat/top-momentos-participacion`
- **Origen:** pedido de Julian Barbieri (textual):
  "3) estoy visualizando mal el grafico que implemento recién. Se ve todo muy pequeño y no
  me gusta que haya una barra que desplace para mostrar información oculta, quiero que se
  muestre todo junto.
  4) con los datos que se estan obteniendo de views (ejemplo "1.573 mirando") se podria
  obtener el numero de cuanta gente estuvo mirando ese momento y que porcentaje de
  participacion hubo en ese momento. Quiero que por lo menos me muestres los 5 momentos
  con más participación asi tengo como compararlos".
  Decisiones ya tomadas por la persona: ranking **por % de participación**, se muestran
  **siempre** los 5 mejores (no solo los que pasan un umbral de pico), cada uno con cuánta
  gente miraba y el %; mínimo de personas para entrar; franjas sin dato de espectadores
  afuera; tabla más simple sin scroll horizontal, con temas y ejemplos debajo de cada fila;
  gráfico más grande y legible.
- **Aprobada por / fecha:** pendiente

## Contexto

Es una mejora sobre la tarjeta "6. Momentos con más participación (para reels)" que entró
con `docs/specs/2026-10-30-picos-participacion-chat.md` (PR #7, `0212942`). No hay bug que
reproducir. Línea de base verificada en esta rama (igual a `main` en `c630563`):
`go build ./...`, `go vet ./...` y
`go test -count=1 -run '^(TestParticipation|TestPeakMoments|TestPeakMomentsBorders|TestParticipationChartAria|TestLiveParticipationView|TestReelLink|TestParticipationBorders)$' ./...`
en verde.

### Qué hace hoy el ranking (punto 4)

- `Participation` (`analyze.go:299-347`) ya calcula por franja de 5 minutos: personas
  distintas (`Authors`), espectadores (`Viewers`, promedio redondeado de las muestras de la
  franja, 0 = sin dato) y `Pct = Authors / Viewers * 100` (`analyze.go:339`). O sea, el %
  por franja **ya existe**; lo que falta es usarlo para elegir.
- `PeakMoments` (`analyze.go:388-413`) elige por **personas**: umbral
  `max(minPersonasPico, factorPico × mediana)` (`analyze.go:361-372`), agrupa franjas pico
  seguidas y ordena por personas de la franja más fuerte. Si nadie pasa el umbral, la tabla
  queda vacía ("Todavía no hubo picos", `templates/partials.html:226`).
- La tabla muestra **totales del momento** (mensajes y personas de todas sus franjas,
  `analyze.go:430`) junto a "mirando" y "%" de **la franja más fuerte**
  (`server.go:422-424`, encabezados en `templates/partials.html:213-214`). Es lo que la
  revisión anterior marcó como confuso: "38 personas" y "1.573 mirando" en la misma fila no
  dan el "%" que se muestra al lado.

### Qué se ve hoy (punto 3), medido en el código

- Gráfico (`participationChart`, `server.go:454-515`; template
  `templates/partials.html:196-211`): `viewBox="0 -8 720 140"` (`server.go:455`) con
  `width="100%"` y `height:auto`, así que **todo escala con el ancho**, letras incluidas.
  288 barras de 2 unidades de ancho y 0,5 de separación (`server.go:491`). Las letras son
  `font-size: 11px` en unidades del SVG (`templates/page.html:75`). Los ticks van en otro
  SVG del mismo ancho (`templates/partials.html:208-210`).
- Ancho real de la tarjeta (calculado del CSS, `box-sizing: border-box`): `main` tope
  1200 px con 16 px de padding (`page.html:52`); la tarjeta `.vivo` y la tarjeta `.full`
  tienen 1 px de borde y 16 px de padding cada una (`page.html:56`). Eso deja
  1200 − 32 − 34 − 34 = **1.100 px** de contenido en escritorio. En un celular de 360 px
  quedan 360 − 32 − 34 = 294 px para `.vgrid`, pero `.vgrid` exige columnas de al menos
  300 px (`page.html:110`): la grilla **desborda 6 px** y la tarjeta queda con 266 px.

| Ventana | Ancho del SVG | Escala (÷ 720) | Alto (148 u) | Letra (11 u) | Barra (2 u) |
|---|---|---|---|---|---|
| ≥ 1.232 px | 1.100 px | 1,53 | 226 px | 16,8 px | 3,1 px |
| 1.024 px | 924 px | 1,28 | 190 px | 14,1 px | 2,6 px |
| 360 px | 266 px | 0,37 | 55 px | 4,1 px | 0,74 px |

  Son números calculados, no observados: no sé con qué ancho mira la persona. En
  escritorio el alto no es chico, pero la escala es la del pico de personas: con un pico de
  30 y una franja típica de 2 o 3, casi todas las barras miden menos de 15 u (unos 20 px).
  En celular todo queda ilegible (letra de 4 px, gráfico de 55 px de alto).
- Tabla: 9 columnas (`templates/partials.html:213-214`) dentro de `<div class="scroll">`
  (`templates/partials.html:212`, `.scroll { overflow-x: auto; }` en `page.html:98`): es la
  barra de desplazamiento que molesta. Temas, ejemplos y el texto de `reelLink` (hasta 70
  caracteres) van en columnas propias y fuerzan el ancho.

**Causa raíz:** no aplica (mejora).

## Objetivo

La tarjeta 6 muestra, para el vivo elegido, las **5 franjas de 5 minutos con más % de
participación** (personas que escribieron / gente mirando en esa franja), cada una con
cuántos miraban, cuántas personas escribieron y el %, de forma que los tres números cierren
entre sí. El gráfico muestra ese % con alto fijo y letras que no se achican, marca las 5
franjas, y la tabla entra entera en el ancho de la tarjeta (también a 360 px) sin scroll
horizontal. Sin cambios de cuota, de `Store` ni de `datos.jsonl`.

## Definiciones (las usan los criterios)

Se mantienen sin cambios las de la spec anterior para **franja**, **ventana** (288 franjas,
últimas 24 h), **mensajes**, **personas**, **espectadores** y **participación de la franja**
(`Participation` no cambia). También la **participación típica** (mediana del % de las
franjas con dato, incluidas las de 0 personas), que pasa a ser solo una referencia.

- **Franja elegible:** `Viewers > 0` y `Authors >= minPersonasTop` (`= 3`). Las franjas sin
  dato de espectadores y las de menos de 3 personas no entran en el ranking.
- **Ranking:** franjas elegibles ordenadas por `Pct` (mayor primero); desempate: más
  personas, después la más reciente.
- **Top:** se recorre el ranking y se elige una franja si su inicio está a **15 minutos o
  más** (`minSeparacion`) del inicio de todas las ya elegidas; se para al llegar a
  `maxMomentos = 5`. `Rank` = orden de elección (1 a 5). Si hay menos de 5 que cumplan, se
  muestran las que haya (ver mensajes E0–E2 en CA6).
- **Momento:** es **una franja de 5 minutos**, no un grupo de franjas. Su % es el de la
  franja y cierra con lo que se muestra: `Pct = Personas / Mirando × 100`, con `Mirando` =
  promedio redondeado de las lecturas de esa franja (el mismo número que usa
  `Participation`).

Por qué por franja y no por momento agrupado: (1) el numerador (personas distintas) se
acumula durante el intervalo y el denominador ("mirando ahora") es instantáneo, así que un
momento de 15 minutos tendría más % que uno de 5 solo por durar más; con franjas de duración
fija los 5 son comparables, que es lo que pidió la persona. (2) Sin umbral de pico no hay un
criterio natural para agrupar: de día casi todas las franjas tienen 3 personas o más y se
formaría un solo "momento" de horas. (3) Un reel dura de segundos a 3 minutos: 5 minutos ya
alcanza para ubicarlo. La separación de 15 minutos evita que una misma charla ocupe varios
puestos del top.

**Qué pasa con el concepto de "pico":** se elimina. Ya no hay umbral `max(3, 2 × mediana)`,
línea de umbral, "X veces lo normal" (`Moment.Ratio`) ni clase `pico`. El mínimo de 3
personas es lo único que queda del umbral. La participación típica se mantiene como línea
de referencia en el gráfico y en el texto de arriba, para comparar los 5 contra lo normal
del vivo.

## Criterios de aceptación

- [ ] CA1: `TopMoments(slots []Slot, titles map[string]string) []Moment` (función pura en
  `analyze.go`) elige según las definiciones. Tabla de casos con franjas armadas a mano
  (`Pct` = `Authors/Viewers*100`, como `Participation`):
  - sin franjas, o franjas sin ningún dato de espectadores → ningún momento;
  - (10 personas, 1.000 mirando) = 1 %, (5, 100) = 5 %, (3, 50) = 6 %, (2, 10) = 20 %,
    (6, sin dato) → orden 6 %, 5 %, 1 %: la de 20 % queda afuera por tener 2 personas y la
    de 6 personas por no tener dato;
  - mínimo justo: 3 personas entra, 2 no;
  - empate de %: (4, 200) y (6, 300) → primero la de 6 personas; empate total → primero la
    más reciente;
  - `Rank` vale 1, 2, 3… en el orden devuelto.
  — se verifica con `go test -count=1 -run '^TestTopMoments$' ./...`.
- [ ] CA2: separación y tope: si la mejor es la franja `i`, las franjas `i+1` e `i+2`
  (5 y 10 minutos) no se eligen aunque sean las siguientes en %, y la `i+3` (15 minutos
  justos) sí; lo mismo hacia atrás (`i−1`, `i−2` afuera, `i−3` adentro). Con 7 franjas
  elegibles separadas devuelve 5 en orden de %. Con 2 elegibles devuelve 2. Con 6
  elegibles pegadas entre sí (todas a menos de 15 minutos de la mejor) devuelve 1.
  — se verifica con `go test -count=1 -run '^TestTopMoments(Borders)?$' ./...`.
- [ ] CA3: cada momento trae, de los mensajes de **su franja**, el programa más frecuente
  (`mainProgram`/`programOf`), los temas (`TopTopics(msgs, 3)`) y hasta 2 ejemplos sin spam
  ni insultos, ordenados por hora, que contengan el tema principal (o los primeros si no
  hay tema): mismas reglas que hoy. Los casos de texto de `TestPeakMoments` y
  `TestPeakMomentsBorders` (programa "LA TARDE", tema "dólar blue", ejemplos `t3`/`t4` y
  `e2`/`e1`) se portan a los tests nuevos y siguen pasando. — se verifica con
  `go test -count=1 -run '^TestTopMoments(Borders)?$' ./...`.
- [ ] CA4: `participationChart` en **modo %** (hay al menos una franja con dato de
  espectadores, o sea `ok` de `Participation`): devuelve `W = len(slots)` (viewBox
  `0 0 W 100`); por franja `i`, una barra con `X = i + 0,1`, `W = 0,8`; alto
  `H = Pct / escala × 100` (mínimo 1 si `Pct > 0`) solo para las franjas elegibles, `H = 0`
  para las demás; `escala = max(mayor Pct elegible, típica)`, así la mayor elegible mide
  100 cuando la típica es menor. Las franjas del top llevan `Class == "top"`. El `<title>`
  de cada franja dice `HH:MM a HH:MM · programa · N mensajes · N personas` y además
  `· N mirando (X,X %)`, o `· sin dato de espectadores`, o, si tiene dato y menos de 3
  personas, `· menos de 3 personas: no entra en el ranking`; las del top suman
  `· N.º k de los 5 con más participación`. Devuelve las marcas `{Left: (i+0,5)/W×100,
  Rank}` de las franjas del top, ticks cada 36 franjas (3 h) con `Left = i/W×100`, la línea
  de la típica `TypicalY = 100 − típica/escala×100`, el rótulo `máx. X,X %` y el
  `aria-label` "Participación del chat cada 5 minutos en las últimas 24 horas (personas que
  escribieron sobre los que miraban); la más alta: X,X % a las HH:MM (N personas de N
  mirando)" o, sin elegibles, "…; todavía sin franjas con dato de espectadores y al menos 3
  personas". — se verifica con `go test -count=1 -run '^TestParticipationChart$' ./...`
  (tabla de casos, `now` fijo, sin servidor).
- [ ] CA5: `participationChart` en **modo personas** (ninguna franja con dato: canal que
  oculta el contador o vivo sin muestras guardadas): alto = `Authors / máximo × 100`, sin
  clase `top`, sin marcas ni línea de típica, rótulo `máx. N personas` y `aria-label`
  "Personas que escribieron en el chat cada 5 minutos en las últimas 24 horas; máximo de N
  personas a las HH:MM" o "…; todavía sin mensajes". — se verifica con
  `go test -count=1 -run '^TestParticipationChart$' ./...`.
- [ ] CA6: `GET /parcial/vivo/analisis?video=<ID>` (con `Store` y datos armados en el
  test) muestra en la tarjeta 6:
  - el SVG con `preserveAspectRatio="none"`, `role="img"`, el `aria-label` de CA4, un
    `<g><title>` por franja y `class="bar top"` en las del top; las marcas numeradas y los
    ticks como HTML (no `<text>` dentro del SVG de barras);
  - una `<table>` con 4 columnas: `Momento` (número, `02/01 15:04 a 15:04` y el programa
    debajo), `Mirando`, `Personas` y `%`; debajo de cada fila, una fila
    `<td colspan="4">` con "Para el reel:" y el link (`target="_blank" rel="noopener"`) o
    el texto de `reelLink`, los temas y los ejemplos. Para el vivo de prueba con 6
    personas y 150 mirando, la fila dice `150`, `6` y `4,0 %`;
  - dentro de la tarjeta 6 no aparece `class="scroll"` ni `overflow-x`;
  - un autor `<script>x</script>` sale escapado (`&lt;script&gt;`) y no aparece sin escapar;
  - no aparecen "veces lo normal", "pico: desde", `class="umbral"` ni "Todavía no hubo
    picos";
  - mensajes cuando hay menos de 5: **E0** sin dato de espectadores en la ventana → sin
    tabla y "Sin registro de cuántos miraban en las últimas 24 h: todavía no se puede armar
    el ranking por %."; **E1** con dato pero ninguna elegible → "Ninguna franja con dato de
    espectadores tuvo al menos 3 personas escribiendo."; **E2** de 1 a 4 → la tabla y "Solo
    N de 5: el resto de las franjas no llega a 3 personas, no tiene dato de espectadores o
    está a menos de 15 minutos de una elegida.";
  - se mantienen los avisos de hoy: "YouTube no informa cuántos miran este vivo" si
    `Concurrent == 0`, el de franjas sin muestra (`Missing`) y "N mirando ahora".
  — se verifica con `go test -count=1 -run '^TestLiveParticipationView$' ./...`.
- [ ] CA7: CSS en `templates/page.html` sin colores nuevos: `.vgrid` pasa a
  `minmax(min(300px, 100%), 1fr)`; el contenedor del gráfico tiene alto fijo de 240 px y
  180 px con `@media (max-width: 600px)`; textos del gráfico (rótulo, marcas, ticks) en
  HTML a 12 px; en ≤ 600 px se ocultan los ticks pares (quedan 00, 06, 12, 18, 24 h);
  `.bar.top` y las marcas usan `--warn-text`; la línea de la típica usa `--text-muted`
  punteada con `vector-effect: non-scaling-stroke`; la fila de detalle tiene
  `overflow-wrap: anywhere`; se borran `.bar.pico` y `.umbral`. — se verifica con
  `git diff main -- templates/page.html | grep '^+' | grep -ciE '#[0-9a-f]{3,6}\b'`
  (debe dar 0) y con la revisión del diff.
- [ ] CA8: sin `template.HTML` con texto de usuarios, sin JS propio ni recursos externos:
  `grep -n 'template.HTML' *.go` no lista nada, `grep -n '<script' templates/*.html` solo
  lista `/static/htmx.min.js` y `go.mod` sigue sin `require`.
- [ ] CA9: cuota, store y worker sin cambios: `git diff --quiet main -- youtube.go
  worker.go store.go` sale con código 0. Las cifras de cuota del README no cambian.
- [ ] CA10: no queda rastro del concepto de pico: `grep -nE
  'PeakMoments|peakThreshold|factorPico|minPersonasPico|veces lo normal|[^t]Ratio\b' *.go
  templates/*.html README.md AGENTS.md` no lista nada (`[^t]` es para no confundir `preserveAspectRatio` con `Moment.Ratio`). El README (sección 3, punto 6) y
  `AGENTS.md` (mapa de `analyze.go`) describen `TopMoments`, el ranking por %, el mínimo
  de 3 personas, la separación de 15 minutos y el gráfico de %. — se verifica con el
  `grep` y revisando `git diff main -- README.md AGENTS.md`.
- [ ] CA11 (manual, lo hace una persona; la evidencia va en el PR): en el navegador, con
  API simulada (`YT_BASE_URL=http://localhost:9999 YT_API_KEY=x`) o con los datos locales,
  en modo claro y oscuro, a 360 px y a 1.200 px de ancho: la página no tiene scroll
  horizontal (`document.documentElement.scrollWidth <= innerWidth` en la consola), la
  tabla de la tarjeta 6 entra entera, las letras del gráfico se leen (12 px) y las barras y
  marcas del top se distinguen en los dos temas.
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde: `gofmt -l .` vacío,
  `go vet ./...`, `staticcheck` limpio, `go build ./...` y
  `GOOS=windows GOARCH=amd64 go build ./...` OK, `go test -race -count=1 ./...` y
  `go test -count=5 ./...` en verde.

## Diseño propuesto

Orden (tests primero): `TestTopMoments` y `TestTopMomentsBorders` (hoy no compilan porque
`TopMoments` no existe: guardar esa salida como evidencia), `TestParticipationChart`, y
después adaptar `TestLiveParticipationView`. Se **borran** `TestPeakMoments`,
`TestPeakMomentsBorders` y `TestParticipationChartAria` porque prueban el comportamiento
que se reemplaza a propósito; sus casos de texto (programa, temas, ejemplos) se portan
(CA3). `TestParticipation`, `TestParticipationBorders`, `TestReelLink*`, `TestStoreViewers*`
y `TestWorkerRecordsViewers` no cambian.

### 1. Análisis (`analyze.go`)

```go
const (
	slotStep       = 5 * time.Minute
	slotCount      = 288
	minPersonasTop = 3                // con menos, 2 personas de madrugada con 20 mirando ganarían
	minSeparacion  = 15 * time.Minute // que una misma charla no ocupe varios puestos
	maxMomentos    = 5
)

// Moment es una de las franjas con más participación.
type Moment struct {
	Slot            // la franja de 5 minutos (N, Authors, Viewers, Pct)
	Rank     int    // 1 a maxMomentos
	Program  string
	Topics   []Count
	Examples []Message
}

func TopMoments(slots []Slot, titles map[string]string) []Moment
```

- `TopMoments`: filtra elegibles, `sort.SliceStable` por `Pct`, `Authors`, `Start` (más
  reciente), recorrido con la separación, corta en 5. `newMoment(s Slot, rank int, titles)`
  conserva la lógica actual de programa, temas y ejemplos (`analyze.go:415-454`) sobre
  `s.Msgs`.
- Se borran `PeakMoments`, `peakThreshold`, `factorPico`, `minPersonasPico`,
  `Moment.Best`, `Moment.Ratio` y los totales `Moment.N/Authors` (ahora vienen de la
  franja embebida). `Participation` y `median` quedan igual.

### 2. Servidor (`server.go`)

- `participationView`: llama a `TopMoments`; `MomentRow` queda con `Moment`, `End`
  (`Start + slotStep`), `ViewersText` (`miles(Viewers)`), `PctText` (`pct(Pct)`),
  `TopicText`, `Href`, `Hint` (de `reelLink`, sin cambios) y se borra `RatioText`. Pasa al
  template el mensaje E0/E1/E2 que corresponda.
- `participationChart(slots []Slot, moments []Moment, typical float64, ok bool, titles
  map[string]string) map[string]any`: modo % si `ok`, modo personas si no (CA4, CA5). Se
  reusa `Bar` con `Class = "top"`. Devuelve `Bars`, `W`, `Marks`, `Ticks`, `TypicalY`
  (solo modo %), `Max` (rótulo) y `Aria`. Las posiciones de marcas y ticks van como
  porcentaje (`printf "%.2f"`) para usarlas en `style="left: …%"`, igual que hoy se hace con
  `width` en las barras de categorías (`templates/partials.html:48`). Se saca el `import
  "math"` si queda sin uso.

### 3. Template (`templates/partials.html`, tarjeta 6)

```html
<div class="pmarks" aria-hidden="true">{{range .Marks}}<span style="left: {{.Left}}%">{{.Rank}}</span>{{end}}</div>
<div class="pchart">
  <svg viewBox="0 0 {{.W}} 100" preserveAspectRatio="none" role="img" aria-label="{{.Aria}}">
    … <g><title>…</title><rect class="hit" x=i width=1 height=100/><rect class="bar …"/></g> …
    <line class="tipica" … y1="{{.TypicalY}}" y2="{{.TypicalY}}"/>   (solo modo %)
    <line … y1=100 y2=100 stroke="var(--grid)"/>                     (base)
  </svg>
</div>
<div class="pticks" aria-hidden="true">{{range .Ticks}}<span style="left: {{.Left}}%">{{.Label}}</span>{{end}}</div>
```

- Por qué HTML para los textos: con `preserveAspectRatio="none"` el SVG se estira al alto
  fijo y al ancho de la tarjeta, y un `<text>` adentro se deformaría. En HTML las letras
  miden siempre 12 px, a cualquier ancho. Las barras siguen armadas en Go dentro del SVG.
- Arriba del gráfico, una línea `muted`: "Los 5 momentos de 5 minutos en que escribió el
  mayor % de los que miraban (con al menos 3 personas). Participación típica del vivo:
  X,X % (línea punteada) · N mirando ahora". En modo personas: "YouTube no informa cuántos
  miraban: el gráfico muestra personas y no hay ranking por %".
- Tabla (sin `.scroll`): encabezados `Momento`, `Mirando`, `Personas` (con
  `title="Personas distintas que escribieron en esos 5 minutos"`) y `%` (en negrita, es el
  criterio). Fila de detalle `<tr class="det"><td colspan="4">`: "Para el reel: …", temas,
  ejemplos (`<strong>Autor</strong>: texto`).
- Cuentas de ancho a 360 px (tarjeta de 260 px con el arreglo de `.vgrid`), estimadas a
  13 px: `Mirando` ≈ 64 px, `Personas` ≈ 70 px, `%` ≈ 58 px (hasta "150,0 %"); quedan
  ≈ 68 px para `Momento`, que corta líneas (número y fecha, hora, programa con
  `overflow-wrap: anywhere`). Con 6 columnas, como sugería el pedido (programa y link
  aparte), el mínimo pasaba de 300 px: por eso el programa va dentro de `Momento` y el link
  en la fila de detalle. Se confirma en CA11.

### 4. CSS (`templates/page.html`)

Según CA7. Ejemplo:

```css
.vgrid { … grid-template-columns: repeat(auto-fit, minmax(min(300px, 100%), 1fr)); }
.pchart svg { display: block; width: 100%; height: 240px; }
.pmarks, .pticks { position: relative; height: 16px; font-size: 12px; color: var(--text-muted); }
.pmarks span, .pticks span { position: absolute; transform: translateX(-50%); white-space: nowrap; }
.pticks span:first-child { transform: none; } .pticks span:last-child { transform: translateX(-100%); }
.pmarks span { color: var(--warn-text); font-weight: 600; }
.bar.top { fill: var(--warn-text); }
.tipica { stroke: var(--text-muted); stroke-dasharray: 4 3; vector-effect: non-scaling-stroke; }
.tops tr.main td { border-bottom: 0; } .tops tr.det td { overflow-wrap: anywhere; }
.tops td.n { white-space: nowrap; }
@media (max-width: 600px) { .pchart svg { height: 180px; } .pticks span:nth-child(even) { display: none; } }
```

Números elegidos: 240 px en escritorio es algo más que los 226 px de hoy, pero ahora no
depende del ancho y la escala es la del mayor % elegible (no la del pico de personas), así
que las barras de la tarde usan todo el alto. 180 px en celular es 3,3 veces lo de hoy
(55 px). Barras de 0,8 u en 288 u: 3,1 px en escritorio y 0,72 px en celular (se lee como
curva; las marcas numeradas ubican el top). Letras de 12 px: entre el `.muted` (13 px) y el
4 px de hoy en celular.

### Alternativas descartadas

- **Ranking por momento agrupado** (personas distintas del momento / promedio de
  espectadores del momento): el % crece con la duración del momento y no hay criterio
  para agrupar sin umbral (ver Definiciones).
- **Seguir con el SVG que escala entero y solo cambiar el viewBox** (por ejemplo 720×300):
  en celular la letra sigue en ~4 px; el problema es la escala, no la proporción.
- **Gráfico de personas con el % como línea superpuesta:** dos escalas en el mismo
  gráfico; el criterio es el %, así que se grafica el % y las personas van en el `<title>`
  y en la tabla.
- **Graficar el % de todas las franjas con dato:** las de 1 o 2 personas de madrugada
  (10 % o más) aplastarían a las de la tarde; por eso solo tienen barra las elegibles y las
  otras explican en el `<title>` por qué no.
- **Lista o tarjetas en vez de tabla:** con flex-wrap entra en cualquier ancho, pero
  pierde la alineación de columnas que sirve para comparar, que es lo que pidió la persona.
- **Achicar la ventana del gráfico a 12 o 6 h:** el ranking es de 24 h y el resto del
  análisis también; ver pregunta abierta 3.

## Fuera de alcance

- El gráfico "Mensajes cada 5 minutos" del resumen (`activity`, `server.go:147-180`): tiene
  el mismo problema de escala en celular, pero no se pidió. Puede ser otra tarea.
- `Participation`, la serie de espectadores (`Store`, `datos.espectadores.jsonl`), el
  worker y el cliente de YouTube: no cambian.
- `reelLink` y `dvrWindow`: no cambian (la verificación del `&t=` sigue pendiente en CA14
  de la spec anterior).
- Exportar el top a CSV, elegir la ventana desde el dashboard, cambiar el clasificador.
- El resto de las tarjetas de `.vgrid` más allá del arreglo de `minmax` (que también les
  saca el desborde de 6 px en celular).

## Riesgos

- **Cuota:** nulo. No hay llamadas nuevas ni cambios en `youtube.go`, `worker.go` ni
  `store.go` (CA9).
- **Compatibilidad de `datos.jsonl`:** no se toca `Message` ni ningún archivo de datos.
- **Calidad del dato:** franjas con una sola lectura de espectadores o con
  `concurrentViewers` cacheado por YouTube pueden subir o bajar el %. Con muestras cada
  3 minutos, cada franja tiene 1 o 2. Es una aproximación, como ya dice la tarjeta. El
  mínimo de 3 personas limita el ruido de las franjas chicas.
- **Muestras recientes:** los espectadores se guardan desde el PR #7; hasta que pasen 24 h
  parte de la ventana no tiene dato y no entra en el ranking (E0/E2 lo explican).
- **Marcas que se pisan en celular:** con 0,9 px por franja, dos franjas del top a menos
  de ~80 minutos pueden tener los números encimados. Es cosmético; se revisa en CA11. Si
  molesta, se puede probar en otra tarea con las marcas solo en escritorio.
- **`html/template` y `style`:** los porcentajes van como números formateados con
  `printf`, que el escapador de CSS deja pasar (lo mismo que hoy en
  `templates/partials.html:48`); ningún texto de usuario entra en `style`.
- **Concurrencia:** sin cambios; el handler sigue leyendo copias (`Snapshot`, `Viewers`).
- **Windows:** sin código específico de plataforma; el build de Windows está en los gates.

## Preguntas abiertas (decisiones de producto)

1. **Mínimo de personas:** recomiendo 3 (`minPersonasTop`), lo que ya venía usando la
   tarjeta. Con 2, las franjas de madrugada con poca gente mirando ganarían el ranking; con
   5, un vivo tranquilo podría quedarse sin top.
2. **Separación entre momentos del top:** recomiendo 15 minutos entre inicios. Opciones:
   10 minutos (solo evita franjas pegadas: una charla de 20 minutos podría ocupar 2
   puestos) o 30 minutos (más variedad, pero puede dejar afuera dos momentos buenos
   cercanos de programas distintos). Con cualquiera, si las franjas buenas están todas
   juntas se muestran menos de 5 (mensaje E2).
3. **Ventana del gráfico:** recomiendo mantener 24 h, la misma del ranking y del resto del
   análisis: en escritorio cada franja mide ~3,8 px y en celular se lee como curva, con las
   marcas numeradas para ubicar el top. Si en CA11 no se lee, la opción es otra tarea con
   un selector de ventana (24 h / 6 h) por parámetro de htmx.
4. **% en lugar de personas en el gráfico:** recomiendo graficar el % (es el criterio del
   ranking) y dejar personas y mirando en el `<title>` y en la tabla. Si no hay ningún dato
   de espectadores, el gráfico vuelve a mostrar personas para no quedar vacío.
5. **Alto del gráfico:** recomiendo 240 px en escritorio y 180 px en celular. Ajustarlo
   después es un número en el CSS.
