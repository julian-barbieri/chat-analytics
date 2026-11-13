package main

import (
	"maps"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Lo que se despliega en el detalle de Programas: el chat completo con sus
// filtros, los mensajes de una franja de 5 min y los de cada tema o grupo de
// preguntas. Todo sale de los mensajes y etiquetas que el detalle ya leyó:
// nada de lecturas extra del Store ni pedidos a Claude.

const (
	chatPageSize = 200 // mensajes por página del chat del programa
	maxChatPag   = 50  // tope de páginas: más que eso no se lee en pantalla
	maxSearch    = 100 // runas del texto buscado
	cqFolds      = 8   // preguntas del resumen (groups(raw.Preguntas, 8))
)

// programFolds es lo que se puede desplegar en el detalle: lista cerrada, en
// el orden en que se escribe "abrir". tema1…tema5 son los temas del resumen,
// qg1…qg30 los grupos de preguntas de las tandas (maspreguntas, los que pasan
// de qgTop) y cq1…cq8 las preguntas del resumen, por posición como en En vivo.
// El chat ya no se pliega: tiene su pestaña.
var programFolds = slices.Concat([]string{"maspreguntas"}, numbered("tema", 5), numbered("qg", qgFolds), numbered("cq", cqFolds), catFoldKeys(), numbered("rec", recFolds))

// chatSents son las opciones del filtro de sentimiento ("sin": sin etiqueta).
var chatSents = []struct{ Key, Label, Class string }{
	{"positivo", "Positivo", "pos"}, {"neutro", "Neutro", "neu"}, {"negativo", "Negativo", "neg"}, {"sin", "Sin etiquetar", ""},
}

// catOne es el nombre de cada categoría en singular, para el tag de un mensaje.
var catOne = map[string]string{
	CatPregunta: "Pregunta", CatQueja: "Queja", CatElogio: "Elogio",
	CatInsulto: "Insulto", CatSpam: "Spam", CatOtro: "Otro",
}

// slotTabs son las pestañas de la franja elegida en Programas: como en En
// vivo, con "Todos" adelante (la participación es de todos los mensajes).
var slotTabs = []struct{ Key, Sentiment, Label string }{
	{"todos", "", "Todos"}, {"neg", "negativo", "Negativos"}, {"neu", "neutro", "Neutros"}, {"pos", "positivo", "Positivos"},
}

// readDetail lee los parámetros del detalle. Nada de lo que no esté en las
// listas cerradas se guarda (así no se refleja); el texto buscado se recorta.
func readDetail(v url.Values, q *programsQuery) {
	open := map[string]bool{}
	chat := false
	for _, raw := range v["abrir"] {
		for _, sec := range strings.Split(raw, ",") {
			if slices.Contains(programFolds, sec) {
				open[sec] = true
			}
			chat = chat || sec == "chat"
		}
	}
	q.Abrir = foldsValue(open)
	for _, t := range programTabs[1:] {
		if t.Key == v.Get("pestana") {
			q.Pestana = t.Key
		}
	}
	// Un link de antes con el chat desplegado abre su pestaña.
	if chat && q.Pestana == "" {
		q.Pestana = "chat"
	}
	if r := []rune(strings.TrimSpace(v.Get("buscar"))); len(r) > maxSearch {
		q.Buscar = string(r[:maxSearch])
	} else {
		q.Buscar = string(r)
	}
	q.Cat = pickOf(v.Get("cat"), categories)
	q.Senti = pickOf(v.Get("senti"), sentKeys())
	q.Tema = pickOf(v.Get("tema"), numbered("tema", 5))
	if n, err := strconv.Atoi(v.Get("pag")); err == nil && n > 0 {
		q.Pag = min(n, maxChatPag)
	}
	if n, err := strconv.ParseInt(v.Get("franja"), 10, 64); err == nil && n > 0 {
		q.Franja = n
	}
	q.Sent = pickOf(v.Get("sent"), []string{"todos", "neg", "neu", "pos"})
	q.Rol = pickOf(v.Get("rol"), recRoleKeys())
	q.Desde = pickOf(v.Get("desde"), []string{"part", "sent"})
}

func pickOf(v string, list []string) string {
	if slices.Contains(list, v) {
		return v
	}
	return ""
}

func sentKeys() []string {
	var out []string
	for _, s := range chatSents {
		out = append(out, s.Key)
	}
	return out
}

// foldsValue es "abrir" en el orden de programFolds, sin repetidos.
func foldsValue(open map[string]bool) string {
	var out []string
	for _, sec := range programFolds {
		if open[sec] {
			out = append(out, sec)
		}
	}
	return strings.Join(out, ",")
}

func (q programsQuery) openSet() map[string]bool {
	open := map[string]bool{}
	for _, sec := range strings.Split(q.Abrir, ",") {
		if sec != "" {
			open[sec] = true
		}
	}
	return open
}

// fold es la flecha de una sección: pide el parcial con la sección cambiada
// en "abrir", así sigue abierta en el refresco de 60 s.
func (q programsQuery) fold(sec string) Fold {
	open := q.openSet()
	next := maps.Clone(open)
	next[sec] = !open[sec]
	q.Abrir = foldsValue(next)
	return Fold{Open: open[sec], Href: q.Partial()}
}

// ChatLine es un mensaje del chat del programa con sus tags.
type ChatLine struct {
	ID, Hour, Author, Text     string
	Cat, CatLabel              string
	Sent, SentLabel, SentClass string // vacíos sin etiqueta de Claude
	Group                      string // texto del grupo de preguntas
	Topics                     []string
	Kind, Amount               string // solo en los Super Chats del programa
}

// ChatView es la página pedida del chat filtrado.
type ChatView struct {
	Rows           []ChatLine
	Total, Matched int // mensajes de chat del tramo y los que pasan el filtro
	More           bool
}

// chatFilter son los filtros del chat, ya validados por readDetail.
type chatFilter struct {
	Text, Cat, Sent, Tema string
	Pag                   int
}

// programChat arma el chat del programa en orden de hora con sus tags: la
// categoría como la tarjeta Categorías (catOf, ADR 0010), el sentimiento y el
// grupo de las tandas, y los temas del resumen que incluyen al mensaje.
// Sin etiquetas (labels nil), todo por reglas y sin sentimiento.
func programChat(msgs []Message, labels map[string]MessageLabel, groups map[string]BatchGroup, topics []ClaudeGroup, f chatFilter) ChatView {
	tops := topicIndex(topics)
	tema := -1
	if f.Tema != "" {
		tema, _ = strconv.Atoi(strings.TrimPrefix(f.Tema, "tema"))
		tema--
	}
	needle := foldSearch(f.Text)
	chat := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Source == "chat" {
			chat = append(chat, m)
		}
	}
	sort.SliceStable(chat, func(i, j int) bool { return chat[i].At.Before(chat[j].At) })
	limit := chatPageSize * max(f.Pag, 1)
	v := ChatView{Total: len(chat)}
	for _, m := range chat {
		l, labeled := labels[m.ID]
		cat, _ := catOf(m, labels)
		switch {
		case f.Cat != "" && cat != f.Cat,
			f.Sent == "sin" && labeled,
			f.Sent != "" && f.Sent != "sin" && l.Sentiment != f.Sent,
			tema >= 0 && !slices.Contains(tops[m.ID], tema),
			needle != "" && !strings.Contains(foldSearch(m.Text), needle) && !strings.Contains(foldSearch(m.Author), needle):
			continue
		}
		v.Matched++
		if len(v.Rows) >= limit {
			continue
		}
		v.Rows = append(v.Rows, chatLine(m, labels, groups, topics, tops))
	}
	v.More = v.Matched > len(v.Rows)
	return v
}

// topicIndex dice, por ID de mensaje, en qué temas del resumen está.
func topicIndex(topics []ClaudeGroup) map[string][]int {
	tops := map[string][]int{}
	for i, tp := range topics {
		for _, id := range groupIDs(tp) {
			tops[id] = append(tops[id], i)
		}
	}
	return tops
}

// chatLine es la fila de un mensaje con sus tags, la misma en el chat y en
// los Super Chats del programa.
func chatLine(m Message, labels map[string]MessageLabel, groups map[string]BatchGroup, topics []ClaudeGroup, tops map[string][]int) ChatLine {
	cat, _ := catOf(m, labels)
	r := ChatLine{ID: m.ID, Hour: m.At.Local().Format("15:04"), Author: m.Author, Text: m.Text, Cat: cat, CatLabel: catOne[cat]}
	if l, ok := labels[m.ID]; ok {
		r.Sent = l.Sentiment
		for _, s := range chatSents {
			if s.Key == l.Sentiment {
				r.SentLabel, r.SentClass = s.Label, s.Class
			}
		}
		if g, ok := groups[l.Group]; ok && l.Group != "" {
			r.Group = g.Text
		}
	}
	for _, i := range tops[m.ID] {
		r.Topics = append(r.Topics, topics[i].Text)
	}
	return r
}

// programSupers son los Super Chats y Super Stickers del tramo en orden de
// hora, con los mismos tags que en el chat del programa y su tipo y monto.
// Son pocos por programa: van todos, sin filtros ni páginas.
func programSupers(msgs []Message, labels map[string]MessageLabel, groups map[string]BatchGroup, topics []ClaudeGroup) []ChatLine {
	var paid []Message
	for _, m := range msgs {
		if m.Super != "" {
			paid = append(paid, m)
		}
	}
	sort.SliceStable(paid, func(i, j int) bool { return paid[i].At.Before(paid[j].At) })
	tops := topicIndex(topics)
	var rows []ChatLine
	for _, m := range paid {
		r := chatLine(m, labels, groups, topics, tops)
		r.Kind, r.Amount = superLabels[m.Super], m.AmountText
		if strings.TrimSpace(r.Text) == "" && m.Sticker != "" {
			r.Text = "Super Sticker: " + m.Sticker
		}
		rows = append(rows, r)
	}
	return rows
}

// foldSearch es el texto para buscar: minúsculas y sin tildes.
func foldSearch(s string) string { return unaccent.Replace(strings.ToLower(strings.TrimSpace(s))) }

// ProgramChat es la sección "Chat del programa": plegada, y abierta con la
// lista y el formulario de filtros.
type ProgramChat struct {
	N               int // mensajes de chat del tramo
	Fold            Fold
	View            *ChatView // solo abierta
	Hidden          []HiddenField
	Buscar          string
	Cats, Sents     []ChannelOption
	Temas           []ChannelOption // solo con temas del resumen
	MoreHref, Clear string
	Capped          string // con maxChatPag páginas: no hay "Ver más"
	Filtered        bool
}

// HiddenField es un parámetro que el formulario del chat repite tal cual.
type HiddenField struct{ Name, Value string }

// programChatSection arma la sección del chat; la lista, solo abierta.
func programChatSection(msgs []Message, labels map[string]MessageLabel, groups map[string]BatchGroup, topics []ClaudeGroup, q programsQuery) ProgramChat {
	c := ProgramChat{Fold: Fold{Open: q.Pestana == "chat"}}
	for _, m := range msgs {
		if m.Source == "chat" {
			c.N++
		}
	}
	if !c.Fold.Open {
		return c
	}
	// Un filtro sin su select (sin etiquetas o sin temas) no se aplica: la
	// lista quedaría vacía sin nada visible para sacarlo.
	if len(labels) == 0 {
		q.Senti = ""
	}
	if len(topics) == 0 {
		q.Tema = ""
	}
	v := programChat(msgs, labels, groups, topics, chatFilter{q.Buscar, q.Cat, q.Senti, q.Tema, q.Pag})
	c.View, c.Buscar = &v, q.Buscar
	c.Filtered = q.Buscar != "" || q.Cat != "" || q.Senti != "" || q.Tema != ""
	switch {
	case v.More && max(q.Pag, 1) < maxChatPag:
		more := q
		more.Pag = max(q.Pag, 1) + 1
		c.MoreHref = more.Partial()
	case v.More:
		c.Capped = "Se muestran los primeros " + miles(len(v.Rows)) + ": usá la búsqueda o los filtros para ver el resto."
	}
	base := q
	base.Buscar, base.Cat, base.Senti, base.Tema, base.Pag = "", "", "", "", 0
	c.Clear = base.Partial()
	vals := base.values()
	for _, k := range slices.Sorted(maps.Keys(vals)) {
		c.Hidden = append(c.Hidden, HiddenField{k, vals.Get(k)})
	}
	c.Cats = []ChannelOption{{"", "Todas", q.Cat == ""}}
	for _, k := range categories {
		c.Cats = append(c.Cats, ChannelOption{k, catOne[k], k == q.Cat})
	}
	if len(labels) > 0 {
		c.Sents = []ChannelOption{{"", "Todos", q.Senti == ""}}
		for _, s := range chatSents {
			c.Sents = append(c.Sents, ChannelOption{s.Key, s.Label, s.Key == q.Senti})
		}
	}
	if len(topics) > 0 {
		c.Temas = []ChannelOption{{"", "Todos", q.Tema == ""}}
		for i, tp := range topics[:min(len(topics), 5)] {
			k := "tema" + strconv.Itoa(i+1)
			c.Temas = append(c.Temas, ChannelOption{k, tp.Text, k == q.Tema})
		}
	}
	return c
}

// validSlot dice si franja (unix) es el inicio de una franja de 5 min del
// tramo, de su inicio a su fin (o a ahora si sigue al aire).
func validSlot(franja int64, sg Segment, now time.Time) bool {
	end := sg.End
	if sg.State == SegLive || end.After(now) {
		end = now
	}
	at := time.Unix(franja, 0)
	return franja > 0 && slotOf(at).Equal(at) && !at.Before(slotOf(sg.Start)) && !at.After(slotOf(end))
}

// programSlotLinks le pone el link de su franja a cada barra de
// Participación y a cada fila de Sentimiento, y arma el detalle de la franja
// elegida con los mensajes del tramo de esos 5 min. q.Franja ya viene
// validada por programDetail (validSlot): 0 si no es una franja del tramo.
func programSlotLinks(d *ProgramDetail, slots []Slot, msgs []Message, labels map[string]MessageLabel, q programsQuery) {
	link := func(at time.Time, desde string) string {
		l := q
		l.Franja, l.Desde, l.Sent = at.Unix(), desde, ""
		return l.Partial()
	}
	at := time.Unix(q.Franja, 0)
	ok := q.Franja > 0
	// El id deja el foco en la barra o la fila tocada al reemplazar #programas.
	id := func(prefix string, t time.Time) string { return prefix + strconv.FormatInt(t.Unix(), 10) }
	if d.Part != nil {
		chart := d.Part["Chart"].(map[string]any)
		bars := chart["Bars"].([]Bar)
		for i := range bars {
			if i < len(slots) {
				bars[i].Href, bars[i].ID = link(slots[i].Start, "part"), id("pb-", slots[i].Start)
				bars[i].Sel = ok && slots[i].Start.Equal(at)
			}
		}
		chart["Links"] = true
	}
	if c := d.SentChart; c != nil {
		for i := range c.Bars {
			b := &c.Bars[i]
			b.Href, b.ID = link(b.Start, "sent"), id("sf-", b.Start)
			b.Sel = ok && b.Start.Equal(at)
		}
		// Solo desde Sentimiento: elegida desde Participación, ninguna barra
		// queda marcada y no tiene sentido atenuarlas todas.
		c.Sel = ok && q.Desde == "sent"
	}
	if !ok {
		return
	}
	d.SlotIn = "part"
	if q.Desde == "sent" && d.SentChart != nil {
		d.SlotIn = "sent"
	}
	end := at.Add(slotStep)
	var in []Message
	for _, m := range msgs {
		if m.Source == "chat" && !m.At.Before(at) && m.At.Before(end) {
			in = append(in, m)
		}
	}
	closeQ := q
	closeQ.Franja, closeQ.Desde, closeQ.Sent = 0, "", ""
	det := &SlotDetail{Title: "Mensajes de las " + at.Local().Format("15:04") + " a " + end.Local().Format("15:04"), Close: closeQ.Partial()}
	tab := q.Sent
	if tab == "" {
		tab = "todos"
		if d.SlotIn == "sent" {
			tab = "neg"
		}
	}
	if len(labels) == 0 {
		tab = "todos"
	}
	by := map[string][]Message{}
	for _, m := range in {
		if l, has := labels[m.ID]; has {
			by[l.Sentiment] = append(by[l.Sentiment], m)
		}
	}
	for _, t := range slotTabs {
		list := in
		if t.Sentiment != "" {
			list = by[t.Sentiment]
		}
		on := t.Key == tab
		if len(labels) > 0 {
			tq := q
			tq.Desde, tq.Sent = d.SlotIn, t.Key
			det.Tabs = append(det.Tabs, SlotTab{Key: t.Key, Label: t.Label + " (" + miles(len(list)) + ")", Href: tq.Partial(), On: on})
		}
		if !on {
			continue
		}
		switch {
		case len(list) > 0:
			det.List = foldMsgs(list, len(list))
		case t.Sentiment != "":
			det.Empty = "Ningún mensaje " + strings.ToLower(strings.TrimSuffix(t.Label, "s")) + " en esta franja."
		default:
			det.Empty = "Ningún mensaje de chat en esta franja."
		}
	}
	d.Slot = det
}

// programFoldLists le pone la flecha a cada tema y pregunta del resumen y a
// cada grupo de preguntas de las tandas con más de un mensaje, y arma la
// lista de los desplegados.
func programFoldLists(d *ProgramDetail, a *ClaudeAnalysis, msgs []Message, labels map[string]MessageLabel, q programsQuery) {
	byID := messagesByID(msgs)
	if d.Claude != nil && a != nil {
		groupFolds(d.Claude.Topics, a.Result.Topics, "tema", byID, q)
		groupFolds(d.Claude.Questions, a.Result.Questions, "cq", byID, q)
	}
	for i := range d.QGroups {
		r := &d.QGroups[i]
		if i >= qgFolds || r.N < 2 {
			continue
		}
		r.Key = "qg" + strconv.Itoa(i+1)
		r.Fold = q.fold(r.Key)
		if !r.Fold.Open {
			continue
		}
		var in []Message
		for _, m := range msgs {
			if l, ok := labels[m.ID]; ok && l.Group == r.ID {
				in = append(in, m)
			}
		}
		r.List = foldMsgs(in, len(in))
	}
	// Como En vivo: con muchos grupos la tarjeta tapaba el resto del detalle.
	// Se corta después de numerar, así el sexto sigue siendo qg6.
	if len(d.QGroups) > qgTop {
		d.QGroups, d.QRest = d.QGroups[:qgTop], d.QGroups[qgTop:]
		d.QMore = q.fold("maspreguntas")
	}
}

// groupFolds le pone la flecha a los ítems de un resumen con más de un mensaje.
func groupFolds(items []ClaudeItem, gs []ClaudeGroup, prefix string, byID map[string]Message, q programsQuery) {
	for i := range items {
		if i >= len(gs) || gs[i].Msgs < 2 {
			continue
		}
		it := &items[i]
		it.Key = prefix + strconv.Itoa(i+1)
		if !slices.Contains(programFolds, it.Key) {
			it.Key = ""
			continue
		}
		it.Fold = q.fold(it.Key)
		if !it.Fold.Open {
			continue
		}
		var in []Message
		for _, id := range groupIDs(gs[i]) {
			if m, ok := byID[id]; ok {
				in = append(in, m)
			}
		}
		it.List = foldMsgs(in, gs[i].Msgs)
	}
}
