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

Opciones: `-canal` (uno o varios canales, ver abajo), `-videos`, `-horas`, `-chat-min`, `-salida`, `-vivos`.

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

En el modo dashboard, el chat de cada vivo llega **en tiempo real** (ver la sección 3). Si YouTube no deja usar el tiempo real en algún chat, ese chat se lee **por turnos**, como en el modo informe: el intervalo (`-chat-seg-pico`, `-chat-seg`) es entre dos lecturas cualesquiera, así que con 2 vivos cada uno se lee cada 2 minutos en el horario pico y cada 6 el resto; con 3, cada 3 y cada 9. El gasto de cuota del chat no cambia con la cantidad de vivos y no se pierden mensajes, solo llegan con más atraso. En el modo informe los `-chat-min` minutos se reparten entre los vivos.

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
- mientras hay un vivo, recibe su chat **en tiempo real**: abre una conexión de streaming con YouTube (`liveChat/messages/stream`) que queda abierta y por la que llega cada mensaje apenas se publica. Con varios vivos abre una por vivo. Si la conexión se corta, la vuelve a abrir desde el último mensaje recibido, así que no se pierde nada. Si YouTube no permite el streaming en algún chat, ese chat se lee como antes: cada 60 segundos de 7 a 20 h (horario pico) y cada 3 minutos el resto del día, por turnos si son varios. El aviso del chat en el dashboard dice cuál de los dos modos se está usando;
- guarda todo en `datos.jsonl`, así no se pierde si lo reiniciás, y borra lo que tiene más de 30 días (lo piden las políticas de YouTube);
- en cada revisión de videos anota cuántos miran cada vivo en `datos.espectadores.jsonl` (sale del mismo pedido, no gasta cuota extra), con la misma retención de 30 días. Si el canal oculta el contador de espectadores, no se anota nada y el dashboard lo muestra como "sin dato".

Arriba del chat de cada vivo hay un **cartel** que dice cómo se lo está consultando: en verde, si la conexión de tiempo real está abierta (desde hace cuánto); en amarillo, si YouTube la cortó (a los cuántos segundos) y cuándo se vuelve a conectar, o si ese chat se lee con consultas comunes. También muestra el mínimo entre dos consultas al chat de YouTube, cada cuánto le toca a cada vivo si se turnan, el promedio real de las últimas consultas de ese vivo y hace cuánto llegó el último dato. Si YouTube corta seguido las conexiones, los mensajes llegan de a tandas al reconectar: no se pierden, pero no es tiempo real. Cada vez que YouTube cierra una conexión queda anotado en el log (`YouTube cerró la conexión de tiempo real a los ...`).

Arriba de todo está el **chat en vivo por programa**: un botón por cada vivo en curso ("RADIO RIVADAVIA en VIVO", "ROCK & POP en VIVO"). Al tocar uno se ve el chat de ese vivo, actualizándose solo, y los demás quedan en pausa en pantalla: el monitor los sigue leyendo y guardando, así que al volver a elegirlos están al día. El vivo elegido queda en el link (`/?vivo=ID`), así que se puede guardar en favoritos. Debajo, para el vivo elegido y con los mensajes de las últimas 24 horas:

1. los 5 temas de los que más se habla (palabras o pares de palabras, como "dólar blue");
2. preguntas para responder (sin las muy cortas ni las repetidas; primero las de más likes y las más nuevas);
3. quejas técnicas por tipo (audio, cortes e imagen, demora) con los últimos ejemplos;
4. usuarios que mandaron spam o insultos más de una vez;
5. filtros para YouTube Studio: las palabras de spam e insultos que aparecieron, listas para copiar en Palabras bloqueadas, y otras para revisar que casi solo aparecen en esos mensajes;
6. momentos con más participación (para reels): las 5 franjas de 5 minutos de las últimas 24 horas en que escribió el mayor % de los que miraban (personas distintas que escribieron sobre los espectadores que informa YouTube en esa franja). Para entrar en el ranking una franja necesita dato de espectadores y al menos 3 personas escribiendo, y entre el inicio de dos momentos del top tiene que haber al menos 15 minutos, para que una misma charla no ocupe varios puestos; si hay menos de 5 que cumplan, se muestran las que haya y se explica por qué. De cada uno muestra la hora local, el programa, cuántos miraban, cuántas personas escribieron y el % (los tres números cierran entre sí), y debajo cómo ubicarlo (un link a YouTube en ese segundo si el vivo empezó hace 12 horas o menos, cuánto retroceder en el reproductor si es un vivo más largo como el de 24 horas, o que se use la grabación propia si pasaron más de 12 horas), los temas y dos mensajes de ejemplo (sin spam ni insultos). Arriba hay un gráfico del % de cada franja con los 5 del top numerados y una línea punteada con la participación típica del vivo (mediana del % de las franjas con dato); si YouTube no informa cuántos miran, el gráfico muestra personas y no hay ranking. La tabla entra entera en el ancho de la pantalla, también en el celular. Es una aproximación: las personas se cuentan por nombre visible y los espectadores son los que YouTube informa "mirando ahora".

Todo sale de reglas simples, sin IA ni costo: es una primera mirada. Los borradores de respuesta y el análisis fino se los pedís a Claude con el CSV (sección 4).

El chat en vivo del dashboard se actualiza cada 2 segundos y el resto, cada 5 segundos: cada vivo en curso con su canal, espectadores y avisos de su chat, totales del día, mensajes cada 5 minutos, categorías, últimos mensajes con filtros y buscador (en el chat muestra el programa), palabras más repetidas, chat por programa, quiénes más escriben, videos recientes y un botón para descargar el CSV.

Cuota en este modo (por día): ~1.440 unidades por revisar videos y vivos (más ~480 por cada canal extra en `-canal`), ~500 a 1.000 por comentarios y el chat. Cada conexión de chat en tiempo real cuesta 5 unidades al abrirse y no gasta más mientras queda abierta: si YouTube la mantiene horas, el chat casi no gasta. Cuánto dura cada conexión lo decide YouTube y no está documentado, así que entre dos conexiones nuevas pasa como mínimo el mismo intervalo que entre dos lecturas comunes (`-chat-seg-pico`, `-chat-seg`): en el peor caso, si YouTube cortara cada conexión enseguida, gasta lo mismo que leyendo por turnos. Revisá el consumo real en Google Cloud Console los primeros días. Leyendo por turnos, con el vivo de 24 horas son ~5.000 de chat (300 por hora en el horario pico, 100 por hora el resto: 3.900 + 1.100), sean uno o varios vivos. Con un solo canal y el tope por defecto (`-cuota 9000`) alcanza para leer el chat todo el día. Con un canal extra se pasa por unas 400 unidades y el chat se pausa cerca de la medianoche; para que llegue al día completo, espaciá el chat fuera del horario pico con `-chat-seg 300`. Si se acelera el chat (bajar `-chat-seg-pico` o ampliar `-pico`), se pausa antes de que se reinicie la cuota; como eso pasa a las 04:00 o 05:00 de Argentina, lo primero que se pierde es la madrugada. Por ejemplo, con `-chat-seg-pico 45` el chat se pausa cerca de las 22 h. Cuando se acerca al tope, el chat se pausa solo y los comentarios siguen hasta el límite. El contador de cuota del dashboard se guarda en `datos.cuota.json`, así que sigue contando aunque lo reinicies; solo cuenta lo que gasta el dashboard (no los informes con `go run .`), y la cuota real la ves en Google Cloud Console.

### Tiempo real continuo para un vivo (`-tiempo-real`)

YouTube corta cada conexión de tiempo real a los ~10 segundos (medido en octubre de 2026 con C5N y Rivadavia). Con los turnos de arriba, el chat llega de a tandas cada 1 o 2 minutos. Para ver un vivo realmente en tiempo real, pasalo en `-tiempo-real` (links o IDs separados por coma): ese vivo se reconecta 1 segundo después de cada corte, fuera de los turnos, y los demás siguen por turnos como siempre.

```bash
go run . -dashboard :8080 -canal UCLAj5TzCrNbqe3LosSKHLcQ,@luzutv -vivos https://www.youtube.com/watch?v=K60NNYn5sqA -tiempo-real https://www.youtube.com/watch?v=CvuuhoLcDcw
```

Cuesta caro: 5 unidades cada ~10 s, unas **1.800 por hora por vivo**. Con lo que queda para el chat después de videos y comentarios (~5.000 por día) alcanza para unas 2 h 45 min de tiempo real continuo; cuando se llega al tope del chat (`-cuota` menos 1.500 de reserva), se pausa el chat de **todos** los vivos, también el de Rivadavia, hasta que se reinicia la cuota (04:00 o 05:00 de Argentina). El cartel del vivo muestra cuántas unidades quedan y para cuánto tiempo alcanzan. Usalo para un programa puntual y después reinicialo sin `-tiempo-real`.

Opciones: `-tiempo-real` (ver arriba), `-pico` (horario pico en hora local, por defecto `7-20`; `-pico ""` lo desactiva), `-chat-seg-pico` (segundos entre lecturas del chat en el horario pico, o mínimo entre conexiones de tiempo real), `-chat-seg` (lo mismo el resto del día), `-comentarios-min`, `-cuota`, `-videos`, `-canal`, `-vivos`.

Para dejarlo corriendo siempre: una PC o servidor de la oficina que quede prendido. En Windows, una tarea programada "Al iniciar" que ejecute `yt-monitor.exe -dashboard :8080` (se compila con `go build`). En Linux, un servicio de systemd. Solo se ve desde esa máquina; para verlo desde otras PCs de la red, usar `-dashboard 0.0.0.0:8080` (no tiene usuario ni contraseña, así que no exponerlo a internet).

## 4. Analizarlo con Claude

Subir el CSV a un chat o proyecto de Claude con este pedido:

> Este CSV tiene comentarios y mensajes de chat del canal de YouTube de Radio Rivadavia. Armame: 1) los 5 temas de los que más habla la audiencia, 2) las preguntas que valdría la pena responder al aire o en comentarios, con un borrador de respuesta breve y respetuoso para cada una, 3) quejas técnicas (audio, cortes), 4) usuarios que hacen spam o insultan de forma repetida, 5) qué filtros de palabras conviene cargar en YouTube Studio.

## 5. Automatizar el informe diario

- Simple: correrlo una vez por día a mano o con `cron` en cualquier PC o servidor (`0 9 * * * cd /ruta && YT_API_KEY=... go run .`).
- Siguiente paso: una rutina programada de Claude Code que lo corra todos los días y deje el análisis listo.

## Próximos pasos del piloto

1. Correrlo una semana para medir volumen y tipo de mensajes.
2. Con esos datos, decidir si se suma Nightbot (moderación y comandos del chat) y qué palabras bloquear. Para eso hace falta que un administrador del canal agregue el bot como moderador.
3. Si hay muchas preguntas sin respuesta, sumar borradores de respuesta con aprobación (necesita OAuth del canal).
