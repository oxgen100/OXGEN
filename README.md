# OXGEN · 氧气邮政员

跨平台的中文终端基础环境与 Agent 安装工具箱。使用工具箱无需预装 Python、Node.js 或 Go；首次下载使用 Windows PowerShell，或 macOS/Linux 自带的 curl/wget、tar 和 SHA256 工具。具体 Agent 的运行依赖在安装前单独检测。

终端菜单排版参考 [kejilion/sh](https://github.com/kejilion/sh)：亮青色编号、24 字符短分隔线。v0.3.2 按用户选择改成单行亮青色极简字标 `O X G E N`，下方显示中文名和版本。输入步骤统一使用 `0` 返回；正文跟随终端主题，背景、字体和窗口大小由终端设置决定。

v0.3.0 新增主菜单 `4. Agent 安装`：Codex、Claude Code、WorkBuddy、DeepSeek Harness、pi、OpenCode，单独检测／安装／更新，继续使用粘贴目录和 `0` 返回。详见 [Agent 安装说明](docs/AGENT-INSTALL.md)。

## 当前功能

- Git、Node.js LTS/npm、Python/pip/venv、VS Code 的检测、安装及可选更新。
- Windows 10/11、macOS、Ubuntu/Debian；分别提供 x64 与 ARM64 程序。所选软件仍须满足自身官方系统版本要求，安装失败不会标记成功。
- Windows 可选 WSL＋Ubuntu，选择发行版位置、记录待重启/待初始化步骤；Windows 与 WSL 内的环境分别管理。
- 直接粘贴安装目录，无选盘子菜单；下载位置与实际安装位置在同一清单分别显示。
- 官方软件发布渠道和发行版官方签名仓库，不使用第三方镜像、PPA，不修改系统既有 APT 软件源。
- 六款 Agent 按需安装，官方版本实时查询；不自动登录或配置模型 API，不改动已有账户。
- pi / DeepSeek Harness 需要 Node.js 22.19+ 与 npm，Windows pi 需要 Git Bash；Codex、Claude Code 和 OpenCode 用原生程序。
- WorkBuddy 是桌面应用，支持 Windows/macOS；Ubuntu/Debian 显示官方包暂不可用。已有 Windows WorkBuddy 更新沿用原目录，不自动搬迁。

## 一行安装（Windows CMD）

在 CMD 粘贴下面整行，按提示粘贴安装目录、确认安装，完成后自动打开 OXGEN：

```cmd
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/oxgen100/OXGEN/main/install.ps1 | iex"
```

不需要预装 Git、Node.js、Python 或 Go。下载安装器需要能访问 GitHub、raw.githubusercontent.com 和 GitHub 发行 CDN。安装包按架构选择，校验 SHA256 和大小后才替换程序；已有安装记录保留。输入 `0` 可取消。

也可以从 [Releases](https://github.com/oxgen100/OXGEN/releases/latest) 下载对应 ZIP/tar.gz 后运行 `bin/oxgen`。安装后若选择添加用户 PATH，新开终端即可输入 `oxgen`。

## 本地使用

从发布包解压后运行 `bin/oxgen.exe`（Windows）或 `bin/oxgen`（macOS/Linux）。Windows 可双击 exe；其他系统在终端运行。macOS 对未签名程序可能要求你在系统设置中允许打开；本项目不自动修改 Gatekeeper 设置。首版发布包尚未签署 Apple 开发者证书。

```text
oxgen
oxgen doctor
oxgen doctor --json
oxgen doctor --online
oxgen --version
oxgen --home D:\OXGEN
oxgen --no-color
```

`doctor` 仅进行只读检测；不创建状态、日志或虚拟环境。`--online` 另查询官方推荐版本，查询失败单独显示。实际安装后才创建临时虚拟环境验证 pip，并在限定临时目录内清理。

推荐使用主菜单 `3. 一条龙安装`，或 `1. Agent 基础环境` → `1. 一条龙安装`：先检测 Git、Node/npm、Python/pip/venv、VS Code；已有可用环境直接保留，只补缺失或不完整的项目。然后粘贴一次总目录，核对一次安装清单，输入 `1` 连续安装并为新组件配置当前用户 PATH。没有缺失时直接给出检测汇总，不询问目录、不联网查新版本。WSL 仍为独立可选项。

例如粘贴 `D:\AI环境`，安装包在 `D:\AI环境\downloads\<组件>`，程序分别放入 `git`、`node`、`python`、`vscode` 子目录；使用版本目录的组件继续按 `versions/<版本>` 存放。系统组件固定位置、Python 安装管理器、用户配置和 PATH 等额外写入位置在清单中明示。APT 使用刷新后的官方仓库实际候选版本；Apple 首次开发工具安装需在系统窗口完成，工具箱记为待操作，其余项目继续。

单项安装也只粘贴一次目录：下载包默认放在该目录的 `downloads` 中；固定系统位置的项目，粘贴目录只用于下载，实际安装位置会明确显示。更新可从单项菜单选择，已有可用版本在一条龙中不自动更新。

所有输入步骤均可输入 `0` 返回；确认页是 `1. 继续 / 0. 返回`，回车默认不执行安装。Windows 的检测、查询、下载及安装调度中也可输入 `0` 并回车取消后续步骤；已完成的安装保留。macOS/Linux 的系统权限或 Apple 安装窗口由系统处理，密码/授权期间使用系统自身的取消操作；工具箱中的输入仍统一使用 `0`。重定向/管道运行不会额外暂停。

VS Code 可从子菜单 `6. VS Code 检测／安装／更新` 单独管理；Windows 的 `5. WSL＋Ubuntu` 编号保持不变。

程序位于 `bin/` 时，默认在其上级目录保存状态。开发模式默认 `~/.oxgen`；可用 `--home` 或 `OXGEN_HOME` 覆盖。

```text
OXGEN/
  bin/oxgen[.exe]
  state.json
  server-url.txt             发布后记录你的 HTTPS 下载根地址
  logs/                     单次安装日志
  scripts/                  需要执行的固定 PowerShell 适配脚本
  tools/                    可自定义位置的软件（默认建议目录）
  downloads/                默认建议下载目录
```

每个组件都可单独改目录。系统安装管理器、用户 PATH、Apple 安装收据、APT 数据库及 WSL 内核等可能仍需系统/用户资料目录；安装前会说明。Windows 系统级 PATH 可能优先于用户 PATH，完成后务必在新终端用 `oxgen doctor` 检查实际生效位置。

## 各系统的安装方式

| 组件 | Windows | macOS | Ubuntu/Debian |
|---|---|---|---|
| Git | 官方 Git for Windows 安装器，校验发布者，传入安装目录 | Apple 命令行开发工具及其官方更新，系统位置固定 | 独立官方签名 APT 索引，系统位置固定 |
| Node/npm | nodejs.org 的 LTS 完整 ZIP | nodejs.org 的 LTS 完整 tar.gz | nodejs.org 的 LTS 完整 tar.gz |
| Python | 官方 MSIX 安装管理器，下载完整运行时，通过离线索引提取至所选位置 | python.org 签名 pkg，安装位置由官方包决定 | 官方 python3、python3-pip、python3-venv 软件包 |
| VS Code | 微软官方完整 ZIP，目录可选 | 微软官方应用 ZIP，保留 .app 内部链接和签名并进行系统检查，目录可选 | 微软官方完整 tar.gz，目录可选；桌面会话和系统库须满足官方要求 |

Node、Python 自定义运行时采用版本目录并存，旧目录不自动删除。已有非 OXGEN Node/Python 安装无法可靠接管时，明确提供并存安装，不伪装为原位更新。Git for Windows 可在识别出的原目录更新，受保护的系统目录会请求管理员授权；不自动搬迁软件。macOS Apple 工具包更新由 `softwareupdate` 的具体 Command Line Tools 标签触发，不执行全系统升级。

VS Code 通过微软更新接口查询对应系统/架构的稳定版和 SHA256，安装完整桌面编辑器，校验版本、提交编号及架构；不会用独立 CLI/tunnel 包代替编辑器。程序放入所选目录的 `versions/<版本>/`，Windows ZIP 版可由 OXGEN 按需安装新版本。已有用户/系统安装器、系统包或便携安装保留原样，选择更新时采用并存官方发行包，不搬迁原程序。设置及扩展继续由 VS Code 自身管理，安装前显示通常使用的位置；不会自动安装扩展、修改账户或打开编辑器窗口。安装完成后可选择把发行包的 `bin` 目录加入用户 PATH，以使用 `code` 命令。图形界面须在桌面环境中另行验证。

WSL 中的 VS Code 操作会引导在 Windows 主机安装编辑器，再使用微软 WSL 扩展连接 Ubuntu；内部基础环境流程不会重复安装 Linux 桌面编辑器。

Python 使用完整 CPython 运行时，**不是**缺少 pip 的 embeddable 包。Windows 官方安装管理器及应用别名若尚未可用，会显示“待用户操作”。APT 只刷新所选下载目录中的临时官方索引；系统的配置文件不被替换。APT 的系统数据库仍正常更新。

WSL 不自动安装、不自动重启、不搬迁已有发行版。首次启用后如需重启，完成后重新进入 `WSL＋Ubuntu`；用户名/密码初始化在系统 Ubuntu 终端完成。已有 Ubuntu 可直接复用，并只读查询及显示其实际磁盘位置。完成后可选在 Ubuntu 内启动 Linux 版 OXGEN，这需要先配置你的发布地址（`--base-url https://你的域名/oxgen/`）。

## 构建与发布

开发机需要 Go 1.23+，使用者不需要 Go。只用标准库，无第三方 Go 模块。

```sh
go test ./...
go vet ./...
go run ./cmd/pack -version 0.4.0 -github-repository oxgen100/OXGEN
```

发布包支持 Windows、macOS、Linux 的 x64/ARM64。GitHub 模式生成平铺的 Release 安装包清单，安装器锁定同一版本后下载，支持 GitHub 官方发行 CDN 的 HTTPS 重定向。

推送 `v*` 标签后，GitHub Actions 在三种系统执行自动测试和 go vet，生成六个安装包与校验清单，先上传 Draft Release 的全部附件再发布，避免用户下载到未完成的版本。已发布版本不覆盖，改动后使用新的版本标签。

```sh
git tag v0.4.0
git push origin v0.4.0
```

自有 HTTPS 服务器模式仍可使用 `go run ./cmd/pack -version 0.4.0 -base-url https://你的域名/oxgen/`；上传 `dist/server` 中的安装脚本、manifest 和 releases 目录，不上传 build。发布包仅托管 OXGEN，Git、Node、Python 和 Agent 仍从软件官方来源下载。

## 验证边界

单元测试、六平台编译与实际安装测试分别记录。查看 [验证记录](docs/VALIDATION.md)；没有对应真实系统运行证据的功能，不标记为已完成平台安装验收。安装测试保留了本机已有 Git/Node/Python 和 WSL，使用已有 Ubuntu 进行隔离验证。

可选的实际运行测试：设置 `OXGEN_LIVE_DIR` 后运行 `go test ./internal/oxgen -run TestLiveExistingPythonVenv -v`，只创建隔离 venv。`TestLiveOfficialRuntimes` 会从官方来源下载隔离运行时，不运行系统安装器；额外设置 `OXGEN_VERIFY_MANAGER=1` 才下载并检查 Python MSIX 签名。它们不等于系统安装器实装测试。

## 官方参考

- VS Code 下载及安装包：https://code.visualstudio.com/download
- VS Code Windows 安装及 ZIP：https://code.visualstudio.com/docs/setup/windows
- VS Code 命令行：https://code.visualstudio.com/docs/configure/command-line
- VS Code 官方下载域名：https://code.visualstudio.com/docs/setup/network
- VS Code 与 WSL：https://code.visualstudio.com/docs/remote/wsl

- 原教程：https://www.bilibili.com/opus/1194096510018519076
- Node.js 官方发行：https://nodejs.org/dist/index.json
- Git for Windows：https://gitforwindows.org/silent-or-unattended-installation.html
- Python 安装管理器：https://docs.python.org/3/using/windows.html
- macOS Python：https://docs.python.org/3/using/mac.html
- WSL：https://learn.microsoft.com/windows/wsl/basic-commands
- WSL 磁盘位置：https://learn.microsoft.com/windows/wsl/disk-space
