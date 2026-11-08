# NOFX Managed Execution Safety Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 Binance/OKX 提供可对账的托管执行、保护和减仓路径，阻止旧交易器绕过它。

**Architecture:** 扩展现有 `execution` 契约与 `planning/live.Controller`，保留人工计划审批和旧记录摘要。新增托管绑定及保护监督，不复制一个执行引擎；此阶段不自动开仓。

**Tech Stack:** Go（沿用 go.mod）、GORM、SQLite/Postgres 既有驱动、math/big、现有 Binance SDK 与 OKX HTTP 客户端；不新增依赖。

**Spec:** `../specs/2026-11-06-unattended-futures-design.md`

## Global Constraints

- 基线 `4d11af37b3273220674fc657e0c619439fb04d77`；保护既有 `.gitignore`、`kernel/engine.go`、`provider/localpool/`。
- 首版 Binance USDⓈ-M / OKX USDT 永续、单向逐仓、专用子账户、单实例、每账户一个活动计划。
- 新自动交易默认禁用；不读取凭据、不执行生产迁移、不下真实订单、不部署。
- 金额和数量使用 `execution.Decimal`；base quantity 对外一致，OKX contracts 仅在适配器内部转换。
- 意图先持久化；UNKNOWN 不换 ID 重试；数据库失败禁止新增外部写入；减仓不得反向开仓。
- HTTP 2s，单轮只读重试总截止 4s；保护确认期限 5s；已有人工模式不强制套用新增政策。
- 无新依赖；传递 context、error 用 `%w`；每任务 gofmt、定向测试、单独提交。

## Review Focus

- OKX `ctVal`/lot step 与 Binance base qty 混淆：Task 1 用两个非 1 面值和最小量边界验证。
- 两个 API Key 属于同一物理账户：Task 2 验证共享绑定和唯一性，不再用 key hash 仲裁托管账户。
- SL 请求实际受理但响应丢失：Task 3 查询原 ID，禁止重复 SL，也不得提前释放风险。
- 移动 SL 期间旧 SL 已触发、人工已平仓：Task 4 必须最终 flat，不反向、不撤未确认的保护。
- 服务重启或直接调用旧自动器绕过 HTTP 校验：Task 5 验证 manager、构造与 Run 边界均拒绝。

---

## 文件与接口地图

新增 `trader/execution/managed.go` 定义 spec 中 ManagedTrader/ManagedAccount/AccountEvent/Capabilities/Environment；新增 `trader/execution/binding.go` 管理物理账户绑定；新增 `planning/live/protection.go` 和 `exit.go` 各自负责保护与安全减仓。只从 controller 提取相关分支，不重排其他算法。

修改 `trader/binance/execution.go`、`trader/okx/execution.go` 及只读账户客户端的环境配置；`api/handler_planning.go` 负责工厂；`api/account_close.go` 复用托管减仓；`manager/trader_manager.go` 与 `trader/auto_trader.go` 阻止旁路。公共契约变更必须同时检查 Bitget/Hyperliquid mock，不增加现有 Trader 必需方法。

### Task 1: 两所环境隔离、账户读模型与模式校验

**Files:**
- Create: `trader/execution/managed.go`, `trader/execution/managed_test.go`
- Modify: `trader/binance/execution.go`, `trader/binance/futures.go`, `trader/okx/execution.go`, `trader/okx/frontier_account.go`, `trader/okx/trader.go`
- Test: `trader/binance/managed_test.go`, `trader/okx/managed_test.go`

**Interfaces:**
- Consumes: `execution.Trader`、`execution.Order`、spec 中全部托管类型。
- Produces: `NewManagedAccountTrader(ctx context.Context, key, secret, userID string, env execution.Environment) (*FuturesTrader,error)`（binance）；`NewManagedAccountTrader(ctx context.Context, key, secret, passphrase string, env execution.Environment) (*OKXTrader,error)`（okx）。
- ManagedTrader 还显式包含两所现有 `RouteClientID(string) (string,error)` 与 `PrepareExecutionLeverage(context.Context,string,int) error`，账户包装器必须透传。两所实现 `ManagedSnapshot(context.Context) (execution.ManagedAccount,error)`、`VerifyManagedSettings(context.Context,string,int) error`、`ManagedCapabilities() execution.Capabilities`、`AmendStop(context.Context,execution.Order,execution.Decimal) (execution.Order,error)`、`Events(context.Context,func(execution.AccountEvent)error) error`。
- 本任务 `SafeStopAmend=false/PrivateEvents=false`，相关方法返回 `execution.ErrUnsupported`；后续任务提供实际能力，禁止伪报支持。

- [ ] **Step 1: 写失败测试。** 使用 `httptest.Server` 和无效占位密钥，构造器在任何网络请求前注入测试 HTTP 客户端；定义以下断言：
  ```go
  // 两个包各有 TestManagedEnvironmentAndSettings(t *testing.T)
  // env="" -> error；sandbox 请求绝不发往生产；live 不带模拟标记。
  // net/one_way + isolated + leverage=3 -> nil；hedge/cross/leverage mismatch -> error。
  // OKX ctVal="0.01", sz="10" -> Quantity="0.1"；ctVal="0.1", sz="1" -> "0.1"。
  // qty 非 lot step 整数倍、未知账户模式、缺失 equity/保护查询失败 -> error。
  // 验证函数只发读取，不调用模式切换；Read error 不返回空仓快照。
  if !execution.Equal(string(got.Positions[0].Quantity), "0.1") { t.Fatal("wrong contract conversion") }
  if productionRequests != 0 { t.Fatal("sandbox escaped to production") }
  ```
- [ ] **Step 2: 运行确认失败。** `go test ./trader/binance ./trader/okx -run '^TestManagedEnvironmentAndSettings$' -count=1`；预期新构造器/托管类型未定义。
- [ ] **Step 3: 实现上述契约与构造器。** 依据执行时最新官方文档核对 sandbox host、OKX 模拟标记、模式/保护查询及字段，在测试 fixture 注释记录来源 URL 和核对日期；复用已有精确单位转换。固定生产/模拟端点，测试 transport 注入不可成为生产任意 URL 配置。不改变旧构造器默认行为。
- [ ] **Step 4: 运行定向测试并确认 PASS。** 同 Step 2；增加 `go test ./trader/execution ./planning/live -count=1` 验证原契约兼容。
- [ ] **Step 5: 提交。** `git add trader/execution/managed* trader/binance/execution.go trader/binance/futures.go trader/binance/managed_test.go trader/okx/execution.go trader/okx/frontier_account.go trader/okx/trader.go trader/okx/managed_test.go`，`git commit -m "feat(execution): 增加双交易所托管账户契约与环境隔离"`。

### Task 2: 物理账户绑定与环境匹配

**Files:**
- Create: `trader/execution/binding.go`, `trader/execution/binding_test.go`, `api/handler_managed_accounts.go`, `api/handler_managed_accounts_test.go`
- Modify: `api/handler_planning.go`, `api/server.go`

**Interfaces:**
- Consumes: Task 1 托管构造器；`store.Exchange.Testnet`；保留原 `accountKey(*store.Exchange) string` 供历史非托管记录使用。
- Produces: `execution.Binding`（spec 的 managed_account_bindings 字段，ID/UserID/ExchangeID/Venue/VenueAccountID 为 string、Environment 为 Environment、VerifiedAt 为 time.Time、Enabled 为 bool）；`ResolveBinding(ctx context.Context, db *gorm.DB, user, exchangeID string) (Binding,error)`。
- Produces: `execution.WithBinding(t ManagedTrader,b Binding) ManagedTrader` 包装 snapshot/event 的 AccountKey 为 Binding.ID；`(*Server).managedTrader(ctx context.Context,user,exchangeID string) (execution.ManagedTrader,execution.Binding,error)`，`(*Server).handleManagedAccountBinding(c *gin.Context)`；POST `/api/managed-accounts/bindings`。
- 绑定请求字段 `exchange_id, venue_account_id, environment, confirm_dedicated_account`；只能绑定 owner 所有的账户；无法签名确认 UID 时保留 disabled 且要求初始化核验，不接受空身份。

- [ ] **Step 1: 写失败测试。** `TestManagedBindingIdentity` 断言 `(venue,env,venueAccountID)` 唯一，第二凭据配置必须拒绝；轮换更新原 ExchangeID 并保持 Binding.ID；跨 owner 冲突拒绝、不泄露旧 owner。`TestManagedBindingEnvironment` 断言 Binance Testnet/OKX Demo 可通过托管工厂，配置与 binding 环境不符、未验证身份或非 owner 均拒绝；原人工计划账户选择行为不变；WithBinding 后 broker 的路由与3x准备能力保持一致，不回退为空路由/1x。
  ```go
  if first.ID != rotated.ID { t.Fatal("rotation changed physical account binding") }
  if duplicateErr == nil { t.Fatal("second credential binding accepted") }
  ```
- [ ] **Step 2: 运行确认失败。** `go test ./trader/execution ./api -run '^TestManagedBinding' -count=1`；预期新绑定类型/handler 未定义。
- [ ] **Step 3: 实现表和 handler。** 沿用现有 GORM AutoMigrate 与认证组；初始化仅在测试临时库/开发库进行。绑定记录做唯一索引、环境强校验；live.Factory 在找到托管绑定时返回该 Binding.ID 作为 AccountKey。历史 run 没有 binding 的路径维持旧身份算法，不批量改写旧摘要。未绑定托管账户不能进入无人值守流程。
- [ ] **Step 4: 运行确认 PASS。** 同 Step 2，另运行 `go test ./planning/live -run 'TestApproval|TestUnused|TestRecovery' -count=1` 确认历史审批兼容。
- [ ] **Step 5: 提交。** 仅 git add 本任务文件；`git commit -m "feat(execution): 绑定托管物理账户并校验运行环境"`。

### Task 3: 保护监督与有界故障处置

**Files:**
- Create: `planning/live/protection.go`, `planning/live/protection_test.go`
- Modify: `planning/live/controller.go`, `planning/live/types.go`

**Interfaces:**
- Consumes: `live.Broker.Place/Lookup/Cancel`、`live.Run`、`live.Order`、现有 `routedOrder`/`validReceipt`；不增加 Broker 必需方法。
- Produces: `(*Controller).ensureProtection(ctx context.Context,v *View,b Broker,now time.Time) error`；`Run.ProtectionObservedAt *time.Time`、`Run.ProtectionState string`（pending|protected|exit_required|unknown），可空附加字段不参与原计划 hash。
- 时钟：Controller 新增可选 `Now func() time.Time`，nil 使用 UTC now，所有新期限通过同一时钟；保持 `New(db,f,m)` 原签名。

- [ ] **Step 1: 写失败测试。** `TestManagedProtectionDeadline` 固定 now，成交 q=0.4 后立即持久化 protection clock；T+4.999s 不触发退出，T+5s 无确认 SL 必须 exit_required。`TestManagedProtectionUnknownAck` 模拟已受理但断连接，原 clientID 的查询返回有效 SL 后不得重复 POST。`TestManagedPartialFillProtection` 0.4→0.7 仅补缺少的 0.3，SL 确认前不发下一入场；TP 失败保留 SL。`TestManagedProtectionDBFailure` 确认 save 失败时外部 POST 次数为 0。
  ```go
  if atDeadline.ProtectionState != "exit_required" { t.Fatal("unprotected position not escalated") }
  if slPOSTs != 1 { t.Fatalf("SL POSTs=%d, want 1", slPOSTs) }
  ```
- [ ] **Step 2: 运行确认失败。** `go test ./planning/live -run '^TestManaged(Protection|PartialFill)' -count=1`；预期新状态或监督函数未定义/旧流程未达要求。
- [ ] **Step 3: 提取并实现监督。** 将 step 的保护分支提取到 protection.go；托管运行优先 SL、核实实际仓位与累计退出，UNKNOWN 先查询；限时失败进入 exit_required 并停止剩余入场。普通人工 run 保留已有人工恢复语义。禁止只凭 ACK 将整仓标为 protected；核对实际剩余量、方向与触发价。
- [ ] **Step 4: 运行确认 PASS。** 同 Step 2 及 `go test ./planning/live -count=1`；本任务 exit_required 仍冻结，Task 4 提供自动减仓，未完成 Task 4 不启用自动交易。
- [ ] **Step 5: 提交。** `git add planning/live/protection* planning/live/controller.go planning/live/types.go`；`git commit -m "feat(execution): 增加持仓保护监督与故障期限"`。

### Task 4: 可恢复减仓与安全止损调整

**Files:**
- Create: `planning/live/exit.go`, `planning/live/exit_test.go`
- Modify: `planning/live/controller.go`, `planning/live/protection.go`, `api/account_close.go`, `trader/binance/execution.go`, `trader/okx/execution.go`
- Test: `trader/binance/managed_test.go`, `trader/okx/managed_test.go`, `api/account_close_test.go`

**Interfaces:**
- Consumes: Task 3 exit_required；`execution.ManagedTrader.AmendStop`；现有 `api.accountCloseIntent` 和 Submit/Lookup。
- Produces: `(*Controller).Flatten(ctx context.Context,user,runID,reason string) (View,error)`；`(*Controller).MoveStop(ctx context.Context,user,runID string,price execution.Decimal) (View,error)`。
- Flatten 退出意图 Role=`exit`、Type=`market`、ReduceOnly=true，clientID 绑定 run/退出代次；UNKNOWN 保留该 ID。`halted_unknown` 仍持有 Lease，不作为 terminal。

- [ ] **Step 1: 写失败测试。** `TestManagedExitIsReduceOnly` 交易所先处理 SL 后收到退出请求，最终仓位始终 0 不反向；`TestManagedExitUnknownReceipt` 第一次退出实际成交但响应丢失，重启查询同 ID，POST 总数 1；`TestManagedStopAmendRace` 旧 SL 已触发时不新开保护或退出过量；不支持安全 amend 返回 ErrUnsupported 且旧 SL 未撤。`TestManagedExitDBFailure` 持久化失败不发新退出请求。HTTP adapter 测试同时验证单向减仓字段和 OKX contracts。
  ```go
  if !exit.ReduceOnly || exitPOSTs != 1 { t.Fatal("unsafe or duplicate exit") }
  if !execution.Equal(remainingPosition, "0") { t.Fatal("position did not finish flat") }
  ```
- [ ] **Step 2: 运行确认失败。** `go test ./planning/live ./trader/binance ./trader/okx ./api -run '^TestManaged(Exit|StopAmend)' -count=1`。
- [ ] **Step 3: 实现退出并接入 Task 3。** 查询实际仓位、持久化退出再发送、查询确认 flat、清理本运行的保护。外部保护查不全不得报告完成。Amend 仅在官方接口和回读能保证保留旧保护时启用能力；否则保持 false、拒绝移动而不撤旧单。禁止 SL 放宽或越过当前市场，越价触发 Flatten。人工 account_close 使用同样减仓规则，保留原 API 幂等记录和 owner 校验，不递归调用互锁的公共方法。
- [ ] **Step 4: 运行确认 PASS。** 同 Step 2；`go test ./planning/live ./trader/execution -count=1`。专门检查 get-position 失败不会以 0 替代。
- [ ] **Step 5: 提交。** 仅暂存本任务文件；`git commit -m "fix(execution): 统一安全减仓与止损调整故障处理"`。

### Task 5: 关闭托管账户的旧执行旁路

**Files:**
- Create: `trader/execution/ownership.go`, `trader/execution/ownership_test.go`, `manager/managed_ownership_test.go`
- Modify: `manager/trader_manager.go`, `trader/auto_trader.go`, `api/handler_trader.go`, `api/handler_planning.go`, `api/account_close.go`
- Test: `trader/managed_ownership_test.go`, `api/handler_managed_accounts_test.go`

**Interfaces:**
- Consumes: Task 2 Binding、Task 4 Flatten，现有 AutoTrader New/Run 和 manager 自动重启入口。检查前必须做只读账户身份解析；发现与任一托管 Binding 同物理 UID 的其他 ExchangeID 同样拒绝。无可靠 UID 时按专用账户配置约束拒绝未核验的同所旧写入，不声称能检测所有未声明密钥副本。
- Produces: `execution.RequireLegacyAllowed(ctx context.Context,db *gorm.DB,user,exchangeID string) error`；检查托管绑定存在且 Enabled 或持有未终结托管 Run 时，旧路径一律拒绝。

- [ ] **Step 1: 写失败测试。** `TestManagedOwnershipBlocksLegacy` 覆盖 API Start、manager AutoStart、直接 NewAutoTrader/Run、Grid 启动；网络 mock 断言模式写入和下单调用 0。`TestManagedOwnershipAllowsEmergencyClose` 证明认证人工减仓不被开仓冻结拦住。`TestManagedOwnershipReadFailure` DB 查询失败不得按“未托管”放行。
  ```go
  if startErr == nil { t.Fatal("legacy start accepted managed account") }
  if writes != 0 { t.Fatalf("exchange writes=%d, want 0", writes) }
  ```
- [ ] **Step 2: 运行确认失败。** `go test ./trader/execution ./manager ./trader ./api -run '^TestManagedOwnership' -count=1`。
- [ ] **Step 3: 实现最早边界检查。** 所有旧构造器外部写入之前检查；Start/Run 再校验防止配置竞争。托管绑定启用的事务必须确认旧自动器已停止且不持有未完成入场，不能“先启用后 Stop”。不修改未绑定其他交易所的交易逻辑。
- [ ] **Step 4: 运行确认 PASS。** 同 Step 2；`go vet ./trader/execution ./planning/live`，审查所有 `SubmitOrder/PlaceOrder/OpenLong/OpenShort` 调用点，记录目标账户无法旁路的证据。
- [ ] **Step 5: 提交。** 仅暂存本任务文件；`git commit -m "fix(trading): 禁止旧交易器绕过托管账户执行权"`。

## 阶段验收与交接

仅使用临时库、固定时钟和本地 HTTP fake。执行全部新 TestManaged*、既有 `planning/core`/`planning/live`/`trader/execution` 测试；保存实际命令与通过数。不要运行那些先创建真实客户端再覆盖测试 URL 的旧构造器测试。

本阶段退出条件：两所 sandbox 隔离已由 HTTP fixture 验证、保护/退出安全不变量通过、旧旁路已封锁；自动交易仍 disabled。然后执行 `2026-11-06-02-hard-risk-controls.md`。
