# Spec: el worker no se puede parar y los tests lo dejan vivo

- **Estado:** aprobada
- **Tipo:** bug
- **Rama:** `fix/worker-sin-parada`
- **Origen:** CI del PR #2 de `julian-barbieri/chat-analytics`, job "Tests repetidos (intermitencias)" (`go test -count=5 ./...`): `TestWorkerStreamsChat` falla con `TempDir RemoveAll cleanup: ... directory not empty`. Localmente falla ~1 de cada 8 corridas.
- **Aprobada por / fecha:** Julian Barbieri, 2026-10-30. Decisiones: al cancelar, los
  eventos de streaming que siguen llegando **se procesan con `handle`** (no se pierden mensajes
  ni cuota ya respondida); `AddUnits(0)` queda fuera de alcance; `-race -count=5 -run TestWorker`
  queda en los criterios de esta spec, sin sumarlo al CI.
- **Base:** `chore/software-factory-v2` (PR #2), porque `main` todavía no tiene la factory.

## Contexto

`RunWorker` (`worker.go:129`) es un bucle infinito (`for` de `worker.go:218` a `worker.go:397`) sin
ninguna forma de pararse: no recibe contexto ni canal de parada y el único `select`
(`worker.go:392-396`) espera solo `events` y el tick. Además abre una goroutine de streaming
por vivo (`openStream`, `worker.go:205-216`) con `context.WithCancel(context.Background())`
(`worker.go:210`): solo se cancelan cuando un vivo termina o cambia de chat
(`worker.go:248-249`, `worker.go:258-259`).

Quién lo usa:

- Producción: `main.go:109`, `go RunWorker(yt, st, WorkerConfig{...})` en modo `-dashboard`;
  tiene que seguir corriendo para siempre.
- Tests: `monitor_test.go:335` (`TestWorkerRealTime`) y `monitor_test.go:627`
  (`TestWorkerStreamsChat`) lo lanzan con `go RunWorker(...)` y terminan sin pararlo.

Cuando el test vuelve, el worker sigue vivo y sigue escribiendo en el `t.TempDir()`:

- `spend` (`worker.go:142-147`) y `handle` (`worker.go:169`) llaman a `Store.AddUnits` en cada
  llamado o evento, incluso con 0 unidades; `AddUnits` (`store.go:134-140`) siempre llama a
  `saveUnits` (`store.go:143-149`), que crea `datos.cuota.json.tmp` y lo renombra.
- `Store.Add` (`store.go:105`) abre `datos.jsonl` con `O_CREATE`.

En `TestWorkerStreamsChat` el propio final del test dispara esas escrituras: `defer close(stop)`
(`monitor_test.go:620`) corta la conexión de streaming abierta, la goroutine de streaming manda el
evento `done` (`worker.go:214`), el worker se despierta, llama a `AddUnits` y, como `ChatEvery`
es 1 ms, hace otra lectura común de `p1` con `spend`. Todo eso coincide con el `RemoveAll` del
cleanup de `t.TempDir()`: si se crea un archivo entre que `RemoveAll` lista el directorio y lo
borra, falla con `directory not empty`. Los workers de corridas anteriores (con `-count=N`) quedan
pidiendo a servidores `httptest` ya cerrados: son las líneas `connection refused` con puertos
distintos.

`TestWorkerRealTime` tiene el mismo patrón y además una **carrera de datos**: el defer de
`monitor_test.go:307` restaura `realTimeGap` mientras el worker que quedó vivo lo lee en
`worker.go:313` y `worker.go:432`. Con `-race` falla de forma determinista cuando se corre solo.

**Reproducción** (Go 1.26.8, linux/amd64, rama `fix/worker-sin-parada` en `8033cb1`):

```bash
go test -count=20 -run '^TestWorkerStreamsChat$' ./...      # 3/20 fallas
go test -count=5 ./...                                       # 1 de 3 corridas falla (TestWorkerStreamsChat)
go test -count=20 -run '^TestWorkerRealTime$' ./...          # 0/20 fallas (sin -race)
go test -race -count=1 -run '^TestWorkerRealTime$' ./...     # 8/8 fallas (DATA RACE)
go test -race -count=3 -run 'TestWorker' ./...               # falla: 3 DATA RACE, 2 FAIL de TestWorkerRealTime
go test -race -count=1 ./...                                 # 0/3 fallas (la suite completa lo enmascara hoy)
```

```
--- FAIL: TestWorkerStreamsChat (1.02s)
    testing.go:1464: TempDir RemoveAll cleanup: unlinkat /tmp/TestWorkerStreamsChat2124897506/001: directory not empty
--- FAIL: TestWorkerStreamsChat (0.05s)
    testing.go:1464: TempDir RemoveAll cleanup: unlinkat /tmp/TestWorkerStreamsChat300942389/001: directory not empty
chat de s1: liveChat/messages/stream: Get "http://127.0.0.1:33785/liveChat/messages/stream?…": dial tcp 127.0.0.1:33785: connect: connection refused
chat de p1: liveChat/messages: Get "http://127.0.0.1:41831/liveChat/messages?…": dial tcp 127.0.0.1:41831: connect: connection refused
```

```
WARNING: DATA RACE
Read at 0x0000011b6b18 by goroutine 10:
  github.com/alphamedia/yt-monitor.RunWorker()
      /home/jbarbieri/bots/worker.go:313
  ...
  github.com/alphamedia/yt-monitor.TestWorkerRealTime.gowrap3()
      /home/jbarbieri/bots/monitor_test.go:335
Previous write at 0x0000011b6b18 by goroutine 8:
  github.com/alphamedia/yt-monitor.TestWorkerRealTime.func1()
      /home/jbarbieri/bots/monitor_test.go:307
Found 1 data race(s)
FAIL	github.com/alphamedia/yt-monitor	4.567s
```

**Causa raíz:** `RunWorker` (`worker.go:129`) no tiene forma de terminar y sus goroutines de
streaming dependen de `context.Background()` (`worker.go:210`); los tests (`monitor_test.go:335`,
`monitor_test.go:627`) no pueden esperarlo, así que el worker sigue escribiendo
`datos.cuota.json`/`datos.jsonl` (`store.go:105`, `store.go:146-147`) durante el cleanup de
`t.TempDir()` y lee `realTimeGap` mientras el test lo restaura. **Verificado** (reproducción
arriba y traza de `-race`). La hipótesis del reporte es correcta.

## Objetivo

Que `RunWorker` se pueda cancelar con un `context.Context`, que al cancelarlo corte también sus
streaming y no vuelva hasta que todas sus goroutines terminen, y que los tests lo paren antes de
cerrar el servidor falso, restaurar variables globales o borrar el `t.TempDir()`. En producción
(`main.go`) el comportamiento no cambia: corre para siempre.

## Criterios de aceptación

- [ ] CA1: existe `TestWorkerStops` que cancela el contexto y comprueba que `RunWorker` vuelve
  en menos de 3 s, que la conexión de streaming abierta se cerró del lado del cliente y que
  después de volver no cambia nada en el directorio de datos ni `Store.Units()` durante 1,5 s
  (más de un tick) — se verifica con `go test -count=1 -run '^TestWorkerStops$' ./...`.
  Antes del arreglo (paso 1 del diseño) tiene que fallar con "RunWorker no volvió"; guardar esa
  salida para el PR.
- [ ] CA2: `TestWorkerRealTime` y `TestWorkerStreamsChat` paran el worker y esperan a que vuelva
  antes de cerrar el servidor, restaurar `realTimeGap` y del cleanup del `TempDir` — se verifica
  con `go test -count=20 -run 'TestWorker' ./...` en verde (0 fallas).
- [ ] CA3: sin carreras en los tests del worker — `go test -race -count=5 -run 'TestWorker' ./...`
  en verde y `go test -race -count=1 -run '^TestWorkerRealTime$' ./...` en verde (hoy falla 8/8).
- [ ] CA4: sin líneas `connection refused` en la salida de los tests del worker (no queda ningún
  worker de una corrida anterior pidiendo a un servidor cerrado) —
  `go test -count=5 -v -run 'TestWorker' ./... 2>&1 | grep -c 'connection refused'` da `0`.
- [ ] CA5: producción igual que hoy: `main.go` llama a `RunWorker(context.Background(), ...)` y
  el worker no se para nunca; ningún otro cambio en `main.go` — se verifica con
  `git diff main -- main.go` (solo esa línea y, si hace falta, el import de `context`).
- [ ] CA6: el consumo de cuota no cambia: `TestWorkerStreamsChat` mantiene su chequeo de unidades
  (`u >= 19 && (u-14)%5 == 0`) y `TestFlowAgainstFakeAPI` su chequeo de `yt.Units` sin tocar —
  se verifica con `go test -count=1 -run '^(TestWorkerStreamsChat|TestFlowAgainstFakeAPI)$' ./...`
  y `git diff main -- youtube.go` vacío.
- [ ] CA7: `go test -count=5 ./...` en verde en tres corridas seguidas (hoy falla 1 de 3).
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde: `gofmt -l .` vacío,
  `go vet ./...`, `go run honnef.co/go/tools/cmd/staticcheck@2026.2.2 ./...`, `go build ./...`,
  `GOOS=windows GOARCH=amd64 go build -o /dev/null .`, `go test -race -count=1 ./...`,
  `go test -count=5 ./...`, revisión del `reviewer` y CI verde.

## Diseño propuesto

Cambio elegido: **`RunWorker(ctx context.Context, yt *YouTube, st *Store, cfg WorkerConfig)`**.
Es el idioma de la stdlib, el worker ya usa `context` para los streaming y `main.go` pasa
`context.Background()`, que nunca se cancela: producción queda exactamente igual.

Orden de trabajo:

1. **Test de regresión primero.** Agregar el parámetro `ctx` a `RunWorker` sin usarlo todavía
   (y actualizar las tres llamadas) y escribir `TestWorkerStops` en `monitor_test.go`:
   - API falsa como la de `TestWorkerStreamsChat`: un vivo con streaming que queda abierto
     (el handler espera `r.Context().Done()` o un canal `stop` cerrado en un `defer`, para que un
     arreglo roto no cuelgue `srv.Close`) y otro con streaming 404 que se lee por turnos;
     `ChatEvery`/`ChatPeak` de 1 ms.
   - El handler del streaming cierra un canal `clientGone` solo cuando sale por
     `r.Context().Done()`.
   - Esperar (hasta 5 s) a que lleguen los mensajes de los dos vivos, `cancel()`, y exigir que
     `RunWorker` vuelva en menos de 3 s (`t.Fatal("RunWorker no volvió 3 s después de cancelar")`).
   - Después: `clientGone` cerrado en menos de 1 s; tomar `os.ReadDir` del directorio con nombre,
     tamaño y `ModTime` de cada archivo más `st.Units()`, dormir 1,5 s y comparar: tiene que ser
     idéntico.
   - Correrlo y guardar la salida: hoy falla de forma determinista porque `RunWorker` ignora el
     contexto. Guardar también la salida de `go test -race -count=1 -run '^TestWorkerRealTime$' ./...`
     (falla 8/8) como segunda evidencia.
2. **`worker.go`, `RunWorker`:**
   - `openStream` (`worker.go:210`): derivar el contexto del streaming de `ctx`
     (`context.WithCancel(ctx)`) en vez de `context.Background()`. Así cancelar `ctx` corta todas
     las conexiones abiertas, incluidas las de vivos cuyo estado se reemplazó.
   - Llevar la cuenta de goroutines de streaming vivas en una variable local del worker (por
     ejemplo `streams int`): `+1` en `openStream`, `-1` al recibir un evento con `done == true`
     en el `select`, **antes** de llamar a `handle` (el `done` de un vivo olvidado vuelve en
     `worker.go:182-183` sin tocar el estado, pero igual tiene que descontarse). Solo la toca la
     goroutine del worker: no hace falta lock.
   - Agregar `case <-ctx.Done():` al `select` de `worker.go:392`. Ahí: `tick.Stop()`, cancelar
     todos los `c.cancel` que queden en `chats`, y seguir leyendo `events` hasta que `streams`
     llegue a 0 (procesándolos con `handle` para que la cuota de un streaming que ya respondió
     quede sumada, o solo descontándolos: decisión del implementer, ver preguntas). Recién
     entonces `return`. Leer `events` hasta el final evita que una goroutine de streaming quede
     bloqueada en `events <- ...` (`worker.go:213-214`) para siempre.
   - Actualizar el comentario de `worker.go:128`: corre hasta que se cancela `ctx` y no vuelve
     hasta que terminan sus streaming; con `context.Background()` corre para siempre.
   - La iteración que está en curso al cancelar termina normalmente: los llamados comunes
     (`YouTube.get`, `youtube.go:35`) no reciben contexto y tienen timeout de 30 s
     (`youtube.go:30`). No se cambia eso.
3. **`main.go:109`:** `go RunWorker(context.Background(), yt, st, WorkerConfig{...})`.
4. **Tests existentes:** en `TestWorkerRealTime` y `TestWorkerStreamsChat`, crear
   `ctx, cancel := context.WithCancel(context.Background())`, lanzar el worker con un canal
   `done` que se cierra al volver, y registrar `defer func() { cancel(); <-done }()` (con un
   límite de tiempo y `t.Error` si no vuelve) **después** de `defer srv.Close()` y del `defer`
   que restaura `realTimeGap`. Por el orden LIFO de los `defer`, el worker se para primero, luego
   se cierra el servidor y se restaura `realTimeGap`; el cleanup de `t.TempDir()` (un
   `t.Cleanup`) corre después de todos los `defer`. Conviene un helper chico en
   `monitor_test.go`, por ejemplo `startWorker(t, yt, st, cfg) (stop func())`, que devuelva la
   función para el `defer`. No usar `t.Cleanup` para el stop: correría después de `srv.Close` y
   de restaurar `realTimeGap`, que es justo la carrera.

Alternativas descartadas:

- **Canal `Stop <-chan struct{}` en `WorkerConfig`** (nil = para siempre): funciona igual, pero
  hay que convertirlo a contexto para los streaming y mezcla configuración con ciclo de vida.
  `context.Context` es la convención de la stdlib.
- **Arreglar solo los tests** (directorio propio sin cleanup, `time.Sleep` antes de volver, no
  usar `t.TempDir()`): no hay forma de parar el worker desde afuera, así que seguirían quedando
  goroutines vivas pidiendo a servidores cerrados, basura en `/tmp` y la carrera sobre
  `realTimeGap`. Solo esconde el síntoma.
- **Que `AddUnits` no escriba cuando `n == 0`:** baja la frecuencia pero no la elimina (`Store.Add`
  y los `AddUnits` con unidades siguen) y no arregla la carrera. Fuera de esta tarea.
- **`sync.WaitGroup` para los streaming:** igual habría que seguir leyendo `events` mientras se
  espera (la goroutine se bloquea en el envío), así que un contador local en la goroutine del
  worker es más simple.
- **Parar el worker con señales (Ctrl+C) en `main.go`:** no lo pidió nadie y cambia el
  comportamiento de producción. Fuera de alcance.

## Fuera de alcance

- Cancelación de los llamados comunes (`YouTube.get` con contexto) o cambiar timeouts.
- Apagado ordenado del modo `-dashboard` (señales, `http.Server.Shutdown`).
- Que `AddUnits(0)` no reescriba `datos.cuota.json`.
- Cambios en `Store`, en la retención de 30 días, en el formato de `datos.jsonl`, en flags o en
  `README.md` (no cambian flags, comportamiento visible ni cuota).

## Riesgos

- **Cuota de YouTube:** el arreglo no agrega llamados; solo corta conexiones al cancelar, cosa que
  en producción no pasa. Se verifica con CA6 y con `git diff main -- youtube.go` vacío.
- **Producción:** con `context.Background()` el `case <-ctx.Done()` nunca se elige; el bucle es el
  mismo. Riesgo bajo; lo cubre CA5 y la revisión.
- **Concurrencia:** el contador de streaming y el drenaje de `events` viven en la goroutine del
  worker; las goroutines de streaming siguen sin tocar `chats` ni `lives`. Si el contador se
  descuenta mal (por ejemplo, en `handle` después del `return` de `worker.go:183`), `RunWorker` no
  vuelve nunca: lo detecta `TestWorkerStops` por el límite de 3 s. Se verifica con CA1 y CA3.
- **Tests colgados:** si el streaming no se cancela, el handler falso bloquearía `srv.Close`; por
  eso el handler también sale con el canal `stop` y el test usa límites de tiempo en vez de
  esperas sin fin.
- **`datos.jsonl`, privacidad, Windows:** no se tocan el formato ni el almacenamiento; el cambio es
  stdlib pura y compila igual en Windows (gate de build de Windows).
