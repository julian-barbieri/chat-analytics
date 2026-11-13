# Spec: pestañas en Programas y En vivo

- **Estado:** implementada
- **Tipo:** mejora
- **Rama:** `feat/pestanas-programas-en-vivo`
- **Origen:** pedido del usuario (opciones A de Programas y C de En vivo del lienzo de diseño, probadas en la vista previa con datos reales). Reemplaza a `programas-mosaico-chat-derecha`, que no se implementa.
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-13

## Contexto

Las dos pantallas apilan todo en una columna. Lo medí en la vista previa a 1.440 px, con QUIÉN PAGA LA FIESTA (Rock & Pop, 11/11):

- **Programas:** la página mide 5.272 px. La tabla y el gráfico de likes ocupan los primeros 768 px (`templates/programas.html:8-48`). La fila `.detail-grid` mide 1.409 px y deja dos huecos de ~750 px (`templates/programas.html:94`). Fidelización e historial (4 semanas fijas) siempre se calculan y se muestran (`programs.go:791`, plantilla `historia`).
- **En vivo:** la página mide 6.156 px y el chat recién aparece a 5.401 px, después de todo el análisis (`templates/partials.html:4-12`). Los 7 números quedan en 6 + 1 (`.tiles`, `layout.html:178`).

## Objetivo

Que cada pantalla muestre arriba lo fijo (los números) y el resto en pestañas. Así ninguna vista pasa de ~2.700 px en ese caso y el chat de En vivo se ve en la primera pantalla.

## Criterios de aceptación

**Programas** — `TestProgramsTabs`

- [x] CA1: El detalle muestra arriba los números del programa (Mensajes, Personas, Preguntas, Super Chats, Likes, Likes c/1.000 esp.). Abajo van las pestañas **Resumen · Conversación · Audiencia y fidelización · Chat del programa**, como links de al menos 44 px; la elegida lleva `aria-current="page"`.
- [x] CA2: Cada pestaña muestra lo suyo:
  - Resumen: Participación, Resumen del chat, motivos, Sentimiento y Super Chats.
  - Conversación: Categorías y preguntas, y De qué se habla.
  - Audiencia: Fidelización, Emisión por emisión, Franjas, Mapa y Likes del vivo cada 30 min.
  - Chat: el chat del programa con sus filtros.

  Lo de las otras pestañas no está en el HTML. Sin `pestana`, va Resumen.
- [x] CA3: `?pestana=` usa la lista cerrada `resumen|conversacion|audiencia|chat`. Un valor fuera de la lista da Resumen y nunca se refleja. Viaja en `programsQuery` (refresco de 60 s, `abrir`, franja, comparar, filtros y páginas del chat). Elegir otro programa (`WithTramo`) la conserva.
- [x] CA4: La lista no tiene el gráfico de likes ni la nota larga. La columna Canal aparece solo con todos los canales. Super Chats y Likes c/1.000 salen de la tabla y quedan en los números del detalle.
- [x] CA5: `programHistory` (fidelización e historial) se arma solo en la pestaña Audiencia. Las emisiones para «Comparar con» se siguen armando cuando hacen falta.

**En vivo** — `TestLiveTabs`

- [x] CA6: La tarjeta Canal, la línea de Claude, el título, la frase y los 7 números van siempre arriba; los 7 números entran en una fila desde 1.280 px. Debajo van las pestañas **Ahora · Momentos y audiencia · De qué se habla · Super Chats**, como links de al menos 44 px con `aria-current="page"`.
- [x] CA7: Cada pestaña muestra lo suyo:
  - Ahora: Alertas, Chat en vivo, Sentimiento y Preguntas, con el chat a la izquierda y Sentimiento y Preguntas a la derecha.
  - Momentos: Momentos para reels, Quiénes participan y Likes.
  - De qué se habla: el resumen de Claude, Categorías y De qué se habla.
  - Super Chats: la tarjeta de `#vivo-abajo`.

  Sin `pestana`, va Ahora.
- [x] CA8: `?pestana=` usa la lista cerrada `ahora|momentos|temas|supers`. Un valor fuera de la lista da Ahora y no se refleja. Viaja en `vivo-q`, en los `.vq` (para que la recarga por cambio de vivo, `HX-Retarget: #vivo`, la conserve), en `/parcial/vivo` y en el push-url de los botones de canal.
- [x] CA9: `#chat` sigue en la página y se sigue refrescando cada 2 s en todas las pestañas: fuera de Ahora va oculto. Así se detecta igual el cambio de vivo.

**Las dos**

- [x] CA10: `gofmt`, `go vet`, staticcheck, los tests con `-race` y `-count=5`, y el build de Linux y de Windows pasan (`scripts/gates.sh completo`). `TestTemplateTokens` en verde: colores nuevos solo con tokens.
- [ ] CA11: En la vista previa, con QUIÉN PAGA LA FIESTA del 11/11 a 1.440 px, ninguna pestaña de Programas pasa de ~2.800 px. En En vivo de Rock & Pop, el chat empieza antes de los 1.300 px. Se revisan las cinco pantallas en modo claro y oscuro y a 390 px (las pestañas se desplazan de costado). Es verificación manual.

## Diseño

- **Programas:** `Pestana` en `programsQuery`, leída en `readDetail` con su lista cerrada, y `ProgramDetail.Tab` con un bloque por pestaña en `programa-detalle`. Los números de arriba salen de la `ProgramRow` elegida. `LikesChart` pasa al detalle, a la pestaña Audiencia.
- **En vivo:** `pestana` en `vivo-q` y `keepView`, y `liveSection` la pasa al template `vivo`. `vivo-analisis` dibuja la barra de pestañas después de los números y solo las tarjetas de la pestaña. `#vivo-abajo` va vacío fuera de Super Chats.
- **CSS en `layout.html`:**
  - En Ahora, `#vivo` es una grilla de dos columnas: `#vivo-analisis` va con `display: contents` y el chat ocupa dos filas. Debajo de 900 px queda una sola columna (en 720 a 900 px, dos columnas de chat y análisis no entran bien).
  - En las otras pestañas, `#chat` va oculto.
  - La barra de pestañas usa la misma clase en las dos pantallas.
- Sin JS propio: cada pestaña es un link con `hx-get` y `hx-push-url`, como los filtros de hoy.

## Fuera de alcance

- Cambiar qué muestra cada tarjeta o sus cálculos.
- Comentarios, Sin vivo y Canales.
- Recordar la última pestaña entre visitas.

## Riesgos

- **Links que se olvidan `pestana`:** el estado vive en muchos links (pliegues, franjas, filtros, comparar, chat). Un link que no la lleve vuelve a la primera pestaña. Los tests revisan una muestra de cada tipo.
- **`display: contents` en `#vivo-analisis`:** htmx lo reemplaza con `outerHTML` cada 30 s. El CSS va por id y clase, así que sobrevive al reemplazo (lo probé en el prototipo).
- **Cuota, datos y Claude:** no se tocan. Es solo vista, y Audiencia hasta calcula menos.
