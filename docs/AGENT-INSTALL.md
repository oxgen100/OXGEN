# Agent 安装（v0.3.0）

主菜单输入 `4`，再选 Agent。已有版本可选 `1. 保留 / 2. 更新 / 0. 返回`；新安装时粘贴绝对目录，核对下载位置、程序位置和额外写入位置后输入 `1` 开始。所有 OXGEN 输入页支持 `0` 返回；执行期输入 `0` 并回车取消后续步骤。原生系统安装器已经开始的操作可能仍由安装器完成，完成后可重新检测；不会把取消记为成功。

| 编号 | 工具 | 官方来源与安装方式 | 首次使用 |
|---|---|---|---|
| 1 | Codex CLI | [OpenAI 官方文档](https://developers.openai.com/cookbook/examples/codex/using_goals_in_codex)、OpenAI 官方独立发行渠道的完整原生包，SHA256；官方 npm 原生包作为后备渠道，SHA512，无需 Node | 在项目目录运行 `codex`，按提示登录 |
| 2 | Claude Code | [Anthropic 官方文档](https://code.claude.com/docs/en/setup)、`downloads.claude.ai` 的版本清单和原生程序，SHA256；Windows 另验签名 | 运行 `claude`，按提示登录 |
| 3 | WorkBuddy | [腾讯官方版本页](https://www.codebuddy.cn/docs/workbuddy/Download-History)；Windows 官方签名安装器，macOS 官方 DMG 和应用签名/系统检查 | 从所选目录的应用或开始菜单启动 |
| 4 | DeepSeek Harness | [DeepSeek 官方仓库](https://github.com/deepseek-ai/deepseek-harness) 的 `@deepseek-ai/dsh` npm 包；当前是官方开发预览，版本可能含 rc/alpha | `dsh web` 打开本机网页后配置模型；`dsh --help` 查看命令 |
| 5 | pi | [pi 官方文档](https://pi.dev/docs/latest/quickstart) 的 `@earendil-works/pi-coding-agent`，不使用同名 npm 包 `pi` | 项目目录运行 `pi`，通过 `/login` 连接模型 |
| 6 | OpenCode | [当前 OpenCode V2](https://opencode.ai/v2/docs/) 的 `@opencode/cli` 对应架构原生包，SHA512；x64 使用兼容旧 CPU 的 baseline 版，无需 Node | `opencode`，通过 `/connect` 连接模型 |

每次选择安装/更新都会查询官方版本，不把版本写死在 OXGEN 中。查询失败显示“暂时无法查询”，可重试或 `0` 返回；选择保留不联网。版本比较包含预发布编号，避免把 `rc.5` 当成与 `rc.4` 相同，也不降级已有更高版本。安装不代表已经登录或获得模型使用额度。

## 目录和依赖

例如为 Codex 粘贴 `D:\AI工具\Codex`，下载包在 `D:\AI工具\Codex\downloads`，程序在 `D:\AI工具\Codex\versions\<版本>`。Claude Code、pi、DeepSeek Harness 和 OpenCode 也按版本并存，旧版本不自动删除。更新其他来源的安装时，原安装保留，所选目录并存安装官方版本；请在新终端使用 `oxgen doctor` 核对命令实际生效路径。Claude 的 OXGEN 启动入口关闭其自身更新器，由本菜单管理版本和目录，不运行会写入默认安装位置的 `claude install`。

pi 和 DeepSeek Harness 需要现有 Node.js 22.19+ 与 npm；缺少时提示先到基础环境补齐。它们使用所选目录中的私有项目和锁文件，先核对所有依赖来源和 SHA512，再由 npm 安装；不改全局 npm prefix，缓存放在该目录的 `downloads/npm-cache`，不读取用户 npm 源配置。依赖安装脚本关闭，实际 CLI 版本验证成功后才记录成功。Windows pi 的启动入口加入检测到的 Git Bash 路径，保留用户既有 pi 设置。入口兼容 CMD 中文代码页中的中文 Node 路径，退出时恢复原代码页；依赖安装期间每 15 秒提示进度，重新运行可复用经过校验的锁文件和缓存。

WorkBuddy 新安装的程序在 `所选目录/WorkBuddy`，下载包在 `所选目录/downloads`。Windows 官方安装器还会管理快捷方式、开始菜单和卸载记录；已有应用更新沿用实际原目录，只允许另选下载目录，不自动迁移。运行中的 WorkBuddy 须先自行关闭，OXGEN 不强制结束应用。macOS 复制经系统校验的完整应用。腾讯尚未提供 Ubuntu/Debian 安装包，此平台显示“待用户操作”，不使用非官方移植版。

登录、会话和模型配置仍由各 Agent 管理，通常位于用户资料目录（例如 `.codex`、`.claude`、`.pi/agent`）；不承诺所有数据都搬到所选磁盘。最终确认前列出额外写入位置，不自动登录、写 API Key、启动 Web 服务、打开应用或迁移旧配置。

## 检测与验证边界

`oxgen doctor` 和 Agent 子菜单 `7` 检测六个 Agent，报告版本及实际命令/应用路径，不写 OXGEN 状态。选择保留时记录已发现的路径，便于下次找到自定义目录。

本机验证及各平台尚未验收的部分见 [VALIDATION.md](VALIDATION.md)。模型对话、账户登录、WorkBuddy GUI、macOS 实装和 ARM64 实机须分别验证；编译或版本检查通过不代表这些场景也通过。
