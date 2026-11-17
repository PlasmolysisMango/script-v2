package kv1

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"jbs/internal/script"
)

// Import converts the local KV1 script collection into framework packages.
type Options struct {
	Only map[string]bool
}

type Manifest struct {
	ImportedAt string         `json:"imported_at"`
	Root       string         `json:"root"`
	Scripts    []ScriptReport `json:"scripts"`
}

type ScriptReport struct {
	ID         string         `json:"id"`
	Title      string         `json:"title"`
	SourceDir  string         `json:"source_dir"`
	Characters int            `json:"characters"`
	Clues      int            `json:"clues"`
	Phases     int            `json:"phases"`
	PhaseTypes map[string]int `json:"phase_types"`
	Votes      int            `json:"votes"`
	Endings    int            `json:"endings"`
	Assets     []string       `json:"assets,omitempty"`
	Adapted    []string       `json:"adapted,omitempty"`
	Warnings   []string       `json:"warnings,omitempty"`
	Error      string         `json:"error,omitempty"`
}

type sourceScript struct {
	Meta     map[string]any   `json:"meta"`
	Roles    []map[string]any `json:"roles"`
	Clues    []map[string]any `json:"clues"`
	Phases   []map[string]any `json:"phases"`
	Truth    map[string]any   `json:"truth"`
	Facts    []map[string]any `json:"facts"`
	CardGame *script.CardGame `json:"cardGame"`
}

var actHeading = regexp.MustCompile(`(?m)^\s*#{1,4}\s*(序章|尾声|终章|第[一二三四五六七八九十百零0-9]+幕|第[一二三四五六七八九十百零0-9]+章)[^\n]*$`)

func Import(root, out, assets string, opts Options) (*Manifest, error) {
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	if assets == "" {
		assets = filepath.Join(out, "assets")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	m := &Manifest{ImportedAt: time.Now().UTC().Format(time.RFC3339), Root: filepath.Base(root)}
	for _, de := range dirs {
		if !de.IsDir() {
			continue
		}
		dir := filepath.Join(root, de.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "script.json"))
		if err != nil {
			continue
		}
		id := scriptID(de.Name())
		if len(opts.Only) > 0 && !opts.Only[id] {
			continue
		}
		rep := ScriptReport{ID: id, SourceDir: de.Name(), PhaseTypes: map[string]int{}}
		pkg, assetsFound, warnings, adapted, err := convert(raw, id, de.Name())
		rep.Warnings, rep.Adapted, rep.Assets = warnings, adapted, assetsFound
		if err != nil {
			rep.Error = err.Error()
			m.Scripts = append(m.Scripts, rep)
			continue
		}
		if err := pkg.Validate(); err != nil {
			rep.Error = "validate: " + err.Error()
			m.Scripts = append(m.Scripts, rep)
			continue
		}
		if err := pkg.Save(filepath.Join(out, id)); err != nil {
			rep.Error = "save: " + err.Error()
			m.Scripts = append(m.Scripts, rep)
			continue
		}
		copied, err := copyAssets(dir, filepath.Join(assets, de.Name()))
		if err != nil {
			rep.Warnings = append(rep.Warnings, "assets: "+err.Error())
		}
		if len(copied) > 0 {
			rep.Assets = append(rep.Assets, copied...)
		}
		rep.Title, rep.Characters, rep.Clues, rep.Phases = pkg.Meta.Title, len(pkg.Characters), len(pkg.Clues), len(pkg.Flow)
		rep.Votes, rep.Endings = len(pkg.Votes), len(pkg.Endings)
		rep.Adapted = append(rep.Adapted, specialNotes(id)...)
		for _, s := range pkg.Flow {
			rep.PhaseTypes[s.Type]++
		}
		m.Scripts = append(m.Scripts, rep)
	}
	if err := writeReports(out, m); err != nil {
		return nil, err
	}
	return m, nil
}

func scriptID(dir string) string {
	base := strings.ToLower(dir)
	if strings.HasPrefix(base, "ai-01") {
		return "ai01"
	}
	if strings.HasPrefix(base, "ai-02") {
		return "ai02"
	}
	if strings.HasPrefix(base, "aofu") {
		return "aofu"
	}
	if strings.HasPrefix(base, "k") {
		n := 0
		for _, r := range base[1:] {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
		}
		if n > 0 {
			return fmt.Sprintf("k%d", n)
		}
	}
	re := regexp.MustCompile(`[^a-z0-9]+`)
	return strings.Trim(re.ReplaceAllString(base, "-"), "-")
}

func convert(raw []byte, id, dirName string) (*script.Package, []string, []string, []string, error) {
	var src sourceScript
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("parse: %w", err)
	}
	var warnings, adapted, assets []string
	readingCount := 0
	for _, ph := range src.Phases {
		if str(ph, "type") == "reading" {
			readingCount++
		}
	}
	if readingCount == 0 {
		readingCount = 1
	}
	acts := make([]script.Act, readingCount)
	for i := range acts {
		acts[i] = script.Act{ID: i + 1, Title: fmt.Sprintf("第%d幕", i+1)}
	}
	chars := make([]script.Character, 0, len(src.Roles))
	killerIDs := stringSlice(src.Truth, "killerRoleIds")
	if len(killerIDs) == 0 && str(src.Truth, "killerRoleId") != "" {
		killerIDs = []string{str(src.Truth, "killerRoleId")}
	}
	npc := str(src.Truth, "killerKind") == "npc"
	killerSet := map[string]bool{}
	for _, k := range killerIDs {
		killerSet[k] = true
	}
	for _, r := range src.Roles {
		rid := str(r, "id")
		pub := mapOf(r, "public")
		priv := mapOf(r, "private")
		secs, splitWarn := splitSections(str(priv, "script"), readingCount)
		if splitWarn != "" {
			warnings = append(warnings, splitWarn)
		}
		intro := str(pub, "intro")
		if ap := str(pub, "appearance"); ap != "" {
			if intro != "" {
				intro += "\n\n"
			}
			intro += ap
		}
		ch := script.Character{
			ID: rid, Name: str(r, "name"), PublicIntro: intro,
			TTS:      script.Voice{Hint: "zh-CN", Rate: 1, Pitch: 1},
			Sections: secs, Points: num(r, "points"), Floor: str(r, "floor"),
			Letter: str(r, "letter"), CanLie: killerSet[rid] || npc, IsCulprit: killerSet[rid],
			Skill: convertSkill(mapOf(r, "skill")),
		}
		chars = append(chars, ch)
	}
	clueLocations := map[string]bool{}
	clues := make([]script.Clue, 0, len(src.Clues))
	for _, c := range src.Clues {
		loc := firstNonEmpty(str(c, "location"), str(c, "back"), str(c, "floor"), "未归类")
		clueLocations[loc] = true
		vis := "private"
		if b, _ := c["isPublic"].(bool); b {
			vis = "public"
		}
		unlock := (*script.Unlock)(nil)
		if ids := stringSlice(c, "unlockedBy"); len(ids) > 0 {
			unlock = &script.Unlock{RequiresClues: ids}
		}
		clues = append(clues, script.Clue{
			ID: str(c, "id"), Location: loc, Title: str(c, "title"), Body: str(c, "content"),
			Act: 1, Visibility: vis, Unlock: unlock, Kind: str(c, "kind"),
			Color: str(c, "color"), Back: str(c, "back"), Floor: str(c, "floor"),
			ImageURL: str(c, "imageUrl"), OwnerRoleID: str(c, "ownerRoleId"),
			SubjectRoleID: str(c, "subjectRoleId"), Keywords: stringSlice(c, "keywords"),
			KeywordsAll: boolv(c, "keywordsAll"), MustReveal: boolv(c, "mustReveal"),
			RequiresItems: stringSlice(c, "requiresItems"), UnlockedBy: stringSlice(c, "unlockedBy"),
			GrantsItem: str(c, "grantsItem"), GrantsClue: stringSlice(c, "grantsClue"),
			ExtraDraw: boolv(c, "extraDraw"), Number: num(c, "number"),
		})
	}
	locations := make([]script.Location, 0, len(clueLocations))
	names := make([]string, 0, len(clueLocations))
	for name := range clueLocations {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		locations = append(locations, script.Location{ID: name, Name: name, Act: 1})
	}
	// Clues with an owner still need a location for the existing validator/UI.
	// Empty locations are normalized above, so every encountered location is declared.

	readingIdx := 0
	flow := make([]script.Step, 0, len(src.Phases))
	votes := []script.Vote{}
	voteByPhase := map[string][]string{}
	primaryVote := ""
	for _, ph := range src.Phases {
		pid := str(ph, "id")
		srcType := str(ph, "type")
		typ := mapPhaseType(srcType, str(ph, "name"))
		p := copyParams(ph)
		if typ == "reading" {
			readingIdx++
			p["act"] = readingIdx
		} else if len(acts) > 1 {
			p["act"] = max(readingIdx, 1)
		}
		if typ == "cards" {
			p["cards_mode"] = srcType
		}
		if typ == "host" {
			adapted = append(adapted, pid+": "+srcType+" -> host (宿主裁定)")
		}
		// Build votes for this phase.
		if srcType == "vote" {
			var ids []string
			matched := false
			for _, ca := range caseList(src.Truth) {
				if str(ca, "phaseId") != pid {
					continue
				}
				matched = true
				vid := str(ca, "id")
				if vid == "" {
					vid = pid
				}
				corrects := stringSlice(ca, "killerRoleIds")
				if len(corrects) == 0 {
					corrects = stringSlice(ca, "killerRoleId")
				}
				v := script.Vote{ID: vid, Question: firstNonEmpty(str(ca, "name"), str(ph, "votePrompt")) + " 的凶手是谁？",
					Options: "characters", Correct: first(corrects), Corrects: corrects, PhaseID: pid}
				votes = append(votes, v)
				ids = append(ids, vid)
				if primaryVote == "" {
					primaryVote = vid
				}
			}
			if !matched {
				vid := pid
				if vid == "" {
					vid = fmt.Sprintf("vote%d", len(votes)+1)
				}
				question := firstNonEmpty(str(ph, "votePrompt"), "请指认你认为是凶手的人。")
				corrects := []string{}
				if !npc && isMurderVote(question, str(ph, "name")) {
					corrects = killerIDs
				}
				v := script.Vote{ID: vid, Question: question, Options: "characters",
					Correct: first(corrects), Corrects: corrects, AllowAbstain: strings.Contains(question, "弃权"), PhaseID: pid}
				if strings.Contains(str(ph, "name"), "侍寝") {
					var opts []script.VoteOption
					for _, ch := range chars {
						if ch.ID != "r_huanghou" {
							opts = append(opts, script.VoteOption{Value: ch.ID, Label: ch.Name})
						}
					}
					v.Options, v.OptionList = "options", opts
				}
				votes = append(votes, v)
				ids = append(ids, vid)
				if primaryVote == "" {
					primaryVote = vid
				}
			}
			p["vote_ids"] = ids
			voteByPhase[pid] = ids
		}
		flow = append(flow, script.Step{ID: pid, Type: typ, Title: str(ph, "name"), Params: p})
	}
	// K19 has no vote phase but needs a verdict for reveal.
	if len(votes) == 0 && len(src.Phases) > 0 {
		correct := first(killerIDs)
		if !npc {
			v := script.Vote{ID: "card_murderer", Question: "你认为凶手是谁？", Options: "characters", Correct: correct, Corrects: killerIDs}
			votes = append(votes, v)
			primaryVote = v.ID
			for i := range flow {
				if flow[i].Type == "cards" {
					flow[i].Params["vote_ids"] = []string{v.ID}
				}
			}
		}
	}
	if primaryVote == "" && len(votes) > 0 {
		primaryVote = votes[0].ID
	}
	truth := renderTruth(src.Truth)
	cases := make([]script.Case, 0)
	for _, ca := range caseList(src.Truth) {
		cases = append(cases, script.Case{ID: str(ca, "id"), Name: str(ca, "name"), PhaseID: str(ca, "phaseId"),
			KillerRoleIDs: stringSlice(ca, "killerRoleIds"), Method: str(ca, "method"), Motive: str(ca, "motive")})
	}
	endings := convertEndings(src.Truth, primaryVote)
	facts := make([]script.Fact, 0, len(src.Facts))
	for _, f := range src.Facts {
		facts = append(facts, script.Fact{ID: str(f, "id"), Text: str(f, "text")})
	}
	meta := map[string]any(src.Meta)
	pkg := &script.Package{
		Meta: script.Meta{ID: id, Title: str(meta, "title"), Players: maxInt(num(mapOf(meta, "playerCount"), "max"), len(chars)),
			Lang: "zh", Intro: str(meta, "summary"), Credits: str(meta, "author"),
			Source: "kv1:" + dirName, LicenseNote: "由仓库维护者提供；维护者确认已合法取得并同意在本仓库提交与发布。版权归原权利人，如有异议请联系删除。",
			Series: str(meta, "series"), Author: str(meta, "author"), Difficulty: str(meta, "difficulty"),
			Tags: stringSlice(meta, "tags"), DurationMin: num(meta, "durationMin"),
			PointsLabel: str(meta, "pointsLabel"), HidePoints: boolv(meta, "hidePoints"), StartPoints: num(meta, "startPoints")},
		Characters: chars, Acts: acts, Locations: locations, Clues: clues, Flow: flow, Votes: votes,
		Endings: endings, Truth: truth, HostFacts: facts, TruthCases: cases,
		KillerRoleIDs: killerIDs, KillerKind: str(src.Truth, "killerKind"), KillerName: str(src.Truth, "killerName"),
		CardGame: src.CardGame,
	}
	if pkg.Meta.Title == "" {
		pkg.Meta.Title = dirName
	}
	return pkg, assets, warnings, adapted, nil
}

func specialNotes(id string) []string {
	switch id {
	case "k12":
		return []string{
			"侍寝投票自动排除皇后；被罚者与侍寝结果由线上积分与宿主面板近似。",
			"多轮投票、罚赏与恩宠结算按线上 points/vote 实现，废后与冷宫由宿主裁定。",
		}
	case "k13":
		return []string{
			"20 轮骰子由系统自动掷骰并按 tieBreak 加权，提问/回答公开记录。",
			"关键词必答由系统校验；主持人追问与违规裁定由宿主旁白兜底。",
		}
	case "k18":
		return []string{"NPC 凶手「阿飞」不占用玩家座位，投票仍指认玩家角色。"}
	case "k19":
		return []string{
			"135 张牌按 27 张/人发牌，出牌与计分由 cards 插件实现。",
			"UNO 式边界与技能牌结算为线上近似，最终由宿主裁定。",
		}
	case "k28":
		return []string{
			"仅皇帝案进入玩家投票，其余案件由宿主揭晓。",
			"封妃、立后与多凶手判定由宿主面板兜底。",
		}
	case "aofu":
		return []string{"旁白角色 r_narrator 接收本阶段剩余线索；信件音频作为本地资产提供。"}
	}
	return nil
}

func mapPhaseType(srcType, name string) string {	switch srcType {
	case "reading", "search", "vote", "reveal", "opening", "dice":
		return srcType
	case "discussion":
		for _, k := range []string{"私聊", "密聊", "密谈", "耳语"} {
			if strings.Contains(name, k) {
				return "whisper"
			}
		}
		return "discussion"
	case "play", "score":
		return "cards"
	default:
		return "host"
	}
}

func splitSections(body string, readingCount int) ([]script.Section, string) {
	if strings.TrimSpace(body) == "" {
		return []script.Section{{Act: 1, Title: "角色本", Body: ""}}, ""
	}
	locs := actHeading.FindAllStringIndex(body, -1)
	var chunks []script.Section
	if len(locs) == 0 {
		return []script.Section{{Act: 1, Title: "角色本", Body: body}}, ""
	}
	preamble := strings.TrimSpace(body[:locs[0][0]])
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		titleEnd := strings.IndexByte(body[loc[0]:loc[1]], '\n')
		title := strings.TrimSpace(strings.TrimLeft(body[loc[0]:loc[1]], "# "))
		if titleEnd < 0 {
			titleEnd = loc[1] - loc[0]
		}
		start := loc[0] + titleEnd
		chunk := script.Section{Title: title, Body: strings.TrimSpace(body[start:end])}
		if i == 0 && preamble != "" {
			chunk.Body = preamble + "\n\n" + chunk.Body
		}
		chunks = append(chunks, chunk)
	}
	if readingCount <= 1 {
		merged := ""
		for i := range chunks {
			if chunks[i].Title != "" {
				merged += "## " + chunks[i].Title + "\n\n"
			}
			merged += chunks[i].Body + "\n\n"
			chunks[i].Act = 1
		}
		return []script.Section{{Act: 1, Title: "角色本", Body: strings.TrimSpace(merged)}}, ""
	}
	for i := range chunks {
		chunks[i].Act = min(i+1, readingCount)
	}
	// If a rolebook has more acts than reading phases, fold the tail into the last phase.
	if len(chunks) > readingCount {
		last := &chunks[readingCount-1]
		for _, c := range chunks[readingCount:] {
			last.Body += "\n\n## " + c.Title + "\n\n" + c.Body
		}
		chunks = chunks[:readingCount]
	}
	return chunks, ""
}

func convertSkill(m map[string]any) *script.Skill {
	if len(m) == 0 || str(m, "id") == "" {
		return nil
	}
	return &script.Skill{ID: str(m, "id"), Name: str(m, "name"), Text: str(m, "text"), Effect: str(m, "effect"),
		Cost: num(m, "cost"), ExactCost: num(m, "exactCost"), AllCost: boolv(m, "allCost"), Value: num(m, "value"),
		Target: str(m, "target"), TargetRoleID: str(m, "targetRoleId"), Public: boolv(m, "public")}
}

func renderTruth(t map[string]any) string {
	var b strings.Builder
	if s := str(t, "summary"); s != "" {
		b.WriteString("## 真相\n\n" + s + "\n\n")
	}
	if s := str(t, "method"); s != "" {
		b.WriteString("### 手法\n\n" + s + "\n\n")
	}
	if s := str(t, "motive"); s != "" {
		b.WriteString("### 动机\n\n" + s + "\n\n")
	}
	if tl := stringSlice(t, "timeline"); len(tl) > 0 {
		b.WriteString("### 时间线\n\n")
		for _, s := range tl {
			b.WriteString("- " + s + "\n")
		}
		b.WriteString("\n")
	}
	for _, ca := range caseList(t) {
		b.WriteString("### " + str(ca, "name") + "\n\n")
		if s := str(ca, "method"); s != "" {
			b.WriteString(s + "\n\n")
		}
		if s := str(ca, "motive"); s != "" {
			b.WriteString("动机：" + s + "\n\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func convertEndings(t map[string]any, voteID string) []script.Ending {
	raw, _ := t["endings"].([]any)
	var out []script.Ending
	defaultID := str(t, "defaultEndingId")
	for _, item := range raw {
		m, _ := item.(map[string]any)
		if len(m) == 0 {
			continue
		}
		e := script.Ending{ID: str(m, "id"), Title: firstNonEmpty(str(m, "name"), str(m, "title")), Text: str(m, "text")}
		if k := str(m, "killerRoleId"); k != "" && voteID != "" {
			e.When = map[string]string{voteID: "winner:" + k}
		} else if defaultID == "" || e.ID != defaultID {
			e.When = map[string]string{}
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		out = append(out, script.Ending{Title: "结局", Text: renderTruth(t), When: map[string]string{}})
	}
	return out
}

func copyParams(src map[string]any) map[string]any {
	out := map[string]any{}
	aliases := map[string]string{
		"timeoutSec": "timeout", "speechPerPlayer": "rounds", "cluePool": "clue_pool",
		"perPlayerPick": "per_player_pick", "searchMode": "search_mode", "orderRoleIds": "order_role_ids",
		"pickByRank": "pick_by_rank", "drawCost": "draw_cost", "privateClues": "private_clues",
		"hostExtraPicks": "host_extra_picks", "leftoverRoleId": "leftover_role_id",
		"leftoverReveal": "leftover_reveal", "revealOnFind": "reveal_on_find",
		"onlyRoleIds": "only_role_ids", "votePrompt": "vote_prompt", "settleFavor": "settle_favor",
		"throneRoleId": "throne_role_id", "dmScript": "dm_script",
	}
	for k, v := range src {
		if k == "id" || k == "type" || k == "name" || k == "dmScript" {
			continue
		}
		out[k] = v
		if a := aliases[k]; a != "" {
			out[a] = v
		}
	}
	if s, ok := src["dmScript"].(string); ok {
		out["dm_script"] = s
	}
	if s, ok := src["type"].(string); ok {
		out["source_type"] = s
	}
	return out
}

func caseList(t map[string]any) []map[string]any {
	raw, _ := t["cases"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, x := range raw {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func copyAssets(src, dst string) ([]string, error) {
	var found []string
	for _, sub := range []string{"images", "audio"} {
		from := filepath.Join(src, sub)
		if fi, err := os.Stat(from); err != nil || !fi.IsDir() {
			continue
		}
		err := filepath.WalkDir(from, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}
			to := filepath.Join(dst, rel)
			if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
				return err
			}
			if err := copyFile(path, to); err != nil {
				return err
			}
			found = append(found, "/script-assets/"+filepath.ToSlash(filepath.Join(filepath.Base(src), rel)))
			return nil
		})
		if err != nil {
			return found, err
		}
	}
	sort.Strings(found)
	return found, nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func writeReports(out string, m *Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "manifest.json"), b, 0o644); err != nil {
		return err
	}
	var md strings.Builder
	md.WriteString("# 导入报告\n\n")
	md.WriteString("本目录中的剧本与素材由仓库维护者提供；维护者确认已合法取得并同意在本仓库提交与发布。版权归原权利人，如有异议请联系删除。\n\n")
	md.WriteString("| ID | 标题 | 角色 | 线索 | 阶段 | 投票 | 结局 | 状态 |\n|---|---|---:|---:|---:|---:|---:|---|\n")
	for _, s := range m.Scripts {
		status := "OK"
		if s.Error != "" {
			status = "FAIL: " + s.Error
		}
		fmt.Fprintf(&md, "| %s | %s | %d | %d | %d | %d | %d | %s |\n", s.ID, s.Title, s.Characters, s.Clues, s.Phases, s.Votes, s.Endings, status)
	}
	md.WriteString("\n## 适配说明\n\n")
	for _, s := range m.Scripts {
		if len(s.Adapted) == 0 && len(s.Warnings) == 0 {
			continue
		}
		fmt.Fprintf(&md, "### %s\n\n", s.ID)
		for _, a := range s.Adapted {
			md.WriteString("- " + a + "\n")
		}
		for _, w := range s.Warnings {
			md.WriteString("- 警告：" + w + "\n")
		}
		md.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(out, "import-report.md"), []byte(md.String()), 0o644); err != nil {
		return err
	}
	readme := "# 导入剧本\n\n本目录包含 29 个剧本及素材。资料由仓库维护者提供；维护者确认已合法取得并同意在本仓库提交与发布。版权归原权利人，如有异议请联系删除。\n\n- 启动服务时使用 `--scripts scripts/imported --script-assets scripts/imported/assets`。\n- 资产 URL 为 `/script-assets/<源目录>/images|audio/...`。\n"
	return os.WriteFile(filepath.Join(out, "README.md"), []byte(readme), 0o644)
}

func str(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func num(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

func boolv(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	b, _ := m[key].(bool)
	return b
}

func stringSlice(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	if s, ok := m[key].(string); ok {
		if s == "" {
			return nil
		}
		return []string{s}
	}
	raw, _ := m[key].([]any)
	out := make([]string, 0, len(raw))
	for _, x := range raw {
		if s, ok := x.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func mapOf(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	v, _ := m[key].(map[string]any)
	return v
}

func isMurderVote(question, name string) bool {
	for _, s := range []string{"凶手", "真凶", "指认", "杀害", "杀死", "投凶"} {
		if strings.Contains(question, s) || strings.Contains(name, s) {
			return true
		}
	}
	return false
}

func first(v []string) string {
	if len(v) > 0 {
		return v[0]
	}
	return ""
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
