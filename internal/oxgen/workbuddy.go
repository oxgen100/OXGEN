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

func (a *App) workBuddyVersion(ctx context.Context, exe string) (string, error) {
	if f, e := os.Stat(exe); e != nil || !f.Mode().IsRegular() {
		return "", fmt.Errorf("WorkBuddy 主程序尚未就绪")
	}
	var command Command
	if a.Host.OS == "windows" {
		ps, err := a.Runner.LookPath("powershell.exe")
		if err != nil {
			return "", err
		}
		command = Command{Name: ps, Args: []string{"-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; [Diagnostics.FileVersionInfo]::GetVersionInfo($env:OXGEN_WORKBUDDY_EXE).ProductVersion"}, Env: []string{"OXGEN_WORKBUDDY_EXE=" + exe}}
	} else {
		command = Command{Name: "/usr/bin/plutil", Args: []string{"-extract", "CFBundleShortVersionString", "raw", "-o", "-", filepath.Join(filepath.Dir(filepath.Dir(exe)), "Info.plist")}}
	}
	text, err := a.Runner.Capture(ctx, command)
	if err != nil {
		return "", err
	}
	version := numericVersion(text)
	if version == "" {
		return "", fmt.Errorf("WorkBuddy 应用版本无法确认")
	}
	return version, nil
}

func (a *App) detectWorkBuddy(ctx context.Context) Detection {
	d := Detection{Tool: WorkBuddy}
	if a.Host.OS == "linux" {
		d.Details = []string{"腾讯官方未提供 Ubuntu/Debian 安装包。"}
		return d
	}
	var candidates []string
	if r, ok := a.State.Records[WorkBuddy]; ok {
		candidates = append(candidates, r.Path)
	}
	if p, e := a.Runner.LookPath("WorkBuddy"); e == nil {
		candidates = append(candidates, p)
	}
	user, _ := os.UserHomeDir()
	if a.Host.OS == "windows" {
		for _, base := range []string{os.Getenv("LOCALAPPDATA"), os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
			if base != "" {
				candidates = append(candidates, filepath.Join(base, "Programs", "WorkBuddy", "WorkBuddy.exe"), filepath.Join(base, "WorkBuddy", "WorkBuddy.exe"))
			}
		}
		if ps, err := a.Runner.LookPath("powershell.exe"); err == nil {
			// Read only the app's uninstall registration, which also finds installs
			// on a custom drive made outside OXGEN. Never invoke the uninstaller.
			body := "$keys=@('HKCU:\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\*','HKLM:\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\*','HKLM:\\Software\\WOW6432Node\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\*'); $paths=@(Get-Process -Name WorkBuddy -ErrorAction SilentlyContinue | ForEach-Object { if ($_.Path) { [IO.Path]::GetDirectoryName($_.Path) } }); $paths+=@(Get-ItemProperty -Path $keys -ErrorAction SilentlyContinue | Where-Object { $_.DisplayName -eq 'WorkBuddy' } | ForEach-Object { $_.InstallLocation } | Where-Object { $_ }); ConvertTo-Json -InputObject $paths -Compress"
			if text, err := a.Runner.Capture(ctx, Command{Name: ps, Args: []string{"-NoProfile", "-NonInteractive", "-Command", body}}); err == nil {
				var roots []string
				if json.Unmarshal([]byte(text), &roots) == nil {
					for _, root := range roots {
						if filepath.IsAbs(root) {
							candidates = append(candidates, filepath.Join(root, "WorkBuddy.exe"))
						}
					}
				}
			}
		}
	} else {
		for _, root := range []string{"/Applications", filepath.Join(user, "Applications")} {
			candidates = append(candidates, filepath.Join(root, "WorkBuddy.app", "Contents", "MacOS", "WorkBuddy"))
		}
	}
	for _, path := range candidates {
		if ctx.Err() != nil {
			return d
		}
		if v, err := a.workBuddyVersion(ctx, path); err == nil {
			d.Installed, d.Healthy, d.Active, d.Version, d.Path, d.Source = true, true, true, v, path, "现有 WorkBuddy 桌面应用"
			if r, ok := a.State.Records[WorkBuddy]; ok && samePath(r.Path, path) {
				d.Source = r.Source
			}
			return d
		}
	}
	return d
}

const workBuddyNSIS = `param([Parameter(Mandatory=$true)][string]$Installer,[Parameter(Mandatory=$true)][string]$Directory)
$ErrorActionPreference = 'Stop'
if ($Directory.Contains('"') -or $Directory.Contains([char]10) -or $Directory.Contains([char]13)) { throw '安装目录包含无效字符' }
if (@(Get-Process -Name WorkBuddy -ErrorAction SilentlyContinue).Count -gt 0) { throw '请先关闭正在运行的 WorkBuddy，再重新安装；不会强行结束应用。' }
# NSIS requires /D to be last, with its directory unquoted even if it has spaces.
$process = Start-Process -FilePath $Installer -ArgumentList ('/S /currentuser /D=' + $Directory) -WindowStyle Hidden -Wait -PassThru
if ($process.ExitCode -ne 0) { throw ('WorkBuddy 安装器退出码: ' + $process.ExitCode) }
`

func (a *App) installWorkBuddy(ctx context.Context, p InstallPlan, out io.Writer) (Record, error) {
	if err := a.agentPrerequisite(ctx, WorkBuddy); err != nil {
		return Record{}, err
	}
	if a.Host.OS == "windows" {
		ps, err := a.Runner.LookPath("powershell.exe")
		if err != nil {
			return Record{}, err
		}
		status, err := a.Runner.Capture(ctx, Command{Name: ps, Args: []string{"-NoProfile", "-NonInteractive", "-Command", "[int](@(Get-Process -Name WorkBuddy -ErrorAction SilentlyContinue).Count -gt 0)"}})
		if err != nil {
			return Record{}, &pendingError{"无法确认 WorkBuddy 是否正在运行，请检查后重试。"}
		}
		if strings.TrimSpace(status) == "1" {
			return Record{}, &pendingError{"请先关闭正在运行的 WorkBuddy，再重新安装；未下载或改动现有应用。"}
		}
	}
	file, err := a.Client.Download(ctx, p.Release.Artifact, p.DownloadDir, out)
	if err != nil {
		return Record{}, err
	}
	var exe string
	if a.Host.OS == "windows" {
		if err = a.verifySignature(ctx, file, "Tencent WorkBuddy", out); err != nil {
			return Record{}, err
		}
		if p.Current.Installed && !samePath(filepath.Dir(p.Current.Path), p.InstallDir) {
			return Record{}, fmt.Errorf("WorkBuddy 更新须沿用原目录，不自动跨盘迁移")
		}
		if !p.Current.Installed {
			if _, e := os.Lstat(p.InstallDir); e == nil {
				return Record{}, fmt.Errorf("WorkBuddy 目标目录已存在，未覆盖；请选择空目录的上一级")
			} else if !os.IsNotExist(e) {
				return Record{}, e
			}
		}
		if err = a.powershell(ctx, "install-workbuddy", workBuddyNSIS, []string{"-Installer", file, "-Directory", p.InstallDir}, out); err != nil {
			return Record{}, err
		}
		exe = filepath.Join(p.InstallDir, "WorkBuddy.exe")
	} else {
		if err = prepareVersionedTarget(p); err != nil {
			return Record{}, err
		}
		mount, e := os.MkdirTemp(p.DownloadDir, ".oxgen-workbuddy-mount-*")
		if e != nil {
			return Record{}, e
		}
		defer os.Remove(mount) // Never recursively delete a mounted filesystem.
		if err = a.Runner.Run(ctx, Command{Name: "/usr/bin/hdiutil", Args: []string{"attach", file, "-readonly", "-nobrowse", "-mountpoint", mount}}, out); err != nil {
			return Record{}, err
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if e := a.Runner.Run(cleanup, Command{Name: "/usr/bin/hdiutil", Args: []string{"detach", mount}}, out); e != nil {
				fmt.Fprintln(out, "请在 Finder 中弹出 WorkBuddy 安装映像：", mount)
			}
		}()
		app := filepath.Join(mount, "WorkBuddy.app")
		if _, err = os.Stat(app); err != nil {
			return Record{}, fmt.Errorf("官方 DMG 中没有 WorkBuddy.app: %w", err)
		}
		if err = a.Runner.Run(ctx, Command{Name: "/usr/bin/codesign", Args: []string{"--verify", "--deep", "--strict", app}}, out); err != nil {
			return Record{}, err
		}
		if err = a.Runner.Run(ctx, Command{Name: "/usr/sbin/spctl", Args: []string{"--assess", "--type", "execute", app}}, out); err != nil {
			return Record{}, err
		}
		if err = os.MkdirAll(p.InstallDir, 0755); err != nil {
			return Record{}, err
		}
		dest := filepath.Join(p.InstallDir, "WorkBuddy.app")
		if err = a.Runner.Run(ctx, Command{Name: "/usr/bin/ditto", Args: []string{app, dest}}, out); err != nil {
			return Record{}, err
		}
		exe = filepath.Join(dest, "Contents", "MacOS", "WorkBuddy")
	}
	version, err := a.workBuddyVersion(ctx, exe)
	if err != nil || version != p.Release.Version {
		return Record{}, fmt.Errorf("WorkBuddy 实际安装位置或应用版本验证失败: %s (%v)", version, err)
	}
	fmt.Fprintln(out, "已验证 WorkBuddy 应用：", version, exe)
	return Record{Version: version, Path: exe, Root: p.Root, Source: workBuddySource, UpdatedAt: time.Now()}, nil
}
