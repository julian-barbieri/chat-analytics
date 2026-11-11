# Spec: En riesgo a los 7 días

- **Estado:** aprobada
- **Tipo:** mejora
- **Rama:** `feat/en-riesgo-7-dias`
- **Origen:** pedido del usuario en el chat (2026-11-11): "que esos participantes que están en riesgo sean +7 y no +14 días".
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-11

## Contexto

En Programas → Fidelización del chat, la tarjeta "En riesgo" cuenta a los habituales y
al núcleo que no escriben hace 14 días o más (`loyalty.go:190`,
`now.Sub(p.last) >= 14*24*time.Hour`). El texto de la tarjeta dice "habituales sin
escribir hace 14+ días" (`templates/programas.html:219`) y el README lo repite
(`README.md:112`). Para una radio con programas diarios, 14 días es tarde para avisar.

## Objetivo

"En riesgo" cuenta a los habituales y al núcleo sin escribir hace 7 días o más, y la
tarjeta y el README lo dicen.

## Criterios de aceptación

- [ ] CA1: un habitual cuyo último mensaje fue hace 7 días exactos o más cuenta como en
  riesgo; uno de hace menos de 7 días no. Un ocasional o nuevo nunca cuenta. — `TestLoyaltyAtRisk` (nuevo, tabla de casos).
- [ ] CA2: `TestLoyalty` sigue en verde sin cambiar sus números (B, a 15 días, sigue
  siendo el único en riesgo). — `go test -count=1 -run '^TestLoyalty$' ./...`
- [ ] CA3: la tarjeta dice "habituales sin escribir hace 7+ días". — test de la
  plantilla con `grep` sobre el HTML de `programDetail` o chequeo en `TestLoyaltyAtRisk`
  sobre el render; y vista previa.
- [ ] CA4: el README dice "7 días o más" en "en riesgo".
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

- `loyalty.go`: constante `atRiskAfter = 7 * 24 * time.Hour` con un comentario del por
  qué, usada en la comparación de la línea 190.
- `templates/programas.html:219`: "14+" → "7+".
- `README.md:112`: "hace 14 días o más" → "hace 7 días o más".
- Test primero: `TestLoyaltyAtRisk` con un habitual a 6, 7 y 8 días del `now` y un
  ocasional a 10 días.

## Fuera de alcance

- El retorno semanal ("de los que escribieron hace 7 a 14 días") no cambia.
- Los cortes de habituales y núcleo (`habitualShare`, `coreShare`) no cambian.
- Hacer el umbral configurable.

## Riesgos

- Ninguno de cuota, datos ni concurrencia: es una cuenta pura sobre mensajes ya leídos.
- El número de la tarjeta va a subir de golpe: es lo pedido.
