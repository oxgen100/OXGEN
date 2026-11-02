package oxgen

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (a *App) install(ctx context.Context, p InstallPlan, out io.Writer) (Record, error) {
	if isAgent(p.Tool) {
		return a.installAgent(ctx, p, out)
	}
	if p.Tool == VSCode {
		return a.installVSCode(ctx, p, out)
	}
	if a.Host.OS == "linux" && systemPackage(p.Tool) {
		return a.installAPT(ctx, p, out)
	}
	if a.Host.OS == "darwin" && p.Tool == Git {
		return a.installAppleGit(ctx, p, out)
	}
	if p.Tool == Python && a.Host.OS == "windows" {
		return a.installWindowsPython(ctx, p, out)
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
	var exe, source string
	switch p.Tool {
	case Node:
		if err = prepareVersionedTarget(p); err != nil {
			return Record{}, err
		}
		fmt.Fprintln(out, "正在解压至：", p.InstallDir)
		if err = extractArchiveContext(ctx, file, p.InstallDir, true); err != nil {
			return Record{}, err
		}
		exe = filepath.Join(p.InstallDir, "bin", "node")
		if a.Host.OS == "windows" {
			exe = filepath.Join(p.InstallDir, "node.exe")
		}
		if err = a.verifyNode(ctx, exe, out); err != nil {
			return Record{}, err
		}
		source = "OXGEN 官方发行包"
	case Git:
		args := []string{"/VERYSILENT", "/NORESTART", "/NOCLOSEAPPLICATIONS", "/DIR=" + p.InstallDir, "/o:PathOption=BashOnly", "/o:EditorOption=Notepad"}
		if p.Update && windowsProtected(p.InstallDir) {
			args = append(args, "/ALLUSERS")
			err = a.runElevated(ctx, file, args, out)
		} else {
			args = append(args, "/CURRENTUSER")
			err = a.Runner.Run(ctx, Command{Name: file, Args: args}, out)
		}
		if err != nil {
			return Record{}, err
		}
		exe = filepath.Join(p.InstallDir, "cmd", "git.exe")
		if _, err = a.Runner.Capture(ctx, Command{Name: exe, Args: []string{"--version"}}); err != nil {
			return Record{}, fmt.Errorf("安装后 Git 运行检查失败: %w", err)
		}
		source = "Git for Windows 官方安装器"
	case Python:
		sig, err := a.Runner.Capture(ctx, Command{Name: "pkgutil", Args: []string{"--check-signature", file}})
		if err != nil || !strings.Contains(sig, "Python Software Foundation") {
			return Record{}, fmt.Errorf("Python macOS 安装包发布者签名校验失败")
		}
		if err = a.rootRun(ctx, Command{Name: "/usr/sbin/installer", Args: []string{"-pkg", file, "-target", "/"}}, out); err != nil {
			return Record{}, err
		}
		minor := strings.Split(p.Release.Version, ".")
		exe = filepath.Join(p.InstallDir, "Versions", strings.Join(minor[:2], "."), "bin", "python3")
		if err = a.verifyPython(ctx, exe, p.DownloadDir, out); err != nil {
			return Record{}, err
		}
		source = "Python 官方 pkg"
	default:
		return Record{}, fmt.Errorf("不支持的安装组件")
	}
	versionOut, err := a.Runner.Capture(ctx, Command{Name: exe, Args: []string{"--version"}})
	if err != nil {
		return Record{}, err
	}
	if compareToolVersion(p.Tool, versionOut, p.Release.Version) != 0 {
		return Record{}, fmt.Errorf("安装后的版本与所选版本不一致: %s", strings.TrimSpace(versionOut))
	}
	return Record{Version: toolVersion(p.Tool, versionOut), Path: exe, Root: p.Root, Source: source, UpdatedAt: time.Now()}, nil
}

func (a *App) verifyNode(ctx context.Context, exe string, out io.Writer) error {
	if err := a.Runner.Run(ctx, Command{Name: exe, Args: []string{"--version"}}, out); err != nil {
		return err
	}
	cli := filepath.Join(filepath.Dir(exe), "..", "lib", "node_modules", "npm", "bin", "npm-cli.js")
	if a.Host.OS == "windows" {
		cli = filepath.Join(filepath.Dir(exe), "node_modules", "npm", "bin", "npm-cli.js")
	}
	return a.Runner.Run(ctx, Command{Name: exe, Args: []string{cli, "--version"}, Env: []string{"NPM_CONFIG_CACHE=" + filepath.Join(filepath.Dir(exe), ".oxgen-npm-cache"), "NPM_CONFIG_UPDATE_NOTIFIER=false"}}, out)
}

func (a *App) verifyPython(ctx context.Context, exe, work string, out io.Writer) error {
	if err := a.Runner.Run(ctx, Command{Name: exe, Args: []string{"-m", "pip", "--version"}}, out); err != nil {
		return fmt.Errorf("Python pip 验证失败: %w", err)
	}
	if err := ensureWritable(work); err != nil {
		return err
	}
	temp, err := os.MkdirTemp(work, ".oxgen-venv-*")
	if err != nil {
		return err
	}
	defer removeTemp(work, temp)
	venv := filepath.Join(temp, "environment")
	if err = a.Runner.Run(ctx, Command{Name: exe, Args: []string{"-m", "venv", venv}, Timeout: 3 * time.Minute}, out); err != nil {
		return fmt.Errorf("创建虚拟环境失败: %w", err)
	}
	python := filepath.Join(venv, "bin", "python")
	if a.Host.OS == "windows" {
		python = filepath.Join(venv, "Scripts", "python.exe")
	}
	if err = a.Runner.Run(ctx, Command{Name: python, Args: []string{"-c", "import sys; assert sys.prefix != sys.base_prefix; print('venv interpreter OK')"}}, out); err != nil {
		return err
	}
	return a.Runner.Run(ctx, Command{Name: python, Args: []string{"-m", "pip", "--version"}}, out)
}

func (a *App) installWindowsPython(ctx context.Context, p InstallPlan, out io.Writer) (Record, error) {
	if err := prepareVersionedTarget(p); err != nil {
		return Record{}, err
	}
	manager, err := a.Runner.LookPath("pymanager.exe")
	if err != nil {
		file, e := a.Client.Download(ctx, p.Release.Manager, p.DownloadDir, out)
		if e != nil {
			return Record{}, e
		}
		if e = a.verifySignature(ctx, file, "Python Software Foundation", out); e != nil {
			return Record{}, e
		}
		if e = a.powershell(ctx, "install-python-manager", appxScript, []string{"-Package", file}, out); e != nil {
			return Record{}, e
		}
		manager, err = a.Runner.LookPath("pymanager.exe")
		if err != nil {
			candidate := filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WindowsApps", "pymanager.exe")
			if _, e = os.Stat(candidate); e == nil {
				manager = candidate
				err = nil
			}
		}
		if err != nil {
			return Record{}, &pendingError{"Python 安装管理器已安装，请开启其应用执行别名或重新打开终端后继续。"}
		}
	}
	// The official manager downloads a COMPLETE runtime, verifies its official index,
	// and extracts it to the selected destination. No embeddable Python is used.
	cache := filepath.Join(p.DownloadDir, "python-"+p.Release.Version+"-"+a.Host.Arch)
	if err = ensureWritable(cache); err != nil {
		return Record{}, err
	}
	if err = a.Runner.Run(ctx, Command{Name: manager, Args: []string{"install", "--source=https://www.python.org/ftp/python/index-windows.json", "--download=" + cache, p.Release.Tag}}, out); err != nil {
		return Record{}, err
	}
	if err = a.Runner.Run(ctx, Command{Name: manager, Args: []string{"install", "--source=" + filepath.Join(cache, "index.json"), "--target=" + p.InstallDir, p.Release.Tag}}, out); err != nil {
		return Record{}, err
	}
	exe := filepath.Join(p.InstallDir, "python.exe")
	if err = a.prepareWindowsPython(ctx, exe, out); err != nil {
		return Record{}, err
	}
	if err = a.verifyPython(ctx, exe, p.DownloadDir, out); err != nil {
		return Record{}, err
	}
	v, err := a.Runner.Capture(ctx, Command{Name: exe, Args: []string{"--version"}})
	if err != nil {
		return Record{}, err
	}
	if compareVersion(v, p.Release.Version) != 0 {
		return Record{}, fmt.Errorf("Python 版本验证不一致")
	}
	return Record{Version: numericVersion(v), Path: exe, Root: p.Root, Source: "OXGEN Python 官方运行时", UpdatedAt: time.Now()}, nil
}

func windowsProtected(dir string) bool {
	dir = strings.ToLower(filepath.Clean(dir))
	for _, key := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432", "WINDIR"} {
		root := os.Getenv(key)
		if root != "" {
			root = strings.ToLower(filepath.Clean(root))
			if dir == root || strings.HasPrefix(dir, root+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

func (a *App) prepareWindowsPython(ctx context.Context, exe string, out io.Writer) error {
	if _, err := a.Runner.Capture(ctx, Command{Name: exe, Args: []string{"-m", "pip", "--version"}}); err != nil {
		if err = a.Runner.Run(ctx, Command{Name: exe, Args: []string{"-m", "ensurepip", "--upgrade", "--default-pip"}}, out); err != nil {
			return fmt.Errorf("为完整运行时准备内置 pip 失败: %w", err)
		}
	}
	for name, body := range map[string]string{"python3.cmd": "@echo off\r\n\"%~dp0python.exe\" %*\r\n", "pip.cmd": "@echo off\r\n\"%~dp0python.exe\" -m pip %*\r\n", "pip3.cmd": "@echo off\r\n\"%~dp0python.exe\" -m pip %*\r\n"} {
		file := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(file); os.IsNotExist(err) {
			if err = os.WriteFile(file, []byte(body), 0644); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *App) installAppleGit(ctx context.Context, p InstallPlan, out io.Writer) (Record, error) {
	_, cltErr := a.Runner.Capture(ctx, Command{Name: "xcode-select", Args: []string{"-p"}})
	if cltErr != nil {
		if err := a.Runner.Run(ctx, Command{Name: "xcode-select", Args: []string{"--install"}}, out); err != nil {
			return Record{}, fmt.Errorf("无法打开 Apple 安装向导: %w", err)
		}
		a.UI.Line("请在 macOS 的系统安装界面完成命令行工具安装。")
		if p.Automatic {
			return Record{}, &pendingError{"请完成 Apple 系统安装界面后重新运行一条龙；其余组件继续处理。"}
		}
		if !a.UI.Confirm("系统安装已完成，继续验证？") {
			return Record{}, &pendingError{"Apple 命令行工具等待系统安装完成；完成后重新检测。"}
		}
	} else {
		list, err := a.Runner.Capture(ctx, Command{Name: "softwareupdate", Args: []string{"--list"}, Timeout: 3 * time.Minute})
		if err != nil {
			return Record{}, err
		}
		label := ""
		for _, line := range strings.Split(list, "\n") {
			if strings.Contains(line, "Label:") && strings.Contains(line, "Command Line Tools") {
				_, label, _ = strings.Cut(line, "Label:")
				label = strings.TrimSpace(label)
			}
		}
		if label != "" && !p.Automatic && !a.UI.Confirm("Apple 提供更新 "+label+"，现在安装？") {
			return Record{}, errCancelled
		}
		if label != "" {
			if err = a.rootRun(ctx, Command{Name: "/usr/sbin/softwareupdate", Args: []string{"--install", label}}, out); err != nil {
				return Record{}, err
			}
		}
	}
	version, err := a.Runner.Capture(ctx, Command{Name: "/usr/bin/git", Args: []string{"--version"}})
	if err != nil {
		return Record{}, fmt.Errorf("Apple Git 尚不可用: %w", err)
	}
	return Record{Version: numericVersion(version), Path: "/usr/bin/git", Root: p.InstallDir, Source: "Apple 命令行开发工具", UpdatedAt: time.Now()}, nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

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

func prepareVersionedTarget(p InstallPlan) error {
	marker := filepath.Join(filepath.Dir(p.InstallDir), ".oxgen-target-"+filepath.Base(p.InstallDir)+".json")
	owner := struct {
		Path string `json:"path"`
		Tool Tool   `json:"tool"`
	}{p.InstallDir, p.Tool}
	if _, err := os.Lstat(p.InstallDir); err == nil {
		var previous struct {
			Path string `json:"path"`
			Tool Tool   `json:"tool"`
		}
		b, e := os.ReadFile(marker)
		if e != nil || json.Unmarshal(b, &previous) != nil || !samePath(previous.Path, p.InstallDir) || previous.Tool != p.Tool {
			return fmt.Errorf("所选版本目录已存在且不是可确认的 OXGEN 安装，未覆盖: %s", p.InstallDir)
		}
		if err = os.Rename(p.InstallDir, p.InstallDir+fmt.Sprintf(".previous-%d", time.Now().UnixNano())); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return saveJSON(marker, owner)
}

func (a *App) addPath(ctx context.Context, record Record, out io.Writer) error {
	i, err := a.UI.Choice("配置新安装程序的命令入口", []string{"加入当前用户 PATH", "仅本次使用", "保持现状"}, 0)
	if err != nil {
		return err
	}
	return a.setPath(ctx, record, out, i)
}

func (a *App) setPath(ctx context.Context, record Record, out io.Writer, i int) error {
	if record.Source == workBuddySource {
		return nil
	}
	var err error
	dirs := []string{filepath.Dir(record.Path)}
	if record.Source == vscodeSource {
		root := vscodeRootFromPath(record.Path, a.Host.OS)
		_, _, bin := vscodeLayout(root, a.Host.OS)
		dirs = []string{bin}
	}
	if a.Host.OS == "windows" && strings.Contains(record.Source, "Python") {
		dirs = append(dirs, filepath.Join(filepath.Dir(record.Path), "Scripts"))
	}
	if strings.HasPrefix(record.Path, "/usr/bin/") {
		return nil
	}
	if i == 2 {
		return nil
	}
	if i == 0 {
		if a.Host.OS == "windows" {
			b, _ := json.Marshal(dirs)
			if err = a.powershell(ctx, "add-user-path", pathScript, []string{"-PathsJSON", string(b)}, out); err != nil {
				return err
			}
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			line := "export PATH=" + shellQuote(strings.Join(dirs, string(os.PathListSeparator))) + ":\"$PATH\""
			for _, file := range shellProfiles(home, os.Getenv("SHELL"), a.Host.OS == "darwin") {
				if b, e := os.ReadFile(file); e == nil && strings.Contains(string(b), line) {
				} else {
					f, e := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
					if e != nil {
						return e
					}
					_, err = f.WriteString("\n# OXGEN managed command path\n" + line + "\n")
					f.Close()
					if err != nil {
						return err
					}
				}
			}
			fmt.Fprintln(out, "已添加用户 shell 配置。请打开新登录终端，或读取相应配置文件。")
		}
	}
	_ = os.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator))+string(os.PathListSeparator)+os.Getenv("PATH"))
	fmt.Fprintln(out, "请在新的终端执行 oxgen doctor，核对命令实际生效位置；已有系统级命令可能仍优先。")
	return nil
}
