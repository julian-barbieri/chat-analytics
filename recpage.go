package main

import (
	"cmp"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// Lo que muestra las recomendaciones (recomendaciones-por-rol): la pestaña
// Recomendaciones de Programas, las marcas Hecha / No sirve y el parte por
// rol. Los handlers solo leen el Store (copias) y nunca llaman a Claude: las
// redacciones las guarda el analista.

// recInputOf junta lo que hace falta para las señales de un programa con
// lecturas del Store. answered puede ser nil (el analista no tiene marcas).
func recInputOf(st *Store, answered func(string) bool, ps ProgramSeg, msgs []Message, a *ClaudeAnalysis, videos []Video, now time.Time) recInput {
	var ems []Emission
	if ps.Seg.Program != "" {
		ems, _ = programHistoryOf(st, ps, videos, now)
	}
	slots, _, _, _ := programSlotsOf(st, ps, msgs, ems, now)
	return withLabels(st, recInput{Seg: ps, Msgs: msgs, Answered: answered, Analysis: a, Slots: slots, Ems: ems, Now: now})
}

// withLabels suma las etiquetas de las tandas de los mensajes y sus grupos.
func withLabels(st *Store, in recInput) recInput {
	in.Labels = st.Labels(messageIDs(in.Msgs))
	var gids []string
	for _, l := range in.Labels {
		if l.Group != "" {
			gids = append(gids, l.Group)
		}
	}
	in.Groups = st.Groups(gids)
	return in
}

// savedRecs es la última redacción de Claude de un programa, si hay.
func (s *Server) savedRecs(ps ProgramSeg) *ProgramRecs {
	if p, ok := latestRecs(s.st.Recs(ps.Channel.ID, ps.Seg.Start))[ps.Key]; ok {
		return &p
	}
	return nil
}

// RecsView es la pestaña Recomendaciones.
type RecsView struct {
	Cards  []RecCard
	Roles  []RoleLink
	Claude bool   // alguna tarjeta la redactó Claude
	Marks  bool   // hay dónde guardar las marcas: sin eso, no hay botones
	Empty  string // por qué no hay tarjetas
	Parte  string // link al parte del rol elegido
}

// RoleLink es un botón del filtro "Para quién".
type RoleLink struct {
	Label, Page, Partial string
	On                   bool
}

// RecCard es una recomendación para un rol, con su evidencia y su marca.
type RecCard struct {
	Key                           string // rec1…: lo plegado de "abrir"
	Role, RoleLabel, Prio, Signal string
	Title, Action, Facts          string
	Claude                        bool
	State, StateLabel             string
	N                             int // mensajes que la muestran
	Fold                          Fold
	List                          *FoldMsgs // solo si está desplegada
	Href, Hint                    string    // clip: el momento en YouTube
	Hidden                        []Field   // el formulario de las marcas
	Marks                         bool      // con botones Hecha / No sirve
}

// Field es un input oculto de un formulario.
type Field struct{ Name, Value string }

// recFolds es cuántas tarjetas se pueden desplegar (rec1…recN en "abrir").
const recFolds = 30

// recsView arma la pestaña de un programa: las señales de ahora, la
// redacción guardada de Claude para las que la tienen y las marcas.
func (s *Server) recsView(ps ProgramSeg, msgs []Message, a *ClaudeAnalysis, snap Snapshot, now time.Time, q programsQuery) RecsView {
	return s.recsViewOf(ps, recSignals(recInputOf(s.st, s.answers.Answered, ps, msgs, a, snap.Videos, now)), msgs, snap, now, q)
}

// recsViewOf es recsView con las señales ya calculadas (el POST de marcas
// las calcula para validar y no las vuelve a calcular).
func (s *Server) recsViewOf(ps ProgramSeg, sigs []RecSignal, msgs []Message, snap Snapshot, now time.Time, q programsQuery) RecsView {
	saved := s.savedRecs(ps)
	v := RecsView{Parte: "/parte?rol=" + url.QueryEscape(q.Rol), Marks: s.recMarks != nil}
	for _, r := range append([]struct{ Key, Label string }{{"", "Todos"}}, recRoles...) {
		rq := q
		rq.Rol = r.Key
		v.Roles = append(v.Roles, RoleLink{Label: r.Label, Page: rq.Page(), Partial: rq.Partial(), On: r.Key == q.Rol})
	}
	if len(sigs) == 0 {
		v.Empty = "Este programa no dejó recomendaciones."
		return v
	}
	bySig := map[string]RecSignal{}
	for _, sg := range sigs {
		bySig[sg.ID] = sg
	}
	claude := map[string]bool{}
	if saved != nil {
		for _, r := range saved.Recs {
			claude[r.Signal] = true
		}
	}
	byID := messagesByID(msgs)
	for i, r := range recsFor(sigs, saved) {
		if q.Rol != "" && r.Role != q.Rol {
			continue
		}
		sg := bySig[r.Signal]
		c := RecCard{Role: r.Role, RoleLabel: recRoleLabel(r.Role), Prio: r.Prio, Signal: r.Signal, Title: r.Title, Action: r.Action,
			Facts: sg.Facts, Claude: claude[r.Signal], N: len(sg.IDs)}
		c.State = s.recMarks.State(recID(ps.Key, r.Signal, r.Role))
		c.StateLabel = recStates[c.State]
		if sg.Kind == "clip" && sg.VideoID != "" {
			c.Href, c.Hint = momentLink(snap.Videos, sg.VideoID, sg.At, now)
		}
		// La clave de lo desplegado es su lugar entre todas las del programa,
		// antes de filtrar y de ordenar: no cambia al marcar ni al filtrar.
		if i < recFolds && c.N > 0 {
			c.Key = fmt.Sprintf("rec%d", i+1)
		}
		v.Claude = v.Claude || c.Claude
		v.Cards = append(v.Cards, c)
	}
	sortRecCards(v.Cards, func(c RecCard) string { return c.Prio }, func(c RecCard) bool { return c.State != "" })
	for i := range v.Cards {
		c := &v.Cards[i]
		if c.Key != "" {
			c.Fold = q.fold(c.Key)
			if c.Fold.Open {
				var list []Message
				for _, id := range bySig[c.Signal].IDs {
					if m, ok := byID[id]; ok {
						list = append(list, m)
					}
				}
				c.List = foldMsgs(list, c.N)
			}
		}
		c.Marks = v.Marks
		c.Hidden = []Field{{"tramo", ps.Key}, {"senal", c.Signal}, {"para", c.Role}, {"pestana", "recomendaciones"},
			{"dia", q.Dia}, {"canal", q.Canal}, {"rol", q.Rol}, {"abrir", q.Abrir}}
	}
	if len(v.Cards) == 0 {
		v.Empty = "Nada para " + recRoleLabel(q.Rol) + " en este programa."
	}
	return v
}

// finalOf es el resumen final de un programa (el del último pedazo que tenga).
func (s *Server) finalOf(ps ProgramSeg) *ClaudeAnalysis {
	finals := map[string]ClaudeAnalysis{}
	for _, a := range s.st.Analyses(ps.Channel.ID, ps.Seg.Start) {
		if !a.Partial {
			finals[a.Key] = a
		}
	}
	return finalAnalysis(ps, finals)
}

// recMarkPost es POST /recomendaciones/marca: Hecha, No sirve o Deshacer de
// una tarjeta. Responde la pestaña con la marca puesta. Solo escribe
// datos.recmarcas.jsonl: ni el Store ni Claude.
func (s *Server) recMarkPost(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) || !localRequest(r) {
		http.Error(w, "Para marcar una recomendación, abrí el dashboard en la PC del monitor.", http.StatusForbidden)
		return
	}
	if s.recMarks == nil {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	f := r.PostForm
	state, ok := map[string]string{"hecha": "hecha", "nosirve": "nosirve", "deshacer": ""}[f.Get("estado")]
	if !ok || !slices.Contains(recRoleKeys(), f.Get("para")) {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	now := s.clock()
	snap := s.st.Status()
	// El tramo del día de la vista: en "Ayer", un programa que cruzó la
	// medianoche es el cortado a las 00:00, como en la tarjeta.
	q := readPrograms(f, snap.Channels)
	ps, msgs, found := s.findProgram(f.Get("tramo"), q.Dia, snap, now)
	if !found {
		http.Error(w, "Ese programa ya no está guardado.", http.StatusBadRequest)
		return
	}
	sigs := recSignals(recInputOf(s.st, s.answers.Answered, ps, msgs, s.finalOf(ps), snap.Videos, now))
	i := slices.IndexFunc(sigs, func(sg RecSignal) bool { return sg.ID == f.Get("senal") && slices.Contains(sg.Roles, f.Get("para")) })
	if i < 0 {
		http.Error(w, "Esa recomendación ya no está.", http.StatusBadRequest)
		return
	}
	if err := s.recMarks.Mark(recID(ps.Key, sigs[i].ID, f.Get("para")), state, now); err != nil {
		log.Printf("aviso: no pude guardar la marca de la recomendación: %v", err)
		http.Error(w, "No pude guardar la marca: probá de nuevo.", http.StatusInternalServerError)
		return
	}
	q.Tramo, q.Pestana = ps.Key, "recomendaciones"
	s.render(w, "recs", s.recsViewOf(ps, sigs, msgs, snap, now, q))
}

// Parte por rol: lo de ayer o de los últimos 7 días, para leer, copiar o
// imprimir. Se arma una vez por día local (parteCache) y se filtra por rol y
// por las marcas en cada pedido.

// parteItem es una recomendación del parte.
type parteItem struct {
	Rec
	ID, Kind, RoleLabel, Facts string
	Channel, Program           string
	Start                      time.Time
	Times                      int // emisiones en las que salió (semana)
	State, StateLabel          string
}

// Where es "Radio Test · AYER · 12/11 19:00".
func (p parteItem) Where() string {
	return p.Channel + " · " + programLabel(p.Program) + " · " + p.Start.Local().Format("02/01 15:04")
}

type parteBuilt struct {
	items          []parteItem
	programs, msgs int
}

// parteCache guarda el parte armado de cada período: armarlo lee 4 semanas
// de mensajes por canal. Con su mutex, solo en memoria.
type parteCache struct {
	mu    sync.Mutex
	key   map[string]string
	built map[string]parteBuilt
}

var partePeriods = []struct{ Key, Label, Title string }{
	{"dia", "Ayer", "Parte de ayer"}, {"semana", "Últimos 7 días", "Parte de los últimos 7 días"},
}

// parteDays son los días del período: ayer o los 7 anteriores a hoy.
func parteDays(periodo string, now time.Time) []time.Time {
	n := 1
	if periodo == "semana" {
		n = 7
	}
	var days []time.Time
	for i := 1; i <= n; i++ {
		days = append(days, dayStart(now).AddDate(0, 0, -i))
	}
	return days
}

// parte devuelve el parte del período, armado una vez por día y vuelto a
// armar si llegaron redacciones o resúmenes nuevos.
func (s *Server) parte(periodo string, now time.Time) parteBuilt {
	days := parteDays(periodo, now)
	oldest := days[len(days)-1].Add(-12 * time.Hour)
	snap := s.st.Status()
	// Se vuelve a armar si cambia lo que usa: los canales, los resúmenes y
	// redacciones de los programas del período (no los de hoy) o las
	// preguntas marcadas como respondidas.
	today, n := dayStart(now), 0
	for _, ch := range snap.Channels {
		for _, r := range s.st.Recs(ch.ID, oldest) {
			if r.From.Before(today) {
				n++
			}
		}
		for _, a := range s.st.Analyses(ch.ID, oldest) {
			if a.From.Before(today) {
				n++
			}
		}
	}
	key := fmt.Sprintf("%s|%d|%d|%d", today.Format("2006-01-02"), len(snap.Channels), n, s.answers.Version())
	c := &s.parteC
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.key[periodo] == key {
		return c.built[periodo]
	}
	b := s.buildParte(snap, days, periodo == "semana", now)
	if c.key == nil {
		c.key, c.built = map[string]string{}, map[string]parteBuilt{}
	}
	c.key[periodo], c.built[periodo] = key, b
	return b
}

// chanHistory son 4 semanas de un canal leídas una sola vez: los programas,
// los mensajes por programa y los espectadores por vivo.
type chanHistory struct {
	segs    []ProgramSeg
	byProg  map[string][]Message // por programOf
	byLower map[string][]Message // por programOf en minúsculas (fillEmissions)
	viewers map[string][]ViewerSample
	titles  map[string]string
	from    time.Time
}

func (s *Server) chanHistory(ch ChannelStatus, videos []Video, now time.Time) chanHistory {
	h := chanHistory{byProg: map[string][]Message{}, byLower: map[string][]Message{}, viewers: map[string][]ViewerSample{}, titles: videoTitles(videos)}
	var days []time.Time
	for i := range int(loyaltyWindow / (24 * time.Hour)) {
		days = append(days, dayStart(now).AddDate(0, 0, -i))
	}
	h.from = days[len(days)-1]
	h.segs = programSegs([]ChannelStatus{ch}, s.st.Lives(h.from.Add(-12*time.Hour)), videos, days, now)
	ids := map[string]bool{}
	for _, ps := range h.segs {
		for _, id := range ps.videoIDs() {
			ids[id] = true
		}
	}
	if len(ids) == 0 {
		return h
	}
	for _, m := range s.st.MessagesOf(ids, h.from.Add(-segmentLead)) {
		p := programOf(m, h.titles)
		h.byProg[p] = append(h.byProg[p], m)
		h.byLower[strings.ToLower(p)] = append(h.byLower[strings.ToLower(p)], m)
	}
	for id := range ids {
		h.viewers[id] = s.st.Viewers(id, h.from)
	}
	return h
}

// ems son las emisiones de un programa en las 4 semanas, como programHistoryOf.
func (h chanHistory) ems(program string, now time.Time) []Emission {
	ems := programEmissions(h.segs, program, now)
	fillEmissions(ems, h.byLower[strings.ToLower(program)], h.titles)
	for id, vs := range h.viewers {
		for i := range ems {
			ems[i].addViewers(id, vs)
		}
	}
	return ems
}

// buildParte calcula las recomendaciones de los programas terminados de esos
// días en los canales configurados. Con merge (semana), las del mismo tipo,
// programa y rol van juntas: la más nueva, con cuántas veces salió.
func (s *Server) buildParte(snap Snapshot, days []time.Time, merge bool, now time.Time) parteBuilt {
	var b parteBuilt
	oldest := days[len(days)-1].Add(-12 * time.Hour)
	samples := s.st.Lives(oldest)
	for _, ch := range snap.Channels {
		h := s.chanHistory(ch, snap.Videos, now)
		finals := map[string]ClaudeAnalysis{}
		for _, a := range s.st.Analyses(ch.ID, oldest) {
			if !a.Partial {
				finals[a.Key] = a
			}
		}
		saved := latestRecs(s.st.Recs(ch.ID, oldest))
		for _, ps := range programSegs([]ChannelStatus{ch}, samples, snap.Videos, days, now) {
			if ps.Seg.State == SegLive || ps.Seg.End.After(now) {
				continue
			}
			msgs := programMessages(h.byProg[ps.Seg.Program], ps, h.titles)
			var ems []Emission
			if ps.Seg.Program != "" {
				ems = h.ems(ps.Seg.Program, now)
			}
			slots, _, _, _ := programSlotsOf(s.st, ps, msgs, ems, now)
			in := withLabels(s.st, recInput{Seg: ps, Msgs: msgs, Answered: s.answers.Answered, Analysis: finalAnalysis(ps, finals), Slots: slots, Ems: ems, Now: now})
			sigs := recSignals(in)
			b.programs++
			b.msgs += len(msgs)
			var sv *ProgramRecs
			if p, ok := saved[ps.Key]; ok {
				sv = &p
			}
			kinds := map[string]string{}
			facts := map[string]string{}
			for _, sg := range sigs {
				kinds[sg.ID], facts[sg.ID] = sg.Kind, sg.Facts
			}
			for _, r := range recsFor(sigs, sv) {
				b.items = append(b.items, parteItem{Rec: r, ID: recID(ps.Key, r.Signal, r.Role), Kind: kinds[r.Signal], RoleLabel: recRoleLabel(r.Role),
					Facts: facts[r.Signal], Channel: channelName(ch), Program: ps.Seg.Program, Start: ps.Seg.Start, Times: 1})
			}
		}
	}
	if merge {
		b.items = mergeParte(b.items)
	}
	return b
}

// mergeParte junta, en el parte de 7 días, lo que salió en varias emisiones
// de un programa: misma señal de ID fijo (técnica, corte, participación con
// su dirección, riesgo, pico, Super Chats) y mismo rol. Queda la más nueva
// con la prioridad más alta, y Times son las emisiones (días) distintas.
// Preguntas, temas y clips no se juntan: cada uno habla de otra cosa.
func mergeParte(items []parteItem) []parteItem {
	var out []parteItem
	at := map[string]int{}
	days := map[string]map[time.Time]bool{}
	for _, it := range items {
		if it.Kind == "pregunta" || it.Kind == "tema" || it.Kind == "clip" {
			out = append(out, it)
			continue
		}
		k := it.Channel + "|" + strings.ToLower(it.Program) + "|" + it.Signal + "|" + it.Role
		i, ok := at[k]
		if !ok {
			at[k] = len(out)
			days[k] = map[time.Time]bool{dayStart(it.Start): true}
			out = append(out, it)
			continue
		}
		days[k][dayStart(it.Start)] = true
		prev := out[i]
		if it.Start.After(prev.Start) {
			if prioRank(prev.Prio) < prioRank(it.Prio) {
				it.Prio = prev.Prio
			}
			out[i] = it
		} else if prioRank(it.Prio) < prioRank(prev.Prio) {
			out[i].Prio = it.Prio
		}
	}
	for k, i := range at {
		out[i].Times = len(days[k])
	}
	return out
}

// parteData arma el parte de un rol y un período (listas cerradas: un valor
// que no está se ignora y nunca se refleja).
func (s *Server) parteData(v url.Values) map[string]any {
	now := s.clock()
	rol := pickOf(v.Get("rol"), recRoleKeys())
	periodo := pickOf(v.Get("periodo"), []string{"dia", "semana"})
	if periodo == "" {
		periodo = "dia"
	}
	b := s.parte(periodo, now)
	var items []parteItem
	high := 0
	for _, it := range b.items {
		if rol != "" && it.Role != rol {
			continue
		}
		it.State = s.recMarks.State(it.ID)
		if it.State == "nosirve" {
			continue
		}
		it.StateLabel = recStates[it.State]
		if it.State == "" && it.Prio == "alta" {
			high++
		}
		items = append(items, it)
	}
	sortRecCards(items, func(it parteItem) string { return it.Prio }, func(it parteItem) bool { return it.State != "" })
	link := func(page, per, r string) string {
		return page + "?" + url.Values{"periodo": {per}, "rol": {r}}.Encode()
	}
	title := partePeriods[0].Title
	var periods, roles []RoleLink
	for _, p := range partePeriods {
		periods = append(periods, RoleLink{Label: p.Label, Page: link("/parte", p.Key, rol), On: p.Key == periodo})
		if p.Key == periodo {
			title = p.Title
		}
	}
	for _, r := range append([]struct{ Key, Label string }{{"", "Todos"}}, recRoles...) {
		roles = append(roles, RoleLink{Label: r.Label, Page: link("/parte", periodo, r.Key), On: r.Key == rol})
	}
	who := cmp.Or(recRoleLabel(rol), "Todos")
	return map[string]any{
		"Title": title + " · " + who, "Items": items, "Periods": periods, "Roles": roles,
		"Line": plural(b.programs, "programa", "programas") + " · " + plural(b.msgs, "mensaje", "mensajes") + " · " + fmt.Sprintf("%d de prioridad alta", high),
		"Txt":  link("/parte.txt", periodo, rol), "Empty": len(items) == 0, "Semana": periodo == "semana",
	}
}

// partePage es GET /parte.
func (s *Server) partePage(w http.ResponseWriter, r *http.Request) {
	data := s.parteData(r.URL.Query())
	data["Tab"], data["Channel"] = "programas", s.st.Status().Channel
	s.render(w, "parte.html", data)
}

// parteText es GET /parte.txt: el mismo parte en texto plano, para copiar.
func (s *Server) parteText(w http.ResponseWriter, r *http.Request) {
	data := s.parteData(r.URL.Query())
	var b strings.Builder
	b.WriteString(data["Title"].(string) + "\n" + data["Line"].(string) + "\n")
	for _, it := range data["Items"].([]parteItem) {
		fmt.Fprintf(&b, "\n• [%s] %s\n  %s\n  %s\n  %s\n", strings.ToUpper(it.Prio), it.Title, it.Action, it.Facts, it.Where())
		if it.Times > 1 {
			fmt.Fprintf(&b, "  Se repitió en %d emisiones\n", it.Times)
		}
		if it.StateLabel != "" {
			fmt.Fprintf(&b, "  (%s)\n", it.StateLabel)
		}
	}
	if len(data["Items"].([]parteItem)) == 0 {
		b.WriteString("\nNo hay recomendaciones para este período.\n")
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write([]byte(b.String()))
}
