# Spec: rediseño de En vivo y Programas

- **Estado:** borrador
- **Tipo:** mejora
- **Rama:** `feat/rediseno-envivo-programas`
- **Origen:** pedido del usuario con capturas; diseño aprobado en https://claude.ai/artifact/3MuSJa3AXYKoAWHDeZa3Xx (5 pantallas)
- **Aprobada por / fecha:** —

## Contexto

- **En vivo › Ahora:** el chat va a la izquierda (sticky), y Sentimiento y Preguntas a la derecha (`templates/layout.html:230-242`). "Por qué hay mensajes negativos" y "Qué está gustando" están dentro de `#sentimiento`, con su filtro de período (`.mwin`, `templates/partials.html:157-171`; `motiveWindows`, `server.go:472`). "Preguntas agrupadas" (`questionsData`, `server.go:1618`) cuenta todo el alcance y no tiene filtro.
- **En vivo › De qué se habla:** Categorías y De qué se habla van lado a lado en `.grid2` (`partials.html:248-252`). El detalle de la categoría elegida (`.gdet`) queda debajo del semicírculo (`cgauge`, `partials.html:406`).
- **Programas › Resumen:** usa dos columnas, `.tcols` (`programas.html:62-124`). A la izquierda van Participación → Resumen del chat → motivos, y a la derecha Sentimiento (filas `sfila`, `SentRows`, `programs.go:1302`) y Super Chats.
- **Programas › Audiencia:** el gráfico de Segmentos (`segflow`) va arriba de su tabla, y la tabla, al lado de Cohortes (`programas.html:244-266`).

## Objetivo

Que las cuatro pantallas queden como en el diseño aprobado, sin datos nuevos, sin cuota y sin pedidos nuevos a Claude.

## Criterios de aceptación

- [ ] **CA1 En vivo › Ahora:** el orden del HTML es Alertas → `#chat` a todo el ancho (una fila por mensaje: categoría, autor, hora y texto) → `#sentimiento` (el clima a la izquierda y "Cómo fue cambiando" a la derecha) → una sección nueva `#motivos`. Esa sección tiene el filtro (`mot-5`, `mot-30`, `mot-60`, `mot-programa`) y, en tres columnas, Negativos, Qué está gustando y `#preguntas`. Los motivos ya no van dentro de `#sentimiento`. — `TestLiveNowLayout`
- [ ] **CA2 Preguntas con período:** "Preguntas agrupadas" cuenta solo los mensajes de la ventana elegida (por defecto, los últimos 5 min; con "Todo el programa", o "Todo el día" sin vivo, todo el alcance como hoy). El encabezado lo dice ("… en los últimos 30 min"). Un `motivos` fuera de la lista no se refleja. El número "Preguntas" de arriba sigue contando todo el alcance. Lo plegado (`qg1…`, `maspreguntas`) conserva `motivos`. — `TestQuestionsWindow`
- [ ] **CA3 Sin Claude:** sin tandas, `#motivos` muestra solo las preguntas por reglas, sin filtro, como hoy. — `TestQuestionsWindow` (caso apagado)
- [ ] **CA4 En vivo › De qué se habla:** en `#categorias`, el semicírculo y su lista van a la izquierda y `#cat-detalle` a la derecha (en celular, apilados). `#palabras` va debajo, a todo el ancho, con temas y palabras en dos columnas. La tarjeta de Programas › Conversación no cambia. — `TestLiveTopicsLayout` + navegador
- [ ] **CA5 Programas › Resumen:** el orden es Resumen del chat → Participación → Sentimiento del programa → los dos motivos lado a lado. El sentimiento son barras verticales apiladas (`.schart`, como En vivo), con el promedio arriba. Cada barra abre su franja como la fila de hoy (`desde=sent&franja=…`) y la elegida queda marcada. En Resumen no hay `#supers`. — `TestProgramSummaryLayout`
- [ ] **CA6 Programas › Super Chats:** la pestaña nueva `supers` es la **última** de `programTabs` (después de Chat del programa) y aparece aunque no haya pagos. Muestra el total por moneda, la tabla por moneda y la lista de pagos del programa. Sin pagos dice "Sin Super Chats ni Super Stickers en este programa.". — `TestProgramSupersTab`
- [ ] **CA7 Programas › Audiencia:** en Segmentos por asistencia, el `segflow` va a la izquierda y su tabla a la derecha. Cohortes va después, a todo el ancho. — `TestProgramAudienceLayout`
- [ ] **CA8:** las cinco pantallas con datos, vistas en la vista previa en claro, oscuro y ancho de celular.
- [ ] **CA9:** AGENTS.md (pestañas de Programas, layout de Ahora) y README (Resumen, pestaña Super Chats, preguntas con período) actualizados.
- [ ] Gates de `CLAUDE.md` → "Definición de terminado" en verde.

## Diseño propuesto

- **Ahora:**
  - En `vivo-analisis` (`partials.html`) se saca el bloque de motivos de `#sentimiento` y se arma `<section id="motivos">` con `.mwin`, los dos motivos y `{{template "vivo-preguntas"}}`.
  - En `layout.html`, `#vivo.t-ahora`: `#chat` va a todo el ancho, sin sticky y con `order: 1`; `#sentimiento` lleva `order: 2` y un grid clima | gráfico; `#motivos` lleva `order: 3` y `repeat(3, minmax(0,1fr))`, que pasa a 1 columna en menos de 900 px. Dentro de Ahora, `.vfeed li` es un grid de una fila.
- **Ventana compartida:** el cálculo de `since` de `sentimentData` pasa a `motiveSince(ls, motives, now)`. `questionsData` recibe `since` y filtra `ls.msgs` por `At` antes de `questionGroups`. El número de arriba usa el conteo de todo el alcance.
- **Categorías de En vivo:** se usa un CSS limitado a `#vivo #categorias`. `.cgauge` pasa a ser un grid de 2 columnas (`.gwrap` en columna a la izquierda y `.gdet` a la derecha). En la pestaña temas se saca `.grid2` y `#palabras` va a todo el ancho, con temas y palabras en dos columnas.
- **Programas Resumen:**
  - Se reordena `programa-detalle` en una sola columna y se sacan `.tcols`, `.tmain` y `.tside` si quedan sin uso.
  - `programSentiment` arma también un `SentChart` (reusa `sentimentChart` con las franjas del tramo), y el `Href` de cada barra sale de `programSlotLinks` igual que las filas.
  - Las barras del gráfico se sacan de En vivo a una plantilla común `sent-bars`, con su `hx-target`.
- **Pestaña Super Chats:** se agrega `{"supers", "Super Chats"}` al final de `programTabs` y se mueve `#supers` de Resumen a `{{else if eq .Tab "supers"}}`, en el formato de En vivo (`.sbigs`, tabla y lista). Se refresca cada 60 s como el resto (no es Chat).
- **Audiencia:** se arma un contenedor `.segcols` (grid `minmax(0,440px) 1fr`) para el `segflow` y la tabla, y Cohortes queda fuera de `.grid2`.
- **TDD:** primero los tests de CA1 a CA7, que fallan. Se ajustan los tests que hoy esperan el orden o el lugar viejos (`monitor_test.go:9478`, `:10745`).

## Fuera de alcance

- Los otros gráficos y tarjetas, la pantalla Comentarios, las pestañas Momentos y Super Chats de En vivo, y Programas › Conversación.
- Marcar preguntas como respondidas (sigue sin botón).
- Cualquier cambio en las tandas, el clasificador o los datos guardados.

## Riesgos

- **Cuota, Claude y datos:** nada. Solo cambian las plantillas, el CSS y cómo se filtran en memoria datos que ya se leen.
- **Cambio visible:** "Preguntas agrupadas" arranca en los últimos 5 min, cuando hoy muestra todo el programa. Lo confirmó el usuario (2026-11-13), como en el diseño. Para ver todo, está "Todo el programa".
- **Links guardados:** un `?pestana=supers` en Programas antes no existía y caía en Resumen; ahora abre la pestaña. Un valor fuera de la lista sigue sin reflejarse.
- **Celular:** las tres columnas y los dos grids nuevos se apilan en menos de 900 px y de 720 px. Se verifica en la vista previa (CA8).
