package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Tests de la spec recomendaciones-por-rol.

// recMsg es un mensaje de chat del vivo v1 con su programa.
func recMsg(id, author, text string, at time.Time) Message {
	return Message{ID: id, Source: "chat", VideoID: "v1", Program: "LA MAÑANA", Author: author, Text: text, At: at}
}

// recSeg es LA MAÑANA de hoy, de 08:00 a 09:00, ya terminado.
func recSeg(now time.Time) ProgramSeg {
	day := dayStart(now)
	sg := Segment{VideoID: "v1", Program: "LA MAÑANA", Start: day.Add(8 * time.Hour), End: day.Add(9 * time.Hour), State: SegEnded}
	return ProgramSeg{Channel: ChannelStatus{ID: "UC1", Name: "Radio Test"}, Seg: sg, Key: segmentKey("UC1", sg)}
}

func signalOf(sigs []RecSignal, kind string) *RecSignal {
	for i := range sigs {
		if sigs[i].Kind == kind {
			return &sigs[i]
		}
	}
	return nil
}

// recHistory arma 4 emisiones del programa (hoy y las 3 semanas anteriores,
// el mismo día de la semana) de 08:00 a 09:00 con espectadores cada 5 min.
// Antes: 2 personas por franja con 100 mirando y 3 habituales (h1 a h3) que
// escribieron hace 2 y 3 semanas y no volvieron. Hoy: 10 personas con 300.
func recHistory(now time.Time) []Emission {
	return recHistoryWith(now, 10, 300, []string{"h1", "h2", "h3"})
}

// recHistoryWith es recHistory con las personas y espectadores de hoy y los
// habituales que dejaron de escribir.
func recHistoryWith(now time.Time, curPeople int, curViewers int64, risky []string) []Emission {
	var ems []Emission
	for w := range 4 {
		day := dayStart(now).AddDate(0, 0, -7*w)
		start := day.Add(8 * time.Hour)
		e := Emission{Day: day, Program: "LA MAÑANA", Segs: []Segment{{VideoID: "v1", Program: "LA MAÑANA", Start: start, End: start.Add(time.Hour), State: SegEnded}}}
		people, viewers := 2, int64(100)
		if w == 0 {
			people, viewers = curPeople, curViewers
		}
		for k := range 12 {
			at := start.Add(time.Duration(k)*slotStep + time.Minute)
			e.Viewers = append(e.Viewers, ViewerSample{VideoID: "v1", At: at, Viewers: viewers})
			for p := range people {
				e.Msgs = append(e.Msgs, recMsg(fmt.Sprintf("e%d-%d-%d", w, k, p), fmt.Sprintf("p%d", p), "hola", at))
			}
		}
		if w >= 2 {
			for _, h := range risky {
				e.Msgs = append(e.Msgs, recMsg("x"+h+fmt.Sprint(w), h, "presente", start.Add(30*time.Minute)))
			}
		}
		ems = append(ems, e)
	}
	return ems
}

// TestRecSignals (CA1): cada señal sale cuando llega a su umbral y no sale
// cuando no llega, con sus roles, sus números y sus mensajes.
func TestRecSignals(t *testing.T) {
	now := noonToday()
	ps := recSeg(now)
	at := ps.Seg.Start.Add(10 * time.Minute)
	people := func(n int, text string) []Message {
		var out []Message
		for i := range n {
			out = append(out, recMsg(fmt.Sprintf("q%d", i), fmt.Sprintf("autor%d", i), text, at.Add(time.Duration(i)*time.Minute)))
		}
		return out
	}
	grouped := func(msgs []Message) map[string]MessageLabel {
		l := map[string]MessageLabel{}
		for _, m := range msgs {
			l[m.ID] = MessageLabel{ID: m.ID, Cat: CatPregunta, Group: "UC1|1|1"}
		}
		return l
	}
	groups := map[string]BatchGroup{"UC1|1|1": {ID: "UC1|1|1", Text: "¿Cuándo vuelve el invitado?"}}
	five, four := people(5, "¿cuándo vuelve?"), people(4, "¿cuándo vuelve?")
	sup := recMsg("s1", "Pago", "gracias", at)
	sup.Super, sup.Currency, sup.AmountMicros = SuperChat, "ARS", 1_500_000_000
	cut := ps
	cut.Parts = []Segment{{VideoID: "v1", Program: "LA MAÑANA", Start: ps.Seg.Start, End: ps.Seg.Start.Add(20 * time.Minute)},
		{VideoID: "v1", Program: "LA MAÑANA", Start: ps.Seg.Start.Add(29 * time.Minute), End: ps.Seg.End}}
	slotMsgs := people(4, "qué golazo")
	hot := Slot{Start: ps.Seg.Start.Add(15 * time.Minute), Msgs: slotMsgs, N: 4, Authors: 4, Viewers: 100, Pct: 4}
	cold := Slot{Start: ps.Seg.Start.Add(40 * time.Minute), Msgs: slotMsgs[:2], N: 2, Authors: 2, Viewers: 100, Pct: 2}
	topics := &ClaudeAnalysis{Key: ps.Key, Result: ClaudeResult{Topics: []ClaudeGroup{
		{Text: "el tránsito", Msgs: 8, People: 4, IDs: []string{"q0", "q1"}}, {Text: "el clima", Msgs: 2, People: 2}}}}
	questions := &ClaudeAnalysis{Key: ps.Key, Result: ClaudeResult{Questions: []ClaudeGroup{{Text: "¿Hay paro?", Msgs: 6, People: 5, IDs: []string{"q0"}}}}}
	ems := recHistory(now)
	flat := recHistoryWith(now, 2, 100, []string{"h1", "h2"})
	past := ps // el mismo programa hace 2 semanas: el riesgo se mide a ese día
	past.Seg.Start, past.Seg.End = past.Seg.Start.AddDate(0, 0, -14), past.Seg.End.AddDate(0, 0, -14)
	past.Key = segmentKey("UC1", past.Seg)
	oneTopic := &ClaudeAnalysis{Key: ps.Key, Result: ClaudeResult{Topics: []ClaudeGroup{{Text: "el clima", Msgs: 2, People: 2}}}}

	cases := []struct {
		name  string
		in    recInput
		kind  string
		want  bool
		roles []string
		facts []string
		ids   int
	}{
		{"pregunta de las tandas con 5 personas", recInput{Msgs: five, Labels: grouped(five), Groups: groups}, "pregunta", true, []string{"produccion"}, []string{"5 personas"}, 5},
		{"pregunta con 4 personas", recInput{Msgs: four, Labels: grouped(four), Groups: groups}, "pregunta", false, nil, nil, 0},
		{"pregunta ya respondida", recInput{Msgs: five, Labels: grouped(five), Groups: groups, Answered: func(string) bool { return true }}, "pregunta", false, nil, nil, 0},
		{"pregunta del resumen de Claude", recInput{Msgs: five, Analysis: questions}, "pregunta", true, []string{"produccion"}, []string{"5 personas"}, 1},
		{"3 quejas técnicas", recInput{Msgs: people(3, "no se escucha nada")}, "tecnica", true, []string{"tecnica"}, []string{"3 quejas técnicas", "3 personas"}, 3},
		{"2 quejas técnicas", recInput{Msgs: people(2, "no se escucha nada")}, "tecnica", false, nil, nil, 0},
		{"momento para un clip", recInput{Msgs: slotMsgs, Slots: []Slot{hot, cold}}, "clip", true, []string{"redes"}, []string{"4 personas"}, 4},
		{"sin espectadores no hay clip", recInput{Msgs: slotMsgs, Slots: []Slot{{Start: hot.Start, Msgs: slotMsgs, N: 4, Authors: 4}}}, "clip", false, nil, nil, 0},
		{"tema del resumen de Claude", recInput{Msgs: five, Analysis: topics}, "tema", true, []string{"redes", "produccion"}, []string{"8 mensajes", "4 personas"}, 2},
		{"el vivo se cortó", recInput{Seg: cut}, "corte", true, []string{"tecnica"}, []string{"de 08:20 a 08:29"}, 0},
		{"sin corte", recInput{}, "corte", false, nil, nil, 0},
		{"Super Chats", recInput{Msgs: []Message{sup}}, "supers", true, []string{"comercial"}, []string{"1 pago", "ARS"}, 1},
		{"participación por encima de lo normal", recInput{Ems: ems}, "participacion", true, []string{"direccion", "produccion"}, []string{"3,3 %", "2,0 %"}, 0},
		{"pico de espectadores", recInput{Ems: ems}, "pico", true, []string{"comercial"}, []string{"300", "100"}, 0},
		{"habituales en riesgo", recInput{Ems: ems}, "riesgo", true, []string{"produccion", "direccion"}, []string{"3 habituales"}, 0},
		{"sin historia no hay participación", recInput{Ems: ems[:2]}, "participacion", false, nil, nil, 0},
		{"participación normal", recInput{Ems: flat}, "participacion", false, nil, nil, 0},
		{"pico normal", recInput{Ems: flat}, "pico", false, nil, nil, 0},
		{"2 habituales en riesgo", recInput{Ems: flat}, "riesgo", false, nil, nil, 0},
		{"el riesgo se mide al día del programa", recInput{Seg: past, Ems: ems}, "riesgo", false, nil, nil, 0},
		{"un tema de 2 personas", recInput{Msgs: five, Analysis: oneTopic}, "tema", false, nil, nil, 0},
		{"sin Super Chats", recInput{Msgs: five}, "supers", false, nil, nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.in.Seg.Key == "" {
				c.in.Seg = ps
			}
			c.in.Now = now
			s := signalOf(recSignals(c.in), c.kind)
			if (s != nil) != c.want {
				t.Fatalf("señal %s: %+v", c.kind, recSignals(c.in))
			}
			if s == nil {
				return
			}
			if !slices.Equal(s.Roles, c.roles) {
				t.Errorf("roles = %v, quiero %v", s.Roles, c.roles)
			}
			for _, f := range c.facts {
				if !strings.Contains(s.Facts, f) {
					t.Errorf("Facts = %q, falta %q", s.Facts, f)
				}
			}
			if len(s.IDs) != c.ids {
				t.Errorf("IDs = %v, quiero %d", s.IDs, c.ids)
			}
			if !strings.HasPrefix(s.ID, c.kind) || s.Prio == "" {
				t.Errorf("ID %q, prioridad %q", s.ID, s.Prio)
			}
		})
	}
	// Como mucho recMaxQuestion grupos de preguntas: los de más personas.
	var many []Message
	labels, gs := map[string]MessageLabel{}, map[string]BatchGroup{}
	for g := range 7 {
		gid := fmt.Sprintf("UC1|1|%d", g)
		gs[gid] = BatchGroup{ID: gid, Text: fmt.Sprintf("¿pregunta %d?", g)}
		for p := range 5 + g {
			m := recMsg(fmt.Sprintf("m%d-%d", g, p), fmt.Sprintf("a%d", p), "¿y?", at)
			many, labels[m.ID] = append(many, m), MessageLabel{ID: m.ID, Group: gid}
		}
	}
	var qs []RecSignal
	for _, s := range recSignals(recInput{Seg: ps, Msgs: many, Labels: labels, Groups: gs, Now: now}) {
		if s.Kind == "pregunta" {
			qs = append(qs, s)
		}
	}
	if len(qs) != recMaxQuestion || qs[0].N != 11 || qs[len(qs)-1].N != 7 {
		t.Errorf("preguntas = %d (de %d a %d personas)", len(qs), qs[0].N, qs[len(qs)-1].N)
	}
	if s := signalOf(recSignals(recInput{Seg: ps, Ems: ems, Now: now}), "participacion"); s == nil || s.ID != "participacion|arriba" {
		t.Errorf("la dirección va en el ID de participación: %+v", s)
	}
	// El ID es estable entre dos cálculos: con él se juntan la redacción de Claude y las marcas.
	in := recInput{Seg: ps, Msgs: slotMsgs, Slots: []Slot{hot, cold}, Now: now}
	if a, b := recSignals(in), recSignals(in); len(a) != 1 || a[0].ID != b[0].ID || a[0].ID != fmt.Sprintf("clip|%d", hot.Start.Unix()) || a[0].VideoID != "v1" {
		t.Errorf("clip = %+v", a)
	}
}

// TestRecFallback (CA2): sin Claude, cada señal sale con un texto fijo por
// rol, la prioridad por reglas y la evidencia de Go.
func TestRecFallback(t *testing.T) {
	tech := RecSignal{ID: "tecnica", Kind: "tecnica", Roles: []string{"tecnica"}, Facts: "12 quejas técnicas de 9 personas · 08:10–08:40", N: 12}
	tech.Prio = recPrio(tech)
	recs := recFallback(tech)
	if len(recs) != 1 || recs[0].Role != "tecnica" || recs[0].Prio != "alta" || recs[0].Title == "" || !strings.Contains(recs[0].Action, tech.Facts) {
		t.Errorf("técnica = %+v", recs)
	}
	part := RecSignal{ID: "participacion", Kind: "participacion", Roles: []string{"direccion", "produccion"}, Facts: "Participación 3 %", Dir: "abajo"}
	part.Prio = recPrio(part)
	recs = recFallback(part)
	if len(recs) != 2 || recs[0].Role != "direccion" || recs[1].Role != "produccion" || recs[0].Title == recs[1].Title {
		t.Errorf("participación = %+v", recs)
	}
	for _, kind := range []string{"pregunta", "tecnica", "clip", "participacion", "riesgo", "tema", "corte", "supers", "pico"} {
		for _, role := range recRoleKeys() {
			if title, action := recText(RecSignal{Kind: kind, Subject: "algo", Facts: "dato"}, role); (title == "") != (action == "") {
				t.Errorf("%s/%s: título %q, acción %q", kind, role, title, action)
			}
		}
	}
}

// TestRecPrompt (CA3): se mandan las señales numeradas con sus roles y hasta
// 5 ejemplos por señal, sin autores, sin IDs y sin spam.
func TestRecPrompt(t *testing.T) {
	now := noonToday()
	ps := recSeg(now)
	var msgs []Message
	var ids []string
	for i := range 7 {
		m := recMsg(fmt.Sprintf("id-%d", i), fmt.Sprintf("Persona Secreta %d", i), fmt.Sprintf("¿cuándo vuelve %d?", i), ps.Seg.Start.Add(time.Duration(i)*time.Minute))
		msgs, ids = append(msgs, m), append(ids, m.ID)
	}
	msgs = append(msgs, recMsg("spam", "Spammer", "entrá a http://ganaplata.example ya", ps.Seg.Start), recMsg("largo", "Otro", strings.Repeat("á", 400), ps.Seg.Start))
	sigs := []RecSignal{
		{ID: "pregunta|g", Kind: "pregunta", Roles: []string{"produccion"}, Subject: "¿Cuándo vuelve?", Facts: "7 personas · 08:00–08:06", IDs: append([]string{"spam", "largo"}, ids...)},
		{ID: "corte", Kind: "corte", Roles: []string{"tecnica"}, Facts: "se cortó de 08:20 a 08:29"},
	}
	p := recPrompt("Radio Test", ps, sigs, msgs)
	for _, w := range []string{"LA MAÑANA", "Radio Test", "[1] pregunta", "roles: produccion", "7 personas · 08:00–08:06", "[2] corte", "roles: tecnica", "<mensajes>", "</mensajes>", strings.Repeat("á", 300)} {
		if !strings.Contains(p, w) {
			t.Errorf("falta %q en:\n%s", w, p)
		}
	}
	for _, no := range []string{"Persona Secreta", "Spammer", "ganaplata", "id-0", strings.Repeat("á", 301)} {
		if strings.Contains(p, no) {
			t.Errorf("el pedido tiene %q", no)
		}
	}
	if n := strings.Count(p, "- "); n != 5 {
		t.Errorf("ejemplos = %d, quiero 5:\n%s", n, p)
	}
	if recWorstCase(defaultClaudeModel, p) < claudeCost(defaultClaudeModel, len(p)/3, recMaxTokens) {
		t.Error("el peor caso no cuenta la salida completa")
	}
}

// TestValidRecResult (CA4): de la respuesta solo se toma lo que cuadra con
// las señales mandadas.
func TestValidRecResult(t *testing.T) {
	sigs := []RecSignal{
		{ID: "pregunta|g", Kind: "pregunta", Roles: []string{"produccion"}, Subject: "¿Hay paro?", Facts: "5 personas · 09:10–09:40"},
		{ID: "riesgo", Kind: "riesgo", Roles: []string{"produccion", "direccion"}, Facts: "3 habituales"},
	}
	type item = struct {
		Senal                          int
		Rol, Prioridad, Titulo, Accion string
	}
	ok := item{1, "produccion", "alta", "Responder lo del paro", "Lo preguntaron 5 personas entre las 09:10 y las 09:40."}
	cases := []struct {
		name string
		it   item
		want bool
	}{
		{"válida", ok, true},
		{"señal que no existe", item{3, "produccion", "alta", "x", "y"}, false},
		{"señal 0", item{0, "produccion", "alta", "x", "y"}, false},
		{"rol que no es de la señal", item{1, "comercial", "alta", "x", "y"}, false},
		{"rol inventado", item{1, "gerencia", "alta", "x", "y"}, false},
		{"prioridad fuera de lista", item{1, "produccion", "urgente", "x", "y"}, false},
		{"título largo", item{1, "produccion", "alta", strings.Repeat("a", 91), "y"}, false},
		{"acción larga", item{1, "produccion", "alta", "x", strings.Repeat("a", 241)}, false},
		{"título vacío", item{1, "produccion", "alta", " ", "y"}, false},
		{"número inventado", item{1, "produccion", "alta", "Responder a las 47 personas", "y"}, false},
		{"número de la señal", item{2, "direccion", "media", "Se fueron 3 habituales", "Revisar."}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := validRecResult(RecRaw{Recomendaciones: []item{c.it}}, sigs)
			if (len(got) == 1) != c.want {
				t.Fatalf("got %+v", got)
			}
			if c.want && (got[0].Signal != sigs[c.it.Senal-1].ID || got[0].Role != c.it.Rol || got[0].Prio != c.it.Prioridad || got[0].Facts != sigs[c.it.Senal-1].Facts) {
				t.Errorf("got %+v", got[0])
			}
		})
	}
	if got := validRecResult(RecRaw{Recomendaciones: []item{ok, ok}}, sigs); len(got) != 1 {
		t.Errorf("la misma señal y el mismo rol dos veces: %+v", got)
	}
	if _, err := decodeRecRaw(`{"recomendaciones":[],"otro":1}`); err == nil {
		t.Error("un campo de más es una respuesta que no se entiende")
	}
}

// TestRecToSend (revisión 1, punto 5): como mucho recMaxSignals señales por
// pedido, las de más prioridad, para que la respuesta entre en recMaxTokens.
func TestRecToSend(t *testing.T) {
	var sigs []RecSignal
	for i := range 20 {
		p := "baja"
		if i%4 == 0 {
			p = "alta"
		}
		sigs = append(sigs, RecSignal{ID: fmt.Sprint(i), Prio: p})
	}
	got := recToSend(sigs)
	if len(got) != recMaxSignals || got[0].ID != "0" || got[4].Prio != "alta" || got[5].Prio != "baja" || len(sigs) != 20 || sigs[1].ID != "1" {
		t.Errorf("recToSend = %+v", got)
	}
	p := recPrompt("Radio", recSeg(noonToday()), got, nil)
	if strings.Contains(p, fmt.Sprintf("[%d]", recMaxSignals+1)) {
		t.Error("el pedido lleva más señales que recMaxSignals")
	}
}

// TestRecsStore (CA5): las redacciones se guardan aparte, con retención de
// 30 días desde el fin del tramo.
func TestRecsStore(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(dir + "/datos.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	st.AddRecs(ProgramRecs{Key: "UC1|viejo", ChannelID: "UC1", From: now.Add(-41 * 24 * time.Hour), To: now.Add(-40 * 24 * time.Hour)})
	st.AddRecs(ProgramRecs{Key: "UC1|nuevo", ChannelID: "UC1", From: now.Add(-2 * time.Hour), To: now.Add(-time.Hour),
		Recs: []Rec{{Signal: "corte", Role: "tecnica", Prio: "alta", Title: "Revisar", Action: "Ya."}}})
	st.AddRecs(ProgramRecs{Key: "UC2|otro", ChannelID: "UC2", From: now.Add(-2 * time.Hour), To: now.Add(-time.Hour)})
	if got := st.Recs("UC1", time.Time{}); len(got) != 2 {
		t.Errorf("Recs = %d", len(got))
	}
	if _, err := os.Stat(filepath.Join(dir, "datos.recomendaciones.jsonl")); err != nil {
		t.Fatal(err)
	}
	st2, err := OpenStore(dir + "/datos.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	got := st2.Recs("UC1", time.Time{})
	if len(got) != 1 || got[0].Key != "UC1|nuevo" || len(got[0].Recs) != 1 || got[0].Recs[0].Title != "Revisar" {
		t.Errorf("después de reabrir: %+v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "datos.jsonl")); len(b) != 0 {
		t.Error("datos.jsonl cambió")
	}
}

// recsClaude es una API de Claude falsa: a los pedidos de recomendaciones les
// responde una para la técnica y a los resúmenes, okResult.
type recsClaude struct {
	srv   *httptest.Server
	mu    sync.Mutex
	recs  []string
	reqs  atomic.Int64
	reply string
}

func newRecsClaude(t *testing.T) *recsClaude {
	f := &recsClaude{reply: `{"recomendaciones":[{"senal":1,"rol":"tecnica","prioridad":"alta","titulo":"Revisar el sonido de la mañana","accion":"Hubo quejas de audio: revisá la consola antes de mañana."}]}`}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.reqs.Add(1)
		var body struct {
			System   string                     `json:"system"`
			Messages []struct{ Content string } `json:"messages"`
			Output   struct {
				Format struct{ Schema json.RawMessage }
			} `json:"output_config"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if strings.Contains(string(body.Output.Format.Schema), "recomendaciones") {
			f.mu.Lock()
			f.recs = append(f.recs, body.Messages[0].Content)
			f.mu.Unlock()
			json.NewEncoder(w).Encode(claudeText(defaultClaudeModel, f.reply, "end_turn", 3000, 800))
			return
		}
		json.NewEncoder(w).Encode(claudeText(defaultClaudeModel, okResult, "end_turn", 20000, 1500))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *recsClaude) client() *Claude {
	return NewClaude(ClaudeConfig{On: true, Key: testClaudeKey, BaseURL: f.srv.URL, Model: defaultClaudeModel})
}

// TestAnalystRecs (CA3, CA5): con Recs prendido, cuando todos los programas
// terminados tienen su resumen, el analista pide las recomendaciones de los
// que tienen señales y anota los que no tienen (sin pedido).
func TestAnalystRecs(t *testing.T) {
	now := noonToday()
	st, _ := analystServer(t, now)
	day := dayStart(now)
	st.Add([]Message{
		recMsg("t1", "Ana", "no se escucha nada", day.Add(8*time.Hour+10*time.Minute)),
		recMsg("t2", "Beto", "no se escucha", day.Add(8*time.Hour+11*time.Minute)),
		recMsg("t3", "Caro", "se escucha mal", day.Add(8*time.Hour+12*time.Minute)),
	})
	f := newRecsClaude(t)
	stop := startAnalyst(t, f.client(), st, NewAnalyst(), now, AnalystConfig{Budget: 2_000_000, Recs: true})
	waitUntil(t, 5*time.Second, func() bool { return len(st.Recs("UC1", time.Time{})) >= 4 })
	time.Sleep(100 * time.Millisecond)
	stop()

	got := map[string]ProgramRecs{}
	for _, r := range st.Recs("UC1", time.Time{}) {
		if _, dup := got[r.Program]; dup {
			t.Errorf("%s tiene dos pedidos de recomendaciones", r.Program)
		}
		got[r.Program] = r
	}
	m := got["LA MAÑANA"]
	if len(m.Recs) != 1 || m.Recs[0].Signal != "tecnica" || m.Recs[0].Role != "tecnica" || m.Recs[0].Prio != "alta" || m.Error != "" ||
		m.CostMicros != claudeCost(defaultClaudeModel, 3000, 800) || m.Model != defaultClaudeModel || m.Signals == 0 {
		t.Errorf("LA MAÑANA = %+v", m)
	}
	for _, p := range []string{"POCO", "CRUCE", "AYER"} {
		if r, ok := got[p]; !ok || r.Signals != 0 || r.CostMicros != 0 || len(r.Recs) != 0 {
			t.Errorf("%s = %+v (sin señales: se anota sin pedido)", p, r)
		}
	}
	if _, ok := got["LA TARDE"]; ok {
		t.Error("LA TARDE está al aire: no tiene recomendaciones")
	}
	f.mu.Lock()
	prompts := slices.Clone(f.recs)
	f.mu.Unlock()
	if len(prompts) != 1 || f.reqs.Load() != 4 {
		t.Fatalf("pedidos de recomendaciones = %d, en total %d", len(prompts), f.reqs.Load())
	}
	for _, no := range []string{"Ana", "Beto", "Caro", "autor-", "t1"} {
		if strings.Contains(prompts[0], no) {
			t.Errorf("el pedido tiene %q", no)
		}
	}
	if !strings.Contains(prompts[0], "no se escucha nada") {
		t.Errorf("faltan los ejemplos:\n%s", prompts[0])
	}
	if want := 3*claudeCost(defaultClaudeModel, 20000, 1500) + claudeCost(defaultClaudeModel, 3000, 800); st.ClaudeSpend() != want {
		t.Errorf("gasto = %d, quiero %d", st.ClaudeSpend(), want)
	}

	// Sin Recs (los tests de antes y el modo informe) no hay pedidos de recomendaciones.
	st2, _ := analystServer(t, now)
	f2 := newRecsClaude(t)
	stop = startAnalyst(t, f2.client(), st2, NewAnalyst(), now, AnalystConfig{Budget: 2_000_000})
	waitUntil(t, 5*time.Second, func() bool { return len(st2.Analyses("UC1", time.Time{})) >= 4 })
	time.Sleep(100 * time.Millisecond)
	stop()
	if len(st2.Recs("UC1", time.Time{})) != 0 || len(f2.recs) != 0 {
		t.Error("sin Recs se pidieron recomendaciones")
	}

	// Con un tope que no alcanza para el peor caso, no se manda ni se calcula
	// nada: armar las señales lee 4 semanas por programa (revisión 1, punto 1).
	st3, _ := analystServer(t, now)
	st3.Add([]Message{recMsg("t1", "Ana", "no se escucha nada", day.Add(8*time.Hour+10*time.Minute)),
		recMsg("t2", "Beto", "no se escucha", day.Add(8*time.Hour+11*time.Minute)), recMsg("t3", "Caro", "se escucha mal", day.Add(8*time.Hour+12*time.Minute))})
	f3 := newRecsClaude(t)
	calcs := recCalcs.Load()
	stop = startAnalyst(t, f3.client(), st3, NewAnalyst(), now, AnalystConfig{Budget: 3*claudeCost(defaultClaudeModel, 20000, 1500) + 1, Recs: true})
	waitUntil(t, 5*time.Second, func() bool { return len(st3.Analyses("UC1", time.Time{})) >= 4 })
	time.Sleep(300 * time.Millisecond) // varias vueltas del analista
	stop()
	if len(f3.recs) != 0 || len(st3.Recs("UC1", time.Time{})) != 0 || recCalcs.Load() != calcs {
		t.Errorf("con el tope alcanzado: %d pedidos, %d guardadas, %d cálculos", len(f3.recs), len(st3.Recs("UC1", time.Time{})), recCalcs.Load()-calcs)
	}
}

// recServer es programServer con 3 quejas técnicas en LA MAÑANA y en AYER, y
// las marcas en un archivo temporal.
func recServer(t *testing.T, now time.Time) (*Store, *Server, http.Handler, string) {
	t.Helper()
	st, srv := programServer(t, now, false)
	day := dayStart(now)
	st.Add([]Message{
		recMsg("t1", "Ana", "no se escucha nada", day.Add(8*time.Hour+10*time.Minute)),
		recMsg("t2", "Beto", "no se escucha", day.Add(8*time.Hour+11*time.Minute)),
		recMsg("t3", "Caro", "se escucha mal", day.Add(8*time.Hour+12*time.Minute)),
	})
	ayer := func(id, a, text string, d time.Duration) Message {
		m := recMsg(id, a, text, day.Add(-5*time.Hour+d))
		m.VideoID, m.Program = "v3", "AYER"
		return m
	}
	st.Add([]Message{ayer("y1", "Ana", "no se escucha nada", time.Minute), ayer("y2", "Beto", "se corta todo", 2*time.Minute), ayer("y3", "Caro", "sin audio", 3*time.Minute)})
	path := filepath.Join(t.TempDir(), "datos.recmarcas.jsonl")
	marks, err := OpenRecMarks(path, now)
	if err != nil {
		t.Fatal(err)
	}
	srv.recMarks = marks
	return st, srv, srv.Routes(), path
}

// TestProgramRecsTab (CA6; antes, pestana-recomendaciones): Recomendaciones
// es la última pestaña de Programas, con el robot de la IA, y muestra una
// tarjeta por recomendación con el filtro por rol. El robot no aparece en
// las pestañas de En vivo.
func TestProgramRecsTab(t *testing.T) {
	if k := programTabs[len(programTabs)-1]; k.Key != "recomendaciones" || k.Label != "Recomendaciones" || k.Icon != "robot" {
		t.Fatalf("la última pestaña es %+v", k)
	}
	now := noonToday()
	day := dayStart(now)
	st, srv, h, _ := recServer(t, now)
	// Con espectadores, LA MAÑANA tiene momentos para clips (CA6: el link a YouTube).
	for at := day.Add(8*time.Hour + time.Minute); at.Before(day.Add(9*time.Hour + 50*time.Minute)); at = at.Add(5 * time.Minute) {
		st.AddViewers(at, []Video{{ID: "v1", LiveStatus: "live", Concurrent: 50}})
	}
	keyMan := segmentKey("UC1", Segment{VideoID: "v1", Program: "LA MAÑANA", Start: day.Add(8 * time.Hour)})
	base := "/parcial/programas?dia=hoy&tramo=" + url.QueryEscape(keyMan)
	resumen := getBody(h, base)
	link := from(resumen, `id="tab-recomendaciones"`, "</a>")
	if link == "" || !strings.Contains(link, "pestana=recomendaciones") || !strings.Contains(link, `class="ico ico-robot"`) || !strings.Contains(link, `aria-hidden="true"`) {
		t.Fatalf("la pestaña con el robot no está bien:\n%s", link)
	}
	if strings.Contains(link, `aria-current="page"`) || strings.Contains(resumen, `id="recomendaciones"`) {
		t.Error("en Resumen, Recomendaciones no está elegida")
	}
	body := getBody(h, base+"&pestana=recomendaciones")
	sec := from(body, `id="recomendaciones"`, "</section>")
	for _, w := range []string{`aria-current="page"><svg class="ico ico-robot"`} {
		if !strings.Contains(body, w) {
			t.Errorf("falta %q", w)
		}
	}
	for _, w := range []string{"Revisar el audio y la transmisión", "3 quejas técnicas de 3 personas", "Hubo Super Chats", "ARS",
		`class="rec alta"`, "Técnica", "Comercial", `aria-label="Para quién"`, "rol=tecnica", "rol=comercial", `id="pf-rec1"`,
		`hx-post="/recomendaciones/marca"`, `name="estado" value="hecha"`, `name="estado" value="nosirve"`, `href="/parte?rol=`, "por reglas"} {
		if !strings.Contains(sec, w) {
			t.Errorf("falta %q en:\n%s", w, sec)
		}
	}
	if strings.Contains(sec, "Próximamente") || strings.Contains(body, `id="chat-programa"`) || strings.Contains(body, "<h3>Resumen del chat") {
		t.Error("la pestaña muestra otra cosa")
	}
	// Lo más importante primero: la técnica (alta) antes que los Super Chats (baja).
	if i, j := strings.Index(sec, "Revisar el audio"), strings.Index(sec, "Hubo Super Chats"); i < 0 || j < i {
		t.Error("las tarjetas no van por prioridad")
	}
	tec := getBody(h, base+"&pestana=recomendaciones&rol=tecnica")
	if !strings.Contains(tec, "Revisar el audio") || strings.Contains(tec, "Hubo Super Chats") || !strings.Contains(from(tec, `aria-label="Para quién"`, "</nav>"), `aria-current="page">Técnica`) {
		t.Error("el filtro por rol no anda")
	}
	if !strings.Contains(tec, "rol=tecnica") || !strings.Contains(from(tec, `id="tab-recomendaciones"`, "</nav>"), "rol=tecnica") {
		t.Error("el rol no viaja en los links")
	}
	if bad := getBody(h, base+"&pestana=recomendaciones&rol=gerencia%3Cb%3E"); !strings.Contains(bad, "Hubo Super Chats") || strings.Contains(bad, "gerencia") {
		t.Error("un rol desconocido se ignora y no se refleja")
	}
	open := getBody(h, base+"&pestana=recomendaciones&rol=tecnica&abrir=rec1")
	if !strings.Contains(from(open, `id="pf-rec1"`, "</details>"), "no se escucha nada") || !strings.Contains(open, `<details class="plegable rmsgs" open>`) {
		t.Error("la evidencia no se despliega con abrir=rec1")
	}
	redes := getBody(h, base+"&pestana=recomendaciones&rol=redes")
	if !strings.Contains(redes, "Cortar un clip de") || !strings.Contains(redes, `href="https://www.youtube.com/watch?v=v1"`) || !strings.Contains(redes, "Abrir el momento en YouTube") {
		t.Errorf("el clip no tiene su link a YouTube:\n%s", from(redes, `id="recomendaciones"`, "</section>"))
	}
	srv.recMarks = nil
	if no := getBody(h, base+"&pestana=recomendaciones"); strings.Contains(no, `hx-post="/recomendaciones/marca"`) || !strings.Contains(no, "Revisar el audio") {
		t.Error("sin dónde guardar las marcas, no hay botones")
	}
	keyPoco := segmentKey("UC1", Segment{VideoID: "v2", Program: "POCO", Start: day.Add(6 * time.Hour)})
	if empty := getBody(h, "/parcial/programas?dia=hoy&pestana=recomendaciones&tramo="+url.QueryEscape(keyPoco)); !strings.Contains(empty, "Este programa no dejó recomendaciones.") {
		t.Error("sin señales, lo dice")
	}
	if !strings.Contains(getBody(h, "/programas?dia=hoy&pestana=recomendaciones&tramo="+url.QueryEscape(keyMan)), `id="recomendaciones"`) {
		t.Error("la pantalla entera abre la pestaña desde la URL")
	}
	if live := getBody(h, "/"); strings.Contains(live, "ico-robot") {
		t.Error("el robot aparece en En vivo")
	}
}

// TestProgramRecsClaude (CA6): con una redacción de Claude guardada, la
// tarjeta usa su texto y la evidencia sigue saliendo de Go.
func TestProgramRecsClaude(t *testing.T) {
	now := noonToday()
	day := dayStart(now)
	st, _, h, _ := recServer(t, now)
	keyMan := segmentKey("UC1", Segment{VideoID: "v1", Program: "LA MAÑANA", Start: day.Add(8 * time.Hour)})
	st.AddRecs(ProgramRecs{Key: keyMan, ChannelID: "UC1", Program: "LA MAÑANA", From: day.Add(8 * time.Hour), To: day.Add(10 * time.Hour), Model: defaultClaudeModel,
		Recs: []Rec{{Signal: "tecnica", Role: "tecnica", Prio: "media", Title: "Chequear la consola", Action: "Antes de mañana.", Facts: "3 quejas técnicas de 3 personas · 08:10–08:12"},
			// Redactada con otros números (llegaron más datos después): no vale (revisión 1, punto 4).
			{Signal: "supers", Role: "comercial", Prio: "alta", Title: "Vender 9 espacios", Action: "Ya.", Facts: "9 pagos · ARS 9.000,00"}}})
	sec := from(getBody(h, "/parcial/programas?dia=hoy&pestana=recomendaciones&tramo="+url.QueryEscape(keyMan)), `id="recomendaciones"`, "</section>")
	for _, w := range []string{"Chequear la consola", "Antes de mañana.", "3 quejas técnicas de 3 personas", `class="rec media"`, "Hubo Super Chats"} {
		if !strings.Contains(sec, w) {
			t.Errorf("falta %q", w)
		}
	}
	if strings.Contains(sec, "Revisar el audio y la transmisión") {
		t.Error("con la redacción de Claude, el texto fijo de esa señal no va")
	}
	if strings.Contains(sec, "Vender 9 espacios") {
		t.Error("una redacción con otros números se mostró igual")
	}
}

// TestRecMarks (CA7): Hecha, No sirve y Deshacer, con las guardas, valores
// en lista cerrada y guardadas aparte.
func TestRecMarks(t *testing.T) {
	now := noonToday()
	day := dayStart(now)
	_, srv, h, path := recServer(t, now)
	keyMan := segmentKey("UC1", Segment{VideoID: "v1", Program: "LA MAÑANA", Start: day.Add(8 * time.Hour)})
	form := url.Values{"tramo": {keyMan}, "senal": {"tecnica"}, "para": {"tecnica"}, "estado": {"hecha"}, "dia": {"hoy"}, "pestana": {"recomendaciones"}, "abrir": {"rec1"}}
	post := func(v url.Values, edit func(*http.Request)) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, postLocal("/recomendaciones/marca", v.Encode(), edit))
		return w
	}
	with := func(k, val string) url.Values {
		v := url.Values{}
		for key, vs := range form {
			v[key] = slices.Clone(vs)
		}
		v.Set(k, val)
		return v
	}
	for _, c := range []struct {
		name string
		v    url.Values
		edit func(*http.Request)
		code int
	}{
		{"sin HX-Request", form, func(r *http.Request) { r.Header.Del("HX-Request") }, 403},
		{"desde otra PC", form, func(r *http.Request) { r.RemoteAddr = "192.168.0.9:5000" }, 403},
		{"estado fuera de lista", with("estado", "borrar"), nil, 400},
		{"rol fuera de lista", with("para", "gerencia"), nil, 400},
		{"rol que no es de la señal", with("para", "comercial"), nil, 400},
		{"señal que no existe", with("senal", "pico"), nil, 400},
		{"tramo que no existe", with("tramo", "UC1|nada"), nil, 400},
		{"cuerpo grande", with("x", strings.Repeat("a", 2000)), nil, 400},
	} {
		t.Run(c.name, func(t *testing.T) {
			if w := post(c.v, c.edit); w.Code != c.code {
				t.Errorf("código %d, quiero %d: %s", w.Code, c.code, w.Body.String())
			}
		})
	}
	if b, _ := os.ReadFile(path); len(b) != 0 {
		t.Fatalf("un pedido rechazado escribió: %s", b)
	}
	w := post(form, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `id="recomendaciones"`) || !strings.Contains(w.Body.String(), `class="rec alta hecha"`) || !strings.Contains(w.Body.String(), `value="deshacer"`) {
		t.Fatalf("marcar: %d\n%s", w.Code, w.Body.String())
	}
	// La marcada va al final y sigue desplegada la misma (la clave no depende del orden).
	if sec := w.Body.String(); strings.Index(sec, "Hubo Super Chats") > strings.Index(sec, "Revisar el audio") {
		t.Error("la marcada no pasó al final")
	}
	if open := from(w.Body.String(), `id="pf-rec1"`, "</details>"); !strings.Contains(open, "no se escucha nada") || strings.Index(w.Body.String(), `id="pf-rec1"`) < strings.Index(w.Body.String(), "Revisar el audio") {
		t.Error("después de marcar quedó desplegada otra tarjeta")
	}
	id := recID(keyMan, "tecnica", "tecnica")
	if srv.recMarks.State(id) != "hecha" {
		t.Error("no quedó marcada")
	}
	again, err := OpenRecMarks(path, now)
	if err != nil || again.State(id) != "hecha" {
		t.Errorf("después de reabrir: %v %q", err, again.State(id))
	}
	if post(with("estado", "deshacer"), nil).Code != 200 || srv.recMarks.State(id) != "" {
		t.Error("deshacer no anda")
	}
	if old, _ := OpenRecMarks(path, now.Add(31*24*time.Hour)); old.State(id) != "" {
		t.Error("las marcas vencen a los 30 días")
	}
}

// TestParte (CA8, CA9): el parte de ayer y de la semana por rol, sin lo
// marcado como No sirve, también en texto para copiar.
func TestParte(t *testing.T) {
	now := noonToday()
	day := dayStart(now)
	_, srv, h, _ := recServer(t, now)
	keyAyer := segmentKey("UC1", Segment{VideoID: "v3", Program: "AYER", Start: day.Add(-5 * time.Hour)})
	body := getBody(h, "/parte?rol=tecnica")
	for _, w := range []string{"Parte de ayer", "Técnica", "AYER", "Radio Test", "Revisar el audio y la transmisión", "3 quejas técnicas de 3 personas",
		`aria-label="Para quién"`, `href="/parte.txt?periodo=dia&amp;rol=tecnica"`, `href="/parte?periodo=semana&amp;rol=tecnica"`, "@media print", "1 de prioridad alta"} {
		if !strings.Contains(body, w) {
			t.Errorf("falta %q", w)
		}
	}
	if strings.Contains(body, "LA MAÑANA") {
		t.Error("el parte de ayer tiene un programa de hoy")
	}
	if com := getBody(h, "/parte?rol=comercial"); strings.Contains(com, "Revisar el audio") {
		t.Error("el parte de Comercial tiene lo de Técnica")
	}
	if bad := getBody(h, "/parte?rol=%3Cx%3E&periodo=mes"); !strings.Contains(bad, "Parte de ayer") || strings.Contains(bad, "&lt;x&gt;") {
		t.Error("los valores fuera de lista se ignoran")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/parte.txt?rol=tecnica", nil))
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") || !strings.Contains(w.Body.String(), "Revisar el audio y la transmisión") || strings.Contains(w.Body.String(), "<") {
		t.Errorf("parte.txt: %s\n%s", ct, w.Body.String())
	}
	week := getBody(h, "/parte?rol=tecnica&periodo=semana")
	if !strings.Contains(week, "Parte de los últimos 7 días") || !strings.Contains(week, "AYER") {
		t.Error("el parte semanal no anda")
	}
	// El parte se arma una vez por día: un mensaje nuevo de ayer no lo cambia,
	// una redacción nueva de un programa de ayer sí (CA8).
	cruce := func(id, a, text string) Message {
		m := recMsg(id, a, text, day.Add(-30*time.Minute))
		m.VideoID, m.Program = "v0", "CRUCE"
		return m
	}
	st := srv.st
	st.Add([]Message{cruce("c1", "Ana", "no se escucha nada"), cruce("c2", "Beto", "sin audio"), cruce("c3", "Caro", "se escucha mal")})
	if n := strings.Count(getBody(h, "/parte?rol=tecnica"), "Revisar el audio"); n != 1 {
		t.Errorf("el parte se volvió a armar sin que cambie nada: %d", n)
	}
	st.AddRecs(ProgramRecs{Key: "UC1|otro", ChannelID: "UC1", From: day.Add(-5 * time.Hour), To: day.Add(-4 * time.Hour)})
	if n := strings.Count(getBody(h, "/parte?rol=tecnica"), "Revisar el audio"); n != 2 {
		t.Errorf("con una redacción nueva de ayer, el parte no se volvió a armar: %d", n)
	}
	if err := srv.recMarks.Mark(recID(keyAyer, "tecnica", "tecnica"), "nosirve", now); err != nil {
		t.Fatal(err)
	}
	if after := getBody(h, "/parte?rol=tecnica"); strings.Count(after, "Revisar el audio") != 1 || strings.Contains(after, "· AYER ·") {
		t.Error("lo marcado como No sirve sigue en el parte")
	}
}

// TestMergeParte (revisión 1, punto 2): en el parte de 7 días, lo que salió en
// varias emisiones va una vez con la cantidad de emisiones (días) distintas;
// preguntas, temas y clips no se juntan.
func TestMergeParte(t *testing.T) {
	day := dayStart(noonToday())
	it := func(kind, signal string, d int, prio string) parteItem {
		return parteItem{Rec: Rec{Signal: signal, Role: "tecnica", Prio: prio, Title: kind + fmt.Sprint(d)}, Kind: kind, Channel: "Radio", Program: "LA MAÑANA",
			Start: day.AddDate(0, 0, -d).Add(8 * time.Hour)}
	}
	got := mergeParte([]parteItem{
		it("tecnica", "tecnica", 3, "alta"), it("tecnica", "tecnica", 1, "media"), it("tecnica", "tecnica", 1, "media"),
		it("clip", "clip|1", 2, "media"), it("clip", "clip|2", 2, "media"),
		it("participacion", "participacion|arriba", 1, "baja"), it("participacion", "participacion|abajo", 2, "media"),
	})
	if len(got) != 5 {
		t.Fatalf("mergeParte = %+v", got)
	}
	if got[0].Times != 2 || got[0].Title != "tecnica1" || got[0].Prio != "alta" {
		t.Errorf("técnica = %+v (2 días distintos, el texto más nuevo y la prioridad más alta)", got[0])
	}
	for _, g := range got[1:] {
		if g.Times > 1 {
			t.Errorf("%s se juntó: %+v", g.Signal, g)
		}
	}
}
