# Spec: diseño ejecutivo del dashboard

- **Estado:** borrador
- **Tipo:** mejora
- **Rama:** `feat/diseno-ejecutivo`
- **Origen:** pedido del usuario (2026-11-11): un dashboard más profesional y minimalista, para gerentes y directores. Eligió la propuesta A «Ejecutivo claro» con la frase de resumen de la C (lienzo de diseño, `Main.dc.html` y `Editorial.dc.html`). En el ajuste pidió sumar a esta spec el eje X con lo más nuevo a la derecha, los likes en En vivo, «Habituales contra nuevos» y conservar los Super Chats con el estilo nuevo.
- **Aprobada por / fecha:** —

## Contexto

- El dashboard es funcional pero técnico: pestañas redondas, tarjetas que pesan todas igual, «Palabras y autores» como chips y una tabla (`templates/partials.html:195`, `monitorView` en `server.go:913`), momentos para reels en tabla (`partials.html:173`) y Super Chats en tablas (`partials.html:~215`).
- Los gráficos ponen lo más nuevo a la izquierda desde `853dd7e` (pedido de ese momento; ahora se pide al revés): `sentimentChart` (`batch.go:495`, `:527`, `:541`), `participationChart` / `viewersLine` y sus rótulos (`server.go:1969`, `:2068`, `:2105`, `:2140`), el relleno del largo esperado en Programas (`programSlots`/`padChart`, `programs.go:1098`, `:1117`) y la tira «Hoy en el canal» (`dayStrip`, `server.go:1736`).
- En vivo no muestra likes. Están en Programas (`segmentLikes`, `likesDayChart`, `programs.go:1366`, `:1442`) y en Comentarios, en columnas de tablas anchas.

## Objetivo

Que un gerente entienda En vivo en cinco segundos: una frase que resume, los números con su comparación (likes incluidos), gráficos que se leen de izquierda (antes) a derecha (ahora) y tarjetas limpias. Las otras tres pantallas toman el mismo estilo sin cambiar su estructura.

## Criterios de aceptación

**Estilo**
- [ ] CA1: estilo común en `layout.html`: fondo gris claro, tarjetas blancas con borde fino y radio 14 px, un solo acento azul, etiquetas en versalitas, pestañas como control segmentado y marca «Monitor de audiencia · Alphamedia». Todo color nuevo en los dos temas — `TestTemplateTokens`.
- [ ] CA2: cabecera del vivo: chip «EN VIVO», canal y «desde HH:MM»; el programa como título grande; botones de canal y «Ver en YouTube» a la derecha (se apilan en celular) — test de render de `vivo-canal`.

**Arriba de En vivo**
- [ ] CA3: `liveSummary` arma la frase (reglas, sin Claude): ritmo del chat contra lo típico, las dos palabras más repetidas y el mejor momento; cada parte solo si hay dato; sin ningún dato, no hay frase — `TestLiveSummary` (tabla).
- [ ] CA4: siete números en este orden: Mirando ahora, Participación (% de los que miran que escribió en los últimos 5 min, contra la típica del vivo), Likes del vivo, Mensajes por minuto, Personas que escribieron, Preguntas, Super Chats. Cada uno con su comparación en verde, naranja o gris — `TestLiveTiles` actualizado.
- [ ] CA5: «Likes del vivo»: el contador del vivo en curso (`Video.Likes`) y «+N en la última media hora» con `Store.Likes` (la última muestra menos la última hasta hace 30 min, como `segmentLikes`); con «Todo el día del canal», la suma de lo que ganaron los vivos del canal hoy; sin dos muestras, «—» y «sin dato todavía» — `TestLiveLikes` (tabla).

**Tarjetas**
- [ ] CA6: «Clima del chat»: en Sentimiento, el tono que más pesa en grande y una barra apilada positivo / neutro / negativo con los mismos % que las tres tarjetas de hoy, que reemplaza — test de render.
- [ ] CA7: «Mejores momentos para reels» como tarjetas (hora, %, programa, mirando y personas, link al minuto); los mensajes siguen plegados con el mismo `abrir` (`mom1`–`mom5`) — test de render y `TestFoldState` sin cambios.
- [ ] CA8: «De qué se habla»: top 8 en barras, ▲ en las que suben (en los últimos 30 min pesan el doble que antes y tienen al menos 3 menciones, `risingWords`) — `TestRisingWords`.
- [ ] CA9: «Quiénes participan»: barra «habituales / nuevos» (habitual = escribió en el chat del mismo canal otro día de los 7 anteriores a hoy), lo que escriben los 10 más activos sobre el total (`topShare`) y el top 5 con barras y la marca «habitual» o «nuevo» — `TestRegulars`, `TestTopShare`.
- [ ] CA10: «Likes» (tarjeta nueva de En vivo, con canal): barras de likes nuevos del vivo cada 30 min de hoy (reusa `likesDayChart`) y «Lo que más gustó · 7 días»: vivos, videos y shorts del canal publicados en 7 días, por likes, con su etiqueta Vivo / Video / Short (de `Store.VideoArchive`) — `TestTopLiked` y test de render.
- [ ] CA11: Super Chats con el estilo nuevo: total por moneda como números grandes, tabla por programa y últimos pagos como lista; mismos datos que hoy — test de render.

**Eje X**
- [ ] CA12: en todos los gráficos con tiempo, lo más viejo a la izquierda y lo más nuevo a la derecha, con el rótulo «Ahora» en el borde derecho: participación de En vivo y de Programas (barras, línea de espectadores, bandas de comparación, números de los momentos), sentimiento (barras, cortes, franja de programas) y la tira «Hoy en el canal» (lo al aire a la derecha; la tira scrollea hasta el final). El hueco del largo esperado en Programas queda a la derecha. La lista de texto de programas y las filas de Sentimiento de Programas siguen con lo más nuevo arriba — `TestParticipationChartNewestLeft` y `TestDayStripNewestFirst` se dan vuelta (pasan a `…NewestRight`); `TestSentimentChart`, `TestParticipationChart*`, `TestParticipationProgramsStrip` y `TestProgramParticipationAxis` actualizados.

**Cierre**
- [ ] CA13: vista previa con datos: las cuatro pantallas en claro, oscuro y ancho de celular, sin scroll horizontal de la página.
- [ ] README y AGENTS.md: actualizar las menciones a «lo más nuevo a la izquierda» y sumar la tarjeta Likes y Habituales.
- [ ] Gates de `CLAUDE.md` → «Definición de terminado» en verde.

## Diseño propuesto

- `layout.html`: tokens nuevos (mismos nombres, otros valores, más los que hagan falta en los dos temas) y CSS común. Sin fuentes externas: sigue `system-ui` (Segoe UI en Windows).
- Funciones puras nuevas en `server.go` (o `livecards.go` si crece): `liveSummary`, `liveLikes`, `risingWords`, `topShare`, `regularSplit`, `topLiked`. `liveTiles` suma Participación y Likes.
- Habituales sin leer 7 días en cada refresco: `Server` guarda en memoria, por canal, el conjunto de autores de chat de los 7 días anteriores a hoy (`regularsCache`, con mutex). Se arma con `Store.Lives` + `Store.MessagesOf` la primera vez del día local para ese canal y se reusa hasta medianoche; cada refresco solo consulta el mapa. No se guarda en disco.
- Eje X: dejar de espejar en `sentimentChart`, `participationChartCmp`, `viewersLine`, `padChart` y `dayStrip` (posición `i` en vez de `len-1-i`, rótulos desde el principio con «Ahora» al final). La escala de espectadores pasa al borde izquierdo para no chocar con «Ahora».
- Primero los tests en rojo (funciones puras y gráficos invertidos), después las plantillas.

## Fuera de alcance

- Cambiar la estructura de Programas, Comentarios y Canales (solo toman el estilo y el eje X).
- Fuentes web, JS propio, nuevos archivos de datos.

## Riesgos

- Cuota y Claude: ninguno; todo sale de datos ya guardados.
- Performance: la caché de habituales lee 7 días de mensajes de un canal una vez por día (al primer refresco de En vivo); el resto, una pasada más sobre los mensajes del alcance cada 30 s. `topLiked` copia el archivo de videos (`VideoArchive`) en cada refresco del análisis: cientos de registros.
- Privacidad: la caché son nombres de autores que ya están en memoria en el `Store`; no se guardan ni se muestran fuera de En vivo, y los nombres se ven igual que hoy en «Quiénes más escriben».
- Concurrencia: la caché la escriben los handlers con su propio mutex; no toca el `Store` ni llama a YouTube.
- Muchos tests de gráficos dependen del orden espejado: se actualizan los esperados, no la lógica de franjas.
