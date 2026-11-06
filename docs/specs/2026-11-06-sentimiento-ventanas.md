# Spec: sentimiento legible, con ventanas de tiempo y en Programas de hoy

- **Estado:** aprobada
- **Tipo:** mejora
- **Rama:** `feat/sentimiento-ventanas`
- **Origen:** pedido de la persona (capturas de "Cómo fue cambiando" y "Sentimiento del programa"), opciones recomendadas aceptadas
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-06

## Contexto

- En vivo, "Cómo fue cambiando · cada 5 min": `sentimentChart` (`batch.go:466`) pone el nombre de cada programa que empieza en un `<span>` arriba de las barras (`templates/partials.html:126`, `.slines`). Con tramos cortos (el título del vivo cambia varias veces seguidas) los textos se pisan y no se sabe qué programa es cada barra.
- "Por qué hay mensajes negativos" / "Qué está gustando" miran solo los últimos 5 min (`server.go:1022-1025`, `now.Add(-slotStep)`).
- Una franja sin color es la que tiene menos de `minSentMsgs` = 20 mensajes (`batch.go:291`, `batch.go:483`). Ese mismo 20 se usa para lo típico y la alerta de insultos.
- Programas de hoy, "Sentimiento del programa" (`templates/programas.html:76-80`): una sola barra del resumen de Claude; la tarjeta queda vacía. No hay motivos del programa.
- Canales: la persona quiere ver el gasto real y el crédito. El reporte de costos de Anthropic pide una Admin API key (`sk-ant-admin…`) y el saldo de crédito no tiene endpoint público.

## Objetivo

Que el gráfico de En vivo diga sin pisarse qué programa es cada tramo, que los motivos se puedan ver en otras ventanas de tiempo, que Programas de hoy muestre el sentimiento franja por franja y los motivos de todo el programa, y que Canales diga de dónde sale el gasto y dónde ver el oficial. Todo con las tandas ya guardadas: **ningún pedido nuevo a Claude ni a YouTube**.

## Criterios de aceptación

- [ ] CA1: `sentimentChart` no devuelve textos sobre las líneas; devuelve una franja de programas (`Strip`: bloque por programa con `Left`/`Width` en %, nombre y rango "HH:MM a HH:MM" para el `<title>`) y una lista "HH:MM · Programa". Un tramo de menos de 5 min se junta con el anterior (o con el siguiente si es el primero). — `TestSentimentChart` (casos: dos programas, tramo de 2 min absorbido, tramo antes de `from` recortado).
- [ ] CA2: la plantilla ya no tiene `.slines` con texto; la franja lleva `aria-label`/`<title>` y el nombre cortado con `text-overflow: ellipsis`. — `TestDashboardSentiment` (o el test de En vivo existente) busca el bloque y que no esté el span viejo.
- [ ] CA3: botones "Últimos 5 min · 30 min · 1 h · Todo el programa" arriba de los motivos, con parámetro `motivos` = `5|30|60|programa` (lista cerrada; cualquier otro valor cae a `5` y nunca se refleja en el HTML). Viaja como `ver`/`horas`/`abrir` (input oculto `.vq`, refresco y cada botón), con `aria-pressed`. Con "Todo el día" el último botón dice "Todo el día". — `TestMotivesWindow` (parámetro válido, inválido y escapado) y test del parcial.
- [ ] CA4: con la ventana elegida, `Motives` recibe las tandas desde `now - ventana` (o el inicio del tramo / del día) y el total de negativos/positivos de esa ventana, contados con las etiquetas de los mensajes del alcance; el texto dice "N negativos en los últimos 30 min" / "en todo el programa". — `TestMotives` extendido + test del parcial.
- [ ] CA5: una franja queda sin color solo con menos de 5 mensajes (`minChartMsgs = 5`); `minSentMsgs` sigue en 20 para lo típico y la alerta. Leyenda: "Menos de 5 mensajes: sin color". — `TestSentimentChart` (franja de 4 sin color, de 5 con color) y `TestAlerts`/`TestSentimentTypical` sin cambios.
- [ ] CA6: Programas de hoy, "Sentimiento del programa" muestra una fila por franja de 5 min del tramo con datos (hora + barra horizontal apilada pos/neu/neg, `<title>` con %, y conteo; menos de 5 sin color), con `Store.SentSlots` del canal filtrado por el rango y el programa del tramo. La barra del resumen de Claude y el tono siguen arriba si existen. Sin franjas: "Todavía no hay mensajes analizados por Claude en este programa." — `TestProgramSentiment`.
- [ ] CA7: debajo del resumen del chat, "Por qué hubo mensajes negativos" y "Qué fue lo que gustó" con `Motives` de las tandas del canal entre el inicio y el fin del tramo, citas solo de los mensajes del tramo (sin autor). Con Claude apagado o sin tandas, no aparece. — `TestProgramMotives`.
- [ ] CA8: la tarjeta de Claude de Canales dice que "Gastado hoy" se calcula con los tokens de cada respuesta a precio de lista y tiene el link "Ver gasto y crédito en la consola de Anthropic" (`https://platform.claude.com/settings/billing`, `rel="noopener"`). — test de `/canales`.
- [ ] CA9: `TestTemplateTokens` en verde (colores nuevos con tokens en los dos temas), botones de 44 px.
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde, y vista previa en claro, oscuro y ancho de celular.

## Diseño propuesto

- `batch.go`: `minChartMsgs`; `sentimentChart` arma `Strip []StripBlock` (reusar el tipo de `dayStrip` si encaja) y `Programs []string` en lugar de `Label` en `SentLine`; helper puro `mergeShortSegs(segs, 5*time.Minute)`. `Motives` no cambia de firma: cambia el `since` y el `total`.
- `server.go`: `motivesParam(q)` (lista cerrada `motiveWindows`), lo suma a `vivo-q`/`.vq`; `sentimentData` calcula la ventana y los totales (helper puro `sentCount(msgs, labels, since, now)`).
- `programs.go`: `programDetail` suma `SentRows` (de `Store.SentSlots(ch, ps.Start)` filtrado por `[Start, End)` y programa) y `Neg`/`Pos` (`Store.Batches(ch, Start)` con `b.To` dentro del tramo; `byID` de los mensajes del tramo).
- Plantillas: `partials.html` (franja, botones, leyenda, textos), `programas.html` (filas y motivos, reusa `motivos`), `canales.html` (nota y link), CSS en `layout.html` con tokens existentes (`--sent-*`).
- Tests primero: `TestSentimentChart`, `TestMotivesWindow`, `TestProgramSentiment`, `TestProgramMotives`.

## Fuera de alcance

- Consultar el gasto o el saldo en la API de Anthropic (Admin API key): no se agrega ninguna clave.
- Tocar una barra para ver los motivos de esa franja.
- Cambiar umbrales de lo típico y de la alerta de insultos, o el gráfico de "Momentos".

## Riesgos

- Cuota y gasto: ninguno; todo sale de `SentSlots` y `Batches` ya guardados.
- Performance: "Todo el programa"/"Todo el día" recorre las tandas del canal del día en cada refresco de 30 s (hasta ~2.900 con tandas de 30 s). Se limita al canal y a `since`, como ya hace `server.go:1137`; si pesa, se cachea por versión de tandas.
- Seguridad: `motivos` es un parámetro nuevo que se refleja en URLs: lista cerrada, igual que `abrir`.
- Privacidad: las citas siguen sin autor; nada nuevo se guarda.
