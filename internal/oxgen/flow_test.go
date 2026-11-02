package oxgen

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func missingFlowFixture(t *testing.T, input, scenario string) (*App, *bytes.Buffer, string) {
	t.Helper()
	t.Setenv("PATH", os.Getenv("PATH"))
	for _, key := range []string{"LOCALAPPDATA", "ProgramFiles", "ProgramFiles(x86)"} {
		t.Setenv(key, t.TempDir())
	}
	root := filepath.Join(t.TempDir(), "总目录 中文 space")
	python := filepath.Join(t.TempDir(), "python.exe")
	runner := &fakeRunner{paths: map[string]string{"git": "existing-git", "python": python, "powershell.exe": "mock-powershell"}, capture: func(c Command) (string, error) {
		if c.Name == "existing-git" {
			return "git version 2.54.0", nil
		}
		if c.Name == python {
			if len(c.Args) > 1 && c.Args[1] == "import sys; print(sys.executable)" {
				return python, nil
			}
			if c.Args[0] == "--version" {
				return "Python 3.14.5", nil
			}
			return "pip/venv OK", nil
		}
		if filepath.Base(c.Name) == "node.exe" {
			return "v26.0.0", nil
		}
		if filepath.Base(c.Name) == "Code.exe" {
			return testCodeVersion + "\n" + testCodeCommit + "\nx64\n", nil
		}
		return "", os.ErrNotExist
	}, run: func(c Command) error {
		if scenario == "node fails" && filepath.Base(c.Name) == "node.exe" {
			return errors.New("simulated Node failure")
		}
		if c.Name == "existing-git" || c.Name == python {
			t.Fatal("attempted to install retained environment")
		}
		return nil
	}}
	if input == "install" {
		input = "\"" + root + "\"\n1\n"
	}
	if input == "confirm cancel" {
		input = root + "\n0\n"
	}
	a := testApp(t, input, runner)
	a.Host = Host{OS: "windows", Arch: "amd64"}
	var out bytes.Buffer
	a.UI.Out = &out
	node := makeZIP(t, map[string]string{"node-v26.0.0-win-x64/node.exe": "node", "node-v26.0.0-win-x64/node_modules/npm/bin/npm-cli.js": "npm"})
	code := makeZIP(t, map[string]string{"Code.exe": "code", "resources/app/out/cli.js": "cli", "bin/code.cmd": "cmd"})
	fixtures := map[string][]byte{
		"/dist/index.json":                            []byte(`[{"version":"v26.0.0","lts":"LTS"}]`),
		"/dist/v26.0.0/SHASUMS256.txt":                []byte(digest(node) + "  node-v26.0.0-win-x64.zip"),
		"/dist/v26.0.0/node-v26.0.0-win-x64.zip":      node,
		"/api/update/win32-x64-archive/stable/latest": codeMetadata("https://vscode.download.prss.microsoft.com/code.zip", digest(code)),
		"/code.zip": code,
	}
	if scenario == "query fails" {
		delete(fixtures, "/dist/index.json")
	}
	a.Client = fixtureClient(fixtures)
	return a, &out, root
}

func TestOneStopOnlyInstallsMissingWithOnePastedDirectory(t *testing.T) {
	for _, scenario := range []string{"success", "node fails", "query fails"} {
		t.Run(scenario, func(t *testing.T) {
			a, out, root := missingFlowFixture(t, "install", scenario)
			a.oneStop(context.Background())
			if _, ok := a.State.Records[Git]; ok {
				t.Fatal("reinstalled retained Git")
			}
			if _, ok := a.State.Records[Python]; ok {
				t.Fatal("reinstalled retained Python")
			}
			if a.State.Records[VSCode].Version != testCodeVersion {
				t.Fatal("did not install missing VS Code", out.String())
			}
			_, nodeInstalled := a.State.Records[Node]
			if nodeInstalled != (scenario == "success") {
				t.Fatal("incorrect Node success record", out.String())
			}
			if !strings.HasPrefix(a.State.Records[VSCode].Root, root+string(filepath.Separator)) {
				t.Fatal("ignored pasted directory")
			}
			if strings.Count(out.String(), "粘贴总安装目录") != 1 || strings.Count(out.String(), "开始一条龙安装？") != 1 {
				t.Fatal("batch asked repeated install questions", out.String())
			}
			if strings.Contains(out.String(), "选择磁盘") || strings.Contains(out.String(), "配置新安装程序的命令入口") {
				t.Fatal("batch returned to old nested options")
			}
			if scenario != "success" && !strings.Contains(out.String(), "[失败]") {
				t.Fatal("failed component presented as success")
			}
		})
	}
}

func TestOneStopZeroAtDirectoryOrConfirmationMakesNoChanges(t *testing.T) {
	for _, input := range []string{"0\n", "confirm cancel"} {
		a, _, root := missingFlowFixture(t, input, "")
		a.oneStop(context.Background())
		if !a.UI.cancelled.Load() {
			t.Fatal("zero did not cancel")
		}
		if len(a.State.Records) != 0 {
			t.Fatal("cancelled flow installed something")
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("cancelled flow created target")
		}
		if _, err := os.Stat(a.Home); !os.IsNotExist(err) {
			t.Fatal("cancelled flow wrote state or logs")
		}
	}
}

func TestZeroReturnsFromSingleToolStraightToParentMenu(t *testing.T) {
	a, out, _ := missingFlowFixture(t, "3\n0\n0\n", "")
	if err := a.foundationMenu(context.Background()); err != nil {
		t.Fatal("zero did not navigate back cleanly", err)
	}
	if len(a.State.Records) != 0 || strings.Contains(out.String(), "回车也可返回") {
		t.Fatal("cancel required a second acknowledgement")
	}
}

func TestCancellationWatcherDoesNotStealTheNextMenuInput(t *testing.T) {
	for _, cancelOperation := range []bool{false, true} {
		r, w := io.Pipe()
		u := newUI(r, io.Discard, false)
		u.Terminal = true
		parent, stop := context.WithTimeout(context.Background(), 3*time.Second)
		u.Context = parent
		completed := make(chan struct{})
		go func() {
			u.Run(parent, true, func(ctx context.Context) {
				if cancelOperation {
					<-ctx.Done()
				}
			})
			close(completed)
		}()
		if cancelOperation {
			if _, err := io.WriteString(w, "0\n"); err != nil {
				t.Fatal(err)
			}
		}
		select {
		case <-completed:
		case <-parent.Done():
			t.Fatal("operation did not stop")
		}
		if parent.Err() != nil {
			t.Fatal("operation cancelled the entire toolbox")
		}
		go func() { io.WriteString(w, "7\n") }()
		value, err := u.Ask("next menu", "")
		if err != nil || value != "7" {
			t.Fatal("watcher stole next menu choice", value, err)
		}
		if u.cancelled.Load() != cancelOperation {
			t.Fatal("incorrect cancellation flag")
		}
		r.Close()
		w.Close()
		stop()
	}
}

func TestEveryChoiceAndConfirmationUsesZero(t *testing.T) {
	u := newUI(strings.NewReader("0\n"), io.Discard, false)
	if _, err := u.Choice("menu", []string{"continue"}, 0); !errors.Is(err, errCancelled) {
		t.Fatal(err)
	}
	u = newUI(strings.NewReader("0\n"), io.Discard, false)
	if u.Confirm("install") {
		t.Fatal("zero confirmed installation")
	}
	u = newUI(strings.NewReader("\n"), io.Discard, false)
	if u.Confirm("install") {
		t.Fatal("blank input should not start an installation")
	}
}
