# 方案：群聊接力路由（Panel Router）

状态：**P1、P2 已落地 + 四轮迭代**（纲要/直通交接/总结/插话/护栏可配/trace/黄金集均已落地）（sequence 模式零行为变化；router 模式 + 三层护栏 + note 呈现 + 测试在 `internal/app/relayrouter.go` / `relayrouter_test.go`）；P3（@mention、设置 UI、路由器选首位发言者、`panel_blurb`）未启动。设置界面暂不暴露新字段，手编 config.json 即可；`syncAgent` 只回写 VM 已有字段，编辑其他设置不会抹掉新配置。目标：解决"群聊接力必须人工发一条消息才能推进、无法决定谁下一个发言、何时结束"的问题——引入 host 侧的路由决策步骤（supervisor 模式），由本地 Ollama 小模型担任协调者。仍不引入消息总线/常驻守护（spec/agents.md 的红线不变）：接力骨架（`finish()` → `dispatchParticipant`）保留，只把"固定 FIFO 队列"换成"每轮结束后的路由决策"。

## 1. 问题（代码事实）

- 触发面只有人工：`send/resend/regenerate` 是 `startTurn` 仅有的调用方（`internal/app/agent.go:14/31/43`）；agent 回复只写进共享转录，没有路径把"成员发言完成"转成新决策。
- 接力一轮即终：`nextPanelMember` FIFO pop（`agent.go:178`），队列空即 `runEnd`（`agent.go:212`）；每个成员只说一次，顺序 = `Agent.Panel` 配置顺序。
- 固定话术交接：下一位只收到 `panelNudge`（`agent.go:112`），上下文里没有"该谁发言、为什么"的信息。

对照主流设计（AutoGen SelectorGroupChat / LangGraph supervisor / CrewAI hierarchical）：共同点是**每轮结束后由 host 做一次路由决策（选下一个发言者或宣布结束），并配终止护栏**。本方案取 supervisor 模式，路由决策用独立的小模型调用。

## 2. 设计总览

```
用户消息 ──▶ startTurn ──▶ 派发成员① ──▶ finish
                                          │
                              ┌───────────┴───────────┐
                              │ sequence(现状)         │ router(新增)
                              │ FIFO pop 下一位        │ 路由调用(Ollama, 异步):
                              │ 队列空→runEnd          │ {"next":"<name>"|"","reason":…}
                              └───────────┬───────────┘
                                          ▼
                              next==nil → runEnd
                              否则追加 placeholder → dispatchParticipant ──▶ finish(循环)
```

- **路由器是一个独立的、便宜的一次性 chat 调用**，不走任何成员的 harness：输入 = 成员名册（名字 + 职责描述）+ 转录尾部摘要，输出 = 严格 JSON。Ollama 的 OpenAI 兼容端点（`http://localhost:11434/v1`）直接复用现有 `Provider` 配置，不需要新的客户端抽象层。
- **"什么时候结束"由路由器回答**：输出 `{"next":""}` 即结束接力；`panel_max_rounds` 和同成员连讲上限是硬护栏，路由器失去约束时兜底。
- **"哪个 agent 干活"由路由器回答**：从成员名册里挑，依据是各成员职责描述与当前对话所需。

## 3. 配置面（config v2 增量字段）

`Agent`（`internal/config/config.go:68`）新增，全部遵循"空值继承 app 默认"的现有惯例：

```jsonc
{
  "name": "主持 agent",
  "panel": ["架构师", "评审员"],          // 现有字段不变
  "panel_route": "router",               // "" | "sequence"(默认,现行为) | "router"
  "panel_max_rounds": 8,                 // router 模式下的接力派发次数上限;0 → 默认 8。sequence 模式忽略
  "router_provider": "prov-ollama",      // 空 → app 默认 provider
  "router_model": "qwen3:8b"             // 空 → app 默认 model
}
```

Ollama 作为普通 Provider 注册（设置页或手编 config.json，无需新概念）。chat 线路由（任意 OpenAI 兼容模型）与 decision 线路由（tev1 类）的配置示例：

```jsonc
// chat 线:协调者自拟 JSON
{ "id": "prov-ollama", "name": "Ollama", "base_url": "http://localhost:11434/v1",
  "wire": "chat", "models": ["qwen3:8b"] }
// agent: {"panel_route": "router", "router_provider": "prov-ollama", "router_model": "qwen3:8b"}

// decision 线:Jev 决策 API,答案受约束、带概率(同一个 provider 即可)
// agent: {"panel_route": "router", "router_provider": "prov-ollama",
//         "router_model": "tev1", "router_wire": "decision"}

// hybrid 线(推荐):大模型写态势简报 + 决策模型做最终裁决
// agent: {"panel_route": "router",
//         "router_provider": "<big-chat-provider>", "router_model": "<big-model>",
//         "router_wire": "hybrid",
//         "router_judge_provider": "prov-ollama", "router_judge_model": "tev1"}
```

成员职责描述：路由名册直接取各成员 `SystemPrompt` 的首行/头部（约 160 字符），**不新增描述字段**；路由质量不够时再考虑加 `panel_blurb`（P3 备选）。

## 3.5 决策线（decision wire，tev1 类模型）与混合线（hybrid）

`panel_route: "router"` 的协调者默认走 chat completions + JSON 提示词。Agent 另有两个新字段控制协调者形态：

- **`router_wire: "decision"`** — Jev 决策 API（Ollama ≥ 0.35 的 `/v1/systemone`，本地 `ollama.com/library/tev1` 一族）：
- **`router_wire: "hybrid"`** — 两级协调：**大模型（advisor）理解，决策模型（judge）裁决**。advisor（`router_provider/router_model`，chat 线，看 6 KiB 全摘要）写 2-3 句态势简报（当前阶段、已确立的结论、下一步该做什么/为何可以结束）；judge（`router_judge_provider/router_judge_model`，空则回退 router 字段）拿简报出 choice/noul 两题做最终裁决。advisor 失败不终止接力——回退为 decision 线的摘要态。

decision 线要点：

- **请求**：`POST {base}/v1/systemone`（judge 侧 Provider 的 base_url，如 `http://localhost:11434/v1`）。state 为结构化对象：members（名字→职责）、**request（本回合的用户原始请求）**、last_speaker、以及 brief（hybrid）或 conversation（decision 的摘要尾部）。
- **答案约束**：choice 的选项就是成员名，回答不可能跑到名册之外（chat 线的纠正重试在 decision 线不存在）；`noul` 的值即概率，`p(done) ≥ 0.5` 结束接力。单一成员的 panel 不出 choice 题（API 要求 2–26 个选项）；成员多于 26 个是降级。
- **预算**：tev1 类的可用上下文约 2k token——decision 线摘要上限 2500 字节，hybrid 线 judge 读简报不读原始摘要；成员职责用 `promptHead` 160 字符。
- **理由呈现**：judge 的概率与置信度进 note 与交接提示词；hybrid 再拼上 advisor 简报（note 截 200 字符，交接词完整）。
- **调参经验**（真机冒烟，2026-10）：纯 decision 线对单薄摘要 tev1 倾向判 done（p≈0.53）——根因是尾部摘要可能丢掉用户原始请求，"请求是否已解决"无从判断。hybrid 线把 request + 简报喂给 judge，正是为此；若仍过早结束，优先加重 done 题指令，必要时把 `doneProbability` 做成配置。

## 4. 路由器实现（新文件 `internal/app/relayrouter.go`）

- **请求**：非流式 `POST {base}/chat/completions`，`stream:false`、`temperature:0`、`response_format:{"type":"json_object"}`（Ollama 支持；不支持时靠解析兜底）。带 `Authorization: Bearer <key>`（Ollama 可留空 key）。单次调用超时 2 分钟（推理模型读长摘要可能超过半分钟，2026-10 实测 30s 会把健康路由掐成 "coordinator unavailable"），2 次尝试（429/5xx 与传输错误重试）。
- **System prompt**（草案）：

```
You are the coordinator of a panel of AI agents. Roster:
- 架构师: <SystemPrompt 头部>
- 评审员: <SystemPrompt 头部>

Conversation so far:
<panelDigest 尾部 6KB>

Decide which member should speak next. Respond ONLY with JSON:
{"next": "<member name>", "reason": "<one short sentence>"}
Use {"next": "", "reason": "..."} when the user's request has been fully
addressed and another reply would add nothing. You may pick the same
member again if they should continue. Never pick a member whose
specialty does not match what the conversation needs next.
```

- **解析**（宽容序）：`json.Unmarshal` 到 `{"next","reason"}` → 失败则截取文本中第一个 `{...}` 再试 → 仍失败视为路由失败（见 §6 降级）。`next` 按成员**名字**匹配（与 `Panel` 用名字的惯例一致），匹配不到也视为失败。
- **快照纪律**（同 `builtinHarness` 的既有规则）：名册、摘要、endpoint 全部在主线程快照后交给 goroutine；goroutine 回来必须重新走 `a.update` 并复验 `a.byID(th.ID) != nil && a.isRunning(th.ID)` 才派发——路由期间用户 Stop 或删线程则结果直接丢弃。

## 5. finish() 改造（`internal/app/agent.go:193`）

接力状态从 `groupQueue map[string][]string` 换成：

```go
type relayState struct {
    queue      []string // sequence 模式:剩余成员(FIFO 不变)
    rounds     int      // 本回合已派发的成员次数(router 模式计数)
    last       string   // 上一个发言成员名
    sameStreak int      // 同名连续发言次数(停滞护栏)
}
```

`finish` 消息落定后分流：

- **sequence**：现有路径原样保留（同步 pop → dispatch 或 runEnd），零行为变化，现有测试不动。
- **router**：
  1. `rounds+1 > maxRounds` 或 `sameStreak >= 3` → 追加一条 note block（"接力达到轮数上限/检测到停滞，结束"）→ `runEnd`；
  2. 否则主线程快照名册+摘要，`go a.routePanelTurn(snap)`；**运行注册表条目此时不释放**（不复用 `runEnd`），路由期间该线程保持"运行中"（composer 不能并发发送，Stop 仍有效——`stopThread` 清 `runs` 条目，路由结果复验时自然丢弃）；
  3. `routePanelTurn` 拿到决策后回 `a.update`：`next==""` 或复验失败/降级结束 → `runEnd` + 清队列 + `refreshGit`（对齐现有 `finish` 尾部）；否则按名字解析成员（中途被删则再路由一次，再失败 runEnd），追加 placeholder（AgentID = 该成员），把 `reason` 作为该消息第一条 note block 折叠展示，prompt 用 `"(Panel coordinator handed the floor to you: <reason>)"` 替换 `panelNudge`（builtin 成员不加，保持转录干净——理由已在其共享转录的 note block 里可见，CLI 成员拼在 digest prompt 里）。
  4. 每次派发前 `rounds++`，更新 `last`/`sameStreak`。

`startTurn`/`regenerate` 在 router 模式下仍以 `panel[0]` 起步（首发言者固定，后续全由路由器接管）；"路由器也选首位发言者"列为 P3 备选。

## 5.5 接力纲要（rolling outline）

每条成员回复落定时，`finish` 把一条有界 gist 追加进 `relayState.outline`（`名字: 要点≤120字`）。三条协调者线都读它：chat 简报在轮次状态后列出 "What each reply established: …"，decision 线进 `state.outline`，hybrid 的 advisor 同样可见。长讨论的早期决策不再被 6KB 尾巴截掉——这是摘要截尾问题的接力层解法。

## 5.6 成员直通交接（explicit handoff，swarm 混合）

成员回复末尾 `@成员名` 即直接交棒：`routeRelay` 的 default 分支先查 `explicitHandoff`，命中（且非自指）就直接派发——省一次路由调用；同名连讲/轮数等护栏先于直通交接评估，是中央权威的底线。面板协议教成员在"明确知道谁接棒"时使用，否则留给协调者。trace 里记 `handed off directly by X (@Y)`。

## 5.7 路由黄金回归集（golden set）

`relayrouter_golden_test.go`：四个脚本化场景（固定转录+轮次状态 → 期望 next 集合或 done），`MYGO_GOLDEN_BASEURL`(+MODEL/KEY) 门控跑在真模型上。断言是性质（接受名字集合、负向断言"已发言者不得重复"），不钉死唯一答案——真模型是概率性的，钉死名字的黄金集会按日程表失败。2026-10 对 MiniMax M3.1 Flash 四场景全命中。

## 6. 护栏（终止条件可配置，spec/relay-router.md）

路由接力的终止条件在 agent 上逐项可配（零值 = 各自默认或关闭），每次派发前依次评估，触发即落 note 结束接力：

| 配置 | 零值默认 | 语义 |
|---|---|---|
| `panel_max_rounds` | 8 | 单回合成员派发次数上限（AutoGen `MaxMessageTermination` 的对应物） |
| `panel_max_tokens` | 关 | 接力全程的成员 token 预算（finish 落定时从各消息的 turn 累计值汇入 `relayState.tokens`） |
| `panel_timeout` | 关 | 接力整体墙钟秒数上限（悬挂的成员回合另有 per-request 超时兜底） |
| `panel_stall_rounds` | 3 | 同成员连续发言上限（Magentic-One 停滞检测的最小版本，防互相恭维空转） |

外加一条不可配置的底线：**降级**——路由调用失败（超时/非 JSON/名字不匹配，重试后仍失败）→ 追加 note"路由不可用，接力结束"→ `runEnd`。**绝不因路由器挂掉把用户卡在运行态**。

## 6.5 中途插话（interjection）

运行中的 routed 接力不再挡用户发言：composer 的发送键在有草稿时变为"插话"（Enter 同样生效）。

- `send()` 在运行态改走 `interject()`：用户消息**立即入转录**（UI 即时可见），并排入 `relayState.pending`；下一个成员被派发时，交接提示词拼上 `The user added while the panel was talking: …`——话头连同协调者的理由一起交给接棒者。正在发言的成员看不到插话（其上下文已快照），与 Slack 群聊的直觉一致。
- sequence 接力与 solo 回合不可插话（draft 保留，行为同旧版）：sequence 的队列是死的，插话语义不成立。
- 协调者的裁决自然会看到插话（转录/简报已包含），无需特殊处理。

## 6.6 总结陈词（wrap-up）

接力结束（协调者判 done、或任一护栏触发）不再直接收场——最后一句是最后一个专业角色的发言，用户得自己拼结论。收尾时追加一个**总结回合**：

- **总结者**：`panel_summarizer`（成员名）指定；空则线程绑定的 agent（如「产品流水线」自己）。
- **任务**：基于全部讨论写面向用户的最终结论——决策/推荐、关键理由、开放问题与下一步；简明，不复述每段发言。
- **触发**：`wrapUpRelay`——先按 §6 落 note 拆状态，再派发总结回合；总结回合结束时 `finish` 找不到接力状态，自然终止。`rounds == 0`（无人发言）不总结。
- 被护栏中断的接力同样总结：未完成的工作也得告诉用户"哪些已定、哪些悬置"。

## 7. UI

- 路由决策以 `blockNote` 呈现在每个成员消息卡上："→ 评审员：方案风险需要独立审查"（复用现有 note 折叠渲染，`internal/app/types.go:23`）。
- 设置面：panel_route / panel_max_rounds / router provider+model 的编辑项（agents 设置 ViewModel+Actions 已就位，加字段）；落地前允许手编 config.json。
- @mention（P3，可选）：composer 消息里的 `@名字` 命中 panel 成员 → 该成员第一个发言，其余交路由器。只做快捷路径，不做新机制。

## 8. 测试计划

- `relayrouter_test.go`：httptest 假 OpenAI 兼容服务（`builtin/llm_test.go` 有先例）。覆盖：正常 JSON；```json 围栏包裹；前后夹杂文本；非法 JSON → 降级；超时与 5xx 重试后降级；`response_format` 被服务端忽略仍可解析。
- app 层（参照现有 agents/harness 测试的 fake provider 模式）：router 模式下按 mock 路由器指示两成员交替发言多轮；`panel_max_rounds` 截断并落 note；同成员连讲 3 次截断；路由返回 `""` → runEnd；路由期间 `stopThread` → 不派发、注册表清空；成员中途被删 → 重路由一次后结束。
- 回归：sequence 模式现有测试不改一行通过。

## 9. 分阶段

- **P1**：config 字段 + `relayState` 改形 + sequence 行为零变化（重构不引入新行为）。
- **P2**：relayrouter.go（客户端+解析）+ finish() router 分流 + 护栏 + note 呈现。完成后核心问题解决。
- **P3**（可选）：@mention、设置 UI 字段、路由器选首位发言者、`panel_blurb` 成员描述字段。
