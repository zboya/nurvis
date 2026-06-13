# Nurvis — 本地优先的多 Agent 运行时

> 一个本地优先 Agent 平台。通过在进程内直接调用 `llama.cpp` 进行本地推理，**数据不离电脑**；同时支持用户创建多个 Agent，为不同任务（编程 / 画图 / 设计等）绑定不同模型、工具、工作区与对话渠道。

---

# 重要
- 该文档可以随着项目的演进自动更新，避免文档腐败。
- 开发中使用`go mod vendor`来管理依赖，方便检索代码。
- 环境变量使用 `NURVIS` 前缀。
- 调试桌面软件可以使用`make desktop-dev`来启动。
- 后台内置的提示词都使用英文。
- 代码注释都使用英文。
- 单文件的代码尽量不要超过1000行，前后端都是。

## 1. 设计目标

- **本地优先 / 隐私**：默认所有推理在 Nurvis 进程内通过 yzma 调用 `llama.cpp` 完成，会话、配置、记忆全部落地 SQLite，不上传云端、不需要任何外部模型服务进程。
- **可扩展抽象**：核心能力（Provider / Tool / Channel / Skill / MCP / Memory / Scheduler）全部面向接口编程，新增实现不改主流程。
- **多 Agent 多任务**：每个 Agent 是「模型 + 系统提示 + 工具集 + 默认工作区 + 渠道」的组合，互相隔离。
- **统一网关**：交互收敛 WebSocket JSON-RPC Gateway，桌面软件可以部分使用 wails3 的函数接口。
- **可观测**：每次 Agent Loop 的 8 个阶段都产生事件，经事件总线广播，便于流式 UI 与排障。
- **桌面软件**：使用 [wails3](https://v3.wails.io/quick-start/why-wails/) 实现桌面软件。

---

## 2. 技术栈

| 维度 | 选型 | 说明 |
|------|------|------|
| 语言 | Go 1.22+ | 适当使用泛型（Registry、EventBus、Result[T]） |
| 存储 | SQLite（`modernc.org/sqlite`，纯 Go，免 CGO） | 配置 + 会话 + 记忆 + 任务 |
| 迁移 | `golang-migrate` 或自研 embed 迁移 | schema 版本化 |
| 网关 | WebSocket（`coder/websocket`）+ JSON-RPC 2.0 | 单一入口 |
| LLM 推理 | [yzma](https://github.com/hybridgroup/yzma) + `llama.cpp` 动态库 | 进程内推理，purego，免 CGO |
| 模型来源 | HuggingFace Hub（`*.gguf`） | 直接 `https://huggingface.co/<repo>/resolve/main/<file>.gguf` 下载 |
| Tool calling 解析 | `github.com/hybridgroup/yzma/pkg/message`（`ParseToolCalls`） | 支持 Standard / Qwen / GLM / Mistral / Gemma / GPT / Phi-4 等格式 |
| MCP | 官方 `mcp-go` SDK（stdio / SSE / Streamable HTTP） | 工具扩展 |
| 调度 | `robfig/cron/v3` | 定时任务 |
| 渠道 | 微信、QQ（先接入） | 见 §10 |
| 桌面 | Wails3 | 复用 Gateway 协议 |

---

## 3. 整体架构

```mermaid
flowchart TB
    subgraph Clients[客户端层]
        Desktop[Wails3 桌面端]
        WeChat[微信 Channel]
        QQ[QQ Channel]
        API[第三方 API]
    end

    subgraph Gateway[Gateway 层 - WebSocket JSON-RPC]
        WS[WS Server]
        Router[Method Router]
        Sub[Event Subscriber 推送]
    end

    subgraph Core[核心运行时]
        AgentMgr[Agent Manager]
        Loop[Agent Loop 8 阶段]
        Bus[(Event Bus)]
        Sched[Scheduler 定时任务]
    end

    subgraph Capabilities[能力抽象层 - 接口]
        Provider[Provider / yzma]
        ToolReg[Tool Registry]
        MCPMgr[MCP Manager]
        SkillMgr[Skill Manager]
        Memory[Memory Store]
        WS_Mgr[Workspace Manager]
    end

    subgraph Infra[基础设施]
        Runtime[yzma Runtime 加载 llama.cpp 动态库]
        ModelMgr[Model Manager 扫描/下载 GGUF]
        HW[Hardware Probe 内存/GPU]
        DB[(SQLite)]
    end

    Clients --> WS --> Router
    Router --> AgentMgr --> Loop
    Loop --> Provider & ToolReg & Memory & WS_Mgr
    ToolReg --> MCPMgr & SkillMgr
    Loop --> Bus --> Sub --> Clients
    Sched --> AgentMgr
    Provider --> Runtime
    Provider --> ModelMgr
    Runtime --> HW
    ModelMgr --> DB
    AgentMgr --> DB
    Memory --> DB
```

要点：
- **Gateway 不含业务逻辑**，只做协议解析、鉴权、路由、把内部事件推给订阅者。
- **能力抽象层全是接口**，主流程（Agent Loop）只依赖接口，不依赖具体实现。
- **Event Bus 是中枢**：Loop 各阶段、Channel 入站消息、Scheduler 触发都通过它流转。
- **推理在进程内**：yzma Runtime 通过 `purego` 加载 `llama.cpp` 动态库，没有外部 server 进程，也不需要 HTTP 协议层。

---

## 4. 目录结构

```
nurvis/
├── cmd/
│   ├── nurvisd/main.go            # 守护进程入口（启动 Gateway + 后台服务）
│   └── nurvis-desktop/            # Wails3 桌面端入口
│       ├── main.go
│       └── app.go
├── internal/
│   ├── app/
│   │   ├── app.go                 # 依赖装配 (wiring)、生命周期
│   │   ├── channel.go             # Channel 实例构建
│   │   ├── cron.go                # Scheduler 构建
│   │   └── model_meta_adapter.go  # ModelMeta 适配器
│   ├── gateway/
│   │   ├── server.go              # WS server
│   │   ├── methods.go             # JSON-RPC 方法路由
│   │   ├── methods_agents.go      # Agent 相关方法
│   │   ├── methods_channels.go    # Channel 相关方法
│   │   ├── methods_chat.go        # Chat 对话方法
│   │   ├── methods_credentials.go # 凭证管理方法
│   │   ├── methods_cron.go        # 定时任务方法
│   │   ├── methods_mcp.go         # MCP 管理方法
│   │   ├── methods_models.go      # 模型管理方法
│   │   ├── methods_projects.go    # 项目管理方法
│   │   ├── methods_runtime.go     # 运行时状态方法
│   │   ├── methods_setting.go     # 设置方法
│   │   ├── methods_skills.go      # Skill 管理方法
│   │   ├── methods_tools.go       # 工具方法
│   │   ├── nethods_sessions.go    # Session 方法（typo preserved）
│   │   └── middleware.go          # 鉴权等中间件
│   ├── agent/
│   │   ├── manager.go             # Agent CRUD、实例缓存
│   │   ├── loop.go                # 8 阶段编排
│   │   ├── loop_media.go          # 多模态 Loop（to-image/to-video）
│   │   ├── loop_test.go           # Loop 测试
│   │   ├── stage.go               # Stage 接口
│   │   ├── stage_act.go           # act 阶段
│   │   ├── stage_check.go         # check 阶段
│   │   ├── stage_context.go       # context 阶段
│   │   ├── stage_finalize.go      # finalize 阶段
│   │   ├── stage_history.go       # history 阶段
│   │   ├── stage_prepare.go       # prepare 阶段
│   │   ├── stage_prompt.go        # prompt 阶段
│   │   ├── stage_prune.go         # prune 裁剪阶段
│   │   ├── stage_think.go         # think 阶段
│   │   ├── prompt.go              # 系统提示组装
│   │   ├── attachments.go         # 附件处理
│   │   ├── message_buffer.go      # 消息缓冲
│   │   ├── tag.go                 # Agent tag（to-text/to-image/to-video）
│   │   └── tokencount.go          # Token 计数
│   ├── provider/
│   │   ├── provider.go            # Provider 接口
│   │   ├── llama.go               # 本地 llama.cpp Provider 实现（默认）
│   │   └── openai.go              # OpenAI 兼容实现（远程模型可选）
│   ├── backends/                  # 推理后端封装
│   │   ├── llamax/                # llama.cpp Runtime（进程内单例）
│   │   │   ├── runtime.go         # llama.Load / Init / Close 全局生命周期
│   │   │   ├── engine.go          # Engine: 模型加载 + 流式 Chat 生成
│   │   │   ├── engine_test.go     # Engine 测试
│   │   │   ├── install.go         # 首次启动自动下载 llama.cpp 动态库
│   │   │   ├── platform_unix.go   # Unix 平台适配
│   │   │   └── platform_windows.go # Windows 平台适配
│   │   └── gosd/                  # Go Stable Diffusion Runtime（图像生成）
│   │       ├── runtime.go         # sd-server 生命周期管理
│   │       ├── engine.go          # Engine: sd 推理 + API 调用
│   │       ├── install.go         # 首次启动自动下载 sd-server
│   │       └── types.go           # GoSD 类型定义
│   ├── modelmgr/                  # 本地 GGUF 模型管理
│   │   ├── manager.go             # 扫本地模型目录 / 删除 / 元数据
│   │   ├── pull.go                # HuggingFace GGUF 下载（断点续传 + 进度）
│   │   ├── library.go             # HuggingFace 推荐模型清单（默认精选 + 可扩展）
│   │   └── hf_detail.go           # HuggingFace 模型详情解析
│   ├── hardware/
│   │   ├── hardware.go            # 内存/GPU 探测 + 模型推荐
│   │   └── hardware_test.go       # 硬件探测测试
│   ├── tools/                     # 内置工具 + Tool Registry
│   │   ├── tool.go                # Tool 接口 + Registry
│   │   ├── register.go            # 工具注册
│   │   ├── exec.go                # 命令执行
│   │   ├── fs.go                  # 文件读写
│   │   ├── http.go                # HTTP 请求
│   │   ├── edit_file.go           # 文件编辑
│   │   ├── grep.go                # 内容搜索
│   │   ├── glob.go                # 文件模式匹配
│   │   ├── web_preview.go         # Web 预览
│   │   ├── channel.go             # Channel 工具
│   │   ├── cron.go                # Cron 工具
│   │   ├── skill.go               # Skill 工具
│   │   ├── cf_pages_upload.go     # Cloudflare Pages 上传
│   │   ├── publish_cf_pages.go    # Cloudflare Pages 发布
│   │   └── new_tools_test.go      # 新工具测试
│   ├── mcp/manager.go             # MCP Manager（client 连接、工具注册到 Registry）
│   ├── skill/
│   │   ├── manager.go             # Skill Manager（加载、授权、转 Tool）
│   │   ├── parser.go              # Skill manifest 解析
│   │   └── parser_test.go         # 解析测试
│   ├── preview/
│   │   ├── handler.go             # 预览 HTTP handler
│   │   └── registry.go            # 预览注册表
│   ├── workspace/workspace.go     # Workspace/Project 管理（本地目录）
│   ├── memory/store.go            # 会话历史 + 长期记忆
│   ├── bus/
│   │   ├── bus.go                 # 泛型 Event Bus
│   │   └── topics.go              # 主题常量
│   ├── scheduler/scheduler.go     # cron 定时任务
│   ├── channel/
│   │   ├── channel.go             # Channel 接口
│   │   ├── dispatcher.go          # 入站调度（去重/防抖/路由）
│   │   ├── wechat/channel.go      # 微信 Channel 实现
│   │   └── qq/
│   │       ├── channel.go         # QQ Channel 实现
│   │       └── media.go           # QQ 媒体处理
│   ├── store/
│   │   ├── store.go               # SQLite 封装
│   │   ├── migrations/            # *.sql schema 迁移
│   │   └── repo/                  # 各实体 Repository（DAO）
│   │       ├── repo.go
│   │       ├── agent.go
│   │       ├── builtin_tool.go
│   │       ├── channel.go
│   │       ├── cron.go
│   │       ├── mcp.go
│   │       ├── memory.go
│   │       ├── message.go
│   │       ├── model.go
│   │       ├── project.go
│   │       ├── session.go
│   │       ├── settings.go
│   │       ├── site_credential.go
│   │       └── skill.go
│   └── version/version.go         # 版本信息
├── frontend/
│   ├── index.html
│   ├── embed.go                   # Go embed 前端资源
│   ├── package.json
│   ├── vite.config.ts
│   ├── tsconfig.json
│   ├── public/
│   ├── bindings/                  # Wails3 生成的 TS 绑定
│   └── src/
│       ├── main.tsx
│       ├── App.tsx                # 根组件（连接→引导→主界面）
│       ├── index.css              # Tailwind + OKLCH 设计体系
│       ├── lib/
│       │   ├── ws.ts              # WebSocket JSON-RPC 客户端
│       │   ├── constants.ts       # WS_URL / API_BASE
│       │   └── tool-labels.ts     # 工具标签映射
│       ├── types/
│       │   ├── index.ts           # Agent / Session / Message 等类型
│       │   └── wails-bindings.d.ts
│       ├── stores/
│       │   ├── ui-store.ts        # theme / view / activeAgentId
│       │   ├── chat-store.ts      # messages / isRunning / 流式状态
│       │   └── model-store.ts     # 模型列表 / 推荐 / 拉取状态
│       ├── hooks/
│       │   ├── use-agents.ts      # agents CRUD
│       │   ├── use-sessions.ts    # sessions CRUD
│       │   ├── use-chat.ts        # WS 事件订阅 + sendMessage + abort
│       │   ├── use-runtime.ts     # runtime 状态
│       │   ├── use-model.ts       # 模型操作
│       │   ├── use-model-capabilities.ts # 模型能力查询
│       │   └── use-projects.ts    # 项目 CRUD
│       ├── services/              # API 服务封装
│       └── components/
│           ├── ui/index.tsx       # Button / Input / Textarea / Spinner
│           ├── common/            # 通用业务组件
│           ├── onboarding/
│           │   ├── OnboardingWizard.tsx  # 两步引导
│           │   ├── SetupStep.tsx         # 硬件探测 + 库下载 + 模型拉取
│           │   ├── AgentCreateStep.tsx   # 预设角色选择 + 创建 Agent
│           │   └── ModelSearchDialog.tsx # 模型搜索弹窗
│           ├── agents/
│           │   ├── AgentPanel.tsx        # Agent 列表 + 增删改
│           │   ├── AgentFormDialog.tsx   # Emoji/名称/模型/系统提示词表单
│           │   └── AgentTagBadge.tsx     # Agent 标签徽章（to-text/to-image/to-video）
│           ├── chat/
│           │   ├── ChatCanvas.tsx        # 消息区 + dots 背景 + 空状态
│           │   ├── MessageBubble.tsx     # 用户/Assistant/Tool 气泡 + Markdown
│           │   └── InputBar.tsx          # 自增高 textarea + 发送/停止按钮
│           ├── settings/
│           │   ├── SettingsPanel.tsx      # 设置面板主容器
│           │   ├── AgentsTab.tsx         # Agent 管理标签页
│           │   ├── ModelsTab.tsx         # 模型管理标签页
│           │   ├── ChannelsTab.tsx       # Channel 管理标签页
│           │   ├── McpTab.tsx            # MCP 服务器管理标签页
│           │   ├── SkillsTab.tsx         # Skill 管理标签页
│           │   ├── CronTab.tsx           # 定时任务标签页
│           │   ├── ProjectsTab.tsx       # 项目管理标签页
│           │   ├── CredentialsTab.tsx    # 凭证管理标签页
│           │   ├── AppearanceTab.tsx     # 外观设置标签页
│           │   ├── shared-ui.tsx         # 共享 UI 组件
│           │   └── types.ts             # 设置相关类型
│           └── layout/
│               ├── AppShell.tsx          # 侧边栏 + 主内容区
│               └── Sidebar.tsx           # Nav / Agent 列表 / Session 历史
├── go.mod
├── Makefile
├── Taskfile.yml
└── AGENTS.md
```

---

## 5. 核心抽象（接口）

可扩展性的关键：所有能力都收敛为少量稳定接口，主流程只依赖接口。新增模型供应商、工具、渠道时只实现接口并注册，无需改 Loop。

### 5.1 Provider — LLM 抽象

```go
package provider

type Message struct {
    Role      string         `json:"role"`    // system|user|assistant|tool
    Content   string         `json:"content"`
    ToolCalls []ToolCall     `json:"tool_calls,omitempty"`
    Images    []string       `json:"images,omitempty"` // base64, 多模态（VLM）
    Name      string         `json:"name,omitempty"`   // tool 消息对应的工具名
}

type ChatRequest struct {
    Model    string         // GGUF 模型在本地目录中的标识（HF repo 路径或文件名）
    Messages []Message
    Tools    []ToolSchema   // 暴露给模型的工具 JSON Schema
    Stream   bool
    Options  map[string]any // temperature, num_ctx, top_p 等
}

// Chunk 流式增量；非流式时整体返回一个 Chunk。
type Chunk struct {
    Content   string
    ToolCalls []ToolCall   // 仅在 Done=true 时填充（解析自完整文本）
    Done      bool
    Usage     *Usage
}

// Provider 屏蔽不同后端差异；一阶段默认实现 yzma（本地），
// 接口预留 OpenAI 兼容等扩展（远程模型）。
type Provider interface {
    Name() string
    Chat(ctx context.Context, req ChatRequest) (<-chan Chunk, error)
    // Embed 一阶段不在本地 yzma 实现（memory 模块暂不依赖向量检索）；
    // 远程 OpenAI 兼容 Provider 可实现该方法。
    Embed(ctx context.Context, model, text string) ([]float32, error)
    ListModels(ctx context.Context) ([]ModelInfo, error)
}
```

### 5.2 Tool — 工具抽象（内置 / MCP / Skill 统一）

```go
package tool

type Result struct {
    Content  string         // 给模型看的文本
    Media    []Artifact     // 产物（图片/文件）
    IsError  bool
    Meta     map[string]any
}

type Tool interface {
    Name() string
    Description() string
    Schema() ToolSchema           // JSON Schema 入参
    Invoke(ctx context.Context, args map[string]any, scope Scope) (*Result, error)
}

// Scope 注入运行时上下文：当前工作区、agent、session。
type Scope struct {
    WorkspaceDir string
    AgentID      string
    SessionID    string
}
```

泛型 Registry，统一管理三类工具来源（内置、MCP、Skill）：

```go
type Registry struct { /* mu + map[string]Tool */ }
func (r *Registry) Register(t Tool) error
func (r *Registry) Get(name string) (Tool, bool)
func (r *Registry) Schemas(allow []string) []ToolSchema  // 按 agent 白名单过滤
```

> MCP 工具、Skill 都被适配（adapter）成 `Tool` 注册进同一个 Registry，Loop 无需区分来源。

### 5.3 Channel — 对话渠道抽象

```go
package channel

type Inbound struct {
    ChannelID string       // 实例 ID
    Type      string       // wechat|qq
    From      Identity     // 发信人（用户/群）
    Text      string
    Media     []Artifact
    Ts        time.Time
}

type Outbound struct {
    To    Identity
    Text  string
    Media []Artifact
}

type Channel interface {
    Type() string
    Start(ctx context.Context, in chan<- Inbound) error // 把入站消息投递到 bus
    Send(ctx context.Context, out Outbound) error
    Stop() error
}
```

### 5.4 泛型 Event Bus

```go
package bus

type Event[T any] struct {
    Topic string
    Data  T
    Ts    time.Time
}

type Bus interface {
    Publish(topic string, data any)
    Subscribe(topics ...string) (<-chan Envelope, func()) // 返回取消订阅函数
}
```

主题约定（topic 命名空间）：
`agent.run.*`、`agent.stage.*`、`tool.*`、`channel.inbound`、`channel.outbound`、`cron.fired`、`runtime.*`（含 `runtime.lib.progress`）、`models.pull.progress`。

---

## 6. Agent Loop — 8 阶段

一次用户消息触发一轮 Loop。Loop 是一个有状态的管线，8 个阶段顺序执行；`think→act→observe` 可循环多轮直到模型不再请求工具或达到最大轮次。

```mermaid
flowchart LR
    C[context] --> H[history] --> P[prompt] --> T[think]
    T -->|有 tool_calls| A[act] --> O[observe] --> T
    T -->|无 tool_calls| M[memory] --> S[summarize]
```

| 阶段 | 职责 | 关键输入/输出 |
|------|------|---------------|
| **context** | 装配运行上下文：解析本轮使用的 Agent、Workspace（可对话时临时指定）、可用工具白名单、模型参数 | 输出 `RunContext` |
| **history** | 从 SQLite 拉取会话历史，做 token 预算裁剪（保留 system + 最近 N 轮 + 摘要） | 输出 `[]Message` |
| **prompt** | 组装系统提示：Agent 人设 + 工作区信息 + 工具说明（JSON Schema 注入） + 长期记忆注入 | 输出最终 `ChatRequest` |
| **think** | 调用 Provider 流式推理，逐 token 经 bus 推给前端；生成结束后解析 `tool_calls` | 输出文本增量 + 工具调用列表 |
| **act** | 执行工具调用（多调用可并行），统一通过 Tool Registry，注入 Scope | 输出各 `tool.Result` |
| **observe** | 把工具结果回填为 `tool` 角色消息，判断是否需要再次 think（是则回到 think） | 控制循环 |
| **memory** | 落库本轮完整消息；按规则抽取长期记忆（偏好/事实）写入 memory store | 持久化 |
| **summarize** | 当历史超阈值时生成滚动摘要，压缩 token；更新 session 摘要 | 持久化摘要 |

设计要点：
- 每个阶段实现统一接口 `Stage interface{ Run(ctx, *RunState) error }`，Loop 持有 `[]Stage`，便于插桩、替换、测试。
- 每进入/离开一个阶段发 `agent.stage.{name}.{start|end}` 事件。
- `act` 阶段使用 `errgroup` 并行执行多个工具，结果按原始下标排序回填（参考旧实现的 `indexedResult`）。
- 取消：`context.Context` 贯穿全程，`chat.abort` 触发 cancel；本地推理循环每生成一个 token 检查一次 `ctx.Err()`。

```go
type RunState struct {
    Ctx       RunContext
    Messages  []provider.Message
    ToolCalls []provider.ToolCall
    Output    strings.Builder
    Round     int
    MaxRounds int
}
```

---

## 7 ModelManager（`internal/modelmgr`）

不再托管任何外部进程，只做**本地 GGUF 文件管理**：

```go
type Manager interface {
    Dir() string                                  // ~/.nurvis/models
    List(ctx context.Context) ([]ModelInfo, error) // 扫目录 + 读 GGUF 头
    Resolve(model string) (path string, err error) // 把逻辑名解析为本地绝对路径
    Pull(ctx context.Context, ref ModelRef) (<-chan PullProgress, error) // 从 HF 下载
    Delete(ctx context.Context, model string) error
    ListLibrary(ctx context.Context) ([]LibraryModel, error) // 推荐清单
}

type ModelRef struct {
    Repo string // e.g. "ggml-org/gemma-3-4b-it-GGUF"
    File string // e.g. "gemma-3-4b-it-Q4_K_M.gguf"
}
```

下载实现要点：
- URL 模板：`https://huggingface.co/{Repo}/resolve/main/{File}`；
- 复用旧 `download.go` 的分块流式 + Range 续传 + 重试；进度通过 `models.pull.progress` 事件投递。
- 文件落地：`~/.nurvis/models/{Repo}/{File}`（Repo 中 `/` 保留为子目录）。
- 本地存在时 `Pull` 直接走 “already present” 短路。
- 推荐库（`ListLibrary`）：一阶段使用内置精选 JSON（gemma3 / qwen2.5 / llama3.2 / phi-3.5 等几十条），后续可改为查 HuggingFace search API。

### 7.1 硬件探测与模型推荐

```go
type HardwareInfo struct {
    TotalRAMBytes uint64
    GPUs          []GPU   // 厂商、显存
    Platform      string  // darwin/linux/windows, arm64/amd64
}

func Probe() (HardwareInfo, error)
func Recommend(hw HardwareInfo) []ModelRecommend // 按显存/内存给出可运行的 GGUF 模型档位
```

- macOS（Apple Silicon）：统一内存，按总内存推荐；Linux/Win 优先看独立 GPU 显存。
- **默认模型 `gemma-3-4b-it-Q4_K_M.gguf`**（HF repo：`ggml-org/gemma-3-4b-it-GGUF`）。推荐档位示例：
  - < 8GB → `gemma-3-1b-it-Q4_K_M`（`ggml-org/gemma-3-1b-it-GGUF`）/ `Qwen2.5-1.5B-Instruct-Q4_K_M`
  - 8–16GB → `gemma-3-4b-it-Q4_K_M`（默认）
  - 16–32GB → `gemma-3-12b-it-Q4_K_M` / `Qwen2.5-7B-Instruct-Q4_K_M`
  - > 32GB → `gemma-3-27b-it-Q4_K_M`
- 首次启动流程：
  1. `llamax.Runtime.EnsureReady`：自动下载 `llama.cpp` 动态库到 `~/.nurvis/lib`；
  2. `hardware.Probe` + `Recommend`；
  3. 用户在引导页选定模型 → `modelmgr.Pull` → 完成。

---

## 8. Workspace / Project 管理

- **Project** = 一个本地目录（工作区）+ 元信息。不同项目隔离不同的工作目录。
- Agent 绑定一个**默认工作区**；对话时可在 `chat.send` 参数里临时**指定工作区**覆盖默认值。
- 工具执行通过 `Scope.WorkspaceDir` 拿到当前工作目录，文件类工具在此根目录下做路径约束（防越界）。

```go
type WorkspaceManager interface {
    Create(name, dir string) (*Project, error)
    List() ([]Project, error)
    Resolve(projectID string) (*Project, error) // 校验目录存在/可写
}
```

---

## 9. MCP / Skill / 内置工具

三者统一适配为 `tool.Tool` 注册进 Registry：

- **内置工具**：`read_file/write_file/list_files`、`exec`（在工作区内执行命令）、`web_fetch`、`image.gen`（对接本地出图模型）等。每个工具可全局启用/禁用。
- **MCP**：MCP Manager 连接 stdio/SSE/HTTP server，拉取其 tool 列表，逐个包成 `Tool`（名字加 `mcp_<server>_` 前缀防冲突），运行时把入参透传、结果回收。支持按 Agent 授权（grant）。
- **Skill**：Skill 是「指令 + 脚本 + 资源」的目录包。Skill Manager 加载 manifest，将其暴露为一个可调用 `Tool`（或注入到 prompt 的能力清单）。支持上传、启用、按 Agent 授权。

> Agent 通过 `allowed_tools` 白名单决定可见哪些工具，Registry 的 `Schemas(allow)` 据此过滤。

---

## 10. Channels（微信 / QQ 优先）

- 每个 Channel 实例独立配置，`Start` 后把入站消息投递到 bus 的 `channel.inbound`。
- 一个**入站调度器**消费 `channel.inbound`：按「渠道+发信人」映射到目标 Agent + Session，触发 Agent Loop；Loop 产出经 `channel.outbound` 回投给对应 Channel 的 `Send`。
- **微信**：优先采用可控的协议网关（如 Gewechat / 个人号网关）适配为 `Channel`；接口隔离，后续可替换实现。
- **QQ**：基于 OneBot 11 / NapCat 之类的协议端，适配为 `Channel`。
- 入站做去重 + 防抖（参考旧实现 `consumer/dedup`、`debounce`），避免重复触发。

```mermaid
sequenceDiagram
    participant U as 微信用户
    participant CH as WeChat Channel
    participant BUS as Event Bus
    participant D as Inbound Dispatcher
    participant L as Agent Loop
    U->>CH: 发消息
    CH->>BUS: publish channel.inbound
    BUS->>D: deliver
    D->>L: 路由到 Agent+Session, 启动 Loop
    L-->>BUS: publish channel.outbound
    BUS->>CH: deliver
    CH->>U: 回复
```

---

## 11. Scheduler — 定时任务

- 基于 `robfig/cron/v3`，任务持久化在 SQLite，进程启动时重建。
- 每个 cron 任务绑定：目标 Agent、Session（或新建）、初始 prompt、可选工作区。
- 触发时发 `cron.fired`，由调度器构造一次 Agent Loop（等价于一条系统发起的用户消息）。
- 支持 list / create / delete / toggle / run（立即执行）/ runs（历史）。

---

## 12. Gateway — WebSocket JSON-RPC

单一入口，桌面端 / API / 内部都走它。帧分三类：`req` / `res` / `event`。

### 帧格式
```jsonc
// 请求
{ "type":"req", "id":"uuid", "method":"chat.send", "params":{...} }
// 响应
{ "type":"res", "id":"uuid", "ok":true, "payload":{...} }
// 错误
{ "type":"res", "id":"uuid", "ok":false, "error":{"code":"...","message":"..."} }
// 事件推送（订阅后下发）
{ "type":"event", "event":"agent.chunk", "payload":{"content":"..."} }
```

### 方法清单

| 分组 | 方法 |
|------|------|
| 握手 | `connect`, `health`, `status` |
| 对话 | `chat.send`, `chat.history`, `chat.abort` |
| 会话 | `sessions.list`, `sessions.delete`, `sessions.label` |
| 项目 | `projects.list`, `projects.create`, `projects.update`, `projects.delete` |
| Agent | `agents.list`, `agents.create`, `agents.update`, `agents.delete` |
| Provider/模型 | `providers.list`, `models.list`, `models.library`, `models.pull`, `models.delete`, `models.recommend` |
| 工具 | `tools.list`, `tools.builtin.toggle` |
| MCP | `mcp.list`, `mcp.add`, `mcp.update`, `mcp.delete`, `mcp.grant` |
| Skill | `skills.list`, `skills.upload`, `skills.toggle`, `skills.grant` |
| Channel | `channels.list`, `channels.create`, `channels.update`, `channels.delete`, `channels.status` |
| Cron | `cron.list`, `cron.create`, `cron.delete`, `cron.toggle`, `cron.run`, `cron.runs` |
| 硬件 / 运行时 | `hardware.probe`, `runtime.status`, `runtime.ensure` |

### 推送事件

`agent.run.started` / `agent.run.completed` / `agent.chunk` / `agent.stage` / `tool.call` / `tool.result` / `runtime.lib.progress` / `models.pull.progress` / `channel.status` / `cron.fired`。

---

## 13. 前端

### 13.1 技术栈

- **React 19 + Vite + TypeScript + Tailwind CSS v4**，零 CGO，纯 Web 技术。
- 状态管理：**Zustand**（`ui-store` / `chat-store`）。
- 通信：复用 Gateway WebSocket JSON-RPC（`frontend/src/lib/ws.ts`）。
- 表单：`react-hook-form + zod`；Markdown 渲染：`react-markdown + remark-gfm`。

### 13.2 三个核心流程

**初始化引导（首次启动）**
1. App 启动 → WS 连接 Gateway → 读 `onboarded` 状态
2. 未完成 → `OnboardingWizard`：Step1 调 `runtime.status` + `models.recommend` 展示硬件 / llama.cpp 库下载进度 / 推荐模型，用户选择后调 `runtime.ensure`（拉库）+ `models.pull`（拉模型）；Step2 选预设角色，调 `agents.create`
3. 完成 → 写 `onboarded=true`，进入主界面

**对话主页**
- 侧边栏显示 Agent 列表 + 当前 Agent 的 Session 历史
- 选中 Agent → 自动加载最近 Session；新建对话调 `sessions.create`
- `chat.send` 发送消息，WS 事件 `agent.run.started/chunk/tool.call/run.completed` 流式渲染
- 光标闪烁动画表示流式输出；ActivityDot 显示当前阶段（thinking/acting/…）

**Agent 管理**
- 切换到「助手」视图，展示所有 Agent 卡片
- 支持新建（含 Emoji 选择、预设角色）、编辑、删除
- 删除前确认，删除当前选中 Agent 时清空 activeAgentId


