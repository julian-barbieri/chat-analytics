# Spec: canales editables, Claude en tandas y En vivo con sentimiento, alertas y preguntas agrupadas

- **Estado:** aprobada (F1 implementada, falta la revisión en el navegador CA1.9; F2 implementada en `feat/canales-editables`, revisada (reviewer: aprobado con cambios menores), falta la revisión en el navegador CA2.14; F3 implementada en `feat/claude-tandas` con el flujo manual (spec corta `docs/specs/2026-11-06-claude-tandas.md`; su ADR es el 0009 porque el 0008 ya era el flujo manual), falta la revisión en el navegador; F4 pendiente)
- **Tipo:** mejora (feat)
- **Rama:** la spec, en `feat/canales-editables-claude-vivo`. Implementación en cuatro
  fases con un PR cada una, cada rama desde `main` actualizado después de que entre la
  anterior: `feat/en-vivo-plegar-quitar` (F1), `feat/canales-editables` (F2, el nombre que ya
  reservaba la 010), `feat/claude-tandas` (F3) y `feat/claude-en-vivo` (F4). Ver "Fases y PRs".
  Al aprobar, el orquestador decidió que F1 vaya en la rama de la spec
  (`feat/canales-editables-claude-vivo`), así la spec aprobada entra a `main` con el primer PR;
  F2 a F4 siguen en sus ramas propias, como tareas nuevas de la factory.
- **Origen:** pedido de Julian Barbieri (2026-11-05, tarea 012), con tres imágenes de
  referencia (tarjeta "Análisis con Claude" de Canales, "Sentimiento del chat" y "Preguntas
  agrupadas para producción"; descriptas en "Diseño de referencia"):

  > "SECCION CANALES: Me gustaría que tenga la posibilidad de agregar, pausar y eliminar
  > canales desde la pestaña "Canales". Actualmente veo que tengo a la Radio Rivadavia.
  > Dentro de esa seccion por cada canal quiero poder seleccionar el tiempo que quiero que se
  > actualice el chat dentro del horario pico y fuera del horario pico. Dentro de "Análisis
  > con Claude" quiero que se analice y se puedan hacer estas opciones (adjunto imagenes).
  > SECCION EN VIVO: Quiero que se visualicen las alertas analizadas por Claude de insultos y
  > quejas técnicas y que se puedan ver esos mensajes de esas alertas. Dentro del programa en
  > curso quiero que se visualicen los sentimientos del chat (adjunto imagen de como se
  > deberia visualizar), reemplazalo por el grafico que se muestra actualmente. Quiero que se
  > visualicen de esta manera las preguntas agrupadas y ordenadas por cantidad de veces
  > realizada (adjunto imagen). Por otro lado, Las preguntas agrupadas y Quejas que estan por
  > debajo del resumen no quiero que se muestren a simple vista, quiero que se escondan y que
  > se puedan desplegar con una flecha. Elimina las siguientes secciones que no son de
  > utilidad como "Filtros para YouTube Studio", "Temas (por reglas)". Dentro de la seccion
  > "Todo el monitor · últimas 24 h", elimina la seccion de "categorias" que la veo duplicada"

- **Reemplaza:** las **etapas 2 y 3 de `docs/specs/2026-11-05-rediseno-tres-pantallas.md`**
  (la 010; aprobadas, sin implementar; la etapa 2 a su vez absorbía la etapa 2 de
  `2026-11-05-canales-continuos.md`). Nadie las implementa por su cuenta: esta spec las toma,
  con los cambios que pide el pedido nuevo (tabla "Qué cambia respecto de lo aprobado"). Esta
  tarea no edita esas specs; el orquestador marca su estado al aprobar.
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-05 ("Aprobar con recomendaciones"):
  se aceptan las recomendaciones de las 12 preguntas abiertas: (1) reemplaza las etapas 2 y 3
  de la 010, cuatro PRs en orden; (2) un select para tiempo real (Haiku) y resúmenes con
  `-claude-modelo` mostrado como texto; (3) tandas cada 30 s por defecto; (4) tope sugerido
  US$ 3; (5) "Marcar respondida" persistida 30 días con "Deshacer"; (6) el sentimiento
  reemplaza la barra del resumen de Claude y el gráfico de participación queda; (7) "lo
  típico" según (f); (8) las alertas muestran el autor; (9) F5 opcional, después de medir
  costos; (10) se puede prender desde la pantalla con clave y tope > 0; (11) se quita Filtros;
  (12) se aprueban los textos de "Reglas que cambian" y los ADR 0005 y 0008.

## Contexto

### Qué hay hoy en `main` (`1154b21`, verificado leyendo el código)

**En vivo.** `/parcial/vivo/analisis` (30 s, `server.go:598-640`) arma con `analysisData`
(`server.go:722-738`) y `channelAnalysis` (`server.go:744-829`): tiles, `#quejas` (Quejas
técnicas por reglas, `templates/partials.html:94-104`), `#claude-card` (resumen de la 009,
`partials.html:243-257`), `#preguntas` (por reglas) y `#categorias` (`partials.html:108-128`),
`#momentos` con el gráfico de participación y el panel de oyentes (`partials.html:130-172`),
y abajo, por `hx-swap-oob` en `#vivo-abajo`, `#supers`, **`#filtros`** ("Filtros para YouTube
Studio" con el bloque **"Temas (por reglas)"**, `partials.html:196-215`) y **`#monitor`**
("Todo el monitor · últimas 24 h" con **Categorías**, Palabras y Quiénes,
`partials.html:217-238`; datos de `monitorView`, `server.go:648-651`).

**Resumen de Claude (009).** En "Programa en curso", `claudeView` (`server.go:1451-1513`)
muestra el último análisis del tramo con `claudeBlock` (`server.go:1515-1549`): resumen,
**barra de sentimiento** (`claude-sent`, una barra apilada de 6 px con los % del modelo,
`partials.html:277-280`), temas, **"Preguntas agrupadas:"** y **"Quejas:"** (`claude-items`,
`partials.html:268-271`), todo a la vista. Los ejemplos se buscan en el `Store` por ID
(`server.go:908-925`) y se muestran con autor (`partials.html:284`). Programas de hoy usa
`claude-sent` y `claude-items` en su detalle (`templates/programas.html:78-83`).

**Refresco.** `#vivo-analisis` se reemplaza entero cada 30 s (`hx-swap="outerHTML"`,
`partials.html:71`). Lo que el usuario eligió se conserva porque viaja en la URL (`ver`,
`horas`) y en inputs ocultos `.vq` (`partials.html:72`, `keepView` en `server.go:330`). Un
`<details>` abierto **se cerraría a los 30 s** porque htmx no conserva el atributo `open` al
reemplazar (hipótesis fuerte: htmx 2.0.4 solo asienta `class`, `style`, `width` y `height`;
se confirma en el navegador con la vista previa).

**Canales.** Solo lectura (CA1.14 de la 010): `channelsPage` / `channelsData` /
`claudeSetup` (`server.go:1598-1756`), `templates/canales.html` sin `<form>`, `<input>`,
`<select>` ni `hx-post` (lo afirma `TestChannelsPageReadOnly`, `monitor_test.go:6700`). Los
canales salen de `-canal` (`main.go:45`); el intervalo de chat es **global**: `-chat-seg-pico`
y `-chat-seg` (`main.go:52-53`), `chatInterval` (`worker.go:48-56`) y un solo turno entre
todos los vivos (`nextChat`, `worker.go:124-137`). La tarjeta de Claude dice "El modelo y el
tope se cambian con -claude-modelo y -claude-usd" (`canales.html:40`).

**Claude (009, ADR 0006).** Opt-in con `-claude-usd` > 0 y `CLAUDE_API_KEY`
(`claudeConfig`, `claude.go:105-131`; `main.go:57-58`, `:146-156`). Una goroutine
(`RunAnalyst`, `analyst.go:117-141`) con tick de 30 s (`analystTick`, `analyst.go:14`): un
pedido por programa terminado (`step`, `analyst.go:145-165`) y el botón "Analizar ahora" como
mucho cada 10 min por canal (`analyst.go:16`). Peor caso antes de cada pedido
(`analyst.go:205`, `claudeWorstCase` en `claude.go:79-81`). Precios en `claudePrices`
(`claude.go:51-55`): Haiku 5.5 US$ 0,10 / 0,50 por millón (0,50 / 2,50 arriba de 100.000 de
entrada), Sonnet 5.5 2 / 10, Opus 5.5 4 / 20. Modelo por defecto `claude-sonnet-5-5`
(`claude.go:31`). No hay tandas, etiquetas por mensaje ni "Marcar respondida".

**ADRs.** Existen 0001-0004, 0006 y 0007. El **0005** está reservado (canales editables,
canales-continuos) y nunca se escribió; el **0008** también lo reservaba la 010 para las
tandas. Esta spec usa los dos con ese mismo contenido.

**Reproducción:** no aplica (mejora). Línea de base en `feat/canales-editables-claude-vivo`
(igual a `main`, `1154b21`), Go 1.26.8:

```bash
go build ./... && go vet ./... && gofmt -l . && go test -count=1 ./...
```

```
ok  	github.com/alphamedia/yt-monitor	19.535s
ok  	github.com/alphamedia/yt-monitor/tools/factory-board	3.342s
ok  	github.com/alphamedia/yt-monitor/tools/preview	19.720s
```

(`go build`, `go vet` sin salida y `gofmt -l .` vacío.) Se vuelve a tomar al empezar cada fase.

**Causa raíz:** no es un bug.

### Diseño de referencia (las tres imágenes)

1. **Canales → "Análisis con Claude"** (chip "Claude", pastilla verde "Clave cargada desde
   .env"): "La clave se lee de CLAUDE_API_KEY en el archivo .env y nunca se muestra acá ni en
   los registros. Si falta o se llega al tope de gasto, el monitor sigue funcionando con las
   reglas de siempre." Campos: **Modelo** (select, "Claude Haiku 5.5 (rápido, recomendado)"),
   **Analizar en tandas cada** (select, "30 s", ayuda "el dato llega con esa demora"),
   **Tope de gasto por día (US$)** (input, ayuda "al llegar, se vuelve a las reglas").
   Tarjetas: "Mensajes analizados hoy 3.150 — de 3.180 (30 en la próxima tanda)", "Gasto
   estimado de hoy US$ x,xx — con los tokens que informa la API", "Última tanda hace 12 s —
   sin errores".
2. **"Sentimiento del chat"** (chip Claude; a la derecha "1.060 mensajes en los últimos 5
   min"): cartel amarillo "El negativo sube: 19% en los últimos 5 min, contra 12% típico del
   programa. La causa principal son quejas por el audio."; tres tarjetas Positivo 52 % "−5 pts
   sobre lo típico", Neutro 29 % "−2 pts", Negativo 19 % "+7 pts"; "Cómo fue cambiando · cada
   5 min": barras apiladas al 100 % (positivo abajo, neutro, negativo arriba), eje 13:00 …
   "14:00 · empieza [Programa 3]" (línea punteada) … "15:40 en curso", leyenda con "Menos de
   20 mensajes: sin color"; debajo "Por qué hay mensajes negativos" (200 negativos en 5 min;
   Problemas de audio 61 % con citas, Desacuerdo con el invitado 22 %, Otros 17 %) y "Qué está
   gustando" (550 positivos; El invitado / la entrevista 54 %, Saludos 31 %, Otros 15 %).
3. **"Preguntas agrupadas para producción"** (chip Claude; "87 preguntas · 14 grupos"):
   filas "24 · ¿Vuelve el invitado la semana que viene?", "17 · …", cada una con un botón
   "Marcar respondida".

### Qué cambia respecto de lo aprobado en la 010

| Tema | Aprobado (010) | Pedido nuevo | En esta spec |
|---|---|---|---|
| Filtros para YouTube Studio y "Temas (por reglas)" | Se conservan (decisión 4) | Eliminarlos | **Se quitan** (F1) |
| Categorías en "Todo el monitor" | Se conservan (decisión 12.5) | Eliminarlas | **Se quitan** (F1); queda `#categorias` del alcance |
| Preguntas agrupadas y Quejas del resumen | A la vista; en la etapa 3 salían del resumen | Plegadas, con flecha | **Plegadas** con `<details>` (F1), siguen ahí en F4 |
| Gráfico de sentimiento | Barra de la 009 + tarjeta nueva (CA3.7) | "Reemplazalo por el gráfico que se muestra actualmente" | La tarjeta nueva **reemplaza la barra `claude-sent`** del resumen; el de participación queda (**decisión abierta 6**) |
| Modelos de Claude | Dos: tiempo real (Haiku) y resúmenes (Sonnet), los dos editables (decisión 10) | La imagen muestra un solo "Modelo" | Un select (tiempo real); resúmenes por flag (**decisión abierta 2**) |
| Tandas | Cada 1 min por defecto (CA3.15) | La imagen muestra 30 s | **30 s por defecto** (**decisión abierta 3**) |
| "Marcar respondida" | En memoria (decisión 6) | — (lo pide el orquestador, punto d) | **Persistida 30 días** (**decisión abierta 5**) |
| "Lo típico" del sentimiento | Mediana de las franjas del alcance de hoy (CA3.7) | "contra 12 % típico del programa" | Mismo programa, últimos 7 días, con caída al tramo de hoy (**decisión abierta 7**) |
| CSV, categorías de Claude en el chat, "Desde dónde escriben", resumen por momento, Programas con Claude | Etapa 3 | No se mencionan | Pasan a una **F5 opcional** (**decisión abierta 9**) |

## Choques con reglas vigentes (para decidir)

**(a) Canales editables desde el dashboard.** Hoy Canales es de solo lectura (CA1.14) y
AGENTS.md / CLAUDE.md dicen "bot de solo lectura" y que la configuración sale de `-canal`.
Editar implica `POST` que cambian la configuración del worker en caliente y un archivo nuevo,
`datos.canales.json` (ya ignorado por `datos.*.json`). Propuesta (igual a lo aprobado en
canales-continuos y la 010): los `POST` van detrás de `sameOrigin` + `localRequest`
(`guard.go:13-39`) y `http.MaxBytesReader`; el archivo guarda solo lo que escribió la persona
(nada de la API); sin archivo se arranca con `-canal` y `-chat-seg-pico` / `-chat-seg` × N
(mismo gasto que hoy; con Rivadavia sola, N = 1); con archivo **gana el archivo** y, si además
se pasó `-canal` o `-chat-seg*` a mano, se loguea un `aviso:` y la pantalla lo dice. **Cuota:**
cada canal agregado cuesta 1 unidad al resolverlo y ~480 por día de revisión de su lista de
vivos (más ≤ 40 por corte de búsqueda rápida); cada intervalo cambia el chat de ese canal. Antes
de guardar, la barra de cuota se recalcula con lo elegido (`GET /parcial/canales/cuota`, sin
escribir) y dice "Entra en el día, quedan N" o "No entra: faltan N unidades, subí algún
intervalo". `-vivos` y `-tiempo-real` siguen siendo solo flags. Pide ADR 0005.

**(b) Tandas de Claude cada 30 s.** ADR 0006 manda un pedido por programa al terminar + el
botón cada 10 min, y descartó "análisis cada N minutos" porque reenviaba lo mismo. Las tandas
**no** reenvían: cada una manda solo los mensajes nuevos desde la última etiquetada, y si no hay
nuevos no hay pedido. Lo que se manda sigue siendo **solo texto y hora**, sin `Author`, sin
IDs, sin spam, 300 runas por texto. Cambia el modelo de costo: ~US$ 0,82 por día con Haiku 5.5
y Rivadavia sola, ~US$ 1,27 con Rivadavia + un canal como Rock & Pop (tabla en "Costo de
Claude"); el tope diario sigue siendo el freno (peor caso antes de cada tanda, 75 % del tope
para tiempo real). Pide ADR 0008 ("Modifica: ADR 0006").

**(c) Modelo, tandas y tope desde la pantalla vs `-claude-modelo` / `-claude-usd`.**
Propuesta de precedencia: los flags son **valores iniciales**; lo guardado desde Canales (sección
`Claude` de `datos.canales.json`) gana, y si además se pasó un flag de Claude a mano se loguea un
`aviso:` y la tarjeta dice "-claude-usd 2 no se usa: vale lo guardado acá (US$ 3)". Choque con
ADR 0006 ("se activa con `-claude-usd` > 0 y `CLAUDE_API_KEY`"): con la clave cargada,
`RunAnalyst` arranca siempre y espera con tope 0; el análisis **se activa** cuando el tope
vigente (flag o pantalla) es mayor que 0. Sigue apagado por defecto (sin archivo y con
`-claude-usd 0` no se manda nada) y solo se puede prender desde la PC del monitor. Sin clave,
los controles quedan deshabilitados con "Falta CLAUDE_API_KEY".

**(d) "Marcar respondida".** Es un estado nuevo que escribe un handler. La 010 lo dejó en
memoria (decisión 6, "se olvida al reiniciar"). El pedido de esta tarea lo pide persistido:
`datos.respondidas.jsonl` (append bajo su mutex; al abrir descarta lo de más de 30 días y
reescribe con tmp + `os.Rename`), detrás de `sameOrigin` + `localRequest`, con "Deshacer".
Guarda solo `{At, ChannelID, Group}` (el ID del grupo lo arma Go). Cambia la decisión 6.

**(e) Alertas de insultos y quejas técnicas con sus mensajes.** Ver los mensajes muestra texto
y autor en el dashboard; ya se muestran en el chat en vivo y en "Quejas técnicas" por reglas
(`partials.html:101-103`), así que no se expone nada nuevo (dashboard local, sin cambiar el
default `localhost`). El modelo **nunca** devuelve textos ni IDs de YouTube: devuelve el número
`n` de cada mensaje dentro de la tanda; Go valida `1 ≤ n ≤ len(enviados)` y sin repetidos y lo
convierte en `enviados[n-1].ID`, `VideoID` y `At`. Las alertas buscan esos IDs en el `Store`
(copias) y muestran autor, hora y texto originales; un mensaje vencido o que no está, no se
muestra. Los textos del modelo (motivos, preguntas de grupo) se cortan y salen escapados.

**(f) "Lo típico del programa".** Propuesta: mediana del % de cada sentimiento en las franjas de
5 min con al menos 20 mensajes etiquetados del **mismo canal y mismo programa** (mismo
`ProgramName`, comparado sin tildes ni mayúsculas) de los **últimos 7 días**, sin la franja
actual; hacen falta al menos 6 franjas. Si no hay, se usa el tramo de hoy del programa (las
franjas anteriores de hoy, también 6 como mínimo, que es lo aprobado en CA3.7). Si tampoco, las
tarjetas dicen "todavía sin historia para comparar" y no hay cartel. Como las etiquetas empiezan
con F3, la comparación con 7 días se arma sola durante la primera semana. Con "Todo el día del
canal", lo típico es lo del mismo canal en los últimos 7 días.

**(g) Plegado y refresco de 30 s.** Un `<details>` abierto se cerraría en cada refresco (ver
Contexto). Sin JS propio, el estado viaja como `ver` y `horas`: un parámetro `abrir` (lista
cerrada: `preguntas`, `quejas`, `alertas`), conservado en un input oculto `.vq` y en el `hx-get`
del refresco; el `<summary>` lleva un `hx-get` que vuelve a pedir el análisis con esa sección
cambiada. Es una regla de estilo nueva para AGENTS.md.

**(h) Quitar secciones aprobadas.** Quitar Filtros, Temas (por reglas) y las Categorías globales
contradice la decisión 4 y la 12.5 de la 010. Con Filtros se pierden la lista para copiar en
"Palabras bloqueadas" de YouTube Studio y el aviso "conviene activar Bloquear vínculos".

## Objetivo

Que la persona agregue, pause y quite canales y elija el intervalo de chat de cada uno (pico y
fuera) desde Canales, viendo antes el impacto en la cuota; que Claude etiquete el chat en tandas
(Haiku 5.5, cada 30 s por defecto) con modelo, tandas y tope configurables y el estado a la
vista; y que En vivo muestre, del programa en curso, el sentimiento como en la imagen 2, alertas
de insultos y quejas técnicas con sus mensajes, y preguntas agrupadas ordenadas por cantidad con
"Marcar respondida", sin las secciones que la persona no usa y con lo secundario plegado.

## Fases y PRs

| Fase | Qué | Depende de | Riesgo |
|---|---|---|---|
| **F1** | Quitar Filtros, Temas (por reglas) y Categorías de "Todo el monitor"; plegar Preguntas agrupadas y Quejas del resumen con estado que sobrevive al refresco | — | Bajo: plantillas y `server.go`, sin cuota ni datos |
| **F2** | Canales editables (agregar, pausar, quitar), intervalo de chat por canal (pico / fuera), cuota estimada antes de guardar, `datos.canales.json`, ADR 0005 | — | Medio-alto: toca el worker (`nextChat`) |
| **F3** | Tandas de Claude, etiquetas por mensaje (`datos.etiquetas.jsonl`), tarjeta "Análisis con Claude" editable con sus tres tarjetas de estado, ADR 0008 | F2 (`Settings`) | Alto: gasta plata, goroutine del analista |
| **F4** | En vivo: "Sentimiento del chat", alertas con mensajes, "Preguntas agrupadas para producción" con "Marcar respondida" persistida | F3 (etiquetas) | Medio: vista y un archivo nuevo chico |
| F5 (opcional) | Lo que quedaba de la etapa 3 de la 010 y no se pidió ahora | F3 | — |

**Recomendación: un PR por fase**, en ese orden. F1 entra rápido y le saca ruido a la pantalla.
F2 toca el worker y la cuota: conviene revisarlo solo. F3 sin F4 ya da valor medible: deja
correr las tandas unos días con las tarjetas de Canales (mensajes, gasto real, última tanda) y
permite comparar el costo con la hipótesis **antes** de construir la vista de F4. Un solo PR
mezclaría worker, plata y plantillas en una revisión enorme (la 010 tuvo ~15 tests de texto
que ajustar con una sola etapa).

## Criterios de aceptación

Tests nuevos en `monitor_test.go` (salvo donde se indica), escritos **antes** del código, con
datos armados en el test, sin red y sin `datos.jsonl`. En cada fase el primero falla hoy **en
ejecución** (no por compilación): guardar esa salida para el PR.

### F1 — quitar y plegar

- [x] CA1.1 (falla hoy): `GET /parcial/vivo/analisis` (`?video=v1`, `?canal=…&ver=programa` y
  `?canal=…&ver=dia`) **no** contiene "Filtros para YouTube Studio", `id="filtros"`,
  "Palabras bloqueadas" ni "Temas (por reglas)"; el bloque `id="monitor"` no contiene
  `<h3>Categorías</h3>` ni "Por reglas simples; el detalle fino" y sí "Palabras más repetidas"
  y "Quiénes más escriben"; la tarjeta `id="categorias"` del alcance sigue — se verifica con
  `TestLiveRemovedSections` (tabla de las tres rutas; hoy falla porque aparece "Filtros para
  YouTube Studio").
- [x] CA1.2: en el resumen de Claude de En vivo (`claude-block` no compacto), "Preguntas
  agrupadas" y "Quejas" van cada una en `<details class="plegable">` con
  `<summary>Preguntas agrupadas (N)</summary>` / `<summary>Quejas (N)</summary>` (N = cantidad
  de grupos), **cerradas** por defecto; el resumen, el sentimiento y los temas siguen a la
  vista; sin grupos no hay `<details>`. El detalle de Programas no cambia — se verifica con
  `TestClaudeCard` extendido y `TestProgramDetail` sin cambios.
- [x] CA1.3: el plegado sobrevive al refresco: con `abrir=preguntas` (o `quejas`, o los dos
  separados por coma) el `<details>` sale con `open`; el valor se conserva en
  `<input type="hidden" class="vq" name="abrir">` y en el `hx-get` del refresco de
  `#vivo-analisis` y de los botones de `ver` / `horas`; cada `<summary>` tiene un `hx-get` al
  mismo parcial con su sección agregada o sacada de `abrir`; valores fuera de la lista se
  ignoran (no se reflejan en el HTML) — se verifica con `TestLiveFoldState` (tabla: vacío,
  uno, dos, valor inválido con `<script>`, conservado al cambiar `ver`).
- [x] CA1.4: `TestLiveScreenOrder`, `TestDashboardWithoutRemovedSections` y `TestClaudeCardOff`
  actualizados al orden sin `#filtros`: `#quejas`, `#claude-card`, `#preguntas`, `#categorias`,
  `#momentos`, `#vivo-abajo`, `#supers`, `#monitor`.
- [x] CA1.5: `summary` de 44 px de alto como mínimo, con flecha (el marcador nativo o `::marker`
  con tokens) y foco visible; ningún color fuera de los tokens; `TestTemplateTokens` en verde.
- [x] CA1.6: lo que deja de usarse se borra: `Analysis.Filters`, `Analysis.Candidates`,
  `Analysis.Links`, `candidates()` y `FilterLine`, con sus casos de `TestAnalyze`; las listas de
  palabras que usa `Classify` no se tocan; `Analysis.Topics` queda (lo usan Programas y los
  momentos) — se verifica con `staticcheck` limpio y
  `grep -n "Candidates\|FilterLine" *.go` sin resultados.
- [x] CA1.7: README (líneas de "Filtros para YouTube Studio" y "Todo el monitor") y AGENTS.md
  (`monitorView` sin categorías; regla de estilo del plegado con `abrir`) — se verifica con
  `grep -n "Filtros para YouTube Studio" README.md AGENTS.md` vacío.
- [x] CA1.8: la cuota de YouTube no cambia: `git diff main -- youtube.go worker.go` vacío.
- [ ] CA1.9: revisado en el navegador con la vista previa (claro, oscuro, celular): las
  secciones no están, el plegado abre y cierra con la flecha y sigue abierto después de 30 s.
  Manual, anotado en el PR.
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde (`gofmt -l .` vacío,
  `go vet ./...`, `staticcheck`, build Linux y Windows, `go test -race -count=1 ./...`,
  `go test -count=5 ./...`, reviewer sin bloqueantes, PR con la salida de los gates locales).

### F2 — canales editables

Mismo contenido que la etapa 2 de la 010 (que adoptaba CA2.1-CA2.9 y CA2.11 de
canales-continuos con sus decisiones: pausar solo el chat, editar solo desde la PC del monitor,
gana el archivo con aviso, valores iniciales `-chat-seg* × N`), escrito acá completo:

- [x] CA2.1 (falla hoy): `nextChat` por canal: cada canal tiene su intervalo (pico / fuera)
  entre dos conexiones o lecturas **de sus vivos** (por turnos entre ellos); entre canales, a lo
  sumo una acción por tick; se respeta el `wait` de `pollingIntervalMillis`; un canal en pausa
  no se elige; los vivos sueltos de `-vivos` usan `-chat-seg-pico` / `-chat-seg` — se verifica
  con `TestNextChatByChannel` (tabla con `now` fijo; el primer caso usa dos vivos de canales
  distintos que con un solo `lastChat` no pueden estar listos a la vez).
- [x] CA2.2: con el worker andando, el canal A con 50 ms abre al menos 3 conexiones en 3,5 s y
  el B con 1 h abre 1; el falso nunca ve dos `liveChat/messages` comunes en curso a la vez — se
  verifica con `TestWorkerChatPerChannel`, también con `-race`.
- [x] CA2.3: `LoadSettings`: sin `datos.canales.json` arranca con `-canal` e intervalos
  `-chat-seg-pico × N` / `-chat-seg × N`; con el archivo, manda el archivo y si se pasó
  `-canal` o `-chat-seg*` a mano (se detecta con `flag.Visit`) loguea un `aviso:`; un archivo
  ilegible frena el arranque con un error en español que dice qué hacer y no se pisa — se
  verifica con `TestLoadSettings`.
- [x] CA2.4: `Settings.Update` valida (1 a `maxChannels` = 5 canales, `ParseChannel` no vacío,
  sin repetidos, intervalos múltiplos de 30 s entre 30 s y 10 min) y guarda con tmp +
  `os.Rename`; si no puede guardar, devuelve el error y queda la configuración anterior — se
  verifica con `TestSettingsUpdate`.
- [x] CA2.5: el worker toma los cambios sin reiniciar (compara `version` en cada tick): un canal
  agregado lo resuelve el worker con `spend` (1 unidad, sumada a `st.Units()`); uno quitado
  corta los streaming de sus vivos y deja de revisarse; uno en pausa no lee chat pero sigue la
  revisión de sus vivos; si se quita el principal, pasa a serlo el siguiente; no se puede quitar
  el último — se verifica con `TestWorkerAppliesSettings`.
- [x] CA2.6: los handlers nunca llaman a YouTube: `POST /canales/agregar` con un `@handle`
  responde sin que el falso reciba pedidos; el canal queda "resolviendo…" hasta que el worker lo
  resuelve o anota "no se encontró el canal @x" — se verifica con
  `TestChannelsAddDoesNotCallAPI`.
- [x] CA2.7: `EstimateQuota` con un grupo por canal: el ejemplo (2 canales, `-pico 7-20`,
  Rivadavia 2 / 5 min y 24 h en vivo, Rock & Pop 3 / 5 min y 18 h, cuota 9.000) da videos 1.920,
  comentarios 1.000, Rivadavia 2.610, Rock & Pop 1.600, total 7.130 y "Entra en el día, quedan
  370"; un canal en pausa suma 0 de chat — se verifica con `TestEstimateQuota` extendido.
- [x] CA2.8: `GET /canales` editable desde loopback: formulario "Agregar canal" (`<label>`,
  placeholder "Link, @handle o ID del canal", `hx-post="/canales/agregar"`); por canal, selects
  "Actualizar el chat cada" (pico: 30 s, 1, 1:30, 2, 3, 5 min) y "Fuera del pico, cada" (1, 2, 3,
  5, 10 min) con el valor actual elegido (y agregado si no está en la lista), "Cuota estimada de
  este chat: N unidades por día (X h en vivo | sin historial)", "Pausar" / "Reanudar"
  (`POST /canales/pausar`, inmediato) y "Quitar" (`POST /canales/quitar`, `hx-confirm` "¿Quitar
  <canal>? Lo guardado queda hasta que venza (30 días)."); abajo "Los cambios se aplican sin
  reiniciar el monitor", "Descartar" (`GET /parcial/canales/form`) y "Guardar cambios"
  (`POST /canales`). Un cambio de select pide `GET /parcial/canales/cuota` con `hx-include` y
  recalcula la barra sin escribir. El formulario no tiene `every` (no pisa lo que se edita); el
  estado de cada tarjeta sí se refresca. Desde un pedido que no es de loopback: "Para cambiar la
  configuración, abrí el dashboard en la PC del monitor" y ningún control — se verifica con
  `TestChannelsPage` (tabla; reemplaza a `TestChannelsPageReadOnly`).
- [x] CA2.9: guardas: `POST /canales`, `/canales/agregar`, `/canales/pausar` y
  `/canales/quitar` responden 403 sin tocar `datos.canales.json` si falla `sameOrigin` o
  `localRequest` (`guard.go`, sin copiar otra versión); cuerpo de 16 KB como mucho; el mismo
  pedido válido da 200 — se verifica con `TestChannelsPostGuards` (tabla).
- [x] CA2.10: En vivo y Programas toman los canales nuevos o quitados sin reiniciar (los botones
  salen de `Status().Channels`); un canal quitado deja de tener botón y sus programas siguen en
  Programas hasta que vence la retención — se verifica con un caso de `TestWorkerAppliesSettings`
  más `GET /parcial/vivo/canal` y `/parcial/programas`.
- [x] CA2.11: `go run . -h` (no toca ninguna API) describe `-canal`, `-chat-seg-pico` y
  `-chat-seg` como valores iniciales cuando no existe `datos.canales.json`.
- [x] CA2.12: con `Settings` en `nil` (modo informe, tests de hoy) todo sigue igual:
  `TestFlowAgainstFakeAPI` con el mismo `yt.Units`; `git diff main -- youtube.go` vacío.
- [x] CA2.13: README (Canales editable, intervalos por canal, cifras de cuota por canal,
  `datos.canales.json`), AGENTS.md y CLAUDE.md ("Reglas que cambian", F2) y ADR 0005 — se
  verifica con `git diff --stat main` y `ls docs/decisions/0005-*.md`.
- [ ] CA2.14: navegador con la vista previa (claro, oscuro, celular): agregar (queda
  "resolviendo…"), cambiar un intervalo (la barra cambia antes de guardar), descartar, guardar,
  pausar y quitar. Manual, anotado en el PR.
- [ ] Gates de `CLAUDE.md` en verde.

### F3 — Claude en tandas, configuración y estado

- [ ] CA3.1 (falla hoy): `GET /canales` desde loopback, con el analista configurado, muestra en
  "Análisis con Claude" un `<form>` con `<select name="modelo">` (las claves de `claudePrices`;
  `claude-haiku-5-5` como "Claude Haiku 5.5 (rápido, recomendado)"), `<select name="tanda">`
  (30 s, 1, 2 y 5 min; ayuda "el dato llega con esa demora"), `<input name="tope"
  type="number" min="0" max="50" step="0.01">` (ayuda "al llegar, se vuelve a las reglas"), cada
  uno con su `<label>`, y el botón "Guardar" con `hx-post="/canales/claude"`; desde un pedido que
  no es de loopback, los valores en texto y ningún control — se verifica con
  `TestChannelsClaudeCard` (hoy falla: Canales no tiene `<select>`).
- [ ] CA3.2: `batchPrompt` (pura): una línea por mensaje `[n] HH:MM:SS texto`, **sin** `Author`,
  `ID`, likes ni montos; sin los que `Classify` marca spam; texto cortado en 300 runas; a lo sumo
  `claudeBatchMax` (300) mensajes, los más viejos primero (el resto va en la siguiente); lleva
  canal y programa, los grupos de preguntas abiertos del programa (texto, a lo sumo 20) y los
  motivos usados en los últimos 10 min (a lo sumo 10 por tipo); delimitadores y `system` que
  dice que el material no son instrucciones — se verifica con `TestBatchPrompt` (autor testigo,
  ID testigo y `https://` que no aparecen).
- [ ] CA3.3: `validBatchResult` (pura): `n` fuera de rango o repetidos se descartan;
  `sentimiento` ∉ {positivo, neutro, negativo} o `categoria` ∉ `categories` → ese mensaje queda
  sin etiqueta; `grupo` que no es un abierto ni `nuevoK` → sin grupo; grupos nuevos sin texto se
  descartan y el ID lo pone Go (`<canal>|<unix de la tanda>|<k>`, estable entre reinicios);
  motivos cortados en 60 runas, 5 por tipo, con sus `mensajes` validados igual que `n`; todos los
  conteos en Go — se verifica con `TestBatchResultValidate`.
- [ ] CA3.4: tandas en `RunAnalyst` (la misma goroutine): cada `BatchEvery` (por defecto 30 s),
  por canal con "Analizar el chat con Claude", una tanda con los mensajes de chat nuevos desde la
  última etiquetada, con el modelo de tiempo real; **sin mensajes nuevos no hay pedido**;
  prioridad botón de la 009 > tanda vencida > programa terminado; un mensaje no se manda dos
  veces; al arrancar se etiqueta solo desde 10 min antes (`batchCatchUp`); el tick del analista
  pasa a `min(30 s, BatchEvery / 2)`; vuelve al cancelar el contexto también con un pedido en
  curso — se verifica con `TestAnalystBatches` (tick achicado; el falso cuenta pedidos y
  mensajes y guarda el `model` de cada uno), también con `-race`.
- [ ] CA3.5: tope y reparto: un solo tope diario (día local, contador de la 009); tandas como
  mucho `tope × 0,75`; cierres de programa y botón pueden usar el tope entero. Antes de cada
  tanda, peor caso con `claudeBatchMaxTokens` = 12.000 de salida; si no entra, no se manda y el
  estado dice "Se llegó a la parte del tope para tiempo real (US$ X): se vuelve a las reglas
  hasta mañana" — se verifica con `TestAnalystBatchBudget` (tope chico: 0 tandas y los cierres
  siguen; con US$ 3 la parte es 2.250.000 micro-dólares).
- [ ] CA3.6: `datos.etiquetas.jsonl`: un `ClaudeBatch` por línea (`At`, `ChannelID`, `From`,
  `To`, `Model`, tokens, `CostMicros`, `StopReason`, `Labels []MessageLabel{ID, VideoID, At,
  Program, Sentiment, Cat, Tech, Group}`, `NewGroups`, `Motives`, `Error`); al abrir descarta lo
  que tiene `To` de hace más de 30 días, líneas ilegibles y de más de 256 KB, y reescribe con tmp
  + `os.Rename`; `datos.jsonl` queda byte a byte igual después de una tanda; `Store.Labels`,
  `Store.Batches` y `Store.SentSlots` devuelven copias o agregados — se verifica con
  `TestStoreBatchesOpen` (tabla, como `TestStoreLivesOpen`) y un chequeo en `TestAnalystBatches`.
- [ ] CA3.7: configuración de Claude: `POST /canales/claude` (guardas de CA2.9, cuerpo de 1 KB)
  valida modelo ∈ `claudePrices`, tanda ∈ {30, 60, 120, 300} s y tope entre 0 y 50 (acepta coma
  decimal: "2,5"), y guarda la sección `Claude` de `datos.canales.json` con `Settings.Update`;
  más el check por canal "Analizar el chat con Claude" (activado por defecto, también en los que
  se agregan). Precedencia: sin la sección valen `-claude-modelo-vivo` (nuevo, por defecto
  `claude-haiku-5-5`), `-claude-tanda-seg` (nuevo, 30) y `-claude-usd`; con la sección, gana el
  archivo y un flag pasado a mano se avisa. Con clave, `RunAnalyst` arranca siempre y no manda
  nada con tope 0; el analista toma modelo, tanda y tope sin reiniciar — se verifica con
  `TestSettingsClaude` y `TestChannelsClaudePost` (tabla: valores inválidos 400 sin escribir,
  403 de las guardas, tope 0 → 0 pedidos, cambio de modelo → el siguiente pedido usa el nuevo).
- [ ] CA3.8: tarjetas de estado de Canales, por los mensajes de hoy (día local) de los canales
  con el check: "Mensajes analizados hoy N — de M (K en la próxima tanda)" (M = chat no spam
  desde el inicio de las tandas de hoy; N = con etiqueta; K = pendientes, a lo sumo 300); "Gasto
  estimado de hoy US$ x,xx — con los tokens que informa la API" y debajo "tiempo real US$ a ·
  resúmenes US$ b · tope US$ c"; "Última tanda hace N s — sin errores" o "— <error>" o
  "Todavía no hubo tandas hoy". El chip dice "Clave cargada" (nunca la clave ni parte de ella) —
  se verifica con `TestChannelsClaudeStatus` (tabla con un `getenv` falso con clave testigo que
  no aparece en el HTML).
- [ ] CA3.9: `claudeProjection` (pura) al lado del modelo: con tandas de hoy, el gasto de tiempo
  real de hoy llevado a 24 h y escalado por el precio del modelo elegido (Sonnet = 20 × Haiku,
  Opus = 40 × Haiku); sin tandas hoy, la hipótesis de la tabla de costos con la tanda elegida;
  si no entra en el 75 % del tope: "Con <modelo> gastaría ~US$ X por día y la parte del tope es
  US$ Y: se cortaría a las ~HH:MM" — se verifica con `TestClaudeProjection`.
- [ ] CA3.10: privacidad y clave: la tanda no lleva autores (testigo); los errores de las tandas
  pasan por el mismo tapado de la clave que la 009 — se verifica con un caso de tanda en
  `TestClaudeErrorsHideKey`.
- [ ] CA3.11: vista previa: la API simulada responde también el esquema de las tandas (distingue
  el pedido por el esquema) y la copia suma `datos.etiquetas.jsonl` con la regla de CA1.21 de la
  010 (carpeta privada `0700`, se borra al cortar, si no existe se sigue); sigue sin copiar
  `datos.canales.json` ni `datos.claude.json` — se verifica con `TestFakeClaudeMessages` y
  `TestPreviewStartup` extendidos en `tools/preview` (datos sintéticos en `t.TempDir()`).
- [ ] CA3.12: la cuota de YouTube no cambia: `git diff main -- youtube.go worker.go` vacío y
  `TestFlowAgainstFakeAPI` con el mismo `yt.Units`.
- [ ] CA3.13: `go run . -h` describe `-claude-modelo-vivo`, `-claude-tanda-seg`, `-claude-modelo`
  (resúmenes de cada programa) y `-claude-usd` como valores iniciales cuando
  `datos.canales.json` no tiene la sección `Claude`.
- [ ] CA3.14: README (tandas, costos de la tabla, tope sugerido, `datos.etiquetas.jsonl`, qué se
  manda), AGENTS.md, CLAUDE.md ("Reglas que cambian", F3) y ADR 0008 — se verifica con
  `git diff --stat main`, `grep -n "datos.etiquetas.jsonl" README.md AGENTS.md` y
  `ls docs/decisions/0008-*.md`.
- [ ] CA3.15: navegador con la vista previa (API de Claude simulada): guardar modelo, tanda y
  tope; las tres tarjetas se actualizan. Manual, anotado en el PR.
- [ ] Gates de `CLAUDE.md` en verde.

### F4 — En vivo con Claude

- [ ] CA4.1 (falla hoy): `GET /parcial/vivo/analisis?canal=…&ver=programa` con el analista
  configurado y sin etiquetas tiene `id="sentimiento"` con "Sentimiento del chat", el chip
  "Claude" y "Todavía no hay mensajes analizados por Claude en este programa"; con Claude
  apagado, "El sentimiento lo calcula Claude: está apagado (ver Canales)"; en En vivo ya no
  aparece la barra `class="csent"` del resumen (en Programas sí) — se verifica con
  `TestSentimentCard` (hoy falla: no hay `id="sentimiento"`).
- [ ] CA4.2: `sentimentSlots` (pura): franjas de 5 min con positivos / neutros / negativos
  etiquetados (sin spam); franjas con menos de `minSentMsgs` (20) quedan "sin color"; las tres
  tarjetas son la franja de los últimos 5 min con "±N pts sobre lo típico"; encabezado "N
  mensajes en los últimos 5 min"; cartel "El negativo sube: X % en los últimos 5 min, contra Y %
  típico del programa. La causa principal son <motivo negativo más usado>." si X ≥ Y + 5 y la
  franja tiene 20 o más — se verifica con `TestSentimentSlots` (bordes 19/20 mensajes, +4/+5 pts,
  sin motivos).
- [ ] CA4.3: lo típico según (f): mismo canal y programa en 7 días (≥ 6 franjas de 20 o más, sin
  la actual); si no, el tramo de hoy (≥ 6); si no, "todavía sin historia para comparar" y sin
  cartel — se verifica con `TestSentimentTypical` (tabla: con 7 días, solo hoy, sin historia,
  programa con otro nombre que no cuenta).
- [ ] CA4.4: gráfico "Cómo fue cambiando · cada 5 min": SVG de barras apiladas al 100 %
  (positivo abajo, neutro, negativo arriba) con `role="img"`, `aria-label` y `<title>` por barra;
  con "Programa en curso" las últimas 3 h del canal, con "Todo el día" desde las 00:00; línea
  punteada en cada cambio de programa con la etiqueta "HH:MM · empieza <programa>" y "HH:MM en
  curso" al final (textos en HTML, como `participationChart`); leyenda con "Menos de 20
  mensajes: sin color"; colores `--sent-pos`, `--sent-neu`, `--sent-neg` y uno nuevo
  `--sent-low` en los dos temas — se verifica con `TestSentimentChart` y `TestTemplateTokens`.
- [ ] CA4.5: "Por qué hay mensajes negativos" / "Qué está gustando": motivos de las tandas de
  los últimos 5 min juntados por texto normalizado (minúsculas, sin tildes), % sobre los
  negativos (o positivos) de esos 5 min, los 3 primeros más "Otros", hasta dos citas por motivo
  sacadas del `Store` por ID (texto sin autor) — se verifica con `TestMotives`.
- [ ] CA4.6: alertas (`Alerts`, pura, umbrales fijos de la 010): **quejas técnicas** ≥ 15 en los
  últimos 5 min (`Tech` de Claude o, sin etiqueta, `TechIssue`); **insultos** ≥ 5 % de los
  mensajes de los últimos 10 min con al menos 20 mensajes (categoría de Claude o, sin etiqueta,
  `Classify`). Cada alerta con su umbral y "Ver los mensajes (N)" plegado (`abrir=alertas`) con
  hasta 10 mensajes: autor, hora y texto del `Store`, nunca texto del modelo. Sin alertas: "Sin
  alertas: N quejas técnicas en 5 min (umbral 15) · X % de insultos en 10 min (umbral 5 %)". La
  tarjeta `id="alertas"` reemplaza a `id="quejas"`; la nota dice qué parte es de Claude y qué
  de reglas — se verifica con `TestAlerts` (bordes 14/15, 4,9 %/5 %, 19/20, sin etiquetas, ID
  que ya no está en el `Store`).
- [ ] CA4.7: "Preguntas agrupadas para producción": grupos del alcance ordenados por cantidad
  (empate: el de la pregunta más reciente primero), filas "N · <pregunta>", encabezado "P
  preguntas · G grupos"; los respondidos van al final con "Respondida · Deshacer"; sin Claude o
  sin etiquetas, la lista por reglas de hoy con la etiqueta "por reglas" — se verifica con
  `TestQuestionGroups`.
- [ ] CA4.8: "Marcar respondida" (`POST /preguntas/respondida`, `grupo=…&deshacer=0|1`): 403 sin
  `sameOrigin` + `localRequest`, cuerpo de 1 KB, grupo inexistente 400; guarda en `Answers`
  (`answers.go`: mutex propio, fuera del `Store`) y agrega una línea a `datos.respondidas.jsonl`
  (`{At, ChannelID, Group, Undo}`); al abrir descarta lo de más de 30 días y reescribe con tmp +
  `os.Rename`; el handler no llama a Claude ni escribe el `Store`; el botón (44 px) solo se dibuja
  desde loopback; responde la tarjeta actualizada — se verifica con `TestAnswerPost` (tabla) y
  `TestAnswersOpen`.
- [ ] CA4.9: orden de En vivo con "Programa en curso": `#canal`, selector, tiles, `#alertas`,
  `#claude-card` (resumen con preguntas y quejas plegadas, sin barra de sentimiento),
  `#sentimiento`, `#preguntas`, `#categorias`, `#momentos` (gráfico de participación y panel de
  oyentes **sin cambios**), `#chat`, `#supers`, `#monitor` — se verifica con
  `TestLiveScreenOrder` actualizado.
- [ ] CA4.10: línea de estado debajo del selector: "Claude (Haiku 5.5) · analizado hasta las
  HH:MM:SS · N s de demora · tandas cada 30 s" (del `To` y el `At` de la última tanda del canal)
  o el motivo por el que no analiza (apagado, sin clave, tope de tiempo real, canal sin el
  check) — se verifica con `TestLiveAnalysisClaude`.
- [ ] CA4.11: los handlers no copian 7 días de etiquetas: usan `Store.SentSlots(canal, desde)`
  (agregados por franja y programa) y `Store.Labels(ids)` solo para los mensajes que se
  muestran — se verifica en la revisión y con
  `go test -run '^$' -bench '^BenchmarkSentimentView$' .` (70.000 etiquetas sintéticas; el
  resultado va en el PR, informativo).
- [ ] CA4.12: README (En vivo: sentimiento, alertas, preguntas; `datos.respondidas.jsonl`),
  AGENTS.md y CLAUDE.md ("Reglas que cambian", F4) — se verifica con `git diff --stat main` y
  `grep -n "datos.respondidas.jsonl" README.md AGENTS.md`.
- [ ] CA4.13: navegador con la vista previa (API de Claude simulada) en claro, oscuro y celular:
  sentimiento con cartel, alertas con sus mensajes plegados que siguen abiertos después de 30 s,
  preguntas ordenadas, marcar y deshacer. Manual, anotado en el PR.
- [ ] Gates de `CLAUDE.md` en verde.

### F5 (opcional, según la decisión abierta 9)

Si la persona lo confirma, se adoptan tal cual CA3.1 (columnas `sentimiento` y
`categoria_claude` al final del CSV), CA3.10 (categorías de Claude con caída a reglas y badges en
el chat), CA3.12 ("Desde dónde escriben", que suma `lugar` al esquema de la tanda), CA3.13
(resumen por momento) y CA3.14 (Programas con Claude) de la 010, en `feat/claude-extras`.

## Diseño propuesto

### F1

- `templates/partials.html`: borrar `#filtros` (`:196-215`) y el bloque Categorías de `#monitor`
  (`:220-228`); en `claude-block` (`:268-271`), envolver Preguntas y Quejas en
  `<details class="plegable"{{if .Open…}} open{{end}}>` con `<summary hx-get="…&abrir=…"
  hx-target="#vivo-analisis" hx-swap="outerHTML">`. `claude-items` no cambia (lo usa Programas).
- `server.go`: `monitorView` sin `Cats`; `analysisData` sin `FilterLine` ni `Topics`;
  `keepView` y la plantilla suman `abrir` (parseo con lista cerrada, `foldParam(q) map[string]bool`
  y `foldToggle(open, sección) string` puras); `ClaudeBlock` suma qué secciones van abiertas.
- `analyze.go`: borrar `Filters`, `Candidates`, `Links` y `candidates()` (CA1.6).
- `templates/layout.html`: `.plegable summary` con `min-height: 44px` y tokens.
- **Test a escribir primero:** `TestLiveRemovedSections` (CA1.1).
- **Descartadas:** `hx-preserve` en el `<details>` (conserva el estado pero congela el contenido
  hasta recargar la página); sacar el resumen del refresco de 30 s (se desactualiza el estado
  "Analizando…"); JS propio para recordar el `open` (prohibido).

### F2

Todo el diseño de la etapa 2 de canales-continuos (`docs/specs/2026-11-05-canales-continuos.md`,
"Diseño propuesto → Etapa 2") con los cambios de la 010 ("Diseño propuesto → Etapa 2"):

- **`channels.go` (nuevo):** `Settings` con `sync.Mutex`, la lista de `ChannelSetting{Input,
  ChatPeakSec, ChatOffSec, Paused}` y un contador `version`; `LoadSettings(path, fromFlags)`,
  `Get()` (copia) y `Update(f)` que valida, guarda con tmp + `os.Rename` y recién ahí cambia la
  lista y suma `version`. `WorkerConfig.Settings *Settings`; con `nil`, todo como hoy.
- **`worker.go`:** `nextChat` por grupo (canal configurado o `""` para los sueltos); un vivo
  está listo si no está bloqueado, no tiene el streaming abierto, su grupo no está en pausa,
  cumplió el `wait` de YouTube y su grupo cumplió su intervalo; entre los listos, el del grupo
  más atrasado; **una acción por tick**, así nunca hay dos lecturas comunes en paralelo. En cada
  tick compara `version`: resuelve los canales nuevos con `spend`, corta los streaming de los
  quitados y adelanta `nextVideos`. `chatInfos` calcula `Gap`, `Lives` y `Next` por grupo.
- **Rutas:** `GET /parcial/canales/form`, `GET /parcial/canales/cuota`, `POST /canales`,
  `POST /canales/agregar`, `POST /canales/pausar`, `POST /canales/quitar`.
- **Cuota:** `EstimateQuota` (ya existe, `quota.go`) recibe un `ChatGroup` por canal.
- **Archivo:** `datos.canales.json`, solo lo que escribió la persona:
  `{"Channels":[{"Input":"UCLAj5TzCrNbqe3LosSKHLcQ","ChatPeakSec":60,"ChatOffSec":180,"Paused":false}]}`.
  Nombre e ID resueltos quedan en memoria (`ChannelStatus`).
- **Test a escribir primero:** `TestNextChatByChannel` (CA2.1).
- **Descartadas:** mandar la configuración al worker por un canal de Go (el handler igual
  necesita leerla; bloquearía durante una lectura); resolver el `@handle` en el handler (usaría el
  cliente desde otra goroutine y gastaría fuera de `spend`); "Pausar" que espera a "Guardar"
  (necesita un borrador del lado del servidor).

### F3

- **`batch.go` (nuevo):** `batchPrompt`, `validBatchResult`, `ClaudeBatch`, `MessageLabel`,
  `claudeProjection`, el esquema de la tanda:

  ```json
  {"mensajes":[{"n":1,"sentimiento":"positivo","categoria":"pregunta","tecnica":false,"grupo":"g3"}],
   "grupos_nuevos":[{"clave":"nuevo1","pregunta":"¿Vuelve el invitado la semana que viene?"}],
   "motivos":[{"tipo":"negativo","motivo":"Problemas de audio","mensajes":[2,5]}]}
  ```

  Salida estructurada con `output_config.format` y `effort: "low"` como la 009, sin
  `tool_choice`, `thinking`, `temperature`, `top_p`, `top_k` ni prefill. Que Haiku 5.5 acepte
  `effort: "low"` (H6 de la 009) es condición de esta fase: se prueba con la API simulada y se
  confirma con la primera tanda real que pida la persona.
- **`analyst.go`:** un paso más por vuelta (tandas vencidas por canal), modelo por pedido
  (`cl.Model` se cambia dentro de la goroutine, que es la única que usa el cliente), lectura de
  `Settings.Get()` en cada tick, reparto 75 / 25 del tope. Un cierre de programa (decenas de
  segundos, hasta `claudeTimeout`) atrasa una tanda: la siguiente trae más mensajes, nada se
  pierde; la línea de estado muestra la demora.
- **`store.go`:** `datos.etiquetas.jsonl` (`loadJSONL`, retención por `To`), índice en memoria
  `map[Message.ID]MessageLabel`, grupos por canal, `Labels(ids)`, `Batches(canal, desde)`,
  `SentSlots(canal, desde)` (agregados por franja de 5 min y programa, bajo el lock de lectura,
  sin copiar etiquetas). `MessageLabel.Program` se toma de `Message.Program` al etiquetar, para
  que lo típico por programa (F4) no tenga que releer mensajes de 7 días.
- **`channels.go`:** sección `Claude{RealtimeModel, BatchSec, BudgetMicros}` y
  `ChannelSetting.Claude bool` (por defecto `true`). El tope se guarda en micro-dólares
  (`int64`), no en `float`.
- **`main.go`:** flags `-claude-modelo-vivo` y `-claude-tanda-seg`; con clave, `RunAnalyst`
  arranca siempre (tope vigente 0 = no manda nada); avisos de precedencia con `flag.Visit`.
- **`server.go` / `templates/canales.html`:** tarjeta editable (CA3.1, CA3.7-CA3.9),
  `POST /canales/claude`. El modelo de resúmenes se muestra como texto: "Resúmenes de cada
  programa: <modelo> (se cambia con -claude-modelo)".
- **`tools/preview`:** esquema de tandas en la API simulada y copia de `datos.etiquetas.jsonl`.
- **Test a escribir primero:** `TestChannelsClaudeCard` (CA3.1).
- **Descartadas:** una goroutine aparte para las tandas (dos goroutines chequeando el mismo tope
  pueden pasarlo; haría falta una reserva atómica); dos clientes `Claude` (el modelo es un campo
  del pedido); reenviar la ventana de 5 min en cada tanda (manda lo mismo varias veces);
  guardar la etiqueta en `Message` (cambia `datos.jsonl`); prompt caching (centavos por día con
  Haiku; se revisa con los costos medidos).

### F4

- **`batch.go`:** `sentimentSlots`, `sentimentTypical`, `Motives`, `Alerts`, `questionGroups`
  (puras).
- **`answers.go` (nuevo):** `Answers` con mutex, `OpenAnswers(path)` (retención 30 días, tmp +
  `os.Rename`), `Mark(canal, grupo, undo, now)` (append de una línea), `Answered(grupo) bool`.
  Vive en el `Server`, como la cola del `Analyst`. Es lo único además de `Settings` que escriben
  los handlers.
- **`server.go` / `templates/partials.html`:** tarjetas `#alertas`, `#sentimiento` y
  `#preguntas` nuevas; `claudeBlock` sin `Sent` en En vivo (`claude-sent` se sigue usando en
  Programas); la línea de estado de Claude. `abrir` suma `alertas`.
- **Test a escribir primero:** `TestSentimentCard` (CA4.1).
- **Descartadas:** reemplazar el gráfico de participación por el de sentimiento (pierde
  "Momentos con más participación (para reels)" y el panel de oyentes; ver decisión abierta 6);
  umbrales de alertas configurables (la 010 los fijó en el código); guardar "Marcar respondida"
  en `datos.etiquetas.jsonl` (mezcla lo que escribe el modelo con lo que decide la persona y
  complica la retención).

## Costo de Claude (hipótesis, para F3)

Hipótesis de volumen de la 009 y la 010 (explícita): Rivadavia ~300 mensajes de chat por hora
las 24 h (~7.200 por día, ~5 por minuto); un segundo canal como Rock & Pop ~150 por hora unas
18 h (~2.700). Por tanda: ~1.200 tokens fijos de entrada (system, esquema, grupos y motivos
abiertos) + 30 por mensaje; salida ~280 fijos (thinking con esfuerzo bajo; hipótesis H7 de la
009) + 30 por mensaje. Con Haiku 5.5: **~US$ 0,00026 por tanda + ~US$ 0,000018 por mensaje**.
Tandas por día = ventanas con al menos un mensaje (Poisson: con 2,5 mensajes en 30 s, el 92 %
de las ventanas de Rivadavia tienen alguno; con 1,25, el 71 % de las de Rock & Pop).

| Tiempo real con Haiku 5.5 | Tandas por día (Riv. / Riv. + R&P) | Rivadavia sola | Rivadavia + Rock & Pop |
|---|---|---|---|
| **Cada 30 s (propuesto)** | 2.640 / 4.180 | **~US$ 0,82** | **~US$ 1,27** |
| Cada 1 min (lo aprobado) | 1.430 / 2.420 | ~US$ 0,50 | ~US$ 0,81 |
| Cada 2 min | 720 / 1.260 | ~US$ 0,32 | ~US$ 0,51 |
| Cada 5 min | 290 / 500 | ~US$ 0,20 | ~US$ 0,31 |

Con 30 s el 85 % del costo es la parte fija de cada tanda: bajar a la mitad el intervalo casi
duplica el gasto. Con Sonnet 5.5 cada número se multiplica por 20 (~US$ 25 por día con dos
canales cada 30 s) y con Opus por 40.

**Total por día con lo propuesto** (dos canales, tandas cada 30 s con Haiku, resúmenes de
programa con Sonnet como hoy): tiempo real ~US$ 1,27 + resúmenes ~US$ 1,33 (cifra del README) ≈
**US$ 2,6 por día (~US$ 78 por mes)**. Con Rivadavia sola: ~0,82 + ~1,0 ≈ US$ 1,8. Si los
resúmenes también fueran con Haiku (un solo modelo), bajarían a ~US$ 0,07 y el total a ~US$ 1,35.

**Tope y freno.** Con el tope sugerido de la 010 (`-claude-usd 3`), el tiempo real puede usar
US$ 2,25 (1,8 veces lo estimado) y a los resúmenes les quedan 3 − 1,27 = US$ 1,73 contra 1,33:
entra, con ~US$ 0,40 de margen (decisión abierta 4). Peor caso de una tanda (300 mensajes de 300
runas, ~35.000 tokens de entrada y 12.000 de salida): ~US$ 0,01 con Haiku, ~US$ 0,19 con Sonnet;
ninguna tanda llega al escalón de 100.000 tokens de Haiku. **Sensibilidad:** si el chat tuviera
el volumen de la imagen 2 (1.060 mensajes en 5 min, ~212 por minuto), un canal así costaría
~US$ 6,2 por día con Haiku: no entra en US$ 2,25, el tiempo real se corta después de ~9 h de
tandas (a la mañana, contando desde las 00:00) y vuelve a reglas con aviso (CA3.5), y la tarjeta
lo proyecta antes de guardar (CA3.9). Cada tanda guarda tokens, modelo y costo para medir la
primera semana.

## Cuota de YouTube

- **F1, F3, F4:** sin cambios (CA1.8, CA3.12; F4 no toca `worker.go` ni `youtube.go`).
- **F2:** sin archivo, mismo gasto que hoy (intervalos `-chat-seg* × N`). Cada canal suma su
  propio chat (ya no se turnan entre canales; dentro de un canal, sí). Agregar un canal: 1 unidad
  al resolverlo y ~480 por día de revisión, más ≤ 40 por corte. La barra de Canales lo muestra
  antes de guardar. Las cifras del README pasan a ser por canal.

## Reglas de AGENTS.md / CLAUDE.md / README que cambian (texto propuesto)

1. **AGENTS.md:7 y CLAUDE.md:10 ("solo lectura"), F2-F4:** "El bot no escribe en YouTube. El
   dashboard escribe en tres puntos, solo desde la PC del monitor (`sameOrigin` +
   `localRequest`): la configuración (canales, intervalos y Claude, en `datos.canales.json`),
   pedir un análisis a Claude (cola) y marcar una pregunta como respondida
   (`datos.respondidas.jsonl`)."
2. **AGENTS.md, Cuota (F2):** "Cada canal tiene su intervalo de chat (pico y fuera) entre dos
   conexiones o lecturas de sus vivos; con varios vivos del mismo canal se turnan. Cada canal
   suma su propio chat a la cuota. Entre canales, una sola acción por tick: nunca dos lecturas
   comunes en paralelo. Los vivos de `-vivos` que no son de un canal configurado usan
   `-chat-seg-pico` / `-chat-seg`." Y recalcular las cifras.
3. **AGENTS.md, Análisis con Claude (F3):** "Apagado por defecto. Con `CLAUDE_API_KEY`, se activa
   cuando el tope diario (`-claude-usd` o el guardado en Canales) es mayor que 0. Un pedido por
   programa al terminar, otro por botón como mucho cada 10 min por canal, **y tandas** cada
   `BatchEvery` (30 s por defecto) por canal con el check, solo con mensajes nuevos. Tiempo real
   con `claude-haiku-5-5` por defecto (`-claude-modelo-vivo`, editable en Canales); resúmenes de
   programa con `-claude-modelo`. El tiempo real usa como mucho el 75 % del tope. Etiquetas por
   mensaje en `datos.etiquetas.jsonl`. Modelo, tandas y tope se editan en Canales; los flags son
   valores iniciales y lo guardado gana." (Más lo que decida la persona en las decisiones 2 y 3.)
4. **AGENTS.md, Datos y privacidad (F2-F4):** `datos.canales.json` (solo lo que escribió la
   persona); `datos.etiquetas.jsonl` (etiquetas por mensaje, 30 días desde el fin de la tanda,
   tmp + `os.Rename`, nunca en `datos.jsonl`); `datos.respondidas.jsonl` (30 días, tmp +
   `os.Rename`). La vista previa suma `datos.etiquetas.jsonl` a la copia.
5. **AGENTS.md, Concurrencia (F2, F4):** "`Settings` y `Answers` son lo único que escriben los
   handlers, cada uno con su mutex, fuera del `Store`. El worker lee `Settings` en cada tick y
   resuelve los `@handle` con `spend`; `RunAnalyst` lee `Settings` para modelo, tandas y tope."
6. **AGENTS.md, Estilo → Dashboard (F1):** "Lo plegado va en `<details>`; para que siga abierto
   después del refresco, el estado viaja en el parámetro `abrir` (lista cerrada) como `ver` y
   `horas`, sin JS propio."
7. **AGENTS.md, mapa del código y rutas:** `channels.go` (F2), `batch.go` (F3, F4), `answers.go`
   (F4); rutas nuevas de F2, `POST /canales/claude` (F3), `POST /preguntas/respondida` (F4).
8. **README:** F1 (sin Filtros ni Categorías globales), F2 (Canales editable, cuota por canal),
   F3 (tandas, costos, tope sugerido), F4 (En vivo con Claude, respondidas).
9. **ADR 0005 (F2):** "La configuración de canales se edita desde el dashboard y el worker la
   toma en caliente" (el de canales-continuos, con la pantalla de esta spec).
   **ADR 0008 (F3):** "Claude etiqueta el chat en tandas": tandas solo con mensajes nuevos,
   Haiku en tiempo real, una goroutine, reparto 75 / 25, etiquetas en archivo aparte,
   configuración editable con precedencia del archivo, activación con clave + tope > 0;
   "Modifica: ADR 0006". F4 lo amplía con "Marcar respondida" persistida (o un ADR 0009 si la
   persona prefiere uno por decisión).
10. **Specs relacionadas (las marca el orquestador al aprobar):** la 010, "etapas 2 y 3
    reemplazadas por `2026-11-05-canales-editables-claude-vivo.md`".

## Fuera de alcance

- Escribir en YouTube, publicar respuestas, moderar u OAuth ("Marcar respondida" es una marca
  local).
- Autenticación del dashboard, editar desde otras PCs, cambiar el default `localhost`.
- Reordenar canales, elegir el principal, editar `-pico`, `-cuota`, `-comentarios-min`, `-vivos`
  o `-tiempo-real` desde la pantalla.
- Umbrales de alertas configurables; notificaciones fuera del dashboard (sonido, mail).
- Analizar con Claude días anteriores o reetiquetar lo ya etiquetado.
- Prompt caching, Batches API, fallback del servidor ante refusal.
- Cambiar el gráfico de participación y el panel de oyentes (salvo que la decisión 6 diga otra
  cosa).
- F5 hasta que la persona la confirme; la etapa B de la 009.
- `search.list`, base de datos, frontend con build, JS propio, dependencias.

## Riesgos

- **Plata (F3).** Las tandas gastan todo el día. Mitigación: apagado por defecto, peor caso
  antes de cada pedido, 75 % del tope para tiempo real, proyección antes de guardar, tokens y
  costo por tanda guardados. La hipótesis de ~280 tokens de salida fijos con thinking es la más
  incierta: si es el doble, el costo de 30 s sube ~50 %. Se mide la primera semana con F3 sola.
- **Worker (F2).** `nextChat` por canal cambia cómo se gasta la cuota del chat. Mitigación:
  sin archivo el gasto es el de hoy; una acción por tick; `TestWorkerChatPerChannel` con
  `-race`; `TestFlowAgainstFakeAPI` con el mismo `yt.Units` y `Settings` en `nil`.
- **Concurrencia.** Dos escritores nuevos desde handlers (`Settings`, `Answers`), cada uno con su
  mutex y tmp + `os.Rename`; ninguno toca el `Store` ni llama a YouTube o a Claude. El analista
  sigue siendo el único usuario del cliente `Claude`.
- **Datos guardados.** `datos.etiquetas.jsonl` ~1 MB por día (~30 MB a los 30 días), se carga al
  abrir; `datos.respondidas.jsonl` es chico. `Message` y `datos.jsonl` no cambian.
- **Calidad con Haiku en tandas de 30 s.** Menos contexto por tanda: grupos y motivos pueden
  fragmentarse (se le pasan los abiertos para que los reuse) y el sentimiento puede fallar con
  ironía o lunfardo. La tarjeta lo aclara ("Lo arma Claude: puede equivocarse") y se puede pasar a
  Sonnet con la proyección a la vista.
- **Prompt injection.** Sin herramientas, listas cerradas validadas en Go, números de mensaje
  validados, textos del modelo cortados y escapados.
- **Privacidad.** Se manda solo texto y hora; las alertas muestran autor y texto que ya se ven en
  el chat, solo en el dashboard local y plegados.
- **Plegado (F1).** Si htmx no dispara el `hx-get` del `<summary>` como se espera, el plegado
  igual funciona (nativo) pero se cierra a los 30 s: se verifica en el navegador (CA1.9).
- **Tamaño.** Muchos tests afirman textos de En vivo y Canales (`TestChannelsPageReadOnly`,
  `TestLiveScreenOrder`, `TestDashboardWithoutRemovedSections`, `TestClaudeCardOff`,
  `TestClaudeCard`, `TestAnalyze`). Mitigación: cuatro PRs y tests de contenido, no de maquetación.
- **Windows.** Solo stdlib y tmp + `os.Rename`, como el resto de los archivos.

## Preguntas abiertas para la persona que aprueba

1. **Reemplazo de la 010.** ¿Esta spec reemplaza las etapas 2 y 3 de
   `2026-11-05-rediseno-tres-pantallas.md` (recomendado), con cuatro PRs (F1 a F4) en ese orden?
2. **Uno o dos modelos.** La imagen 1 tiene un solo "Modelo". Recomiendo: ese select es el de
   **tiempo real** (Haiku por defecto) y los resúmenes de cada programa siguen con Sonnet por
   `-claude-modelo`, mostrado como texto (respeta la decisión 10). Alternativas: un solo modelo
   para todo (Haiku: ~US$ 1,35 por día en total, resúmenes más pobres) o dos selects.
3. **Tandas por defecto.** ¿30 s, como la imagen (~US$ 1,27 por día con dos canales), o 1 min, lo
   aprobado (~US$ 0,81)? La demora del dato es el intervalo más lo que tarda el pedido.
4. **Tope sugerido.** ¿Sigue en US$ 3 (entra con ~US$ 0,40 de margen según la hipótesis) o se
   sube a US$ 4 hasta medir la primera semana?
5. **"Marcar respondida" persistida.** Cambia la decisión 6 de la 010 (memoria). ¿Persistida 30
   días en `datos.respondidas.jsonl`, con "Deshacer" (recomendado), o en memoria?
6. **"Reemplazalo por el gráfico que se muestra actualmente".** Entiendo que la tarjeta
   "Sentimiento del chat" reemplaza la **barra de sentimiento del resumen de Claude** y que el
   gráfico de participación (momentos para reels y panel de oyentes) queda. ¿Es así, o querés
   reemplazar también el gráfico de participación?
7. **"Lo típico del programa".** ¿Aprobás la definición de (f): mismo canal y programa en los
   últimos 7 días, con caída al tramo de hoy y "sin historia" si no alcanza?
8. **Alertas con autor.** ¿Los mensajes de las alertas muestran el autor (como el chat y las
   quejas técnicas de hoy; recomendado) o solo hora y texto?
9. **F5.** Lo que la 010 aprobó para la etapa 3 y el pedido nuevo no menciona (columnas del CSV,
   categorías de Claude en el chat, "Desde dónde escriben", resumen por momento, columnas de
   Claude en Programas): ¿queda como F5 opcional para después de medir costos (recomendado), se
   suma a F4 o se descarta?
10. **Activar desde la pantalla.** ¿Aprobás que, con la clave cargada, el análisis se pueda prender
    poniendo un tope mayor que 0 en Canales aunque se haya arrancado con `-claude-usd 0`? (cambia
    ADR 0006; sigue apagado por defecto y solo desde la PC del monitor).
11. **Filtros.** Al quitar la tarjeta se pierde la lista para copiar en "Palabras bloqueadas" y el
    aviso de "Bloquear vínculos". ¿Confirmás que no hace falta en otro lado?
12. **Reglas y ADRs.** ¿Aprobás los textos de "Reglas que cambian" y los ADR 0005 (F2) y 0008 (F3)?
