package oxgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type App struct {
	UI      *UI
	Runner  Runner
	Client  *Client
	Host    Host
	State   State
	Home    string
	Version string
	BaseURL string
}

func Main(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer, version string) int {
	home := defaultHome()
	baseURL := ""
	doctor, jsonMode, noColor, online := false, false, false, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--version", "version":
			fmt.Fprintln(out, "OXGEN 氧气邮政员 "+version)
			return 0
		case "--help", "-h", "help":
			fmt.Fprintln(out, "OXGEN 氧气邮政员\n\noxgen                          打开编号菜单\noxgen doctor [--json] [--online] 只读检测（--online 查询官方推荐版本）\noxgen --version\noxgen --home <绝对路径>          指定状态、日志和工具目录\noxgen --base-url <HTTPS地址>     设置你的 OXGEN 下载入口\noxgen --no-color                关闭 ANSI 颜色\n\n基础环境：Git、Node.js/npm、Python/pip/venv、VS Code；Windows 可选 WSL。\nAgent：Codex、Claude Code、WorkBuddy、DeepSeek Harness、pi、OpenCode。")
			return 0
		case "--home":
			i++
			if i == len(args) {
				fmt.Fprintln(errOut, "--home 缺少目录")
				return 2
			}
			home = args[i]
		case "--base-url":
			i++
			if i == len(args) {
				fmt.Fprintln(errOut, "--base-url 缺少地址")
				return 2
			}
			baseURL = args[i]
		case "doctor":
			doctor = true
		case "--json":
			jsonMode = true
		case "--online":
			online = true
		case "--no-color":
			noColor = true
		default:
			fmt.Fprintf(errOut, "未知参数 %q，请使用 oxgen --help\n", args[i])
			return 2
		}
	}
	if !filepath.IsAbs(home) {
		fmt.Fprintln(errOut, "--home 必须是绝对路径")
		return 2
	}
	if baseURL == "" {
		if b, e := os.ReadFile(filepath.Join(home, "server-url.txt")); e == nil {
			baseURL = strings.TrimSpace(string(b))
		}
	}
	if baseURL != "" {
		if _, _, e := releaseClient(baseURL); e != nil {
			fmt.Fprintln(errOut, e)
			return 2
		}
	}
	s, err := loadState(home)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	terminal := os.Getenv("TERM") != "dumb" && enableColor(out)
	color := !noColor && os.Getenv("NO_COLOR") == "" && terminal
	ui := newUI(in, out, color)
	ui.Terminal = terminal && terminalInput(in)
	ui.Context = ctx
	a := &App{UI: ui, Runner: OSRunner{Input: ui.reader}, Client: newClient(), Host: detectHost(), State: s, Home: home, Version: version, BaseURL: baseURL}
	if doctor {
		if err = a.doctor(ctx, jsonMode, online); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		return 0
	}
	if jsonMode || online {
		fmt.Fprintln(errOut, "--json 和 --online 仅用于 doctor")
		return 2
	}
	if err = a.Host.Supported(); err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	if err = a.menu(ctx); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}

func (a *App) doctor(ctx context.Context, jsonMode, online bool) error {
	type entry struct {
		Detection   Detection `json:"detection"`
		Recommended *Release  `json:"recommended,omitempty"`
		QueryError  string    `json:"query_error,omitempty"`
	}
	report := struct {
		Host      Host     `json:"host"`
		Home      string   `json:"home"`
		Supported bool     `json:"supported"`
		Tools     []entry  `json:"tools"`
		Pending   *Pending `json:"pending,omitempty"`
	}{Host: a.Host, Home: a.Home, Supported: a.Host.Supported() == nil, Pending: a.State.Pending}
	tools := append([]Tool{}, coreTools...)
	tools = append(tools, agentTools...)
	if a.Host.OS == "windows" {
		tools = append(tools, WSL)
	}
	for _, tool := range tools {
		e := entry{Detection: a.detect(ctx, tool)}
		if online && tool != WSL {
			r, err := a.latest(ctx, tool)
			if err != nil {
				e.QueryError = err.Error()
			} else {
				e.Recommended = &r
			}
		}
		report.Tools = append(report.Tools, e)
	}
	if jsonMode {
		enc := json.NewEncoder(a.UI.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	a.UI.Banner(a.Version)
	a.UI.Section("本机环境检测")
	a.UI.Print("系统：%s %s %s %s\n状态目录：%s\n", a.Host.OS, a.Host.Arch, a.Host.Distro, a.Host.Release, a.Home)
	if !report.Supported {
		a.UI.Warn("本机不在首版支持范围；以下为只读检测结果。")
	}
	for _, e := range report.Tools {
		a.showDetection(e.Detection)
		if e.Recommended != nil {
			a.showRelease(*e.Recommended)
		}
		if e.QueryError != "" {
			a.UI.Warn("推荐版本：暂时无法查询 · " + e.QueryError)
		}
	}
	if a.State.Pending != nil {
		a.UI.Warn("存在未完成步骤：" + a.State.Pending.Stage + "；打开菜单后可继续。")
	}
	return nil
}

func (a *App) menu(ctx context.Context) error {
	for ctx.Err() == nil {
		a.UI.Clear()
		a.UI.Banner(a.Version)
		a.UI.Print("%s%s%s\n", a.UI.Accent("命令参考："), a.UI.paint("33", "oxgen"), a.UI.Accent(" / oxgen doctor"))
		a.UI.Rule()
		a.UI.Item("1", "Agent 基础环境")
		a.UI.Item("2", "本机环境检测")
		a.UI.Item("3", "一条龙安装（只补缺失环境）")
		a.UI.Item("4", "Agent 安装（六款可选）")
		a.UI.Rule()
		a.UI.Item("0", "退出脚本")
		a.UI.Rule()
		if a.State.Pending != nil {
			a.UI.Warn("有未完成的 WSL 步骤，可进入“WSL＋Ubuntu”继续。")
		}
		s, err := a.UI.Ask("请输入你的选择", "")
		if err != nil {
			return err
		}
		switch s {
		case "0":
			return nil
		case "1":
			if err = a.foundationMenu(ctx); err != nil {
				return err
			}
		case "4":
			if err = a.agentMenu(ctx); err != nil {
				return err
			}
		case "2":
			a.UI.Clear()
			if err = a.doctor(ctx, false, false); err != nil {
				a.UI.Warn("检测失败：" + err.Error())
			}
			if err = a.UI.Pause(); err != nil {
				return err
			}
		case "3":
			a.UI.Clear()
			a.oneStop(ctx)
			if a.UI.cancelled.Load() {
				a.UI.cancelled.Store(false)
				continue
			}
			if err = a.UI.Pause(); err != nil {
				return err
			}
		default:
			a.UI.Warn("无效选项，请重新输入。")
			if err = a.UI.Pause(); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func (a *App) foundationMenu(ctx context.Context) error {
	for ctx.Err() == nil {
		a.UI.Clear()
		a.UI.Section("Agent 基础环境")
		a.UI.Item("1", "一条龙安装（只补缺失环境）")
		a.UI.Rule()
		a.UI.Item("2", "Git 检测／安装／更新")
		a.UI.Item("3", "Node.js/npm 检测／安装／更新")
		a.UI.Item("4", "Python/pip/venv 检测／安装／更新")
		if a.Host.OS == "windows" {
			a.UI.Item("5", "WSL＋Ubuntu（可选）")
		}
		a.UI.Item("6", "VS Code 检测／安装／更新")
		a.UI.Rule()
		a.UI.Item("0", "返回主菜单")
		a.UI.Rule()
		s, err := a.UI.Ask("请输入你的选择", "")
		if err != nil {
			return err
		}
		switch s {
		case "0":
			return nil
		case "1":
			a.UI.Clear()
			a.oneStop(ctx)
		case "2", "3", "4", "6":
			tool := map[string]Tool{"2": Git, "3": Node, "4": Python, "6": VSCode}[s]
			a.UI.Clear()
			a.UI.Section(tool.String() + " 检测／安装／更新")
			a.summary([]Result{a.manage(ctx, tool)})
		case "5":
			if a.Host.OS == "windows" {
				a.UI.Clear()
				a.UI.Section("WSL＋Ubuntu")
				a.summary([]Result{a.manageWSL(ctx)})
			} else {
				a.UI.Warn("此项仅 Windows 提供。")
			}
		default:
			a.UI.Warn("无效选项，请重新输入。")
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

func (a *App) showDetection(d Detection) {
	a.UI.Print("\n%s：", a.UI.Accent(d.Tool.String()))
	if !d.Installed {
		a.UI.Line("未检测到可运行版本")
	} else {
		label := "实际命令"
		if !d.Active {
			label = "已安装程序（PATH 未生效）"
		}
		a.UI.Print("%s\n%s：%s\n安装来源：%s\n", d.Version, label, d.Path, d.Source)
		if !d.Healthy {
			a.UI.Warn("组件不完整或配套命令尚不可用。")
		}
	}
	for _, s := range d.Details {
		a.UI.Line("  " + s)
	}
}
func (a *App) showRelease(r Release) {
	v := r.Version
	if v == "" {
		v = "由系统官方组件提供，暂不比较上游版本号"
	}
	a.UI.Print("推荐版本：%s\n版本渠道：%s\n", v, r.Channel)
}

func (a *App) executePlan(ctx context.Context, p InstallPlan) Result {
	tool := p.Tool
	var err error
	if err = ensureWritable(p.DownloadDir); err != nil {
		return a.result(tool, "失败", "下载目录不可写: "+err.Error(), "")
	}
	if !p.Fixed {
		if err = ensureWritable(filepath.Dir(p.InstallDir)); err != nil {
			return a.result(tool, "失败", "安装目录不可写: "+err.Error(), "")
		}
		if err = checkSpace(filepath.Dir(p.InstallDir), 256*1024*1024); err != nil {
			return a.result(tool, "失败", err.Error(), "")
		}
	}
	logPath, log, err := a.openLog(tool)
	if err != nil {
		return a.result(tool, "失败", err.Error(), "")
	}
	defer log.Close()
	writer := io.MultiWriter(a.UI.Out, log)
	record, err := a.install(ctx, p, writer)
	if err != nil {
		if errors.Is(err, errCancelled) || errors.Is(err, context.Canceled) {
			return a.result(tool, "跳过", "用户取消后续安装", logPath)
		}
		var waiting *pendingError
		if errors.As(err, &waiting) {
			return a.result(tool, "待用户操作", err.Error(), logPath)
		}
		return a.result(tool, "失败", err.Error(), logPath)
	}
	if ctx.Err() != nil {
		return a.result(tool, "跳过", "用户取消后续安装", logPath)
	}
	a.State.Records[tool] = record
	if err = saveState(a.Home, a.State); err != nil {
		return a.result(tool, "失败", "已安装，但保存安装记录失败: "+err.Error(), logPath)
	}
	if err = a.setPath(ctx, record, writer, 0); err != nil {
		return a.result(tool, "待用户操作", "程序已安装，但命令入口需要手动配置: "+err.Error(), logPath)
	}
	return a.result(tool, "成功", record.Version+" · "+record.Path, logPath)
}

func (a *App) planInstall(tool Tool, r Release, d Detection, update bool) (InstallPlan, error) {
	p := InstallPlan{Tool: tool, Release: r, Current: d, Update: update, Root: filepath.Join(a.Home, "tools", string(tool))}
	if isAgent(tool) {
		p.Root = filepath.Join(a.Home, "agents", string(tool))
		if previous, ok := a.State.Records[tool]; ok && samePath(previous.Path, d.Path) && (previous.Source == agentSource || previous.Source == npmAgentSource || previous.Source == workBuddySource) {
			p.Root = previous.Root
		}
		p.InstallDir = p.Root
		p.Extra = append(p.Extra, agentLocationNote(tool))
		if tool != WorkBuddy {
			p.Extra = append(p.Extra, "将命令入口加入当前用户 PATH；新终端生效。")
			if d.Installed && d.Source != agentSource && d.Source != npmAgentSource {
				p.Extra = append(p.Extra, "现有其他来源安装保留，官方版本另放在所选目录；请核对新终端的命令实际路径。")
				p.Update = false
			}
			if tool == Pi || tool == DeepSeek {
				p.Extra = append(p.Extra, "使用现有 Node.js 22.19+ 和 npm；依赖包及缓存限定在所选目录，不改全局 npm prefix。")
			}
		} else if a.Host.OS == "windows" && d.Installed {
			p.Fixed, p.InstallDir = true, filepath.Dir(d.Path)
			p.Root = p.InstallDir
			p.Extra = append(p.Extra, "WorkBuddy 官方安装器按原目录更新；下面仅可选择下载目录。跨盘迁移不自动执行。请先关闭 WorkBuddy。")
		}
		return p, nil
	}
	if a.Host.OS == "windows" {
		p.Extra = append(p.Extra, "命令入口将加入当前用户 PATH；系统安装器临时目录："+os.TempDir())
	}
	if tool == Node {
		p.Extra = append(p.Extra, "npm 日常配置和缓存仍由 npm 自身设置控制，默认位于用户资料目录；所选下载目录只管理本次安装包。")
	}
	if tool == VSCode {
		p.Extra = append(p.Extra, vscodeLocationNote(a.Host.OS))
	}
	if previous, ok := a.State.Records[tool]; ok && samePath(previous.Path, d.Path) {
		p.Root = previous.Root
	}
	if a.Host.OS == "linux" && systemPackage(tool) {
		p.Fixed = true
		p.InstallDir = "/usr"
		p.Extra = []string{"系统包由 APT 管理，将写入 /usr、/etc、/var/lib/dpkg；不会搬迁系统 Python。"}
		return p, nil
	}
	if a.Host.OS == "darwin" && tool == Git {
		p.Fixed = true
		p.InstallDir = "/Library/Developer/CommandLineTools"
		p.Extra = []string{"Apple 系统组件及 /usr/bin/git 入口由 macOS 管理；系统安装界面可能需要你确认。"}
		if d.Installed && d.Source != "Apple 命令行开发工具" {
			p.Extra = append(p.Extra, "当前 Git 来自其他目录，将保留原安装；本项只管理 Apple 提供的 /usr/bin/git，不接管 Homebrew 等其他渠道。")
		}
		return p, nil
	}
	if a.Host.OS == "darwin" && tool == Python {
		p.Fixed = true
		p.InstallDir = "/Library/Frameworks/Python.framework"
		p.Extra = []string{"python.org 官方 pkg 还可能写入 /Applications、/usr/local/bin 和系统安装收据；安装需要管理员授权。"}
		return p, nil
	}
	if tool == Git && update && d.Source == "Git for Windows 官方安装器" {
		p.Root = filepath.Dir(filepath.Dir(d.Path))
		p.Fixed = true
		p.InstallDir = p.Root
		p.Extra = append(p.Extra, "官方安装器在现有目录更新；更换磁盘属于迁移，本期不自动执行。")
		return p, nil
	}
	if update && d.Source != "OXGEN 官方发行包" && d.Source != "OXGEN Python 官方运行时" && d.Source != vscodeSource {
		p.Extra = append(p.Extra, "无法安全接管该现有安装；将并存安装官方版本，原目录保留。新终端的命令优先级需检查 PATH。")
		p.Update = false
	}
	if tool == Python {
		p.Extra = append(p.Extra, "Python 安装管理器作为 Windows 系统应用安装，位置由系统控制（通常为 "+filepath.Join(os.Getenv("ProgramFiles"), "WindowsApps")+"）；完整 Python 运行时放入所选目录。系统命令入口仍可能写入 "+os.Getenv("LOCALAPPDATA")+"。")
	}
	p.InstallDir = p.Root
	return p, nil
}

func (a *App) openLog(tool Tool) (string, *os.File, error) {
	dir := filepath.Join(a.Home, "logs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", nil, err
	}
	name := filepath.Join(dir, time.Now().Format("20060102-150405.000000000")+"-"+string(tool)+".log")
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	return name, f, err
}

func (a *App) result(tool Tool, status, message, log string) Result {
	r := Result{Tool: tool, Status: status, Message: message, Log: log, Time: time.Now()}
	a.State.History = append(a.State.History, r)
	if err := saveState(a.Home, a.State); err != nil {
		a.UI.Warn("保存本次结果失败: " + err.Error())
	}
	return r
}
func (a *App) summary(results []Result) {
	a.UI.Section("本次结果")
	for _, r := range results {
		a.UI.Print("%s %s\n  %s\n", a.UI.Status(r.Status), r.Tool.String(), r.Message)
		if r.Log != "" {
			a.UI.Line("  日志：" + r.Log)
		}
	}
	a.UI.Rule()
}

type pendingError struct{ message string }

func (e *pendingError) Error() string { return e.message }
