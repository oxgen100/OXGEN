package oxgen

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const vscodeSource = "OXGEN VS Code 官方发行包"

func vscodePlatform(host Host) (string, error) {
	arch := host.Arch
	if arch == "amd64" {
		arch = "x64"
	}
	if arch != "x64" && arch != "arm64" {
		return "", fmt.Errorf("VS Code 暂不支持此架构")
	}
	switch host.OS {
	case "windows":
		return "win32-" + arch + "-archive", nil
	case "linux":
		return "linux-" + arch, nil
	case "darwin":
		if arch == "x64" {
			return "darwin", nil
		}
		return "darwin-arm64", nil
	}
	return "", fmt.Errorf("VS Code 暂不支持此系统")
}

func (a *App) vscodeLatest(ctx context.Context) (Release, error) {
	platform, err := vscodePlatform(a.Host)
	if err != nil {
		return Release{}, err
	}
	var metadata struct {
		URL     string `json:"url"`
		Name    string `json:"name"`
		Version string `json:"version"`
		SHA256  string `json:"sha256hash"`
	}
	if err = a.Client.JSON(ctx, "https://update.code.visualstudio.com/api/update/"+platform+"/stable/latest", &metadata); err != nil {
		return Release{}, err
	}
	if !stableVersion.MatchString(metadata.Name) || !validSHA(metadata.SHA256) || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(metadata.Version) {
		return Release{}, fmt.Errorf("微软稳定版信息缺少有效版本、提交编号或 SHA256")
	}
	u, err := url.Parse(metadata.URL)
	if err != nil {
		return Release{}, err
	}
	if err = officialURL(metadata.URL); err != nil {
		return Release{}, err
	}
	if u.Hostname() != "vscode.download.prss.microsoft.com" && u.Hostname() != "update.code.visualstudio.com" {
		return Release{}, fmt.Errorf("VS Code 安装包不在微软官方发布渠道")
	}
	ext := ".zip"
	if a.Host.OS == "linux" {
		ext = ".tar.gz"
	}
	if !strings.HasSuffix(u.Path, ext) || u.Fragment != "" {
		return Release{}, fmt.Errorf("微软返回的不是完整 VS Code 桌面发行包")
	}
	return Release{Version: metadata.Name, Tag: metadata.Version, Channel: "Microsoft VS Code 官方稳定版（完整桌面发行包）", Artifact: Artifact{URL: metadata.URL, SHA256: strings.ToLower(metadata.SHA256)}}, nil
}

// Root is the actual application directory: on macOS it is the .app itself.
func vscodeLayout(root, osName string) (exe, cli, bin string) {
	if osName == "darwin" {
		app := filepath.Join(root, "Contents", "Resources", "app")
		return filepath.Join(root, "Contents", "MacOS", "Electron"), filepath.Join(app, "out", "cli.js"), filepath.Join(app, "bin")
	}
	name := "code"
	if osName == "windows" {
		name = "Code.exe"
	}
	app := filepath.Join(root, "resources", "app")
	if osName == "windows" {
		// Fast-update Windows installations keep resources in a commit directory.
		// Read the shipped launcher instead of guessing between old/new payloads.
		if launcher, err := os.ReadFile(filepath.Join(root, "bin", "code.cmd")); err == nil && len(launcher) < 64*1024 {
			match := regexp.MustCompile(`(?i)%~dp0[\\/]?\.\.[\\/]([0-9a-f]{10,40})[\\/]resources[\\/]app[\\/]out[\\/]cli\.js`).FindSubmatch(launcher)
			if len(match) == 2 {
				app = filepath.Join(root, string(match[1]), "resources", "app")
			}
		}
	}
	return filepath.Join(root, name), filepath.Join(app, "out", "cli.js"), filepath.Join(root, "bin")
}

func vscodeRootFromPath(path, osName string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if osName == "darwin" {
		for dir := filepath.Dir(path); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
			if strings.HasSuffix(dir, ".app") {
				return dir
			}
		}
	}
	root := filepath.Dir(path)
	if strings.EqualFold(filepath.Base(root), "bin") {
		root = filepath.Dir(root)
	}
	return root
}

func vscodeCommand(root, osName string) Command {
	exe, cli, _ := vscodeLayout(root, osName)
	// This is how Microsoft's code.cmd/code wrapper invokes the CLI. Executing
	// Code.exe --version directly can start the GUI on Windows instead.
	return Command{Name: exe, Args: []string{cli, "--version"}, Env: []string{"ELECTRON_RUN_AS_NODE=1", "VSCODE_DEV="}}
}

func parseVSCodeVersion(text string) (version, commit, arch string) {
	lines := strings.Fields(text)
	if len(lines) >= 3 && stableVersion.MatchString(lines[0]) && regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(lines[1]) && (lines[2] == "x64" || lines[2] == "arm64") {
		return lines[0], lines[1], lines[2]
	}
	return "", "", ""
}

func (a *App) detectVSCode(ctx context.Context) Detection {
	type candidate struct {
		root   string
		active bool
	}
	var candidates []candidate
	if p, err := a.Runner.LookPath("code"); err == nil {
		candidates = append(candidates, candidate{vscodeRootFromPath(p, a.Host.OS), true})
	}
	if r, ok := a.State.Records[VSCode]; ok && r.Path != "" {
		candidates = append(candidates, candidate{root: vscodeRootFromPath(r.Path, a.Host.OS)})
	}
	home, _ := os.UserHomeDir()
	switch a.Host.OS {
	case "windows":
		for _, entry := range []struct{ key, suffix string }{{"LOCALAPPDATA", "Programs"}, {"ProgramFiles", ""}, {"ProgramFiles(x86)", ""}} {
			if base := os.Getenv(entry.key); base != "" {
				candidates = append(candidates, candidate{root: filepath.Join(base, entry.suffix, "Microsoft VS Code")})
			}
		}
	case "darwin":
		candidates = append(candidates, candidate{root: "/Applications/Visual Studio Code.app"}, candidate{root: filepath.Join(home, "Applications", "Visual Studio Code.app")})
	case "linux":
		for _, root := range []string{"/usr/share/code", "/usr/lib/code", "/opt/visual-studio-code", "/snap/code/current/usr/share/code"} {
			candidates = append(candidates, candidate{root: root})
		}
	}
	d := Detection{Tool: VSCode}
	seen := map[string]bool{}
	for _, c := range candidates {
		if seen[c.root] {
			continue
		}
		seen[c.root] = true
		exe, cli, bin := vscodeLayout(c.root, a.Host.OS)
		if _, err := os.Stat(exe); err != nil {
			continue
		}
		if _, err := os.Stat(cli); err != nil {
			continue // Standalone tunnel/CLI downloads are not the desktop editor.
		}
		text, err := a.Runner.Capture(ctx, vscodeCommand(c.root, a.Host.OS))
		version, _, arch := parseVSCodeVersion(text)
		if err != nil || version == "" {
			d.Details = append(d.Details, "VS Code 命令验证失败："+exe+" · "+strings.TrimSpace(text))
			packagePath := filepath.Join(filepath.Dir(filepath.Dir(cli)), "package.json")
			var pkg struct {
				Version string `json:"version"`
			}
			data, _ := os.ReadFile(packagePath)
			if json.Unmarshal(data, &pkg) != nil || !stableVersion.MatchString(pkg.Version) {
				continue
			}
			d.Installed, d.Active = true, c.active
			d.Version, d.Path, d.Source = pkg.Version, exe, "现有 VS Code 安装（命令尚不可用）"
			return d
		}
		d.Installed, d.Healthy, d.Active = true, true, c.active
		d.Version, d.Path, d.Source = version, exe, "现有 VS Code 安装（保留原安装方式）"
		if record, ok := a.State.Records[VSCode]; ok && samePath(record.Path, exe) {
			d.Source = record.Source
		}
		d.Details = append(d.Details, "架构："+arch+"；code 命令目录："+bin)
		if !c.active {
			d.Details = append(d.Details, "已找到程序，但当前 PATH 未指向该安装的 code 命令。")
		}
		return d
	}
	return d
}

func vscodeLocationNote(osName string) string {
	home, _ := os.UserHomeDir()
	switch osName {
	case "windows":
		config := os.Getenv("APPDATA")
		if config == "" {
			config = filepath.Join(home, "AppData", "Roaming")
		}
		return "使用微软官方完整 ZIP，程序放入所选目录；编辑器配置通常写入 " + filepath.Join(config, "Code") + "，扩展通常写入 " + filepath.Join(home, ".vscode", "extensions") + "。ZIP 版由 OXGEN 按需更新，不自动创建开始菜单或文件关联。已有设置由 VS Code 自身管理。"
	case "darwin":
		return "使用微软官方应用 ZIP，将 Visual Studio Code.app 放入所选目录；设置通常位于 " + filepath.Join(home, "Library", "Application Support", "Code") + "，扩展位于 " + filepath.Join(home, ".vscode", "extensions") + "。保留系统 Gatekeeper 检查；不自动移动原应用或配置。"
	default:
		config := os.Getenv("XDG_CONFIG_HOME")
		if config == "" {
			config = filepath.Join(home, ".config")
		}
		return "使用微软官方完整 tar.gz，程序放入所选目录；设置通常位于 " + filepath.Join(config, "Code") + "，扩展位于 " + filepath.Join(home, ".vscode", "extensions") + "。桌面使用需要图形会话及微软文档所列的系统库；不自动修改系统沙箱设置。"
	}
}

func runningInWSL() bool {
	b, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	return strings.Contains(strings.ToLower(string(b)), "microsoft")
}

func (a *App) installVSCode(ctx context.Context, p InstallPlan, out io.Writer) (Record, error) {
	file, err := a.Client.Download(ctx, p.Release.Artifact, p.DownloadDir, out)
	if err != nil {
		return Record{}, err
	}
	if err = prepareVersionedTarget(p); err != nil {
		return Record{}, err
	}
	fmt.Fprintln(out, "正在解压完整 VS Code 至：", p.InstallDir)
	if err = extractArchiveOptions(ctx, file, p.InstallDir, a.Host.OS == "linux", a.Host.OS == "darwin"); err != nil {
		return Record{}, err
	}
	root := p.InstallDir
	if a.Host.OS == "darwin" {
		root = filepath.Join(root, "Visual Studio Code.app")
		if err = a.Runner.Run(ctx, Command{Name: "/usr/bin/codesign", Args: []string{"--verify", "--deep", "--strict", root}}, out); err != nil {
			return Record{}, fmt.Errorf("VS Code 应用签名验证失败: %w", err)
		}
		if err = a.Runner.Run(ctx, Command{Name: "/usr/sbin/spctl", Args: []string{"--assess", "--type", "execute", root}}, out); err != nil {
			return Record{}, &pendingError{"VS Code 已解压，但需通过 macOS 系统安全检查；请按系统提示处理后重新检测。"}
		}
	}
	exe, cli, bin := vscodeLayout(root, a.Host.OS)
	codeName := "code"
	if a.Host.OS == "windows" {
		codeName = "code.cmd"
	}
	for _, path := range []string{exe, cli, filepath.Join(bin, codeName)} {
		if info, e := os.Stat(path); e != nil || !info.Mode().IsRegular() {
			return Record{}, fmt.Errorf("VS Code 完整桌面包缺少文件: %s", path)
		}
	}
	text, err := a.Runner.Capture(ctx, vscodeCommand(root, a.Host.OS))
	if err != nil {
		return Record{}, fmt.Errorf("VS Code CLI 验证失败（请检查官方系统要求）: %s %w", strings.TrimSpace(text), err)
	}
	version, commit, arch := parseVSCodeVersion(text)
	expectedArch := a.Host.Arch
	if expectedArch == "amd64" {
		expectedArch = "x64"
	}
	if version != p.Release.Version || commit != p.Release.Tag || arch != expectedArch {
		return Record{}, fmt.Errorf("VS Code 版本、提交或架构验证不一致: %s", strings.TrimSpace(text))
	}
	fmt.Fprintln(out, "VS Code 命令验证通过：", strings.TrimSpace(text))
	fmt.Fprintln(out, "编辑器程序：", exe, "；命令目录：", bin)
	fmt.Fprintln(out, "本次验证 code --version；图形界面请在桌面会话中打开确认。")
	return Record{Version: version, Path: exe, Root: p.Root, Source: vscodeSource, UpdatedAt: time.Now()}, nil
}
