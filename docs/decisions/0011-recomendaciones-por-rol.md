# ADR 0011: Recomendaciones por rol en dos capas, con las señales en Go y la redacción de Claude

- **Estado:** aceptada
- **Fecha:** 2026-11-13

## Contexto

Se pidió que el monitor genere solo recomendaciones para distintas personas de la radio
(producción, redes, técnica, comercial, dirección) a partir del chat de cada programa, y
que se lean por programa y en un parte diario o semanal. Casi todo lo que hace falta ya
se calcula con reglas: grupos de preguntas, quejas técnicas, momentos con más
participación, lo normal de 4 semanas, habituales en riesgo, cortes y Super Chats. Pedirle
a un modelo que recomiende directamente sobre el chat sería caro, mandaría muchos más
mensajes a Anthropic y dejaría números sin verificar.

## Decisión

- **Capa 1 (Go, gratis):** `recSignals` calcula señales con umbrales fijos. Cada una
  tiene un ID estable, los roles a los que puede ir, la evidencia (`Facts`) y los IDs de
  los mensajes que la muestran. Las ven siempre la pestaña Recomendaciones y el parte,
  con un texto fijo por tipo y rol (`recText`).
- **Capa 2 (Claude, opcional):** cuando un programa terminado ya tiene el resumen final
  de todos sus pedazos, `RunAnalyst` manda sus señales con el modelo de resúmenes. Cada
  señal va con hasta 5 ejemplos: solo texto, sin autor, sin ID y sin spam. Claude
  devuelve título, acción y prioridad por señal y rol. Reglas del pedido:
  - va con la prioridad más baja: después del botón, las tandas y los resúmenes;
  - sale solo si su peor caso (`recWorstCase`) entra en el tope;
  - un programa sin señales se anota sin pedido.
- **Qué se toma de la respuesta:** `validRecResult` deja solo lo que cuadra con una
  señal mandada:
  - un rol de esa señal;
  - una prioridad de la lista;
  - textos cortos;
  - ningún número que no esté en la señal.

  Lo demás queda con el texto fijo. La evidencia la pone siempre Go.
- **Dónde se guarda:**
  - las redacciones, en `datos.recomendaciones.jsonl` (30 días desde el fin del
    programa; la vista previa lo copia);
  - las marcas Hecha / No sirve, en `datos.recmarcas.jsonl`, fuera del `Store` y solo
    desde la PC del monitor (la vista previa no lo copia).
- **Cálculo en las pantallas:** la pestaña y el parte recalculan las señales con los
  datos de ahora y les aplican la redacción guardada por ID de señal. El parte se arma
  una vez por día en memoria (`parteCache`), leyendo 4 semanas por canal una sola vez.

## Consecuencias

- **Lo que se gana:**
  - sin Claude la función sirve igual;
  - con Claude, el costo queda acotado: ~US$ 0,03 por programa con señales con
    Sonnet 5.5;
  - no se pueden colar números inventados;
  - una marca de "respondida" o un dato que cambia se ven enseguida.
- **Lo que se pierde:** Claude no descubre cosas que no están en una señal. Una señal
  nueva es código y un caso en `TestRecSignals`.
- **Alternativas descartadas:**
  - **Mandar el chat entero y pedir recomendaciones libres:** caro, más datos afuera y
    números sin verificar.
  - **Guardar las señales en disco:** duplicaría datos de la API y quedaría viejo frente
    a las marcas.
  - **Mandar el parte por Slack o mail:** fuera de alcance sin consultar.
