# 方案：群聊接力路由（Panel Router）

状态：**已全部落地**。sequence 模式（原始行为）与 router 模式并存；router 侧已交付：三线协调者（chat / decision / hybrid）、路由选首位发言者、@点名三入口与并行 fan-out、回声防护、接力纲要、面板协议、可配置护栏、总结陈词、中途插话、路由 trace、黄金回归集、设置 UI 全字段。经验沉淀（坑与模式）在 [relay-lessons.md](relay-lessons.md)，本文是契约。目标：解决"群聊接力必须人工发一条消息才能推进、无法决定谁下一个发言、何时结束"——host 侧的路由决策（supervisor 模式）。不引入消息总线/常驻守护（spec/agents.md 红线不变）：接力骨架（`finish()` → 派发）保留，固定 FIFO 队列换成"每轮结束后的路由决策"。

## 1. 问题（原始代码事实）

- 触发面只有人工：`send/resend/regenerate` 是 `startTurn` 仅有的调用方；成员发言完成无法转成新决策。
- 接力一轮即终：FIFO pop 队列空即结束，每成员只说一次，顺序 = `Panel` 配置顺序。
- 固定话术交接：下一位只收到 `panelNudge`，上下文没有"该谁发言、为什么"。

对照主流（AutoGen SelectorGroupChat / LangGraph supervisor / CrewAI hierarchical）：共同点是**每轮结束后 host 做一次路由决策（选下一个发言者或宣布结束），并配终止护栏**。

## 2. 设计总览

```
用户消息(可含 @点名) ──▶ startTurn
        │ @点名 → 并行 fan-out(被点名者同时开工)      │ 无点名 → 协调者选首位
        ▼                                            ▼
   成员回合 ──▶ finish ──▶ 路由决策(异步,运行注册表保持持有)
                              │  守护栏先评估(轮数/token/超时/停滞)
                              │  插话里的 @点名 优先
                              │  成员回复的 @点名 直通交接(每成员每回合一次)
                              ▼
        next==""(完成) → 总结回合 → 结束
        否则 → placeholder + 交接词 → 派发 ──▶ finish(循环)
```

- 协调者是独立、便宜的一次性调用，不走任何成员的 harness；输入 = 名册（职责描述）+ 用户原始请求 + 轮次状态 + 接力纲要 + 转录尾部。
- "谁下一个发言"与"何时结束"都由协调者回答（`{"next":"<name>"|"","reason":…}`）；护栏是协调者无法说服的硬底线。

## 3. 配置面（config v2，Agent 增量字段）

全部遵循"空值继承/默认"惯例：

```jsonc
{
  "panel": ["架构师", "评审员"],        // 现有字段:成员名(与 mcp_servers 同惯例)
  "panel_route": "router",             // "" | "sequence"(默认) | "router"
  "panel_max_rounds": 8,               // 派发次数上限;0 → 默认 8。sequence 忽略
  "panel_max_tokens": 0,               // 接力全程 token 预算;0 关闭
  "panel_timeout": 0,                  // 接力整体墙钟秒数;0 关闭
  "panel_stall_rounds": 0,             // 同成员连讲上限;0 → 默认 5
  "panel_summarizer": "",              // 总结者成员名;空 → 线程绑定的 agent
  "panel_blurb": "",                   // 成员职责行(路由名册读);空 → system prompt 首行
  "router_provider": "",               // 协调者(或 hybrid advisor)provider;空 → app 默认
  "router_model": "",                  // 同上
  "router_wire": "",                   // "" (chat) | "decision" | "hybrid"
  "router_judge_provider": "",         // decision/hybrid 的 judge;空回退 router 字段
  "router_judge_model": ""
}
```

**两种角色，两套配置，别混淆**：带 panel 的 agent（群主）的 `router_*` 是协调者的模型；该 agent 自己的 backend/provider/model/mode/tools 是**总结回合**（§6.6）的执行配置——接力结束时由绑定的 agent（除非 `panel_summarizer` 另指）亲自写结论，用的是它自己的档案。设置页的 Group 表单里后者就是为此服务的。

成员职责：路由名册优先读 `panel_blurb`（≤200 字符），空则 `system_prompt` 首行（`promptHead`，160 字符）——路由依据与角色人格解耦，调路由措辞不影响成员行为。

## 3.5 决策线（decision）与混合线（hybrid）

- **chat**（默认）：非流式 chat completions，temperature 0，`response_format: json_object`；宽容解析（严格 JSON → 剥围栏 → 剥夹文本）；念错名册名字有一次纠正往返。单次调用超时 2 分钟（推理模型读长摘要超过 30 秒是正常的，2026-10 实测 30s 会把健康路由掐成不可用），2 次尝试（429/5xx/传输错误）。
- **decision**：Jev 决策 API（Ollama ≥ 0.35 `/v1/systemone`，tev1 类）。choice 题的选项就是成员名（不可能答到名册外），noul 题判完成（`p ≥ 0.5` 结束）；单一成员 panel 不出 choice 题（API 限 2–26 选项）。上下文约 2k token：judge 读结构化 state（members/request/last_speaker/spoken_history/outline），不读原始长摘要。
- **hybrid**：大模型（advisor，chat 线，读 6KiB 全摘要）写 2-3 句态势简报，judge（decision 线）对简报做最终裁决；advisor 失败降级为 decision 线，不是单点依赖。判决理由（概率/置信度/简报）进 note 与交接词。

chat 简报的构成顺序（被截断的只会是尾巴）：名册 → 用户原始请求 → 轮次状态（`N replies so far. Speakers in order: … Not yet spoken: …`）→ 接力纲要（每成员一条 gist）→ 6KiB 对话尾巴 → 指令（先公平轮转后按需路由；开放选项不是 done，路由给能拍板的人）。这个顺序是实测调出来的：点名状态和请求放后面就丢。

## 4. 路由器实现（`internal/app/relayrouter.go`）

- 三线共享 `routerPost`（有界 POST + 瞬态重试）；决策统一成 `relayRoute{Next, Reason, Tokens}`。
- **快照纪律**：名册、摘要、endpoint 在主线程快照后交给 goroutine；回来必须 `a.update` 并复验线程存在且运行中，路由期间 Stop/删线程则决策丢弃。
- **trace**：每次决策落一行（kind `route`：结果/失败、耗时、reply 报告的 token）——协调成本可审计。
- **黄金回归集**（`relayrouter_golden_test.go`）：四个脚本化场景，`MYGO_GOLDEN_BASEURL`(+MODEL/KEY) 门控跑真模型。断言是性质（接受名字集合、负向断言），不钉死唯一答案。改协调者提示词后必跑。

## 5. 接力状态与 finish 分流

```go
type relayState struct {
    queue      []string  // sequence:剩余成员(FIFO 原样)
    rounds     int       // 本回合已派发次数
    last       string    // 上一发言成员(停滞护栏)
    sameStreak int       // 连续发言计数
    tokens     int64     // 成员已耗 token(预算护栏)
    start      time.Time // 接力开始(超时护栏)
    pending    []string  // 用户插话,等下一次交接
    spoken     []string  // 派发顺序(轮次状态,协调者读)
    retries    int       // 成员回合重试计数
    outline    []string  // 接力纲要:每成员一条 gist
    honored    map[string]bool // 成员 mention 已派发过的成员(回声防护)
    batchLeft  int                   // 并行批次在途数
    batchByAt  map[int]*batchMember  // 并发成员(按消息槽)
    batchSeq   []*batchMember        // CLI 成员,批次后串行
}
```

`finish` 分流：**sequence** 原样（FIFO pop，零行为变化）。**router**：

1. 批次成员落定 → 合并其 fork 转录、递减计数；失败重试一次（同成员，新槽）；批次未空则等待；CLI 排队成员依次补位；**全部落定才路由**。
2. 普通成员落定 → token/纲要入账 → 失败重试一次 → `routeRelay`。
3. `routeRelay` 顺序：护栏（触发即总结收场）→ 插话里的 @点名 → 成员回复的 @点名（honored 过滤）→ 协调者（异步，注册表保持持有——线程保持运行态：无并发发送、Stop 有效、决策回来复验）。
4. 首位发言者同样由协调者选（`routeFirstSpeaker`，用户 @点名优先）；兜底：协调者失败/答空/念错名 → `panel[0]`——接力死在开跑前比默认选人更糟。
5. 每次派发更新 `rounds/last/sameStreak/spoken/outline`。

## 5.4 转录降噪（按种类跨位置折叠）

成员工作日志把散文和命令/思考交错，按相邻 run 折叠只剩一圈塌行。`itemize` **按种类跨位置分组**：同种类（command/reasoning/note；diff 另绑文件）≥2 次全部折进一个组（"5 commands · 4.1s"），组落首个成员位置，散文保持原位；单次出现仍是普通卡片。`ToggleBlock` 对可分组种类做全种类翻转；error/approval 刻意不分组。heredoc 写文件（多行命令）落定自动展开并显示完整命令文本——内容在命令里，不展开等于工作成果不可见。

## 5.5 接力纲要（rolling outline）

每条成员回复落定时追加一条 gist（`名字: 要点≤120字`）进 `relayState.outline`，三条协调者线都读（列在摘要之前——截断只会截尾巴）。长讨论的早期决策不再被 6KiB 尾巴截掉。

## 5.6 @点名与并行 fan-out

`@成员名` 统一语法，`extractMentions`（出现顺序、去重）供三入口共享：

- **输入框**（新回合）：router 下 @n 人直接并行开工，不经协调者选首位；sequence 只把被点名者提到队首。
- **插话**（运行中）：点名是**路由指令**——handoff 消费插话时把含 mention 的留在 pending，交给落定后的 `routeRelay`；普通插话才随交接词送达。
- **成员回复**：@一人直通交接（省一次路由调用）；@多人起 fan-out。

**并行 fan-out**：每人独立消息槽并发运行；builtin 成员用 `forkMemory` 隔离转录视图（派发时快照共享 ChatLog，落定时按完成顺序并入私有后缀——两条并发循环绝不互写一份历史）；CLI 成员共享线程会话，排在并发批次之后串行。全部落定才路由。护栏先于 fan-out 评估；注册表持有多份 cancel（`runAdd`/`runRelease`），Stop 一次取消全部；批次成员失败重试一次。

**回声防护（honored）**：成员回复必然引用请求里的 @，把回声当新交接 = 同一成员每轮重跑（轮数兜底的循环，协调者全程没有发言权）。成员 mention 每回合对每人只生效一次，之后回落协调者；**用户亲自 @ 不受限**。

## 5.7 面板协议（panel protocol）

成员提示词是对着人写的，遇到选择的本能是把题抛回用户——面板当场停摆，协调者还顺势收场。派发时在成员 system prompt 的**拷贝**上追加协议：给明确推荐（方案+理由+成本）让面板推进，不把选择题交回用户；真正的业务取舍才留给用户且仍先给推荐；可用 `@名字` 直通交接。三个实现要点：用拷贝（路由名册与设置页读的仍是干净档案）；忽略 system prompt 的后端（codex/pi）协议改走交接词文本（先查证每个适配器实际消费哪些字段）；协议内容即路由行为的契约，改动需过黄金集。

## 6. 护栏（终止条件可配置）

每次派发前依次评估，触发即落 note 总结收场（§6.6）：

| 配置 | 零值默认 | 语义 |
|---|---|---|
| `panel_max_rounds` | 8 | 派发次数上限（AutoGen `MaxMessageTermination` 对应物） |
| `panel_max_tokens` | 关 | 接力全程 token 预算 |
| `panel_timeout` | 关 | 整体墙钟秒数（单请求另有超时兜底） |
| `panel_stall_rounds` | 5 | 同成员连讲上限（太小会掐死连续干活的执行者） |

不可配置的底线：**降级**——协调者失败（超时/坏 JSON/名字不符，重试后仍败）→ note → 总结收场。绝不因路由器挂掉把用户卡在运行态。

## 6.5 中途插话（interjection）

运行中的 routed 接力不挡用户发言：composer 有草稿时发送键变"插话"（Enter 同效）。`interject()` 把消息立即入转录并排入 `pending`；正在发言的成员看不到（上下文已快照）；无 mention 的插话随下一次交接词送达，含 mention 的作为路由指令生效。sequence/solo 不可插话（队列是死的，语义不成立）。

## 6.6 总结陈词（wrap-up）

接力结束（判 done、护栏、降级）不再停在最后一个专业角色的发言上：`wrapUpRelay` 先落 note 拆状态，再派发**总结回合**——总结者（`panel_summarizer` 指定，空则线程绑定的 agent）基于全部讨论写面向用户的结论（决策/推荐、关键理由、开放问题）。总结回合用总结者自己的执行档案运行（模型/模式/工具）；它落定时接力状态已不存在，`finish` 自然终止。`rounds==0` 不总结；被护栏中断的接力同样总结（未完成也得说清"哪些已定、哪些悬置"）。

## 7. UI

- 路由决策以 note 呈现：`→ 成员: 理由`（含概率/置信度或简报摘录）；结束原因落 note。
- 设置页 GROUP RELAY 区全字段：成员、Sequence/Router 段选、Chat/Decision/Hybrid 段选、blurb、轮数、停滞、token 预算、超时、总结者、路由 provider/model——数字留空即默认或关闭，typo 保留原值；回环测试钉住"重渲染不漂移"。群主 agent 的执行字段（backend/model/mode/tools）服务总结回合（§3）。
- composer：运行中且有草稿 → 插话键；placeholder 提示 `@name` 语法。
- 转录：运行中全程 spinner；markdown 全部可选中复制；按种类折叠（§5.4）。
- header：接力运行中显示 `relay N`（·`Xk/Yk tokens` 设了预算时）。

## 8. 测试

- 单元：解析宽容序、三线 wire（httptest 假服务）、护栏表驱动、mention 顺序/去重、itemize 分组。
- 接力链路：fan-out 真并发（**到达时间重叠断言**——barrier channel 会被总结回合等晚到请求卡死，`srv.Close()` 连带挂死整个测试）、批次汇合后路由、回声不重复派发（回复引用 @ 只跑一次，之后协调者接管）、插话 mention、Stop 丢弃在途决策、sequence 零行为变化。
- 黄金集（§4）跑真模型；race 全量必须绿（fixture 共享变量加锁是真实教训，见 relay-lessons.md §9）。

## 9. 迭代路径（已全部走完）

固定顺序接力 → 路由决策（chat）→ 护栏+降级 → 轮次状态+面板协议 → 点名/直通交接/并行 fan-out → 总结回合 → trace+黄金集 → UI 打磨。每步独立交付且向后兼容：无 `panel_route` 的旧配置行为不变。
