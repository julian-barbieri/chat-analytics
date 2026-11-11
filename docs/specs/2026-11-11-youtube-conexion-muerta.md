# Spec: descartar la conexión muerta con YouTube

- **Estado:** implementada
- **Tipo:** bug
- **Rama:** `fix/youtube-conexion-muerta`
- **Origen:** mensaje del usuario (2026-11-11): el dashboard muestra durante minutos
  `Error al consultar YouTube: playlistItems: Get "…": context deadline exceeded
  (Client.Timeout exceeded while awaiting headers)`.
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-11

## Contexto

Con el monitor corriendo, los pedidos a YouTube fallan uno tras otro a los 30 s y el
dashboard deja de actualizarse (última actualización buena 09:34, error sostenido después).
Mientras tanto, desde la misma máquina, una conexión nueva a `www.googleapis.com` (también a
la misma IP) responde en 80 ms.

**Reproducción (en producción, no en test):** `ss -tnpi` sobre el proceso muestra **una sola**
conexión con Google, `172.217.115.4:443`, con `Send-Q 1499`, `backoff:11`, `retrans` y
`lastrcv:480460` (8 min sin recibir nada). La red (NAT de WSL o firewall) la cortó sin avisar.
El test de regresión que la reproduce sin red se escribe en `/implementar` (CA1).

**Causa raíz:** `NewYouTube` (`youtube.go:27-33`) arma `HTTP` y `Stream` sin `Transport`, así
que usan `http.DefaultTransport`: una sola conexión HTTP/2 por host para todo (pedidos comunes y
streaming del chat) y **sin pings**. Cuando esa conexión muere en silencio, Go la sigue usando:
cada pedido espera el `Timeout` de 30 s (`youtube.go:31`) y falla, el streaming espera
`streamIdle` (5 min, `youtube.go:618`), y la conexión recién se descarta cuando Linux se rinde
(`tcp_retries2=15`, entre 15 y 30 min). El mismo problema tiene `NewClaude` (`claude.go:148-154`),
con un timeout de 5 min y, si el pedido salió, el peor caso sumado al gasto (`MaybeBilled`).

## Objetivo

Una conexión muerta con YouTube (o con Claude) se detecta y se descarta en menos de un
minuto, y el pedido siguiente sale por una conexión nueva, sin reiniciar el monitor.

## Criterios de aceptación

- [x] CA1: con un servidor HTTP/2 de prueba detrás de un proxy TCP que en un momento deja de
  reenviar sin cerrar (conexión "agujero negro"), un pedido de `YouTube.get` falla por la
  conexión muerta **antes** del `Timeout` del cliente, y el pedido siguiente responde 200 por una
  conexión nueva. Falla hoy (espera el `Timeout` y el siguiente también se cuelga) —
  `TestYouTubeDeadConnection`.
- [x] CA2: `StreamChat` sobre la misma conexión muerta termina antes de `streamIdle` (vuelve sin
  error, como un cierre normal de YouTube: el worker lo reabre sin anotar nada; es como ya
  trata cualquier corte a mitad del streaming) — `TestStreamChatDeadConnection`.
- [x] CA3: `NewYouTube` y `NewClaude` usan el transporte con pings; `NewClaude` sigue sin
  seguir redirecciones — test sobre los clientes armados (`TestClientsTransport`).
- [x] CA4: `TestFlowAgainstFakeAPI` y los tests de Claude siguen en verde sin cambios de cuota.
- [x] Gates de `CLAUDE.md` → "Definición de terminado" en verde (incluido build de Windows).

## Diseño propuesto

- Nuevo `newTransport()` (en `youtube.go`, lo usa también `claude.go`): clon de
  `http.DefaultTransport` (mantiene proxy del entorno, HTTP/2 y timeouts de dial/TLS) con
  `HTTP2: &http.HTTP2Config{SendPingTimeout: pingEvery, PingTimeout: pingWait}` (Go 1.24, ya
  es el mínimo de `go.mod`). Valores: 15 s y 15 s → una conexión muerta se cierra en ~30 s.
  Variables de paquete para que el test las baje a milisegundos.
- `NewYouTube`: `HTTP` y `Stream` comparten un mismo `newTransport()` (como hoy comparten el
  default). `NewClaude`: su propio `newTransport()`, mismo `CheckRedirect`.
- Test primero: servidor `httptest` con `EnableHTTP2` + `StartTLS`, proxy TCP propio en el test
  con un interruptor para dejar de reenviar, cliente con `RootCAs` del certificado de prueba.

Alternativas descartadas: bajar el `Timeout` (no descarta la conexión, solo falla antes);
`CloseIdleConnections` tras un error (no cierra una conexión HTTP/2 con el streaming abierto);
desactivar HTTP/2 (una conexión TCP por pedido y sin pings igual).

## Fuera de alcance

Reintentar el pedido fallido en el momento, cambiar `streamIdle`, el `Timeout` de 30 s o el de
Claude, y la configuración de red de WSL.

## Riesgos

- **Cuota:** los pings no son pedidos a la API, no gastan unidades. Al cerrarse una conexión
  muerta el streaming se reabre (5 unidades) antes que hoy, pero es la misma reapertura que ya
  pasaba a los 5 min; no hay gasto nuevo con la red sana.
- **Concurrencia:** el transporte es seguro para uso concurrente; no cambia quién usa cada cliente.
- **Windows:** `HTTP2Config` es de la biblioteca estándar, sin código por plataforma.
- **Privacidad:** los errores siguen pasando por el reemplazo de la clave por `***`.
- **Test intermitente:** el proxy y los tiempos en milisegundos pueden ser frágiles bajo
  `-race`/`-count=5`; usar márgenes holgados (fallo antes de 1/2 del `Timeout`, no un número fijo).
