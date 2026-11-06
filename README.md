# yt-monitor: monitor de comentarios y chat de YouTube

Primer bot del piloto con **Radio Rivadavia** (canal `UCLAj5TzCrNbqe3LosSKHLcQ`, @RadioRivadaviaAM630).
Solo **lee** datos públicos con una API key: no publica, no modera y **no necesita acceso de administrador** al canal.

Qué hace:
1. Toma los últimos videos del canal y todos sus vivos en curso (también el vivo de 24 horas, aunque haya empezado hace días). Opcionalmente sigue también los vivos de otros canales.
2. Baja los comentarios de las últimas N horas.
3. Si hay vivos en curso y se pide, lee su chat durante N minutos.
4. Clasifica cada mensaje por reglas simples: pregunta, queja, elogio, insulto, spam, otro.
5. Genera `informes/informe_<fecha>.md` (resumen) y `informes/mensajes_<fecha>.csv` (todo, para analizar con Claude).

Go puro, sin dependencias externas. Tiene dos modos: un informe puntual (`go run .`) y un servidor que queda corriendo con un dashboard en vivo (`go run . -dashboard :8080`).

## 1. Conseguir la API key (gratis, 10 minutos)

1. Entrar a https://console.cloud.google.com/ con una cuenta de Google y crear un proyecto (por ejemplo `alphamedia-yt-monitor`).
2. En **APIs y servicios > Biblioteca**, buscar **YouTube Data API v3** y tocar **Habilitar**.
3. En **APIs y servicios > Credenciales**, **Crear credenciales > Clave de API**.
4. Editar la clave y en **Restricciones de API** dejar solo YouTube Data API v3.
5. No compartir la clave ni subirla a git.

La cuota es de 10.000 unidades por día y es gratis; no hace falta cargar tarjeta.

## 2. Correrlo

La clave se puede poner en un archivo `.env` en la misma carpeta (`YT_API_KEY=tu_clave`) o como variable de entorno.

```bash
go run .                       # comentarios de las últimas 24 h en los últimos 15 videos
go run . -horas 72 -videos 30  # más amplio
go run . -chat-min 30          # además, si hay un vivo, lee su chat 30 minutos
```

Opciones: `-canal` (uno o varios canales, ver abajo), `-videos`, `-horas`, `-chat-min`, `-salida`, `-vivos`, `-zona` (zona horaria de las horas que se muestran; por defecto `America/Argentina/Buenos_Aires`, así que da la hora argentina aunque la PC o WSL estén en UTC; `-zona ""` usa la de la PC o la de la variable `TZ`).

Los vivos del canal se detectan solos. Si alguno no aparece, se puede agregar a mano con `-vivos` (links o IDs separados por coma y **sin espacios**; si se escapa un espacio después de la coma, el monitor avisa y suma igual el link):

```bash
go run . -dashboard :8080 -vivos https://www.youtube.com/watch?v=gAqhej1HbKY
```

### Varios canales y varios vivos a la vez

`-canal` acepta varios canales separados por coma, con el ID (`UC...`), el @handle o el link del canal. El primero es el principal: de ese se revisan los últimos videos y sus vivos. De los demás solo se siguen los vivos en curso (y los programados), así no se gasta cuota en comentarios de videos viejos de otros canales:

```bash
go run . -dashboard :8080 -canal UCLAj5TzCrNbqe3LosSKHLcQ,@OtroCanal,https://www.youtube.com/@OtraRadio
```

Si un canal tiene varios vivos al mismo tiempo (por ejemplo, dos programas en paralelo), aparecen todos. Un vivo puntual de un canal que no querés seguir entero se puede agregar con `-vivos`.

Por ejemplo, para sumar a Luzu TV además de Rivadavia y del vivo de Rock & Pop:

```bash
go run . -dashboard :8080 -canal UCLAj5TzCrNbqe3LosSKHLcQ,@luzutv -vivos https://www.youtube.com/watch?v=0oZUGHfEOpU,https://www.youtube.com/watch?v=K60NNYn5sqA
```

Con `@luzutv` en `-canal` también aparecen solos los vivos de los días siguientes; el link en `-vivos` asegura el de hoy aunque falle la detección.

En el modo dashboard, el chat de cada vivo llega **en tiempo real** (ver la sección 3). Si YouTube no deja usar el tiempo real en algún chat, ese chat se lee con consultas comunes. **Cada canal tiene su intervalo** (uno en el horario pico y otro fuera), que se elige en la pantalla Canales: es el mínimo entre dos conexiones o lecturas de los vivos de ese canal, y si un canal tiene varios vivos a la vez se turnan (ese canal gasta lo mismo con uno o con varios). Cada canal suma su propio chat a la cuota. Al arrancar sin configuración guardada, cada canal lee cada `-chat-seg-pico` × N y `-chat-seg` × N (N = cantidad de canales de `-canal`): con 2 canales, cada 2 minutos en el horario pico y cada 6 el resto, el mismo gasto que cuando todos los vivos se turnaban con un solo intervalo. Los vivos de `-vivos` que no son de esos canales leen su chat aparte, con `-chat-seg-pico` y `-chat-seg` tal cual, y **suman su propio chat**: con los valores por defecto, un vivo suelto de 24 h son ~5.000 unidades más por día (lo mismo que un canal), así que con un canal y un vivo suelto el chat no entra en el día; la barra de Canales lo muestra como "Vivos sueltos de -vivos". No se pierden mensajes: con intervalos más largos solo llegan con más atraso. En el modo informe los `-chat-min` minutos se reparten entre los vivos.

Radio Rivadavia transmite un único vivo de 24 horas y le cambia el título con cada programa ("🔴RADIO RIVADAVIA en VIVO | CRISTINA SIN VUELTAS con CRISTINA PÉREZ"). Por eso cada mensaje de chat guarda el programa que estaba al aire cuando llegó, con hasta 3 minutos de demora respecto del cambio de título.

Cuota que gasta: unas 4 unidades fijas, más 1 por cada página de 100 comentarios, más 5 por cada lectura del chat (una cada 10 s como mínimo, unas 30 por cada 5 minutos). Un informe diario con 30 minutos de chat gasta alrededor de 1.000 unidades: sobra.

Sobre el chat en vivo: la documentación de YouTube no dice con claridad si se puede leer solo con API key. Si YouTube lo rechaza, el informe lo muestra y los comentarios igual se procesan; en ese caso, la lectura del chat queda para cuando haya acceso al canal (OAuth).

## 3. Modo continuo con dashboard en vivo

```bash
go run . -dashboard :8080
```

Abrí http://localhost:8080 en el navegador. El programa queda corriendo (Ctrl+C para cortarlo) y:

- cada 3 minutos revisa los videos del canal y si hay vivos (en todos los canales de `-canal`);
- cada 10 minutos baja los comentarios nuevos (solo de los videos cuyo contador cambió);
- mientras hay un vivo, recibe su chat **en tiempo real**: abre una conexión de streaming con YouTube (`liveChat/messages/stream`) que queda abierta y por la que llega cada mensaje apenas se publica. Con varios vivos abre una por vivo. Si la conexión se corta, la vuelve a abrir desde el último mensaje recibido, así que no se pierde nada. Si YouTube no permite el streaming en algún chat, ese chat se lee como antes, con el intervalo de su canal (con un solo canal y sin cambiarlo en Canales, cada 60 segundos de 7 a 20 h, el horario pico, y cada 3 minutos el resto del día), por turnos si el canal tiene varios vivos. El aviso del chat en el dashboard dice cuál de los dos modos se está usando;
- guarda todo en `datos.jsonl`, así no se pierde si lo reiniciás, y borra lo que tiene más de 30 días (lo piden las políticas de YouTube);
- en cada revisión de videos anota cuántos miran cada vivo en `datos.espectadores.jsonl` (sale del mismo pedido, no gasta cuota extra), con la misma retención de 30 días. Si el canal oculta el contador de espectadores, no se anota nada y el dashboard lo muestra como "sin dato";
- en la misma revisión anota qué vivo tiene en curso cada canal y con qué título en `datos.vivos.jsonl` (tampoco gasta cuota extra; misma retención de 30 días). De ahí salen la tira "Hoy en el canal", la pantalla Programas de hoy y el análisis del día entero, aunque el vivo de un canal extra ya no esté en la lista de videos;
- cuando un canal que tenía vivo se queda sin ninguno (por ejemplo, en Rock & Pop, que abre un vivo nuevo por programa), revisa **solo ese canal** cada 1 minuto durante 20 minutos para enganchar enseguida el vivo del programa siguiente; si en 20 minutos no aparece, lo sigue buscando cada 3 minutos con el resto.

El dashboard tiene **tres pantallas** con el mismo encabezado (el link para descargar el CSV y las pestañas **En vivo**, **Programas de hoy** y **Canales**) y, arriba de cada una, una franja de estado: cuándo se actualizó, el error de la API si lo hay y la nota general del chat (cada 5 segundos). Cada pantalla tiene su propia dirección, así que se puede recargar, guardar en favoritos o abrir en otra pestaña: `/`, `/programas` y `/canales`.

#### En vivo (`/`)

Un canal a la vez, con todo su análisis. De arriba hacia abajo:

- **Canal** (se actualiza cada 5 segundos): un botón por cada canal de `-canal`, con o sin vivo ("Radio Rivadavia · en vivo", "Rock & Pop · esperando el próximo vivo"), y uno por cada vivo en curso de `-vivos` que no sea de esos canales. Elegir uno solo cambia lo que se ve: los demás canales se siguen leyendo y guardando, así que al volver a elegirlos están al día. El canal elegido queda en el link (`/?canal=UC...`); un link viejo con `/?vivo=ID` abre el canal de ese vivo. Con vivo dice **EN VIVO**, el programa, desde qué hora está al aire, "2.º vivo de hoy" si el canal ya tuvo otro, cuántos miran y un link para abrirlo en YouTube; sin vivo, "Esperando el próximo vivo" y cómo lo está buscando (cada 1 minuto hasta qué hora, o cada 3). Debajo, el **cartel de continuidad** cuando hoy hubo un cambio ("A las 14:00 cambió el programa y el chat siguió sin cortes", o "A las 14:03 empezó un vivo nuevo y el monitor lo enganchó solo. El vivo de … terminó a las 14:00."; la hora es la de la revisión de videos que lo vio, hasta 3 minutos, o 1 en la búsqueda rápida, después del cambio real), la tira **"Hoy en el canal"** desde las 00:00 (un bloque por programa, más ancho cuanto más duró, que dice si "terminó", está "al aire" o fue un rato "sin vivo") y **cómo sale el canal** según lo que se vio en los últimos 7 días: un vivo de 24 h que cambia de título con cada programa (como Rivadavia), un vivo nuevo por programa (como Rock & Pop) o "todavía no se vio un cambio de programa".
- El selector **"Programa en curso" / "Todo el día del canal"** y el título del análisis. "Programa en curso" usa los mensajes del vivo actual con el programa actual (desde 5 minutos antes de que se viera el título nuevo); "Todo el día del canal", los de todos los vivos del canal desde las 00:00 de hoy. Filtra todo lo que sigue y la elección se mantiene mientras se refresca. Nunca entran mensajes de otro canal. Con el análisis con Claude activo, al lado dice cuándo fue el último resumen. Para un vivo suelto de `-vivos`, el análisis es el de ese vivo en las últimas 24 horas.
- Cinco números: **mensajes por minuto** (los de los últimos 5 minutos, y cuánto por encima o por debajo de lo típico del programa o del día cuando hay al menos 6 franjas de 5 minutos para comparar), **personas que escribieron**, **mirando ahora** (y qué % de ellos escribió en los últimos 5 minutos), **Super Chats** (con el total si son todos de una moneda; nunca se suman monedas distintas) y **preguntas**.
- **Quejas técnicas** por tipo (audio, cortes e imagen, demora) con los últimos ejemplos.
- **Resumen de Claude del programa en curso**, si lo activaste (ver abajo). Con "Todo el día del canal" remite a Programas de hoy, donde está el resumen de cada programa.
- **Preguntas para producción** (sin las muy cortas ni las repetidas; primero las de más likes y las más nuevas) y **Categorías** del alcance elegido.
- **Momentos con más participación (para reels)**: las 5 franjas de 5 minutos de las últimas 24 horas en que escribió el mayor % de los que miraban (personas distintas que escribieron sobre los espectadores que informa YouTube en esa franja). Para entrar en el ranking una franja necesita dato de espectadores y al menos 3 personas escribiendo, y entre el inicio de dos momentos del top tiene que haber al menos 15 minutos, para que una misma charla no ocupe varios puestos; si hay menos de 5 que cumplan, se muestran las que haya y se explica por qué. Con dos vivos del mismo canal a la vez, "cuántos miraban" es la suma de los dos y cada momento se linkea en el vivo que tuvo más mensajes en esa franja (si ese vivo ya no está en la lista de videos, el link es al video y se dice la hora a buscar). De cada uno muestra la hora local, el programa, cuántos miraban, cuántas personas escribieron y el % (los tres números cierran entre sí), y debajo cómo ubicarlo (como la sección muestra vivos en curso, lo normal es un link al vivo y cuánto retroceder en el reproductor, porque YouTube ignora el segundo del link mientras el vivo está en el aire; si el vivo ya terminó, un link en ese segundo cuando empezó hace 12 horas o menos y, si no, cuánto retroceder; si pasaron más de 12 horas, que se use la grabación propia), los temas y dos mensajes de ejemplo (sin spam ni insultos). Arriba hay un gráfico del % de cada franja con los 5 del top numerados y una línea punteada con la participación típica del vivo (mediana del % de las franjas con dato). Encima de las barras, un panel de oyentes con su propia escala (desde 0) muestra en escalones cuántos miraban en cada franja (promedio de lo que informa YouTube), para distinguir un % alto con mucha gente de uno con pocos oyentes; la línea se corta donde no hay dato de espectadores y el panel no aparece si no hay ningún dato en las horas graficadas. Al pasar el mouse por una franja, en el panel o en las barras, se ven los mensajes, las personas, cuántos miraban y el %. El gráfico muestra las últimas 6 horas (con los botones «Últimas 6 h» y «Últimas 24 h» se cambia, y la elección se mantiene mientras se refresca), sin el tramo del principio que todavía no tiene datos; si un momento del top queda fuera, se avisa debajo; si YouTube no informa cuántos miran, el gráfico muestra personas y no hay ranking. La tabla entra entera en el ancho de la pantalla, también en el celular. Es una aproximación: las personas se cuentan por nombre visible y los espectadores son los que YouTube informa "mirando ahora".
- **Chat en vivo** (cada 2 segundos), con los mensajes de todos los vivos del canal de hoy. Si el canal cambia de vivo, la sección pasa sola al vivo nuevo del mismo canal, sin saltar a otro canal. Arriba dice si llega en tiempo real y cada cuánto se le consulta a YouTube como mínimo, y debajo un **cartel** con el detalle: en verde, si la conexión de tiempo real está abierta (desde hace cuánto); en amarillo, si YouTube la cortó (a los cuántos segundos) y cuándo se vuelve a conectar, o si ese chat se lee con consultas comunes; también cada cuánto le toca a cada vivo si se turnan, el promedio real de las últimas consultas y hace cuánto llegó el último dato. Si YouTube corta seguido las conexiones, los mensajes llegan de a tandas al reconectar: no se pierden, pero no es tiempo real. Cada vez que YouTube cierra una conexión queda anotado en el log (`YouTube cerró la conexión de tiempo real a los ...`).
- **Super Chats y Super Stickers**: cuántos pagos hubo y cuánto suman por moneda, lo mismo por programa, y los últimos 10 (hora, quién, tipo, el monto tal como lo muestra YouTube y el comentario o la descripción del sticker). Los montos son **brutos**: lo que pagó cada persona, en su moneda y **sin convertir** (no se suman pesos con dólares); no es lo que cobra el canal, que depende del reparto de YouTube y solo se ve con acceso de administrador. Sin pagos en las últimas 24 horas, la tarjeta lo dice. En el chat en vivo, cada Super Chat o Super Sticker lleva una etiqueta con el tipo y el monto. Los datos vienen en las mismas respuestas del chat que ya se leían, así que no gastan cuota extra; se cuentan desde que se actualizó el monitor (los Super Chats de antes quedaron guardados como mensajes comunes, sin monto, y los Super Stickers sin texto no se guardaban).
- **Todo el monitor · últimas 24 h**: las palabras más repetidas y quiénes más escriben de todos los mensajes de todos los canales, también los comentarios de los videos. Las categorías están una sola vez, en la tarjeta **Categorías** del alcance elegido.

Todo eso sale de reglas simples, sin IA ni costo, salvo el resumen de Claude si lo activás. Los borradores de respuesta se los pedís a Claude con el CSV (sección 4).

#### Programas de hoy (`/programas`)

Una tabla con cada programa de cada canal: canal, horario ("14:00–" si está al aire, "10:00–14:00" si terminó), mensajes, personas, preguntas y Super Chats, y si ya tiene el resumen de Claude. Los programas salen de `datos.vivos.jsonl` (un programa nuevo cada vez que cambia el título del vivo o empieza otro vivo), con los mismos mensajes que usa "Programa en curso". Se filtra por canal y por día: **Hoy** (se actualiza cada minuto), **Ayer** o **Últimos 7 días** (estos dos no se actualizan solos y dicen a qué hora se armaron); un programa que cruza la medianoche aparece una vez. Al tocar un programa se ve su detalle: canal, horario, por qué terminó (cambió el título o terminó el vivo), cuántos mensajes y personas, un botón **CSV de este programa** (`/export.csv?tramo=…`, con el nombre del programa en el archivo) y otro para abrirlo en YouTube; el **resumen del chat**, los **temas**, el **sentimiento** y las **preguntas** del resumen de Claude si lo tiene (si no, temas y preguntas por reglas), y los Super Chats del programa por moneda. La dirección cambia con lo que elegís (`/programas?dia=ayer&canal=UC…&tramo=…`).

#### Canales (`/canales`)

Desde la PC donde corre el monitor se **editan los canales**, sin reiniciar:

- **Agregar canal**: pegá el link, el @handle o el ID (`UC…`). Queda "Resolviendo…" hasta que el monitor lo busca en YouTube en la próxima revisión (1 unidad); si no existe, la tarjeta dice "no se encontró el canal". Arranca leyendo el chat cada 5 minutos en el pico y cada 10 fuera, para no pasarse de la cuota sin querer. Hasta 5 canales; el primero es el principal (de ese se bajan también los últimos videos y sus comentarios).
- **Intervalos de cada canal**: "Actualizar el chat cada" (en el pico: 30 s, 1, 1 min 30 s, 2, 3 o 5 min) y "Fuera del pico, cada" (1, 2, 3, 5 o 10 min). Al cambiar un valor, la barra de cuota y la cuota de ese chat se recalculan **antes de guardar** ("Entra en el día…" o "No entra: faltan N unidades"). **Guardar cambios** los aplica; **Descartar** vuelve a lo guardado.
- **Pausar / Reanudar**: deja de leer el chat de ese canal (no gasta cuota de chat) pero sigue revisando sus vivos, así no se pierde la continuidad. Se aplica al tocarlo; En vivo lo dice en el cartel del chat.
- **Quitar**: el canal deja de seguirse y de tener botón en En vivo. Lo guardado queda hasta que venza (30 días) y sus programas siguen en Programas de hoy (aunque se reinicie el monitor: salen de `datos.vivos.jsonl`, marcados "no está en Canales"; los vivos sueltos de `-vivos` aparecen como "vivo suelto"). No se puede quitar el último canal; si se quita el principal, pasa a serlo el siguiente.

Lo que elegís se guarda en `datos.canales.json` (solo lo que escribiste: canales, intervalos y pausas; nada que venga de YouTube). Si ese archivo existe, **manda el archivo**: `-canal`, `-chat-seg-pico` y `-chat-seg` pasan a ser solo los valores iniciales, y si además los pasás a mano el monitor lo avisa al arrancar y en la pantalla. Si el archivo se rompe, el monitor no arranca y dice qué hacer (corregirlo o borrarlo para volver a `-canal`). Desde otra PC de la red la pantalla se ve igual pero sin controles: "Para cambiar la configuración, abrí el dashboard en la PC del monitor".

Además muestra una tarjeta por canal (nombre, lo que pusiste en `-canal`, si es el principal o un extra, si está en vivo y desde cuándo, o desde cuándo no tiene vivo y cómo lo busca, cómo sale, las horas en vivo de cada uno de los últimos 7 días y los intervalos del chat), qué pasa cuando termina un programa, la **cuota de YouTube estimada por día** con esta configuración (un chat por canal; una barra sobre las 10.000 unidades con la marca donde se pausa el chat y la del tope, lo usado hoy, y si entra en el día o cuántas unidades faltan; las horas en vivo y los cortes por día salen de `datos.vivos.jsonl` y, con menos de 24 horas de historial, se suponen 24 horas en vivo) y el estado del **análisis con Claude** (si la clave está cargada, sin mostrarla nunca; modelo, tope, lo gastado hoy, el último análisis y el último aviso).

### Análisis con Claude desde el dashboard

Si lo activás, el monitor le pide a Claude un análisis de **cada programa de cada canal** de `-canal` cuando termina: un resumen de qué se habló en el chat, el sentimiento (positivo, neutro, negativo y el tono), los temas, las preguntas que valdría responder al aire agrupadas por tema (con cuántos mensajes y cuántas personas las hicieron y hasta 3 ejemplos) y las quejas. En En vivo aparece la tarjeta **"Resumen de Claude del programa en curso"**: el del programa al aire, con un botón **"Analizar ahora"** que muestra el costo estimado y pide un análisis parcial (hasta ese momento). Las **preguntas agrupadas** y las **quejas** del resumen vienen plegadas: se abren y se cierran con la flecha, y quedan como las dejaste aunque la tarjeta se refresque. El resumen de cada programa terminado se ve en **Programas de hoy**, y el estado (clave, modelo, tope, gasto) en **Canales**. El análisis parcial se reemplaza por el final cuando termina el programa. Al arrancar, analiza los programas de hoy que ya terminaron y no tenían análisis. El botón se puede tocar una vez cada 10 minutos por canal y solo desde la PC del monitor (desde otra PC de la red, la tarjeta lo dice y no lo muestra). Lo arma Claude: puede equivocarse, revisalo antes de usarlo al aire.

Para activarlo:

1. Creá una clave en la consola de Anthropic (https://console.anthropic.com) y ponela en el archivo de la clave, al lado de la de YouTube: `CLAUDE_API_KEY=...` (o como variable de entorno). Conviene ponerle también un límite de gasto en la consola, como segunda barrera.
2. Arrancá el dashboard con un tope de gasto diario en dólares: `go run . -dashboard :8080 -claude-usd 2`. Con `-claude-usd 0` (el valor por defecto) o sin la clave, todo sigue como siempre y la tarjeta dice que está apagado.

Opciones: `-claude-usd` (tope de gasto diario en dólares; `0` = apagado) y `-claude-modelo` (por defecto `claude-sonnet-5-5`; también `claude-haiku-5-5`, unas 20 veces más barato, o `claude-opus-5-5`).

Qué se manda a Anthropic y qué no: solo el **texto** y la hora de los mensajes de **chat** del programa, cada uno cortado en 300 caracteres y como mucho 1.500 por pedido (si hay más, una muestra pareja), más el nombre del canal y del programa. **No** se mandan los nombres de quienes escriben, los IDs, los likes ni los montos de los Super Chats, y se saltean los mensajes que el clasificador marca como spam (los que tienen links o teléfonos). Las cantidades de mensajes y de personas de cada grupo se cuentan en el monitor, no las da Claude. Los comentarios de los videos no se analizan todavía.

Costo (precios de Anthropic al 2026-10-06, por millón de tokens: Sonnet 5.5 US$ 2 de entrada y 10 de salida; Haiku 5.5 US$ 0,10 y 0,50, o 0,50 y 2,50 por encima de 100.000 de entrada; Opus 5.5 US$ 4 y 20): un programa de ~600 mensajes cuesta ~US$ 0,06 con Sonnet. Con Rivadavia (~7.200 mensajes por día, ~8 programas), Rock & Pop (~2.700, ~6 programas) y ~10 análisis con el botón son ~US$ 1,33 por día con Sonnet (~US$ 40 por mes), ~US$ 0,07 con Haiku y ~US$ 2,65 con Opus. Es una estimación: cada análisis guarda los tokens y el costo reales para compararlos con la consola de Anthropic la primera semana. El tope sugerido es `-claude-usd 2`. Antes de cada pedido se calcula lo más que podría costar (la respuesta completa) y, si con eso se pasaría el tope, no se manda: la tarjeta dice "Se llegó al tope de gasto de Claude de hoy" y se retoma a las 00:00 (hora local). La tarjeta muestra lo gastado en el día.

Archivos: los análisis se guardan en `datos.analisis.jsonl` (textos de Claude y los IDs de los mensajes de ejemplo, no sus textos) con la misma retención de 30 días, y el gasto del día en `datos.claude.json`. Ninguno cambia `datos.jsonl` ni el CSV. Si Claude rechaza la clave o el modelo, el análisis se apaga hasta reiniciar el monitor y la tarjeta dice qué revisar; si Claude está saturado, se reintenta dos veces (a los 5 y 15 minutos). Si un pedido salió pero no volvió cuánto costó (por ejemplo, se cortó la conexión), se cuenta lo más que pudo costar, así el tope no se pasa.

El chat en vivo del dashboard se actualiza cada 2 segundos; la franja de estado, la tarjeta Canal y la pantalla Canales, cada 5 segundos; el análisis de En vivo, cada 30, y Programas de hoy (con "Hoy"), cada minuto. Arriba hay un link para descargar el CSV.

Cuota en este modo (por día): ~1.440 unidades por revisar videos y vivos (más ~480 por cada canal extra; agregar uno cuesta además 1 unidad al buscarlo), ~500 a 1.000 por comentarios y el chat de **cada canal** con sus intervalos. Cada conexión de chat en tiempo real cuesta 5 unidades al abrirse y no gasta más mientras queda abierta: si YouTube la mantiene horas, el chat casi no gasta. Cuánto dura cada conexión lo decide YouTube y no está documentado, así que entre dos conexiones nuevas de un canal pasa como mínimo el mismo intervalo que entre dos lecturas comunes (el del canal): en el peor caso, si YouTube cortara cada conexión enseguida, gasta lo mismo que leyendo por turnos. Revisá el consumo real en Google Cloud Console los primeros días. La búsqueda rápida del próximo vivo cuesta 2 unidades por minuto mientras dura: a lo sumo 40 cada vez que un canal se queda sin vivo (con 6 programas por día en un canal, unas 240), y mucho menos si el vivo siguiente aparece enseguida; con el tope de cuota alcanzado no se hace. Con un intervalo de 1 minuto en el pico y 3 fuera (los valores por defecto con un solo canal), el vivo de 24 horas son ~5.000 de chat (300 por hora en el horario pico, 100 por hora el resto: 3.900 + 1.100), sean uno o varios vivos de ese canal. La cuota de un canal es 5 × (horas en vivo en el pico × lecturas por hora + horas fuera × lecturas por hora): por ejemplo, con Rivadavia cada 2 min en el pico y cada 5 fuera, 24 h en vivo (13 h × 30 + 11 h × 12 = 522 lecturas, 2.610) y Rock & Pop cada 3 min y cada 5, 18 h en vivo (13 h × 20 + 5 h × 12 = 320, 1.600), el día da 1.920 de videos + 1.000 de comentarios + 2.610 + 1.600 = 7.130 y entra con 370 de margen. La pantalla Canales hace esta cuenta con las horas en vivo de los últimos 7 días antes de guardar. Cada vivo suelto de `-vivos` que no es de un canal configurado suma su propio chat con `-chat-seg-pico` / `-chat-seg`: con 60 s y 180 s y 24 h en vivo, ~5.000 por día (en la barra, "Vivos sueltos de -vivos"); con un vivo así hay que subir esos flags o los intervalos de los canales para que entre. Con un solo canal y el tope por defecto (`-cuota 9000`) alcanza para leer el chat todo el día: 1.440 + 1.000 + 5.000 = 7.440, justo debajo de las 7.500 hasta las que se lee el chat (`-cuota` menos 1.500 de reserva). Con un canal extra que abre un vivo por programa (como Rock & Pop) arrancando con `-canal` y sin configuración guardada, cada canal lee cada 2 minutos en el pico y cada 6 fuera, así que el chat de los dos suma lo mismo que antes (~5.000 si los dos estuvieran 24 h en vivo) y se agregan ~480 de videos y hasta ~240 de búsqueda rápida: 7.440 + 480 + 240 = 8.160, unas 660 de más, y el chat se pausa unas horas antes de que se reinicie la cuota (el corte exacto depende de cuánto gasten después videos y comentarios). Para que llegue al día completo, subí algún intervalo en Canales mirando la barra (por ejemplo, el del ejemplo de arriba entra con 370 de margen) o pausá el chat de un canal. Si se acelera el chat (bajar `-chat-seg-pico` o ampliar `-pico`), se pausa antes de que se reinicie la cuota; como eso pasa a las 04:00 o 05:00 de Argentina, lo primero que se pierde es la madrugada. Por ejemplo, con `-chat-seg-pico 45` el chat se pausa cerca de las 22 h. Cuando se acerca al tope, el chat se pausa solo y los comentarios siguen hasta el límite. El contador de cuota del dashboard se guarda en `datos.cuota.json`, así que sigue contando aunque lo reinicies; solo cuenta lo que gasta el dashboard (no los informes con `go run .`), y la cuota real la ves en Google Cloud Console.

### Tiempo real continuo para un vivo (`-tiempo-real`)

YouTube corta cada conexión de tiempo real a los ~10 segundos (medido en octubre de 2026 con C5N y Rivadavia). Con los turnos de arriba, el chat llega de a tandas cada 1 o 2 minutos. Para ver un vivo realmente en tiempo real, pasalo en `-tiempo-real` (links o IDs separados por coma): ese vivo se reconecta 1 segundo después de cada corte, fuera de los turnos, y los demás siguen por turnos como siempre.

```bash
go run . -dashboard :8080 -canal UCLAj5TzCrNbqe3LosSKHLcQ,@luzutv -vivos https://www.youtube.com/watch?v=K60NNYn5sqA -tiempo-real https://www.youtube.com/watch?v=CvuuhoLcDcw
```

Cuesta caro: 5 unidades cada ~10 s, unas **1.800 por hora por vivo**. Con lo que queda para el chat después de videos y comentarios (~5.000 por día) alcanza para unas 2 h 45 min de tiempo real continuo; cuando se llega al tope del chat (`-cuota` menos 1.500 de reserva), se pausa el chat de **todos** los vivos, también el de Rivadavia, hasta que se reinicia la cuota (04:00 o 05:00 de Argentina). El cartel del vivo muestra cuántas unidades quedan y para cuánto tiempo alcanzan. Usalo para un programa puntual y después reinicialo sin `-tiempo-real`.

Opciones: `-tiempo-real` (ver arriba), `-pico` (horario pico en hora local, por defecto `7-20`; `-pico ""` lo desactiva), `-zona` (también fija la hora del horario pico), `-chat-seg-pico` (segundos entre lecturas del chat en el horario pico, o mínimo entre conexiones de tiempo real), `-chat-seg` (lo mismo el resto del día), `-comentarios-min`, `-cuota`, `-videos`, `-canal`, `-vivos`, `-claude-usd` y `-claude-modelo` (análisis con Claude, ver arriba). `-canal`, `-chat-seg-pico` y `-chat-seg` son valores iniciales: si existe `datos.canales.json`, valen los canales e intervalos guardados en la pantalla Canales.

Para ver los cambios de una rama sin tocar los datos reales ni gastar cuota, la vista previa (`go run ./tools/preview -datos datos.jsonl`, ver AGENTS.md) copia `datos.jsonl`, `datos.cuota.json`, `datos.espectadores.jsonl`, `datos.vivos.jsonl` y `datos.analisis.jsonl` a una carpeta privada temporal que se borra al cortarla (`go run ./tools/preview -cortar`); no copia `datos.claude.json` (el gasto de Claude arranca en 0) ni `datos.canales.json` (la vista previa usa su propio `-canal`).

Para dejarlo corriendo siempre: una PC o servidor de la oficina que quede prendido. En Windows, una tarea programada "Al iniciar" que ejecute `yt-monitor.exe -dashboard :8080` (se compila con `go build`). En Linux, un servicio de systemd. Solo se ve desde esa máquina; para verlo desde otras PCs de la red, usar `-dashboard 0.0.0.0:8080` (no tiene usuario ni contraseña, así que no exponerlo a internet).

## 4. Analizarlo con Claude

El CSV (del informe o del botón del dashboard) tiene las columnas `fuente, fecha, video, programa, autor, categoria, likes, respuestas, texto` y, al final, seis más para los Super Chats y Super Stickers: `pago` (`superchat` o `supersticker`), `monto` (con punto decimal, por ejemplo `5.5`), `moneda` (código como `ARS` o `USD`), `monto_youtube` (el monto como lo muestra YouTube), `nivel` y `sticker` (la descripción del sticker). En los mensajes comunes esas seis van vacías. El monto es bruto, en la moneda de quien pagó y sin convertir. El informe `.md` suma una sección "Super Chats y Super Stickers" con los totales por moneda y por programa. Como el CSV muestra cuánto pagó cada persona con su nombre (es público en el chat), tratalo igual que el resto de los datos: no lo subas a ningún lado que no sea el chat con Claude. Si activás el análisis con Claude del dashboard, el texto del chat (sin nombres) también va a la API de Anthropic.

Subir el CSV a un chat o proyecto de Claude con este pedido:

> Este CSV tiene comentarios y mensajes de chat del canal de YouTube de Radio Rivadavia. Armame: 1) los 5 temas de los que más habla la audiencia, 2) las preguntas que valdría la pena responder al aire o en comentarios, con un borrador de respuesta breve y respetuoso para cada una, 3) quejas técnicas (audio, cortes), 4) usuarios que hacen spam o insultan de forma repetida, 5) qué filtros de palabras conviene cargar en YouTube Studio.

## 5. Automatizar el informe diario

- Simple: correrlo una vez por día a mano o con `cron` en cualquier PC o servidor (`0 9 * * * cd /ruta && YT_API_KEY=... go run .`).
- Siguiente paso: una rutina programada de Claude Code que lo corra todos los días y deje el análisis listo.

## Próximos pasos del piloto

1. Correrlo una semana para medir volumen y tipo de mensajes.
2. Con esos datos, decidir si se suma Nightbot (moderación y comandos del chat) y qué palabras bloquear. Para eso hace falta que un administrador del canal agregue el bot como moderador.
3. Si hay muchas preguntas sin respuesta, sumar borradores de respuesta con aprobación (necesita OAuth del canal).
