# ADR 0010: la tarjeta Categorías de En vivo usa las etiquetas de Claude

- **Estado:** aceptada
- **Fecha:** 2026-11-10

## Contexto

Las tandas (ADR 0009) ya le piden a Claude una categoría por mensaje (`MessageLabel.Cat`,
validada contra `categories`). La tarjeta Categorías de En vivo seguía contando con
`Classify`, que confunde ironía, lunfardo y saludos. La regla de `AGENTS.md` decía que la
categoría de reglas era la única del CSV y de Categorías.

## Decisión

Con Claude prendido y mensajes del alcance ya etiquetados, Categorías cuenta cada mensaje
con su `Cat` de Claude (`catRowsWith`). El spam se cuenta siempre por reglas, porque
`batchPrompt` no se lo manda a Claude. Los que todavía no entraron en una tanda también se
cuentan por reglas, y la tarjeta dice cuántos son. Sin Claude, o sin ninguna etiqueta, la
tarjeta queda como antes. El CSV, el tile "Preguntas" por reglas y `Classify` no cambian.

## Consecuencias

- La tarjeta mezcla dos fuentes mientras llegan las tandas (unos 30 s de atraso). La nota lo
  dice para que no parezca un error.
- No hay pedidos nuevos a Claude ni gasto extra: se reusan las etiquetas guardadas.
- Descartado: guardar la categoría en `datos.jsonl`, porque cambiaría el formato de
  `Message`. También descartado mandar el CSV con la categoría de Claude, porque el CSV
  está pensado para que Claude lo analice aparte.
