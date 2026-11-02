package oxgen

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func integrity512(data []byte) string {
	s := sha512.Sum512(data)
	return "sha512-" + base64.StdEncoding.EncodeToString(s[:])
}
func npmFixture(name, version, raw string, data []byte, optional map[string]string) []byte {
	return jsonBytes(map[string]any{"name": name, "version": version, "dist": map[string]string{"tarball": raw, "integrity": integrity512(data)}, "optionalDependencies": optional})
}

func TestAgentSourcesMatchAllPlatforms(t *testing.T) {
	for _, osName := range []string{"windows", "darwin", "linux"} {
		for _, arch := range []string{"amd64", "arm64"} {
			a := testApp(t, "", &fakeRunner{})
			a.Host = Host{OS: osName, Arch: arch}
			platform := agentPlatform(a.Host)
			ocPlatform := strings.ReplaceAll(platform, "win32", "windows")
			if arch == "amd64" {
				ocPlatform += "-baseline"
			}
			codexNativeVersion := "1.2.3-" + platform
			fixtures := map[string][]byte{
				"/@openai/codex/latest":                       npmFixture("@openai/codex", "1.2.3", npmRegistry+"/codex.tgz", []byte("top"), map[string]string{"@openai/codex-" + platform: "npm:@openai/codex@" + codexNativeVersion}),
				"/@openai/codex/" + codexNativeVersion:        npmFixture("@openai/codex", codexNativeVersion, npmRegistry+"/codex-native.tgz", []byte("native"), nil),
				"/@opencode/cli/latest":                       npmFixture("@opencode/cli", "2.2.5", npmRegistry+"/opencode.tgz", []byte("top"), map[string]string{"@opencode/cli-" + ocPlatform: "2.2.5"}),
				"/@opencode/cli-" + ocPlatform + "/2.2.5":     npmFixture("@opencode/cli-"+ocPlatform, "2.2.5", npmRegistry+"/native.tgz", []byte("native"), nil),
				"/@earendil-works/pi-coding-agent/latest":     npmFixture("@earendil-works/pi-coding-agent", "1.4.0", npmRegistry+"/pi.tgz", []byte("pi"), nil),
				"/@deepseek-ai/dsh/latest":                    npmFixture("@deepseek-ai/dsh", "0.2.1-rc.5", npmRegistry+"/dsh.tgz", []byte("dsh"), nil),
				"/claude-code-releases/latest":                []byte("2.1.316\n"),
				"/claude-code-releases/2.1.316/manifest.json": jsonBytes(map[string]any{"version": "2.1.316", "platforms": map[string]any{platform: map[string]any{"checksum": strings.Repeat("a", 64), "size": 123}}}),
				"/docs/workbuddy/Download-History":            []byte(`<a href="https://acc-1258344699.cos.ap-guangzhou.myqcloud.com/workbuddy/saas/win32-x64-user/WorkBuddy-win32-x64-user-5.9.3.42398134-d4692421.exe">win</a><a href="https://download.codebuddy.cn/workbuddy/WorkBuddy-darwin-x64-5.9.3.42398134-d4692421.dmg">mac</a><a href="https://download.codebuddy.cn/workbuddy/WorkBuddy-darwin-arm64-5.9.3.42398134-d4692421.dmg">arm</a>`),
			}
			a.Client = fixtureClient(fixtures)
			for _, tool := range agentTools {
				r, err := a.latest(context.Background(), tool)
				if tool == WorkBuddy && osName == "linux" {
					if err == nil {
						t.Fatal("invented Linux WorkBuddy package")
					}
					continue
				}
				if err != nil || r.Version == "" {
					t.Fatalf("%s/%s %s: %+v %v", osName, arch, tool, r, err)
				}
				if tool == Pi && r.Package != "@earendil-works/pi-coding-agent" {
					t.Fatal("obsolete pi package")
				}
				if tool == OpenCode && !strings.HasPrefix(r.Artifact.URL, npmRegistry) {
					t.Fatal("not official OpenCode V2 package")
				}
			}
		}
	}
}

func TestAgentMetadataRejectsWrongPackageAndDownloadHost(t *testing.T) {
	a := testApp(t, "", &fakeRunner{})
	for _, m := range [][]byte{
		npmFixture("pi", "1.0.0", npmRegistry+"/pi.tgz", []byte("pi"), nil),
		npmFixture(agentPackage(Pi), "1.0.0", "https://untrusted.example/pi.tgz", []byte("pi"), nil),
		npmFixture(agentPackage(Pi), "1.0.0/../../oops", npmRegistry+"/pi.tgz", []byte("pi"), nil),
	} {
		a.Client = fixtureClient(map[string][]byte{"/@earendil-works/pi-coding-agent/latest": m})
		if _, err := a.latest(context.Background(), Pi); err == nil {
			t.Fatal("accepted unsafe metadata")
		}
	}
}

func TestAgentSHA512FailureAndCache(t *testing.T) {
	data := []byte("official agent archive")
	sum := sha512.Sum512(data)
	a := Artifact{URL: npmRegistry + "/agent.tgz", SHA512: hex.EncodeToString(sum[:])}
	c := fixtureClient(map[string][]byte{"/agent.tgz": data})
	dir := t.TempDir()
	file, err := c.Download(context.Background(), a, dir, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixtureClient(nil).Download(context.Background(), a, dir, io.Discard); err != nil {
		t.Fatal("verified cache not reused", err)
	}
	os.WriteFile(file, []byte("corrupted"), 0600)
	if _, err = c.Download(context.Background(), Artifact{URL: a.URL, SHA512: strings.Repeat("a", 128)}, dir, io.Discard); err == nil {
		t.Fatal("checksum failure reported as success")
	}
}

func TestAgentNativeInstallAndFailure(t *testing.T) {
	for _, broken := range []bool{false, true} {
		root := t.TempDir()
		archive := makeZIP(t, map[string]string{"package/bin/opencode.exe": "payload"})
		runner := &fakeRunner{capture: func(c Command) (string, error) {
			if broken {
				return "2.0.0", nil
			}
			return "2.2.5", nil
		}}
		a := testApp(t, "", runner)
		a.Host = Host{OS: "windows", Arch: "amd64"}
		a.Client = fixtureClient(map[string][]byte{"/opencode.zip": archive})
		p := InstallPlan{Tool: OpenCode, Release: Release{Version: "2.2.5", Artifact: Artifact{URL: npmRegistry + "/opencode.zip", SHA256: digest(archive)}}}
		locatePlan(&p, filepath.Join(root, "程序 中文 space"), filepath.Join(root, "downloads"))
		result := a.executePlan(context.Background(), p)
		if broken {
			if result.Status != "失败" || len(a.State.Records) != 0 {
				t.Fatal("bad version recorded as success", result)
			}
		} else {
			// PATH cannot be configured by a fixture without PowerShell; the
			// verified install must still be recorded as waiting, never lost.
			if result.Status != "待用户操作" || a.State.Records[OpenCode].Version != "2.2.5" {
				t.Fatal(result)
			}
		}
	}
}

func TestAgentZeroAndRetainAvoidNetworkAndInstall(t *testing.T) {
	for _, input := range []string{"0\n", "1\n"} {
		r := &fakeRunner{paths: map[string]string{"codex": "existing-codex"}, capture: func(c Command) (string, error) {
			if c.Name == "existing-codex" {
				return "codex-cli 0.166.0", nil
			}
			return "", os.ErrNotExist
		}, run: func(Command) error { t.Fatal("installed retained agent"); return nil }}
		a := testApp(t, input, r)
		result := a.manage(context.Background(), Codex)
		if input == "0\n" && result.Status != "跳过" || input == "1\n" && result.Status != "保留" {
			t.Fatal(result)
		}
	}
	a := testApp(t, "0\n", &fakeRunner{})
	archive := []byte("payload")
	a.Client = fixtureClient(map[string][]byte{"/@openai/codex/latest": npmFixture("@openai/codex", "1.2.3", npmRegistry+"/codex.tgz", archive, map[string]string{"@openai/codex-" + agentPlatform(a.Host): "npm:@openai/codex@1.2.3-native"}), "/@openai/codex/1.2.3-native": npmFixture("@openai/codex", "1.2.3-native", npmRegistry+"/native.tgz", archive, nil)})
	result := a.manage(context.Background(), Codex)
	if result.Status != "跳过" || len(a.State.Records) != 0 {
		t.Fatal("zero did not cancel directory", result)
	}
	if _, err := os.Stat(filepath.Join(a.Home, "agents")); !os.IsNotExist(err) {
		t.Fatal("cancel created install root")
	}
}

func TestAgentLockValidation(t *testing.T) {
	main := map[string]any{"version": "1.4.0", "resolved": npmRegistry + "/pi.tgz", "integrity": integrity512([]byte("pi"))}
	r := Release{Package: agentPackage(Pi), Version: "1.4.0", Integrity: main["integrity"].(string)}
	file := filepath.Join(t.TempDir(), "package-lock.json")
	for _, scenario := range []string{"good", "foreign", "link", "bad integrity", "wrong version"} {
		pkg := map[string]any{"version": "1.4.0", "resolved": main["resolved"], "integrity": main["integrity"]}
		switch scenario {
		case "foreign":
			pkg["resolved"] = "https://mirror.example/pi.tgz"
		case "link":
			pkg["link"] = true
		case "bad integrity":
			pkg["integrity"] = "sha512-AAAA"
		case "wrong version":
			pkg["version"] = "9.9.9"
		}
		os.WriteFile(file, jsonBytes(map[string]any{"lockfileVersion": 3, "packages": map[string]any{"": map[string]any{}, "node_modules/" + r.Package: pkg}}), 0600)
		err := validateAgentLock(file, r)
		if (err == nil) != (scenario == "good") {
			t.Fatal(scenario, err)
		}
	}
}

func TestNpmAgentPrivateInstallAndUntrustedLock(t *testing.T) {
	for _, tool := range []Tool{DeepSeek, Pi} {
		for _, broken := range []bool{false, true} {
			root := t.TempDir()
			node := filepath.Join(root, "node.exe")
			npm := filepath.Join(root, "node_modules", "npm", "bin", "npm-cli.js")
			git := filepath.Join(root, "Git", "cmd", "git.exe")
			bash := filepath.Join(root, "Git", "bin", "bash.exe")
			for _, file := range []string{node, npm, git, bash} {
				os.MkdirAll(filepath.Dir(file), 0755)
				os.WriteFile(file, []byte("fixture"), 0600)
			}
			version := "1.4.0"
			if tool == DeepSeek {
				version = "0.2.1-rc.5"
			}
			r := Release{Version: version, Package: agentPackage(tool), Integrity: integrity512([]byte("agent"))}
			p := InstallPlan{Tool: tool, Release: r}
			locatePlan(&p, filepath.Join(root, "安装 中文 space"), filepath.Join(root, "下载 space"))
			ensureWritable(p.DownloadDir)
			ci := false
			resolves := 0
			runner := &fakeRunner{paths: map[string]string{"node": node, "npm": filepath.Join(root, "npm.cmd"), "git": git}, capture: func(c Command) (string, error) {
				if c.Name == git {
					return "git version 2.54.0", nil
				}
				if c.Name == node && len(c.Args) > 1 && strings.HasSuffix(c.Args[0], "npm-cli.js") {
					return "11.13.0", nil
				}
				if c.Name == node && len(c.Args) == 1 {
					return "v24.16.0", nil
				}
				if c.Name == node && len(c.Args) == 2 {
					return version, nil
				}
				return "", os.ErrNotExist
			}, run: func(c Command) error {
				if c.Name != node {
					t.Fatal("used shell or global npm", c)
				}
				if !strings.Contains(strings.Join(c.Args, " "), "--global=false") || !strings.Contains(strings.Join(c.Env, " "), "NPM_CONFIG_CACHE="+p.DownloadDir) {
					t.Fatal("global npm cache/prefix used", c)
				}
				if c.Args[1] == "install" {
					resolves++
					source := npmRegistry + "/agent.tgz"
					if broken {
						source = "https://mirror.example/agent.tgz"
					}
					return saveJSON(filepath.Join(p.InstallDir, "package-lock.json"), map[string]any{"lockfileVersion": 3, "packages": map[string]any{"": map[string]any{}, "node_modules/" + r.Package: map[string]any{"version": version, "resolved": source, "integrity": r.Integrity}}})
				}
				if c.Args[1] != "ci" {
					t.Fatal("unexpected npm action", c)
				}
				ci = true
				pkg := filepath.Join(p.InstallDir, "node_modules", filepath.FromSlash(r.Package))
				os.MkdirAll(filepath.Join(pkg, "dist"), 0755)
				os.WriteFile(filepath.Join(pkg, "dist", "cli.js"), []byte("fixture"), 0600)
				return saveJSON(filepath.Join(pkg, "package.json"), map[string]any{"bin": map[string]string{agentCommand(tool): "dist/cli.js"}})
			}}
			a := testApp(t, "", runner)
			a.Host = Host{OS: "windows", Arch: "amd64"}
			record, err := a.installNpmAgent(context.Background(), p, io.Discard)
			if broken {
				if err == nil || ci {
					t.Fatal("installed packages before validating their origin", err)
				}
				continue
			}
			if err != nil || !ci || record.Runtime != node || record.Version != version {
				t.Fatal(record, err)
			}
			launcher, e := os.ReadFile(record.Path)
			if e != nil || !strings.Contains(string(launcher), "%~dp0..") {
				t.Fatal("no path-safe launcher", e)
			}
			if tool == Pi && !strings.Contains(string(launcher), filepath.Dir(bash)) {
				t.Fatal("custom Git Bash not propagated")
			}
			if _, err = a.installNpmAgent(context.Background(), p, io.Discard); err != nil || resolves != 1 {
				t.Fatal("retry resolved the same dependency graph again", resolves, err)
			}
		}
	}
}

func TestCodexPrefersOfficialReleaseChannel(t *testing.T) {
	a := testApp(t, "", &fakeRunner{})
	a.Host = Host{OS: "windows", Arch: "amd64"}
	name := "codex-npm-win32-x64-0.166.0.tgz"
	raw := "https://releases.openai.com/codex/releases/0.166.0/" + name
	a.Client = fixtureClient(map[string][]byte{"/codex/channels/latest": jsonBytes(map[string]any{"tag_name": "rust-v0.166.0", "assets": []map[string]string{{"name": name, "browser_download_url": raw, "digest": "sha256:" + strings.Repeat("a", 64)}}})})
	r, err := a.latest(context.Background(), Codex)
	if err != nil || r.Artifact.URL != raw || !validSHA(r.Artifact.SHA256) {
		t.Fatal(r, err)
	}
}

func TestWorkBuddyInstallerFailureAndRunningAppProtection(t *testing.T) {
	for _, scenario := range []string{"success", "signature failure", "running"} {
		root := t.TempDir()
		installed := false
		p := InstallPlan{Tool: WorkBuddy, Release: Release{Version: "5.9.3", Artifact: Artifact{URL: "https://download.codebuddy.cn/WorkBuddy.exe", Signature: "Tencent WorkBuddy"}}}
		locatePlan(&p, filepath.Join(root, "所选 中文 space"), filepath.Join(root, "downloads"))
		runner := &fakeRunner{paths: map[string]string{"powershell.exe": "powershell"}, capture: func(c Command) (string, error) {
			if strings.Contains(strings.Join(c.Args, " "), "FileVersionInfo") {
				return "5.9.3.0", nil
			}
			if scenario == "running" {
				return "1", nil
			}
			return "0", nil
		}, run: func(c Command) error {
			args := strings.Join(c.Args, " ")
			if strings.Contains(args, "verify-signature.ps1") && scenario == "signature failure" {
				return errors.New("publisher mismatch")
			}
			if strings.Contains(args, "install-workbuddy.ps1") {
				installed = true
				os.MkdirAll(p.InstallDir, 0755)
				return os.WriteFile(filepath.Join(p.InstallDir, "WorkBuddy.exe"), []byte("fixture"), 0600)
			}
			return nil
		}}
		a := testApp(t, "", runner)
		a.Host = Host{OS: "windows", Arch: "amd64"}
		a.Client = fixtureClient(map[string][]byte{"/WorkBuddy.exe": []byte("installer")})
		result := a.executePlan(context.Background(), p)
		if scenario == "success" {
			if !installed || result.Status != "成功" {
				t.Fatal(result)
			}
		} else {
			if installed || result.Status == "成功" || len(a.State.Records) != 0 {
				t.Fatal("unverified installer or running app was modified", result)
			}
		}
	}
}

func TestAgentPrereleaseComparison(t *testing.T) {
	for _, pair := range [][2]string{{"0.2.1-rc.5", "0.2.1-rc.4"}, {"0.2.1-rc.10", "0.2.1-rc.9"}, {"0.2.1", "0.2.1-rc.5"}, {"2.0.0-alpha.1", "1.9.9"}} {
		if compareAgentVersion(pair[0], pair[1]) <= 0 || compareAgentVersion(pair[1], pair[0]) >= 0 {
			t.Fatal("bad prerelease ordering", pair)
		}
	}
}

func TestWorkBuddyCustomRunningPathIsReadOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "现有 应用")
	os.MkdirAll(root, 0755)
	os.WriteFile(filepath.Join(root, "WorkBuddy.exe"), []byte("existing"), 0600)
	runner := &fakeRunner{paths: map[string]string{"powershell.exe": "powershell"}, capture: func(c Command) (string, error) {
		if strings.Contains(strings.Join(c.Args, " "), "Get-Process -Name WorkBuddy") {
			return string(jsonBytes([]string{root})), nil
		}
		if strings.Contains(strings.Join(c.Args, " "), "FileVersionInfo") {
			return "5.9.3.0", nil
		}
		return "", os.ErrNotExist
	}, run: func(Command) error { t.Fatal("WorkBuddy detection invoked installer"); return nil }}
	a := testApp(t, "", runner)
	a.Host = Host{OS: "windows", Arch: "amd64"}
	d := a.detect(context.Background(), WorkBuddy)
	if !d.Healthy || d.Version != "5.9.3" || d.Path != filepath.Join(root, "WorkBuddy.exe") {
		t.Fatal(d)
	}
	if _, e := os.Stat(a.Home); !os.IsNotExist(e) {
		t.Fatal("doctor wrote state")
	}
}

func TestAgentDependencyAndWorkBuddyLocation(t *testing.T) {
	a := testApp(t, "", &fakeRunner{})
	for _, tool := range []Tool{Pi, DeepSeek} {
		var pending *pendingError
		if err := a.agentPrerequisite(context.Background(), tool); !errors.As(err, &pending) {
			t.Fatal("missing runtime not reported", tool, err)
		}
	}
	a.Host = Host{OS: "windows", Arch: "amd64"}
	d := Detection{Tool: WorkBuddy, Installed: true, Path: filepath.Join(t.TempDir(), "既有 应用", "WorkBuddy.exe")}
	p, err := a.planInstall(WorkBuddy, Release{Version: "5.9.3"}, d, true)
	if err != nil || !p.Fixed || p.InstallDir != filepath.Dir(d.Path) {
		t.Fatal("WorkBuddy migration allowed", p, err)
	}
	locatePlan(&p, filepath.Join(t.TempDir(), "other disk"), t.TempDir())
	if p.InstallDir != filepath.Dir(d.Path) {
		t.Fatal("moved installed WorkBuddy")
	}
}

// Opt in: real official installs under a caller-selected isolated directory.
// No user PATH changes, login, GUI launch, or model requests are performed.
func TestLiveOfficialAgents(t *testing.T) {
	root := os.Getenv("OXGEN_LIVE_AGENT_DIR")
	selection := os.Getenv("OXGEN_LIVE_AGENTS")
	if root == "" || selection == "" {
		t.Skip("set OXGEN_LIVE_AGENT_DIR and OXGEN_LIVE_AGENTS")
	}
	if !filepath.IsAbs(root) {
		t.Fatal("absolute test directory required")
	}
	for _, tool := range agentTools {
		if tool == WorkBuddy || !strings.Contains(","+selection+",", ","+string(tool)+",") {
			continue
		}
		t.Run(string(tool), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 18*time.Minute)
			defer cancel()
			a := &App{UI: newUI(strings.NewReader(""), os.Stdout, false), Runner: OSRunner{}, Client: newClient(), Host: detectHost(), Home: filepath.Join(root, "状态", string(tool)), State: State{Schema: 1, Records: map[Tool]Record{}}}
			r, err := a.latest(ctx, tool)
			if err != nil {
				t.Fatal(err)
			}
			p, err := a.planInstall(tool, r, Detection{Tool: tool}, false)
			if err != nil {
				t.Fatal(err)
			}
			locatePlan(&p, filepath.Join(root, string(tool), "程序 中文 space"), filepath.Join(root, string(tool), "下载 space"))
			if err = ensureWritable(p.DownloadDir); err != nil {
				t.Fatal(err)
			}
			record, err := a.install(ctx, p, os.Stdout)
			if err != nil {
				t.Fatal(err)
			}
			if record.Version != r.Version || !contained(root, record.Path) {
				t.Fatal("bad record", record)
			}
			if err = saveJSON(filepath.Join(root, string(tool), "verified-record.json"), record); err != nil {
				t.Fatal(err)
			}
			t.Logf("verified %s %s %s on %s/%s", tool, record.Version, record.Path, runtime.GOOS, runtime.GOARCH)
		})
	}
}

func TestAgentMenuAndHelp(t *testing.T) {
	a := testApp(t, "4\n0\n0\n", &fakeRunner{})
	var b bytes.Buffer
	a.UI.Out = &b
	if err := a.menu(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, tool := range agentTools {
		if !strings.Contains(b.String(), tool.String()+" 检测／安装／更新") {
			t.Fatal("missing agent menu", tool)
		}
	}
	var out bytes.Buffer
	if code := Main(context.Background(), []string{"--help"}, strings.NewReader(""), &out, io.Discard, "test"); code != 0 || !strings.Contains(out.String(), "DeepSeek Harness") {
		t.Fatal("missing help")
	}
}

func TestWindowsAgentLauncherWithChineseNodePath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows CMD encoding")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node not available")
	}
	ps, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("Windows PowerShell not available")
	}
	root := filepath.Join(t.TempDir(), "程序 中文 %space!&")
	os.MkdirAll(filepath.Join(root, "bin"), 0755)
	runtimePath := filepath.Join(root, "Node 运行时", "node.exe")
	os.MkdirAll(filepath.Dir(runtimePath), 0755)
	if err = copyAgentFile(context.Background(), node, runtimePath); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(root, "cli.js")
	if err = os.WriteFile(entry, []byte("console.log('1.4.0'); if(process.argv[2] !== '--version') process.exit(7)"), 0600); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(root, "bin", "pi.cmd")
	if err = os.WriteFile(launcher, []byte(windowsNpmAgentLauncher(runtimePath, "..\\cli.js", "")), 0600); err != nil {
		t.Fatal(err)
	}
	text, err := (OSRunner{}).Capture(context.Background(), Command{Name: ps, Args: []string{"-NoProfile", "-NonInteractive", "-Command", "chcp 936 >$null; & $env:OXGEN_TEST_AGENT --version; exit $LASTEXITCODE"}, Env: []string{"OXGEN_TEST_AGENT=" + launcher}})
	if err != nil || !strings.Contains(text, "1.4.0") {
		t.Fatal("Chinese Node path did not run", text, err)
	}
}
