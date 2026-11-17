package dice

import (
	"encoding/json"
	"fmt"
	"strings"

	"jbs/internal/engine"
)

type Plugin struct{}

type Round struct {
	Rolls    map[string]int `json:"rolls"`
	Asker    string         `json:"asker"`
	Answerer string         `json:"answerer"`
	Question string         `json:"question"`
	Answer   string         `json:"answer"`
}

type state struct {
	Round     int
	Total     int
	Sides     int
	Tie       map[string]int
	Questions map[string][]string
	Keywords  map[string]map[string]string
	Rolls     map[string]int
	Asker     string
	Answerer  string
	Question  string
	Answer    string
	History   []Round
	Done      bool
}

type View struct {
	Round      int               `json:"round"`
	Total      int               `json:"total"`
	Sides      int               `json:"sides"`
	Rolls      map[string]int    `json:"rolls,omitempty"`
	Asker      string            `json:"asker,omitempty"`
	Answerer   string            `json:"answerer,omitempty"`
	Question   string            `json:"question,omitempty"`
	Answer     string            `json:"answer,omitempty"`
	Questions  []string          `json:"questions,omitempty"`
	Keywords   map[string]string `json:"keywords,omitempty"`
	History    []Round           `json:"history,omitempty"`
	WaitingFor string            `json:"waiting_for,omitempty"`
}

func (Plugin) Name() string { return "dice" }
func (Plugin) Init(r *engine.Registry) error {
	r.PhaseType("dice", engine.PhaseType{
		OnEnter: enter,
		EndIf:   func(c *engine.Ctx) bool { s := get(c); return s != nil && s.Done },
		Allowed: allowed,
		Hint: func(c *engine.Ctx, pid string) string {
			s := get(c)
			if s == nil {
				return ""
			}
			switch pid {
			case s.Asker:
				return "You asked the question. Wait for the answer."
			case s.Answerer:
				return "Answer the question. If it touches a keyword, include the required detail."
			}
			return "Watch the dice round."
		},
	})
	r.Action("dice.roll", func(c *engine.Ctx, a engine.Action) error {
		if a.Actor != engine.HostID {
			return engine.ErrNotAllowed
		}
		s := get(c)
		if s == nil || s.Done {
			return engine.ErrNotAllowed
		}
		roll(c, s)
		return nil
	})
	r.Action("dice.ask", ask)
	r.Action("dice.answer", answer)
	r.Allowed(func(c *engine.Ctx, pid string) []engine.ActionSpec {
		if pid != engine.HostID || c.Step().Type != "dice" {
			return nil
		}
		s := get(c)
		if s == nil || s.Done {
			return nil
		}
		return []engine.ActionSpec{{Type: "dice.roll", Desc: "Resolve the round manually", Optional: true}}
	})
	r.View(func(c *engine.Ctx, pid string) (string, any) {
		s := get(c)
		if s == nil {
			return "", nil
		}
		v := View{Round: s.Round, Total: s.Total, Sides: s.Sides, Rolls: s.Rolls, Asker: s.Asker, Answerer: s.Answerer,
			Question: s.Question, Answer: s.Answer, History: s.History}
		if pid == s.Asker {
			v.Questions = s.Questions[pid]
		}
		if pid == s.Answerer || pid == engine.HostID {
			v.Keywords = s.Keywords[pid]
		}
		switch {
		case s.Asker == "":
		case s.Question == "":
			v.WaitingFor = "question"
		case s.Answer == "":
			v.WaitingFor = "answer"
		}
		return "dice", v
	})
	r.Panel(engine.Panel{ID: "dice", Plugin: "dice", Kind: "dice", Title: "Dice", DataKey: "dice", Order: 35})
	return nil
}

func get(c *engine.Ctx) *state { s, _ := c.Get("dice.state").(*state); return s }

func enter(c *engine.Ctx) {
	s := parse(c)
	roll(c, s)
}

func parse(c *engine.Ctx) *state {
	full := parseConfig(c.Step().Params["dice"])
	if full.Total == 0 {
		full = parseConfig(c.Step().Params["dice_config"])
	}
	if full.Total == 0 {
		full = parseConfig(c.Step().Params["diceConfig"])
	}
	s := &state{Total: full.Total, Sides: full.Sides, Tie: map[string]int{}, Questions: map[string][]string{},
		Keywords: map[string]map[string]string{}, History: []Round{}}
	if s.Total <= 0 {
		s.Total = 1
	}
	if s.Sides <= 0 {
		s.Sides = 6
	}
	s.Tie = full.Tie
	s.Questions = full.Questions
	s.Keywords = full.Keywords
	c.Set("dice.state", s)
	return s
}

type config struct {
	Total     int                          `json:"rounds"`
	Sides     int                          `json:"sides"`
	Tie       map[string]int               `json:"tieBreak"`
	Questions map[string][]string          `json:"questions"`
	Keywords  map[string]map[string]string `json:"keywords"`
}

func parseConfig(v any) config {
	b, err := json.Marshal(v)
	if err != nil {
		return config{}
	}
	var c config
	_ = json.Unmarshal(b, &c)
	return c
}

func roll(c *engine.Ctx, s *state) {
	if s.Done {
		return
	}
	s.Round++
	if s.Round > s.Total {
		s.Done = true
		c.Emit("dice.end", "", engine.Public(), map[string]any{"rounds": s.Total})
		return
	}
	s.Rolls, s.Question, s.Answer = map[string]int{}, "", ""
	rolled := []string{}
	for _, seat := range c.Seats() {
		if seat.Joined {
			s.Rolls[seat.ID] = c.Intn(s.Sides) + 1
			rolled = append(rolled, seat.ID)
		}
	}
	if len(rolled) == 0 {
		s.Done = true
		return
	}
	high, low := rolled[0], rolled[0]
	for _, pid := range rolled[1:] {
		if s.Rolls[pid] > s.Rolls[high] || (s.Rolls[pid] == s.Rolls[high] && s.Tie[pid] > s.Tie[high]) {
			high = pid
		}
		if s.Rolls[pid] < s.Rolls[low] || (s.Rolls[pid] == s.Rolls[low] && s.Tie[pid] < s.Tie[low]) {
			low = pid
		}
	}
	s.Asker, s.Answerer = high, low
	c.Emit("dice.round", "", engine.Public(), map[string]any{"round": s.Round, "rolls": s.Rolls, "asker": s.Asker, "answerer": s.Answerer})
	if s.Asker == s.Answerer {
		s.Question = "（本轮只有一名玩家，请分享一段回忆。）"
	}
}

func allowed(c *engine.Ctx, pid string) []engine.ActionSpec {
	s := get(c)
	if s == nil || s.Done {
		return nil
	}
	if pid == engine.HostID {
		return []engine.ActionSpec{{Type: "dice.roll", Desc: "Resolve the round", Optional: true}}
	}
	if pid == s.Asker && s.Question == "" && s.Answer == "" {
		opts := []engine.Option{}
		for _, q := range s.Questions[pid] {
			opts = append(opts, engine.Option{Value: q, Label: q})
		}
		return []engine.ActionSpec{{Type: "dice.ask", Desc: "Ask the lowest roller", Params: map[string]string{"question": "Question"}, Options: opts}}
	}
	if pid == s.Answerer && s.Question != "" && s.Answer == "" {
		return []engine.ActionSpec{{Type: "dice.answer", Desc: "Answer the question", Params: map[string]string{"text": "Answer"}}}
	}
	return nil
}

func ask(c *engine.Ctx, a engine.Action) error {
	s := get(c)
	if s == nil || s.Done || a.Actor != s.Asker || s.Question != "" {
		return engine.ErrNotAllowed
	}
	var p struct {
		Question string `json:"question"`
	}
	if err := a.Decode(&p); err != nil {
		return err
	}
	if len(s.Questions[a.Actor]) > 0 && !contains(s.Questions[a.Actor], p.Question) {
		return fmt.Errorf("question is not one of your cards")
	}
	s.Question = p.Question
	c.Emit("dice.question", a.Actor, engine.Public(), map[string]any{"round": s.Round, "asker": a.Actor, "answerer": s.Answerer, "question": p.Question})
	return nil
}

func answer(c *engine.Ctx, a engine.Action) error {
	s := get(c)
	if s == nil || s.Done || a.Actor != s.Answerer || s.Question == "" || s.Answer != "" {
		return engine.ErrNotAllowed
	}
	var p struct {
		Text string `json:"text"`
	}
	if err := a.Decode(&p); err != nil {
		return err
	}
	text := strings.TrimSpace(p.Text)
	if text == "" {
		return fmt.Errorf("answer must not be empty")
	}
	required := requiredDetail(s, a.Actor)
	if required != "" && !strings.Contains(text, required) {
		return fmt.Errorf("answer must include the required detail: %s", required)
	}
	s.Answer = text
	s.History = append(s.History, Round{Rolls: clone(s.Rolls), Asker: s.Asker, Answerer: s.Answerer, Question: s.Question, Answer: text})
	c.Emit("dice.answer", a.Actor, engine.Public(), map[string]any{"round": s.Round, "answerer": a.Actor, "question": s.Question, "text": text})
	roll(c, s)
	return nil
}

func requiredDetail(s *state, pid string) string {
	for trigger, detail := range s.Keywords[pid] {
		if strings.Contains(s.Question, trigger) {
			return detail
		}
	}
	return ""
}

func CloneRequired(c *engine.Ctx, pid string) string {
	s := get(c)
	if s == nil {
		return ""
	}
	return requiredDetail(s, pid)
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func clone(m map[string]int) map[string]int {
	out := map[string]int{}
	for k, v := range m {
		out[k] = v
	}
	return out
}
