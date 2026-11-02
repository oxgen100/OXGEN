[CmdletBinding()]
param(
    [string]$InstallDir,
    [string]$DownloadDir,
    [switch]$NoPath,
    [switch]$NoLaunch
)
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
$response = Invoke-WebRequest -UseBasicParsing -Uri 'https://github.com/oxgen100/OXGEN/releases/latest/download/install.ps1' -MaximumRedirection 5 -TimeoutSec 120
$content = if ($response.Content -is [byte[]]) { [Text.Encoding]::UTF8.GetString($response.Content) } else { [string]$response.Content }
& ([ScriptBlock]::Create($content.TrimStart([char]0xFEFF))) @PSBoundParameters