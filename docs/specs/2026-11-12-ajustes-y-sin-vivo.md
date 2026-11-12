# Spec: ajustes de las pantallas y pantalla Sin vivo

- **Estado:** implementada
- **Tipo:** mejora (con 4 bugs: 2, 6, 8, 10)
- **Rama:** `feat/ajustes-y-sin-vivo`
- **Origen:** pedido del usuario del 2026-11-12 (11 puntos, con capturas de «Cómo llegaron los likes» y de la tira «Hoy en el canal»)
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-12

## Contexto y causas

1. Programas solo ofrece Hoy / Ayer / 7 días (`dayRange`, `programs.go:118`; opciones en `programs.go:551`).
2. **Bug:** tocar el semicírculo de Categorías baja la pantalla. Sectores y filas tienen `href="#cat-detalle"` (`templates/partials.html:393`, `:398`): htmx no cancela el clic en links que empiezan con `#` (y en un `<a>` de SVG no cancela nunca), así que el navegador salta al ancla. Pasa en En vivo, Programas y Comentarios.
3. En vivo: las barras de «Momentos con más participación» no se pueden tocar; en Programas sí (`programSlotLinks`, `programchat.go:341`).
4. Programas: el semicírculo de Categorías solo aparece con etiquetas de Claude **y** sin temas del resumen (`else if .CatsClaude`, `templates/programas.html:100`; `d.Gauge` solo con Claude, `programs.go:769`). Al terminar el programa, o sin Claude, no se ve.
5. Programas, «Segmentos por asistencia»: solo una tabla (`templates/programas.html:230`).
6. **Bug:** Comentarios en «Todos» muestra videos y shorts de OLGA, que no está en Canales. El archivo de videos guarda 30 días de todo lo revisado y `commentScope.in` no filtra canal cuando `Canal == ""` (`comments.go:299`).
7. Comentarios tiene el período «Publicados en 30 días» (`pub`, `comments.go:45`) que no se quiere.
8. **Bug:** «Cómo llegaron los likes» se dibuja vacío. Para un video publicado antes de que se guardaran los likes (11/11 17:11) la primera muestra es la base (`likeCurve`, `comments.go:793`); si no sumó likes nuevos, todas las barras son 0. Además los rótulos se pisan («0–1 h1–2 h»).
9. En vivo tiene la tarjeta «Lo que más gustó · 7 días» (`topLiked`, `livecards.go:405`; `templates/partials.html:229`) que no se quiere.
10. **Bug:** en la tira «Hoy en el canal», el hueco entre dos vivos distintos de 09:17 a 09:23 no sale como «Sin vivo»: el umbral es `segmentGap` = 7 min (`analyze.go:480`, usado en `analyze.go:529`) y el hueco fue de 6.
11. No hay dónde analizar los momentos sin vivo. Se eligió la opción A: una pantalla nueva.

## Objetivo

Las 10 correcciones y ajustes de arriba en las pantallas actuales, y una quinta pestaña «Sin vivo» con los cortes de cada canal, armada solo con `datos.vivos.jsonl` (sin cuota extra).

## Criterios de aceptación

- [x] CA1: Programas tiene «Últimos 30 días» (`?dia=30d`): lista los tramos de los 30 días locales hasta hoy y el título dice «Programas de los últimos 30 días · del DD/MM al DD/MM». Un `dia` desconocido sigue cayendo en Hoy. — `TestDayRange`, `TestProgramsPage30d`
- [x] CA2: ningún link ni sector del semicírculo (`cgauge`) tiene `href` que empiece con `#`. Los sectores del SVG siguen siendo alcanzables con el teclado (`tabindex="0"`, `role="button"`, `hx-trigger` con clic y Enter) y las filas de la lista son `<button>` (sin `href`). — `TestCatGaugeNoAnchor` + navegador (tocar no mueve la pantalla)
- [x] CA3: en En vivo cada barra del gráfico de Momentos es un link a `/parcial/vivo/analisis?…&pfranja=<unix>` (`franja` ya es la del gráfico de Sentimiento). Con una `pfranja` que coincide con una barra del gráfico, debajo aparece el detalle de esa franja (hora, programa y sus mensajes de chat, sin spam ni vacíos) y la barra queda marcada; una `pfranja` que no coincide se ignora y no se refleja en el HTML. El refresco de 30 s conserva la franja elegida. — `TestLiveSlotDetail`
- [x] CA4: el detalle de un programa siempre tiene la tarjeta «Categorías» con el semicírculo: por reglas (`Classify`) sin Claude, con las etiquetas de las tandas cuando hay (como En vivo, ADR 0010). Los temas del resumen de Claude se siguen viendo aparte. — `TestProgramDetailGauge` (con y sin Claude, con y sin resumen)
- [x] CA5: «Segmentos por asistencia» muestra dos barras apiladas al 100 %, «Personas» y «Mensajes», un tramo por segmento con su color (`.sg` con los mismos colores que `.sw`) y `title` con nombre y %; con 0 participantes no se dibujan. — `TestSegmentBars`
- [x] CA6: en Comentarios, «Todos» (resumen, tabla, gráfico y números) suma solo comentarios de videos de canales que hoy están en la configuración; un video de un canal quitado, o de canal desconocido, no aparece. — `TestCommentsAllOnlyConfigured`
- [x] CA7: el período «Publicados en 30 días» ya no se ofrece; `?periodo=pub` cae en «Últimos 30 días». Se borra la lógica de `pub` (`PubSince` y las ramas de `CommentsSummary` / `commentRows`). — `TestCommentsQuery`, `TestCommentsSummary`
- [x] CA8: si la curva de likes de un video no tiene ningún like repartido, no se dibuja el gráfico: se muestra «Desde el DD/MM HH:MM, cuando se empezaron a guardar los likes, este video no sumó likes nuevos.» Los rótulos del eje de la curva no se pisan en ancho de escritorio ni de celular. — `TestLikeCurveEmpty` + navegador
- [x] CA9: En vivo ya no tiene «Lo que más gustó · 7 días»; se borran `topLiked`, `topLikedN`, `LikedRow` si quedan sin uso. — `TestLivePageNoTopLiked`
- [x] CA10: `ProgramSegments` marca «sin vivo» un hueco de más de 4 min (`newLiveGap`: una revisión de 3 min + 1 de margen) cuando el tramo que sigue es de **otro** vivo; entre tramos del mismo vivo sigue `segmentGap` (7 min). El caso 09:14–09:17 (vivo A) y 09:23 (vivo B) da un tramo «sin vivo» de 09:17 a 09:23; un hueco de 3 min entre vivos distintos no. — `TestProgramSegments` (casos nuevos)
- [x] CA11: `GET /sin-vivo` (pestaña «Sin vivo», `?canal=UC…` de los configurados, `?periodo=7d|30d`, por defecto 7d) y `GET /parcial/sin-vivo` (refresco 60 s) muestran, por canal:
  - números: cortes hoy, minutos sin vivo hoy, % al aire del período y corte más largo del período;
  - una tira de 24 h por día del período (hoy hasta ahora), con los tramos al aire y sin vivo en su posición y `<title>` con horario;
  - la lista de cortes, del más nuevo al más viejo (como mucho 50, con «y N más»): día, de HH:MM a HH:MM, duración, programa de antes → programa de después;
  - un hueco de más de 2 h (`maxCut`) no cuenta como corte: se pinta «sin datos» (puede ser el monitor apagado) y queda fuera de los números;
  - nota de precisión: «Los cortes se miden con las revisiones de videos: pueden correrse de 1 a 3 minutos».
  Sin llamadas a YouTube ni escrituras: solo `Store.Lives` y `Store.Status`. — `TestNoLiveCuts` (función pura), `TestNoLivePage`
- [x] CA12: las cinco pestañas en `templates/layout.html` y `TestTemplateTokens` en verde (colores nuevos en ambos temas).
- [x] Gates de `CLAUDE.md` → «Definición de terminado» en verde; README y `AGENTS.md` (mapa, rutas) actualizados; navegador en las cinco pantallas, claro y oscuro, ancho de celular.

## Diseño propuesto

- **1:** `dayRange` con `"30d"` (30 días); opción y título en `programsData`. `programRows` ya hace una pasada con búsqueda binaria; medir con datos sintéticos de 30 días.
- **2:** en `cgauge`, filas como `<button>`; sectores SVG como `<g>` sin `href`, con `tabindex="0" role="button" hx-trigger="click, keyup[key=='Enter']"`. Sin JS propio.
- **3:** `liveAnalysis` lee `pfranja`; `participationView` la valida contra las franjas del gráfico (`pickSlot`) y `momentSlotLinks` pone los links y arma el detalle (`SlotDetail`, plantilla `vivo-franja`). `part-chart` toma destino y `hx-sync` de cada pantalla. La franja viaja en el input oculto `.vq` como `ver`/`horas`.
- **4:** `programDetail` arma siempre `d.Gauge` (con `labels` nil sin Claude); en `programas.html` una tarjeta «Categorías» propia y los temas en la suya.
- **5:** `segmentBars(segs []LoyaltySegment)` en `loyalty.go` (pura) y SVG en la plantilla.
- **6:** `commentScope` recibe el conjunto de IDs configurados (`Status().Channels`); con `Canal == ""` exige `ChannelID` en ese conjunto.
- **7:** sacar `pub` de `commentPeriods` y sus ramas.
- **8:** `likeCurve` devuelve también el total repartido; con 0, `LikeCurve` nil y la frase. Rótulos de `curveChart` más cortos o en dos líneas.
- **9:** sacar la tarjeta y su código.
- **10:** en `ProgramSegments`, el umbral del hueco antes de `sg` es `newLiveGap` si `sg.VideoID` ≠ el del tramo que cubrió hasta `covered`, si no `segmentGap`. `LiveHours` y `ContinuityBanner` no cambian.
- **11:** archivo nuevo `nolive.go` (puro): `NoLiveCuts(samples, channelID, days, now)` usa `ProgramSegments` con `from = día − 12 h` (como `programSegs`) recortado a cada día, y devuelve días con tramos, cortes y números. `nolivepage.go` con los handlers; `templates/sinvivo.html` (`sinvivo-cuerpo`). Canales: los de `Status().Channels`, como En vivo.

## Fuera de alcance

- Guardar un «latido» del worker para distinguir «sin vivo» de «monitor apagado» (se aproxima con `maxCut`).
- Mapa de calor, transiciones con audiencia y tarjeta en En vivo (opciones B, C y D).
- Cambiar `LiveHours` o la estimación de cuota.

## Riesgos

- **Cuota:** ninguno; todo sale de datos ya guardados.
- **Falsos «sin vivo» (10, 11):** si se pierde la revisión justo en un cambio de vivo, aparece un corte corto que no fue; se acota con el umbral de 4 min y la nota de precisión.
- **Monitor apagado (11):** sin muestras no se sabe si hubo vivo; huecos de más de 2 h se muestran como «sin datos», no como corte.
- **Rendimiento (1, 11):** 30 días de mensajes y muestras por refresco; los handlers solo leen copias y se verifica con un test de datos sintéticos grandes.
- **Datos:** no cambia ningún formato de archivo; Comentarios solo deja de mostrar canales quitados (siguen en el archivo hasta los 30 días).
