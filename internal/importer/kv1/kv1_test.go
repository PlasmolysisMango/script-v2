package kv1

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jbs/internal/script"
)

func TestScriptID(t *testing.T) {
	cases := map[string]string{
		"K1-某某":        "k1",
		"K28-某某":       "k28",
		"K13":          "k13",
		"AI-01-某某":     "ai01",
		"AI-02-某某":     "ai02",
		"AOFU-持斧奥夫":    "aofu",
		"eastern-star": "eastern-star",
	}
	for in, want := range cases {
		if got := scriptID(in); got != want {
			t.Errorf("scriptID(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestImport(t *testing.T) {
	out := t.TempDir()
	assets := filepath.Join(out, "assets")
	m, err := Import("testdata", out, assets, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Scripts) != 1 {
		t.Fatalf("imported %d scripts, want 1", len(m.Scripts))
	}
	rep := m.Scripts[0]
	if rep.Error != "" {
		t.Fatalf("import error: %s", rep.Error)
	}
	if rep.ID != "k1" || rep.Characters != 2 || rep.Clues != 4 || rep.Phases != 5 || rep.Votes != 1 || rep.Endings != 2 {
		t.Fatalf("bad report: %+v", rep)
	}
	wantTypes := map[string]int{"reading": 1, "whisper": 1, "vote": 1, "host": 1, "reveal": 1}
	for typ, n := range wantTypes {
		if rep.PhaseTypes[typ] != n {
			t.Errorf("phase type %s=%d, want %d", typ, rep.PhaseTypes[typ], n)
		}
	}
	if !strings.Contains(strings.Join(rep.Adapted, "\n"), "p4: mystery -> host") {
		t.Errorf("missing host fallback note: %v", rep.Adapted)
	}
	for _, name := range []string{"manifest.json", "import-report.md", "README.md"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	report, err := os.ReadFile(filepath.Join(out, "import-report.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "合法取得") || strings.Contains(string(report), "未授权") {
		t.Error("report missing authorized-source notice")
	}
	readme, err := os.ReadFile(filepath.Join(out, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "合法取得") || strings.Contains(string(readme), "禁止提交") {
		t.Error("readme missing authorized-source notice")
	}
	for _, asset := range []string{"images/a.png", "audio/a.mp3"} {
		if _, err := os.Stat(filepath.Join(assets, "K1-测试", asset)); err != nil {
			t.Errorf("missing copied asset %s: %v", asset, err)
		}
	}
	if !containsURL(rep.Assets, "/script-assets/K1-测试/images/a.png") {
		t.Errorf("assets missing image URL: %v", rep.Assets)
	}

	pkg, err := script.Load(filepath.Join(out, "k1", "script.json"))
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Meta.ID != "k1" || pkg.Meta.Title != "导入测试" || !strings.Contains(pkg.Meta.LicenseNote, "合法取得") {
		t.Fatalf("bad meta: %+v", pkg.Meta)
	}
	if pkg.Meta.Series != "测试系列" || pkg.Meta.PointsLabel != "积分" || !pkg.Meta.HidePoints || pkg.Meta.StartPoints != 5 {
		t.Fatalf("meta extras not mapped: %+v", pkg.Meta)
	}
	if !strings.Contains(pkg.Truth, "真相摘要") || !strings.Contains(pkg.Truth, "刀伤") {
		t.Fatalf("bad truth: %q", pkg.Truth)
	}
	if len(pkg.HostFacts) != 1 || pkg.HostFacts[0].Text != "主持人事实" {
		t.Fatalf("bad host facts: %+v", pkg.HostFacts)
	}

	a := pkg.Character("r_a")
	if a == nil || a.Points != 3 || a.Skill == nil || a.Skill.ID != "s1" || a.Skill.Cost != 1 {
		t.Fatalf("bad character: %+v", a)
	}
	if !strings.Contains(a.PublicIntro, "公开介绍甲") || !strings.Contains(a.PublicIntro, "甲穿着长衣") {
		t.Fatalf("bad public intro: %q", a.PublicIntro)
	}
	if len(a.Sections) != 1 || a.Sections[0].Act != 1 || !strings.Contains(a.Sections[0].Body, "序章") ||
		!strings.Contains(a.Sections[0].Body, "尾声") {
		t.Fatalf("bad merged sections: %+v", a.Sections)
	}

	c1, c2, c3, c4 := pkg.Clue("c1"), pkg.Clue("c2"), pkg.Clue("c3"), pkg.Clue("c4")
	if c1 == nil || c1.Visibility != "public" || c1.Location != "大厅" || c1.ImageURL == "" {
		t.Fatalf("bad public clue: %+v", c1)
	}
	if c2 == nil || c2.OwnerRoleID != "r_a" || len(c2.UnlockedBy) != 1 || c2.GrantsItem != "钥匙" {
		t.Fatalf("bad owner clue: %+v", c2)
	}
	if c3 == nil || c3.Location != "未归类" || len(c3.RequiresItems) != 1 || len(c3.GrantsClue) != 1 {
		t.Fatalf("bad item clue: %+v", c3)
	}
	if c4 == nil || !c4.MustReveal || c4.Location != "未归类" {
		t.Fatalf("bad reward clue: %+v", c4)
	}

	index := map[string]script.Step{}
	for _, s := range pkg.Flow {
		index[s.ID] = s
	}
	if index["p1"].Type != "reading" || index["p1"].Str("dm_script", "") != "主持人私密手稿" || index["p1"].Int("act", 0) != 1 {
		t.Fatalf("bad reading step: %+v", index["p1"])
	}
	if index["p2"].Type != "whisper" || index["p2"].Int("rounds", 0) != 2 {
		t.Fatalf("bad whisper step: %+v", index["p2"])
	}
	ids, _ := index["p3"].Params["vote_ids"].([]any)
	if index["p3"].Type != "vote" || len(ids) != 1 || ids[0] != "case1" {
		t.Fatalf("bad vote step: %+v", index["p3"].Params)
	}
	if index["p4"].Type != "host" || index["p4"].Str("source_type", "") != "mystery" {
		t.Fatalf("bad host fallback step: %+v", index["p4"])
	}
	if index["p5"].Type != "reveal" {
		t.Fatalf("bad reveal step: %+v", index["p5"])
	}

	if len(pkg.Votes) != 1 {
		t.Fatalf("votes=%d", len(pkg.Votes))
	}
	v := pkg.Votes[0]
	if v.ID != "case1" || v.Correct != "r_a" || v.PhaseID != "p3" || len(v.Corrects) != 1 {
		t.Fatalf("bad vote: %+v", v)
	}
	if len(pkg.Endings) != 2 {
		t.Fatalf("endings=%d", len(pkg.Endings))
	}
	for _, e := range pkg.Endings {
		switch e.ID {
		case "e1":
			if e.When["case1"] != "winner:r_a" {
				t.Errorf("bad conditional ending: %+v", e)
			}
		case "e0":
			if len(e.When) != 0 {
				t.Errorf("default ending should be unconditional: %+v", e.When)
			}
		}
	}
	if len(pkg.TruthCases) != 1 || pkg.TruthCases[0].ID != "case1" || pkg.KillerRoleIDs[0] != "r_a" {
		t.Fatalf("bad truth cases: %+v", pkg.TruthCases)
	}
}

func TestImportOnly(t *testing.T) {
	out := t.TempDir()
	m, err := Import("testdata", out, "", Options{Only: map[string]bool{"k2": true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Scripts) != 0 {
		t.Fatalf("imported %d scripts, want 0", len(m.Scripts))
	}
	if _, err := os.Stat(filepath.Join(out, "manifest.json")); err != nil {
		t.Fatal(err)
	}
}

func TestValidateReferences(t *testing.T) {
	out := t.TempDir()
	if _, err := Import("testdata", out, "", Options{}); err != nil {
		t.Fatal(err)
	}
	load := func() *script.Package {
		pkg, err := script.Load(filepath.Join(out, "k1", "script.json"))
		if err != nil {
			t.Fatal(err)
		}
		return pkg
	}
	cases := []struct {
		name   string
		mutate func(*script.Clue)
		want   string
	}{
		{"requires item", func(c *script.Clue) { c.RequiresItems = []string{"幽灵钥匙"} }, "requires unknown item"},
		{"unlocked by", func(c *script.Clue) { c.UnlockedBy = []string{"ghost"} }, "unlocked by unknown clue"},
		{"grants clue", func(c *script.Clue) { c.GrantsClue = []string{"ghost"} }, "grants unknown clue"},
		{"owner role", func(c *script.Clue) { c.OwnerRoleID = "ghost" }, "unknown owner role"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pkg := load()
			tc.mutate(&pkg.Clues[0])
			err := pkg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate()=%v, want %q", err, tc.want)
			}
		})
	}
}

func containsURL(urls []string, want string) bool {
	for _, u := range urls {
		if u == want {
			return true
		}
	}
	return false
}
