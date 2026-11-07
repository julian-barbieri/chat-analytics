# Spec: fidelización, emisión por emisión y franjas en Programas

- **Estado:** implementada
- **Tipo:** mejora
- **Rama:** `feat/fidelizacion-programas`
- **Origen:** pedido del usuario (2026-11-06) con cuatro capturas de referencia.
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-06

## Contexto

- La consola se llena de `chat de <ID>: YouTube cerró la conexión de tiempo real a los 11 s`
  (`worker.go:316`): es el cierre normal del streaming y no le sirve a la persona.
- El detalle de un programa (`programs.go:444`, `templates/programas.html`) mira un solo
  tramo. No hay forma de comparar un programa con sus emisiones anteriores ni de saber si la
  gente que escribe vuelve.
- La tarjeta Categorías de En vivo (`templates/partials.html:184`, `catRows` en
  `server.go:156`) son barras sueltas sin % ni ejemplo.
- No se guarda si quien escribe es moderador o dueño del canal: `chatPage` solo lee
  `authorDetails.displayName` (`youtube.go:503`).

## Objetivo

Sacar ese log; en el detalle de Programas, comparar el programa con sus emisiones de las
últimas 4 semanas (fidelización, emisión por emisión, franjas fuertes y débiles, mapa por
emisión); y rediseñar Categorías de En vivo como la captura.

## Definiciones

- **Programa**: mismo canal y mismo nombre (`ProgramName`, sin distinguir mayúsculas).
- **Emisión**: los tramos de ese programa en un mismo día local, juntos (un título que
  parpadea no parte la emisión).
- **Ventana**: 28 días fijos hasta ahora, sin importar el filtro Día (entra en la
  retención de 30). **Participante**: `Author` de chat, sin mensajes que `Classify` marque
  spam y sin moderadores ni dueño. Es por nombre visible: una aproximación, lo dice el pie.

## Criterios de aceptación

- [x] CA1: al cerrarse el streaming sin error no se loguea nada (los otros logs del chat
  siguen). — `TestStreamCloseNoLog` (captura `log.SetOutput`).
- [x] CA2: `chatPage` toma `isChatOwner` / `isChatModerator` en `Message.Role`
  (`"dueño"` / `"moderador"`, `omitempty`); una línea vieja de `datos.jsonl` carga igual. —
  `TestFlowAgainstFakeAPI` (servidor falso extendido) y `TestMessageRole`.
- [x] CA3: `Loyalty` (función pura) da, con datos armados: retorno semanal (escribieron en
  los 7 días anteriores y volvieron en los últimos 7), asistencia media (emisiones por
  participante, "de N vivos"), stickiness (únicos promedio por emisión ÷ únicos de la
  ventana), núcleo (% de personas y % de mensajes), en riesgo (habituales + núcleo sin
  escribir hace 14+ días), segmentos (nuevos = 1 emisión; núcleo ≥ 50 % de las emisiones;
  habituales ≥ 20 %; ocasionales, el resto) y cohortes por semana del primer mensaje con %
  que vuelve en +1..+3 ("—" si la semana no pasó). Excluye spam, moderadores y dueño. —
  `TestLoyalty` (tabla de casos).
- [x] CA4: `EmissionSentiment`: por emisión, % positivo / neutro / negativo con
  `Store.SentSlots` del programa, mediana de negativo y marca si se aleja 5 pts o más
  ("Más negativa de lo normal" / "Menos negativa de lo normal", y "· con quejas técnicas"
  si hubo 3+ mensajes con `TechIssue`). Largo de la barra proporcional a los mensajes
  analizados. Lo último arriba. Sin tandas de Claude, un texto que lo explica. —
  `TestEmissionSentiment`.
- [x] CA5: `StrongWeakSlots`: franjas de 5 min por hora del reloj presentes en al menos la
  mitad de las emisiones; columnas mediana de mensajes, rango normal (p25–p75),
  espectadores (mediana), % participación (mediana) y "en el top 5 de su emisión" (N de M).
  Muestra las 4 más altas (Fuerte) y las 2 más bajas (Débil). Con menos de 3 emisiones, un
  aviso en vez de la tabla. — `TestStrongWeakSlots`.
- [x] CA6: `EmissionMap`: filas = hasta 10 emisiones (lo último arriba), columnas = bloques
  de 30 min del horario del programa, celda = % de participación (promedio de sus franjas
  de 5 min), última fila Mediana; intensidad de verde en 5 clases (`.m0`–`.m4`, tokens
  nuevos en los dos temas), nunca color en `style`. — `TestEmissionMap`, `TestTemplateTokens`.
- [x] CA7: en el detalle de Programas, debajo de Temas / Sentimiento / Preguntas y en este
  orden: Fidelización del chat, Emisión por emisión, Franjas fuertes y débiles junto a Mapa
  por emisión, y al final Super Chats del programa. Tablas anchas en `.tablewrap` (scroll
  propio en celular). — `TestProgramDetailSections` (orden de los `id` en el HTML).
- [x] CA8: Categorías de En vivo: barra apilada por categoría, y por fila color, nombre,
  un ejemplo corto (el mensaje más reciente de esa categoría, ≤ 60 runas, sin autor),
  cantidad y %; mismo alcance que hoy. Título "Categorías" y el total a la derecha. —
  `TestCatRowsShare` (% suman 100, ejemplo recortado, Otros al final y la plantilla) y vista previa. Spam y Otros llevan una descripción en vez de un ejemplo (no se muestra un link de spam).
- [x] CA9: README actualizado (Programas y el campo nuevo). Gates de `CLAUDE.md` →
  "Definición de terminado" en verde; vista previa de las tres pantallas en claro, oscuro y
  ancho de celular.

## Diseño propuesto

- `worker.go`: borrar el `log.Printf` del caso `default` del cierre del streaming.
- `youtube.go`: `IsChatOwner`, `IsChatModerator` en `authorDetails` → `Message.Role`.
  Misma parte que ya se pide: sin cuota extra.
- `loyalty.go` (nuevo, funciones puras): `programEmissions` (tramos de 28 días → emisiones
  del programa), `Loyalty`, `EmissionSentiment`, `StrongWeakSlots`, `EmissionMap`. Las
  franjas reusan `ParticipationRange` por emisión.
- `programs.go`: `programDetail` arma los tramos de 28 días del canal (`programSegs`),
  filtra los del programa, pide los mensajes con `Store.MessagesOf` (solo los vivos de
  esas emisiones), espectadores con `Store.Viewers` y sentimiento con `Store.SentSlots`.
- `templates/programas.html` y `templates/partials.html` (Categorías); CSS y tokens en
  `layout.html`. `catRows` suma `Share` y `Example`.

## Fuera de alcance

- Insultos "al conductor / entre usuarios" (necesita otra etiqueta de Claude).
- Excluir moderadores en los mensajes guardados antes de este cambio (no hay dato).
- Cohorte "+4": con 28 días hay como mucho +3.
- Cambiar el filtro Día o agregar un período de 4 semanas a la lista de programas.

## Riesgos

- **Cuota**: ninguna llamada nueva a YouTube ni a Claude.
- **`datos.jsonl`**: `Role` con `omitempty`; las líneas viejas y las comunes no cambian.
- **Performance**: el detalle recorre 28 días de un programa en cada refresco de 60 s.
  Se piden solo los mensajes de los vivos de sus emisiones (`MessagesOf`), una pasada
  por cálculo; se mide con un test de 28 emisiones × 3.000 mensajes que tiene que tardar
  menos de 500 ms; con `-race` se saltea, porque los tiempos no dicen nada (mide ~180 ms, casi todo `Classify`; con 200 ms quedaba intermitente con la máquina cargada) (`TestLoyaltyPerf`, se saltea con `-short`; `raceEnabled` sale de `race_on_test.go` / `race_off_test.go`).
- **Privacidad**: solo agregados; ningún nombre de participante en la pantalla. Los
  ejemplos de Categorías muestran texto sin autor, como el resto de En vivo. La cohorte de
  la primera semana incluye a quienes venían de antes (solo hay 30 días): lo dice el pie.
- **Windows**: nada específico.
