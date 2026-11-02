package oxgen

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// Redirected input must not lose later menu choices to an interactive pause.
func TestPipedMenuNavigationDoesNotPauseOrClear(t *testing.T) {
	var out bytes.Buffer
	u := newUI(strings.NewReader("1\n0\n2\n0\n"), &out, false)
	a := &App{UI: u, Runner: &fakeRunner{}, Host: Host{OS: "windows", Arch: "amd64"}, Version: "test"}
	if err := a.menu(context.Background()); err != nil {
		t.Fatalf("piped navigation did not reach exit: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "Git 检测／安装／更新") || !strings.Contains(text, "状态目录：") {
		t.Fatal("navigation skipped the foundation menu or local detection")
	}
	if strings.Contains(text, "\x1b[") || strings.Contains(text, "回车也可返回") {
		t.Fatal("terminal control or pause leaked into redirected output")
	}
}

// A real terminal pauses on errors and reports, but returns between menu levels
// directly. The blank lines below represent the user's explicit acknowledgement.
func TestInteractiveMenuRetainsResultsUntilAcknowledged(t *testing.T) {
	var out bytes.Buffer
	u := newUI(strings.NewReader("wrong\n\n1\n0\n2\n\n0\n"), &out, true)
	u.Terminal = true
	a := &App{UI: u, Runner: &fakeRunner{}, Host: Host{OS: "windows", Arch: "amd64"}, Version: "test"}
	if err := a.menu(context.Background()); err != nil {
		t.Fatalf("interactive navigation consumed the wrong input: %v", err)
	}
	text := out.String()
	for _, marker := range []string{"无效选项", "Git 检测／安装／更新", "状态目录："} {
		if !strings.Contains(text, marker) {
			t.Fatalf("navigation did not visit %q", marker)
		}
	}
	report := strings.Index(text, "状态目录：")
	pause := strings.Index(text[report:], "回车也可返回")
	if pause < 0 || !strings.Contains(text[report+pause:], "\x1b[2J\x1b[H") {
		t.Fatal("detection report was not acknowledged before returning to the menu")
	}
}
