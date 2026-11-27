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
)

type commandEntry struct {
	Name string
	Path string
}

func (a *App) commandDirs(tool Tool, exe string) []string {
	if tool == VSCode {
		root := vscodeRootFromPath(exe, a.Host.OS)
		_, _, bin := vscodeLayout(root, a.Host.OS)
		return []string{bin}
	}
	dir := filepath.Dir(exe)
	if tool == Python && a.Host.OS == "windows" {
		return []string{dir, filepath.Join(dir, "Scripts")}
	}
	return []string{dir}
}

func sameCommandFile(a, b string) bool {
	if samePath(a, b) {
		return true
	}
	left, lerr := os.Stat(a)
	right, rerr := os.Stat(b)
	return lerr == nil && rerr == nil && os.SameFile(left, right)
}

func (a *App) detectedCommandEntry(d Detection) (commandEntry, bool) {
	if !filepath.IsAbs(d.Path) {
		return commandEntry{}, false
	}
	names := []string{string(d.Tool)}
	suffix := ""
	if d.Tool == VSCode {
		names = []string{"code"}
	} else if d.Tool == Python {
		names = []string{"python3", "python"}
	}
	if a.Host.OS == "windows" {
		suffix = ".exe"
		if d.Tool == VSCode {
			suffix = ".cmd"
		} else if d.Tool == Python {
			names = []string{"python", "python3"}
		}
	}
	dir := a.commandDirs(d.Tool, d.Path)[0]
	for _, name := range names {
		path := filepath.Join(dir, name+suffix)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && (d.Tool == VSCode || sameCommandFile(path, d.Path)) {
			return commandEntry{Name: name, Path: path}, true
		}
	}
	return commandEntry{}, false
}

func (a *App) repairCommandPath(ctx context.Context, p InstallPlan) Result {
	entry := *p.Repair
	if info, err := os.Stat(entry.Path); err != nil || !info.Mode().IsRegular() {
		return a.result(p.Tool, "待用户操作", "已确认的命令入口不再可用，请重新检测："+entry.Path, "")
	}
	if err := a.setUserPath(ctx, a.commandDirs(p.Tool, p.Current.Path), a.UI.Out); err != nil {
		return a.result(p.Tool, "待用户操作", "配置当前用户命令路径未完成："+err.Error(), "")
	}
	actual, err := a.effectiveCommandPath(ctx, entry.Name)
	if err != nil || actual == "" {
		return a.result(p.Tool, "待用户操作", "已配置用户 PATH，但无法确认命令是否生效。请在新终端核对："+entry.Name, "")
	}
	if !sameCommandFile(entry.Path, actual) {
		return a.result(p.Tool, "待用户操作", "命令 "+entry.Name+" 仍会优先运行："+actual+"；已配置的入口："+entry.Path, "")
	}
	return a.result(p.Tool, "成功", "已配置命令入口："+actual, "")
}

const effectiveWindowsPathScript = `
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$paths = @([Environment]::GetEnvironmentVariable('Path','Machine'), [Environment]::GetEnvironmentVariable('Path','User'))
$env:Path = [Environment]::ExpandEnvironmentVariables(($paths -join ';'))
Get-Command -Name $env:OXGEN_REPAIR_COMMAND -CommandType Application -ErrorAction SilentlyContinue |
  Select-Object -First 1 -ExpandProperty Source
`

func (a *App) effectiveCommandPath(ctx context.Context, name string) (string, error) {
	if a.Host.OS != "windows" {
		return a.Runner.LookPath(name)
	}
	ps, err := a.Runner.LookPath("powershell.exe")
	if err != nil {
		return "", err
	}
	text, err := a.Runner.Capture(ctx, Command{Name: ps, Args: []string{"-NoProfile", "-NonInteractive", "-Command", effectiveWindowsPathScript}, Env: []string{"OXGEN_REPAIR_COMMAND=" + name}})
	return strings.TrimSpace(text), err
}

func shellProfiles(home, shell string, mac bool) []string {
	switch filepath.Base(shell) {
	case "zsh":
		return []string{filepath.Join(home, ".zprofile"), filepath.Join(home, ".zshrc")}
	case "bash":
		login := ".profile"
		if mac {
			login = ".bash_profile"
		}
		return []string{filepath.Join(home, login), filepath.Join(home, ".bashrc")}
	default:
		return []string{filepath.Join(home, ".profile")}
	}
}

func (a *App) setUserPath(ctx context.Context, dirs []string, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, dir := range dirs {
		if !filepath.IsAbs(dir) || strings.ContainsRune(dir, os.PathListSeparator) || strings.ContainsAny(dir, "\r\n\x00") {
			return fmt.Errorf("目录无法写入 PATH: %s", dir)
		}
	}
	prefix := strings.Join(dirs, string(os.PathListSeparator))
	if a.Host.OS == "windows" {
		b, _ := json.Marshal(dirs)
		if err := a.powershell(ctx, "add-user-path", pathScript, []string{"-PathsJSON", string(b)}, out); err != nil {
			return err
		}
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		line := "export PATH=" + shellQuote(prefix) + ":\"$PATH\""
		for _, file := range shellProfiles(home, os.Getenv("SHELL"), a.Host.OS == "darwin") {
			if b, e := os.ReadFile(file); e == nil && strings.Contains(string(b), line) {
				continue
			}
			f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return err
			}
			_, err = f.WriteString("\n# OXGEN managed command path\n" + line + "\n")
			if err = errors.Join(err, f.Close()); err != nil {
				return err
			}
		}
		fmt.Fprintln(out, "已添加用户 shell 配置。请打开新登录终端，或读取相应配置文件。")
	}
	if err := os.Setenv("PATH", prefix+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
		return err
	}
	fmt.Fprintln(out, "请在新的终端执行 oxgen doctor，核对命令实际生效位置。")
	return nil
}
