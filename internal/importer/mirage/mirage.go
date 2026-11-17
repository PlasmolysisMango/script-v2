package mirage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"jbs/internal/script"
)

var characterIDs = map[string]string{"韩乘务": "han", "洪船长": "hong", "林歌手": "lin", "修经理": "xiu", "张二副": "zhang"}
var order = []string{"韩乘务", "洪船长", "林歌手", "修经理", "张二副"}
var locations = []struct{ Name, ID string }{{"尸体信息", "body"}, {"仓库", "warehouse"}, {"林歌手房间", "lin-room"}, {"修经理房间", "xiu-room"}, {"韩乘务房间", "han-room"}, {"张二副房间", "zhang-room"}, {"洪船长房间", "captain-room"}, {"刘大副房间", "victim-room"}, {"酒吧", "bar"}, {"操舵室", "wheelhouse"}}

func Import(dir, truthPath, culprit string) (*script.Package, error) {
	var chars map[string]map[string]string
	var clues map[string][]string
	var truths map[string]string
	for _, item := range []struct {
		Path  string
		Value any
	}{{filepath.Join(dir, "人物剧本.json"), &chars}, {filepath.Join(dir, "线索.json"), &clues}, {truthPath, &truths}} {
		b, err := os.ReadFile(item.Path)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, item.Value); err != nil {
			return nil, fmt.Errorf("%s: %w", item.Path, err)
		}
	}
	if len(chars) != 5 {
		return nil, fmt.Errorf("Eastern Star needs 5 characters, got %d", len(chars))
	}
	p := &script.Package{Meta: script.Meta{ID: "eastern-star", Title: "东方之星号游轮事件", Players: 5, Lang: "zh-CN", Intro: "东方之星号豪华游轮即将抵达港口。烟花表演后，大副刘奇被发现死在仓库。案发时，五位船员各有不能告人的秘密。找出致命的一击，还原今晚的真相。", Rules: "只有凶手可以说谎。其他玩家可以隐瞒不利事实，但不能编造。搜到的线索先归自己，可在讨论时主动公开。", Credits: "原作：比格沃斯大呆侠；结构化数据：MIRAGE (arXiv 2501.01652)", Source: "https://github.com/lime728/MIRAGE", LicenseNote: "由仓库维护者提供；维护者确认已合法取得并同意在本仓库提交与发布。MIRAGE 未声明数据许可证，版权归原权利人，如有异议请联系删除。"}, Acts: []script.Act{{ID: 1, Title: "游轮之夜"}}, Truth: truths["东方之星号游轮事件"]}
	for _, name := range order {
		fields, ok := chars[name]
		if !ok {
			return nil, fmt.Errorf("missing %s", name)
		}
		gender := "male"
		if name == "韩乘务" || name == "林歌手" {
			gender = "female"
		}
		story := fields["Story"]
		intro := strings.SplitN(story, "。", 2)[0] + "。"
		intro = strings.ReplaceAll(intro, "你", "这位船员")
		c := script.Character{ID: characterIDs[name], Name: name, PublicIntro: intro, Gender: gender, TTS: script.Voice{Hint: gender, Rate: 1, Pitch: 1}, Goals: []string{fields["Purpose"]}, Performance: fields["Performance"], Ability: fields["Ability"], CanLie: name == culprit, IsCulprit: name == culprit}
		for _, f := range []struct{ Key, Title string }{{"Story", "人物背景"}, {"Script", "案发日的经历"}, {"Relationship", "人物关系"}} {
			c.Sections = append(c.Sections, script.Section{Act: 1, Title: f.Title, Body: fields[f.Key]})
		}
		p.Characters = append(p.Characters, c)
	}
	if characterIDs[culprit] == "" {
		return nil, fmt.Errorf("unknown culprit %s", culprit)
	}
	// MIRAGE mixes several Chinese aliases for the same room.
	aliases := map[string][]string{"captain-room": {"洪船长房间", "船长房间", "船长室"}, "victim-room": {"刘大副房间", "刘奇房间", "刘奇的房间"}}
	for _, loc := range locations {
		items, ok := clues[loc.Name]
		name := loc.Name
		if !ok {
			for _, alias := range aliases[loc.ID] {
				if v, yes := clues[alias]; yes {
					items, ok, name = v, true, alias
					break
				}
			}
		}
		if !ok {
			return nil, fmt.Errorf("missing location %s; actual keys %v", loc.Name, keys(clues))
		}
		p.Locations = append(p.Locations, script.Location{ID: loc.ID, Name: name, Act: 1})
		for i, body := range items {
			c := script.Clue{ID: fmt.Sprintf("%s-%02d", loc.ID, i+1), Location: loc.ID, Title: fmt.Sprintf("%s · 线索%d", name, i+1), Body: body, Act: 1, Visibility: "private"}
			if loc.ID == "body" {
				c.Visibility = "public"
			}
			if loc.ID == "zhang-room" && strings.Contains(body, "钥匙") {
				c.Body = "床头柜里有一把钥匙，上面刻着操舵室枪箱的编号。"
			}
			if loc.ID == "wheelhouse" && i == 0 {
				c.Body = "用钥匙打开写着‘紧急使用枪支’的保险箱，里面是一把左轮手枪，枪里只有2发子弹。"
				c.Unlock = &script.Unlock{RequiresClues: []string{"zhang-room-07"}}
			}
			if loc.ID == "wheelhouse" && i == 1 {
				idx := strings.Index(body, "第一张")
				if idx < 0 {
					return nil, fmt.Errorf("password clue text missing cards")
				}
				c.Body = "操舵室暗格中的密码箱已被打开。里面有6张传真过来的塔罗牌：\n" + body[idx:]
				c.Unlock = &script.Unlock{RequiresPhase: "search2"}
			}
			p.Clues = append(p.Clues, c)
		}
	}
	p.Flow = DefaultFlow()
	p.Votes = []script.Vote{{ID: "murderer", Question: "谁对刘奇实施了致命的一击？", Options: "characters", Correct: characterIDs[culprit]}}
	p.Endings = []script.Ending{{When: map[string]string{"murderer": "correct"}, Title: "真凶浮出水面", Text: "你们辨明了致命枪伤和那些企图被隐藏的证据，成功指认了修经理。"}, {When: map[string]string{"murderer": "wrong"}, Title: "烟花掩盖的秘密", Text: "多数人的指认未能锁定真正的凶手。请阅读复盘，对照每个人的时间线和关键线索。"}}
	if p.Truth == "" {
		return nil, fmt.Errorf("missing truth")
	}
	return p, p.Validate()
}
func keys(m map[string][]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
func DefaultFlow() []script.Step {
	return []script.Step{
		{ID: "lobby", Type: "ready_check", Title: "入座与准备"},
		{ID: "background", Type: "reading", Title: "阅读角色剧本", Params: map[string]any{"act": 1}},
		{ID: "intro", Type: "intro", Title: "人物自我介绍", Params: map[string]any{"rounds": 1}},
		{ID: "search1", Type: "search", Title: "第一轮搜证", Params: map[string]any{"action_points": 3, "act": 1}},
		{ID: "discuss1", Type: "discussion", Title: "第一轮讨论", Params: map[string]any{"rounds": 2}},
		{ID: "search2", Type: "search", Title: "第二轮搜证", Params: map[string]any{"action_points": 2, "act": 1}},
		{ID: "discuss2", Type: "discussion", Title: "第二轮讨论", Params: map[string]any{"rounds": 2}},
		{ID: "vote", Type: "vote", Title: "指认真凶"},
		{ID: "reveal", Type: "reveal", Title: "真相复盘"},
		{ID: "end", Type: "end", Title: "游戏结束"},
	}
}
