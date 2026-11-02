package oxgen

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const testCodeVersion = "1.144.0"

var testCodeCommit = strings.Repeat("a", 40)

func codeMetadata(rawURL, hash string) []byte {
	return jsonBytes(map[string]string{"url": rawURL, "name": testCodeVersion, "version": testCodeCommit, "sha256hash": hash})
}

func TestVSCodeSourcesUseDesktopStableForSixTargets(t *testing.T) {
	for _, host := range []Host{{OS: "windows", Arch: "amd64"}, {OS: "windows", Arch: "arm64"}, {OS: "darwin", Arch: "amd64"}, {OS: "darwin", Arch: "arm64"}, {OS: "linux", Arch: "amd64"}, {OS: "linux", Arch: "arm64"}} {
		t.Run(host.OS+"-"+host.Arch, func(t *testing.T) {
			a := testApp(t, "", &fakeRunner{capture: func(Command) (string, error) { t.Fatal("source query invoked system package manager"); return "", nil }})
			a.Host = host
			platform, err := vscodePlatform(host)
			if err != nil {
				t.Fatal(err)
			}
			ext := ".zip"
			if host.OS == "linux" {
				ext = ".tar.gz"
			}
			endpoint := "/api/update/" + platform + "/stable/latest"
			goodURL := "https://vscode.download.prss.microsoft.com/stable/VSCode" + ext
			for _, sample := range []struct {
				url, hash string
				good      bool
			}{{goodURL, strings.Repeat("a", 64), true}, {"https://mirror.example/code" + ext, strings.Repeat("a", 64), false}, {goodURL, "", false}, {"https://github.com/other/code" + ext, strings.Repeat("a", 64), false}, {"http://vscode.download.prss.microsoft.com/code" + ext, strings.Repeat("a", 64), false}, {"https://vscode.download.prss.microsoft.com/code.exe", strings.Repeat("a", 64), false}} {
				a.Client = fixtureClient(map[string][]byte{endpoint: codeMetadata(sample.url, sample.hash)})
				r, err := a.latest(context.Background(), VSCode)
				if (err == nil) != sample.good {
					t.Fatalf("accepted=%v URL=%s err=%v", err == nil, sample.url, err)
				}
				if sample.good && (r.Version != testCodeVersion || r.Tag != testCodeCommit) {
					t.Fatal(r)
				}
			}
		})
	}
}

func codeFixtureFiles(t *testing.T, root, osName string) {
	t.Helper()
	exe, cli, bin := vscodeLayout(root, osName)
	name := "code"
	if osName == "windows" {
		name = "code.cmd"
	}
	for _, path := range []string{exe, cli, filepath.Join(bin, name)} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("original"), 0755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestVSCodeDetectionUsesNativeCLIAndKeepsExistingInstallation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "编辑器 space")
	codeFixtureFiles(t, root, "windows")
	var err error
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	var captures int
	runner := &fakeRunner{paths: map[string]string{"code": filepath.Join(root, "bin", "code.cmd")}, capture: func(c Command) (string, error) {
		captures++
		if c.Name != filepath.Join(root, "Code.exe") || len(c.Args) != 2 || c.Args[1] != "--version" || !strings.Contains(strings.Join(c.Env, ";"), "ELECTRON_RUN_AS_NODE=1") {
			t.Fatalf("unsafe GUI/shell probe: %+v", c)
		}
		return "1.140.0\n" + testCodeCommit + "\nx64\n", nil
	}, run: func(Command) error { t.Fatal("retain ran an installer"); return nil }}
	a := testApp(t, "1\n", runner)
	a.Host = Host{OS: "windows", Arch: "amd64"}
	a.Client = fixtureClient(map[string][]byte{"/api/update/win32-x64-archive/stable/latest": codeMetadata("https://vscode.download.prss.microsoft.com/VSCode.zip", strings.Repeat("a", 64))})
	d := a.detect(context.Background(), VSCode)
	if !d.Installed || !d.Active || !d.Healthy || d.Version != "1.140.0" {
		t.Fatal(d)
	}
	if result := a.manage(context.Background(), VSCode); result.Status != "保留" {
		t.Fatal(result)
	}
	if captures < 2 {
		t.Fatal("did not verify existing installation")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "Code.exe")); string(b) != "original" {
		t.Fatal("existing editor was modified")
	}
}

func TestVSCodeCustomLocationsRemainSeparateFromSystemPackages(t *testing.T) {
	for _, osName := range []string{"windows", "linux", "darwin"} {
		a := testApp(t, "\n", &fakeRunner{})
		a.Host.OS = osName
		p, err := a.planInstall(VSCode, Release{Version: testCodeVersion}, Detection{Installed: true, Path: filepath.Join(t.TempDir(), "old", "code"), Source: "现有安装"}, true)
		if err != nil || p.Fixed || p.Update {
			t.Fatal(p, err)
		}
		if err = a.pickLocations(&p); err != nil {
			t.Fatal(err)
		}
		if p.InstallDir != filepath.Join(p.Root, "versions", testCodeVersion) || p.DownloadDir == p.InstallDir {
			t.Fatal(p)
		}
		if len(p.Extra) == 0 {
			t.Fatal("missing configuration and extension location notice")
		}
	}
}

func TestVSCodeWindowsFastUpdatePayloadIsTakenFromLauncher(t *testing.T) {
	root := filepath.Join(t.TempDir(), "新结构 space")
	codeFixtureFiles(t, root, "windows")
	app := filepath.Join(root, "e646868227", "resources", "app")
	if err := os.MkdirAll(filepath.Join(app, "out"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "out", "cli.js"), []byte("cli"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "code.cmd"), []byte(`"%~dp0..\Code.exe" "%~dp0..\e646868227\resources\app\out\cli.js" %*`), 0644); err != nil {
		t.Fatal(err)
	}
	_, cli, _ := vscodeLayout(root, "windows")
	if cli != filepath.Join(app, "out", "cli.js") {
		t.Fatal("selected stale or guessed payload", cli)
	}
}

func TestVSCodeInstallVerifiesCommitArchitectureAndFailures(t *testing.T) {
	archive := makeZIP(t, map[string]string{"Code.exe": "exe", "bin/code.cmd": "cmd", "resources/app/out/cli.js": "cli"})
	for _, scenario := range []string{"success", "hash", "commit", "arch", "command", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			runner := &fakeRunner{capture: func(Command) (string, error) {
				if scenario == "command" {
					return "", errors.New("cannot execute")
				}
				commit, arch := testCodeCommit, "x64"
				if scenario == "commit" {
					commit = strings.Repeat("b", 40)
				}
				if scenario == "arch" {
					arch = "arm64"
				}
				return testCodeVersion + "\n" + commit + "\n" + arch + "\n", nil
			}}
			a := testApp(t, "", runner)
			a.Host = Host{OS: "windows", Arch: "amd64"}
			body := archive
			if scenario == "missing" {
				body = makeZIP(t, map[string]string{"Code.exe": "standalone CLI is not desktop"})
			}
			hash := digest(body)
			if scenario == "hash" {
				hash = strings.Repeat("0", 64)
			}
			a.Client = fixtureClient(map[string][]byte{"/VSCode.zip": body})
			p := InstallPlan{Tool: VSCode, Root: filepath.Join(a.Home, "VS Code 中文 space"), DownloadDir: filepath.Join(a.Home, "downloads"), Release: Release{Version: testCodeVersion, Tag: testCodeCommit, Artifact: Artifact{URL: "https://vscode.download.prss.microsoft.com/VSCode.zip", SHA256: hash}}}
			p.InstallDir = filepath.Join(p.Root, "versions", testCodeVersion)
			record, err := a.install(context.Background(), p, io.Discard)
			if scenario == "success" {
				if err != nil || record.Version != testCodeVersion || record.Source != vscodeSource {
					t.Fatal(record, err)
				}
			} else if err == nil || record.Path != "" {
				t.Fatalf("failed install returned success: %+v %v", record, err)
			}
		})
	}
}

func TestVSCodeOfflineQueryDoesNotClaimLatest(t *testing.T) {
	var output bytes.Buffer
	a := testApp(t, "2\n", &fakeRunner{})
	a.Host = Host{OS: "windows", Arch: "amd64"}
	a.UI.Out = &output
	r := a.manage(context.Background(), VSCode)
	if r.Status != "跳过" || !strings.Contains(output.String(), "暂时无法查询") || strings.Contains(output.String(), "无需重复安装") {
		t.Fatal(r, output.String())
	}
}

func TestMacApplicationZIPLinksStayWithinBundle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("valid macOS symlink extraction runs on POSIX; Windows still rejects links by default")
	}
	for _, escaped := range []bool{false, true} {
		var data bytes.Buffer
		z := zip.NewWriter(&data)
		file, _ := z.Create("App.app/Versions/A/framework")
		file.Write([]byte("signed contents"))
		header := &zip.FileHeader{Name: "App.app/Current"}
		header.SetMode(os.ModeSymlink | 0755)
		link, _ := z.CreateHeader(header)
		target := "Versions/A"
		if escaped {
			target = "../../outside"
		}
		link.Write([]byte(target))
		z.Close()
		base := t.TempDir()
		archive := filepath.Join(base, "app.zip")
		os.WriteFile(archive, data.Bytes(), 0600)
		destination := filepath.Join(base, "installed")
		err := extractArchiveOptions(context.Background(), archive, destination, false, true)
		if escaped {
			if err == nil {
				t.Fatal("escaped app link accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(filepath.Join(destination, "App.app", "Current", "framework")); err != nil || string(got) != "signed contents" {
			t.Fatal(string(got), err)
		}
	}
}

func TestLiveExistingVSCode(t *testing.T) {
	if os.Getenv("OXGEN_LIVE_VSCODE") == "" {
		t.Skip("opt-in read-only editor detection")
	}
	a := &App{Host: detectHost(), Runner: OSRunner{}, State: State{Records: map[Tool]Record{}}}
	d := a.detectVSCode(context.Background())
	if !d.Installed || !d.Healthy {
		t.Fatal(d)
	}
	t.Logf("existing VS Code: %s %s active=%v", d.Version, d.Path, d.Active)
}

func TestLiveOfficialVSCode(t *testing.T) {
	base := os.Getenv("OXGEN_LIVE_VSCODE_DIR")
	if base == "" {
		t.Skip("opt-in isolated official desktop archive installation")
	}
	if runtime.GOOS != "windows" {
		t.Skip("live Windows validation only; other platforms require their own installation acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Minute)
	defer cancel()
	a := &App{Host: detectHost(), Runner: OSRunner{}, Client: newClient()}
	r, err := a.vscodeLatest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("official VS Code stable %s, %s", r.Version, r.Artifact.URL)
	p := InstallPlan{Tool: VSCode, Root: filepath.Join(base, "程序 space"), DownloadDir: filepath.Join(base, "下载 space"), Release: r}
	p.InstallDir = filepath.Join(p.Root, "versions", r.Version)
	record, err := a.installVSCode(ctx, p, os.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("verified isolated editor: %+v", record)
}
