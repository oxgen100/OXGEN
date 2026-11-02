package oxgen

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	paths    map[string]string
	capture  func(Command) (string, error)
	run      func(Command) error
	commands []Command
}

func (f *fakeRunner) LookPath(s string) (string, error) {
	if p, ok := f.paths[s]; ok {
		return p, nil
	}
	return "", os.ErrNotExist
}
func (f *fakeRunner) Capture(_ context.Context, c Command) (string, error) {
	f.commands = append(f.commands, c)
	if f.capture != nil {
		return f.capture(c)
	}
	return "", errors.New("not installed")
}
func (f *fakeRunner) Run(_ context.Context, c Command, _ io.Writer) error {
	f.commands = append(f.commands, c)
	if f.run != nil {
		return f.run(c)
	}
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixtureClient(fixtures map[string][]byte) *Client {
	return &Client{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, ok := fixtures[r.URL.Path]
		status := 200
		if !ok {
			status = 404
			body = []byte("fixture missing: " + r.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: r}, nil
	})}}
}
func jsonBytes(v any) []byte { b, _ := json.Marshal(v); return b }
func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func testApp(t *testing.T, input string, r Runner) *App {
	t.Helper()
	return &App{UI: newUI(strings.NewReader(input), io.Discard, false), Runner: r, Client: fixtureClient(nil), Host: Host{OS: runtime.GOOS, Arch: runtime.GOARCH, Distro: "ubuntu", Codename: "noble"}, Home: filepath.Join(t.TempDir(), "中文 space"), State: State{Schema: 1, Records: map[Tool]Record{}}, Version: "test"}
}

func TestVersions(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want int
	}{{"v24.10.0", "24.9.0", 1}, {"git version 2.56.0.windows.4", "2.56.0", 0}, {"1:2.43.0-1ubuntu", "2.44.0", -1}, {"Python 3.15.2", "3.15.1", 1}} {
		if got := compareVersion(tt.a, tt.b); got != tt.want {
			t.Errorf("%s %s: %d", tt.a, tt.b, got)
		}
	}
}

func TestGitWindowsRevisionAndInterruptedState(t *testing.T) {
	if compareToolVersion(Git, "2.56.0.windows.2", "2.56.0.windows.4") != -1 {
		t.Fatal("ignored Windows revision")
	}
	if toolVersion(Git, "git version 2.56.0.windows.4") != "2.56.0.windows.4" {
		t.Fatal("lost Windows revision")
	}
	home := t.TempDir()
	expected := State{Schema: 1, Records: map[Tool]Record{Git: {Version: "2.56.0.windows.4"}}}
	if err := saveJSON(filepath.Join(home, "state.json.previous"), expected); err != nil {
		t.Fatal(err)
	}
	got, err := loadState(home)
	if err != nil || got.Records[Git].Version != expected.Records[Git].Version {
		t.Fatal("rollback copy lost", got, err)
	}
}

func TestVersionedRetryPreservesUnownedFiles(t *testing.T) {
	root := t.TempDir()
	p := InstallPlan{Tool: Node, InstallDir: filepath.Join(root, "versions", "26.0.0")}
	if err := os.MkdirAll(p.InstallDir, 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(p.InstallDir, "user.txt"), []byte("keep"), 0600)
	if err := prepareVersionedTarget(p); err == nil {
		t.Fatal("claimed unowned directory")
	}
	if b, _ := os.ReadFile(filepath.Join(p.InstallDir, "user.txt")); string(b) != "keep" {
		t.Fatal("lost user file")
	}
	p.InstallDir = filepath.Join(root, "owned", "26.0.0")
	if err := prepareVersionedTarget(p); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(p.InstallDir, 0755)
	os.WriteFile(filepath.Join(p.InstallDir, "partial.txt"), []byte("partial"), 0600)
	if err := prepareVersionedTarget(p); err != nil {
		t.Fatal("could not retry owned attempt", err)
	}
	kept, _ := filepath.Glob(p.InstallDir + ".previous-*")
	if len(kept) != 1 {
		t.Fatal("did not preserve previous attempt")
	}
}

func TestDiskAndDirectoryPreflight(t *testing.T) {
	root := t.TempDir()
	if err := checkSpace(root, 1<<62); err == nil {
		t.Fatal("did not detect impossible disk requirement")
	}
	file := filepath.Join(root, "file")
	os.WriteFile(file, []byte("keep"), 0600)
	if err := ensureWritable(filepath.Join(file, "child")); err == nil {
		t.Fatal("accepted file as directory")
	}
}

func TestRecordedRuntimeIsNotClaimedToBeActive(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "python.exe")
	r := &fakeRunner{capture: func(c Command) (string, error) {
		if len(c.Args) > 1 && strings.Contains(c.Args[1], "sys.executable") {
			return exe, nil
		}
		return "Python 3.15.2", nil
	}}
	a := testApp(t, "", r)
	a.State.Records[Python] = Record{Path: exe, Version: "3.15.2", Source: "OXGEN Python 官方运行时"}
	d := a.detect(context.Background(), Python)
	if !d.Installed || d.Active || !d.Healthy {
		t.Fatal(d)
	}
}

func TestOfficialSourcesRejectForeignOrDowngrade(t *testing.T) {
	for _, raw := range []string{"http://nodejs.org/dist/a.zip", "https://nodejs.org.evil.test/a", "https://user:pass@www.python.org/a", "https://github.com:1234/a", "file:///tmp/a"} {
		if officialURL(raw) == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, raw := range []string{"https://nodejs.org/dist/a.zip", "https://www.python.org/ftp/python/a", "https://release-assets.githubusercontent.com/a"} {
		if err := officialURL(raw); err != nil {
			t.Error(err)
		}
	}
}

func TestNodeSelectsNewestStableLTSWithVerifiedFile(t *testing.T) {
	a := testApp(t, "", &fakeRunner{})
	a.Host = Host{OS: "linux", Arch: "arm64"}
	a.Client = fixtureClient(map[string][]byte{
		"/dist/index.json":             []byte(`[{"version":"v28.0.0-rc.1","lts":"Future"},{"version":"v27.1.0","lts":false},{"version":"v24.1.0","lts":"Old"},{"version":"v26.2.0","lts":"New"}]`),
		"/dist/v26.2.0/SHASUMS256.txt": []byte(strings.Repeat("a", 64) + "  node-v26.2.0-linux-arm64.tar.gz\n"),
	})
	r, err := a.nodeLatest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != "26.2.0" || r.Artifact.SHA256 != strings.Repeat("a", 64) || !strings.Contains(r.Artifact.URL, "linux-arm64") {
		t.Fatalf("wrong release: %+v", r)
	}
}

func TestPythonUsesCompleteRuntimeAndArchitecture(t *testing.T) {
	a := testApp(t, "", &fakeRunner{})
	a.Host = Host{OS: "windows", Arch: "arm64"}
	a.Client = fixtureClient(map[string][]byte{
		"/ftp/python/index-windows.json": []byte(`{"versions":[{"id":"pythonembed-3.99-arm64","company":"PythonEmbed","sort-version":"3.99.0","tag":"3.99-arm64"},{"id":"pythoncore-3.15-64","company":"PythonCore","sort-version":"3.15.2","tag":"3.15-64"},{"id":"pythoncore-3.15-arm64","company":"PythonCore","sort-version":"3.15.2","tag":"3.15-arm64","url":"https://www.python.org/ftp/python/3.15.2/python-3.15.2-arm64.zip","hash":{"sha256":"` + strings.Repeat("a", 64) + `"}}]}`),
		"/downloads/windows/":            []byte(`<a href="https://www.python.org/ftp/python/pymanager/python-manager-26.4.msix">manager</a>`),
	})
	r, err := a.pythonLatest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Tag != "3.15-arm64" || strings.Contains(r.Artifact.URL, "embed") || r.Manager.Signature != "Python Software Foundation" {
		t.Fatalf("wrong runtime %+v", r)
	}
}

func TestAPTFiltersThirdPartyCandidates(t *testing.T) {
	out := `git | 1:99.0.0 | https://mirror.example/ubuntu noble/main amd64 Packages
git | 1:2.43.0-1ubuntu7 | http://archive.ubuntu.com/ubuntu noble-updates/main amd64 Packages
git | 1:2.40.0-1ubuntu1 | https://security.ubuntu.com/ubuntu noble-security/main amd64 Packages`
	if got := aptVersionFromMadison(out, "ubuntu"); got != "1:2.43.0-1ubuntu7" {
		t.Fatal(got)
	}
	if got := aptVersionFromMadison(out, "debian"); got != "" {
		t.Fatal(got)
	}
	for _, host := range []Host{{OS: "linux", Distro: "ubuntu", Codename: "noble", Arch: "arm64"}, {OS: "linux", Distro: "debian", Codename: "trixie", Arch: "amd64"}} {
		s, err := aptSources(host)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(s, "signed-by=") {
			t.Fatal("missing signatures")
		}
		if strings.Contains(s, "http://") {
			t.Fatal("insecure repository")
		}
		if host.Arch == "arm64" && !strings.Contains(s, "ports.ubuntu.com") {
			t.Fatal(s)
		}
	}
	if _, err := aptSources(Host{Distro: "ubuntu", Codename: "noble; touch /tmp/x"}); err == nil {
		t.Fatal("accepted injected codename")
	}
}

func TestDownloadsRejectBadHashAndPreserveExisting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "downloaded data") }))
	defer server.Close()
	c := &Client{HTTP: server.Client(), allowTest: true}
	dir := t.TempDir()
	target := filepath.Join(dir, "file.zip")
	os.WriteFile(target, []byte("keep existing"), 0600)
	_, err := c.Download(context.Background(), Artifact{URL: server.URL + "/file.zip", SHA256: strings.Repeat("0", 64)}, dir, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "SHA256") {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(target)
	if string(b) != "keep existing" {
		t.Fatal("overwrote existing")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("partial download not cleaned")
	}
}

func TestDownloadsReuseVerifiedCacheAndFailClosed(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; fmt.Fprint(w, "payload") }))
	defer server.Close()
	c := &Client{HTTP: server.Client(), allowTest: true}
	dir := t.TempDir()
	asset := Artifact{URL: server.URL + "/file.zip", SHA256: digest([]byte("payload")), Size: 7}
	for i := 0; i < 2; i++ {
		if _, err := c.Download(context.Background(), asset, dir, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	if requests != 1 {
		t.Fatal("verified cache not reused")
	}
	if _, err := c.Download(context.Background(), Artifact{URL: asset.URL}, dir, io.Discard); err == nil {
		t.Fatal("accepted unverifiable file")
	}
}

func TestTruncatedDownloadDoesNotCommit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		fmt.Fprint(w, "short")
	}))
	defer server.Close()
	c := &Client{HTTP: server.Client(), allowTest: true}
	dir := t.TempDir()
	if _, err := c.Download(context.Background(), Artifact{URL: server.URL + "/a.zip", SHA256: digest([]byte("short"))}, dir, io.Discard); err == nil {
		t.Fatal("accepted truncated download")
	}
	if _, err := os.Stat(filepath.Join(dir, "a.zip")); !os.IsNotExist(err) {
		t.Fatal("committed incomplete download")
	}
}

func makeZIP(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, text := range entries {
		f, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		f.Write([]byte(text))
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}

func TestArchiveTraversalAndExistingDirectory(t *testing.T) {
	for _, name := range []string{"../outside.txt", "/absolute.txt", "C:/outside.txt", "top/../../outside.txt", "top\\..\\outside.txt"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, "bad.zip")
			os.WriteFile(file, makeZIP(t, map[string]string{name: "bad"}), 0600)
			if err := extractArchive(file, filepath.Join(root, "target"), false); err == nil {
				t.Fatal("accepted traversal")
			}
			if _, err := os.Stat(filepath.Join(root, "target")); !os.IsNotExist(err) {
				t.Fatal("committed failed extraction")
			}
		})
	}
	root := t.TempDir()
	file := filepath.Join(root, "good.zip")
	os.WriteFile(file, makeZIP(t, map[string]string{"top/node.exe": "binary", "top/nested/内容.txt": "hello"}), 0600)
	dest := filepath.Join(root, "中文 space", "node")
	if err := extractArchive(file, dest, true); err != nil {
		t.Fatal(err)
	}
	if b, e := os.ReadFile(filepath.Join(dest, "nested", "内容.txt")); e != nil || string(b) != "hello" {
		t.Fatal(e, string(b))
	}
	if err := extractArchive(file, dest, true); err == nil {
		t.Fatal("overwrote existing installation")
	}
}

func TestTarSymlinkCannotEscape(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "bad.tar.gz")
	f, _ := os.Create(file)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "root/bin/link", Typeflag: tar.TypeSymlink, Linkname: "../../../outside", Mode: 0755})
	tw.Close()
	gz.Close()
	f.Close()
	if err := extractArchive(file, filepath.Join(root, "target"), true); err == nil {
		t.Fatal("accepted external symlink")
	}
}

func TestArchiveCancellationAndLinkedParent(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "archive.zip")
	os.WriteFile(file, makeZIP(t, map[string]string{"a.txt": "data"}), 0600)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := extractArchiveContext(ctx, file, filepath.Join(root, "cancelled"), false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "cancelled")); !os.IsNotExist(err) {
		t.Fatal("committed cancelled installation")
	}
	stage := filepath.Join(root, "stage")
	links := map[string]bool{filepath.Join(stage, "alias"): true}
	if _, err := archivePath(stage, "top/alias/file.txt", true, links); err == nil {
		t.Fatal("allowed archive-created symlink parent")
	}
}

func TestLiveLinuxCachedNode(t *testing.T) {
	root := os.Getenv("OXGEN_LIVE_LINUX_DIR")
	cache := os.Getenv("OXGEN_NODE_CACHE")
	if runtime.GOOS != "linux" || root == "" || cache == "" {
		t.Skip("opt-in Linux isolated installation check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	a := &App{Host: detectHost(), Runner: OSRunner{}, Client: newClient(), State: State{Schema: 1, Records: map[Tool]Record{}}, Home: root, UI: newUI(strings.NewReader(""), io.Discard, false)}
	release, err := a.nodeLatest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plan := InstallPlan{Tool: Node, Release: release, Root: filepath.Join(root, "node"), InstallDir: filepath.Join(root, "node", "versions", release.Version), DownloadDir: cache}
	start := time.Now()
	record, err := a.install(ctx, plan, os.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("isolated Linux install verified:", record.Version, "elapsed", time.Since(start))
}

func TestStateRoundTripAndCorruption(t *testing.T) {
	home := filepath.Join(t.TempDir(), "目录 space")
	s := State{Schema: 1, Records: map[Tool]Record{Node: {Version: "26.0.0", Path: "D:\\Tools\\node.exe"}}, Pending: &Pending{Stage: "等待重启", Location: "D:\\WSL"}}
	if err := saveState(home, s); err != nil {
		t.Fatal(err)
	}
	got, err := loadState(home)
	if err != nil || got.Pending.Location != s.Pending.Location || got.Records[Node].Version != "26.0.0" {
		t.Fatal(got, err)
	}
	if err := saveState(home, s); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(home, "state.json"), []byte("broken"), 0600)
	if _, err = loadState(home); err == nil {
		t.Fatal("silently discarded corrupted state")
	}
}

func TestDoctorIsReadOnlyWithoutCommandsOnPATH(t *testing.T) {
	r := &fakeRunner{run: func(Command) error { t.Fatal("doctor invoked an installer"); return nil }}
	a := testApp(t, "", r)
	if err := a.doctor(context.Background(), true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.Home); !os.IsNotExist(err) {
		t.Fatal("doctor wrote state directory")
	}
	for _, command := range r.commands {
		// Existing editors can be discovered in their standard directory even
		// without PATH. Only their read-only version probe is permitted here.
		if len(command.Args) == 0 || command.Args[len(command.Args)-1] != "--version" {
			t.Fatalf("doctor issued a non-version command: %+v", command)
		}
	}
}

func TestNonInteractiveCommandDoesNotConsumeMenuInput(t *testing.T) {
	input := bytes.NewBufferString("3\n0\n0\n")
	r := OSRunner{Input: input}
	c := Command{Name: "true"}
	if runtime.GOOS == "windows" {
		c = Command{Name: "cmd.exe", Args: []string{"/d", "/c", "exit", "0"}}
	}
	if err := r.Run(context.Background(), c, io.Discard); err != nil {
		t.Fatal(err)
	}
	if input.String() != "3\n0\n0\n" {
		t.Fatal("version-check child consumed later menu input")
	}
}

func TestCaptureKeepsWarningsOutOfParsedStdout(t *testing.T) {
	r := OSRunner{}
	c := Command{Name: "sh", Args: []string{"-c", "printf actual; printf warning >&2"}}
	if runtime.GOOS == "windows" {
		c = Command{Name: "powershell.exe", Args: []string{"-NoProfile", "-Command", "[Console]::Out.Write('actual'); [Console]::Error.Write('warning')"}}
	}
	out, err := r.Capture(context.Background(), c)
	if err != nil || out != "actual" {
		t.Fatal("stderr contaminated parsed output", out, err)
	}
}

func TestCachedRepositoryVersionDoesNotSuppressUpdateChoice(t *testing.T) {
	r := &fakeRunner{paths: map[string]string{"git": "/usr/bin/git"}, capture: func(c Command) (string, error) {
		if c.Name == "apt-cache" {
			return "git | 2.53.0 | https://archive.ubuntu.com/ubuntu resolute/main amd64 Packages", nil
		}
		return "git version 2.53.0", nil
	}}
	a := testApp(t, "1\n", r)
	a.Host = Host{OS: "linux", Distro: "ubuntu"}
	var output bytes.Buffer
	a.UI.Out = &output
	result := a.manage(context.Background(), Git)
	if result.Status != "保留" || !strings.Contains(output.String(), "刷新官方仓库") {
		t.Fatal("cached index was treated as verified latest", result, output.String())
	}
}

func TestAppleGitDoesNotRelabelExternalInstallation(t *testing.T) {
	r := &fakeRunner{capture: func(c Command) (string, error) {
		switch c.Name {
		case "xcode-select":
			return "/Library/Developer/CommandLineTools", nil
		case "softwareupdate":
			return "No new software available.", nil
		case "/usr/bin/git":
			return "git version 2.50.0 (Apple Git-160)", nil
		}
		return "", os.ErrNotExist
	}}
	a := testApp(t, "", r)
	a.Host.OS = "darwin"
	got, err := a.installAppleGit(context.Background(), InstallPlan{Current: Detection{Installed: true, Version: "99.0.0", Path: "/opt/homebrew/bin/git"}, InstallDir: "/Library/Developer/CommandLineTools"}, io.Discard)
	if err != nil || got.Path != "/usr/bin/git" || got.Version != "2.50.0" {
		t.Fatal("external Git mislabeled as Apple", got, err)
	}
}

func TestLiveExistingPythonVenv(t *testing.T) {
	root := os.Getenv("OXGEN_LIVE_DIR")
	if root == "" {
		t.Skip("isolated live verification is opt-in")
	}
	a := &App{Runner: OSRunner{Input: strings.NewReader("")}, Host: detectHost(), State: State{Schema: 1, Records: map[Tool]Record{}}, UI: newUI(strings.NewReader(""), io.Discard, false)}
	d := a.detect(context.Background(), Python)
	if !d.Installed {
		t.Fatal("existing Python not available for isolated venv check", d.Details)
	}
	if err := a.verifyPython(context.Background(), d.Path, filepath.Join(root, "已有 Python 验证 space"), os.Stdout); err != nil {
		t.Fatal(err)
	}
	t.Log("verified existing Python without changing its installation:", d.Version)
}

func TestWindowsStoreAliasDoesNotTriggerPythonInstall(t *testing.T) {
	r := &fakeRunner{paths: map[string]string{"python": "C:\\Users\\test\\AppData\\Local\\Microsoft\\WindowsApps\\python.exe"}}
	a := testApp(t, "", r)
	a.Host.OS = "windows"
	d := a.detect(context.Background(), Python)
	if d.Installed || len(r.commands) != 0 {
		t.Fatal("executed store alias")
	}
}

func TestPythonMissingPipReportedUnhealthy(t *testing.T) {
	python := filepath.Join(t.TempDir(), "python.exe")
	r := &fakeRunner{paths: map[string]string{"python": python, "python3": python}, capture: func(c Command) (string, error) {
		if len(c.Args) > 1 && c.Args[0] == "-m" {
			return "pip missing", errors.New("missing")
		}
		if len(c.Args) > 1 && strings.Contains(c.Args[1], "sys.executable") {
			return python, nil
		}
		return "Python 3.15.2", nil
	}}
	a := testApp(t, "", r)
	d := a.detect(context.Background(), Python)
	if !d.Installed || d.Healthy {
		t.Fatalf("incorrect status %+v", d)
	}
}

func TestFixedInstallLocationCanBeSkipped(t *testing.T) {
	a := testApp(t, "0\n", &fakeRunner{})
	a.Host.OS = "linux"
	p, err := a.planInstall(Git, Release{}, Detection{}, false)
	if err != nil {
		t.Fatal(err)
	}
	// Linux filesystem path semantics are exercised on real Linux; on Windows,
	// use an absolute fixture for the fixed-location UI test.
	p.InstallDir = filepath.Join(t.TempDir(), "fixed")
	if err = a.pickLocations(&p); !errors.Is(err, errCancelled) {
		t.Fatal(err)
	}
	if !p.Fixed {
		t.Fatal("system package should stay fixed")
	}
}

func TestChooseDirectoryRejectsRelativeAndCancels(t *testing.T) {
	a := testApp(t, "relative/path\n0\n", &fakeRunner{})
	if _, err := a.chooseDir("location", a.Home); !errors.Is(err, errCancelled) {
		t.Fatal(err)
	}
}

func TestKeepOldVersionNeverRunsInstaller(t *testing.T) {
	node := filepath.Join(t.TempDir(), "node")
	npm := filepath.Join(filepath.Dir(node), "npm")
	r := &fakeRunner{paths: map[string]string{"node": node, "npm": npm}, capture: func(c Command) (string, error) { return "v22.0.0", nil }, run: func(Command) error { t.Fatal("installer invoked for keep"); return nil }}
	a := testApp(t, "1\n", r)
	a.Host.OS = "linux"
	name := nodeFilename("v26.0.0", a.Host.OS, a.Host.Arch)
	a.Client = fixtureClient(map[string][]byte{"/dist/index.json": []byte(`[{"version":"v26.0.0","lts":"LTS"}]`), "/dist/v26.0.0/SHASUMS256.txt": []byte(strings.Repeat("a", 64) + "  " + name)})
	result := a.manage(context.Background(), Node)
	if result.Status != "保留" {
		t.Fatal(result)
	}
}

func TestFailedInstallNeverCreatesSuccessfulRecord(t *testing.T) {
	r := &fakeRunner{capture: func(c Command) (string, error) { return "v26.0.0", nil }, run: func(Command) error { return errors.New("simulated executable failure") }}
	a := testApp(t, "\n1\n", r)
	a.Host = Host{OS: "windows", Arch: "amd64"}
	archive := makeZIP(t, map[string]string{"node-v26.0.0-win-x64/node.exe": "fake", "node-v26.0.0-win-x64/node_modules/npm/bin/npm-cli.js": "fake"})
	name := "node-v26.0.0-win-x64.zip"
	a.Client = fixtureClient(map[string][]byte{"/dist/index.json": []byte(`[{"version":"v26.0.0","lts":"LTS"}]`), "/dist/v26.0.0/SHASUMS256.txt": []byte(digest(archive) + "  " + name), "/dist/v26.0.0/" + name: archive})
	result := a.manage(context.Background(), Node)
	if result.Status != "失败" {
		t.Fatal(result)
	}
	if _, ok := a.State.Records[Node]; ok {
		t.Fatal("recorded failed install as success")
	}
}

func TestWSLDecodingAndResumeMarkers(t *testing.T) {
	if got := ubuntuNames("Ubuntu\r\nUbuntu-24.04\r\ndocker-desktop\n"); len(got) != 2 {
		t.Fatal(got)
	}
	if !wslNeedsReboot("需要重新启动系统") || !wslNeedsReboot("exit 3010") {
		t.Fatal("missed reboot")
	}
	encoded := []byte{0xff, 0xfe, 'U', 0, 'b', 0, 'u', 0, 'n', 0, 't', 0, 'u', 0}
	if decodeOutput(encoded) != "Ubuntu" {
		t.Fatal("UTF16 decoding failed")
	}
}

func TestWSLRecordsActualDistributionDisk(t *testing.T) {
	r := &fakeRunner{paths: map[string]string{"powershell.exe": "powershell.exe"}, capture: func(c Command) (string, error) { return `[{"name":"Ubuntu","path":"\\\\?\\D:\\WSL\\Ubuntu"}]`, nil }}
	a := testApp(t, "", r)
	a.Host.OS = "windows"
	if got := a.ubuntuLocation(context.Background(), "Ubuntu"); got != `D:\WSL\Ubuntu` {
		t.Fatal(got)
	}
}

func TestFirstPartyReleaseClientRejectsEscape(t *testing.T) {
	c, _, err := releaseClient("https://downloads.example/oxgen/")
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"http://downloads.example/oxgen/file", "https://other.example/oxgen/file", "https://downloads.example/other/file", "https://downloads.example/oxgen/../private", "https://downloads.example/oxgen/%2e%2e/private"} {
		if err = c.HTTP.Transport.(*originTransport).check(raw); err == nil {
			t.Fatal("accepted foreign release URL")
		}
	}
	if _, _, err = releaseClient("https://user:secret@example.com/oxgen"); err == nil {
		t.Fatal("accepted embedded credentials")
	}
}

func TestCleanupCannotRemoveParent(t *testing.T) {
	root := t.TempDir()
	if err := removeTemp(root, root); err == nil {
		t.Fatal("accepted root deletion")
	}
	if err := removeTemp(root, filepath.Dir(root)); err == nil {
		t.Fatal("accepted parent deletion")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal("deleted root")
	}
}

func TestEntrypointsAndInstallerScriptsParse(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell parser only")
	}
	ps, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("PowerShell not available")
	}
	dir := t.TempDir()
	sources := map[string]string{"signature": signatureScript, "appx": appxScript, "path": pathScript, "elevate": elevateScript, "workbuddy": workBuddyNSIS}
	bootstrap, err := os.ReadFile(filepath.Join("..", "..", "scripts", "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	sources["bootstrap"] = string(bootstrap)
	for name, source := range sources {
		file := filepath.Join(dir, name+".ps1")
		os.WriteFile(file, append([]byte{0xef, 0xbb, 0xbf}, []byte(source)...), 0600)
		script := "$tokens=$null; $parseErrors=$null; [System.Management.Automation.Language.Parser]::ParseFile($args[0],[ref]$tokens,[ref]$parseErrors) | Out-Null; if ($parseErrors.Count) { $parseErrors | ForEach-Object { Write-Output $_.Message }; exit 1 }"
		// A fixed checker script accepts filenames as data, never interpolated code.
		checker := filepath.Join(dir, "check.ps1")
		os.WriteFile(checker, []byte(script), 0600)
		cmd := exec.Command(ps, "-NoProfile", "-File", checker, file)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v %s", name, err, out)
		}
	}
}

// Explicit opt-in: these tests only download/extract official runtimes underneath
// OXGEN_LIVE_DIR. They do not run a system installer or write the user's PATH.
func TestLiveOfficialRuntimes(t *testing.T) {
	root := os.Getenv("OXGEN_LIVE_DIR")
	if root == "" {
		t.Skip("set OXGEN_LIVE_DIR to run isolated official runtime checks")
	}
	if runtime.GOOS != "windows" {
		t.Skip("current live fixture is Windows")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if !filepath.IsAbs(root) {
		t.Fatal("live directory must be absolute")
	}
	r := OSRunner{Input: strings.NewReader("")}
	a := &App{Host: detectHost(), Client: newClient(), Runner: r, Home: root, State: State{Schema: 1, Records: map[Tool]Record{}}, UI: newUI(strings.NewReader(""), io.Discard, false)}
	if err := ensureWritable(root); err != nil {
		t.Fatal(err)
	}
	t.Run("official_metadata", func(t *testing.T) {
		release, err := a.gitLatest(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if release.Artifact.Signature != "Git for Windows" {
			t.Fatal("missing publisher verification")
		}
		t.Log("Git official release:", release.Version)
	})
	for _, tool := range []Tool{Node, Python} {
		t.Run(string(tool), func(t *testing.T) {
			var release Release
			var err error
			if tool == Node {
				release, err = a.nodeLatest(ctx)
			} else {
				release, err = a.pythonLatest(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			if tool == Python && os.Getenv("OXGEN_VERIFY_MANAGER") == "1" {
				manager, err := a.Client.Download(ctx, release.Manager, filepath.Join(root, "下载 cache"), os.Stdout)
				if err != nil {
					t.Fatal(err)
				}
				if err = a.verifySignature(ctx, manager, "Python Software Foundation", os.Stdout); err != nil {
					t.Fatal(err)
				}
			}
			file, err := a.Client.Download(ctx, release.Artifact, filepath.Join(root, "下载 cache"), os.Stdout)
			if err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(root, "运行时 space", string(tool), release.Version)
			if _, err = os.Stat(dest); os.IsNotExist(err) {
				if err = extractArchive(file, dest, tool == Node); err != nil {
					t.Fatal(err)
				}
			}
			exe := filepath.Join(dest, "node.exe")
			if tool == Python {
				exe = filepath.Join(dest, "python.exe")
				if err = a.prepareWindowsPython(ctx, exe, os.Stdout); err != nil {
					t.Fatal(err)
				}
				err = a.verifyPython(ctx, exe, filepath.Join(root, "验证 venv"), os.Stdout)
			} else {
				err = a.verifyNode(ctx, exe, os.Stdout)
			}
			if err != nil {
				t.Fatal(err)
			}
			version, err := r.Capture(ctx, Command{Name: exe, Args: []string{"--version"}})
			if err != nil {
				t.Fatal(err)
			}
			if compareVersion(version, release.Version) != 0 {
				t.Fatal("runtime version mismatch")
			}
			t.Logf("verified %s %s at %s", tool, release.Version, exe)
		})
	}
}
