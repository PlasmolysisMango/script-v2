package cards

import (
	"fmt"
	"slices"

	"jbs/internal/engine"
	"jbs/internal/plugins/points"
	"jbs/internal/plugins/vote"
	"jbs/internal/script"
)

type Plugin struct{}

type state struct {
	Hands    map[string][]string
	Deck     []string
	Color    string
	Number   int
	Top      string
	Current  string
	Turn     int
	Ranks    []string
	Finished int
	Passes   int
	Scored   bool
	Words    map[string][]string
}

type HandCard struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Color  string `json:"color,omitempty"`
	Number int    `json:"number,omitempty"`
	Title  string `json:"title,omitempty"`
	Text   string `json:"text,omitempty"`
}

type View struct {
	Hand      []HandCard            `json:"hand"`
	Counts    map[string]int        `json:"counts"`
	Top       string                `json:"top,omitempty"`
	Color     string                `json:"color,omitempty"`
	Number    int                   `json:"number,omitempty"`
	Current   string                `json:"current,omitempty"`
	Finished  int                   `json:"finished"`
	Ranks     []string              `json:"ranks,omitempty"`
	Persona   string                `json:"persona,omitempty"`
	Words     []string              `json:"words,omitempty"`
	Playable  []HandCard            `json:"playable,omitempty"`
	HostHands map[string][]HandCard `json:"host_hands,omitempty"`
	ScoreNote string                `json:"score_note,omitempty"`
}

func (Plugin) Name() string { return "cards" }
func (Plugin) Init(r *engine.Registry) error {
	r.PhaseType("cards", engine.PhaseType{
		OnEnter: enter,
		EndIf:   func(c *engine.Ctx) bool { return end(c) },
		Allowed: allowed,
		Hint: func(c *engine.Ctx, pid string) string {
			s := get(c)
			if s == nil {
				return ""
			}
			if c.Step().Str("cards_mode", "") == "score" || c.Step().Str("source_type", "") == "score" {
				return "The cards are scored automatically from the finish order."
			}
			if s.Current == pid {
				return "Play a matching card, or draw if you have no playable card."
			}
			if s.Current != "" {
				return "Wait for your turn."
			}
			return "The card game is complete."
		},
	})
	r.Action("card.play", play)
	r.Action("card.draw", draw)
	r.Action("card.pass", pass)
	r.View(func(c *engine.Ctx, pid string) (string, any) {
		s := get(c)
		game := c.Script().CardGame
		if s == nil || game == nil {
			return "", nil
		}
		v := View{Counts: map[string]int{}, Top: s.Top, Color: s.Color, Number: s.Number, Current: s.Current,
			Finished: s.Finished, Ranks: s.Ranks}
		for _, id := range s.Hands[pid] {
			v.Hand = append(v.Hand, handCard(game, id))
		}
		for _, seat := range c.Seats() {
			v.Counts[seat.ID] = len(s.Hands[seat.ID])
		}
		if ch := c.Script().Character(pid); ch != nil {
			v.Persona = ch.Letter
		}
		v.Words = append([]string{}, s.Words[pid]...)
		v.Playable = playable(c, s, pid)
		if pid == engine.HostID {
			v.HostHands = map[string][]HandCard{}
			for seat, ids := range s.Hands {
				for _, id := range ids {
					v.HostHands[seat] = append(v.HostHands[seat], handCard(game, id))
				}
			}
		}
		if s.Scored {
			v.ScoreNote = "已按出完顺序结算卡牌积分。"
		}
		return "cards", v
	})
	r.Panel(engine.Panel{ID: "cards", Plugin: "cards", Kind: "cards", Title: "Card game", DataKey: "cards", Order: 45})
	return nil
}

func get(c *engine.Ctx) *state { s, _ := c.Get("cards.state").(*state); return s }

func enter(c *engine.Ctx) {
	s := get(c)
	mode := c.Step().Str("cards_mode", c.Step().Str("source_type", ""))
	if mode == "score" {
		score(c, s)
		return
	}
	game := c.Script().CardGame
	if game == nil {
		return
	}
	s = &state{Hands: map[string][]string{}, Scored: false, Words: map[string][]string{}}
	deck := make([]string, 0, len(game.Cards))
	for _, card := range game.Cards {
		deck = append(deck, card.ID)
	}
	c.Shuffle(len(deck), func(i, j int) { deck[i], deck[j] = deck[j], deck[i] })
	for _, seat := range c.Seats() {
		if !seat.Joined {
			continue
		}
		for i := 0; i < game.Deal && len(deck) > 0; i++ {
			s.Hands[seat.ID] = append(s.Hands[seat.ID], deck[0])
			deck = deck[1:]
		}
	}
	s.Deck = deck
	s.Color = ""
	for i, word := range game.Words {
		if word.RoleID != "" {
			s.Words[word.RoleID] = append(s.Words[word.RoleID], word.Word)
			continue
		}
		seats := joined(c)
		if len(seats) > 0 {
			s.Words[seats[i%len(seats)]] = append(s.Words[seats[i%len(seats)]], word.Word)
		}
	}
	s.Current = firstTurn(c, s)
	c.Set("cards.state", s)
	c.Emit("card.deal", "", engine.Public(), map[string]any{"counts": counts(s), "current": s.Current, "mode": "play"})
}

func joined(c *engine.Ctx) []string {
	out := []string{}
	for _, seat := range c.Seats() {
		if seat.Joined {
			out = append(out, seat.ID)
		}
	}
	return out
}

func firstTurn(c *engine.Ctx, s *state) string {
	for _, pid := range joined(c) {
		if len(s.Hands[pid]) > 0 {
			return pid
		}
	}
	return ""
}

func allowed(c *engine.Ctx, pid string) []engine.ActionSpec {
	s := get(c)
	if s == nil || s.Scored {
		return nil
	}
	if pid == engine.HostID {
		return []engine.ActionSpec{{Type: "card.pass", Desc: "Force the turn to pass", Optional: true}}
	}
	if s.Current != pid {
		return nil
	}
	game := c.Script().CardGame
	var hand []engine.Option
	for _, id := range s.Hands[pid] {
		card := cardByID(game, id)
		if card == nil {
			continue
		}
		hand = append(hand, engine.Option{Value: id, Label: card.Title})
	}
	out := []engine.ActionSpec{}
	if len(hand) > 0 {
		spec := engine.ActionSpec{Type: "card.play", Desc: "Play a card", Params: map[string]string{"card": "Card ID"}, Options: hand,
			Fields: []engine.Field{
				{Key: "card", Label: "Card", Kind: "select", Options: hand, Required: true},
				{Key: "color", Label: "Choose a color", Kind: "select", Options: colorOptions(game), Placeholder: "Only for recolor cards"},
				{Key: "target", Label: "Target", Kind: "select", Options: seatOptions(c, pid), Placeholder: "Only for swap/inspect cards"},
			}}
		out = append(out, spec)
	}
	if len(s.Deck) > 0 {
		out = append(out, engine.ActionSpec{Type: "card.draw", Desc: "Draw a card"})
	}
	out = append(out, engine.ActionSpec{Type: "card.pass", Desc: "Pass"})
	return out
}

func play(c *engine.Ctx, a engine.Action) error {
	s := get(c)
	if s == nil || s.Scored {
		return engine.ErrNotAllowed
	}
	if a.Actor != engine.HostID && s.Current != a.Actor {
		return engine.ErrNotAllowed
	}
	var p struct {
		Card   string `json:"card"`
		Color  string `json:"color"`
		Target string `json:"target"`
	}
	if err := a.Decode(&p); err != nil {
		return err
	}
	game := c.Script().CardGame
	card := cardByID(game, p.Card)
	if card == nil || !slices.Contains(s.Hands[a.Actor], p.Card) {
		return fmt.Errorf("card is not in your hand")
	}
	if !matches(card, s) && len(playable(c, s, a.Actor)) > 0 {
		return fmt.Errorf("card does not match the current color or number")
	}
	if card.Kind == "recolor" && p.Color == "" {
		return fmt.Errorf("choose a color for the recolor card")
	}
	if p.Target != "" && c.Seat(p.Target) == nil {
		return fmt.Errorf("unknown target")
	}
	s.Hands[a.Actor] = slices.DeleteFunc(s.Hands[a.Actor], func(id string) bool { return id == p.Card })
	s.Passes = 0
	s.Top, s.Color, s.Number = card.ID, card.Color, card.Number
	if card.Kind == "recolor" {
		s.Color = p.Color
	}
	c.Emit("card.play", a.Actor, engine.Public(), map[string]any{"card": card.ID, "title": card.Title, "kind": card.Kind,
		"color": s.Color, "number": s.Number, "target": p.Target, "count": len(s.Hands[a.Actor])})
	if card.Text != "" && card.Kind == "clue" {
		c.Emit("card.clue", a.Actor, engine.Public(), map[string]any{"card": card.ID, "title": card.Title, "text": card.Text})
	}
	if len(s.Hands[a.Actor]) == 0 {
		s.Finished++
		s.Ranks = append(s.Ranks, a.Actor)
	}
	if card.Kind == "stop" {
		next(c, s)
	}
	next(c, s)
	return nil
}

func draw(c *engine.Ctx, a engine.Action) error {
	s := get(c)
	if s == nil || s.Scored || len(s.Deck) == 0 {
		return fmt.Errorf("no card to draw")
	}
	if a.Actor != engine.HostID && s.Current != a.Actor {
		return engine.ErrNotAllowed
	}
	id := s.Deck[0]
	s.Deck = s.Deck[1:]
	s.Hands[a.Actor] = append(s.Hands[a.Actor], id)
	card := cardByID(c.Script().CardGame, id)
	title := id
	if card != nil {
		title = card.Title
	}
	c.Emit("card.draw", a.Actor, engine.To(a.Actor), map[string]any{"card": id, "title": title, "count": len(s.Hands[a.Actor])})
	next(c, s)
	return nil
}

func pass(c *engine.Ctx, a engine.Action) error {
	s := get(c)
	if s == nil || s.Scored || (a.Actor != engine.HostID && s.Current != a.Actor) {
		return engine.ErrNotAllowed
	}
	s.Passes++
	if a.Actor != engine.HostID && s.Passes >= active(c) && len(s.Hands[a.Actor]) > 0 {
		// No seat can follow the top card: the current seat must play any
		// card (public approximation of the tabletop rule).
		id := s.Hands[a.Actor][c.Intn(len(s.Hands[a.Actor]))]
		action := engine.NewAction("card.play", map[string]string{"card": id, "color": forceColor(c.Script().CardGame)})
		action.Actor = a.Actor
		return play(c, action)
	}
	c.Emit("card.pass", a.Actor, engine.Public(), nil)
	next(c, s)
	return nil
}

func active(c *engine.Ctx) int {
	n := 0
	s := get(c)
	if s == nil {
		return 0
	}
	for _, pid := range joined(c) {
		if len(s.Hands[pid]) > 0 {
			n++
		}
	}
	return n
}

func forceColor(game *script.CardGame) string {
	if game != nil {
		for _, card := range game.Cards {
			if card.Color != "" {
				return card.Color
			}
		}
	}
	return "红"
}

func next(c *engine.Ctx, s *state) {
	seats := joined(c)
	if len(seats) == 0 {
		s.Current = ""
		return
	}
	start := 0
	if i := slices.Index(seats, s.Current); i >= 0 {
		start = i + 1
	}
	for i := 0; i < len(seats); i++ {
		pid := seats[(start+i)%len(seats)]
		if len(s.Hands[pid]) > 0 {
			s.Current = pid
			c.Emit("card.turn", "", engine.Public(), map[string]any{"pid": pid})
			return
		}
	}
	s.Current = ""
}

func end(c *engine.Ctx) bool {
	s := get(c)
	if s == nil {
		return true
	}
	if s.Scored {
		return true
	}
	if c.Step().Str("cards_mode", "") == "score" || c.Step().Str("source_type", "") == "score" {
		return true
	}
	for _, pid := range joined(c) {
		if len(s.Hands[pid]) > 0 {
			return false
		}
	}
	return true
}

func score(c *engine.Ctx, s *state) {
	if s == nil {
		s = &state{Hands: map[string][]string{}, Words: map[string][]string{}}
		c.Set("cards.state", s)
	}
	if s.Scored {
		return
	}
	s.Scored = true
	game := c.Script().CardGame
	var pts []int
	if game != nil {
		pts = game.Points
	}
	if len(pts) == 0 {
		pts = []int{10, 7, 4, 1, 0}
	}
	// Any player with cards left is ranked after finishers by remaining count.
	left := []string{}
	for _, pid := range joined(c) {
		if len(s.Hands[pid]) > 0 {
			left = append(left, pid)
		}
	}
	slices.SortStableFunc(left, func(a, b string) int { return len(s.Hands[a]) - len(s.Hands[b]) })
	order := append(append([]string{}, s.Ranks...), left...)
	for i, pid := range order {
		if i >= len(pts) {
			break
		}
		points.Adjust(c, pid, pts[i])
	}
	// The card game resolves the murderer identity as a question for reveal.
	correct := ""
	for _, v := range c.Script().Votes {
		if v.ID == "card_murderer" {
			correct = v.Correct
			if correct == "" && len(v.Corrects) > 0 {
				correct = v.Corrects[0]
			}
		}
	}
	if correct != "" {
		results := vote.Results(c)
		results = append(results, vote.Result{Question: "card_murderer", Winner: correct, Counts: map[string]int{correct: len(order)}, Correct: true})
		c.Set("vote.results", results)
		c.Emit("vote.result", "", engine.Public(), map[string]any{"question": "card_murderer", "winner": correct, "correct": true})
	}
	c.Emit("card.score", "", engine.Public(), map[string]any{"order": order, "points": pts, "murderer": correct})
	c.Advance()
}

func cardByID(game *script.CardGame, id string) *script.Card {
	if game == nil {
		return nil
	}
	for i := range game.Cards {
		if game.Cards[i].ID == id {
			return &game.Cards[i]
		}
	}
	return nil
}

func handCard(game *script.CardGame, id string) HandCard {
	card := cardByID(game, id)
	if card == nil {
		return HandCard{ID: id}
	}
	return HandCard{ID: card.ID, Kind: card.Kind, Color: card.Color, Number: card.Number, Title: card.Title, Text: card.Text}
}

func matches(card *script.Card, s *state) bool {
	if card == nil {
		return false
	}
	if s.Color == "" && s.Number == 0 {
		return true
	}
	if card.Kind == "black" || card.Kind == "recolor" || card.Color == "" {
		return true
	}
	return card.Color == s.Color || card.Number == s.Number
}

func playable(c *engine.Ctx, s *state, pid string) []HandCard {
	var out []HandCard
	for _, id := range s.Hands[pid] {
		card := cardByID(c.Script().CardGame, id)
		if matches(card, s) {
			out = append(out, handCard(c.Script().CardGame, id))
		}
	}
	return out
}

func counts(s *state) map[string]int {
	out := map[string]int{}
	for pid, hand := range s.Hands {
		out[pid] = len(hand)
	}
	return out
}

func colorOptions(game *script.CardGame) []engine.Option {
	seen := map[string]bool{}
	var out []engine.Option
	if game != nil {
		for _, card := range game.Cards {
			if card.Color != "" && !seen[card.Color] {
				seen[card.Color] = true
				out = append(out, engine.Option{Value: card.Color, Label: card.Color})
			}
		}
	}
	return out
}

func seatOptions(c *engine.Ctx, pid string) []engine.Option {
	var out []engine.Option
	for _, seat := range c.Seats() {
		if seat.Joined && seat.ID != pid {
			out = append(out, engine.Option{Value: seat.ID, Label: seat.CharName})
		}
	}
	return out
}
