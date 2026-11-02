package oxgen

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

func ubuntuNames(text string) []string {
	var result []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\x00", ""), "\n") {
		name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "*"))
		if strings.HasPrefix(name, "Ubuntu") && !strings.ContainsAny(name, " \t") {
			result = append(result, name)
		}
	}
	return result
}

func wslNeedsReboot(text string) bool {
	s := strings.ToLower(text)
	return strings.Contains(s, "reboot") || strings.Contains(s, "restart") || strings.Contains(s, "重启") || strings.Contains(s, "重新启动") || strings.Contains(s, "3010")
}

func (a *App) runElevated(ctx context.Context, name string, args []string, out io.Writer) error {
	b, _ := json.Marshal(args)
	return a.powershell(ctx, "run-elevated", elevateScript, []string{"-Program", name, "-ArgumentsJSON", string(b)}, out)
}

func (a *App) ubuntuLocation(ctx context.Context, distro string) string {
	ps, err := a.Runner.LookPath("powershell.exe")
	if err != nil {
		return ""
	}
	// Read-only query documented by Microsoft Learn's WSL disk-space guide.
	script := `[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false); $locations=@(Get-ChildItem -LiteralPath 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Lxss' -ErrorAction SilentlyContinue | ForEach-Object { if ($_.GetValue('DistributionName') -like 'Ubuntu*') { [PSCustomObject]@{name=$_.GetValue('DistributionName');path=$_.GetValue('BasePath')} } }); ConvertTo-Json -InputObject $locations -Compress`
	out, err := a.Runner.Capture(ctx, Command{Name: ps, Args: []string{"-NoProfile", "-Command", script}})
	if err != nil {
		return ""
	}
	var locations []struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(out), &locations) != nil {
		return ""
	}
	for _, location := range locations {
		if location.Name == distro {
			return strings.TrimPrefix(location.Path, `\\?\`)
		}
	}
	return ""
}

func (a *App) manageWSL(ctx context.Context) Result {
	if a.Host.OS != "windows" {
		return a.result(WSL, "跳过", "仅 Windows 提供此项", "")
	}
	a.UI.Warn("WSL 为可选系统组件；Windows 部分写入系统盘，Ubuntu 的位置在支持时可自定义。安装可能请求管理员授权或需要重启；不会自动重启电脑。")
	if !a.UI.Confirm("进入 WSL／Ubuntu 安装或继续未完成步骤？") {
		return a.result(WSL, "跳过", "未改动 WSL", "")
	}
	download, err := a.chooseDir("WSL 记录和后续 Linux 工具箱下载目录", filepath.Join(a.Home, "downloads"))
	if err != nil {
		return a.result(WSL, "跳过", err.Error(), "")
	}
	logPath, log, err := a.openLog(WSL)
	if err != nil {
		return a.result(WSL, "失败", err.Error(), "")
	}
	defer log.Close()
	out := io.MultiWriter(a.UI.Out, log)
	wsl, err := a.Runner.LookPath("wsl.exe")
	if err != nil {
		return a.result(WSL, "失败", "系统没有 wsl.exe，请检查 Windows 版本是否支持 WSL。", logPath)
	}
	version, versionErr := a.Runner.Capture(ctx, Command{Name: wsl, Args: []string{"--version"}})
	if versionErr != nil {
		a.State.Pending = &Pending{Stage: "启用 WSL／等待系统就绪"}
		if err = saveState(a.Home, a.State); err != nil {
			return a.result(WSL, "失败", err.Error(), logPath)
		}
		if err = a.runElevated(ctx, wsl, []string{"--install", "--no-distribution", "--web-download"}, out); err != nil {
			return a.result(WSL, "待用户操作", "WSL 启用未完成，请检查安装日志及 Windows 虚拟化支持；完成或重启后重新运行。"+err.Error(), logPath)
		}
		version, err = a.Runner.Capture(ctx, Command{Name: wsl, Args: []string{"--version"}})
		if err != nil || wslNeedsReboot(version) {
			return a.result(WSL, "待用户操作", "WSL 已提交安装，等待系统就绪；如系统提示重启，请自行重启后再运行本菜单。", logPath)
		}
	} else {
		fmt.Fprintln(out, strings.TrimSpace(version))
		i, e := a.UI.Choice("已有 WSL：", []string{"保留当前版本", "通过 Microsoft 官方来源更新 WSL"}, 0)
		if e != nil {
			return a.result(WSL, "跳过", e.Error(), logPath)
		}
		if i == 1 {
			if e = a.runElevated(ctx, wsl, []string{"--update", "--web-download"}, out); e != nil {
				return a.result(WSL, "失败", e.Error(), logPath)
			}
		}
	}
	list, listErr := a.Runner.Capture(ctx, Command{Name: wsl, Args: []string{"--list", "--quiet"}})
	if listErr != nil {
		return a.result(WSL, "待用户操作", "无法读取现有 WSL 发行版，未将查询失败当作未安装。请检查权限和系统就绪状态后重试。"+listErr.Error(), logPath)
	}
	names := ubuntuNames(list)
	distro := "Ubuntu"
	if len(names) > 0 {
		distro = names[0]
		if len(names) > 1 {
			i, e := a.UI.Choice("选择现有 Ubuntu（不会搬迁或重装）：", names, 0)
			if e != nil {
				return a.result(WSL, "跳过", e.Error(), logPath)
			}
			distro = names[i]
		}
		a.UI.Line("复用现有发行版：" + distro)
	} else {
		location := filepath.Join(a.Home, "wsl", "Ubuntu")
		if a.State.Pending != nil && a.State.Pending.Location != "" {
			location = a.State.Pending.Location
		}
		location, err = a.chooseDir("选择 Ubuntu 发行版存放目录", location)
		if err != nil {
			return a.result(WSL, "跳过", err.Error(), logPath)
		}
		help, _ := a.Runner.Capture(ctx, Command{Name: wsl, Args: []string{"--help"}})
		args := []string{"--install", "-d", distro, "--web-download", "--no-launch"}
		if strings.Contains(help, "--location") {
			if stat, e := os.Stat(location); e == nil && stat.IsDir() {
				entries, _ := os.ReadDir(location)
				if len(entries) > 0 {
					return a.result(WSL, "失败", "所选 Ubuntu 目录非空，未覆盖现有文件。", logPath)
				}
			}
			if err = ensureWritable(filepath.Dir(location)); err != nil {
				return a.result(WSL, "失败", err.Error(), logPath)
			}
			if err = checkSpace(filepath.Dir(location), 3*1024*1024*1024); err != nil {
				return a.result(WSL, "失败", err.Error(), logPath)
			}
			args = append(args, "--location", location)
		} else {
			a.UI.Warn("当前 WSL 不支持 --location，无法使用所选目录。实际将由 WSL 分配到 Windows 用户资料／系统盘目录。")
			if !a.UI.Confirm("继续使用 WSL 官方默认位置？") {
				return a.result(WSL, "跳过", "未安装 Ubuntu", logPath)
			}
			location = ""
		}
		a.UI.Print("Ubuntu 目标：%s\nWSL 系统组件仍由 Windows 管理；系统下载临时文件也由 Windows 管理。\n", location)
		if !a.UI.Confirm("开始安装 Ubuntu？") {
			return a.result(WSL, "跳过", "未安装 Ubuntu", logPath)
		}
		a.State.Pending = &Pending{Stage: "Ubuntu 安装／首次初始化", Location: location, Distribution: distro}
		if err = saveState(a.Home, a.State); err != nil {
			return a.result(WSL, "失败", err.Error(), logPath)
		}
		if err = a.runElevated(ctx, wsl, args, out); err != nil {
			return a.result(WSL, "待用户操作", "Ubuntu 安装尚未完成；检查日志和系统重启提示后重试。"+err.Error(), logPath)
		}
		list, _ = a.Runner.Capture(ctx, Command{Name: wsl, Args: []string{"--list", "--quiet"}})
		if len(ubuntuNames(list)) == 0 {
			return a.result(WSL, "待用户操作", "尚未检测到 Ubuntu，安装未验证完成；如需重启请完成后重试。", logPath)
		}
	}
	storage := a.ubuntuLocation(ctx, distro)
	if storage == "" && a.State.Pending != nil {
		storage = a.State.Pending.Location
	}
	if storage == "" {
		storage = "由 WSL 管理（本次无法读取实际磁盘路径）"
		a.UI.Warn(storage)
	} else {
		a.UI.Line("Ubuntu 实际存放位置：" + storage)
	}
	user, err := a.Runner.Capture(ctx, Command{Name: wsl, Args: []string{"-d", distro, "--exec", "whoami"}, Timeout: 15 * time.Second})
	if err != nil {
		a.State.Pending = &Pending{Stage: "Ubuntu 首次用户初始化", Distribution: distro}
		return a.result(WSL, "待用户操作", "请在新终端运行 wsl -d "+distro+"，完成用户名和密码设置，再回本菜单继续。", logPath)
	}
	if strings.TrimSpace(user) == "root" && !a.UI.Confirm("Ubuntu 当前默认用户是 root。确认已完成初始化，并继续使用该用户？") {
		a.State.Pending = &Pending{Stage: "Ubuntu 普通用户初始化", Distribution: distro}
		return a.result(WSL, "待用户操作", "请在新终端完成 Ubuntu 用户设置后继续。", logPath)
	}
	a.State.Pending = nil
	a.State.Records[WSL] = Record{Version: numericVersion(version), Path: wsl, Root: storage, Distribution: distro, Source: "Microsoft 官方 WSL", UpdatedAt: time.Now()}
	if err = saveState(a.Home, a.State); err != nil {
		return a.result(WSL, "失败", err.Error(), logPath)
	}
	if !a.UI.Confirm("WSL／Ubuntu 已可运行。是否继续在 Ubuntu 内打开 OXGEN，配置其独立基础环境？") {
		return a.result(WSL, "成功", "Ubuntu 可运行；其内部基础环境尚未配置", logPath)
	}
	base := a.BaseURL
	if base == "" {
		return a.result(WSL, "待用户操作", "Ubuntu 可运行；OXGEN 下载域名待定。发布后配置 --base-url，再继续内部环境安装。", logPath)
	}
	archText, err := a.Runner.Capture(ctx, Command{Name: wsl, Args: []string{"-d", distro, "--exec", "uname", "-m"}})
	if err != nil {
		return a.result(WSL, "失败", err.Error(), logPath)
	}
	arch := map[string]string{"x86_64": "amd64", "aarch64": "arm64"}[strings.TrimSpace(archText)]
	if arch == "" {
		return a.result(WSL, "失败", "不支持该 Ubuntu 架构", logPath)
	}
	client, artifact, err := a.linuxArtifact(ctx, base, arch)
	if err != nil {
		return a.result(WSL, "失败", err.Error(), logPath)
	}
	file, err := client.Download(ctx, artifact, download, out)
	if err != nil {
		return a.result(WSL, "失败", err.Error(), logPath)
	}
	stage, err := os.MkdirTemp(download, ".oxgen-linux-*")
	if err != nil {
		return a.result(WSL, "失败", err.Error(), logPath)
	}
	defer removeTemp(download, stage)
	extracted := filepath.Join(stage, "unpacked")
	if err = extractArchiveContext(ctx, file, extracted, false); err != nil {
		return a.result(WSL, "失败", err.Error(), logPath)
	}
	linuxHome, e := a.Runner.Capture(ctx, Command{Name: wsl, Args: []string{"-d", distro, "--exec", "printenv", "HOME"}})
	if e != nil {
		return a.result(WSL, "失败", e.Error(), logPath)
	}
	location, e := a.UI.AskValue("粘贴 Ubuntu 内的 OXGEN 完整安装目录", path.Join(strings.TrimSpace(linuxHome), ".oxgen"))
	if e != nil {
		return a.result(WSL, "跳过", e.Error(), logPath)
	}
	if !path.IsAbs(location) || path.Clean(location) == "/" {
		return a.result(WSL, "失败", "需要 Ubuntu 内的完整具体目录", logPath)
	}
	mapped, e := a.Runner.Capture(ctx, Command{Name: wsl, Args: []string{"-d", distro, "--exec", "wslpath", "-u", filepath.Join(extracted, "bin", "oxgen")}})
	if e != nil {
		return a.result(WSL, "失败", e.Error(), logPath)
	}
	bin := path.Join(location, "bin", "oxgen")
	tmp := bin + fmt.Sprintf(".download-%d", time.Now().UnixNano())
	a.UI.Print("Ubuntu 内最终程序位置：%s\n", bin)
	if !a.UI.Confirm("复制已校验的 OXGEN 并打开安装引导？") {
		return a.result(WSL, "跳过", "Ubuntu 已就绪；未复制工具箱", logPath)
	}
	for _, command := range [][]string{{"mkdir", "-p", "--", path.Dir(bin)}, {"cp", "--", strings.TrimSpace(mapped), tmp}, {"chmod", "755", tmp}, {"mv", "--", tmp, bin}, {bin, "--home", location, "--base-url", base}} {
		args := append([]string{"-d", distro, "--exec"}, command...)
		if err = a.Runner.Run(ctx, Command{Name: wsl, Args: args, Interactive: command[0] == bin}, out); err != nil {
			return a.result(WSL, "失败", err.Error(), logPath)
		}
	}
	return a.result(WSL, "成功", "Ubuntu 和内部 OXGEN 已可运行；内部组件结果以 Linux 安装引导的汇总为准", logPath)
}
