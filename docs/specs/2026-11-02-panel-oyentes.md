# Spec: panel de oyentes arriba del gráfico de participación

- **Estado:** borrador
- **Tipo:** mejora
- **Rama:** `feat/panel-oyentes`
- **Origen:** pedido de Julian Barbieri (conversado y elegido entre opciones): sumar al
  gráfico "6. Momentos con más participación (para reels)" una línea con el promedio de
  oyentes por franja de 5 minutos, en un panel propio arriba de las barras.
- **Aprobada por / fecha:** <pendiente>

## Contexto

La tarjeta 6 del análisis del vivo (`GET /parcial/vivo/analisis`) grafica el % de
participación por franja de 5 minutos (personas que escribieron sobre los que miraban).
Un pico de % puede venir de mucha gente escribiendo o de pocos oyentes (por ejemplo, de
madrugada), y hoy para distinguirlo hay que pasar el mouse franja por franja. Se quiere
verlo de un vistazo para elegir momentos para reels.

El dato ya existe y no hace falta pedir nada nuevo a YouTube:

- `analyze.go:291`: `Slot.Viewers` es el promedio redondeado de las muestras de
  espectadores de la franja; 0 = sin dato (`analyze.go:326-341`). Sale de
  `Store.Viewers` (`server.go:366`), que ya se lee para el %.
- `server.go:395` `chartWindow` recorta la ventana graficada (6 h por defecto o 24 h con
  `?horas=24`, `server.go:338-341`), contando como "primer dato" tanto el chat como los
  espectadores.
- `server.go:480` `participationChart` arma `Bars` (una unidad de viewBox por franja,
  `X = i + 0,1`, `W = 0,8`), `Marks`, `Ticks`, `Max`, `Aria`, `TypicalY` y `ByPct`.
- `server.go:569` `slotTitle` arma el `<title>` de cada franja. Hoy **no** muestra los
  espectadores cuando la franja tiene dato pero menos de 3 personas
  (`server.go:578-579`: "menos de 3 personas: no entra en el ranking"), así que en esas
  franjas el tooltip no dice cuántos miraban ni el %.
- `templates/partials.html:183-194`: el gráfico es un `<svg viewBox="0 0 W 100"
  preserveAspectRatio="none" role="img">` con los rótulos (`.pmax`, `.pmarks`,
  `.pticks`) en HTML. CSS en `templates/page.html:118-137` (240 px de alto, 180 px en
  ≤ 600 px).

**Reproducción:** no aplica (mejora). Línea de base verificada en este worktree con
Go 1.26.8: `go build ./...`, `go vet ./...` y
`go test -count=1 -run 'Participation|ChartWindow|TopMoments' ./...` en verde.

**Causa raíz:** no aplica.

## Objetivo

Que, cuando haya dato de espectadores en la ventana graficada, la tarjeta 6 muestre arriba
de las barras un panel con la línea de cuántos miraban en cada franja, alineado columna a
columna con las barras, con su propia escala, cortado donde no hay dato, y que el tooltip
de cada franja diga oyentes, personas, mensajes y %. Sin dato en la ventana, el gráfico
queda exactamente como hoy.

## Criterios de aceptación

Los tests nuevos van en `monitor_test.go` y se escriben **antes** del código: los de CA1 a
CA6 tienen que fallar hoy (no existe la clave `Viewers` ni el panel; `slotTitle` no muestra
espectadores con menos de 3 personas). Guardar esa salida para el PR.

- [ ] CA1 (línea cortada y escala propia): `participationChart` devuelve en
  `c["Viewers"]` un `ViewersLine{Path, Max, Aria string}`. `Path` es una línea en
  escalones: por cada tramo de franjas consecutivas con `Viewers > 0` que empieza en la
  franja `i`, `M{i} {y}H{i+1}`, y por cada franja `j` siguiente del tramo `V{y}H{j+1}`,
  con `y = 100 − Viewers/máx × 100` (`%.2f`) y `x` entero (índice de franja, mismas
  columnas que las barras). Una franja con `Viewers == 0` cierra el tramo: el siguiente
  empieza con otro `M`, nunca baja a `y = 100`. Ejemplo: franjas 2 (100), 3 (200) y 5 (50)
  de una ventana de 36 dan `Path == "M2 50.00H3V0.00H4M5 75.00H6"`. — se verifica con
  `go test -count=1 -run '^TestParticipationChartViewers$' ./...` (subtest "escalones y
  cortes").
- [ ] CA2 (rótulo con miles y aria): con un máximo de 1.573 en la franja `k` (y otra con
  el mismo valor más tarde), `Max == "máx. 1.573 mirando"` y `Aria` es
  `"Cuántos miraban el vivo cada 5 minutos <span> (promedio de lo que informa YouTube);
  máximo de 1.573 a las <hora de la franja k>"`, con `<span>` igual al del aria de las
  barras ("en las últimas 24 horas" o "desde las HH:MM"); si hay franjas sin dato dentro
  de la ventana, termina en `"; sin dato en N franjas"`. Empate: la primera franja. — se
  verifica con `TestParticipationChartViewers` (subtests "miles y aria" y "empate").
- [ ] CA3 (sin dato, sin panel): no hay clave `Viewers` en el mapa si `ok` es `false`
  (modo personas) o si ninguna franja de `slots` tiene `Viewers > 0`, y en ese caso
  `Bars`, `Marks`, `Ticks`, `Max`, `Aria` y `TypicalY` son iguales a los de hoy. — se
  verifica con `TestParticipationChartViewers` (subtest "sin dato") y con
  `TestParticipationChart`, `TestParticipationChartEdges` y
  `TestParticipationChartWindow` sin cambios salvo los títulos de CA4.
- [ ] CA4 (tooltip completo): `slotTitle` muestra `"· N mirando (P %)"` siempre que
  `Viewers > 0`, también cuando hay menos de 3 personas; en ese caso el motivo va después:
  `"… · 2 mensajes · 2 personas · 10 mirando (20,0 %) · menos de 3 personas: no entra en
  el ranking"`. Sin dato sigue `"· sin dato de espectadores"`. — se verifica actualizando
  la tabla `titles` de `TestParticipationChart` (franjas 10 y 250) antes del cambio (falla
  hoy) y con `go test -count=1 -run '^TestParticipationChart$' ./...`.
- [ ] CA5 (HTML del panel): en `GET /parcial/vivo/analisis?video=<id>` de un vivo con
  muestras de espectadores, la tarjeta 6 (`section6`) tiene dos `<svg` con el **mismo**
  `viewBox="0 0 W 100"` y `preserveAspectRatio="none"`, dos `role="img"`, el panel de
  oyentes **antes** de las barras (`strings.Index` menor), un `<path class="oyentes"
  d="M…" vector-effect="non-scaling-stroke">`, el rótulo `máx. N mirando` en un `<div`
  con `aria-hidden="true"`, una zona de tooltip por franja en el panel
  (`<rect class="hit" …><title>` con el mismo texto que la barra) y ningún `<text`. El
  conteo de `<g><title>` sigue igual a la cantidad de franjas (el panel no usa `<g>`), así
  que `TestLiveParticipationView` y `TestParticipationWindowView` no cambian sus números.
  — se verifica con `go test -count=1 -run '^TestParticipationViewersView$' ./...`.
- [ ] CA6 (selector 6 h / 24 h y casos sin panel): en el mismo test, un vivo con muestras
  de espectadores solo hace 10 h no muestra el panel con `horas=6` (no hay
  `class="oyentes"` y hay un solo `role="img"`) y sí con `horas=24`; un vivo con el
  máximo de espectadores fuera de las últimas 6 h tiene distinto `máx. … mirando` en 6 h y
  en 24 h. Un vivo sin muestras (como `v3` de `TestLiveParticipationView`) y uno con el
  contador oculto (como `v2`, modo personas) no muestran el panel. — se verifica con
  `TestParticipationViewersView`.
- [ ] CA7 (geometría dentro del viewBox): para ventanas de 36, 72 y 288 franjas, todos los
  `x` del `Path` están en `[0, W]` y todos los `y` en `[0, 100]`, sin `NaN` ni `Inf`, y
  el `Path` no tiene `L` (solo `M`, `H`, `V`). — se verifica con
  `TestParticipationChartViewers` (subtest "dentro del viewBox", parseando el `Path`).
- [ ] CA8 (estilos y temas): la línea usa un token de color que existe en los dos temas
  de `templates/page.html` (propuesta: `var(--text-secondary)`); si se agrega un token
  nuevo, aparece en `:root` y en el bloque `prefers-color-scheme: dark`. No hay colores
  sueltos fuera de esos bloques: `git diff main -- templates/page.html | grep -nE
  '^\+.*#[0-9a-fA-F]{3,6}'` solo lista líneas de los dos bloques de tokens (o nada). El
  panel tiene alto propio en escritorio y en `@media (max-width: 600px)`, y
  `.oyentes` tiene `fill: none` y `pointer-events: none` (para que el tooltip lo tome la
  zona de la franja). — se verifica con el `grep` y `grep -n 'oyentes' templates/page.html`.
- [ ] CA9 (sin cuota, sin cambios en lo guardado ni en la concurrencia):
  `git diff --quiet main -- youtube.go worker.go store.go analyze.go` sale con código 0;
  `go.mod` sigue sin `require`; `grep -n 'template.HTML' *.go` no lista nada nuevo. Las
  cifras de cuota del README no cambian.
- [ ] CA10 (documentación): el README (sección 3, punto 6) y `AGENTS.md` (mapa de
  `server.go`) mencionan el panel de oyentes arriba de las barras, que se corta donde no
  hay dato y que no aparece sin dato en la ventana. — se verifica con
  `grep -n 'oyentes' README.md AGENTS.md` y revisando `git diff main -- README.md AGENTS.md`.
- [ ] CA11 (manual, lo hace una persona; la evidencia va en el PR): con la vista previa
  (`go run ./tools/preview -datos <checkout principal>/datos.jsonl`, que copia también
  `datos.espectadores.jsonl`; cortarla con `go run ./tools/preview -cortar`), en modo
  claro y oscuro, a 360 px y a 1.200 px de ancho, con 6 h y con 24 h: los escalones de la
  línea quedan exactamente arriba de su barra, el trazo tiene el mismo grosor en todo el
  ancho, los textos (`máx. … mirando`, `máx. … %`, números del top y horas) miden 12 px y
  no se deforman, la línea se ve cortada en los huecos sin dato, el tooltip aparece al
  pasar el mouse tanto por el panel como por las barras, y la página no tiene scroll
  horizontal (`document.documentElement.scrollWidth <= innerWidth` en la consola).
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde: `gofmt -l .` vacío,
  `go vet ./...`, `go run honnef.co/go/tools/cmd/staticcheck@2026.2.2 ./...`,
  `go build ./...`, `GOOS=windows GOARCH=amd64 go build ./...`,
  `go test -race -count=1 ./...` y `go test -count=5 ./...`.

## Diseño propuesto

Cambio solo de presentación en `server.go`, `templates/partials.html`,
`templates/page.html`, más README y AGENTS.md. `analyze.go` no se toca: `Slot.Viewers` ya
tiene el promedio por franja.

**Orden de trabajo:** primero los tests (CA1-CA7) y la tabla de títulos de
`TestParticipationChart` (CA4); correrlos y guardar que fallan; después el código.

1. `server.go`
   - Tipo nuevo, cerca de `Mark`/`Tick`:
     ```go
     // ViewersLine es el panel de oyentes arriba de las barras: escalones con el
     // promedio de espectadores de cada franja, cortados donde no hay dato.
     type ViewersLine struct{ Path, Max, Aria string }
     ```
   - Función chica `viewersLine(slots []Slot, span string) (ViewersLine, bool)`: busca el
     máximo de `Viewers` (la primera franja en empate); si es 0 devuelve `false`; si no,
     arma el `Path` en escalones (CA1), el `Max` con `miles` y el `Aria` (CA2), contando
     las franjas sin dato para el final del aria.
   - `participationChart`: si `ok`, llama a `viewersLine(slots, span)` y, si hay panel,
     `out["Viewers"] = line`. La escala es el máximo de la **ventana** recibida, así que
     cambia con 6 h / 24 h sin tocar `chartWindow` ni `participationView`. El resto del
     mapa no cambia (CA3).
   - `slotTitle`: reordenar el `switch` para que con `Viewers > 0` siempre agregue
     `· N mirando (P %)` y después, si `Authors < minPersonasTop`, el motivo (CA4).
2. `templates/partials.html`, dentro de `{{with .Chart}}` y antes de `.pmax`:
   ```html
   {{if .Viewers}}<div class="pmax" aria-hidden="true">{{.Viewers.Max}}</div>
   <div class="poyentes">
     <svg viewBox="0 0 {{.W}} 100" preserveAspectRatio="none" role="img" aria-label="{{.Viewers.Aria}}">
       {{range .Bars}}<rect class="hit" x="{{.X}}" y="0" width="{{.W}}" height="100"><title>{{.Label}}</title></rect>{{end}}
       <path class="oyentes" d="{{.Viewers.Path}}" vector-effect="non-scaling-stroke"></path>
     </svg>
   </div>{{end}}
   ```
   Las zonas de tooltip reusan `Bars` (mismo `X`, `W` y `Label`) para no duplicar datos y
   que la columna coincida con la barra. Van sin `<g>` porque no hay barra que agrupar,
   y así los conteos de `<g><title>` de los tests actuales siguen siendo de las barras.
   En el párrafo de `{{if .ByPct}}`, sumar con `{{if .Chart.Viewers}}` una frase:
   "Arriba, cuántos miraban cada 5 minutos (la línea se corta donde no hay dato)."
3. `templates/page.html`: `.poyentes svg { display: block; width: 100%; height: 72px;
   overflow: visible; margin-bottom: 8px; }`, `56px` en `@media (max-width: 600px)`, y
   `.oyentes { fill: none; stroke: var(--text-secondary); stroke-width: 2;
   stroke-linejoin: round; pointer-events: none; }`. `overflow: visible` evita que se
   corte medio trazo cuando el máximo queda en `y = 0`.
4. README (sección 3, punto 6) y `AGENTS.md` (mapa de `server.go`): una oración cada uno.

**Alternativas descartadas:**

- Línea que une los centros de las franjas (`polyline`): una franja con dato aislada entre
  dos sin dato no se vería (segmento de largo 0), y el vértice en el centro sugiere un
  valor instantáneo cuando es un promedio de 5 minutos. Los escalones resuelven las dos.
- Área rellena: el pedido admite línea o área; un área pide otro color de relleno en los
  dos temas y compite con las barras azules de abajo. Si se quiere, es un `fill` con
  `opacity` sobre el mismo `Path` cerrado, en otra tarea.
- Un solo SVG con los dos paneles (viewBox de 0 a 140): obliga a mover la `Y` de todas las
  barras y de `TypicalY`, rompe los tests de geometría actuales y mezcla escalas en un
  mismo `viewBox`, que es lo que se pidió evitar.
- Escala de mínimo a máximo: exagera variaciones chicas (1.400 a 1.600 parecería caer a
  cero). Desde 0 se compara de verdad "muchos" contra "pocos" oyentes, que es el motivo
  del pedido.
- Doble eje en el panel de las barras: descartado en el pedido.

## Fuera de alcance

- Filtros nuevos, otro selector de horas o cambiar `chartWindow`, `minChartSlots` o las
  6 h por defecto.
- Cambiar el ranking (`TopMoments`), la participación típica o `Participation`.
- Pedir espectadores más seguido o de otra forma (cuota), guardar algo nuevo o cambiar
  `datos.espectadores.jsonl` / `datos.jsonl`.
- El "mirando ahora" del resto del dashboard y la tabla de momentos.

## Riesgos

- **Cuota:** ninguno. Usa `Store.Viewers`, que el handler ya leía; no hay llamadas nuevas
  a YouTube. Verificado por CA9.
- **Datos guardados / retención:** ninguno; no cambia `Message`, `ViewerSample` ni los
  archivos. CA9.
- **Concurrencia:** ninguno; el handler sigue usando copias (`Snapshot`, `Viewers`).
- **Alineación entre paneles:** si los dos SVG no tienen el mismo ancho de caja, los
  escalones no caen sobre su barra. Ambos están en el mismo contenedor con `width: 100%`
  y el mismo `viewBox` (CA5); se confirma a ojo en CA11.
- **Tooltip tapado por la línea:** el `path` va arriba de las zonas de tooltip; con
  `pointer-events: none` (CA8) el mouse llega a la zona. Se confirma en CA11.
- **Tests existentes:** cambian a propósito solo los títulos de las franjas 10 y 250 de
  `TestParticipationChart` (CA4). Si algún otro test de gráfico cambia de número, es una
  regresión.
- **Escapado:** `d` y `aria-label` son atributos de texto para `html/template`; el
  `Path` solo tiene `M`, `H`, `V`, dígitos, punto y espacio. `TestLiveParticipationViewEdges`
  ya chequea que no aparezca `ZgotmplZ`.
- **Windows:** sin impacto (solo templates y Go puro); lo cubre el build de Windows de
  los gates.

## Preguntas abiertas

1. **Palabra:** se propone `máx. 1.573 mirando` y `· 1.573 mirando` en el tooltip, igual
   que la columna "Mirando" de la tabla. ¿O prefieren "oyentes" en el panel (por ejemplo
   `máx. 1.573 oyentes`)? Si cambia, cambiar también en el tooltip para que no convivan
   dos palabras.
2. **Color de la línea:** se propone reusar `--text-secondary` (gris, sin tokens nuevos)
   para que se lea como contexto y no compita con las barras. La alternativa es un token
   nuevo `--series-2` definido en los dos temas.
3. **Tooltip también en el panel de arriba:** se propone que sí (mismo texto que la
   barra). Si no, se sacan las zonas `hit` del panel y queda solo la línea.
4. **Alto del panel:** 72 px en escritorio y 56 px en celular (las barras siguen en
   240 / 180 px). Se ajusta en CA11 si queda chato.
