# 方案：Agent 化与多端管理（配置化工具 / MCP / Skills / 模型，单与多 Agent，Web 管理，会话追踪）

状态：提案（待评审拍板后按阶段实施）。本文给出概念模型、与现有代码的衔接点、数据与协议演进、分阶段计划。

## 1. 目标与产品分工

1. **Agent 成为可配置的一等实体**：每个 Agent 绑定一套 harness 后端 + 模型 + 工具集 + MCP 服务器 + Skills + 权限规则 + 系统提示词。
2. **两端分工（已定）**：
   - **Web 端 = 治理面**：创建和管理 Agent 及其资源——工具 / MCP / Skills / 模型 / 权限，外加用量与追踪观测。
   - **桌面客户端 = 使用面（personal agent）**：使用者直接挑一个"通用功能 Agent"（写作、评审、整理…）开始干活——聊天、审批、本机工作区；桌面不做复杂编排表单。
3. **单 Agent 与多 Agent**：会话绑定某个 Agent；多个 Agent 可管理、可并发运行；进一步支持会话内协作（委托）。
4. **聊天记录可追踪**：事件级落盘（工具调用、耗时、token/费用），可搜索、可回放、可汇总用量。

## 2. 现状盘点（与本方案相关的代码事实）

- **配置** `internal/config`：v1，`providers`（OpenAI 兼容端点 + codex/claude 内置）、`mcp_servers`（全局）、`permissions.rules`、`backend/model/effort/max_turns`（**全局单选**）。严格解析（未知字段报错），版本化，拒绝更新版本的文件。
- **Harness 协议** `internal/harness`：`Harness/Turn/Event` 单回合协议 + 四个适配器（builtin / codex app-server / claude stream-json / demo）。`Turn` 携带 prompt/workdir/mode/rules/model/effort/sessionID/sandbox/memory。
- **工具**：`harness.Tools(workdir, skills, ToolOptions)` 构建内置 6 件（bash / read_file / edit_file / list_files / grep / read_skill），每个 `Tool{Name, Description, Actions, Parameters, Execute}` —— 天然的注册表素材。
- **Skills**：`DiscoverSkills(projectDir)` 从项目目录发现，经 `read_skill` 工具暴露。
- **MCP**：运行时 `a.effectiveMCPServers()`（全局配置 + 项目 `.mcp.json`）spawn 后把工具并入工具集（仅 builtin）。
- **运行态**：`a.running`/`a.cancel` **全局单飞** —— 同一时刻只能有一个回合。这是多 Agent 的第一个硬阻塞点。
- **会话存储**：`threads/<projectID>/<threadID>.json`（meta + messages + chat_log），原子写，版本化（spec/data.md）。无线程 ↔ Agent 绑定，无事件级 trace，无用量统计（builtin loop 目前**不采集** usage；codex 有 `thread/tokenUsage/updated` 通知；claude 的 result 行带 `duration_ms/total_cost_usd/usage`）。
- **UI**：`internal/ui` 已全部 ViewModel+Actions 化（Transcript/Viewer/Settings/Header/Sidebar/Home/Composer）；`internal/app` 只剩状态、分发、持久化与桥接。这个形状就是为"第二个 UI 表面"准备的。
- **UI 框架**：mygo 支持**两种**窗口：native（当前，GPU 直绘）与 **web page**（系统 webview + 由 Go 服务定义生成的 TS client，typed IPC：bind 服务 / channel 流 / 类型化事件）。二者可混用 —— Web 端有现成路径，不必自起前端脚手架之外的东西。

## 3. 概念模型与数据模型（config v2）

核心新增实体 **Agent**（配置档案，不是运行实例）：

```jsonc
// config.json — version 2（新增片段）
{
  "version": 2,
  "agents": [{
    "id": "ag-01",
    "name": "Refactorer",
    "emoji": "🛠",
    "backend": "builtin",            // builtin | codex | claude | demo
    "provider": "prov-9676…",         // 模型来源
    "model": "deepseek-chat",
    "effort": 1,
    "mode": 1,                        // 默认审批模式
    "max_turns": 25,
    "system_prompt": "…",             // 追加在 harness 默认提示词之后
    "tools": {
      "disabled": ["list_files"],     // 内置工具注册表按名启停
      "rules": {"bash": "ask"}        // 工具级权限（并入 harness.Rules）
    },
    "mcp_servers": ["filesystem"],    // 引用 config.mcp_servers 的名字
    "skills": {"mode": "project", "allow": [], "deny": []}  // project|all|custom
  }],
  "default_agent": "ag-01"
}
```

迁移规则（spec/data.md 惯例）：读 v1 时把 `backend/model/effort/permissions` 折叠为一个 "Default" Agent；`Thread` 增 `meta.agent_id`，旧线程迁移后指向 Default。`permissions.rules` 全局块保留为"项目级底座"，Agent 规则叠加其上（Agent 更具体者优先）。

**"插件"的定位**（需要明确，见 §11-D）：本方案把 Agent 能力扩展收敛为三类一等公民——**内置工具**（注册表启停）、**MCP 服务器**（按 Agent 挂载）、**Skills**（按 Agent 启用）。mygo 库级的 `plugins/`（如终端）属客户端能力，不属于 Agent 配置。将来若有新的扩展形态（如 HTTP 工具源），以 MCP 优先。

## 4. 能力域设计

### 4.1 工具（内置工具注册表）

- `harness` 增加元数据：`ToolCatalog()` 返回内置工具的 `{name, description, actions, parameter schema}`（构建时不绑定 workdir，仅描述），供 UI 与配置校验使用。
- `harness.Tools(...)` 增加 `ToolOptions.Enabled map[string]bool`（缺省全开），构建时过滤。
- 权限：Agent 的 `tools.rules` 合入 `Turn.Rules`（现有 `harness.Rules` 选择器机制不变）。

### 4.2 MCP

- `Turn` 增加 `MCPServers []harness.MCPServer`（由 Host 按 Agent 名单解析后注入，含 `.mcp.json` 合并逻辑保持在 Host）。
- builtin：`runBuiltin` 用 `turn.MCPServers` 取代 `a.effectiveMCPServers()`。
- claude：经 `--mcp-config`（stdio server 列表）映射；codex：app-server 配置中按名单挂载（能力见 §4.5 映射表）。

### 4.3 Skills

- `Turn` 增加 `Skills SkillSelection{Allow, Deny []string}`；builtin 的 `DiscoverSkills` 结果按选择过滤。
- claude/codex 自身读项目 skills，Host 只能以文档方式说明（映射表标注"尽力而为"）。
- 管理 UI：设置页新增 Skills 区（项目发现的列表 + 每个 Agent 的 allow/deny）。

### 4.4 模型 / Provider

- Agent 引用 `provider + model + effort`；composer 现有模型选择器升级为 **Agent 选择器**（选择 Agent 即选定其模型；模型微调作为 Agent 的临时覆盖，写回线程而不写回 Agent）。

### 4.5 各 harness 的能力映射（诚实版）

| 能力 | builtin | claude CLI | codex app-server |
| --- | --- | --- | --- |
| 模型/effort | 全支持 | `--model` | thread/start `model`、`model_reasoning_effort`（已接） |
| 内置工具启停 | 完整（注册表过滤） | `--allowedTools/--disallowedTools` 映射 | **弱**：仅 sandbox/approval 策略级 |
| MCP 挂载 | 完整（进程内） | `--mcp-config` | 配置覆盖，按名单映射 |
| Skills | read_skill + 过滤 | CLI 自读项目 skills | 同左 |
| 系统提示词 | LoopConfig 追加 | `--append-system-prompt` | 不可注入（写明限制） |
| 用量统计 | 需新增采集（loop/llm 解析 usage） | result 行已带（已解析一半） | `tokenUsage/updated` 通知（已观测到） |

原则：配置统一声明，各适配器**尽力映射**，做不到的在 UI 上标注"该后端不支持"，不静默忽略。

## 5. 单 / 多 Agent 运行时

### 5.1 会话绑定 + 并发（前置重构）

- `Thread.AgentID`；新会话用当前选中的 Agent，旧会话迁移为 Default。
- **`a.running/a.cancel` 全局单飞 → `runs map[threadID]*runState{ctx, cancel, startedAt}`**。受影响的读取面：header 的 Working 徽标、composer 的 Stop、sidebar 的转圈、transcript 的 `isLast`/Regenerate 门控、Escape 停止（只停当前线程）。这组状态已全部走 VM 快照，改造是机械的。
- turnFor 按 `AgentID` 解析 Agent 快照组装 `Turn`（含 §4 的工具/MCP/Skills/提示词）。

### 5.2 多 Agent 协作（Phase 2 之后）

- **M1 会话内切换/并排**：一个会话可中途换 Agent（自然形成多 Agent 接力）；线程内每条消息记录产生它的 Agent。
- **M2 委托**：builtin 增加 `delegate` 内置工具（`{agent, task}`）——Host 复用 `harness.Run` 起子回合，结果作为工具结果返回；claude 后端映射到其自带子 Agent 能力。只做"父调子"，不做任意网状。
- **M3（可选，最后）**：编排视图（把多个 Agent 的输出在 UI 并排/接力）。不引入消息总线/常驻多 Agent 守护这类重机制。

## 6. 两端分工与服务层

### 6.1 服务层（唯一的事实源）

- 新增 `internal/server`：把 Host 的能力暴露为 **HTTP JSON API + SSE**。资源：`/agents` CRUD、`/config`、`/projects`、`/threads`（列表/消息/trace/搜索/导出）、`/threads/{id}/turns`（POST 发送、DELETE 停止）、`/events`（SSE：复用 `harness.Event` + 状态变更事件）。
- 实现方式：Host 现有动作已经是窄接口（Actions/turns/projector），server 是**同一批方法的 HTTP 壳**；桌面进程内调用，server 进程经同一 Host 代码路径。
- 认证：默认绑 `127.0.0.1` + 启动时生成的 token（配置可关）；远程部署交给反代。

### 6.2 Web 管理端（治理面）

- **Agent CRUD 与资源库**：Agent 表单（后端/模型/提示词/并发参数）；工具目录（启停 + 工具级权限）；MCP 服务器；Skills（allow/deny）；Provider 与模型；默认权限规则。
- **观测**：会话列表、trace 回放、用量面板（按 Agent/模型聚合）。
- **形态**：内嵌静态控制台，经 HTTP/SSE 访问 agentd，浏览器即用。mygo page 模式（webview + 生成的 TS client）是后续可叠加的路线——把同一控制台打包进桌面应用，共用同一服务层，仅传输不同。

### 6.3 桌面客户端（使用面，personal agent）

- **Agent 启动器**：home 演进为 Agent 卡片列表——"写作助手 / 代码评审 / 数据整理"这类通用功能 Agent，选一个即开会话；自由输入保留。
- 聊天、审批卡、workspace、终端、查看器照旧；线程与消息显示所属 Agent。
- Agent 在桌面是**只读详情 + 轻量覆盖**（审批模式、临时换模型；覆盖写线程不写 Agent）。复杂编排表单不进桌面——治理在 Web 端，单一事实源，避免双表单漂移。
- 过渡期（Web 管理端落地前，P1）：桌面设置页先放最小 Agent CRUD，P5 后降级为只读。

### 6.4 部署形态

- **单机个人（默认）**：桌面进程内嵌 Host 与本机服务层，浏览器打开本机控制台治理——Agent/资源/会话同盘同源。
- **远端共享（后续可选，P7）**：agentd 常驻服务器承载 Agent 与会话，Web 管理其上资源；桌面以"连接服务器"模式消费（会话与事件走 API+SSE）。此形态需要桌面 Host 支持远程后端，单列阶段。

## 7. 聊天记录追踪

- **事件 trace**：每线程新增 `events.jsonl`（append-only，一行一事件：`ts, kind, tool, ms, tokens, summary`），projector（`applyEvent`）是唯一写入点，四个后端天然统一。`finish` 时写一行 turn 汇总。
- **用量采集**：builtin 在 `llm.go` 响应解析处采集 usage（当前缺失，需补）；claude 取 result 行的 `duration_ms/cost/usage`；codex 订阅 `tokenUsage/updated`。按 线程/Agent/模型 聚合，UI 加用量面板。
- **搜索**：先做文件扫描的简单检索（标题 + 消息全文，够用且零依赖）；数据量大后迁移 sqlite（单文件、只读副本模式，不引入服务）。索引存 `<configDir>/search.db`。
- **回放**：Transcript 已按消息/卡片渲染；trace 视图按 turn 分组显示事件流水（工具、耗时、token），从线程详情进入。

## 8. 数据与迁移汇总

| 变更 | 内容 | 迁移 |
| --- | --- | --- |
| config v2 | `agents[]`、`default_agent`；全局 `backend/model/effort` 保留读取用于迁移 | 读 v1 自动折叠为 Default Agent |
| thread meta v2 | `agent_id` | 缺省 Default |
| events.jsonl | 新增，append-only | 无（老线程无 trace，从 messages 可推导粗粒度） |
| search.db | 新增（Phase 3 可选） | 可重建 |

## 9. 安全

- Agent 配置含 API key：沿用 config.json 0600；Web API 不回显 key（掩码）。
- delegate/多 Agent：子 Agent 继承父会话的审批模式起点，但**审批卡仍弹给用户**（spec/approvals.md 的"每次审批绑一个调用"不变）；不做"Agent 替 Agent 批准"。
- 服务层默认本机 + token；`mode` 语义不变（read-only 依旧先拒绝）。

## 10. 分阶段实施计划

> 量为专注人日的相对估算；每阶段独立可交付、可停。

**P0 配置地基（2–3 天）**
config v2 + Default Agent 迁移；`Thread.AgentID`；Host 增 `agents` 状态与解析。
验收：旧配置/旧线程无感升级；设置页能读出 Default。

**P1 Agent 配置化 + 桌面使用面（5–6 天，核心）**
`ToolCatalog` + `ToolOptions.Enabled`；`Turn` 增 MCPServers/Skills/SystemPrompt；turnFor 按 Agent 组装；桌面 home 改 **Agent 启动器**，composer 换 Agent 选择器；设置页放最小 Agent CRUD（过渡，Web 为最终归属）。
验收：建"只读 + 只开 grep/read + 挂 filesystem MCP"的 Agent 并跑通；从启动器选 Agent 开会话；claude/codex 的映射行为与 §4.5 表一致。

**P2 并发多会话（2–3 天）**
`runs map[threadID]…`；全部 `a.running` 读取面切到 per-thread；sidebar/header/composer 显示各自状态。
验收：两个会话分别用不同 Agent 同时跑、分别停。

**P3 会话追踪（3–4 天）**
projector 写 events.jsonl；三后端 usage 采集与聚合；搜索（文件扫描版）；线程详情 trace 视图。
验收：一次会话后能看到每工具调用的耗时/token；按关键词搜到历史会话。

**P4 服务层 agentd（4–5 天）**
`internal/server`：agents/threads/config API + SSE；桌面进程内同源。
验收：curl 能建 Agent、发消息、收到 SSE 事件流；桌面与 API 操作同一份数据无冲突。

**P5 Web 管理端（5–6 天）**
内嵌控制台：Agent CRUD 与资源库（工具/MCP/Skills/Provider）、会话列表与 trace 回放、用量面板；`mygo-agent web` 一键打开；桌面设置页降级为只读详情 + 轻量覆盖。
验收：浏览器完成"建 Agent → 配资源 → 桌面立即可见可用 → 看 trace"全流程；桌面不再出现复杂编排表单。

**P6 多 Agent 协作（3–5 天）**
消息记录产生者 Agent；`delegate` 工具（builtin）+ claude 子 Agent 映射；协作卡片 UI。
验收：父 Agent 委托子 Agent 完成子任务并在会话中可见两端流水。

**P7（可选）桌面远程连接**
桌面 Host 支持远程后端：连接常驻 agentd，会话与事件走 API+SSE，支撑团队共享 Agent 库（§6.4 远端形态）。
验收：桌面连远端 agentd 聊天，Agent 列表来自服务器，本地无缝切回。

## 11. 待定决策点（实施前需拍板）

- **A. Web 形态**：~~已定~~ Web = 治理面（创建/管理 Agent 与资源），桌面 = 使用面（personal agent）；控制台走浏览器（HTTP/SSE），mygo page 模式作为后续叠加（§6.2）。
- **B. 搜索存储**：文件扫描（零依赖，推荐 P3）还是直接上 sqlite？→ 推荐先扫描，量级触发再迁。
- **C. 多 Agent 协作形态**：仅 M2 委托（推荐）还是要 M3 编排视图？→ 先 M2，M3 看真实需求。
- **D. "插件"边界**：接受"内置工具 + MCP + Skills"三类即插件的全部，还是预留自有插件格式？→ 推荐前者（MCP 已是业界标准扩展面），预留注掉，不实现。
- **E. 并发深度**：P2 只做"多会话并发"，同会话内多 Agent 并行（投票/竞标）不在此方案内 —— 确认够用。
- **F. 过渡期管理**：P1 先在桌面放最小 Agent CRUD（Web 端落地前的唯一管理入口），P5 后降级为只读 —— 确认可接受。
