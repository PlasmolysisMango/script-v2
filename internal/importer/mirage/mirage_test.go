package mirage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"jbs/internal/script"
)

var expectedRoles = []struct{ Name, ID, English string }{
	{"韩乘务", "han", "Crew Member Han"},
	{"洪船长", "hong", "Captain Hong"},
	{"林歌手", "lin", "Singer Lin"},
	{"修经理", "xiu", "Manager Xiu"},
	{"张二副", "zhang", "Second Mate Zhang"},
}

var expectedLocations = []struct {
	ID, Chinese, English string
	Count                int
}{
	{"body", "尸体信息", "Corpse Information", 3},
	{"warehouse", "仓库", "Warehouse", 3},
	{"lin-room", "林歌手房间", "Singer Lin's Room", 6},
	{"xiu-room", "修经理房间", "Manager Xiu's Room", 5},
	{"han-room", "韩乘务房间", "Crew Member Han's Room", 7},
	{"zhang-room", "张二副房间", "Second Mate Zhang's Room", 7},
	{"captain-room", "船长室", "Captain's Room", 5},
	{"victim-room", "刘大副房间", "First Mate Liu's Room", 3},
	{"bar", "酒吧", "Bar", 1},
	{"wheelhouse", "操舵室", "Wheelhouse", 2},
}

func readJSON(t *testing.T, path string, value any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, value); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func assertRolePreservation(t *testing.T, p *script.Package, source map[string]map[string]string, truth string) {
	t.Helper()
	if len(source) != 5 || len(p.Characters) != 5 || p.Meta.Players != 5 {
		t.Fatal("expected all five roles")
	}
	for _, role := range expectedRoles {
		fields, ok := source[role.Name]
		if !ok {
			t.Fatalf("source role %s missing", role.Name)
		}
		c := p.Character(role.ID)
		if c == nil || c.Name != role.Name {
			t.Fatalf("imported role %s missing or renamed", role.Name)
		}
		wantSections := []script.Section{
			{Act: 1, Title: "人物背景", Body: fields["Story"]},
			{Act: 1, Title: "案发日的经历", Body: fields["Script"]},
			{Act: 1, Title: "人物关系", Body: fields["Relationship"]},
		}
		for _, field := range []string{"Story", "Script", "Relationship", "Purpose", "Performance", "Ability"} {
			if fields[field] == "" {
				t.Fatalf("source role %s has empty %s", role.Name, field)
			}
		}
		if !reflect.DeepEqual(c.Sections, wantSections) {
			t.Errorf("%s: Story/Script/Relationship sections changed", role.Name)
		}
		if !reflect.DeepEqual(c.Goals, []string{fields["Purpose"]}) {
			t.Errorf("%s: Purpose not preserved as Goals", role.Name)
		}
		if c.Performance != fields["Performance"] || c.Ability != fields["Ability"] {
			t.Errorf("%s: Performance/Ability changed", role.Name)
		}
		culprit := role.ID == "xiu"
		if c.IsCulprit != culprit || c.CanLie != culprit {
			t.Errorf("%s: culprit/lying flags inconsistent", role.Name)
		}
	}
	if truth == "" || p.Truth != truth {
		t.Error("complete truth not preserved")
	}
	if len(p.Votes) != 1 || p.Votes[0].ID != "murderer" || p.Votes[0].Options != "characters" || p.Votes[0].Correct != "xiu" {
		t.Error("culprit vote inconsistent")
	}
}

func assertCluePreservation(t *testing.T, p *script.Package, source map[string][]string) {
	t.Helper()
	if len(p.Locations) != 10 {
		t.Fatalf("location count = %d, want 10", len(p.Locations))
	}
	cards := source["操舵室"]
	if len(cards) != 2 || !strings.Contains(cards[1], "第一张") {
		t.Fatal("source tarot clue incomplete")
	}
	adaptations := map[string]string{
		"zhang-room-07": "床头柜里有一把钥匙，上面刻着操舵室枪箱的编号。",
		"wheelhouse-01": "用钥匙打开写着‘紧急使用枪支’的保险箱，里面是一把左轮手枪，枪里只有2发子弹。",
		"wheelhouse-02": "操舵室暗格中的密码箱已被打开。里面有6张传真过来的塔罗牌：\n" + cards[1][strings.Index(cards[1], "第一张"):],
	}
	total := 0
	for _, loc := range p.Locations {
		bodies, ok := source[loc.Name]
		if !ok || loc.Act != 1 {
			t.Fatalf("location %s not preserved", loc.ID)
		}
		total += len(bodies)
		for i, body := range bodies {
			id := fmt.Sprintf("%s-%02d", loc.ID, i+1)
			c := p.Clue(id)
			if c == nil || c.Location != loc.ID || c.Act != 1 {
				t.Fatalf("clue %s missing or moved", id)
			}
			if adapted, ok := adaptations[id]; ok {
				body = adapted
			}
			if c.Body != body {
				t.Errorf("clue %s body changed", id)
			}
			visibility := "private"
			if loc.ID == "body" {
				visibility = "public"
			}
			if c.Visibility != visibility {
				t.Errorf("clue %s visibility changed", id)
			}
			var unlock *script.Unlock
			switch id {
			case "wheelhouse-01":
				unlock = &script.Unlock{RequiresClues: []string{"zhang-room-07"}}
			case "wheelhouse-02":
				unlock = &script.Unlock{RequiresPhase: "search2"}
			}
			if !reflect.DeepEqual(c.Unlock, unlock) {
				t.Errorf("clue %s unlock adaptation changed", id)
			}
			if strings.Contains(c.Body, "图片给搜索的玩家") || strings.Contains(c.Body, "答案 8792") {
				t.Errorf("clue %s leaked host-only instructions", id)
			}
		}
	}
	if len(p.Clues) != total {
		t.Errorf("clue count = %d, want %d", len(p.Clues), total)
	}
}

func TestEasternStarQuality(t *testing.T) {
	dir := "/tmp/mirage/dataset/Zh/东方之星号游轮事件"
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Skip("optional MIRAGE checkout not present")
	} else if err != nil {
		t.Fatal(err)
	}
	truthPath := filepath.Join(filepath.Dir(dir), "Truth.json")
	p, err := Import(dir, truthPath, "修经理")
	if err != nil {
		t.Fatal(err)
	}
	var roles map[string]map[string]string
	var clues map[string][]string
	var truths map[string]string
	readJSON(t, filepath.Join(dir, "人物剧本.json"), &roles)
	readJSON(t, filepath.Join(dir, "线索.json"), &clues)
	readJSON(t, truthPath, &truths)
	assertRolePreservation(t, p, roles, truths["东方之星号游轮事件"])
	assertCluePreservation(t, p, clues)
	if len(p.Clues) != 42 {
		t.Fatalf("clue count = %d, want 42", len(p.Clues))
	}
	b, err := os.ReadFile("/tmp/mirage/ScriptEnvs/SOO.py")
	if err != nil || !strings.Contains(string(b), "self.culprit.append('修经理')") {
		t.Fatal("environment culprit inconsistent")
	}

	enDir := "/tmp/mirage/dataset/En/The Eastern Star Cruise Ship"
	var enRoles map[string]map[string]string
	var enClues map[string][]string
	readJSON(t, filepath.Join(enDir, "script.json"), &enRoles)
	readJSON(t, filepath.Join(enDir, "clues.json"), &enClues)
	if len(enRoles) != len(roles) || len(enRoles) != 5 {
		t.Fatal("Chinese/English role counts differ")
	}
	for _, role := range expectedRoles {
		if _, ok := enRoles[role.English]; !ok {
			t.Errorf("English role %s missing", role.English)
		}
	}
	if len(clues) != 10 || len(enClues) != 10 {
		t.Fatal("Chinese/English location counts differ")
	}
	counts := map[string]int{}
	for _, c := range p.Clues {
		counts[c.Location]++
	}
	for _, loc := range expectedLocations {
		zh, zhOK := clues[loc.Chinese]
		en, enOK := enClues[loc.English]
		imported := p.Location(loc.ID)
		if !zhOK || !enOK || imported == nil || imported.Name != loc.Chinese {
			t.Errorf("canonical location %s missing or renamed", loc.ID)
			continue
		}
		if len(zh) != loc.Count || len(en) != loc.Count || counts[loc.ID] != loc.Count {
			t.Errorf("%s: Chinese=%d English=%d imported=%d, want %d", loc.ID, len(zh), len(en), counts[loc.ID], loc.Count)
		}
	}
}

func TestImportSyntheticFixture(t *testing.T) {
	// Original test prose; no MIRAGE dataset text is stored in this fixture.
	dir := t.TempDir()
	roles := map[string]map[string]string{}
	for i, role := range expectedRoles {
		fields := map[string]string{}
		for _, key := range []string{"Story", "Script", "Relationship", "Purpose", "Performance", "Ability"} {
			fields[key] = fmt.Sprintf("  演练角色%d的%s：你记录了一枚蓝色纽扣。\n第二行保留标点与空格！  ", i+1, key)
		}
		roles[role.Name] = fields
	}
	clues := map[string][]string{}
	for i, loc := range expectedLocations {
		clues[loc.Chinese] = []string{fmt.Sprintf("原创演练线索%d：纸上画着一个橙色圆圈。\n  不要裁剪空格。  ", i+1)}
	}
	clues["刘奇的房间"] = clues["刘大副房间"]
	delete(clues, "刘大副房间")
	clues["张二副房间"] = nil
	for i := 1; i <= 6; i++ {
		clues["张二副房间"] = append(clues["张二副房间"], fmt.Sprintf("演练抽屉%d里有一张彩色便签。", i))
	}
	clues["张二副房间"] = append(clues["张二副房间"], "演练钥匙放在紫色信封里，主持提示应当移除。")
	clues["操舵室"] = []string{
		"演练锁箱装着一把道具枪，应先找到演练钥匙。",
		"演练主持提示：图片给搜索的玩家，答案 8792。\n第一张是蓝色钟楼\n第二张是绿色雨伞\n最后一行必须完整保留。  ",
	}
	truth := "  原创演练复盘：修经理把蓝色纽扣藏进了纸盒。\n没有其他人参与，这一整段都必须保留。  "
	truthPath := filepath.Join(dir, "Truth.json")
	for _, item := range []struct {
		name  string
		value any
	}{
		{"人物剧本.json", roles},
		{"线索.json", clues},
		{"Truth.json", map[string]string{"东方之星号游轮事件": truth}},
	} {
		b, err := json.Marshal(item.value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, item.name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Import(dir, truthPath, "修经理")
	if err != nil {
		t.Fatal(err)
	}
	assertRolePreservation(t, p, roles, truth)
	assertCluePreservation(t, p, clues)
	if len(p.Clues) != 17 || p.Location("captain-room").Name != "船长室" || p.Location("victim-room").Name != "刘奇的房间" {
		t.Fatal("synthetic clue counts or canonical aliases changed")
	}
}
