package llm

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRealQoderCLI(t *testing.T) {
	if os.Getenv("JBS_REAL_LLM") != "1" {
		t.Skip("set JBS_REAL_LLM=1 for an authenticated qodercli smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := (&Qoder{}).Complete(ctx, "只返回一个JSON对象，不使用工具。", `返回 {"action":"say","payload":{"text":"你好"},"thought":"等待其他玩家"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"action"`) {
		t.Fatal("missing action JSON")
	}
}
