package oxgen

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

func (a *App) chooseDir(prompt, def string) (string, error) {
	for {
		value, err := a.UI.AskValue(prompt+"（直接粘贴，回车用默认目录）", def)
		if err != nil {
			return "", err
		}
		value = strings.TrimSpace(strings.Trim(value, "\""))
		if !filepath.IsAbs(value) || strings.ContainsAny(value, "\r\n\x00") {
			a.UI.Warn("请粘贴完整绝对目录，输入 0 返回。")
			continue
		}
		value = filepath.Clean(value)
		if value == filepath.VolumeName(value)+string(filepath.Separator) {
			a.UI.Warn("请使用磁盘中的具体文件夹；输入 0 返回。")
			continue
		}
		return value, nil
	}
}

func locatePlan(p *InstallPlan, root, downloads string) {
	p.DownloadDir = downloads
	if !p.Fixed {
		p.Root, p.InstallDir = root, root
		if p.Tool == Node || p.Tool == Python || p.Tool == VSCode || isAgent(p.Tool) && p.Tool != WorkBuddy {
			p.InstallDir = filepath.Join(root, "versions", p.Release.Version)
		}
		if p.Tool == WorkBuddy {
			p.InstallDir = filepath.Join(root, "WorkBuddy")
		}
	}
	p.Automatic = true
}

func (a *App) pickLocations(p *InstallPlan) error {
	if p.Fixed {
		a.UI.Line("实际安装位置固定：" + p.InstallDir + "；下面粘贴的目录仅存放下载包。")
	}
	prompt := "粘贴 " + p.Tool.String() + " 的安装目录"
	def := p.Root
	if p.Fixed {
		prompt = "粘贴下载包存放目录"
		def = filepath.Join(a.Home, "downloads", string(p.Tool))
	}
	root, err := a.chooseDir(prompt, def)
	if err != nil {
		return err
	}
	// With a system-managed component the pasted folder is only a download
	// workspace, and the fixed location is shown explicitly before confirmation.
	locatePlan(p, root, filepath.Join(root, "downloads"))
	if p.Fixed {
		p.DownloadDir = root
	}
	a.State.LastDownload = p.DownloadDir
	return nil
}

func (a *App) showPlan(p InstallPlan) {
	a.UI.Print("\n%s %s\n  下载：%s\n  安装：%s\n", a.UI.Accent(p.Tool.String()), p.Release.Version, p.DownloadDir, p.InstallDir)
	for _, extra := range p.Extra {
		a.UI.Line("  " + extra)
	}
}

func (a *App) queryRelease(ctx context.Context, tool Tool) (Release, error) {
	for {
		r, err := a.latest(ctx, tool)
		if err == nil {
			return r, nil
		}
		if ctx.Err() != nil {
			return Release{}, ctx.Err()
		}
		a.UI.Warn("推荐版本：暂时无法查询 · " + err.Error())
		opts := []string{"重试官方查询"}
		if a.Host.OS == "linux" && systemPackage(tool) || a.Host.OS == "darwin" && tool == Git {
			opts = append(opts, "使用系统官方渠道继续")
		}
		i, e := a.UI.Choice("如何继续？", opts, -1)
		if e != nil {
			return Release{}, e
		}
		if i == 1 {
			return Release{Channel: "系统官方渠道（安装时核对候选版本）"}, nil
		}
	}
}

func (a *App) manage(ctx context.Context, tool Tool) Result {
	var result Result
	watch := a.Host.OS == "windows" || tool == Node || tool == VSCode || isAgent(tool)
	a.UI.Run(ctx, watch, func(ctx context.Context) { result = a.manageSingle(ctx, tool) })
	return result
}

func (a *App) manageSingle(ctx context.Context, tool Tool) Result {
	a.UI.Line("0. 返回上一级（执行中输入后回车，取消后续步骤）")
	if tool == VSCode && a.Host.OS == "linux" && runningInWSL() {
		return a.result(tool, "待用户操作", "请在 Windows 主机安装 VS Code，再用微软 WSL 扩展连接 Ubuntu。", "")
	}
	d := a.detect(ctx, tool)
	if ctx.Err() != nil {
		return a.result(tool, "跳过", "已取消当前操作", "")
	}
	a.showDetection(d)
	if d.Installed {
		label := "更新到官方推荐版本"
		if a.Host.OS == "linux" && systemPackage(tool) {
			label = "刷新官方仓库并检查更新／补齐组件"
		}
		i, err := a.UI.Choice("已有环境", []string{"保留当前版本", label}, 0)
		if err != nil {
			return a.result(tool, "跳过", "未改动现有安装", "")
		}
		if i == 0 {
			if isAgent(tool) {
				if _, known := a.State.Records[tool]; !known {
					a.State.Records[tool] = Record{Version: d.Version, Path: d.Path, Root: filepath.Dir(d.Path), Source: d.Source}
				}
			}
			return a.result(tool, "保留", d.Version+" · "+d.Path, "")
		}
	}
	if isAgent(tool) {
		if err := a.agentPrerequisite(ctx, tool); err != nil {
			return a.result(tool, "待用户操作", err.Error(), "")
		}
	}
	r, err := a.queryRelease(ctx, tool)
	if err != nil {
		return a.result(tool, "跳过", "未取得推荐版本，未执行安装", "")
	}
	a.showRelease(r)
	currentEnough := compareToolVersion(tool, d.Version, r.Version) >= 0
	if isAgent(tool) {
		currentEnough = compareAgentVersion(d.Version, r.Version) >= 0
	}
	if d.Installed && d.Healthy && r.Version != "" && currentEnough && !(a.Host.OS == "linux" && systemPackage(tool)) {
		return a.result(tool, "保留", "已达到该渠道推荐版本 · "+d.Path, "")
	}
	p, err := a.planInstall(tool, r, d, d.Installed)
	if err != nil {
		return a.result(tool, "失败", err.Error(), "")
	}
	if err = a.pickLocations(&p); err != nil {
		return a.result(tool, "跳过", err.Error(), "")
	}
	a.showPlan(p)
	if tool != WorkBuddy {
		a.UI.Line("安装完成后配置当前用户 PATH。")
	}
	if !a.UI.Confirm("按上面的清单开始安装？") {
		return a.result(tool, "跳过", "安装前取消", "")
	}
	if a.Host.OS == "windows" || tool == Node || tool == VSCode || isAgent(tool) {
		a.UI.Line("执行期间输入 0 并回车，可取消后续步骤。")
	}
	return a.executePlan(ctx, p)
}

func (a *App) oneStop(ctx context.Context) {
	a.UI.Run(ctx, a.Host.OS == "windows", func(ctx context.Context) { a.installMissing(ctx) })
}

func (a *App) installMissing(ctx context.Context) {
	a.UI.Section("一条龙安装 · 先检测，只补缺失环境")
	a.UI.Line("0. 返回上一级")
	var results []Result
	var needs []Detection
	for _, tool := range coreTools {
		if ctx.Err() != nil {
			return
		}
		a.UI.Print("正在检测 %s...\n", tool.String())
		d := a.detect(ctx, tool)
		if ctx.Err() != nil {
			return
		}
		if tool == VSCode && a.Host.OS == "linux" && runningInWSL() {
			results = append(results, Result{Tool: tool, Status: "待用户操作", Message: "在 Windows 主机安装 VS Code，再连接 WSL。"})
			a.UI.Line("  在 Windows 主机管理 VS Code")
		} else if d.Installed && d.Healthy {
			results = append(results, Result{Tool: tool, Status: "保留", Message: d.Version + " · " + d.Path})
			a.UI.Print("  已有 %s · 保留\n", d.Version)
		} else {
			needs = append(needs, d)
			a.UI.Line("  缺失或不完整 · 准备安装")
		}
	}
	if len(needs) == 0 {
		a.UI.Line("检测完成，没有需要补装的环境。")
		a.summary(results)
		return
	}
	var plans []InstallPlan
	for _, d := range needs {
		if ctx.Err() != nil {
			return
		}
		r, err := a.latest(ctx, d.Tool)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			a.UI.Warn(d.Tool.String() + "：暂时无法查询官方版本，先跳过，完成后可单项重试。")
			results = append(results, Result{Tool: d.Tool, Status: "失败", Message: "官方版本查询失败，未安装：" + err.Error()})
			continue
		}
		p, err := a.planInstall(d.Tool, r, d, d.Installed)
		if err != nil {
			results = append(results, Result{Tool: d.Tool, Status: "失败", Message: err.Error()})
			continue
		}
		plans = append(plans, p)
	}
	if len(plans) == 0 {
		a.summary(results)
		return
	}
	root, err := a.chooseDir("粘贴总安装目录", filepath.Join(a.Home, "tools"))
	if err != nil {
		return
	}
	a.UI.Section("本次安装清单")
	for i := range plans {
		p := &plans[i]
		locatePlan(p, filepath.Join(root, string(p.Tool)), filepath.Join(root, "downloads", string(p.Tool)))
		a.showPlan(*p)
	}
	a.UI.Line("只补上面列出的组件；保留已有可用版本。安装后统一配置当前用户 PATH。")
	if !a.UI.Confirm("开始一条龙安装？") {
		return
	}
	for i, p := range plans {
		if ctx.Err() != nil {
			break
		}
		a.UI.Section(fmt.Sprintf("%d/%d · %s", i+1, len(plans), p.Tool.String()))
		if a.Host.OS == "windows" {
			a.UI.Line("0. 取消后续安装（输入后回车）")
		}
		r := a.executePlan(ctx, p)
		results = append(results, r)
		if a.UI.cancelled.Load() || errors.Is(ctx.Err(), context.Canceled) {
			for _, remaining := range plans[i+1:] {
				results = append(results, Result{Tool: remaining.Tool, Status: "跳过", Message: "用户取消后续安装"})
			}
			break
		}
	}
	a.summary(results)
}
