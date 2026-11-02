package oxgen

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const npmRegistry = "https://registry.npmjs.org"
const agentSource = "OXGEN Agent 官方发行包"
const npmAgentSource = "OXGEN Agent 官方 npm 包"
const workBuddySource = "OXGEN WorkBuddy 官方安装包"

var agentVersion = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[A-Za-z0-9]+(?:[.-][A-Za-z0-9]+)*)?$`)

func compareAgentVersion(current, recommended string) int {
	if cmp := compareVersion(current, recommended); cmp != 0 {
		return cmp
	}
	_, a, _ := strings.Cut(current, "-")
	_, b, _ := strings.Cut(recommended, "-")
	if a == b {
		return 0
	}
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] == bs[i] {
			continue
		}
		n, e := strconv.ParseUint(as[i], 10, 64)
		m, f := strconv.ParseUint(bs[i], 10, 64)
		if e == nil && f == nil {
			if n < m {
				return -1
			}
			return 1
		}
		if e == nil {
			return -1
		}
		if f == nil {
			return 1
		}
		return strings.Compare(as[i], bs[i])
	}
	if len(as) < len(bs) {
		return -1
	}
	return 1
}

func agentPackage(tool Tool) string {
	return map[Tool]string{Codex: "@openai/codex", Pi: "@earendil-works/pi-coding-agent", DeepSeek: "@deepseek-ai/dsh", OpenCode: "@opencode/cli"}[tool]
}

func agentCommand(tool Tool) string {
	if tool == DeepSeek {
		return "dsh"
	}
	return string(tool)
}

func agentPlatform(h Host) string {
	osName, arch := h.OS, h.Arch
	if osName == "windows" {
		osName = "win32"
	}
	if arch == "amd64" {
		arch = "x64"
	}
	return osName + "-" + arch
}

type npmMetadata struct {
	Name     string            `json:"name"`
	Version  string            `json:"version"`
	Optional map[string]string `json:"optionalDependencies"`
	Engines  struct {
		Node string `json:"node"`
	} `json:"engines"`
	Dist struct {
		URL       string `json:"tarball"`
		Integrity string `json:"integrity"`
	} `json:"dist"`
}

func npmArtifact(m npmMetadata) (Artifact, error) {
	u, err := url.Parse(m.Dist.URL)
	if err != nil || u.Scheme != "https" || u.Host != "registry.npmjs.org" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasSuffix(u.Path, ".tgz") {
		return Artifact{}, fmt.Errorf("官方 npm 元数据中的发行包地址无效")
	}
	encoded, ok := strings.CutPrefix(m.Dist.Integrity, "sha512-")
	sum, err := base64.StdEncoding.DecodeString(encoded)
	if !ok || err != nil || len(sum) != 64 {
		return Artifact{}, fmt.Errorf("官方 npm 包缺少有效 SHA512")
	}
	return Artifact{URL: m.Dist.URL, SHA512: hex.EncodeToString(sum)}, nil
}

func (a *App) npmMetadata(ctx context.Context, name, version string) (npmMetadata, error) {
	var m npmMetadata
	err := a.Client.JSON(ctx, npmRegistry+"/"+name+"/"+version, &m)
	if err != nil {
		return m, err
	}
	if m.Name != name || !agentVersion.MatchString(m.Version) || version != "latest" && m.Version != version {
		return m, fmt.Errorf("npm 包名称或版本与官方请求不一致")
	}
	if _, err = npmArtifact(m); err != nil {
		return m, err
	}
	return m, nil
}

func (a *App) agentLatest(ctx context.Context, tool Tool) (Release, error) {
	if tool == WorkBuddy {
		return a.workBuddyLatest(ctx)
	}
	if tool == Codex {
		// This is also the preferred channel of OpenAI's current installer.
		// The official npm distribution remains a fallback if it is unavailable.
		if r, err := a.codexNativeLatest(ctx); err == nil {
			return r, nil
		}
		if ctx.Err() != nil {
			return Release{}, ctx.Err()
		}
	}
	if tool == Claude {
		const base = "https://downloads.claude.ai/claude-code-releases"
		version, err := a.Client.Text(ctx, base+"/latest")
		version = strings.TrimSpace(version)
		if err != nil {
			return Release{}, err
		}
		if !stableVersion.MatchString(version) {
			return Release{}, fmt.Errorf("Claude 官方版本响应无效")
		}
		var manifest struct {
			Version   string `json:"version"`
			Platforms map[string]struct {
				Checksum string `json:"checksum"`
				Size     int64  `json:"size"`
			} `json:"platforms"`
		}
		if err = a.Client.JSON(ctx, base+"/"+version+"/manifest.json", &manifest); err != nil {
			return Release{}, err
		}
		item, ok := manifest.Platforms[agentPlatform(a.Host)]
		if !ok || !validSHA(item.Checksum) || manifest.Version != "" && manifest.Version != version {
			return Release{}, fmt.Errorf("Claude 官方清单缺少此系统/架构的有效校验值")
		}
		name := "claude"
		artifact := Artifact{SHA256: item.Checksum, Size: item.Size}
		if a.Host.OS == "windows" {
			name += ".exe"
			artifact.Signature = "Anthropic"
		}
		artifact.URL = base + "/" + version + "/" + agentPlatform(a.Host) + "/" + name
		return Release{Version: version, Channel: "Anthropic 官方 latest 原生版", Artifact: artifact}, nil
	}
	name := agentPackage(tool)
	if name == "" {
		return Release{}, fmt.Errorf("没有该 Agent 的官方包配置")
	}
	m, err := a.npmMetadata(ctx, name, "latest")
	if err != nil {
		return Release{}, err
	}
	r := Release{Version: m.Version, Channel: "官方 npm latest", Package: name, Integrity: m.Dist.Integrity}
	if tool == DeepSeek {
		r.Channel += "（官方开发预览版，可能包含不兼容更新）"
	}
	if tool == Codex || tool == OpenCode {
		platform := agentPlatform(a.Host)
		if tool == OpenCode {
			platform = strings.ReplaceAll(platform, "win32", "windows")
			// The baseline build also runs on older x64 CPUs without AVX2.
			if a.Host.Arch == "amd64" {
				platform += "-baseline"
			}
		}
		platformName := name + "-" + platform
		spec, ok := m.Optional[platformName]
		if !ok {
			return Release{}, fmt.Errorf("官方未发布 %s 的 %s 原生程序", tool.String(), platform)
		}
		platformVersion := spec
		if tool == Codex {
			prefix := "npm:" + name + "@"
			if !strings.HasPrefix(spec, prefix) {
				return Release{}, fmt.Errorf("Codex 原生包引用格式发生变化，请更新 OXGEN")
			}
			platformName, platformVersion = name, strings.TrimPrefix(spec, prefix)
		}
		if !agentVersion.MatchString(platformVersion) {
			return Release{}, fmt.Errorf("官方原生程序未固定版本")
		}
		payload, e := a.npmMetadata(ctx, platformName, platformVersion)
		if e != nil {
			return Release{}, e
		}
		r.Artifact, err = npmArtifact(payload)
		r.Channel = "官方 npm 原生发行包（无需 Node.js）"
		return r, err
	}
	r.Artifact, err = npmArtifact(m)
	return r, err
}

func (a *App) codexNativeLatest(ctx context.Context) (Release, error) {
	var m struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
			URL    string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := a.Client.JSON(ctx, "https://releases.openai.com/codex/channels/latest", &m); err != nil {
		return Release{}, err
	}
	version := strings.TrimPrefix(m.Tag, "rust-v")
	if !stableVersion.MatchString(version) {
		return Release{}, fmt.Errorf("OpenAI 官方版本清单不是稳定版")
	}
	name := "codex-npm-" + agentPlatform(a.Host) + "-" + version + ".tgz"
	for _, item := range m.Assets {
		if item.Name != name {
			continue
		}
		expected := "https://releases.openai.com/codex/releases/" + version + "/" + name
		sum := strings.TrimPrefix(item.Digest, "sha256:")
		if item.URL != expected || !validSHA(sum) {
			return Release{}, fmt.Errorf("OpenAI 原生包地址或 SHA256 无效")
		}
		return Release{Version: version, Channel: "OpenAI 官方原生发行包（无需 Node.js）", Artifact: Artifact{URL: item.URL, SHA256: sum}}, nil
	}
	return Release{}, fmt.Errorf("OpenAI 暂未发布此系统/架构的原生包")
}

func (a *App) workBuddyLatest(ctx context.Context) (Release, error) {
	if a.Host.OS != "windows" && a.Host.OS != "darwin" {
		return Release{}, fmt.Errorf("WorkBuddy 官方未提供 Ubuntu/Debian 桌面安装包；不使用第三方移植版")
	}
	page, err := a.Client.Text(ctx, "https://www.codebuddy.cn/docs/workbuddy/Download-History")
	if err != nil {
		return Release{}, err
	}
	platform := "win32-x64-user"
	if a.Host.OS == "darwin" {
		platform = agentPlatform(a.Host)
	}
	ext := "exe"
	if a.Host.OS == "darwin" {
		ext = "dmg"
	}
	pattern := regexp.MustCompile(`https://(?:acc-1258344699\.cos\.ap-guangzhou\.myqcloud\.com|download\.codebuddy\.cn)/[^"<>\s]*WorkBuddy-` + regexp.QuoteMeta(platform) + `-(\d+\.\d+\.\d+)(?:\.[0-9]+)?-[A-Za-z0-9]+\.` + ext)
	var latest Release
	for _, match := range pattern.FindAllStringSubmatch(html.UnescapeString(page), -1) {
		if latest.Version == "" || compareVersion(match[1], latest.Version) > 0 {
			latest = Release{Version: match[1], Channel: "腾讯 WorkBuddy 官方正式版（桌面应用）", Artifact: Artifact{URL: match[0], Signature: "Tencent WorkBuddy"}}
		}
	}
	if latest.Version == "" {
		return Release{}, fmt.Errorf("官方页面没有可校验的 WorkBuddy 发行包，请重试或更新 OXGEN")
	}
	if a.Host.OS == "windows" && a.Host.Arch == "arm64" {
		latest.Channel += " · 官方 x64 兼容 ARM64 包"
	}
	return latest, nil
}
