package oxgen

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (a *App) detect(ctx context.Context, tool Tool) Detection {
	if isAgent(tool) {
		return a.detectAgent(ctx, tool)
	}
	if tool == VSCode {
		return a.detectVSCode(ctx)
	}
	d := Detection{Tool: tool}
	names := map[Tool][]string{Git: {"git"}, Node: {"node"}, Python: {"python3", "python"}, WSL: {"wsl.exe"}}[tool]
	if a.Host.OS == "windows" && tool == Python {
		names = []string{"python", "python3"}
		if manager, e := a.Runner.LookPath("pymanager.exe"); e == nil {
			if output, e := a.Runner.Capture(ctx, Command{Name: manager, Args: []string{"list", "--format=json"}}); e == nil {
				names = append(names, pythonManagerPaths(output)...)
			}
		}
	}
	if record, ok := a.State.Records[tool]; ok && record.Path != "" {
		names = append(names, record.Path)
	}
	for _, name := range names {
		p := name
		var err error
		active := !filepath.IsAbs(name)
		if active {
			p, err = a.Runner.LookPath(name)
		}
		if err != nil {
			continue
		}
		if tool == Python && a.Host.OS == "windows" && strings.Contains(strings.ToLower(p), "windowsapps") {
			continue
		}
		if tool == Git && a.Host.OS == "darwin" && p == "/usr/bin/git" {
			if _, err = a.Runner.Capture(ctx, Command{Name: "xcode-select", Args: []string{"-p"}}); err != nil {
				d.Details = append(d.Details, "Apple 命令行工具尚未就绪；检测不会触发安装弹窗。")
				continue
			}
		}
		out, err := a.Runner.Capture(ctx, Command{Name: p, Args: []string{"--version"}})
		if err != nil {
			d.Details = append(d.Details, fmt.Sprintf("%s 无法运行: %s %v", p, strings.TrimSpace(out), err))
			continue
		}
		d.Path = p
		d.Version = toolVersion(tool, out)
		d.Installed = d.Version != ""
		d.Active = active
		d.Source = "现有安装"
		d.Healthy = d.Installed
		if tool == WSL {
			d.Installed = true
			d.Healthy = true
			d.Details = append(d.Details, strings.TrimSpace(out))
			if names, e := a.Runner.Capture(ctx, Command{Name: p, Args: []string{"--list", "--quiet"}}); e != nil {
				d.Healthy = false
				d.Details = append(d.Details, "WSL 命令已安装，但发行版查询失败: "+e.Error())
			} else {
				d.Details = append(d.Details, "已安装发行版: "+strings.ReplaceAll(strings.TrimSpace(names), "\n", ", "))
			}
			return d
		}
		if record, ok := a.State.Records[tool]; ok && samePath(record.Path, p) {
			d.Source = record.Source
		}
		if a.Host.OS == "linux" && (strings.HasPrefix(p, "/usr/bin/") || strings.HasPrefix(p, "/bin/")) {
			d.Source = "系统软件包"
		}
		if tool == Git && a.Host.OS == "darwin" && p == "/usr/bin/git" {
			d.Source = "Apple 命令行开发工具"
		}
		if tool == Git && a.Host.OS == "windows" {
			root := filepath.Dir(filepath.Dir(p))
			if _, err = os.Stat(filepath.Join(root, "git-bash.exe")); err == nil {
				d.Source = "Git for Windows 官方安装器"
			}
		}
		if tool == Python {
			out, e := a.Runner.Capture(ctx, Command{Name: p, Args: []string{"-c", "import sys; print(sys.executable)"}})
			if e == nil && filepath.IsAbs(strings.TrimSpace(out)) {
				d.Path = strings.TrimSpace(out)
			}
			for _, probe := range []struct {
				args []string
				name string
			}{{[]string{"-m", "pip", "--version"}, "pip"}, {[]string{"-c", "import venv, ensurepip; print('venv/ensurepip OK')"}, "venv/ensurepip"}} {
				s, e := a.Runner.Capture(ctx, Command{Name: d.Path, Args: probe.args})
				if e != nil {
					d.Healthy = false
					d.Details = append(d.Details, probe.name+" 尚不可用")
				} else {
					d.Details = append(d.Details, strings.TrimSpace(s))
				}
			}
		}
		if tool == Node {
			if !active {
				cli := filepath.Join(filepath.Dir(p), "..", "lib", "node_modules", "npm", "bin", "npm-cli.js")
				if a.Host.OS == "windows" {
					cli = filepath.Join(filepath.Dir(p), "node_modules", "npm", "bin", "npm-cli.js")
				}
				if output, e := a.Runner.Capture(ctx, Command{Name: p, Args: []string{cli, "--version"}}); e != nil {
					d.Healthy = false
					d.Details = append(d.Details, "已记录运行时的 npm 尚不可用")
				} else {
					d.Details = append(d.Details, "npm "+strings.TrimSpace(output))
				}
			} else if npm, e := a.Runner.LookPath("npm"); e == nil {
				out, e := a.captureNPM(ctx, npm)
				if e != nil {
					d.Healthy = false
					d.Details = append(d.Details, "npm 无法运行")
				} else {
					d.Details = append(d.Details, "npm "+strings.TrimSpace(out)+" · "+npm)
				}
			} else {
				d.Healthy = false
				d.Details = append(d.Details, "缺少 npm")
			}
		}
		if !active {
			d.Details = append(d.Details, "已检测到安装，但当前终端 PATH 尚未指向它。")
		}
		return d
	}
	if record, ok := a.State.Records[tool]; ok && record.Path != "" {
		d.Details = append(d.Details, "OXGEN 记录的安装位置（当前 PATH 未生效）: "+record.Path)
	}
	return d
}

func pythonManagerPaths(text string) []string {
	var entries []struct {
		Executable string `json:"executable"`
		Prefix     string `json:"prefix"`
	}
	if json.Unmarshal([]byte(text), &entries) != nil {
		var index struct {
			Versions []struct {
				Executable string `json:"executable"`
				Prefix     string `json:"prefix"`
			} `json:"versions"`
		}
		if json.Unmarshal([]byte(text), &index) != nil {
			return nil
		}
		entries = index.Versions
	}
	var paths []string
	for _, entry := range entries {
		p := entry.Executable
		if !filepath.IsAbs(p) && entry.Prefix != "" {
			p = filepath.Join(entry.Prefix, p)
		}
		if filepath.IsAbs(p) {
			paths = append(paths, p)
		}
	}
	return paths
}

func samePath(a, b string) bool {
	if filepath.Separator == '\\' {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func (a *App) captureNPM(ctx context.Context, path string) (string, error) {
	if a.Host.OS == "windows" {
		// npm.cmd requires cmd.exe; npm-cli.js can be executed without changing PowerShell policy.
		root := filepath.Dir(path)
		cli := filepath.Join(root, "node_modules", "npm", "bin", "npm-cli.js")
		if _, e := os.Stat(cli); e == nil {
			node, e := a.Runner.LookPath("node")
			if e != nil {
				return "", e
			}
			return a.Runner.Capture(ctx, Command{Name: node, Args: []string{cli, "--version"}})
		}
		return "", fmt.Errorf("无法定位 npm-cli.js")
	}
	return a.Runner.Capture(ctx, Command{Name: path, Args: []string{"--version"}})
}
