package oxgen

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSameVersionNodeRepairKeepsOldProgramUntilCommit(t *testing.T) {
	for _, scenario := range []string{"verification", "cancel", "state write", "success"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("PATH", os.Getenv("PATH"))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			root := filepath.Join(t.TempDir(), "工作目录 中文 space")
			archive := makeZIP(t, map[string]string{"node/node.exe": "new", "node/node_modules/npm/bin/npm-cli.js": "npm"})
			p := InstallPlan{Tool: Node, Release: Release{Version: "26.0.0", Artifact: Artifact{URL: "https://nodejs.org/node.zip", SHA256: digest(archive)}}}
			locatePlan(&p, root, filepath.Join(root, "downloads"))
			oldExe := filepath.Join(p.InstallDir, "node.exe")
			if err := os.MkdirAll(p.InstallDir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(oldExe, []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			old := Record{Version: p.Release.Version, Path: oldExe, Root: p.Root, Source: "OXGEN 官方发行包"}
			marker := filepath.Join(filepath.Dir(p.InstallDir), ".oxgen-target-26.0.0.json")
			if err := saveJSON(marker, targetOwner{p.InstallDir, p.Tool}); err != nil {
				t.Fatal(err)
			}
			runner := &fakeRunner{paths: map[string]string{"powershell.exe": "mock-powershell"}, capture: func(Command) (string, error) {
				return "v26.0.0", nil
			}, run: func(c Command) error {
				if filepath.Base(c.Name) != "node.exe" {
					return nil
				}
				if data, err := os.ReadFile(oldExe); err != nil || string(data) != "old" || samePath(c.Name, oldExe) {
					t.Fatalf("the old program was moved before validation: %q %v", data, err)
				}
				switch scenario {
				case "verification":
					return errors.New("test runtime failed")
				case "cancel":
					cancel()
					return ctx.Err()
				}
				return nil
			}}
			a := testApp(t, "", runner)
			a.Host.OS = "windows"
			a.Client = fixtureClient(map[string][]byte{"/node.zip": archive})
			a.State.Records[Node] = old
			if err := saveState(a.Home, a.State); err != nil {
				t.Fatal(err)
			}
			if scenario == "state write" {
				block := filepath.Join(a.Home, "state.json.previous")
				if err := os.Mkdir(block, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(block, "keep"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			result := a.executePlan(ctx, p)
			data, err := os.ReadFile(oldExe)
			if err != nil {
				t.Fatal("program missing from the original path", err, result)
			}
			state, err := loadState(a.Home)
			if err != nil {
				t.Fatal(err)
			}
			backups, _ := filepath.Glob(p.InstallDir + ".previous-*")
			if scenario == "success" {
				if result.Status != "成功" || string(data) != "new" || len(backups) != 1 || state.Records[Node].UpdatedAt.IsZero() {
					t.Fatal(result, string(data), backups, state.Records[Node])
				}
				if b, e := os.ReadFile(filepath.Join(backups[0], "node.exe")); e != nil || string(b) != "old" {
					t.Fatal("the previous version is not recoverable", e)
				}
			} else {
				want := "失败"
				if scenario == "cancel" {
					want = "跳过"
				}
				if result.Status != want || string(data) != "old" || a.State.Records[Node] != old || state.Records[Node] != old || len(backups) != 0 {
					t.Fatal(result, string(data), state.Records[Node], backups)
				}
			}
			if stages, _ := filepath.Glob(filepath.Join(filepath.Dir(p.InstallDir), ".oxgen-stage-*")); len(stages) != 0 {
				t.Fatal("staging directories left behind", stages)
			}
		})
	}
}

func TestWindowsPythonRepairRestoresPreviousAfterFinalPathFailure(t *testing.T) {
	root := t.TempDir()
	p := InstallPlan{Tool: Python, Release: Release{Version: "3.14.5", Tag: "3.14.5-64"}}
	locatePlan(&p, root, filepath.Join(root, "downloads"))
	exe := filepath.Join(p.InstallDir, "python.exe")
	if err := os.MkdirAll(p.InstallDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(filepath.Dir(p.InstallDir), ".oxgen-target-3.14.5.json")
	if err := saveJSON(marker, targetOwner{p.InstallDir, p.Tool}); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{paths: map[string]string{"pymanager.exe": "mock-manager"}, capture: func(Command) (string, error) {
		return "pip available", nil
	}, run: func(c Command) error {
		if c.Name == "mock-manager" && strings.HasPrefix(c.Args[2], "--download=") {
			if data, err := os.ReadFile(exe); err != nil || string(data) != "old" {
				t.Fatal("Python was moved before download completed", err)
			}
			return nil
		}
		if c.Name == "mock-manager" && c.Args[2] == "--target="+p.InstallDir {
			if err := os.MkdirAll(p.InstallDir, 0755); err != nil {
				return err
			}
			return os.WriteFile(exe, []byte("new"), 0600)
		}
		return errors.New("runtime verification failed")
	}}
	a := testApp(t, "", runner)
	a.Host.OS = "windows"
	if _, err := a.install(context.Background(), p, io.Discard, func(Record) error {
		t.Fatal("saved an unverified Python installation")
		return nil
	}); err == nil {
		t.Fatal("accepted a broken runtime")
	}
	if data, err := os.ReadFile(exe); err != nil || string(data) != "old" {
		t.Fatalf("old Python not restored: %q %v", data, err)
	}
}

func TestUnixAgentLauncherRunsAfterDirectorySwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a Unix shell")
	}
	body := []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo 2.0.0; else printf '%s' \"$1\"; fi\n")
	a := testApp(t, "", OSRunner{})
	a.Host.OS = "linux"
	a.Client = fixtureClient(map[string][]byte{"/claude": body})
	p := InstallPlan{Tool: Claude, Release: Release{Version: "2.0.0", Artifact: Artifact{URL: "https://downloads.claude.ai/claude", SHA256: digest(body)}}}
	root := filepath.Join(t.TempDir(), "Agent 的目录 'a b'")
	locatePlan(&p, root, filepath.Join(root, "downloads"))
	record, err := a.install(context.Background(), p, io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	text, err := a.Runner.Capture(context.Background(), Command{Name: record.Path, Args: []string{"two words"}})
	if err != nil || text != "two words" {
		t.Fatalf("launcher could not find target or forward its argument: %q %v", text, err)
	}
}
