# MyGo Agent

一个 Codex 风格的桌面 AI 编程智能体，基于 [MyGo](https://github.com/egoist/mygo)
构建 —— GPU 原生渲染，无 WebView、无 HTML、无 JavaScript。

![截图](screenshot.png)

## 特性

- **项目（Projects）** —— 侧栏顶部切换工作目录；任务、文件树、git 面板、
  终端均按项目隔离。
- **四种 agent 后端**，左下角菜单切换：
  - *内置 agent* —— 进程内循环（参考 pi）：流式 OpenAI 兼容调用（chat
    completions 或 Responses API）+ 本地工具（`bash`、`read_file`、
    `edit_file`、`list_files`、`grep`、`read_skill`），回复支持表格、
    链接与 diff 卡片的 Markdown 渲染，长任务自动压缩上下文，ChatLog
    随任务持久化、重启后可带完整上下文继续。
  - *Codex CLI* —— 通过 `codex app-server`（JSON-RPC）驱动，自动续接
    会话；Agent 模式下命令/文件变更的原生审批请求直达本应用的审批卡
    （协议契约见 spec/cli-backends.md）。
  - *Claude Code* —— 通过 `claude -p` 双向 stream-json 控制协议驱动，
    自动续接会话；`can_use_tool` 审批请求直达审批卡（只读模式自动拒绝），
    Edit/Write 调用渲染为 diff。
  - *Demo agent* —— 无需任何账号。
- **厂商级模型配置（ZCode 风格）** —— 每个厂商独立的 base URL、API key、
  模型列表与 wire API（chat completions 或 codex/OpenAI 模型所用的
  Responses API）。内置 OpenAI、DeepSeek、OpenRouter、Ollama 预设。
- **技能（Skills）** —— 遵循 Agent Skills 规范：`.agents/skills/`
  （项目或用户目录）下的 `SKILL.md` 目录；系统提示只放名称与描述，
  任务匹配时模型通过 `read_skill` 按需加载。
- **MCP 服务器** —— 设置弹窗中配置 stdio MCP 客户端，自动合并项目里的
  `.mcp.json`（Claude Code / pi 约定）；工具以 `mcp_<server>_<tool>`
  的名字并入 agent 工具集。
- **界面** —— 任务侧栏（日期分组 + 搜索）、文件树 + git 变更面板、
  文件查看器（文本 / 图片 / Markdown / diff）、消息锚点轨（悬停预览、
  点击跳转）、消息操作（复制 / 重发 / 重新生成）、全宽输入框（审批模式
  + 模型选择）、内嵌 Ghostty 终端。

## 安全模型

规范先行：[spec/](spec/) 目录先写不变量再写代码 ——
[permissions.md](spec/permissions.md)（权限门）、[sandbox.md](spec/sandbox.md)
（执行边界）、[approvals.md](spec/approvals.md)（审批流）。

- **三级审批模式**（输入框左下角）对每个后端都真实生效：
  - *Read Only* —— 只读工具（`read_file` / `list_files` / `grep` /
    `read_skill`）；shell、写文件、MCP 一律拒绝。
  - *Agent* —— 可写文件（仅限项目目录）并可跑 shell；shell 在本地
    沙箱中执行：全域可读（凭证目录 `~/.ssh`、`~/.aws`、`~/.gnupg`
    等除外）、只有项目目录与临时目录可写、**网络一律拒绝**
    （macOS 用 Seatbelt，Linux 用 bubblewrap）。
  - *Full Access* —— 不套沙箱，完全访问。
- **逐工具规则**（config.json `permissions.rules`）覆盖模式默认值，
  选择器支持精确名、`前缀*` 与 `*`，值为 `allow` / `deny` / `ask`：

  ```json
  "permissions": { "rules": { "bash": "ask", "mcp_github_*": "allow" } }
  ```

- **审批卡** —— 规则为 `ask`（或 Agent 模式下的 MCP 工具）时，运行
  挂起并在消息流中弹出审批卡：*Allow once* / *Deny*。批准只对这一次
  调用生效（没有 "always allow"——持久授权只来自配置文件）；10 分钟
  无响应自动拒绝；模型输出永远无法创造授权。
- 平台没有沙箱时（如 Windows），Agent 模式会**明确报错**而不是静默
  降级为无沙箱执行。

## 架构

规范先行:[spec/](spec/) 目录持有全部协议契约；`scripts/check-deps.sh`
在 CI 中机器强制分层依赖。四个层次，每条边界一个接口：

```
┌──────────────────────────────────────────────────────────┐
│ UI（internal/ui）   ViewModel + Actions 渲染共享视图：      │
│                     Home · Composer · Sidebar · Workspace │
│                     Markdown · Diff 行 · 主题/图标          │
│   ▲ 状态快照                │ Actions 回调                 │
│ HOST（internal/app）状态、线程、dispatch、审批、持久化：    │
│                     threads.json（版本化+隔离）·config.json │
│   │ harness.Turn / Event（归一化事件流）                    │
│ HARNESS（internal/harness）一个协议，四个可替换后端：       │
│      builtin 循环 · codex app-server · claude 控制协议 · demo│
│   │ Sandbox / Memory 协议                                  │
│ PROVIDERS（internal/providers/sandbox）                     │
│      macOS Seatbelt · Linux bubblewrap · 平台缺失时诚实报错 │
└──────────────────────────────────────────────────────────┘
```

## 目录结构

```
main.go               仅入口：参数、版本号、启动应用
internal/app          应用本体：状态、四种后端、全部界面
  model.go threads.go projects.go      状态、持久化、项目
  view.go sidebar.go thread.go         外壳、任务列表、消息
  composer.go viewer.go workspace.go   输入框、文件查看器、面板
  builtin.go claude.go                 内置 agent 与 Claude Code 运行器
  settings.go rail.go theme.go         设置、锚点轨、调色板
internal/agent        agent 循环：流式客户端（chat + responses）、
                      工具、技能、MCP、diff
internal/config       持久化配置
internal/components   可复用 UI 组件（锚点轨、格式化）
packaging/            macOS .app 的 Info.plist
```

## 构建

需要 Go 1.27+（工具链会自动下载）。无 cgo、无 npm。

```bash
make build     # 编译当前平台的 ./mygo-agent
make run       # 源码运行
make test      # 全部测试
make release   # dist/ 下产出 darwin/linux/windows（amd64+arm64）、
               # macOS .app 包与校验和
```

`--version` / `-v` 打印构建版本。

## 打包

`make release` 将所有目标交叉编译到 `dist/`：

```
mygo-agent-darwin-amd64         mygo-agent-windows-amd64.exe
mygo-agent-darwin-arm64         mygo-agent-windows-arm64.exe
mygo-agent-linux-amd64          mygo-agent-linux-arm64
MyGoAgent-darwin-*.app.zip      checksums.txt
```

macOS 包对外分发需要签名与公证（本机自用可
`codesign --deep --force --sign -` 临时签名）。推送 `v*` 标签会触发
release 工作流，自动把 `dist/*` 附到 GitHub Release。

## 文档

- [README.md](README.md) — English
