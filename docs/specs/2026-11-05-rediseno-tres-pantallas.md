# Spec: rediseño del dashboard en tres pantallas (En vivo, Programas de hoy, Canales), canales editables y Claude en tiempo real

- **Estado:** borrador
- **Tipo:** mejora (feat)
- **Rama:** tres etapas, tres PRs: `feat/rediseno-tres-pantallas` (etapa 1),
  `feat/canales-editables` (etapa 2) y `feat/claude-tiempo-real` (etapa 3). Cada una sale
  de `main` actualizado después de que entre la anterior.
- **Origen:** pedido de Julian Barbieri (2026-11-05): "Me gustaría que la estética sea
  similar a esta https://claude.ai/artifact/TWMPzsPvw2YTbuNHrARujp, la estructura me parece
  mucho más entendible que la que tengo actualmente y me parece más bonita. Permitiendo así
  agregar canales desde la aplicación. Quiero que se vean igual las 3 secciones (En vivo,
  Programas de hoy, Canales). Hacéme las preguntas que creas necesarias para hacer la spec
  de la mejor manera posible. Tené en cuenta que todavía se está implementando
  factory/jobs/009-analisis-claude/." Diseño de referencia: tres pantallas de un canvas
  (`Main.dc.html` = "Canales", `EnVivo.dc.html` = "En vivo", `Programas.dc.html` =
  "Programas de hoy"), con datos de ejemplo; no está en el repo.
- **Depende de:** la tarea 009 (análisis con Claude, spec
  `docs/specs/2026-11-05-analisis-claude.md` en `feat/analisis-claude`), que tiene que
  entrar a `main` **antes** de empezar la etapa 1 (decisión 1). **Absorbe** la etapa 2 de
  `docs/specs/2026-11-05-canales-continuos.md` (canales editables) como su etapa 2, y la
  etapa C de la 009 (categorías por mensaje) dentro de su etapa 3.
- **Aprobada por / fecha:** pendiente.

## Contexto

### Qué hay hoy en `main` (`bd64942`, verificado leyendo el código)

**Una sola página.** `GET /{$}` (`server.go:43`) sirve `templates/page.html`, que carga
cuatro parciales con htmx (`templates/page.html:177-183`):

| Parcial | Refresco | Qué muestra | Dónde |
|---|---|---|---|
| `/parcial/resumen` | 5 s | nombre del canal principal, "Actualizado HH:MM", lista de **todos** los vivos en curso (título, mirando, nota del chat), error de la API, 6 tiles **globales** (mensajes hoy, última hora, chat últimos 5 min, preguntas hoy, spam e insultos hoy, cuota usada hoy) y "Categorías de hoy" por reglas de todos los mensajes del día (incluidos comentarios) | `server.go:90-143`, `templates/partials.html:1-35` |
| `/parcial/vivo` → `/parcial/vivo/chat` | 2 s | botones por canal (`pickChannel`), "Mostrando X · En pausa: Y", cartel de esperando, cartel del chat (`chatBanner`), feed de 60 mensajes del canal; si cambia el vivo en curso del canal responde `HX-Retarget: #vivo` | `server.go:165-238`, `server.go:337-384` (retarget en `:348-357`), `templates/partials.html:48-84` |
| `/parcial/vivo/analisis` | 30 s | tira "Hoy en el canal" con cartel de continuidad, selector "Programa en curso / Todo el día del canal" y tarjetas numeradas 1 a 6 (temas, preguntas, quejas técnicas, filtros para YouTube Studio, momentos con gráfico de participación y panel de oyentes, Super Chats) | `server.go:459-608`, `server.go:620-649`, `templates/partials.html:88-224` |
| `/parcial/extra` | 30 s | "Palabras más repetidas (24 h)" y "Quiénes más escriben (24 h)", de **todos** los mensajes | `server.go:1025-1032`, `templates/partials.html:37-46` |

`/programas` y `/canales` no existen: el mux solo tiene `GET /{$}` y los parciales
(`server.go:41-52`), así que responden 404. No hay navegación.

**Estilos.** CSS inline en `templates/page.html:8-168` con tokens claro/oscuro
(`:root` en `:9-29`, oscuro en `:30-52`). La paleta clara ya coincide casi entera con la
del diseño: `--surface-0` #f4f4f2, `--surface-1` #fcfcfb, `--border` #e2e1dc,
`--text-primary` #0b0b0b, `--text-secondary` #52514e, `--series-1` #2a78d6, `--grid`
#e9e8e4, `--live` #c62828, `--warn-*`, `--crit-*`, `--ok-*` y `--paid-*` son los mismos
valores. Difieren: `--text-muted` (#7a7974 hoy, #6b6a65 en el diseño) y el color de los
links (hoy `--series-1`, en el diseño #1f63b8). Faltan: naranja de "negativo" (#c2570c),
gris de "neutro" (#b9b8b2), chip "Claude" (#e7eefa / #1d4f91), borde de campos (#cfcec8).

**Canales.** Solo por flags (`-canal`, `-chat-seg-pico`, `-chat-seg`, `-pico`,
`main.go:45-54`); intervalo de chat global con turnos entre todos los vivos
(`worker.go:48-57`, `nextChat` en `worker.go:124-137`). `ChannelStatus`
(`store.go:70-77`) tiene ID, nombre y estado de búsqueda, pero no lo que escribió la
persona en `-canal` (el `@handle`).

**CSV.** `csvHeader` (`main.go:229-230`) termina en `sticker`; `categoria` es
`Classify` (`main.go:232-246`).

### Qué deja la 009 al entrar (worktree `.claude/worktrees/analisis-claude`, sin commitear)

Se leyó para diseñar encima; puede cambiar en su revisión (ver Riesgos).

- `claude.go`: cliente `Claude` (no concurrente), `claudePrices` (Opus 4/20, Sonnet 2/10,
  Haiku 0,10/0,50 con escalón 0,50/2,50 arriba de 100.000 tokens de entrada),
  `claudeCost`, `claudeWorstCase`, `claudeEstimate`, `claudePrompt`, `validClaudeResult`,
  `claudeConfig`, `ClaudeResult{Summary, Sentiment, Topics, Questions, Complaints}`.
- `analyst.go`: `RunAnalyst` (única goroutine que usa `Claude`), cola del botón
  (`Analyst`), `segmentKey` (`canal|vivo|programa|inicio`), `segmentMessages`,
  `currentSegment`, `pendingSegments`, `claudeFrom` (00:00 − 12 h).
- `guard.go`: `sameOrigin` y `localRequest` (loopback + `Host` local).
- `store.go`: `ClaudeAnalysis` en `datos.analisis.jsonl`, gasto en `datos.claude.json`,
  `ClaudeStatus` en `Snapshot.Claude`, `MessagesOf(ids, since)` (copia filtrada).
- `server.go`: tarjeta "7. Análisis con Claude" al final de `vivo-analisis` (`claudeCard`,
  `claudeView`, `ClaudeCard`, `ClaudeBlock`), con "Programa en curso" (último análisis del
  tramo y botón "Analizar ahora") y "Todo el día" (un bloque compacto por programa);
  `POST /claude/analizar`; helper `usd`.
- Flags `-claude-usd` (0 = apagado) y `-claude-modelo` (`claude-sonnet-5-5`); ADR 0006.

**Reproducción:** no aplica (mejora). Línea de base en esta rama (Go 1.26.8, `bd64942`):

```bash
go build ./... && go vet ./... && gofmt -l . && go test -count=1 ./...
```

```
ok  	github.com/alphamedia/yt-monitor	14.682s
ok  	github.com/alphamedia/yt-monitor/tools/factory-board	3.323s
ok  	github.com/alphamedia/yt-monitor/tools/preview	19.643s
```

(`go build`, `go vet` sin salida y `gofmt -l .` vacío.) Hay que volver a tomarla al
empezar cada etapa, ya con la 009 (y la etapa anterior) en `main`.

**Causa raíz:** no es un bug. El dashboard es una sola página armada por acumulación
(`templates/page.html:177-183`) y la configuración es solo por flags.

## Objetivo

Que el dashboard tenga tres pantallas con la misma estética (la del diseño, en claro y
oscuro): **En vivo** (un canal a la vez, todo su análisis), **Programas de hoy** (cada
programa con sus números y su resumen) y **Canales** (qué se sigue, cuánto cuesta y, desde
la etapa 2, editarlo sin reiniciar); y, en la etapa 3, que Claude etiquete el chat en
tandas para dar sentimiento, alertas, categorías, preguntas agrupadas y procedencia en
tiempo real, con un tope de gasto que no se pasa.

## Decisiones ya tomadas por la persona (Julian Barbieri, 2026-11-05)

1. **Orden:** se implementa después de que la 009 entre a `main`. La spec se escribe ahora.
2. **Una spec, tres etapas, tres PRs** (ramas de arriba). Etapa 1: rediseño y navegación
   con lo que ya existe (reglas + 009); "Canales" en solo lectura, sin formularios ni
   `POST` nuevos (la estimación de cuota se puede adelantar a esta etapa). Etapa 2: la
   etapa 2 de canales-continuos con el formulario y los botones del diseño. Etapa 3:
   Claude en tiempo real.
3. **Extras de Claude en la etapa 3:** sentimiento por tandas (bloque con cambio cada
   5 min, "Por qué hay mensajes negativos", "Qué está gustando"), alertas (quejas técnicas,
   insultos), categorías de Claude por mensaje (con caída a `Classify` sin clave o con el
   tope, y aviso), preguntas agrupadas con "Marcar respondida", "Desde dónde escriben"
   (solo agregado), etiqueta "Claude" en el chat en vivo, check "Analizar el chat con
   Claude" por canal, y en Canales la tarjeta "Análisis con Claude" con modelo, tandas y
   tope editables (persistidos; los flags de la 009 son valores iniciales) más mensajes
   analizados hoy, gasto de hoy y última tanda. También el resumen por momento en
   "Momentos con más participación", las columnas de Claude en la tabla de Programas y el
   detalle del programa (lo que ya dé la 009, en la etapa 1; lo demás en la 3).
4. **Se conservan en En vivo** con la estética nueva: gráfico de participación + panel de
   oyentes, Filtros para YouTube Studio, Palabras más repetidas, Quiénes más escriben y el
   detalle de Super Chats. Lo demás que existe se mapea a su equivalente (ver "Mapa de lo
   que existe").
5. **"Programas de hoy" es una pantalla aparte** (cambia la decisión 6 de
   canales-continuos): filtro de día Hoy / Ayer / Últimos 7 días y de canal, tabla
   (programa, canal, horario, mensajes, personas, Super Chats, preguntas; sentimiento y "sin
   responder" de Claude cuando existan) y detalle con "CSV de este programa" y "Abrir en
   YouTube". Los programas salen de `ProgramSegments` / `datos.vivos.jsonl`. Cuidar el
   costo de leer 7 días de mensajes.
6. **"Marcar respondida":** solo en memoria (se pierde al reiniciar), solo desde la PC del
   monitor (loopback + `sameOrigin`). No se publica nada en YouTube.
7. **CSV** (informe y `GET /export.csv`): columnas nuevas al final, `sentimiento` y
   `categoria_claude` (vacías sin dato); `categoria` sigue siendo la de reglas. Etiquetas
   de Claude en un archivo aparte con retención de 30 días (tmp + `os.Rename` al abrir),
   nunca en `datos.jsonl`.
8. **Tiempo real por defecto:** `claude-sonnet-5-5`, tandas cada 1 min, editable desde
   Canales; sin tanda si no hubo mensajes nuevos. Misma minimización que la 009.
9. **Umbrales de alertas fijos en el código:** quejas técnicas 15 en 5 min; insultos 5 %
   de los mensajes en 10 min.

## Mapa de lo que existe (después de la 009) y adónde va

| Hoy | Adónde va | Etapa |
|---|---|---|
| Encabezado "Monitor de YouTube" + "Descargar CSV para Claude" | Igual, en las tres pantallas, con las pestañas a la derecha | 1 |
| Resumen: nombre del canal, "Actualizado HH:MM", error de la API, nota global del chat | `/parcial/resumen` queda solo con eso (franja de estado arriba de cada pantalla) | 1 |
| Resumen: lista de todos los vivos en curso | **Se quita.** La reemplazan los botones de canal ("· en vivo") y la línea "EN VIVO" de la tarjeta Canal en En vivo, y el estado de cada canal en Canales | 1 |
| Resumen: tiles globales (mensajes hoy, última hora, chat 5 min, preguntas hoy, spam e insultos hoy) | **Se quitan.** Los reemplazan los 5 tiles por alcance del diseño (ver CA1.6) | 1 |
| Resumen: tile "Cuota usada hoy" | Canales, tarjeta de cuota ("Usadas hoy: N de 9.000") | 1 |
| Resumen: "Categorías de hoy" (reglas, todos los mensajes) | Tarjeta "Categorías" de En vivo, del alcance elegido (por reglas; etapa 3: Claude con caída a reglas). La versión global queda en el bloque "Todo el monitor · últimas 24 h" | 1 / 3 |
| Botones por canal y "Mostrando X · En pausa: Y" | Tarjeta "Canal" de En vivo (botones `vbtn`). "En pausa" se cambia por "Los demás canales se siguen leyendo y guardando" para no confundir con el "Pausar" de la etapa 2 | 1 |
| Cartel "Esperando el próximo vivo" | Tarjeta Canal | 1 |
| Cartel del chat (`chatBanner`) y nota del chat | Tarjeta "Chat en vivo" (resumido en el encabezado: "llega en tiempo real · lectura mínima cada X"; el detalle abajo) | 1 |
| Tira "Hoy en el canal" + cartel de continuidad | Tarjeta Canal, con bloques del diseño (ancho proporcional) | 1 |
| Selector "Programa en curso / Todo el día del canal" | Píldoras `seg` del diseño, debajo de la tarjeta Canal, con la línea de estado de Claude | 1 |
| 1. Temas (reglas) | Detalle del programa en Programas ("Temas de conversación": de la 009 si hay análisis, si no por reglas). En En vivo queda como bloque "Temas (por reglas)" dentro de la tarjeta de Filtros (`#filtros`) | 1 |
| 2. Preguntas para responder (reglas) | Tarjeta "Preguntas para producción" (reglas; etapa 3: grupos de Claude con "Marcar respondida") | 1 / 3 |
| 3. Quejas técnicas (reglas) | Tarjeta "Quejas técnicas" en el lugar de "Alertas" (etapa 3: la reemplaza "Alertas", que incluye los mensajes) | 1 / 3 |
| 4. Filtros para YouTube Studio | En vivo, después de Super Chats | 1 |
| 5. Momentos + gráfico de participación + panel de oyentes + 6 h / 24 h | Tarjeta "Momentos con más participación": gráfico y panel arriba, tabla del diseño abajo (etapa 3: resumen de Claude por momento) | 1 / 3 |
| 6. Super Chats (detalle) | En vivo, después del Chat en vivo; el tile "Super Chats" lo resume | 1 |
| Palabras más repetidas, Quiénes más escriben (`/parcial/extra`) | Bloque "Todo el monitor · últimas 24 h" al final de En vivo, con la versión global de Categorías (incluye comentarios). `/parcial/extra` desaparece (404) | 1 |
| 7. Análisis con Claude (009), modo "Programa en curso" | En vivo: tarjeta "Resumen de Claude del programa en curso" (mismo contenido y botón "Analizar ahora"). Etapa 3: queda compacta (resumen y sentimiento) porque preguntas y quejas pasan a las tarjetas de tiempo real | 1 / 3 |
| 7. Análisis con Claude (009), modo "Todo el día" | Programas de hoy (columna y detalle). En En vivo con "Todo el día" queda un link "Los resúmenes de cada programa están en Programas de hoy" | 1 |
| Estado de Claude de la 009 (clave, modelo, tope, gasto) | Canales, tarjeta "Análisis con Claude" (solo lectura en la 1, editable en la 3) | 1 / 3 |
| Números "1." a "7." en los títulos | **Se quitan** (el diseño no los tiene) | 1 |

No se quita ningún dato salvo la lista de vivos y los tiles globales del resumen (pregunta
7). Comentarios de videos del canal principal: hoy solo aparecen en "Categorías de hoy",
Palabras y Quiénes (globales); siguen ahí, en el bloque "Todo el monitor".

## Criterios de aceptación

Tests nuevos en `monitor_test.go` (salvo donde se indica), escritos **antes** del código,
con datos armados en el test, sin red y sin `datos.jsonl`. En cada etapa el primero de la
lista falla hoy **en ejecución** (no por compilación): guardar esa salida para el PR.

### Etapa 1 — rediseño y tres pantallas con lo que ya existe

- [ ] CA1.1 (falla hoy): `GET /`, `GET /programas` y `GET /canales` responden 200 con el
  mismo encabezado (`<h1>Monitor de YouTube</h1>`, link "Descargar CSV para Claude" a
  `/export.csv`) y `<nav class="tabs" aria-label="Secciones">` con tres links: "En vivo"
  (`/`), "Programas de hoy" (`/programas`) y "Canales" (`/canales`); el de la pantalla
  actual lleva `aria-current="page"` y los otros no — se verifica con `TestDashboardTabs`
  (tabla de las tres rutas; hoy `/programas` y `/canales` dan 404 y `/` no tiene `nav`).
- [ ] CA1.2: `GET /?canal=UC…` y `GET /?vivo=ID` siguen eligiendo canal o vivo suelto como
  hoy (mismos casos que `TestLiveChatByChannel`, que se actualiza a la tarjeta nueva); el
  `HX-Retarget: #vivo` de `/parcial/vivo/chat` cuando cambia el vivo en curso del canal no
  cambia — se verifica con `TestLiveChatByChannel` y `TestLiveChatSelection` ajustados.
- [ ] CA1.3: tarjeta Canal (`GET /parcial/vivo/canal?canal=…&video=…`, cada 5 s): botones
  por canal y por vivo suelto con `aria-pressed`; con vivo: "EN VIVO", programa, "desde
  HH:MM" (inicio del tramo al aire), "N.º vivo de hoy" si el canal tuvo más de uno,
  "N mirando" (suma de `Concurrent` de sus vivos) y "Abrir en YouTube"
  (`https://www.youtube.com/watch?v=ID`); sin vivo: "Esperando el próximo vivo de X
  (buscando cada…)" con `searchText`; cartel de continuidad (`ContinuityBanner`); tira con
  un bloque por tramo con `aria-label`, atributo `title`, clase según estado
  (`fin`/`aire`/`sin`) y `flex-grow` proporcional a los minutos; línea "Cómo sale" (CA1.4).
  No hace `HX-Retarget` (eso queda en el chat) — se verifica con `TestLiveChannelCard`
  (tabla) y `TestChannelDayStrip` movido a este parcial (cambia `<title>` por `title="…"`).
- [ ] CA1.4: `howItAirs(segs)` (pura) deduce cómo sale un canal con los tramos de los
  últimos 7 días: un mismo vivo con dos o más programas → "Un vivo de 24 h: el programa se
  toma del título, que cambia con cada uno"; dos o más vivos distintos en un día → "Un vivo
  nuevo por programa: al cortarse uno, el monitor busca el siguiente"; sin eso → "Todavía
  no se vio un cambio de programa" (decisión 8 de canales-continuos) — se verifica con
  `TestHowItAirs`.
- [ ] CA1.5: debajo de la tarjeta Canal, las píldoras "Programa en curso" / "Todo el día
  del canal" (`class="seg"`, `aria-pressed`) y el título "Análisis de <programa> (programa
  en curso)" o "Análisis de todo el día del canal"; la elección se conserva en los
  refrescos como hoy. Con la 009 activa, una línea con la etiqueta "Claude" y "resumen al
  cerrar cada programa · último análisis HH:MM" (o "todavía sin análisis hoy") — se
  verifica con `TestLiveAnalysisByChannel` extendido.
- [ ] CA1.6: tiles por alcance (`liveTiles`, pura, sobre los mensajes ya filtrados por el
  selector): **Mensajes por minuto** = mensajes de chat de los últimos 5 min / 5, con
  "X % sobre (o bajo) lo típico de <este programa | el día>" solo si hay al menos 6 franjas
  de 5 min con mensajes (típico = mediana de esas franjas), si no "en los últimos 5 min";
  **Personas que escribieron** = autores distintos del alcance, nota "en este programa" /
  "en el día"; **Mirando ahora** = suma de `Concurrent`, nota "X % escribió en los últimos
  5 min" (personas de la última franja / mirando) o "sin dato de espectadores";
  **Super Chats** = cantidad, nota con el total si hay una sola moneda ("ARS 52.300,00",
  con `money`) o "en N monedas" (nunca se suman monedas); **Preguntas** = `Classify`
  pregunta, nota "por reglas" — se verifica con `TestLiveTiles` (tabla: sin mensajes, sin
  espectadores, dos monedas, menos de 6 franjas).
- [ ] CA1.7: orden de En vivo (ids en el HTML): `#estado`, `#canal`, selector y título,
  tiles, `#quejas` (Quejas técnicas por reglas), `#claude-card` (la 009, si hay canal),
  `#preguntas` y `#categorias` (en grilla de dos), `#momentos` (gráfico de participación,
  panel de oyentes, 6 h / 24 h y la tabla), `#chat`, `#supers`, `#filtros` (con "Temas por
  reglas"), `#monitor` ("Todo el monitor · últimas 24 h": Categorías globales con
  comentarios, Palabras más repetidas, Quiénes más escriben). Ningún título lleva número
  — se verifica con `TestLiveScreenOrder` (índices de los ids en `GET /` y en
  `/parcial/vivo/analisis?canal=…`).
- [ ] CA1.8: bloques de la 009: con "Programa en curso" la tarjeta se titula "Resumen de
  Claude del programa en curso" con el mismo contenido, estados y botón "Analizar ahora"
  (mismas guardas); con "Todo el día del canal" la tarjeta muestra solo "Los resúmenes de
  cada programa están en Programas de hoy" con link a `/programas?canal=UC…`. Apagada:
  el texto de apagado de la 009 — se verifica con los tests de la 009 (`TestClaudeCardOff`,
  `TestClaudeCard`, `TestClaudeAnalyzePost`) ajustados al título nuevo.
- [ ] CA1.9: `programRows` (pura, una sola pasada sobre los mensajes) arma las filas de
  Programas: por cada tramo que no es "sin vivo", programa, canal, horario ("14:00–" al
  aire, "10:00–14:00" terminado), mensajes, personas, Super Chats (cantidad), preguntas
  (reglas), "AL AIRE", y el análisis final de la 009 si existe con la misma `segmentKey`.
  Los mensajes de un tramo son los de `segmentMessages` (el mismo filtro que "Programa en
  curso" y que la 009). Día D = tramos de `ProgramSegments(…, dayStart(D) − 12 h,
  min(ahora, dayStart(D+1)))` que terminan en D o después (para hoy, exactamente lo que
  segmenta la 009); "Últimos 7 días" = hoy y los 6 anteriores, sin repetir claves (un
  programa que cruza la medianoche aparece una vez). Filtro de canal: todos o uno; nunca
  entran mensajes de otro canal. Orden: canales en el orden de `-canal`, del más nuevo al
  más viejo — se verifica con `TestProgramRows` (tabla: vivo de 24 h con dos programas, un
  vivo por programa, cruce de medianoche, 7 días, filtro de canal, mensajes de otro canal).
- [ ] CA1.10: pantalla Programas (`GET /programas?dia=hoy|ayer|7d&canal=…&tramo=…` y
  `GET /parcial/programas?…`): título "Programas de hoy · <día> DD/MM" (día de la semana en
  español), selects "Canal" y "Día" con `<label>` que piden el parcial con `hx-get`
  (`hx-trigger="change"`, `hx-push-url`); tabla con `th scope="col"`, fila elegida con
  clase `sel`, cada programa con link `hx-get` al detalle y `hx-push-url`; "Hoy" se
  refresca cada 60 s y "Ayer" / "7 días" no se refrescan solos (dicen "Actualizado a las
  HH:MM"); sin tramos: "Todavía no hay programas guardados para ese día" y, si los datos
  empiezan después, "Hay programas guardados desde el DD/MM"; día o canal inválidos → se
  usa el valor por defecto (hoy, todos) — se verifica con `TestProgramsPage`.
- [ ] CA1.11: detalle (`GET /parcial/programas/detalle?tramo=<clave>`): título del
  programa; línea "Canal · HH:MM a HH:MM · <se cerró cuando cambió el título | terminó el
  vivo | al aire> · N mensajes de N personas"; botones `btn` "CSV de este programa"
  (`/export.csv?tramo=…`) y "Abrir en YouTube" (`momentLink` al inicio del tramo); bloques
  "Resumen del chat", "Temas de conversación", "Sentimiento del programa" y "Preguntas"
  con el análisis final de la 009 si existe (etiqueta "Claude"); si no, Temas
  (`TopTopics`) y Preguntas (`Analyze`) por reglas con la etiqueta "por reglas" y en el
  resumen "Todavía no hay resumen: se hace solo cuando termina el programa" (o el texto de
  apagado); Super Chats del programa por moneda. Sin tramo pedido: el último que terminó,
  o el al aire. Clave desconocida: "Ese programa ya no está guardado". Todo texto de
  usuarios y del modelo sale escapado (un `<script>` aparece como `&lt;script&gt;`) — se
  verifica con `TestProgramDetail` (tabla).
- [ ] CA1.12: `GET /export.csv?tramo=<clave>` exporta solo los mensajes del tramo (mismo
  filtro), con `csvHeader`, y nombre `mensajes_<programa>_<AAAA-MM-DD_HHMM>.csv` donde
  `<programa>` solo tiene `[a-z0-9-]` (sin tildes ni espacios, para que no rompa el
  header); clave desconocida → 404; sin `tramo` → igual que hoy — se verifica con
  `TestExportProgram`.
- [ ] CA1.13: Programas no copia todo el histórico: los handlers usan `Store.Lives` y
  `Store.MessagesOf` (de la 009), no `Snapshot`; `programRows` asigna cada mensaje a su
  tramo con búsqueda binaria por vivo (O(M log S)). Se verifica con la revisión del
  reviewer y `go test -run '^$' -bench '^BenchmarkProgramRows$' .` (70.000 mensajes
  sintéticos, 2 canales, 7 días) con el resultado en el PR; referencia: menos de 100 ms por
  pedido en la PC de desarrollo (informativo, no es gate).
- [ ] CA1.14: Canales en solo lectura (`GET /canales`, estado con
  `GET /parcial/canales/estado` cada 5 s): una tarjeta por canal (nombre, lo que se pasó en
  `-canal`, "canal principal" o "canal extra", "EN VIVO" o "Esperando el próximo vivo",
  "Al aire: <programa> desde HH:MM · N.º vivo de hoy" o "Sin vivo desde HH:MM · buscando
  cada…", "Cómo sale", horas en vivo por día de los últimos 7 días o "sin historial",
  intervalo de chat vigente y global "cada X en el pico (HH a HH h) y cada Y fuera, por
  turnos entre todos los vivos"); "Qué pasa cuando termina un programa" con los valores
  reales (`fastEvery`, `fastFor`, revisión cada 3 min, "a lo sumo 40 unidades cada vez que
  un canal se queda sin vivo"); tarjeta "Cuota de YouTube con esta configuración" (CA1.15);
  tarjeta "Análisis con Claude" (CA1.16); y la nota "Por ahora los canales y los intervalos
  se cambian con -canal, -chat-seg-pico y -chat-seg (ver README)". **Ningún** `<form>`,
  `<input>`, `<select>`, `hx-post` ni botón "Guardar" — se verifica con
  `TestChannelsPageReadOnly`.
- [ ] CA1.15: `EstimateQuota` y `LiveHours` (puras, en `quota.go`; adelantan CA2.7 y
  CA2.8 de canales-continuos). `LiveHours(samples, ids, peakFrom, peakTo, now)` = horas por
  día con al menos una muestra de esos canales (franjas de 5 min), últimos 7 días,
  separadas en pico y fuera; con menos de 24 h de historial devuelve 24 h y "sin
  historial". `EstimateQuota` recibe grupos de chat (`ChatGroup{PeakEvery, OffEvery,
  PeakHours, OffHours, Paused}`): en esta etapa **un solo grupo** con todos los canales
  (turnos) y las horas de la unión; en la 2, uno por canal. Videos = 480 × (3 + canales
  extra); comentarios 1.000; búsqueda rápida = 40 × cortes por día observados (0 sin
  historial: con 24 h de chat supuestas ya es el peor caso, porque mientras busca no hay
  chat que leer). Casos: 1 canal, defaults, sin historial → 1.440 + 1.000 + 5.000 = 7.440,
  "Entra en el día. Quedan 60 unidades antes de que se pause el chat"; 2 canales sin
  historial → 1.920 + 1.000 + 5.000 = 7.920, "No entra: faltan 420 unidades"; 2 canales con
  6 cortes por día en el extra → 8.160 (las cifras del README); y el caso por canal de
  CA2.7 (2.610, 1.600, 7.130, quedan 370). La barra (escala 10.000) tiene marcas en
  `Budget − reserveForComments` y en `Budget`, `role="img"` con `aria-label`, y al lado
  "Usadas hoy: N" (contador real) — se verifica con `TestEstimateQuota`, `TestLiveHours` y
  un caso de `TestChannelsPageReadOnly`.
- [ ] CA1.16: tarjeta "Análisis con Claude" en solo lectura: chip "Clave cargada" o "Falta
  CLAUDE_API_KEY" o "Apagado (-claude-usd 0)" (nunca la clave ni parte de ella), modelo,
  tope, "Gastado hoy US$ x,xx", último análisis y último aviso de la 009, y `<details>`
  "Qué se le manda a Claude y qué se guarda" con lo que es verdad en esta etapa (un pedido
  por programa, sin nombres, `datos.analisis.jsonl` con 30 días). `main` le pasa al
  `Server` el aviso de `claudeConfig` (un campo de texto), no la clave — se verifica con
  `TestChannelsClaudeReadOnly` (tabla con un `getenv` falso con clave testigo que no
  aparece en el HTML).
- [ ] CA1.17: estilos: todo `var(--x)` que usan las plantillas está definido en `:root` y
  en el bloque oscuro; los `style="…"` de las plantillas solo llevan valores calculados
  (`width`, `left`, `flex-grow`) y no colores — se verifica con `TestTemplateTokens`
  (lee las plantillas embebidas con regex) — y los colores nuevos de "Diseño" están en los
  dos temas.
- [ ] CA1.18: `/parcial/resumen` queda solo con estado (actualizado, error, nota global
  del chat) y sin tiles; `/parcial/extra` responde 404 — se verifica con
  `TestDashboardWithoutRemovedSections` y `TestSummaryWithoutActivityChart` actualizados.
- [ ] CA1.19: la cuota de YouTube no cambia: `git diff main -- youtube.go` vacío; en
  `worker.go` solo se agrega `Input` a `ChannelStatus` (lo que se pasó en `-canal`), sin
  pedidos nuevos; `TestFlowAgainstFakeAPI` con el mismo `yt.Units`.
- [ ] CA1.20: README (sección 3 reescrita por pantalla, rutas), AGENTS.md (mapa, rutas,
  estilo del dashboard) y CLAUDE.md con los textos de "Reglas que cambian"; ADR 0007 — se
  verifica con `git diff --stat main` y `grep -n "/programas" README.md AGENTS.md`.
- [ ] CA1.21: revisado en el navegador con la vista previa (paso 7b) en claro, oscuro y
  ancho de celular, las tres pantallas, con los estados "en vivo", "esperando" y "sin
  análisis de Claude". Verificación manual, anotada en el PR (ver pregunta 13).
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde (`gofmt -l .` vacío,
  `go vet ./...`, `staticcheck`, build Linux y Windows, `go test -race -count=1 ./...`,
  `go test -count=5 ./...`, reviewer sin bloqueantes, PR con CI verde).

### Etapa 2 — canales editables (absorbe la etapa 2 de canales-continuos)

Se adoptan **tal cual** CA2.1 a CA2.9 y CA2.11 de
`docs/specs/2026-11-05-canales-continuos.md` (intervalo por canal en `nextChat`,
`TestWorkerChatPerChannel`, `LoadSettings` con `-chat-seg* × N`, `Settings.Update`,
cambios sin reiniciar, handlers que no llaman a YouTube, `EstimateQuota` y `LiveHours` —
ya hechas en la etapa 1, acá se usa el modo por canal —, guardas de los `POST`, `-h`), con
las decisiones de su aprobación (pausar solo el chat, editar solo desde la PC del monitor,
gana el archivo con aviso). CA2.10, CA2.12 y CA2.13 se reemplazan por estos:

- [ ] CA2.1 (falla hoy): igual que en canales-continuos — `TestNextChatByChannel`.
- [ ] CA2.10 (reemplaza al de canales-continuos): `GET /canales` editable con el diseño:
  formulario "Agregar canal" (`<label>`, `input` con placeholder "Link, @handle o ID del
  canal", botón `btn-p`) con `hx-post="/canales/agregar"`; por canal, selects "Actualizar
  el chat cada" (pico: 30 s, 1, 1:30, 2, 3, 5 min) y "Fuera del pico, cada" (1, 2, 3, 5,
  10 min) con el valor actual elegido (y agregado a la lista si no está), "Cuota estimada
  de este chat: N unidades por día (X h en vivo | sin historial)", botones "Pausar" /
  "Reanudar" (`POST /canales/pausar`, inmediato) y "Quitar" (`POST /canales/quitar` con
  `hx-confirm` "¿Quitar <canal>? Lo guardado queda hasta que venza (30 días)."); abajo
  "Los cambios se aplican sin reiniciar el monitor", "Descartar" (`GET
  /parcial/canales/form`) y "Guardar cambios" (`POST /canales`, intervalos de todos). Un
  cambio en un select pide `GET /parcial/canales/cuota` con `hx-include` del formulario y
  recalcula la barra (un grupo por canal) sin escribir nada. El formulario **no** se
  refresca solo (no tiene `hx-trigger` con `every`): solo los bloques de estado de cada
  tarjeta (`/parcial/canales/estado`), así no se pisa lo que la persona está editando.
  Desde un `GET` que no es de loopback, en lugar de los controles: "Para cambiar la
  configuración, abrí el dashboard en la PC del monitor" — se verifica con
  `TestChannelsPage` (tabla: loopback / no loopback, valor fuera de lista, canal en pausa,
  formulario sin `every`).
- [ ] CA2.14: las guardas son las de `guard.go` (`sameOrigin` + `localRequest`, de la 009):
  no se copia otra versión. `TestChannelsPostGuards` cubre `POST /canales`,
  `/canales/agregar`, `/canales/pausar` y `/canales/quitar` (403 sin cambios en
  `datos.canales.json` en cada caso de la tabla de CA2.9; `http.MaxBytesReader` de 16 KB).
- [ ] CA2.15: En vivo y Programas toman los canales nuevos o quitados sin reiniciar (los
  botones salen de `Status().Channels`); un canal quitado deja de tener botón y sus
  programas siguen en Programas hasta que vence la retención — se verifica con un caso de
  `TestWorkerAppliesSettings` más `GET /parcial/vivo/canal` y `/parcial/programas`.
- [ ] CA2.12 (reemplaza): README (pantalla Canales editable, intervalos por canal, cifras de
  cuota por canal, `datos.canales.json`), AGENTS.md y CLAUDE.md con los textos de "Reglas
  que cambian" (los de canales-continuos más los de esta spec) y ADR 0005 — se verifica con
  `git diff --stat main`.
- [ ] CA2.13 (reemplaza): navegador con la vista previa en claro, oscuro y celular:
  agregar (queda "resolviendo…"), cambiar un intervalo (la barra cambia antes de guardar),
  descartar, guardar, pausar y quitar. Verificación manual, anotada en el PR.
- [ ] Gates de `CLAUDE.md` en verde.

### Etapa 3 — Claude en tiempo real (incluye la etapa C de la 009)

- [ ] CA3.1 (falla hoy): `csvHeader` termina en `…, "sticker", "sentimiento",
  "categoria_claude"`; un mensaje sin etiqueta tiene las dos vacías, uno etiquetado tiene
  `positivo|neutro|negativo` y la clave de categoría (`pregunta`, `queja`, …); `categoria`
  sigue siendo `Classify`; el informe (`go run .`) escribe las dos columnas vacías — se
  verifica con `TestCSVClaudeColumns` (hoy falla al comparar el header) y `TestCSVSuper`
  actualizado.
- [ ] CA3.2: `batchPrompt` (pura): una línea por mensaje `[n] HH:MM:SS texto`, **sin**
  `Author`, `ID`, likes ni montos; sin los que `Classify` marca spam; texto cortado en 300
  runas; a lo sumo `claudeBatchMax` (300) mensajes por tanda, los más viejos primero (el
  resto queda para la siguiente); lleva canal y programa, los grupos de preguntas abiertos
  del programa (ID de Go y texto, a lo sumo 20) y los motivos usados en los últimos 10 min
  (a lo sumo 10 por tipo) para que Claude reuse los mismos textos; delimitadores y
  `system` que dice que el material no son instrucciones — se verifica con
  `TestBatchPrompt` (autor testigo y `https://` que no aparecen).
- [ ] CA3.3: `validBatchResult` (pura): números fuera de rango o repetidos se descartan;
  sentimiento o categoría que no son de la lista → ese mensaje queda **sin etiqueta** (cae a
  reglas); `grupo` que no es un ID abierto ni `nuevo` → sin grupo; grupos nuevos sin texto
  se descartan y los IDs los pone Go (`<canal>|g<N>`); `lugar` cortado en 40 runas y
  normalizado (espacios, mayúscula inicial); motivos cortados en 60 runas, 5 por tipo;
  todos los conteos se hacen en Go — se verifica con `TestBatchResultValidate`.
- [ ] CA3.4: tandas en `RunAnalyst` (la misma goroutine de la 009): cada `BatchEvery` (por
  defecto 1 min), por cada canal con "Analizar el chat con Claude", una tanda con los
  mensajes de chat nuevos desde la última etiquetada; **sin mensajes nuevos no hay pedido**
  (el falso recibe 0); prioridad: botón de la 009 > tanda vencida > programa terminado; un
  mensaje no se manda dos veces; al arrancar se etiqueta solo desde 10 min antes
  (`batchCatchUp`), no lo viejo; `RunAnalyst` vuelve al cancelar el contexto, también con
  un pedido en curso — se verifica con `TestAnalystBatches` (tick achicado, falso que cuenta
  pedidos y mensajes), también con `-race`.
- [ ] CA3.5: tope y reparto: un solo tope diario (día local, contador de la 009). Las tandas
  y los resúmenes de momentos usan como mucho `tope × (1 − claudeProgramShare)` con
  `claudeProgramShare = 0,25`; los análisis de programa de la 009 y el botón pueden usar el
  tope entero. Antes de cada tanda se chequea el peor caso (`claudeWorstCase` con
  `claudeBatchMaxTokens` = 12.000 de salida); si no entra, no se manda y la pantalla dice
  "Se llegó a la parte del tope para tiempo real (US$ X): las categorías vuelven a las
  reglas hasta mañana" — se verifica con `TestAnalystBatchBudget` (tope chico: 0 tandas y
  los cierres de programa siguen).
- [ ] CA3.6: `datos.etiquetas.jsonl`: un `ClaudeBatch` por línea (`At`, `ChannelID`,
  `From`, `To`, `Model`, tokens, `CostMicros`, `StopReason`, `Labels []MessageLabel{ID,
  VideoID, At, Sentiment, Cat, Tech, Group, Place}`, `NewGroups`, `Motives`, `Error`); al
  abrir descarta lo que tiene `To` de hace más de 30 días, líneas ilegibles y de más de
  256 KB, y reescribe con tmp + `os.Rename`; sin archivo arranca vacío; `datos.jsonl` queda
  byte a byte igual después de una tanda; `Store.Labels(ids)` y `Store.Batches(canal,
  since)` devuelven copias — se verifica con `TestStoreBatchesOpen` (tabla, como
  `TestStoreLivesOpen`) y un chequeo en `TestAnalystBatches`.
- [ ] CA3.7: sentimiento (`sentimentSlots`, pura): franjas de 5 min del alcance con
  positivos / neutros / negativos etiquetados (sin spam); franjas con menos de
  `minSentMsgs` (20) quedan sin color; los tres tiles son la franja de los últimos 5 min con
  "±N pts sobre lo típico" (típico = mediana de las franjas con 20 o más); cartel "El
  negativo sube: X % en los últimos 5 min, contra Y % típico" si X ≥ Y + 5 y la franja
  tiene 20 o más, con la causa = motivo negativo más usado; gráfico apilado en SVG con
  `role="img"` y `aria-label`, línea punteada en cada cambio de programa del alcance y
  leyenda con "Menos de 20 mensajes: sin color" — se verifica con `TestSentimentSlots` y
  `TestSentimentCard`.
- [ ] CA3.8: "Por qué hay mensajes negativos" / "Qué está gustando": motivos de las tandas
  de los últimos 5 min, juntados por texto normalizado (minúsculas, sin tildes), % sobre los
  negativos (o positivos) etiquetados de esos 5 min, los 3 primeros más "Otros", un ejemplo
  por motivo (texto sin autor) — se verifica con `TestMotives`.
- [ ] CA3.9: alertas (`Alerts`, pura, umbrales fijos): **quejas técnicas** ≥ 15 en los
  últimos 5 min (`Tech` de Claude o, sin etiqueta, `TechIssue`); **insultos** ≥ 5 % de los
  mensajes de los últimos 10 min, con al menos 20 mensajes (categoría de Claude o, sin
  etiqueta, `Classify`). Cada alerta con "Umbral …" y "Ver los mensajes" (`<details>` con
  hasta 10). Sin alertas: "Sin alertas: N quejas técnicas en 5 min (umbral 15) · X % de
  insultos en 10 min (umbral 5 %)". La tarjeta reemplaza a "Quejas técnicas" de la etapa 1
  — se verifica con `TestAlerts` (bordes 14/15, 4,9 %/5 %, 19/20 mensajes, sin etiquetas).
- [ ] CA3.10: categorías con Claude: por mensaje, la de la etiqueta si existe, si no
  `Classify`; nota "N de M mensajes con categoría de Claude; el resto, por reglas"; sin
  clave, con el tope de tiempo real o con el canal sin el check: "Categorías por reglas:
  <motivo>". En el chat en vivo cada mensaje etiquetado lleva su badge y el chip "Claude";
  `Tech` muestra "Queja técnica"; el badge de Super Chat sigue — se verifica con
  `TestCategoriesClaude` y `TestLiveChatClaudeBadges`.
- [ ] CA3.11: "Preguntas agrupadas para producción": grupos del alcance ordenados por
  cantidad, "N · <pregunta>", y "Marcar respondida" (`POST /preguntas/respondida` con
  `grupo=…`): 403 sin `sameOrigin` + `localRequest`, cuerpo de 1 KB, grupo inexistente
  400; guarda en `Answers` (en memoria, con su mutex, fuera del `Store`); el handler no
  llama a Claude ni escribe el `Store`; el botón solo se dibuja desde loopback; un grupo
  respondido sale de "sin responder" y lo dice ("respondida · se olvida al reiniciar el
  monitor"). Tile "Preguntas" con la etiqueta "Claude": "N en G grupos · S sin responder" —
  se verifica con `TestQuestionGroups` y `TestAnswerPost` (tabla).
- [ ] CA3.12: "Desde dónde escriben": agregado de `Place` de las etiquetas del tramo, los 8
  primeros más "Otros"; nunca muestra autores ni mensajes sueltos — se verifica con
  `TestPlaces`.
- [ ] CA3.13: resumen por momento: para cada momento del top (de "Programa en curso" y de
  "Todo el día"), el analista pide una vez una oración sobre el chat de esa franja (misma
  minimización) y la guarda como `ClaudeAnalysis` con `Kind: "momento"` (campo nuevo con
  `omitempty`; las líneas de la 009 siguen leyéndose) y clave `momento|vivo|inicio`; a lo
  sumo `claudeMomentMax` (10) por canal y por día, dentro de la parte del tope de tiempo
  real; la tabla muestra "Claude · En el chat: …" debajo del momento — se verifica con
  `TestMomentSummaries`.
- [ ] CA3.14: Programas con Claude: columna "Sentimiento" (barra `sent` con `role="img"` y
  `aria-label` con los tres %; de las etiquetas del tramo, o de la 009 si no hay) y
  "Preguntas sin responder" (grupos del tramo no marcados); en el detalle, "Momento más
  negativo" y "Momento más positivo" (franjas con 20 o más), "Desde dónde escriben" y
  "Preguntas sin responder" — se verifica con `TestProgramRowsClaude` y
  `TestProgramDetailClaude`.
- [ ] CA3.15: Canales: check "Analizar el chat con Claude" por canal y tarjeta "Análisis con
  Claude" editable: modelo (los de `claudePrices`), "Analizar en tandas cada" (30 s, 1, 2,
  5 min), "Tope de gasto por día (US$)" (0 a `maxClaudeUSD`, 50); se guardan en
  `datos.canales.json` (campo `Claude` por canal y sección `Claude`) con `Settings.Update`;
  sin esa sección valen los flags de la 009; el analista los toma sin reiniciar (cambia el
  `Model` de su cliente en su propia goroutine). Con clave, `RunAnalyst` arranca siempre y
  espera con tope 0; sin clave los controles quedan deshabilitados con "Falta
  CLAUDE_API_KEY". Muestra "Mensajes analizados hoy N de M (K en la próxima tanda)",
  "Gasto estimado de hoy US$ x,xx" y "Última tanda: hace N s · <sin errores | error>" — se
  verifica con `TestSettingsClaude` y `TestChannelsClaudeCard`.
- [ ] CA3.16: línea de estado en En vivo: "Claude · analizado hasta las HH:MM:SS · N s de
  demora · tandas cada X" (del `To` y el `At` de la última tanda del canal) o el motivo por
  el que no analiza — se verifica con `TestLiveAnalysisClaude`.
- [ ] CA3.17: privacidad y clave: la tanda no lleva autores (testigo), y el camino de las
  tandas pasa por los mismos chequeos de clave de la 009 (`TestClaudeErrorsHideKey` con un
  caso de tanda); la API simulada de la vista previa responde también el esquema de las
  tandas (`TestFakeClaudeMessages` extendido en `tools/preview`).
- [ ] CA3.18: la cuota de YouTube no cambia: `git diff main -- youtube.go worker.go` vacío
  y `TestFlowAgainstFakeAPI` con el mismo `yt.Units`.
- [ ] CA3.19: README (sección de Claude con tiempo real, costos, tope y reparto,
  `datos.etiquetas.jsonl`, columnas del CSV), AGENTS.md, CLAUDE.md con los textos de
  "Reglas que cambian" y ADR 0008 — se verifica con `git diff --stat main` y
  `grep -n "datos.etiquetas.jsonl\|categoria_claude" README.md AGENTS.md`.
- [ ] CA3.20: navegador con la vista previa (API de Claude simulada) en claro, oscuro y
  celular: sentimiento, alertas, preguntas con "Marcar respondida", categorías, chat con
  etiquetas, Programas con columnas de Claude y la tarjeta editable de Canales. Manual,
  anotado en el PR.
- [ ] Gates de `CLAUDE.md` en verde.

## Diseño propuesto

### Etapa 1

**Rutas.**

| Ruta | Qué hace |
|---|---|
| `GET /{$}` | En vivo (`?canal=`, `?vivo=` como hoy) |
| `GET /programas` | Programas de hoy (`?dia=hoy\|ayer\|7d&canal=UC…&tramo=<clave>`) |
| `GET /canales` | Canales (solo lectura) |
| `GET /parcial/resumen` | Franja de estado (5 s): actualizado, error de la API, nota global del chat |
| `GET /parcial/vivo` | Sección del canal elegido (como hoy) |
| `GET /parcial/vivo/canal` | **Nueva**, tarjeta Canal (5 s) |
| `GET /parcial/vivo/chat` | Solo la tarjeta Chat en vivo (2 s); conserva el `HX-Retarget` |
| `GET /parcial/vivo/analisis` | Selector, tiles y tarjetas (30 s), con lo de `/parcial/extra` en el bloque "Todo el monitor" |
| `GET /parcial/programas`, `GET /parcial/programas/detalle` | **Nuevas** |
| `GET /parcial/canales/estado` | **Nueva**, estado de los canales y cuota (5 s) |
| `GET /export.csv` | Como hoy; con `?tramo=` exporta un programa |
| `POST /claude/analizar` | De la 009, sin cambios |
| `GET /parcial/extra` | **Se quita** |

Sin JS propio ni build: links comunes entre pantallas y htmx para los parciales. La tarjeta
Canal se refresca aparte del chat porque en el diseño están en las puntas de la pantalla;
el chat sigue siendo el que detecta el cambio de vivo (2 s) para no tocar ese
comportamiento ni sus tests.

**Archivos.**

- `templates/layout.html` (nuevo): `head` (tokens y CSS), encabezado con pestañas y franja
  de estado; `page.html` pasa a ser En vivo. `templates/programas.html` y
  `templates/canales.html` (nuevos). `//go:embed templates/*.html` ya los cubre.
- `programs.go` (nuevo): `programRows`, `programDetail`, `dayRange(dia, now)`, handlers de
  Programas, `csvName`. Usa `Store.Lives`, `Store.MessagesOf` y `Store.Analyses`.
- `quota.go` (nuevo): `LiveHours`, `EstimateQuota`, `QuotaEstimate` (puras).
- `server.go`: `liveTiles`, `howItAirs`, `liveChannelCard`, handlers de Canales; se saca
  `extra` y la parte de tiles de `summary`.
- `store.go` / `worker.go`: `ChannelStatus.Input` (la entrada de `-canal`, para mostrar el
  `@handle`); el worker lo completa en `channelStatuses` (`worker.go:165-171`).
- `main.go`: pasa al `Server` el aviso de `claudeConfig` (sin la clave) y la configuración
  de chat (`-chat-seg-pico`, `-chat-seg`, `-pico`) para mostrarla y estimar.

**Programas sin copiar todo.** Un pedido de "7 días": `Store.Lives(hoy − 7 d − 12 h)`
(~2 canales × 480 muestras por día), `ProgramSegments` por canal y día (CA1.9), y
`Store.MessagesOf(ids de los vivos de esos tramos, inicio − 5 min)`. `programRows` ordena
los tramos de cada vivo por inicio y asigna cada mensaje con `sort.Search` y el mismo
criterio de `segmentMessages` (inicio − 5 min; fin + 5 min si terminó; `programOf`). Con la
hipótesis de volumen de la 009 (~9.900 mensajes de chat por día) son ~70.000 mensajes: una
pasada. "Hoy" se refresca cada 60 s; "Ayer" y "7 días" no se refrescan solos.

**Estética: tokens.** Se mapea la paleta del diseño a los tokens de `page.html`
(`templates/page.html:9-52`) y lo nuevo se define en los dos temas:

| Token | Claro | Oscuro | Uso |
|---|---|---|---|
| `--text-muted` (cambia) | #6b6a65 | #8e8d86 (igual) | textos `.muted`; #7a7974 sobre #fcfcfb da ~4,3:1, debajo de AA |
| `--link`, `--link-hover` (nuevos) | #1f63b8, #174c8e | #6aa6f0, #8dbcf5 | `a` (hoy usa `--series-1`, ~4,3:1) |
| `--sent-pos` (nuevo) | `var(--series-1)` | `var(--series-1)` | positivo |
| `--sent-neu` (nuevo) | #b9b8b2 | #5f5e59 | neutro, "Comentarios" en la barra de cuota |
| `--sent-neg` (nuevo) | #c2570c | #e07a3a | negativo |
| `--series-2` … `--series-5` (nuevos) | #c2570c, #7b5cc4, #2e8b57, #b8860b | #e07a3a, #a58bec, #5cc08a, #e0b44a | chat de cada canal en la barra (hasta 5) |
| `--ia-bg`, `--ia-text` (nuevos) | #e7eefa, #1d4f91 | #1c2a40, #9cc0f5 | chip "Claude", fila elegida |
| `--field-border` (nuevo) | #cfcec8 | #3d3d3a | `select`, `input` |
| `--on-accent` (nuevo) | #ffffff | #ffffff | texto de `btn-p` |

Los valores oscuros nuevos son propuesta (hipótesis): se ajustan en la revisión del
navegador con contraste AA contra `--surface-1`. Clases nuevas o ajustadas con esos
tokens: `.tabs a` / `.tabs a[aria-current]` (negro con texto claro: `--text-primary` /
`--surface-1`, que se invierten solos en oscuro), `.inner`, `.lbl`, `.chip`, `.ia`,
`.btn`, `.btn-p`, `.btn-s`, `.seg`, `.sel`, `.inp`, `.leg`, `.sw`, `.sent`, `tr.sel`,
`.catrow` (columnas `minmax(0, 38%) 1fr 48px`), `.q`. Botones, pestañas y campos con
`min-height: 44px` (toque en celular). `.vbtn` pasa a 44 px. Tablas dentro de un contenedor
con `overflow-x: auto`; la tira, con scroll horizontal en celular. Sin estilos inline salvo
valores calculados (CA1.17).

**Accesibilidad.** `aria-current="page"` en la pestaña, `aria-pressed` en botones de canal
y selector, `<label>` en cada select, `role="img"` + `aria-label` (+ `<title>` en SVG) en
barras y gráficos, `aria-live="off"` en el chat como hoy.

**Test de regresión a escribir primero:** `TestDashboardTabs` (CA1.1). Con el `Server` de
hoy: `GET /programas` y `GET /canales` dan 404 y `GET /` no tiene `<nav class="tabs"`.
Falla en ejecución sin usar nada nuevo.

**Alternativas descartadas (etapa 1):**

- Pestañas con htmx dentro de una sola página (`hx-get` que cambia `#main`): pierde la URL
  por pantalla y el botón "atrás" sin JS extra; con rutas comunes cada pantalla se puede
  abrir y recargar sola.
- Recalcular Programas con `Snapshot()`: copia todo el histórico (hasta 30 días) en cada
  pedido; `MessagesOf` copia solo los vivos y días que hacen falta.
- Guardar los programas cerrados en un archivo nuevo: los tramos ya salen de
  `datos.vivos.jsonl` y los resúmenes de `datos.analisis.jsonl`; otro archivo duplicaría
  datos de la API con su propia retención.
- Mover la detección de cambio de vivo a la tarjeta Canal: cambia `TestLiveChatByChannel`
  y el contrato de AGENTS.md sin ganancia.
- Seguir con las tarjetas numeradas: el diseño no las numera y las etapas cambian el orden.

### Etapa 2

Todo el diseño de la etapa 2 de canales-continuos ("Etapa 2 — canales editables":
archivo `datos.canales.json`, `Settings` + `version`, intervalo por grupo en `nextChat`,
intervalos permitidos, pausar / quitar / agregar, guardas, estimación) vale tal cual, con
estos cambios:

- **Pantalla:** la de CA2.10 de arriba, sobre la pantalla Canales de la etapa 1 (las
  tarjetas de solo lectura pasan a tener controles). Pausar y Quitar son inmediatos
  (`POST /canales/pausar`, `POST /canales/quitar`); "Guardar cambios" aplica los
  intervalos. El formulario no se refresca solo; el estado de cada tarjeta sí.
- **Rutas:** `GET /canales` (ya existe), `GET /parcial/canales/estado` (ya existe),
  `GET /parcial/canales/form` (Descartar), `GET /parcial/canales/cuota`, `POST /canales`,
  `POST /canales/agregar`, `POST /canales/pausar`, `POST /canales/quitar`.
- **Guardas:** `sameOrigin` y `localRequest` de `guard.go` (009). Canales-continuos decía
  "copiar `sameOrigin` al paquete main": ya lo hizo la 009.
- **Cuota:** `EstimateQuota` ya existe (etapa 1); acá recibe un grupo por canal.
- **Archivo:** `channels.go` con `Settings` (como decía canales-continuos).
- **Navegación:** tres pestañas en vez de "En vivo / Canales".

**Test de regresión a escribir primero:** `TestNextChatByChannel` (CA2.1), como en
canales-continuos.

**Alternativa descartada:** que "Pausar" se acumule hasta "Guardar cambios": sin JS propio
haría falta un borrador del lado del servidor; inmediato es más simple y es lo que se lee
en el botón.

### Etapa 3

**Quién llama a Claude.** La misma goroutine `RunAnalyst` de la 009, con un paso más por
vuelta (CA3.4). El tick del analista baja de 30 s a `min(30 s, BatchEvery / 2)`. Un pedido
de cierre de programa (decenas de segundos) atrasa una tanda: la siguiente trae más
mensajes, nada se pierde.

**Qué se le pide en cada tanda.** Salida estructurada (`output_config.format`,
`effort: "low"`, como la 009; mismas prohibiciones de campos):

```json
{"mensajes":[{"n":1,"sentimiento":"positivo","categoria":"pregunta","tecnica":false,
              "grupo":"g3","lugar":"Rosario"}],
 "grupos_nuevos":[{"clave":"nuevo1","pregunta":"¿Vuelve el invitado la semana que viene?"}],
 "motivos":[{"tipo":"negativo","motivo":"Problemas de audio","mensajes":[2,5]}]}
```

`sentimiento` ∈ positivo / neutro / negativo; `categoria` ∈ las claves de `categories`
(`server.go:29`); `grupo` = un ID abierto, `nuevoK` o vacío; `lugar` vacío si el mensaje no
dice desde dónde escribe. Las listas cerradas se validan en Go (H5 de la 009).

**Datos.** `datos.etiquetas.jsonl` (ignorado por `datos.*.jsonl`) con un `ClaudeBatch` por
línea (CA3.6); en memoria, un índice `map[Message.ID]MessageLabel` y los grupos por canal.
`Answers` (nuevo, `answers.go`): `map[grupo]time.Time` con mutex, en el `Server`, como la
cola del `Analyst`; no persiste (decisión 6). Configuración de Claude en
`datos.canales.json` (`Settings`, etapa 2): solo lo que eligió la persona.

**Archivos.** `batch.go` (nuevo: `batchPrompt`, `validBatchResult`, `ClaudeBatch`,
`MessageLabel`, `sentimentSlots`, `Alerts`, `Motives`, `Places`), `answers.go` (nuevo),
`analyst.go` (paso de tandas y de momentos), `store.go` (etiquetas), `main.go` (`csvRow`
recibe la etiqueta), `server.go` / `programs.go` / plantillas (tarjetas).

**Costo de Claude (hipótesis de volumen de la 009, explícita).** Rivadavia ~300 mensajes de
chat por hora las 24 h (~5 por minuto), Rock & Pop ~150 por hora unas 18 h; total ~9.900
por día. Con tandas cada 1 min hay mensajes casi en cada minuto: ~1.440 + 1.080 = 2.520
tandas por día. Por tanda: ~1.200 tokens fijos de entrada (system, esquema, grupos y
motivos abiertos) + 30 por mensaje; salida ~280 fijos (thinking con esfuerzo bajo,
grupos, motivos: hipótesis, como H7 de la 009) + 30 por mensaje.

```
entrada = 2.520 × 1.200 + 9.900 × 30 ≈ 3,32 M tokens por día
salida  = 2.520 ×   280 + 9.900 × 30 ≈ 1,00 M tokens por día
```

| Tiempo real | Haiku 5.5 (0,10 / 0,50) | **Sonnet 5.5 (2 / 10)** | Opus 5.5 (4 / 20) |
|---|---|---|---|
| Tandas cada 1 min | ~US$ 0,83 por día (~25 por mes) | **~US$ 16,7 por día (~500 por mes)** | ~US$ 33 por día |
| Cada 2 min (1.260 tandas) | ~US$ 0,51 | ~US$ 10,1 | ~US$ 20 |
| Cada 5 min (504 tandas) | ~US$ 0,31 | ~US$ 6,2 | ~US$ 12 |

Más lo de la 009 (cierres de programa y botón: ~US$ 1,33 por día con Sonnet, ~0,07 con
Haiku) y los resúmenes de momentos (≤ 20 por día de ~2.000 tokens de entrada y ~300 de
salida: ~US$ 0,14 con Sonnet). **Con lo decidido (Sonnet, 1 min) el total es ~US$ 18 por
día (~US$ 545 por mes): el tope sugerido de la 009 (US$ 2) se acaba antes del mediodía y
desde ahí todo vuelve a reglas** (pregunta 4, bloqueante). Con Haiku en tiempo real y
Sonnet para los resúmenes: ~US$ 2,3 por día (~US$ 69 por mes). Sensibilidad: cada 1.000
mensajes por día más suman ~US$ 0,36 con Sonnet y ~US$ 0,02 con Haiku; si el chat tuviera
el volumen de los datos de ejemplo del diseño (~200 mensajes por minuto), Sonnet costaría
más de US$ 100 por día. Cada tanda guarda tokens y costo para medirlo la primera semana.
Peor caso de una tanda (300 mensajes de 300 runas, ~35.000 tokens de entrada con bytes/3, y
12.000 de salida): ~US$ 0,19 con Sonnet.

**Reparto del tope.** Un solo tope diario (lo que se gasta en el día es lo que importa). El
tiempo real (tandas y momentos) usa como mucho el 75 %; el 25 % (`claudeProgramShare`)
queda para los cierres de programa y el botón de la 009, que pueden usar el tope entero.
Mismo criterio que `reserveForComments` con la cuota de YouTube: lo continuo no se come lo
que se hace una vez por programa.

**Privacidad.** Igual que la 009: solo texto y hora, sin `Author` ni IDs, sin spam, 300
runas. Lo nuevo que se guarda: etiquetas por mensaje (sentimiento, categoría, grupo y
lugar escrito por la persona en un mensaje público), con 30 días de retención; "Desde
dónde escriben" se muestra solo agregado. Los textos que escribe el modelo (grupos,
motivos, lugares, resumen de momento) salen escapados y cortados.

**Concurrencia.** `RunAnalyst` sigue siendo el único que usa `Claude` y el que escribe
etiquetas, gasto y estado en el `Store` (bajo su lock). Los handlers leen con copias,
escriben solo `Answers` y `Settings`, y nunca llaman a Claude ni a YouTube.

**Test de regresión a escribir primero:** `TestCSVClaudeColumns` (CA3.1). Con lo que ya
existe compara `csvHeader` con el header nuevo y falla en ejecución.

**Alternativas descartadas (etapa 3):**

- Una goroutine aparte para las tandas: dos goroutines chequeando el mismo tope pueden
  pasarlo entre las dos (chequean y gastan a la vez); haría falta una reserva atómica en el
  `Store`. Con una sola el tope se chequea en un lugar.
- Reenviar la ventana de 5 min en cada tanda para que Claude resuma el sentimiento: manda
  los mismos mensajes 5 veces; con etiquetas por mensaje el resumen se cuenta en Go.
- Guardar la categoría de Claude en `Message`: cambia `datos.jsonl` (AGENTS.md).
- Persistir "Marcar respondida": la persona eligió memoria (decisión 6).
- Prompt caching del prefijo fijo: con tandas de 1 min el prefijo es ~70 % de la entrada y
  podría bajar el costo, pero cambia el modelo de costos del tope y no se verificó el
  mínimo cacheable de los modelos 5.5. Queda para después de medir (pregunta 12).
- Umbrales configurables: la persona los fijó en el código (decisión 9).

## Cuota de YouTube

- **Etapa 1:** sin cambios. Ningún pedido nuevo (CA1.19); la pantalla Canales muestra la
  estimación con las cifras de hoy (7.440 con un canal, 8.160 con Rock & Pop como extra).
- **Etapa 2:** la de canales-continuos (chat por canal; arranca con `-chat-seg* × N`, mismo
  gasto que hoy; cada intervalo nuevo se ve en la barra antes de guardar). Agregar un canal
  cuesta 1 unidad al resolverlo y suma ~480 de revisión de videos por día.
- **Etapa 3:** sin cambios (CA3.18).

## Reglas de AGENTS.md / CLAUDE.md / README que cambian (texto propuesto)

1. **AGENTS.md, párrafo de rutas** (hoy "Rutas del dashboard: `GET /` …"): "Tres pantallas
   con rutas propias: `GET /` (En vivo; `?canal=UC…` elige el canal, `?vivo=ID` el canal de
   ese vivo o el vivo suelto), `GET /programas` (Programas de hoy; `?dia=hoy|ayer|7d`,
   `?canal=`, `?tramo=<clave>`) y `GET /canales` (Canales). Parciales: `/parcial/resumen`
   (estado, 5 s), `/parcial/vivo`, `/parcial/vivo/canal` (tarjeta Canal, 5 s),
   `/parcial/vivo/chat` (2 s), `/parcial/vivo/analisis` (30 s), `/parcial/programas` (60 s
   con Hoy), `/parcial/programas/detalle`, `/parcial/canales/estado` (5 s);
   `GET /export.csv` (`?tramo=` para un programa), `GET /static/`." Más las de las etapas 2
   y 3 al implementarlas.
2. **AGENTS.md, botones de canales:** "Los botones van en la tarjeta Canal de En vivo. Si el
   vivo en curso del canal cambia, `/parcial/vivo/chat` recarga la sección del mismo canal
   con `HX-Retarget: #vivo`; nunca pasa a otro canal." (el resto igual).
3. **AGENTS.md, mapa del código:** `programs.go`, `quota.go` (etapa 1), `channels.go`
   (etapa 2), `batch.go`, `answers.go` (etapa 3), `templates/layout.html`,
   `templates/programas.html`, `templates/canales.html`; en `server.go`, `liveTiles`,
   `howItAirs`, tarjeta Canal.
4. **AGENTS.md, Estilo → Dashboard:** agregar "Tres pantallas con el mismo encabezado y
   pestañas (`templates/layout.html`); links comunes entre pantallas y htmx para los
   parciales, sin JS propio. Botones, pestañas y campos de al menos 44 px de alto. Tokens
   nuevos: `--link`, `--sent-*`, `--series-2..5`, `--ia-*`, `--field-border`,
   `--on-accent`, definidos en los dos temas; `TestTemplateTokens` lo controla."
5. **AGENTS.md, "Antes de dar algo por terminado", punto 4:** "… verificar en el navegador
   las tres pantallas …".
6. **CLAUDE.md, Propósito:** "continuo con dashboard htmx (`-dashboard :8080`)" → "continuo
   con dashboard htmx de tres pantallas: En vivo, Programas de hoy y Canales
   (`-dashboard :8080`)".
7. **AGENTS.md:7 y CLAUDE.md:10, "solo lectura" (etapas 2 y 3):** "El dashboard escribe en
   tres puntos, solo desde la PC del monitor (`sameOrigin` + `localRequest`): la
   configuración (canales, intervalos y Claude, en `datos.canales.json`), pedir un análisis
   a Claude (cola) y marcar una pregunta como respondida (en memoria). Nunca escribe en
   YouTube." Reemplaza los textos de canales-continuos ("un único punto") y de la 009 ("dos
   puntos").
8. **AGENTS.md, regla "Análisis con Claude" de la 009 (etapa 3):** "Un pedido por programa
   al terminar, otro por botón como mucho cada 10 min por canal, **y tandas de tiempo real**
   cada `BatchEvery` (1 min por defecto) por canal con el check, solo con mensajes nuevos.
   El tiempo real usa como mucho el 75 % del tope diario; el 25 % queda para los cierres de
   programa. Etiquetas por mensaje en `datos.etiquetas.jsonl`. Modelo, tandas y tope se
   editan en Canales; los flags son valores iniciales."
9. **AGENTS.md, Clasificador** (texto de la 009 "la categoría de reglas sigue siendo la
   única del CSV y de 'Categorías de hoy'"): "La columna `categoria` del CSV es siempre la de
   reglas; `categoria_claude` y `sentimiento` van al final. La tarjeta Categorías y el chat
   en vivo muestran la de Claude si el mensaje tiene etiqueta y si no la de reglas, y lo
   dicen."
10. **AGENTS.md, Formato de `Message` y CSV:** la lista de columnas suma `sentimiento,
    categoria_claude` al final.
11. **AGENTS.md, Datos y privacidad:** "`datos.etiquetas.jsonl` guarda las tandas de Claude
    con una etiqueta por mensaje (sentimiento, categoría, grupo de pregunta y lugar): 30 días
    de retención al abrir contados desde el fin de la tanda, tmp + `os.Rename`, nunca en
    `datos.jsonl`. 'Desde dónde escriben' se muestra solo agregado. 'Marcar respondida' va
    solo en memoria."
12. **AGENTS.md, Concurrencia:** "`Answers` (preguntas respondidas) es lo único además de
    `Settings` que escriben los handlers: en memoria, con su mutex, fuera del `Store`."
13. **README:** sección 3 reescrita por pantalla (etapa 1); pantalla Canales editable y
    cifras por canal (etapa 2); Claude en tiempo real con la tabla de costos, el reparto del
    tope y las columnas nuevas del CSV (etapa 3).
14. **ADR 0007 (etapa 1):** "El dashboard pasa a tres pantallas con rutas propias": rutas,
    qué se mudó y qué se quitó, Programas desde `datos.vivos.jsonl` sin archivo nuevo,
    tokens nuevos. **ADR 0005 (etapa 2):** el de canales-continuos, con la pantalla de esta
    spec. **ADR 0008 (etapa 3):** "Claude etiqueta el chat en tandas": una goroutine, reparto
    del tope, etiquetas en archivo aparte, respondidas en memoria, configuración editable;
    "Modifica: ADR 0006". Numeración: 0005 y 0006 ya están reservados (canales editables y
    009).
15. Al aprobar esta spec, la línea de estado de canales-continuos pasa a "etapa 2 absorbida
    por `2026-11-05-rediseno-tres-pantallas.md`" (lo hace el orquestador; esta tarea no edita
    esa spec).

## Fuera de alcance

- La etapa B de la 009 (resumen del día y análisis de comentarios).
- El bloque "siguiente programa" de la tira del diseño ("18:00 según el título"): no hay
  grilla de programación.
- Reordenar canales, elegir el principal desde la pantalla, editar `-pico`, `-cuota` o
  `-comentarios-min` desde el dashboard.
- Persistir "Marcar respondida", publicar respuestas o moderar en YouTube, OAuth.
- Autenticación del dashboard, editar desde otras PCs, exponerlo a la red.
- Umbrales de alertas configurables; notificaciones fuera del dashboard (sonido, mail).
- Prompt caching, Batches API y fallback del servidor ante refusal.
- Analizar con Claude días anteriores o reetiquetar lo ya etiquetado.
- Cambios en el modo informe más allá de las dos columnas vacías del CSV.
- `search.list`, base de datos, frontend con build, JS propio.

## Riesgos

- **Conflicto con la 009.** Esta spec usa nombres de su worktree (`claudeView`,
  `ClaudeCard`, `segmentMessages`, `MessagesOf`, `segmentKey`, `guard.go`,
  `ClaudeStatus`). Si cambian en su revisión, se ajustan las citas al empezar la etapa 1
  (rebase sobre `main` con la 009). Sus tests de la tarjeta cambian de título (CA1.8).
- **Conflicto con canales-editables.** Nadie tiene que implementar la etapa 2 de
  canales-continuos por su cuenta: esta spec la reemplaza (regla 15). La rama
  `feat/canales-editables` todavía no existe.
- **Tamaño del cambio.** Las plantillas se reescriben casi enteras y hay muchos tests que
  buscan textos (`TestSummaryWithoutActivityChart`, `TestDashboardWithoutRemovedSections`,
  `TestLiveChatSelection`, `TestLiveChatByChannel`, `TestChannelDayStrip`,
  `TestLiveAnalysisByChannel`, `TestLiveAnalysisSuper`, `TestLiveChatSuper`,
  `TestLiveParticipationView*` y los de la 009). Se mitiga con las mismas rutas y los mismos
  datos de las plantillas (`map[string]any`), tests que afirman contenido y no
  maquetación, y tres PRs.
- **Carga del servidor.** En vivo pide 4 parciales (2, 5, 5 y 30 s) por pestaña abierta; el
  de 30 s sigue usando `Snapshot()` como hoy. Programas usa copias filtradas (CA1.13).
- **Costo de Claude (etapa 3).** Con lo decidido, ~US$ 18 por día (hipótesis); la pregunta
  4 lo define. El tope nunca se pasa (peor caso antes de cada pedido) y el tiempo real tiene
  su parte; al llegar, todo vuelve a reglas con aviso.
- **Calidad.** Etiquetas de tandas de 1 min tienen poco contexto (ironía, chistes): la
  tarjeta lo dice ("ante la duda, mirá los mensajes"). Los grupos y motivos se mantienen
  parecidos entre tandas porque se le pasan los abiertos (CA3.2), pero pueden fragmentarse.
- **Prompt injection.** Las listas cerradas se validan en Go; los textos libres del modelo se
  cortan y se escapan; el modelo no tiene herramientas.
- **Datos guardados.** `datos.etiquetas.jsonl` crece ~9.900 etiquetas por día (~1 MB por día
  con IDs de chat largos, ~30 MB a los 30 días); se carga al abrir como las demás series.
- **"Marcar respondida" se pierde al reiniciar** (decisión 6): el texto lo dice.
- **Contraste en oscuro** de los colores nuevos: hipótesis, se revisa en CA1.21.
- **Vista previa sin `datos.vivos.jsonl`:** Programas se vería casi vacío (pregunta 13).
- **Windows / compatibilidad.** Solo stdlib y archivos con tmp + `os.Rename`; `Message` y
  `datos.jsonl` no cambian; el CSV solo suma columnas al final.

## Preguntas abiertas para la persona que aprueba

1. **Modo claro y oscuro** con los tokens (AGENTS.md lo exige), cambiando `--text-muted` a
   #6b6a65 y los links a #1f63b8 por contraste. Recomiendo que sí. No bloqueante.
2. **Una ruta por pestaña** (`/`, `/programas`, `/canales`), htmx para los parciales, sin
   JS propio ni build; `?canal=` y `?vivo=` siguen andando. Recomiendo que sí. No
   bloqueante.
3. **Edición solo desde la PC del monitor** (loopback + `sameOrigin`, `guard.go`), también
   para Claude y "Marcar respondida". Recomiendo que sí (es lo decidido en
   canales-continuos y en la 009). No bloqueante.
4. **Costo del tiempo real (bloqueante para la etapa 3).** Con Sonnet cada 1 min son ~US$ 18
   por día (~US$ 545 por mes) con la hipótesis de volumen; el diseño mismo recomienda Haiku.
   Opciones: (a) Sonnet 1 min y un tope de ~US$ 20; (b) **dos modelos: Haiku 5.5 para el
   tiempo real y Sonnet 5.5 para los resúmenes de programa** (~US$ 2,3 por día), con dos
   selects en la tarjeta de Claude; (c) Sonnet cada 5 min (~US$ 7,7 por día). Recomiendo
   (b). Y qué tope por defecto sugerir en el README.
5. **Reparto del tope:** 75 % tiempo real / 25 % reservado a cierres de programa.
   Recomiendo que sí. No bloqueante.
6. **Palabras más repetidas, Quiénes más escriben y Categorías globales** quedan juntas en
   "Todo el monitor · últimas 24 h" (con comentarios y todos los canales, como hoy), en vez
   de pasar a ser por canal. Recomiendo así para no perder los comentarios. No bloqueante.
7. **Se quitan** la lista de vivos en curso y los 5 tiles globales del resumen (los
   reemplazan la tarjeta Canal y los tiles por alcance); "Cuota usada hoy" pasa a Canales.
   ¿De acuerdo? No bloqueante.
8. **Alerta de insultos con un mínimo de 20 mensajes** en los 10 min (si no, 1 insulto en 3
   mensajes dispara). Es un agregado a la decisión 9. No bloqueante.
9. **Al arrancar, el tiempo real etiqueta solo desde 10 min antes** (no se pone al día con
   todo lo no etiquetado, para no gastar de golpe). No bloqueante.
10. **"Analizar el chat con Claude"** viene activado en los canales existentes y en los que
    se agregan (si hay clave). No bloqueante.
11. **Tope máximo editable desde la pantalla:** US$ 50 por día, para que un error de tipeo no
    habilite un gasto grande; más que eso, con el flag. No bloqueante.
12. **Prompt caching** para bajar el costo de las tandas: afuera ahora; se revisa con los
    costos medidos la primera semana. No bloqueante.
13. **Vista previa (bloqueante para cerrar CA1.21 como está escrito).** Hoy copia
    `datos.jsonl`, `datos.cuota.json` y `datos.espectadores.jsonl`, no `datos.vivos.jsonl`
    (`tools/preview/main.go:47`), así que Programas y la tira se verían casi vacíos.
    Opciones: (a) sumar la copia de `datos.vivos.jsonl` (y en la etapa 1 también
    `datos.analisis.jsonl`, para ver resúmenes) a la vista previa, en la misma carpeta 0700
    que se borra al cortar, con su test; cambia el ADR 0003 y la decisión 7 de
    canales-continuos; (b) revisar Programas en el navegador solo con lo que arma la API
    simulada (el programa de hoy) y cubrir el resto con tests. Recomiendo (a).
