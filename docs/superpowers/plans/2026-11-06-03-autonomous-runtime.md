# NOFX Autonomous Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 通过服务端政策授权自动生成和执行候选，重启主动对账，持续监督保护并在故障时自动暂停、退出和告警。

**Architecture:** 新增 `trading/automation`，负责信号和生命周期；仍只经 live 执行订单。政策授权与人工审批分离但共用同一执行状态机、风险门和 Lease。API/main 只负责组装，不把业务循环放进 handler。

**Tech Stack:** Go、现有 planning/model.ChatPlanner 与 market.Client、GORM、现有 WebSocket 库、net/http webhook；无新增依赖。

**Spec:** `../specs/2026-11-06-unattended-futures-design.md`；顺序前置计划 01 和 02。

## Global Constraints

- 自动政策默认 disabled；sandbox/live 独立；owner、绑定、政策版本、计划 hash、signal ID 全部参与授权。
- 首版仅 limit 入场，30s 撤未成交余量；MaxHold=24h；旧 Autopilot/Grid 不得操作托管账户。
- 信号最多30s、快照最多5s；每60s取信号，源连续失败3次只暂停新信号，不停止保护。
- 主动启动且先对账后信号；不重放旧入场，不因重启清除人工 pause 或熔断。
- REST每10s补偿；WS只作增量/唤醒，不能作为完整资金基准的唯一来源。
- 全部执行仍需阶段2事务预算；不得用伪造 APPROVE LIVE 绕过人工审批。

## Review Focus

- 模型输出含自定义数量、账户、政策或工具调用：Task 2 严格 JSON 拒绝未知字段，不能执行。
- 已授权后政策修改/撤销：Task 1 在 Execute 和每次 entry 前重新校验版本。
- 从未访问 planning 页面就重启：Task 3 必须主动恢复保护，无 HTTP 请求依赖。
- WS断线且REST账单分页漏页：Task 4 保持不新鲜，不提交游标和新入场。
- 告警端点不可用或携带恶意 URL：Task 5 不阻塞保护，限制目标和重试，敏感信息不入 outbox。

---

## 文件与接口地图

新增 `trading/automation/{source,runtime,recovery,events,alerts}.go`，分别负责模型输入边界、调度、重启核对、事件补偿和告警。扩展 `planning/live/authorization.go` 给服务端自动政策授权。新增 `api/handler_automation.go`，只开放已认证的管理接口；修改 main/server 的启动与关闭次序。首版不修改前端。

### Task 1: 服务端自动政策授权与撤销

**Files:**
- Create: `planning/live/authorization.go`, `planning/live/authorization_test.go`
- Modify: `planning/live/controller.go`, `planning/live/types.go`, `planning/live/risk_gate.go`

**Interfaces:**
- Consumes: 阶段2 `risk.Store.Policy`、阶段1 Binding、现有 `planning.Draft` 和 `Controller.Execute`。
- Produces: `(*Controller).AuthorizePolicy(ctx context.Context,user,planID,planHash,policyVersion,signalID string) (View,error)`。
- Run 新增 `AuthorizationMode string`（历史空值视为 manual）、`SignalID string`；DB 唯一 `(AccountKey,SignalID)` 仅作用于 policy 类型的独立授权记录，避免 manual 的空 signal 相互冲突。
- Produces: `policyDigest(p core.Plan,user,exchange,account,policyVersion,signalID string) string`；old manual digest 不变，decode 根据服务端持久化 mode 验证。

- [ ] **Step 1: 写失败测试。** `TestPolicyAuthorizationBinding` 逐一修改 owner/account/env/planHash/policyVersion/signalID，均拒绝。`TestPolicyAuthorizationIdempotent` 同信号同计划返回同 Run，异计划同信号拒绝。`TestPolicyAuthorizationRevokedBeforeSubmit` 授权后 disable 或换版本，Place=0且不占新预算。`TestPolicyAuthorizationManualCompatibility` 原 Approve 仍需真实确认短语，历史 digest 原样验证。
  ```go
  if first.ID != repeated.ID { t.Fatal("same signal created multiple runs") }
  if revokedPOSTs != 0 { t.Fatal("revoked policy submitted entry") }
  ```
- [ ] **Step 2: 运行确认失败。** `go test ./planning/live -run '^TestPolicyAuthorization' -count=1`。
- [ ] **Step 3: 实现授权。** 从服务端读 Binding/Policy、完整校验 Plan 的 limits 和政策，不接受调用方自带 Policy 对象；同事务写授权记录与 Lease。只有 policy mode 才允许无逐笔短语。Execute 及每个 entry 发出前检查当前 policy version/Enabled/闩锁，撤销只取消入场，已有保护继续监督。失效的审批不能重新赋值 hash 后复活。
- [ ] **Step 4: 运行确认 PASS。** 同 Step 2；`go test ./planning/live ./trading/risk -count=1`。
- [ ] **Step 5: 提交。** 仅暂存本任务文件；`git commit -m "feat(automation): 增加绑定政策版本的自动交易授权"`。

### Task 2: 无交易权限的模型信号适配器

**Files:**
- Create: `trading/automation/source.go`, `trading/automation/source_test.go`
- Modify: `planning/drafts.go`（仅在需要复用现有 Add 时补充事务/owner 验证，不改变保存格式）

**Interfaces:**
- Produces: `Signal{ID string; Idea core.Idea; CreatedAt,ExpiresAt time.Time}`；`IdeaSource interface { Next(context.Context,risk.Policy) (Signal,error) }`；`ErrNoSignal`。
- Produces: `NewModelSource(planner model.ChatPlanner,feed SourceMarket,now func()time.Time) *ModelSource`；`SourceMarket interface { Snapshot(context.Context,string,string)(core.Snapshot,error); Candles(context.Context,market.CandleQuery)([]market.Candle,error) }`。
- Consumes: `Chat(context.Context,[]model.Message,[]model.Tool)(model.Message,error)`；每轮只选择一个白名单 symbol，15m已收盘K线120根；tools 为空。ID 为 account/policyVersion/symbol/最后已收盘bar时间/Idea规范化JSON 的 SHA-256。

- [ ] **Step 1: 写失败测试。** `TestModelSourceStrictBoundary` 响应只接受 `{"ideas":[...]}`，最多1个；未知字段 quantity/leverage/account/constraints、ToolCalls 非空、无效 JSON、过期行情均拒绝。`TestModelSourceNoTrade` ideas=[]返回 ErrNoSignal，不捏造交易。`TestModelSourceStableID` 同一bar/Idea多次返回同ID；数量与政策始终不来自模型。`TestModelSourceClosedBarsOnly` 当前未收盘bar不进入模型输入，缺120根时拒绝而非补造数据。
  ```go
  if !errors.Is(noIdeasErr, ErrNoSignal) { t.Fatal("empty ideas produced a trade") }
  if injectedQuantityErr == nil { t.Fatal("model changed execution authority") }
  ```
- [ ] **Step 2: 运行确认失败。** `go test ./trading/automation -run '^TestModelSource' -count=1`。
- [ ] **Step 3: 实现严格解析及输入。** `json.Decoder.DisallowUnknownFields` 且检查尾随 JSON；Idea 仅含既有字段，Execution 只能 limit。信号生成时间/过期时间由服务器赋值（30s）；prompt不含凭据或交易工具。按政策顺序轮询 BTC/ETH，每次最多候选1个。不更改现有人工 harness 的系统提示与工具权限，不复用旧策略输出数量。
- [ ] **Step 4: 运行确认 PASS。** 同 Step 2；使用假 ChatPlanner/固定 candles，无外部 LLM 调用；`go test ./planning/model ./planning/core -count=1`。
- [ ] **Step 5: 提交。** 仅暂存本任务文件；`git commit -m "feat(automation): 增加受限模型信号与严格输入校验"`。

### Task 3: 主动启动、账户核对与持续调度

**Files:**
- Create: `trading/automation/runtime.go`, `trading/automation/recovery.go`, `trading/automation/runtime_test.go`, `trading/automation/recovery_test.go`, `trader/execution/process_lock.go`, `trader/execution/process_lock_test.go`
- Modify: `main.go`, `api/server.go`, `api/handler_planning.go`, `planning/live/controller.go`

**Interfaces:**
- Consumes: Task1 `Controller.AuthorizePolicy`、Task2 `IdeaSource.Next`、阶段2 `risk.NewStore/Policy/Evaluate/Observe`、阶段1 `Server.managedTrader`。
- Produces: `automation.Factory func(context.Context,string,string)(execution.ManagedTrader,execution.Binding,error)`；`NewRuntime(db *gorm.DB,controller *live.Controller,risks *risk.Store,factory Factory,source IdeaSource,now func()time.Time) (*Runtime,error)`。
- Produces: `(*Runtime).Start(ctx context.Context) error`、`Shutdown(ctx context.Context) error`、`Recover(ctx context.Context,user,accountKey string) error`、`Tick(ctx context.Context,user,accountKey string) error`。
- Produces: `execution.AcquireProcessLock(ctx context.Context,db *gorm.DB,dataDir string) (func()error,error)`；SQLite 用 Linux/macOS advisory file lock（非PID文件），Postgres 以专用连接持有数据库 advisory lock，连接丢失必须冻结新入场。
- API 新增 `(*Server).StartManagedTrading(ctx context.Context) error`，保持现有 NewServer 签名。

- [ ] **Step 1: 写失败测试。** `TestRuntimeStartsWithoutHTTP` 构造 Server 后直接 StartManagedTrading，已有未保护持仓被恢复监督，未调用路由也成立。`TestRecoveryNoEntryReplay` 各提交断点崩溃重启后原 entry POST总数≤1，过期审批仅撤余单。`TestRecoveryExternalPosition` 账实不符不得接管新开仓，冻结并告警；已核实本运行仓位才可补保护/退出。`TestRuntimePauseSurvivesRestart` 人工pause/风险闩锁仍在。`TestProcessLockRejectsSecondOwner` 第二实例失败，释放后可启动。`TestRuntimePolicyPipeline` 假源→limits→Compile→Draft→Authorize→Execute，授权前 Place=0。
  ```go
  if restartedEntryPOSTs > 1 { t.Fatal("recovery replayed an entry") }
  if secondOwnerErr == nil { t.Fatal("two execution owners acquired the same database") }
  ```
- [ ] **Step 2: 运行确认失败。** `go test ./trading/automation ./trader/execution ./api -run 'TestRuntime|TestRecovery|TestProcessLock' -count=1`；API只选新增本地mock测试。
- [ ] **Step 3: 实现生命周期。** 进程锁必须在旧 manager Load/AutoStart 外部写入之前；managed services 在 main 显式启动，单个 controller 由 API 与 runtime 共享，避免二次 New 重置活跃run。Recover 持账户执行权、验证模式/账户/资金历史/保护；不重放 entry。每60s取信号，每10s对账；每轮先风险/保护再信号。每账户串行，两个账户之间可并发但风险事务统一。持仓24h或入场30s期限由持久化时间决定。停机取消信号并等待监督任务安全结束，不撤交易所保护。
- [ ] **Step 4: 运行确认 PASS。** 同 Step 2；`go test -race ./trading/automation ./planning/live -run 'TestRuntime|TestRecovery|TestManaged' -count=1`。禁止在测试调用 main，避免其 dotenv/真实配置初始化。
- [ ] **Step 5: 提交。** 仅暂存本任务文件；`git commit -m "feat(automation): 增加主动启动与可恢复交易监督"`。

### Task 4: 双交易所私有事件与REST补偿

**Files:**
- Create: `trader/binance/managed_events.go`, `trader/binance/managed_events_test.go`, `trader/okx/managed_events.go`, `trader/okx/managed_events_test.go`, `trading/automation/events.go`, `trading/automation/events_test.go`
- Modify: `trader/execution/managed.go`, `trader/binance/execution.go`, `trader/okx/execution.go`, `trading/automation/recovery.go`, `trading/automation/runtime.go`

**Interfaces:**
- Consumes: Task1 ManagedTrader.Events 与 AccountEvent；不直接写业务余额。
- Produces: `execution.HistoryReader interface { History(context.Context,time.Time,string)([]AccountEvent,string,bool,error) }`；参数为 fromTime/pageCursor，返回 events/nextCursor/complete；complete只在该查询窗口末页为true。
- Produces: `(*Runtime).Ingest(ctx context.Context,user,accountKey string,events []execution.AccountEvent) error`；GORM `managed_event_cursors` 记录账户/类别的最后完整窗口，ledger去重沿用阶段2。

- [ ] **Step 1: 写失败测试。** `TestManagedEventsReconnect` 本地WS断线重连发 reconcile_required，恢复前不入场；心跳/鉴权过期处理不泄露凭据。`TestManagedEventsDuplicateAndOutOfOrder` fill重复乱序累计量不倒退不双计；资金费同ID只入一次。`TestManagedHistoryMissingPage` 第2页失败不提交窗口游标，重启从重叠60s重拉；REST恢复后与完整fixture一致。`TestManagedEventsPartialFillWakeup` 事件立即唤醒保护，不等待10s周期。
  ```go
  if cursorAfterMissingPage != cursorBefore { t.Fatal("incomplete history advanced cursor") }
  if recoveredFundingEvents != originalFundingEvents { t.Fatal("funding replay double counted") }
  ```
- [ ] **Step 2: 运行确认失败。** `go test ./trader/binance ./trader/okx ./trading/automation -run '^TestManaged(Events|History)' -count=1`。
- [ ] **Step 3: 实现事件与分页。** 用当前官方私有WS鉴权/续期流程，首个fixture注释来源与日期；现有库足够则不新增依赖。事件使用交易所账户/订单/成交原生ID去重，没有统一全局序列时不虚构序列。REST历史拉取包含交易、佣金、资金费、充提/划转；未知影响权益的账单类型标记incomplete。首次live建立当日基准需重建UTC日初，记录分页证据。更改 PrivateEvents=true 只在fixture覆盖完成之后。
- [ ] **Step 4: 运行确认 PASS。** 同 Step 2；`go test ./trading/risk ./trading/automation -count=1`，以完整事件回放与断连补偿回放比较最终equity/quantity/cursor一致。
- [ ] **Step 5: 提交。** 仅暂存本任务文件；`git commit -m "feat(automation): 接入私有事件与持久化对账补偿"`。

### Task 5: 管理API、健康状态与持久化告警

**Files:**
- Create: `api/handler_automation.go`, `api/handler_automation_test.go`, `trading/automation/alerts.go`, `trading/automation/alerts_test.go`
- Modify: `api/server.go`, `trading/automation/runtime.go`
- Create: `docs/operations/unattended-futures.md`

**Interfaces:**
- Consumes: Task3 Runtime、阶段2 `risk.Store.PutPolicy/Pause/Reset` 与阶段1 `Controller.Flatten`、现有 protected Gin 路由组。
- Produces: `(*Server).registerAutomation(protected *gin.RouterGroup)`；owner认证接口 `/api/automation/:accountKey/{policy,start,pause,flatten,reset,status}`，分别 PUT/POST/POST/POST/POST/GET。
- start 请求必须含环境与当前政策 version，调用服务端已验证绑定；reset 调用风险 Store.Reset，不允许用 start 隐式复位。
- Produces: `(*Runtime).StartAccount(ctx context.Context,user,accountKey,version string,env execution.Environment) error`、`PauseAccount(ctx context.Context,user,accountKey,reason string) error`、`FlattenAccount(ctx context.Context,user,accountKey,reason string) error`、`AccountStatus(ctx context.Context,user,accountKey string) (Status,error)`；同一Runtime receiver。`Status{AccountKey,State,ProtectionState,PolicyVersion,Reason string; Environment execution.Environment; LastREST,LastWS time.Time; LedgerComplete bool; ReservedRisk execution.Decimal}`。reset handler直接调用 `risk.Store.Reset`。
- Produces: `Alert{ID,AccountKey,Code,Severity,Message string; CreatedAt time.Time}`；`(*Runtime).QueueAlert(ctx context.Context,a Alert) error`；`(*Runtime).DeliverAlerts(ctx context.Context) error`。

- [ ] **Step 1: 写失败测试。** `TestAutomationAPIIsolation` wrong owner/未认证全部拒绝，status不含私密字段。`TestAutomationPauseVsFlatten` pause撤未成交入场保留SL，flatten走同一减仓状态机。`TestAutomationPolicyUpdate` 旧version CAS失败、enabled默认false、live与sandbox不互通。`TestAlertOutboxFailure` webhook500/timeout时保护仍执行，重启后重发并60s去重；消息不含secret/signature/JWT。`TestAlertTargetValidation` 非HTTPS、localhost、私网/链路本地目标、DNS重绑定、重定向均拒绝，测试通过注入transport而非开放生产例外。
  ```go
  if wrongOwnerStatus != http.StatusForbidden { t.Fatal("cross-owner management permitted") }
  if protectedWhileWebhookFailed != true { t.Fatal("alert failure blocked protection") }
  ```
- [ ] **Step 2: 运行确认失败。** `go test ./api ./trading/automation -run 'TestAutomation|TestAlert' -count=1`。
- [ ] **Step 3: 实现管理与告警。** handler只调runtime/risk，不能直接下单。status给出last REST/WS、保护覆盖、预留、熔断、账本完整度；健康HTTP200不等于可入场。持久化outbox独立worker，重试1s/5s/30s再每60s，不阻塞保护；成功也不解除熔断。webhook配置只允许管理员初始化的单一目标，dial时验证实际解析IP并禁用重定向，日志脱敏；文档写明暂停、退出、恢复与凭据轮换步骤。
- [ ] **Step 4: 运行确认 PASS。** 同 Step 2；`go vet ./trading/automation ./trading/risk ./planning/live`。新API测试全部走临时库和mock，不调用真实交易所。
- [ ] **Step 5: 提交。** 仅暂存本任务文件；`git commit -m "feat(automation): 增加管理接口与可恢复告警"`。

## 阶段验收与交接

两所通过相同runtime，自动政策默认disabled。先sandbox通过至少一个完整 signal→entry→SL/TP→exit 周期，且模型失败仍继续监督旧仓。未知状态必须自动冻结并可审计，不要求无条件自动恢复。

下一步 `2026-11-06-04-exchange-certification.md`。未完成该阶段，不能把“模型可以自动下单”等同于上线资格。
