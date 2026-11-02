package oxgen

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var versionPattern = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?`)
var stableVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func numericVersion(s string) string { return versionPattern.FindString(s) }

func toolVersion(tool Tool, text string) string {
	if tool == Git {
		return regexp.MustCompile(`\d+\.\d+\.\d+(?:\.windows\.\d+)?`).FindString(text)
	}
	return numericVersion(text)
}

func compareToolVersion(tool Tool, current, recommended string) int {
	if n := compareVersion(current, recommended); n != 0 || tool != Git {
		return n
	}
	patch := regexp.MustCompile(`\.windows\.(\d+)`)
	left, right := patch.FindStringSubmatch(current), patch.FindStringSubmatch(recommended)
	if len(left) == 2 && len(right) == 2 {
		x, _ := strconv.Atoi(left[1])
		y, _ := strconv.Atoi(right[1])
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}
func compareVersion(a, b string) int {
	parts := func(s string) []int {
		p := strings.Split(numericVersion(s), ".")
		n := make([]int, 3)
		for i := range p {
			if i < 3 {
				n[i], _ = strconv.Atoi(p[i])
			}
		}
		return n
	}
	x, y := parts(a), parts(b)
	for i := range x {
		if x[i] < y[i] {
			return -1
		}
		if x[i] > y[i] {
			return 1
		}
	}
	return 0
}

type nodeRelease struct {
	Version string   `json:"version"`
	LTS     any      `json:"lts"`
	Files   []string `json:"files"`
}

func nodeFilename(version, osName, arch string) string {
	if arch == "amd64" {
		arch = "x64"
	}
	ext := ".tar.gz"
	if osName == "windows" {
		osName = "win"
		ext = ".zip"
	}
	return "node-" + version + "-" + osName + "-" + arch + ext
}

func shaFromSums(sums, name string) (string, error) {
	for _, line := range strings.Split(sums, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && validSHA(f[0]) {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("官方校验清单没有 %s", name)
}

func (a *App) nodeLatest(ctx context.Context) (Release, error) {
	var releases []nodeRelease
	if err := a.Client.JSON(ctx, "https://nodejs.org/dist/index.json", &releases); err != nil {
		return Release{}, err
	}
	sort.Slice(releases, func(i, j int) bool { return compareVersion(releases[i].Version, releases[j].Version) > 0 })
	for _, r := range releases {
		if _, ok := r.LTS.(string); !ok || !stableVersion.MatchString(strings.TrimPrefix(r.Version, "v")) {
			continue
		}
		name := nodeFilename(r.Version, a.Host.OS, a.Host.Arch)
		sums, err := a.Client.Text(ctx, "https://nodejs.org/dist/"+r.Version+"/SHASUMS256.txt")
		if err != nil {
			return Release{}, err
		}
		sum, err := shaFromSums(sums, name)
		if err != nil {
			return Release{}, err
		}
		return Release{Version: strings.TrimPrefix(r.Version, "v"), Channel: "Node.js 官方 LTS", Artifact: Artifact{URL: "https://nodejs.org/dist/" + r.Version + "/" + name, SHA256: sum}}, nil
	}
	return Release{}, fmt.Errorf("没有找到适合本机的官方 LTS 版本")
}

type pythonVersion struct {
	ID      string `json:"id"`
	Version string `json:"sort-version"`
	Tag     string `json:"tag"`
	Company string `json:"company"`
	URL     string `json:"url"`
	Hash    struct {
		SHA256 string `json:"sha256"`
	} `json:"hash"`
}

func (a *App) pythonLatest(ctx context.Context) (Release, error) {
	var index struct {
		Versions []pythonVersion `json:"versions"`
	}
	if err := a.Client.JSON(ctx, "https://www.python.org/ftp/python/index-windows.json", &index); err != nil {
		return Release{}, err
	}
	sort.Slice(index.Versions, func(i, j int) bool { return compareVersion(index.Versions[i].Version, index.Versions[j].Version) > 0 })
	for _, v := range index.Versions {
		if v.Company != "PythonCore" || !strings.HasPrefix(v.ID, "pythoncore-") || !stableVersion.MatchString(v.Version) {
			continue
		}
		if a.Host.Arch == "amd64" && !strings.HasSuffix(v.Tag, "-64") {
			continue
		}
		if a.Host.Arch == "arm64" && !strings.HasSuffix(v.Tag, "-arm64") {
			continue
		}
		r := Release{Version: v.Version, Tag: v.Tag, Channel: "Python 官方稳定版", Artifact: Artifact{URL: v.URL, SHA256: v.Hash.SHA256}}
		if a.Host.OS == "windows" {
			page, err := a.Client.Text(ctx, "https://www.python.org/downloads/windows/")
			if err != nil {
				return r, err
			}
			manager := regexp.MustCompile(`https://www\.python\.org/ftp/python/pymanager/python-manager-[0-9.]+\.msix`).FindString(page)
			if manager == "" {
				return r, fmt.Errorf("未找到 Python 官方安装管理器")
			}
			r.Manager = Artifact{URL: manager, Signature: "Python Software Foundation"}
			return r, nil
		}
		pageURL := "https://www.python.org/downloads/release/python-" + strings.ReplaceAll(v.Version, ".", "") + "/"
		page, err := a.Client.Text(ctx, pageURL)
		if err != nil {
			return r, err
		}
		pkg := regexp.MustCompile(`https://www\.python\.org/ftp/python/` + regexp.QuoteMeta(v.Version) + `/python-[^"<>\s]+-macos[^"<>\s]+\.pkg`).FindString(page)
		if pkg == "" {
			return r, fmt.Errorf("没有找到该稳定版的 macOS 官方安装包")
		}
		r.Artifact = Artifact{URL: html.UnescapeString(pkg), Signature: "Python Software Foundation"}
		return r, nil
	}
	return Release{}, fmt.Errorf("没有找到适合本机架构的完整 Python 稳定运行时")
}

func (a *App) gitLatest(ctx context.Context) (Release, error) {
	if a.Host.OS == "darwin" {
		return Release{Channel: "Apple 命令行开发工具（版本由 Apple 提供）"}, nil
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"assets"`
	}
	if err := a.Client.JSON(ctx, "https://api.github.com/repos/git-for-windows/git/releases/latest", &release); err != nil {
		return Release{}, err
	}
	if release.Prerelease || !regexp.MustCompile(`^v?\d+\.\d+\.\d+\.windows\.\d+$`).MatchString(release.Tag) {
		return Release{}, fmt.Errorf("官方返回的不是稳定版本")
	}
	suffix := "-64-bit.exe"
	if a.Host.Arch == "arm64" {
		suffix = "-arm64.exe"
	}
	for _, asset := range release.Assets {
		if !strings.HasPrefix(asset.Name, "Git-") || !strings.HasSuffix(asset.Name, suffix) {
			continue
		}
		sum := strings.TrimPrefix(asset.Digest, "sha256:")
		if !validSHA(sum) {
			sum = ""
		}
		return Release{Version: strings.TrimPrefix(release.Tag, "v"), Channel: "Git for Windows 官方稳定版", Artifact: Artifact{URL: asset.URL, SHA256: sum, Size: asset.Size, Signature: "Git for Windows"}}, nil
	}
	return Release{}, fmt.Errorf("没有找到该架构的 Git 官方安装器")
}

func (a *App) latest(ctx context.Context, tool Tool) (Release, error) {
	if isAgent(tool) {
		return a.agentLatest(ctx, tool)
	}
	if a.Host.OS == "linux" && systemPackage(tool) {
		return a.aptCandidate(ctx, tool)
	}
	switch tool {
	case VSCode:
		return a.vscodeLatest(ctx)
	case Node:
		return a.nodeLatest(ctx)
	case Python:
		return a.pythonLatest(ctx)
	case Git:
		return a.gitLatest(ctx)
	}
	return Release{}, fmt.Errorf("没有该组件的版本来源")
}
