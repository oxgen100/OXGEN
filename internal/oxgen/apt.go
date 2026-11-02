package oxgen

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func aptPackage(tool Tool) string {
	if tool == Git {
		return "git"
	}
	return "python3"
}

func officialAPTURL(raw, distro string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if distro == "ubuntu" {
		return h == "archive.ubuntu.com" || h == "security.ubuntu.com" || h == "ports.ubuntu.com"
	}
	return h == "deb.debian.org" || h == "security.debian.org" || h == "ftp.debian.org"
}

func aptVersionFromMadison(text, distro string) string {
	best := ""
	for _, line := range strings.Split(text, "\n") {
		p := strings.Split(line, "|")
		if len(p) != 3 {
			continue
		}
		origin := strings.Fields(p[2])
		if len(origin) == 0 || !officialAPTURL(origin[0], distro) {
			continue
		}
		version := strings.TrimSpace(p[1])
		if numericVersion(version) == "" {
			continue
		}
		if best == "" || compareVersion(version, best) > 0 {
			best = version
		}
	}
	return best
}

func (a *App) aptCandidate(ctx context.Context, tool Tool) (Release, error) {
	out, err := a.Runner.Capture(ctx, Command{Name: "apt-cache", Args: []string{"madison", aptPackage(tool)}})
	if err != nil {
		return Release{}, err
	}
	v := aptVersionFromMadison(out, a.Host.Distro)
	if v == "" {
		return Release{}, fmt.Errorf("本机没有可核实的官方仓库索引；可选择安装时刷新官方索引")
	}
	return Release{Version: v, Channel: a.Host.Distro + " 官方仓库本机缓存候选（可能过期，安装时刷新；不代表官网上游最新版）"}, nil
}

func aptSources(h Host) (string, error) {
	if !regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(h.Codename) {
		return "", fmt.Errorf("无法识别发行版代号，未修改系统软件源")
	}
	key := "/usr/share/keyrings/debian-archive-keyring.gpg"
	if h.Distro == "debian" {
		return fmt.Sprintf("deb [signed-by=%s] https://deb.debian.org/debian %s main\ndeb [signed-by=%s] https://deb.debian.org/debian %s-updates main\ndeb [signed-by=%s] https://security.debian.org/debian-security %s-security main\n", key, h.Codename, key, h.Codename, key, h.Codename), nil
	}
	if h.Distro != "ubuntu" {
		return "", fmt.Errorf("不支持该发行版")
	}
	key = "/usr/share/keyrings/ubuntu-archive-keyring.gpg"
	server := "https://archive.ubuntu.com/ubuntu"
	security := "https://security.ubuntu.com/ubuntu"
	if h.Arch == "arm64" {
		server = "https://ports.ubuntu.com/ubuntu-ports"
		security = server
	}
	return fmt.Sprintf("deb [signed-by=%s] %s %s main universe\ndeb [signed-by=%s] %s %s-updates main universe\ndeb [signed-by=%s] %s %s-security main universe\n", key, server, h.Codename, key, server, h.Codename, key, security, h.Codename), nil
}

func (a *App) installAPT(ctx context.Context, p InstallPlan, out io.Writer) (Record, error) {
	sources, err := aptSources(a.Host)
	if err != nil {
		return Record{}, err
	}
	key := "/usr/share/keyrings/debian-archive-keyring.gpg"
	if a.Host.Distro == "ubuntu" {
		key = "/usr/share/keyrings/ubuntu-archive-keyring.gpg"
	}
	if _, err = os.Stat(key); err != nil {
		return Record{}, fmt.Errorf("缺少发行版官方签名密钥，未禁用签名验证: %w", err)
	}
	cache := filepath.Join(p.DownloadDir, "apt", string(p.Tool))
	for _, dir := range []string{cache, filepath.Join(cache, "lists", "partial"), filepath.Join(cache, "archives", "partial")} {
		if err = ensureWritable(dir); err != nil {
			return Record{}, err
		}
	}
	sourceFile := filepath.Join(cache, "official.sources.list")
	if err = os.WriteFile(sourceFile, []byte(sources), 0644); err != nil {
		return Record{}, err
	}
	opts := []string{"-o", "Dir::Etc::sourcelist=" + sourceFile, "-o", "Dir::Etc::sourceparts=-", "-o", "Dir::State::lists=" + filepath.Join(cache, "lists"), "-o", "Dir::Cache::archives=" + filepath.Join(cache, "archives"), "-o", "APT::Get::List-Cleanup=0"}
	fmt.Fprintln(out, "仅刷新本次安装使用的发行版官方索引，不更改系统已有软件源。")
	if err = a.rootRun(ctx, Command{Name: "apt-get", Args: append(append([]string{}, opts...), "update"), Timeout: 10 * time.Minute}, out); err != nil {
		return Record{}, err
	}
	policy, err := a.Runner.Capture(ctx, Command{Name: "apt-cache", Args: append(append([]string{}, opts...), "policy", aptPackage(p.Tool))})
	if err != nil {
		return Record{}, err
	}
	match := regexp.MustCompile(`(?m)^\s*Candidate:\s*(\S+)`).FindStringSubmatch(policy)
	if len(match) < 2 || match[1] == "(none)" {
		return Record{}, fmt.Errorf("官方仓库没有可安装候选版本")
	}
	fmt.Fprintf(out, "官方仓库实际候选版本：%s\n", match[1])
	if !p.Automatic && !a.UI.Confirm("使用以上官方仓库候选版本安装／补齐组件？") {
		return Record{}, errCancelled
	}
	packages := []string{"git"}
	if p.Tool == Python {
		packages = []string{"python3", "python3-pip", "python3-venv"}
	}
	args := append(append([]string{}, opts...), "install", "--no-install-recommends", "-y")
	args = append(args, packages...)
	if err = a.rootRun(ctx, Command{Name: "apt-get", Args: args}, out); err != nil {
		return Record{}, err
	}
	exe := "/usr/bin/git"
	if p.Tool == Python {
		exe = "/usr/bin/python3"
		if err = a.verifyPython(ctx, exe, p.DownloadDir, out); err != nil {
			return Record{}, err
		}
	}
	v, err := a.Runner.Capture(ctx, Command{Name: exe, Args: []string{"--version"}})
	if err != nil {
		return Record{}, err
	}
	return Record{Version: numericVersion(v), Path: exe, Root: "/usr", Source: "系统软件包", UpdatedAt: time.Now()}, nil
}
