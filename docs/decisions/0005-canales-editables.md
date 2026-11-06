# ADR 0005: la configuración de canales se edita desde el dashboard y el worker la toma en caliente

- **Estado:** aceptada
- **Fecha:** 2026-11-06

## Contexto

Los canales salían de `-canal` y el intervalo del chat era uno solo para todos los vivos
(`-chat-seg-pico`, `-chat-seg`, por turnos). La persona pidió agregar, pausar y quitar
canales desde la pantalla Canales y elegir cada cuánto se lee el chat de cada uno, sin
reiniciar y viendo antes cuánta cuota gasta (specs `2026-11-05-canales-continuos.md`,
etapa 2, y `2026-11-05-canales-editables-claude-vivo.md`, F2). El dashboard no tiene
usuarios, puede escucharse en la red y el cliente `YouTube` no es concurrente; la cuota
diaria es compartida.

## Decisión

- La configuración vive en `datos.canales.json` (al lado de `datos.jsonl`), con solo lo que
  escribió la persona: `{"Channels":[{"Input","ChatPeakSec","ChatOffSec","Paused"}]}`. El
  nombre y el ID que devuelve YouTube quedan en memoria (`ChannelStatus`), así el archivo no
  tiene datos de la API.
- `Settings` (`channels.go`) la guarda en memoria con su propio mutex y una `version`.
  `Update` valida (1 a 5 canales, `ParseChannel`, sin repetidos, intervalos múltiplos de
  30 s entre 30 s y 10 min, salvo los que no cambiaron), escribe con tmp + `os.Rename` y
  recién ahí cambia la configuración.
- Sin archivo se arranca con `-canal` y `-chat-seg*` × N (N = canales): el mismo gasto que
  con el intervalo único. Con archivo, gana el archivo y los flags pasados a mano se avisan
  (`aviso:` en el log y cartel en Canales). Un archivo ilegible frena el arranque y no se pisa.
- El worker compara `version` en cada tick: resuelve los canales nuevos con `spend`
  (1 unidad; si YouTube no lo encuentra, lo anota y no insiste), corta los streaming de los
  quitados (y si se quita el principal, pasa a serlo el siguiente) y corta el chat de los
  pausados, que se siguen revisando. `nextChat` elige por grupo (un canal con su intervalo,
  o `""` para los vivos sueltos de `-vivos`, con los flags): el canal más atrasado y, dentro
  del canal, el vivo que hace más que no se lee; una acción de chat por vuelta, así nunca hay
  dos lecturas comunes en paralelo.
- Los handlers solo llaman a `Settings.Update` y leen `Settings.Get` y el `Store`; nunca a
  YouTube. Los `POST` (`/canales`, `/canales/agregar`, `/canales/pausar`, `/canales/quitar`)
  van detrás de `sameOrigin` y `localRequest` (`guard.go`) con un cuerpo de 16 KB como mucho.
  Pausar y Quitar son inmediatos; los intervalos se aplican con "Guardar cambios", y
  `GET /parcial/canales/cuota` recalcula la cuota estimada con el formulario sin escribir.

## Consecuencias

- La persona cambia canales e intervalos sin tocar la línea de comandos ni reiniciar, y ve
  la cuota antes de guardar. Cada canal suma su propio chat: intervalos cortos en varios
  canales pueden no entrar en el día (la barra lo dice; el tope `-cuota` sigue frenando).
- El dashboard deja de ser de solo lectura en un punto más: solo desde la PC del monitor.
- Los canales quitados siguen en Programas de hoy, también después de reiniciar: salen de las
  muestras de `datos.vivos.jsonl` (ID y nombre del canal), sin guardar nada nuevo, hasta que
  vence la retención de 30 días.
- Los vivos sueltos de `-vivos` que no son de un canal configurado leen su chat aparte, con
  `-chat-seg*`, y suman su propio chat; la barra de Canales lo muestra.
- Descartadas: mandar la configuración al worker por un canal de Go (el handler igual
  necesita leerla y bloquearía durante una lectura); resolver el `@handle` en el handler
  (usaría el cliente desde otra goroutine y gastaría fuera de `spend`); "Pausar" que espera a
  "Guardar" (necesita un borrador del lado del servidor, sin JS propio); un token CSRF por
  arranque (no agrega nada a `sameOrigin` + loopback); seguir con un intervalo global
  repartido entre canales (la persona quiere elegirlo por canal).
