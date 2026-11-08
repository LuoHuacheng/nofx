# 审查发现修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 2026-11-07 审查中的 10 个问题，保证用户隔离、托管执行接线、监督与时限有效。

**Architecture:** 保留现有风险 Store、controller 和 runtime，修复它们的调用边界。组合 ID 对外保持不变，内部锁和熔断键按 user/environment/portfolio 隔离，预算查询限制相同域；所有自动退出继续复用 controller 的持久化减仓流程。

**Tech Stack:** Go、现有 GORM、SQLite 测试与本地 HTTP fixtures；不新增依赖。

**Spec:** ../specs/2026-11-06-unattended-futures-design.md；本轮审查 /tmp/nofx-code-review-20261107.md。

## Global Constraints

- 用户已授权修复全部发现；直接在当前 dev-plus 工作区实施，保留既有 .gitignore、kernel/engine.go、本地池及设计文件的无关改动，不提交或部署。
- 不读取 .env/凭据，不连接真实交易所，不启用任何账户。测试使用临时库与本地 fixture。
- 默认 disabled；人工审批保持原语义。自动固定限价 30s 撤余单、持仓 24h 减仓、信号 30s，未来超过 1s 拒绝。
- UNKNOWN 不盲目重发，不释放风险；暂停与熔断不因重启清除。金额保留 Decimal/big.Rat。

## Review Focus

- 政策 ID 伪造、相同组合名的不同用户与环境不能互相覆盖、混算预算、熔断或复位。
- 减仓仓位读取短暂失败后，监督继续、余单取消、未知回执仍不重发。
- 状态查询数据库错误必须向调用方返回失败，缺失可选记录可正常显示未知。
- 自动服务在 HTTP 接受请求前启动恢复，使用绑定身份/环境；人工未绑定交易仍走原工厂。
- 过期、零时间、未来信号及跨重启的入场/持仓期限必须按持久化时间处理。

### Task 1: 权限与监督

**Files:** trading/risk/{store,breaker,reservations}.go 及回归测试；planning/live/{controller,exit}.go 及测试；trading/automation/runtime.go 及状态测试。
**Interfaces:** 保留 PutPolicy、ReserveTx、Evaluate、Reset、AccountStatus 公共签名；内部组合 key 由已认证 Policy 派生。
- [x] 将 /tmp 的政策覆盖、组合熔断及强平监督探针变成永久测试，补同名环境隔离、预算与状态 DB 错误测试；先运行观察失败。
- [x] 政策 ID 拒绝与服务端 owner/account 派生值不一致，更新同时约束 owner/account；组合锁、汇总、熔断和复位统一域。
- [x] 强平瞬态错误不终止监督，先取消余单再读取仓位；状态 API 返回实际 DB 错误。
- [x] `GOPROXY=off GOSUMDB=off go test ./trading/risk ./planning/live ./trading/automation -count=1` 应全部通过。

### Task 2: 自动运行和期限

**Files:** api/{server,handler_managed_runtime,handler_planning}.go、main.go、trading/automation/{source,runtime}.go、planning/live/controller.go 及相关测试。
**Interfaces:** StartManagedTrading(ctx) 接入正常生命周期；绑定账户 broker 复用 managedTrader/NOFXBroker；NewModelSource 保留签名。
- [x] 加入空时钟、过期/未来信号、30s 入场期限、24h 减仓、重启期限与启动环境工厂测试；原始探针先失败后修复。
- [x] 构造器补默认时钟；信号在编译/授权前校验原始时间，并避免计划延长信号有效期。
- [x] 仅政策授权用最多 30s 入场且不超过原始信号到期时间；24h 由持久化 StartedAt 在 controller 监督处理，恢复后仍生效。
- [x] 启动初始化风险/runtime 并恢复，绑定账户 broker 使用托管环境；关闭按 runtime 再 controller 顺序。
- [x] `GOPROXY=off GOSUMDB=off go test ./planning/live ./trading/automation ./api -count=1` 中只运行 API 安全 fixture 范围，所有相关用例通过。

### Task 3: 明确选币来源切换

**Files:** kernel/engine.go、store/strategy.go 与针对性测试。
**Interfaces:** CoinSourceConfig 新增默认 false 的 use_local_pool_fallback；不改变已有 AI500 配置含义。
- [x] 加测试：未选择 fallback 时上游失败直接报错，显式选择后才使用本地池；先失败后实现。
- [x] 保留用户本地池实现和正常 AI500 成功路径，暴露配置说明；来源改为 local_pool，mixed 聚合保留真实来源。
- [x] 运行相关 kernel/store 与 localpool 测试并验证。

### Task 4: 最终验证

- [x] gofmt 修改的 Go 文件，运行相关离线包测试、必要 API/kernel/store fixture、go vet ./... 及 git diff --check。
- [x] 逐项复核 10 个发现、人工/自动调用路径、已有工作区 diff；记录未进行的实盘/sandbox验收，不声称离线等于交易所验收。

## 执行记录

- 初始 HEAD: 507f70e53f0768f7dbd4401550230cfaeb4f3663。采用当前会话直接修复，用户指令已授权，无需新一轮方案确认。
- 依赖：Task 2 使用 Task 1 的风险域与持续监督；Task 3 独立；Task 4 依赖全部修复。所有改动保留未提交以便审阅。
- 政策授权新增持久化 SignalExpiresAt，并绑定 approval digest；历史手工审批摘要不变。旧政策授权没有原始到期时间时，不允许再开新单，启动恢复会取消未使用的旧政策审批。
- 修复 ResolveBinding 未保留 record-not-found 原因的问题，确保未绑定的历史人工账户仍走原工厂；现有账户轮换测试通过。
- 本地池开关通过策略 API 的 coin_source.use_local_pool_fallback 显式设置；省略或 false 均关闭，true 时仅 AI500 失败才回退。后端配置与前端类型已同步，未新增 UI 控件。
- 最终离线统计：核心 7 个包 422 个测试条目通过、1 项 sandbox 验收跳过；API fixture 17、kernel 75、store 配置 13，共 527 个测试条目通过。条目包括子测试。
- go vet ./...、前端 tsc --noEmit、修改类型文件的 eslint/Prettier 检查通过。真实交易所、sandbox 在线验收和部署未执行。
- 对风险预留并发、组合隔离、原始复现探针及启动恢复运行 go test -race，risk/automation/live 三个包全部通过；最终 git diff --check 与全部修改 Go 文件的 gofmt 检查通过。
