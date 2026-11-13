# Spec: recomendaciones por rol y parte diario

- **Estado:** aprobada
- **Tipo:** mejora
- **Rama:** `feat/recomendaciones-por-rol`
- **Origen:** pedido del usuario: generar automáticamente recomendaciones a partir del chat
  en vivo de cada programa, para distintos empleados. Eligió las propuestas A (ficha por
  programa con filtro por rol) y C (parte diario y semanal por rol).
- **Aprobada por / fecha:** Julian Barbieri, 2026-11-13

## Contexto

La pestaña Recomendaciones de Programas hoy muestra solo un cartel de "Próximamente"
(`templates/programas.html:150`). Los datos para recomendar ya se calculan: grupos de
preguntas (`questionGroups`, `batch.go:806`), quejas técnicas (`Alerts`, `batch.go:751`),
momentos para clips (`TopMoments`, `analyze.go:312`), Super Chats (`Supers`,
`analyze.go:415`), lo normal y la comparación con otras emisiones (`NormalBand` y
`CompareEmission`, `loyalty.go:605` y `:630`), habituales en riesgo (`Loyalty`,
`loyalty.go:135`), cortes del tramo (`closedWhy`, `programs.go:192`) y el resumen de
Claude de cada programa terminado (`analyze`, `analyst.go:254`).

## Objetivo

Recomendaciones en dos capas:

1. **Señales.** Las calcula Go con reglas, cada una con sus números y los mensajes que
   la prueban.
2. **Redacción.** Claude redacta y prioriza cada señal para un rol. Es opcional: sin
   Claude, cada señal sale con un texto fijo.

Se ven en dos lugares:

- **(A)** la pestaña Recomendaciones de cada programa, con filtro por rol y marcas
  locales Hecha / No sirve;
- **(C)** un parte por rol con lo de ayer o de los últimos 7 días, para leer, copiar o
  imprimir. No se manda a ningún lado.

## Criterios de aceptación

- [ ] CA1: `recSignals` es una función pura que devuelve las señales de un tramo. Cada
  una tiene `Kind`, `Roles`, números, IDs de mensajes y un `ID` estable
  (`<kind>|<sujeto>`). Por cada tipo hay un caso que dispara la señal y otro que no
  llega al umbral — `TestRecSignals`:

  | Señal | Cuándo sale | Roles |
  |---|---|---|
  | `pregunta` | Grupo de ≥ 5 personas sin marcar como respondido | Producción |
  | `tecnica` | ≥ 3 quejas técnicas | Técnica |
  | `clip` | Cada `TopMoments` del tramo, hasta 2 | Redes |
  | `participacion` | Fuera de `NormalBand`, para arriba o para abajo | Dirección y Producción |
  | `riesgo` | ≥ 3 habituales en riesgo | Producción y Dirección |
  | `tema` | Los 2 temas principales del resumen de Claude, si lo hay | Redes y Producción |
  | `corte` | El tramo se cortó sin vivo | Técnica |
  | `supers` | ≥ 1 Super Chat, con los montos por moneda | Comercial |
  | `pico` | Pico de espectadores sobre lo normal | Comercial |
- [ ] CA2: sin Claude, o sin una redacción guardada, cada señal sale con un texto fijo
  por tipo y la prioridad por reglas (`recFallback`). La evidencia la arma siempre Go
  (por ejemplo, "23 personas · 09:10–09:40") — `TestRecFallback`.
- [ ] CA3: al guardarse el análisis final de un programa, `RunAnalyst` manda un pedido
  de recomendaciones con `-claude-modelo`. La prioridad es la más baja, después de los
  programas terminados. Lo que se manda:
  - las señales y los textos de hasta 5 mensajes de ejemplo por señal, sin `Author`,
    sin IDs y sin spam (`recPrompt`);
  - con un chequeo de peor caso contra el tope (`recWorstCase`) antes de mandar.

  Si no hay señales, no hay pedido — `TestAnalystRecs` y `TestRecPrompt`.
- [ ] CA4: de la respuesta solo se toman recomendaciones que cumplan todo esto:
  - que se refieran a una señal mandada y a uno de sus roles;
  - con prioridad de una lista cerrada (`alta`, `media`, `baja`);
  - con un título de ≤ 90 runas y una acción de ≤ 240;
  - sin números que no estén en la señal.

  Las que no cumplen se descartan y esas señales quedan con el texto fijo
  (`validRecResult`) — `TestValidRecResult`.
- [ ] CA5: las redacciones se guardan en `datos.recomendaciones.jsonl` (`ProgramRecs`:
  clave del tramo, modelo, tokens, costo, recomendaciones y error):
  - retención de 30 días desde el fin del tramo al abrir, con tmp + `os.Rename`;
  - el gasto se suma al del día;
  - la vista previa copia el archivo.

  Se verifica con `TestRecsStore` y `TestCopyData` de `tools/preview`.
- [ ] CA6: la pestaña Recomendaciones muestra una tarjeta por recomendación, ordenadas
  por prioridad. Cada tarjeta tiene:
  - el rol, la prioridad, el título y la acción;
  - la evidencia, con un `<details>` que lista los mensajes;
  - el link al momento en YouTube si es un `clip`.

  Además, el filtro de roles funciona así:
  - `?rol=` es una lista cerrada (`recRoles`: `produccion`, `redes`, `tecnica`,
    `comercial`, `direccion`), viaja en `programsQuery` y un valor desconocido se ignora;
  - el filtro son links con `aria-current`;
  - sin señales, se muestra "Este programa no dejó recomendaciones".

  Se verifica con `TestProgramRecsTab`, que se amplía.
- [ ] CA7: las marcas Hecha, No sirve y Deshacer funcionan así:
  - se mandan con `POST /recomendaciones/marca` (`rec=<clave>|<ID>`, `estado=hecha|nosirve|deshacer`);
  - pasan por `sameOrigin` y `localRequest` y el cuerpo es de 1 KB como mucho;
  - se guardan en `datos.recmarcas.jsonl`, con mutex propio, retención de 30 días y fuera
    del `Store`;
  - la respuesta es la tarjeta.

  Una marcada pasa al final y se ve atenuada. La vista previa no copia el archivo —
  `TestRecMarks` (guardas, valores fuera de lista y persistencia).
- [ ] CA8: `GET /parte` (`?rol=` y `periodo=dia|semana`) muestra un parte por rol:
  - **Con `dia`:** junta las recomendaciones de los programas de ayer de los canales
    configurados.
  - **Con `semana`:** junta las de los 7 días anteriores a hoy, con las del mismo
    `Kind` y programa juntas ("se repitió en 4 emisiones").
  - **Arriba:** una línea con números de Go (programas, mensajes, cuántas son de
    prioridad alta).
  - **Marcas:** sin las marcadas como No sirve.
  - **Caché:** se arma una vez por día local y por rol (caché en memoria con mutex).

  Se verifica con `TestParte`.
- [ ] CA9: `GET /parte.txt` devuelve el mismo parte en texto plano, para copiar. La página
  tiene CSS de impresión, sin JS propio, y un link "Parte de ayer" desde la pestaña
  Recomendaciones — `TestParte`.
- [ ] CA10: el ADR `0011` explica las dos capas y qué se manda a Claude. El README suma
  el costo (~US$ 0,03 por programa con Sonnet 5.5) y los archivos nuevos. AGENTS.md
  incluye el mapa y las reglas de privacidad.
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde. Navegador: pestaña y
  parte en claro, oscuro y celular.

## Diseño propuesto

- **`recs.go` (nuevo, funciones puras):**
  - tipos: `RecSignal`, `Rec`, `ProgramRecs`, `recRoles`;
  - umbrales en constantes;
  - funciones: `recSignals`, `recFallback`, `recPrompt`, `recSchema`,
    `validRecResult`, `recWorstCase`, `recView` (que junta las señales con la redacción
    y las marcas) y `parteView`.
- **Datos de entrada:** `recInput` los junta con lecturas del `Store` y lo usan el
  handler y el analista. Hoy `programHistoryEms` es un método de `Server`: se pasa a una
  función que recibe el `Store`.
- **`analyst.go`:** después de `AddAnalysis` (final y sin error), se pide `recs` en el
  mismo tick, si el tope alcanza. Un error se guarda en `ProgramRecs.Error` sin
  reintento, salvo 429 / 5xx, que usan `retry`.
- **`recmarks.go`:** las marcas, con el mismo patrón que `answers.go`. **`recpage.go`:**
  los handlers de `/parte` y `/parte.txt`, el caché y la plantilla `templates/parte.html`.
- **Orden de implementación:**
  1. señales y pestaña sin Claude;
  2. marcas;
  3. pedido a Claude;
  4. parte.

## Fuera de alcance

- Mandar el parte por Slack, mail o WhatsApp.
- Un texto del parte redactado por Claude: hoy se arma solo con las recomendaciones
  guardadas.
- Una pantalla propia en la navegación.
- Recomendaciones durante el vivo (la propuesta D).
- Elegir quién es cada empleado: el rol es un filtro, no un usuario.

## Riesgos

- **Cuota de YouTube:** ninguno, porque no hay llamados nuevos.
- **Costo de Claude:** un pedido por programa terminado, siempre con el chequeo del peor
  caso contra el tope y con la prioridad más baja. Las tandas no se tocan.
- **Privacidad:** se manda solo texto de mensajes, sin autor ni ID y sin spam, como en el
  ADR 0006. El archivo nuevo es de datos del modelo, con la retención de 30 días y en
  `.gitignore` (`datos.*.jsonl`).
- **Rendimiento:** `recInput` lee 28 días de mensajes de un programa. En la pestaña
  se hace una vez por pedido, como la historia de Audiencia. El parte se arma con caché
  diario, nunca en cada refresco.
- **Concurrencia:** las marcas y el caché del parte tienen mutex propio y quedan fuera del
  `Store`. Los handlers no llaman a Claude.
