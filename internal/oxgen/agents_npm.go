package oxgen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type agentLockedWriter struct {
	sync.Mutex
	out io.Writer
}

func (w *agentLockedWriter) Write(p []byte) (int, error) {
	w.Lock()
	defer w.Unlock()
	return w.out.Write(p)
}

func (a *App) runAgentNPM(ctx context.Context, command Command, out io.Writer) error {
	w := &agentLockedWriter{out: out}
	done := make(chan error, 1)
	go func() { done <- a.Runner.Run(ctx, command, w) }()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	start := time.Now()
	for {
		select {
		case err := <-done:
			return err
		case <-ticker.C:
			fmt.Fprintf(w, "\nnpm 正在处理官方依赖（已用时 %s）；输入 0 并回车可取消后续步骤。\n", time.Since(start).Round(time.Second))
		}
	}
}

func windowsNpmAgentLauncher(node, relative, bash string) string {
	// Decode literal Node/Git paths as UTF-8 even in a fresh Chinese CMD using
	// code page 936. Restore its original console code page when the agent exits.
	body := "@echo off\r\nsetlocal DisableDelayedExpansion\r\nfor /f \"tokens=2 delims=:\" %%c in ('chcp') do set \"OXGEN_SAVED_CP=%%c\"\r\nchcp 65001 >nul\r\n"
	if bash != "" {
		body += "set \"PATH=" + strings.ReplaceAll(filepath.Dir(bash), "%", "%%") + ";%PATH%\"\r\n"
	}
	body += "\"" + strings.ReplaceAll(node, "%", "%%") + "\" \"%~dp0" + relative + "\" %*\r\nset \"OXGEN_EXIT_CODE=%errorlevel%\"\r\nif defined OXGEN_SAVED_CP chcp %OXGEN_SAVED_CP% >nul\r\nexit /b %OXGEN_EXIT_CODE%\r\n"
	return body
}

// Resolve the dependency graph first, validate its origin and the selected
// package's integrity, then let npm verify every tarball against the lockfile.
// Lifecycle scripts stay disabled; these two agents ship ready-to-run JS.
func validateAgentLock(file string, release Release) error {
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var lock struct {
		Version  int `json:"lockfileVersion"`
		Packages map[string]struct {
			Version   string `json:"version"`
			Resolved  string `json:"resolved"`
			Integrity string `json:"integrity"`
			Link      bool   `json:"link"`
		} `json:"packages"`
	}
	if err = json.Unmarshal(b, &lock); err != nil {
		return err
	}
	if lock.Version < 2 {
		return fmt.Errorf("需要支持 lockfile v2/v3 的 npm")
	}
	main, ok := lock.Packages["node_modules/"+release.Package]
	if !ok || main.Version != release.Version || main.Integrity != release.Integrity {
		return fmt.Errorf("锁文件中的 Agent 版本/校验值与所选官方版本不一致")
	}
	for name, pkg := range lock.Packages {
		if name == "" {
			continue
		}
		if !strings.HasPrefix(name, "node_modules/") || strings.Contains(name, "\\") || strings.Contains(name, "..") || pkg.Link {
			return fmt.Errorf("依赖包含本地链接或异常路径: %s", name)
		}
		u, e := url.Parse(pkg.Resolved)
		if e != nil || u.Scheme != "https" || u.Host != "registry.npmjs.org" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("依赖不是官方 npm registry 发布包: %s", name)
		}
		encoded, ok := strings.CutPrefix(pkg.Integrity, "sha512-")
		sum, e := base64.StdEncoding.DecodeString(encoded)
		if !ok || e != nil || len(sum) != 64 {
			return fmt.Errorf("依赖缺少 SHA512 校验值: %s", name)
		}
	}
	return nil
}

func (a *App) installNpmAgent(ctx context.Context, p InstallPlan, out io.Writer) (Record, error) {
	node, npm, err := a.nodeRuntime(ctx)
	if err != nil {
		return Record{}, err
	}
	if p.Release.Package != agentPackage(p.Tool) || !agentVersion.MatchString(p.Release.Version) {
		return Record{}, fmt.Errorf("不支持的 Agent 软件包")
	}
	if err = prepareVersionedTarget(p); err != nil {
		return Record{}, err
	}
	if err = os.MkdirAll(p.InstallDir, 0755); err != nil {
		return Record{}, err
	}
	project := struct {
		Name         string            `json:"name"`
		Private      bool              `json:"private"`
		Dependencies map[string]string `json:"dependencies"`
	}{"oxgen-agent-install", true, map[string]string{p.Release.Package: p.Release.Version}}
	if err = saveJSON(filepath.Join(p.InstallDir, "package.json"), project); err != nil {
		return Record{}, err
	}
	config := "registry=" + npmRegistry + "/\nignore-scripts=true\nstrict-ssl=true\naudit=false\nfund=false\nupdate-notifier=false\n"
	for _, name := range []string{"npmrc", "global.npmrc"} {
		if err = os.WriteFile(filepath.Join(p.DownloadDir, name), []byte(config), 0600); err != nil {
			return Record{}, err
		}
	}
	env := []string{
		"NPM_CONFIG_USERCONFIG=" + filepath.Join(p.DownloadDir, "npmrc"),
		"NPM_CONFIG_GLOBALCONFIG=" + filepath.Join(p.DownloadDir, "global.npmrc"),
		"NPM_CONFIG_CACHE=" + filepath.Join(p.DownloadDir, "npm-cache"),
		"NPM_CONFIG_REGISTRY=" + npmRegistry,
		"NPM_CONFIG_IGNORE_SCRIPTS=true", "NPM_CONFIG_ENGINE_STRICT=true", "NPM_CONFIG_STRICT_SSL=true",
		"NPM_CONFIG_AUDIT=false", "NPM_CONFIG_FUND=false", "NPM_CONFIG_UPDATE_NOTIFIER=false",
		"NPM_CONFIG_LOGS_MAX=0", "NPM_CONFIG_PROGRESS=false",
	}
	common := []string{"--prefix", p.InstallDir, "--global=false", "--registry=" + npmRegistry, "--ignore-scripts", "--include=optional", "--audit=false", "--fund=false", "--engine-strict"}
	resolve := append([]string{npm, "install", "--package-lock-only"}, common...)
	fmt.Fprintln(out, "正在解析官方 npm 依赖并核对锁文件；安装脚本保持关闭。")
	lockfile := filepath.Join(p.InstallDir, "package-lock.json")
	cachedLock := filepath.Join(p.DownloadDir, "oxgen-lock-"+string(p.Tool)+"-"+p.Release.Version+"-"+agentPlatform(a.Host)+".json")
	if validateAgentLock(cachedLock, p.Release) == nil {
		fmt.Fprintln(out, "复用已核对来源、版本和 SHA512 的依赖锁文件。")
		if err = copyAgentFile(ctx, cachedLock, lockfile); err != nil {
			return Record{}, err
		}
	} else {
		if err = a.runAgentNPM(ctx, Command{Name: node, Args: resolve, Dir: p.InstallDir, Env: env}, out); err != nil {
			return Record{}, err
		}
		if err = validateAgentLock(lockfile, p.Release); err != nil {
			return Record{}, err
		}
		data, e := os.ReadFile(lockfile)
		if e != nil {
			return Record{}, e
		}
		if err = os.WriteFile(cachedLock, data, 0600); err != nil {
			return Record{}, err
		}
	}
	install := append([]string{npm, "ci"}, common...)
	if err = a.runAgentNPM(ctx, Command{Name: node, Args: install, Dir: p.InstallDir, Env: env}, out); err != nil {
		return Record{}, err
	}
	entry, err := npmBin(filepath.Join(p.InstallDir, "node_modules", filepath.FromSlash(p.Release.Package)), agentCommand(p.Tool))
	if err != nil {
		return Record{}, err
	}
	output, err := a.Runner.Capture(ctx, Command{Name: node, Args: []string{entry, "--version"}, Dir: p.InstallDir, Env: env, Timeout: 45 * time.Second})
	if err != nil || agentVersionOf(output) != p.Release.Version {
		return Record{}, fmt.Errorf("Agent 实际运行或版本验证失败: %s (%v)", strings.TrimSpace(output), err)
	}
	bin := filepath.Join(p.InstallDir, "bin")
	if err = os.MkdirAll(bin, 0755); err != nil {
		return Record{}, err
	}
	launcher := filepath.Join(bin, agentCommand(p.Tool))
	body := "#!/bin/sh\nexec " + shellQuote(node) + " " + shellQuote(entry) + " \"$@\"\n"
	if a.Host.OS == "windows" {
		launcher += ".cmd"
		// Paths from the pasted root remain relative to this batch file, so cmd
		// does not reinterpret percent signs or shell metacharacters in that root.
		rel, e := filepath.Rel(bin, entry)
		if e != nil {
			return Record{}, e
		}
		bash := ""
		if p.Tool == Pi {
			bash = a.gitBash(ctx)
			if bash == "" {
				return Record{}, &pendingError{"pi 已下载，但 Git Bash 不可用；补齐 Git 后重新安装。"}
			}
		}
		body = windowsNpmAgentLauncher(node, rel, bash)
	}
	if err = os.WriteFile(launcher, []byte(body), 0755); err != nil {
		return Record{}, err
	}
	fmt.Fprintln(out, "已验证：", strings.TrimSpace(output))
	return Record{Version: p.Release.Version, Path: launcher, Root: p.Root, Source: npmAgentSource, Runtime: node, Entry: entry, UpdatedAt: time.Now()}, nil
}
