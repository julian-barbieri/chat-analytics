# Spec: diseño ejecutivo del dashboard

- **Estado:** borrador
- **Tipo:** mejora
- **Rama:** `feat/diseno-ejecutivo`
- **Origen:** pedido del usuario (2026-11-11): un dashboard más profesional y minimalista, para gerentes y directores. Eligió la propuesta A «Ejecutivo claro» con la frase de resumen de la C (lienzo de diseño, `Main.dc.html` y `Editorial.dc.html`).
- **Aprobada por / fecha:** —

## Contexto

Hoy el dashboard es funcional pero técnico: pestañas redondas, tarjetas iguales en importancia, textos largos de ayuda y «Palabras y autores» como chips y una tabla (`templates/partials.html:195`, `monitorView` en `server.go:913`). Los números de arriba (`liveTiles`, `server.go:920`) mezclan datos de operación (Super Chats, mensajes por minuto) sin una lectura rápida de cómo viene el programa. Los momentos para reels son una tabla (`partials.html:173`).

## Objetivo

Que un gerente entienda En vivo en cinco segundos: una frase que resume, cinco números con su comparación y tarjetas más limpias. Las otras tres pantallas toman el mismo estilo (colores, tipografía, pestañas, tarjetas, tablas) sin cambiar su estructura.

## Criterios de aceptación

- [ ] CA1: estilo común nuevo en `layout.html`: fondo gris claro, tarjetas blancas con borde fino y radio 14 px, un solo acento azul, títulos de tarjeta más chicos y etiquetas en versalitas; pestañas como control segmentado; marca «Monitor de audiencia · Alphamedia». Todo color nuevo en los dos temas — `TestTemplateTokens`.
- [ ] CA2: cabecera del vivo en En vivo: chip «EN VIVO», canal y «desde HH:MM» arriba; el programa como título grande; botones de canal y «Ver en YouTube» a la derecha (se apilan en celular) — test de render de `vivo-canal`.
- [ ] CA3: `liveSummary` arma la frase de arriba con reglas (sin Claude): ritmo del chat contra lo típico, las dos palabras más repetidas y el mejor momento; cada parte solo si hay dato, y sin dato ninguno no hay frase — `TestLiveSummary` (tabla).
- [ ] CA4: los cinco números quedan en este orden: Mirando ahora, Participación (% de los que miran que escribió en los últimos 5 min, contra la típica del vivo), Mensajes por minuto, Personas que escribieron, Preguntas. Cada uno con su comparación en verde, naranja o gris. Super Chats sale de los números (sigue su tarjeta abajo) — `TestLiveTiles` actualizado.
- [ ] CA5: «Clima del chat»: en Sentimiento, el tono que más pesa en grande y una barra apilada positivo / neutro / negativo con los mismos % que las tarjetas de hoy, que reemplaza a esas tres tarjetas — test de render.
- [ ] CA6: «Mejores momentos para reels» como tarjetas (hora, %, programa, mirando y personas, link al minuto); los mensajes siguen plegados en cada tarjeta con el mismo `abrir` (`mom1`–`mom5`) — test de render y `TestFoldState` sin cambios.
- [ ] CA7: «Palabras y autores» pasa a dos tarjetas: «De qué se habla» (top 8 en barras; ▲ en las que suben: en los últimos 30 min tienen el doble de peso que antes y al menos 3 menciones, `risingWords`) y «Quiénes participan» (cuánto escriben los 10 más activos sobre el total, `topShare`, y el top 5 con barras) — `TestRisingWords`, `TestTopShare`.
- [ ] CA8: vista previa con datos: las cuatro pantallas en claro, oscuro y ancho de celular, sin scroll horizontal de la página.
- [ ] Gates de `CLAUDE.md` → «Definición de terminado» en verde.

## Diseño propuesto

- `layout.html`: tokens nuevos (mismos nombres, otros valores) y CSS de `.tabs`, `.card`, `.tile`, `h2`, tablas; `.brand` con marca. Sin fuentes externas: sigue `system-ui` (Segoe UI en Windows).
- `server.go`: `liveSummary(tiles, words, moment)` y `risingWords`, `topShare` (funciones puras, sobre los mensajes que ya tiene `channelAnalysis`, sin leer más del `Store`); `liveTiles` reordenado con Participación. `monitorView` suma `Rising` y `Share`.
- `partials.html`: `vivo-canal` (cabecera), `vivo-analisis` (frase, números, Sentimiento, Momentos, Palabras y autores). Las barras son `<div>` con `width` calculado, como `.catrow`.
- Primero los tests de las funciones puras en rojo, después las plantillas.

## Fuera de alcance

- El eje X con lo más nuevo a la derecha y los likes en En vivo: specs aparte (la fila de números queda lista para sumar «Likes del vivo»).
- «Habituales contra nuevos» en Quiénes participan: pide leer 7 días de mensajes en cada refresco; queda para otra spec.
- Cambiar la estructura de Programas, Comentarios y Canales; fuentes web o JS propio.

## Riesgos

- Cuota y Claude: ninguno; todo sale de datos ya cargados, con reglas.
- Performance: `risingWords` y `topShare` recorren los mismos mensajes del alcance una vez más cada 30 s; mismo orden que `TopWords`.
- Pantallas que dependen de clases que cambian (`.tile`, `.tops`, `.words`): se revisan las cuatro en la vista previa (CA8).
