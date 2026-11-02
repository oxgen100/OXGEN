param(
    [string]$BaseUrl = '@OXGEN_BASE_URL@',
    [string]$InstallDir,
    [string]$DownloadDir,
    [switch]$NoPath,
    [switch]$NoLaunch
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
try { [Console]::InputEncoding = [Text.UTF8Encoding]::new($false); [Console]::OutputEncoding = [Text.UTF8Encoding]::new($false) } catch { }

if (-not $BaseUrl -or $BaseUrl.StartsWith('@')) { throw '域名尚未配置，请传入 -BaseUrl https://你的域名/oxgen/' }
$base = [Uri]($BaseUrl.TrimEnd('/') + '/')
if ($base.Scheme -ne 'https' -or $base.UserInfo -or $base.Query -or $base.Fragment) { throw '下载入口必须是无账号和查询参数的 HTTPS 地址。' }
$releaseState = @{ Base = $base }

function Resolve-OwnUrl([string]$relative) {
    $downloadBase = $releaseState.Base
    $resolved = [Uri]::new($downloadBase, $relative)
    if ($resolved.Scheme -ne 'https' -or $resolved.Authority -ne $downloadBase.Authority -or -not $resolved.AbsolutePath.StartsWith($downloadBase.AbsolutePath, [StringComparison]::Ordinal) -or $resolved.UserInfo) { throw '发布清单地址越出了指定服务器目录。' }
    return $resolved.AbsoluteUri
}
function Get-OwnFile([string]$url,[string]$destination) {
    if ($githubRelease) { Get-GitHubFile $url $destination; return }
    Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $destination -MaximumRedirection 0 -TimeoutSec 300
}
$githubRelease = $base.Host -eq 'github.com'
$githubPrefix = ''
if ($githubRelease) {
    if ($base.AbsolutePath -notmatch '^/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/releases/(latest/download|download/v[0-9A-Za-z.-]+)/$') { throw 'GitHub 下载入口格式无效。' }
    $githubPrefix = '/' + $Matches[1] + '/releases/'
}
function Get-GitHubFile([string]$url,[string]$destination) {
    $current = [Uri]$url
    for ($hop=0; $hop -le 5; $hop++) {
        if ($current.Scheme -ne 'https' -or $current.UserInfo -or $current.Port -ne 443 -or $current.Fragment) { throw 'GitHub 下载重定向无效。' }
        if ($current.Host -eq 'github.com') {
            if (-not $current.AbsolutePath.StartsWith($githubPrefix,[StringComparison]::Ordinal) -or $current.Query) { throw '下载地址越出当前 GitHub 仓库。' }
            if ($current.AbsolutePath -match '/releases/download/(v[0-9A-Za-z.-]+)/manifest.json$') {
                $releaseState.Base = [Uri]('https://github.com' + $githubPrefix + 'download/' + $Matches[1] + '/')
            }
        } elseif ($current.Host -ne 'release-assets.githubusercontent.com') { throw '下载未指向 GitHub 官方发行 CDN。' }
        $request = [Net.HttpWebRequest]::Create($current)
        $request.AllowAutoRedirect = $false
        $request.UserAgent = 'OXGEN-Installer'
        $request.Timeout = 300000
        $request.ReadWriteTimeout = 300000
        $response = $request.GetResponse()
        try {
            $status = [int]$response.StatusCode
            if ($status -in @(301,302,303,307,308)) {
                if (-not $response.Headers['Location']) { throw 'GitHub 重定向缺少目标地址。' }
                $current = [Uri]::new($current,$response.Headers['Location'])
                continue
            }
            if ($status -ne 200) { throw ('GitHub 下载失败，HTTP ' + $status) }
            $inputStream = $response.GetResponseStream()
            $outputStream = [IO.File]::Create($destination)
            try { $inputStream.CopyTo($outputStream) } finally { $outputStream.Dispose(); $inputStream.Dispose() }
            return
        } finally { $response.Dispose() }
    }
    throw 'GitHub 下载重定向次数过多。'
}
function Resolve-Directory([string]$value) {
    if (-not [IO.Path]::IsPathRooted($value)) { throw '目录必须是完整绝对路径。' }
    $full = [IO.Path]::GetFullPath($value)
    if ($full.TrimEnd('\') -eq [IO.Path]::GetPathRoot($full).TrimEnd('\')) { throw '请选择磁盘内的具体文件夹。' }
    return $full
}

Write-Host 'OXGEN 氧气邮政员 · 安装工具箱' -ForegroundColor Cyan
if (-not $InstallDir) {
    $defaultDir = Join-Path ([Environment]::GetFolderPath('UserProfile')) '.oxgen'
    $answer = Read-Host ('粘贴 OXGEN 安装目录，输入 0 返回；回车使用 ' + $defaultDir)
    if ($answer -eq '0') { return }
    $InstallDir = if ($answer) { $answer.Trim('"') } else { $defaultDir }
}
$InstallDir = Resolve-Directory $InstallDir
if (-not $DownloadDir) {
    $DownloadDir = Join-Path $InstallDir 'downloads'
}
$DownloadDir = Resolve-Directory $DownloadDir
Write-Host ('下载目录：' + $DownloadDir)
Write-Host ('最终程序：' + (Join-Path $InstallDir 'bin\oxgen.exe'))
Write-Host '如选择添加命令入口，将写入当前用户 PATH；启动脚本本身所在目录不代表最终安装目录。'
if ((Read-Host '输入 1 开始安装，输入 0 返回') -ne '1') { return }

New-Item -ItemType Directory -Path $InstallDir,$DownloadDir -Force | Out-Null
$temporary = Join-Path $DownloadDir ('.oxgen-bootstrap-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $temporary | Out-Null
try {
    $manifestFile = Join-Path $temporary 'manifest.json'
    Get-OwnFile (Resolve-OwnUrl 'manifest.json') $manifestFile
    $manifest = Get-Content -LiteralPath $manifestFile -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($manifest.schema -ne 1) { throw '不支持的发布清单版本。' }
    if ($githubRelease -and ($manifest.version -notmatch '^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$' -or -not $releaseState.Base.AbsolutePath.EndsWith('/v' + $manifest.version + '/'))) { throw 'GitHub 版本与下载清单不一致。' }
    $architecture = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
    $arch = switch ($architecture) { 'x64' { 'amd64' } 'arm64' { 'arm64' } default { throw ('暂不支持架构 ' + $architecture) } }
    $property = $manifest.artifacts.PSObject.Properties['windows-' + $arch]
    if (-not $property) { throw '清单缺少本机对应程序。' }
    $asset = $property.Value
    if ($asset.sha256 -notmatch '^[0-9a-fA-F]{64}$') { throw '发布清单缺少有效 SHA256。' }
    $zipFile = Join-Path $temporary 'release.zip'
    Get-OwnFile (Resolve-OwnUrl $asset.url) $zipFile
    $stream = [IO.File]::OpenRead($zipFile)
    $hasher = [Security.Cryptography.SHA256]::Create()
    try { $actualHash = [BitConverter]::ToString($hasher.ComputeHash($stream)).Replace('-','') }
    finally { $stream.Dispose(); $hasher.Dispose() }
    if ($actualHash -ne $asset.sha256) { throw 'SHA256 校验失败，未安装。' }
    if ((Get-Item -LiteralPath $zipFile).Length -ne $asset.size) { throw '安装包大小校验失败。' }
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [IO.Compression.ZipFile]::OpenRead($zipFile)
    try {
        foreach ($entry in $zip.Entries) {
            $name = $entry.FullName.Replace('\','/')
            if ($name.StartsWith('/') -or $name.Contains(':') -or ($name -split '/') -contains '..' -or (($entry.ExternalAttributes -shr 16) -band 0xF000) -eq 0xA000) { throw '安装包包含不安全路径或符号链接。' }
        }
    } finally { $zip.Dispose() }
    $unpacked = Join-Path $temporary 'unpacked'
    [IO.Compression.ZipFile]::ExtractToDirectory($zipFile,$unpacked)
    $source = Join-Path $unpacked 'bin\oxgen.exe'
    if (-not (Test-Path -LiteralPath $source -PathType Leaf)) { throw '安装包缺少 oxgen.exe。' }
    $bin = Join-Path $InstallDir 'bin'
    New-Item -ItemType Directory -Path $bin -Force | Out-Null
    $destination = Join-Path $bin 'oxgen.exe'
    $candidate = Join-Path $bin 'oxgen.new.exe'
    Copy-Item -LiteralPath $source -Destination $candidate -Force
    & $candidate --version
    if ($LASTEXITCODE -ne 0) { throw '程序不能在本机启动，旧程序未替换。' }
    Move-Item -LiteralPath $candidate -Destination $destination -Force
    [IO.File]::WriteAllText((Join-Path $InstallDir 'server-url.txt'), $releaseState.Base.AbsoluteUri, [Text.UTF8Encoding]::new($false))
    if (-not $NoPath) {
        if ((Read-Host '输入 1 将 oxgen 加入当前用户 PATH，其他输入跳过') -eq '1') {
            $existing = [Environment]::GetEnvironmentVariable('Path','User')
            if (@($existing -split ';') -notcontains $bin) { [Environment]::SetEnvironmentVariable('Path',($bin + ';' + $existing),'User') }
            $env:Path = $bin + ';' + $env:Path
        }
    }
    Write-Host ('安装完成：' + $destination) -ForegroundColor Green
    if (-not $NoLaunch) { & $destination --home $InstallDir }
} finally {
    # Verify the resolved deletion target is an immediate owned child of DownloadDir.
    $cacheRoot = [IO.Path]::GetFullPath($DownloadDir).TrimEnd('\')
    $cleanup = [IO.Path]::GetFullPath($temporary)
    if ([IO.Path]::GetDirectoryName($cleanup) -eq $cacheRoot -and [IO.Path]::GetFileName($cleanup).StartsWith('.oxgen-bootstrap-')) {
        if (Test-Path -LiteralPath $cleanup) { Remove-Item -LiteralPath $cleanup -Recurse -Force }
    }
}
