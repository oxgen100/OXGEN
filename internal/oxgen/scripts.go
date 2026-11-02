package oxgen

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const signatureScript = `param([Parameter(Mandatory=$true)][string]$File,[Parameter(Mandatory=$true)][string]$Publisher)
$ErrorActionPreference = 'Stop'
$signature = Get-AuthenticodeSignature -LiteralPath $File
if ($signature.Status -ne 'Valid') { throw ('发布者签名未通过验证: ' + $signature.Status) }
$expected = switch ($Publisher) {
  'Git for Windows' { 'Git for Windows|Johannes Schindelin' }
  'Python Software Foundation' { 'Python Software Foundation' }
  'Anthropic' { 'CN="?Anthropic,? PBC"?(?:,|$)' }
  'Tencent WorkBuddy' { 'CN=Tencent Technology \(Shenzhen\) Company Limited(?:,|$)' }
  default { throw '未知发布者' }
}
if ($signature.SignerCertificate.Subject -notmatch $expected) { throw '安装包签名发布者不匹配' }
if ($Publisher -eq 'Tencent WorkBuddy' -and [Diagnostics.FileVersionInfo]::GetVersionInfo($File).ProductName -ne 'WorkBuddy') { throw '腾讯签名有效，但安装包不是 WorkBuddy' }
Write-Output ('签名已验证: ' + $signature.SignerCertificate.Subject)
`

const appxScript = `param([Parameter(Mandatory=$true)][string]$Package)
$ErrorActionPreference = 'Stop'
Add-AppxPackage -Path $Package -ErrorAction Stop
Write-Output 'Python 官方安装管理器安装完成。'
`

const pathScript = `param([Parameter(Mandatory=$true)][string]$PathsJSON)
$ErrorActionPreference = 'Stop'
$newPaths = @(ConvertFrom-Json -InputObject $PathsJSON)
$oldPath = [Environment]::GetEnvironmentVariable('Path','User')
$entries = @($oldPath -split ';' | Where-Object { $_ })
foreach ($entry in $newPaths) {
  if ($entries -notcontains $entry) { $entries = @($entry) + $entries }
}
[Environment]::SetEnvironmentVariable('Path',($entries -join ';'),'User')
Write-Output '已添加当前用户 PATH；新终端生效。系统级 PATH 未更改。'
`

const elevateScript = `param([Parameter(Mandatory=$true)][string]$Program,[Parameter(Mandatory=$true)][string]$ArgumentsJSON)
$ErrorActionPreference = 'Stop'
$arguments = @(ConvertFrom-Json -InputObject $ArgumentsJSON)
# Start-Process uses one Windows command line; quote each argument, including spaces.
function Quote-Arg([string]$value) {
  if ($value -notmatch '[\s"]' -and $value.Length -gt 0) { return $value }
  return '"' + [regex]::Replace([regex]::Replace($value,'(\\*)"','$1$1\"'),'(\\+)$','$1$1') + '"'
}
$line = ($arguments | ForEach-Object { Quote-Arg ([string]$_) }) -join ' '
$process = Start-Process -FilePath $Program -ArgumentList $line -Verb RunAs -WindowStyle Hidden -Wait -PassThru
if ($process.ExitCode -ne 0 -and $process.ExitCode -ne 3010) { throw ('安装器退出码: ' + $process.ExitCode) }
if ($process.ExitCode -eq 3010) { Write-Output '安装器提示需要重启；请自行安排重启。' }
`

func (a *App) powershell(ctx context.Context, name, script string, args []string, out io.Writer) error {
	dir := filepath.Join(a.Home, "scripts")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file := filepath.Join(dir, name+".ps1")
	if err := os.WriteFile(file, append([]byte{0xef, 0xbb, 0xbf}, []byte(script)...), 0600); err != nil {
		return err
	}
	ps, err := a.Runner.LookPath("powershell.exe")
	if err != nil {
		return fmt.Errorf("找不到 Windows PowerShell: %w", err)
	}
	return a.Runner.Run(ctx, Command{Name: ps, Args: append([]string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", file}, args...)}, out)
}

func (a *App) verifySignature(ctx context.Context, file, publisher string, out io.Writer) error {
	return a.powershell(ctx, "verify-signature", signatureScript, []string{"-File", file, "-Publisher", publisher}, out)
}

func (a *App) rootRun(ctx context.Context, c Command, out io.Writer) error {
	if os.Geteuid() != 0 {
		sudo, err := a.Runner.LookPath("sudo")
		if err != nil {
			return fmt.Errorf("此步骤需要管理员权限，但没有 sudo")
		}
		c.Args = append([]string{"--", c.Name}, c.Args...)
		c.Name = sudo
	}
	return a.Runner.Run(ctx, c, out)
}
