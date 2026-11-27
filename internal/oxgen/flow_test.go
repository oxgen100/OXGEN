package oxgen

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func TestOneStopRepairsOnlyMissingCommandEntry(t *testing.T) {
	for _, scenario := range []string{"success", "shadowed", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			input := "1\n"
			if scenario == "cancel" {
				input = "0\n"
			}
			a, out, _ := missingFlowFixture(t, input, "")
			root := filepath.Join(t.TempDir(), "已安装 中文 space")
			node := filepath.Join(root, "node.exe")
			code := filepath.Join(root, "editor")
			for _, file := range []string{
				node, filepath.Join(root, "node_modules", "npm", "bin", "npm-cli.js"),
				filepath.Join(code, "Code.exe"), filepath.Join(code, "resources", "app", "out", "cli.js"),
				filepath.Join(code, "bin", "code.cmd"),
			} {
				if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			a.State.Records[Node] = Record{Version: "26.0.0", Path: node, Root: root, Source: "OXGEN 官方发行包"}
			runner := a.Runner.(*fakeRunner)
			runner.paths["code"] = filepath.Join(code, "bin", "code.cmd")
			oldCapture := runner.capture
			actual := node
			if scenario == "shadowed" {
				actual = filepath.Join(t.TempDir(), "node.exe")
			}
			runner.capture = func(c Command) (string, error) {
				if c.Name == "mock-powershell" && len(c.Env) == 1 && c.Env[0] == "OXGEN_REPAIR_COMMAND=node" {
					return actual, nil
				}
				return oldCapture(c)
			}
			pathWrites := 0
			runner.run = func(c Command) error {
				if c.Name != "mock-powershell" || !strings.Contains(strings.Join(c.Args, " "), "add-user-path.ps1") {
					t.Fatalf("attempted an install while only repairing a command entry: %v", c)
				}
				pathWrites++
				return nil
			}
			a.Client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected network request: %s", r.URL)
				return nil, nil
			})
			a.oneStop(context.Background())
			output := out.String()
			if strings.Contains(output, "粘贴总安装目录") || !strings.Contains(output, "修复命令入口："+node) || strings.Count(output, "开始一条龙安装？") != 1 {
				t.Fatal(output)
			}
			if scenario == "cancel" {
				if pathWrites != 0 || len(a.State.History) != 0 {
					t.Fatal("entered PATH after cancel")
				}
				return
			}
			if pathWrites != 1 || len(a.State.History) != 1 {
				t.Fatal(pathWrites, a.State.History)
			}
			result := a.State.History[0]
			if scenario == "shadowed" {
				if result.Status != "待用户操作" || !strings.Contains(result.Message, actual) {
					t.Fatal(result)
				}
			} else if result.Status != "成功" {
				t.Fatal(result)
			}
		})
	}
}

func TestWindowsPATHRepairPrependsEachDirectory(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell array handling")
	}
	ps, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("Windows PowerShell unavailable")
	}
	for _, dirs := range [][]string{{`D:\OXGEN 测试\bin`}, {`D:\OXGEN 测试\bin`, `D:\OXGEN 测试\Scripts`}} {
		script := `[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false); $PathsJSON=$env:OXGEN_TEST_PATH_JSON; $oldPath=$env:OXGEN_TEST_OLD_PATH` +
			mergeUserPath + `Write-Output $newPath`
		text, err := (OSRunner{}).Capture(context.Background(), Command{
			Name: ps, Args: []string{"-NoProfile", "-NonInteractive", "-Command", script},
			Env: []string{"OXGEN_TEST_PATH_JSON=" + string(jsonBytes(dirs)), `OXGEN_TEST_OLD_PATH=C:\Existing;D:\OXGEN 测试\bin;C:\Other`},
		})
		want := strings.Join(dirs, ";") + `;C:\Existing;C:\Other`
		if err != nil || strings.TrimSpace(text) != want {
			t.Fatalf("merged PATH = %q, want %q: %v", text, want, err)
		}
	}
}

func TestOneStopPlansAPTWithoutLocalIndexes(t *testing.T) {
	for _, distro := range []string{"ubuntu", "debian"} {
		t.Run(distro, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "一条龙 中文 space")
			runner := &fakeRunner{capture: func(c Command) (string, error) {
				if c.Name == "apt-cache" && c.Args[0] == "madison" {
					return "", nil
				}
				return "", os.ErrNotExist
			}, run: func(c Command) error {
				t.Fatalf("ran %v before confirmation", c)
				return nil
			}}
			a := testApp(t, root+"\n0\n", runner)
			a.Host = Host{OS: "linux", Arch: "amd64", Distro: distro, Codename: "stable"}
			var out bytes.Buffer
			a.UI.Out = &out
			a.oneStop(context.Background())
			if strings.Count(out.String(), "安装时从发行版官方仓库选版本") != 2 || !a.UI.cancelled.Load() {
				t.Fatal(out.String())
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatal("prepared the download directory before confirmation", err)
			}
		})
	}
}

func TestOneStopContinuesAfterAPTRefreshFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Ubuntu keyring for the APT preflight")
	}
	if _, err := os.Stat("/usr/share/keyrings/ubuntu-archive-keyring.gpg"); err != nil {
		t.Skip("Ubuntu keyring unavailable")
	}
	root := filepath.Join(t.TempDir(), "APT 中文 space")
	gitUpdate, pythonInstall := false, false
	runner := &fakeRunner{paths: map[string]string{"sudo": "mock-sudo"}, capture: func(c Command) (string, error) {
		if c.Name == "/usr/bin/python3" {
			return "Python 3.12.3", nil
		}
		if c.Name == "apt-cache" && c.Args[0] == "madison" {
			return "", nil
		}
		if c.Name == "apt-cache" {
			return "  Candidate: 3.12.3-0ubuntu1\n", nil
		}
		return "", os.ErrNotExist
	}, run: func(c Command) error {
		joined := strings.Join(c.Args, " ")
		if strings.HasSuffix(joined, " update") {
			if !strings.Contains(joined, "APT::Update::Error-Mode=any") {
				t.Fatal("an incomplete mirror refresh could continue")
			}
			if strings.Contains(joined, filepath.Join(root, "downloads", "git")) {
				gitUpdate = true
				return errors.New("simulated Git index refresh failure")
			}
		}
		if strings.Contains(joined, "install --no-install-recommends") {
			if !strings.HasSuffix(joined, "python3 python3-pip python3-venv") {
				t.Fatal("APT tried to install despite a failed refresh", c)
			}
			pythonInstall = true
		}
		return nil
	}}
	a := testApp(t, root+"\n1\n", runner)
	a.Host = Host{OS: "linux", Arch: "amd64", Distro: "ubuntu", Codename: "noble"}
	var out bytes.Buffer
	a.UI.Out = &out
	a.oneStop(context.Background())
	if !gitUpdate || !pythonInstall || a.State.Records[Git].Path != "" ||
		a.State.Records[Python].Version != "3.12.3" || !strings.Contains(out.String(), "刷新发行版官方索引失败") {
		t.Fatal(out.String(), a.State.Records)
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
