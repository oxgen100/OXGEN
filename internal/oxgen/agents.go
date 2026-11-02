package oxgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func agentVersionOf(text string) string {
	return regexp.MustCompile(`\b\d+\.\d+\.\d+(?:-[A-Za-z0-9]+(?:[.-][A-Za-z0-9]+)*)?`).FindString(text)
}

func (a *App) agentMenu(ctx context.Context) error {
	for ctx.Err() == nil {
		a.UI.Clear()
		a.UI.Section("Agent 安装")
		for i, tool := range agentTools {
			a.UI.Item(fmt.Sprint(i+1), tool.String()+" 检测／安装／更新")
		}
		a.UI.Rule()
		a.UI.Item("7", "检测已安装的 Agent")
		a.UI.Item("0", "返回主菜单")
		a.UI.Rule()
		a.UI.Line("粘贴目录即可自选安装位置；登录在安装完成后进行。")
		value, err := a.UI.Ask("请输入你的选择", "")
		if err != nil {
			return err
		}
		if value == "0" {
			return nil
		}
		a.UI.Clear()
		if value == "7" {
			a.UI.Run(ctx, true, func(ctx context.Context) {
				for _, tool := range agentTools {
					if ctx.Err() != nil {
						return
					}
					a.showDetection(a.detect(ctx, tool))
				}
			})
		} else {
			index := -1
			for i := range agentTools {
				if value == fmt.Sprint(i+1) {
					index = i
				}
			}
			if index < 0 {
				a.UI.Warn("无效选项，请重新输入。")
			} else {
				tool := agentTools[index]
				a.UI.Section(tool.String() + " 检测／安装／更新")
				result := a.manage(ctx, tool)
				a.summary([]Result{result})
				if result.Status == "成功" || result.Status == "保留" {
					a.UI.Line(agentStartNote(tool))
				}
			}
		}
		if a.UI.cancelled.Load() {
			a.UI.cancelled.Store(false)
			continue
		}
		if err = a.UI.Pause(); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func agentStartNote(tool Tool) string {
	switch tool {
	case WorkBuddy:
		return "从所选目录的 WorkBuddy 应用或开始菜单启动，按官方界面登录。"
	case DeepSeek:
		return "在新终端运行 dsh web，打开本机网页后配置模型；需要命令行时运行 dsh --help。"
	case Pi:
		return "在项目目录运行 pi，通过 /login 连接模型或按官方文档配置 API。"
	case OpenCode:
		return "在项目目录运行 opencode，通过 /connect 连接模型。"
	case Codex:
		return "在项目目录运行 codex，按提示登录。"
	default:
		return "在项目目录运行 claude，按提示登录。"
	}
}

func agentLocationNote(tool Tool) string {
	user, _ := os.UserHomeDir()
	switch tool {
	case Codex:
		return "登录及会话由 Codex 管理，默认另写入 " + filepath.Join(user, ".codex") + "（CODEX_HOME 可覆盖）。"
	case Claude:
		return "登录及设置由 Claude Code 管理，通常另写入 " + filepath.Join(user, ".claude") + "、~/.claude.json；本次直接安装原生程序，不运行会写入默认安装位置的 claude install。"
	case Pi:
		return "程序及 npm 缓存放入所选目录；pi 的登录、设置和会话默认另写入 " + filepath.Join(user, ".pi", "agent") + "。"
	case DeepSeek:
		return "程序及 npm 缓存放入所选目录；首次 dsh web 由官方程序创建用户数据并启动本机 Web UI，位置由其配置管理。"
	case OpenCode:
		return "程序放入所选目录；账户、缓存、配置和项目数据由 OpenCode 管理，通常另写入用户资料中的 .config、.local/share、.local/state 和 .cache。已有 OpenCode V1 配置不自动迁移。"
	case WorkBuddy:
		return "桌面程序放入所选目录；安装器还会写入当前用户的开始菜单、快捷方式、卸载记录和系统临时目录；登录、缓存及工作数据由 WorkBuddy 在用户资料目录管理。"
	}
	return ""
}

func (a *App) nodeRuntime(ctx context.Context) (node, npm string, err error) {
	d := a.detect(ctx, Node)
	if !d.Installed || !d.Healthy || compareVersion(d.Version, "22.19.0") < 0 {
		return "", "", &pendingError{"需要 Node.js 22.19 或更新版本及 npm。请返回“Agent 基础环境”安装/更新 Node.js 后重试。"}
	}
	node = d.Path
	candidates := []string{filepath.Join(filepath.Dir(node), "node_modules", "npm", "bin", "npm-cli.js"), filepath.Join(filepath.Dir(node), "..", "lib", "node_modules", "npm", "bin", "npm-cli.js")}
	if path, e := a.Runner.LookPath("npm"); e == nil {
		if real, e := filepath.EvalSymlinks(path); e == nil && strings.HasSuffix(real, "npm-cli.js") {
			candidates = append(candidates, real)
		}
		candidates = append(candidates, filepath.Join(filepath.Dir(path), "node_modules", "npm", "bin", "npm-cli.js"))
	}
	for _, candidate := range candidates {
		if info, e := os.Stat(candidate); e == nil && info.Mode().IsRegular() {
			return node, filepath.Clean(candidate), nil
		}
	}
	return "", "", &pendingError{"未找到可由 Node 运行的 npm-cli.js，请在基础环境中补齐 npm 后重试。"}
}

func (a *App) agentPrerequisite(ctx context.Context, tool Tool) error {
	if tool == WorkBuddy && a.Host.OS == "linux" {
		return &pendingError{"腾讯尚未提供 Ubuntu/Debian 的 WorkBuddy 官方安装包；Windows 或 macOS 可安装。"}
	}
	if tool == Pi || tool == DeepSeek {
		if _, _, err := a.nodeRuntime(ctx); err != nil {
			return err
		}
		if tool == Pi && a.Host.OS == "windows" && a.gitBash(ctx) == "" {
			return &pendingError{"Windows 版 pi 需要 Git Bash。请先在“Agent 基础环境”安装 Git for Windows。"}
		}
	}
	return nil
}

func (a *App) gitBash(ctx context.Context) string {
	d := a.detect(ctx, Git)
	candidates := []string{}
	if d.Installed {
		candidates = append(candidates, filepath.Join(filepath.Dir(d.Path), "..", "bin", "bash.exe"), filepath.Join(filepath.Dir(d.Path), "bash.exe"))
	}
	for _, key := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
		if root := os.Getenv(key); root != "" {
			candidates = append(candidates, filepath.Join(root, "Git", "bin", "bash.exe"))
		}
	}
	for _, p := range candidates {
		if f, e := os.Stat(p); e == nil && f.Mode().IsRegular() {
			return filepath.Clean(p)
		}
	}
	return ""
}

func (a *App) detectAgent(ctx context.Context, tool Tool) Detection {
	if tool == WorkBuddy {
		return a.detectWorkBuddy(ctx)
	}
	d := Detection{Tool: tool}
	var candidates []struct {
		path   string
		active bool
	}
	if p, err := a.Runner.LookPath(agentCommand(tool)); err == nil {
		candidates = append(candidates, struct {
			path   string
			active bool
		}{p, true})
	}
	if record, ok := a.State.Records[tool]; ok {
		candidates = append(candidates, struct {
			path   string
			active bool
		}{record.Path, false})
	}
	user, _ := os.UserHomeDir()
	// Native installers commonly keep commands outside the current terminal PATH.
	if tool == Claude || tool == Codex {
		name := agentCommand(tool)
		if a.Host.OS == "windows" {
			name += ".exe"
		}
		candidates = append(candidates, struct {
			path   string
			active bool
		}{filepath.Join(user, ".local", "bin", name), false})
	}
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return d
		}
		if candidate.path == "" {
			continue
		}
		c := Command{Name: candidate.path, Args: []string{"--version"}, Env: []string{"DISABLE_UPDATES=1", "DISABLE_AUTOUPDATER=1"}}
		record, owned := a.State.Records[tool]
		owned = owned && samePath(record.Path, candidate.path)
		if owned && record.Runtime != "" {
			c.Name = record.Runtime
			if record.Entry != "" {
				c.Args = []string{record.Entry, "--version"}
			}
		} else if real, e := filepath.EvalSymlinks(candidate.path); e == nil {
			ext := strings.ToLower(filepath.Ext(real))
			if ext == ".cmd" || ext == ".ps1" {
				entry, err := npmBin(filepath.Join(filepath.Dir(real), "node_modules", filepath.FromSlash(agentNpmName(tool))), agentCommand(tool))
				if err != nil {
					continue
				}
				if strings.HasSuffix(entry, ".exe") {
					c.Name = entry
				} else if node, err := a.Runner.LookPath("node"); err == nil {
					c.Name, c.Args = node, []string{entry, "--version"}
				} else {
					continue
				}
			} else if ext == ".js" || ext == ".mjs" {
				if node, e := a.Runner.LookPath("node"); e == nil {
					c.Name, c.Args = node, []string{real, "--version"}
				} else {
					continue
				}
			}
		}
		text, err := a.Runner.Capture(ctx, c)
		v := agentVersionOf(text)
		if err != nil || v == "" {
			continue
		}
		d.Installed, d.Healthy, d.Active, d.Version, d.Path, d.Source = true, true, candidate.active, v, candidate.path, "现有 Agent 安装"
		if owned {
			d.Source = record.Source
			if record.Runtime != "" {
				d.Details = append(d.Details, "实际运行时："+record.Runtime)
			}
		}
		if !candidate.active {
			d.Details = append(d.Details, "已安装；当前 PATH 尚未指向该入口。")
		}
		return d
	}
	return d
}

func agentNpmName(tool Tool) string {
	if tool == Claude {
		return "@anthropic-ai/claude-code"
	}
	return agentPackage(tool)
}

func npmBin(root, name string) (string, error) {
	var m struct {
		Bin json.RawMessage `json:"bin"`
	}
	b, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return "", err
	}
	if err = json.Unmarshal(b, &m); err != nil {
		return "", err
	}
	entry := ""
	if json.Unmarshal(m.Bin, &entry) != nil {
		var bins map[string]string
		if err = json.Unmarshal(m.Bin, &bins); err != nil {
			return "", err
		}
		entry = bins[name]
	}
	if entry == "" {
		return "", fmt.Errorf("官方 npm 包中没有 %s 命令入口", name)
	}
	file := filepath.Join(root, filepath.FromSlash(entry))
	if !contained(root, file) {
		return "", fmt.Errorf("npm 命令入口超出软件包目录")
	}
	if f, e := os.Stat(file); e != nil || !f.Mode().IsRegular() {
		return "", fmt.Errorf("npm 命令入口缺失: %s", file)
	}
	return file, nil
}

func (a *App) installAgent(ctx context.Context, p InstallPlan, out io.Writer) (Record, error) {
	if p.Tool == WorkBuddy {
		return a.installWorkBuddy(ctx, p, out)
	}
	if err := a.agentPrerequisite(ctx, p.Tool); err != nil {
		return Record{}, err
	}
	if p.Tool == Pi || p.Tool == DeepSeek {
		return a.installNpmAgent(ctx, p, out)
	}
	file, err := a.Client.Download(ctx, p.Release.Artifact, p.DownloadDir, out)
	if err != nil {
		return Record{}, err
	}
	if p.Release.Artifact.Signature != "" && a.Host.OS == "windows" {
		if err = a.verifySignature(ctx, file, p.Release.Artifact.Signature, out); err != nil {
			return Record{}, err
		}
	}
	if err = prepareVersionedTarget(p); err != nil {
		return Record{}, err
	}
	var exe string
	if p.Tool == Claude {
		if err = os.MkdirAll(p.InstallDir, 0755); err != nil {
			return Record{}, err
		}
		exe = filepath.Join(p.InstallDir, filepath.Base(file))
		if err = copyAgentFile(ctx, file, exe); err != nil {
			return Record{}, err
		}
		if a.Host.OS == "darwin" {
			if err = a.Runner.Run(ctx, Command{Name: "/usr/bin/codesign", Args: []string{"--verify", "--verbose=2", exe}}, out); err != nil {
				return Record{}, err
			}
		}
	} else {
		if err = extractArchiveContext(ctx, file, p.InstallDir, true); err != nil {
			return Record{}, err
		}
		name := agentCommand(p.Tool)
		if a.Host.OS == "windows" {
			name += ".exe"
		}
		err = filepath.WalkDir(p.InstallDir, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !entry.IsDir() && entry.Type().IsRegular() && entry.Name() == name {
				if exe != "" {
					return fmt.Errorf("官方包中存在多个同名 Agent 程序，未选择不确定的入口")
				}
				exe = path
			}
			return nil
		})
		if err != nil {
			return Record{}, err
		}
		if exe == "" {
			return Record{}, fmt.Errorf("官方发行包未包含 %s 命令", name)
		}
	}
	if err = os.Chmod(exe, 0755); err != nil {
		return Record{}, err
	}
	text, err := a.Runner.Capture(ctx, Command{Name: exe, Args: []string{"--version"}, Env: []string{"DISABLE_UPDATES=1", "DISABLE_AUTOUPDATER=1"}})
	if err != nil || agentVersionOf(text) != p.Release.Version {
		return Record{}, fmt.Errorf("安装后的 Agent 运行或版本验证失败: %s (%v)", strings.TrimSpace(text), err)
	}
	fmt.Fprintln(out, "已验证：", strings.TrimSpace(text))
	commandPath := exe
	if p.Tool == Claude {
		// Keep updates under OXGEN's explicit directory/version plan. The native
		// installer/updater otherwise installs launchers under ~/.local.
		bin := filepath.Join(p.InstallDir, "bin")
		if err = os.MkdirAll(bin, 0755); err != nil {
			return Record{}, err
		}
		commandPath = filepath.Join(bin, "claude")
		body := "#!/bin/sh\nDISABLE_UPDATES=1 DISABLE_AUTOUPDATER=1 exec " + shellQuote(exe) + " \"$@\"\n"
		if a.Host.OS == "windows" {
			commandPath += ".cmd"
			body = "@echo off\r\nsetlocal DisableDelayedExpansion\r\nset \"DISABLE_UPDATES=1\"\r\nset \"DISABLE_AUTOUPDATER=1\"\r\n\"%~dp0..\\claude.exe\" %*\r\nexit /b %errorlevel%\r\n"
		}
		if err = os.WriteFile(commandPath, []byte(body), 0755); err != nil {
			return Record{}, err
		}
	}
	return Record{Version: p.Release.Version, Path: commandPath, Runtime: exe, Root: p.Root, Source: agentSource, UpdatedAt: time.Now()}, nil
}

func copyAgentFile(ctx context.Context, from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, contextReader{ctx, in})
	return errors.Join(err, out.Close())
}
