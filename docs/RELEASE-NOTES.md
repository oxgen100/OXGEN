OXGEN · AI 工具箱 v0.4.2 改进安装恢复和一条龙流程。

- 使用版本目录的组件在同版本修复时保留原程序，验证或保存安装记录失败会恢复原目录。
- Ubuntu／Debian 本地 APT 索引为空时，仍可一条龙安装缺失的 Git 和 Python。
- 一条龙可给已有程序补命令入口，遇到 Windows 系统路径优先级冲突会显示实际入口。
- WorkBuddy 缓存验签失败会重新下载一次，验签通过后才使用新包。

OXGEN 提供中文终端菜单，可检测、安装和更新基础环境及 Agent。

- 基础环境：Git、Node.js/npm、完整 Python/pip/venv、VS Code；Windows 可选 WSL＋Ubuntu。
- Agent：Codex CLI、Claude Code、WorkBuddy、DeepSeek Harness、pi、OpenCode。
- 支持直接粘贴安装目录，下载位置与程序位置分别显示；输入 `0` 返回。
- Windows、macOS、Ubuntu/Debian 各提供 x64、ARM64 程序；具体软件须满足自身官方系统要求。WorkBuddy 没有 Ubuntu/Debian 官方包。
- GitHub 一行安装入口自动识别平台程序，验证 SHA256/大小后安装并打开菜单；使用者无需预装 Go 或 Python。

安装入口与使用方法见仓库 README。软件仍从各自官方来源下载；模型账号登录和 API 配置在安装后自行完成。

发布前执行三平台自动测试及六平台构建。pi、DeepSeek Harness 的 Windows 隔离实装已通过；其他完整实装、系统安装器升级、macOS/ARM64 实机、登录和模型对话的验证范围见 `docs/VALIDATION.md`。构建通过不代表全部安装场景已完成验收。
