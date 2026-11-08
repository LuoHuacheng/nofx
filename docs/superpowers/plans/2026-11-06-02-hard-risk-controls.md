# NOFX Hard Risk Controls Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 以止损金额决定仓位，持久化账户/组合预算、权益基准和硬熔断，使并发与重启不能绕过限额。

**Architecture:** 新增只依赖 execution 的 `trading/risk`，由 live 适配到 core.Constraints。风险预留与执行意图处于同一数据库事务；资金事件去重，UNKNOWN 不释放预算。先保持自动交易关闭。

**Tech Stack:** Go、GORM 事务与唯一索引、math/big.Rat、现有 core 编译器；无新依赖。

**Spec:** `../specs/2026-11-06-unattended-futures-design.md`；前置完成 `2026-11-06-01-managed-execution-safety.md`。

## Global Constraints

- 精确金额 `execution.Decimal`，禁止 float64 参与预算/比较/取整。
- 默认 disabled、3x、单笔 50bps、账户/组合在险 100bps、日亏 200bps、回撤 500bps。
- 保证金 3000bps、总名义 15000bps、方向名义 10000bps；净 RR≥2；资金费预留 10bps。
- UTC 日界线；账户/组合快照均不超过 5s；基准未知、事件缺口或 DB 失败禁止新增风险。
- 入金不算收益、出金不算亏损；交易所权益中已有的手续费/资金费不重复扣除。
- 不静默修改已保存人工计划、不批量重算历史摘要、不执行生产迁移或实盘操作。

## Review Focus

- 同时两个计划通过检查后超额：Task 3 在同一事务预留并验证只有一笔获准。
- 充提跨 UTC 日界线或历史补录：Task 2 去重并回算受影响基准，不悄悄复位熔断。
- UNKNOWN 被当成空仓释放预算：Task 3 要求平仓及挂单终结的权威证据。
- 老 Plan JSON 缺少资金费字段：Task 1 固定旧 JSON/hash fixture，验证可原样读取。
- 高杠杆/小止损/滑点导致负收益、最小单位取整越预算：Task 1 使用真实编译器检验最终数量和净损益。

---

## 文件与接口地图

`trading/risk/types.go` 定义政策/预留/账本；`policy.go` 校验与版本；`ledger.go` 保存资金事件及权益基准；`store.go` 提供事务预留和熔断。`planning/live/managed_sizing.go` 是风险包到 core 的唯一预算适配，避免 risk 反向依赖 planning。修改 `planning/core/plan.go` 仅新增可省略的资金费预算和测试；保持原 `AccountLimits(a,c)` 行为供非托管人工作业使用。

### Task 1: 止损风险定仓和资金费预留

**Files:**
- Create: `trading/risk/types.go`, `trading/risk/policy.go`, `trading/risk/policy_test.go`, `planning/live/managed_sizing.go`, `planning/live/managed_sizing_test.go`
- Modify: `planning/core/plan.go`, `planning/core/core_test.go`
- Test: `planning/core/funding_reserve_test.go`

**Interfaces:**
- Consumes: 阶段1 `execution.ManagedAccount`、现有 `core.Compile(s Snapshot,c Constraints,i Idea,now time.Time) (Plan,error)`；`execution.Number(string) (*big.Rat,error)`。
- Produces: spec 的 `risk.Policy/Reservation/Decision`；`DefaultPolicy(user,accountKey,portfolioID,venue string,env execution.Environment) Policy`；`ValidatePolicy(Policy) error`；`PolicyVersion(Policy) (string,error)`。
- Produces: `live.ManagedAccountLimits(policy risk.Policy,a execution.ManagedAccount,now time.Time) (core.Constraints,error)`。
- Produces: `core.Constraints.FundingReserveBPS core.Decimal`，`json:"funding_reserve_bps,omitempty"`；编译器 loss 增加 `entry*fundingBPS/10000`，gain 扣除同额；旧空字段按 0。
- Reservation 新增 `UserID,PolicyID string`，其余字段见 spec；禁止匿名预留。所有预算使用最终取整后 quantity。

- [ ] **Step 1: 写失败测试。** `TestManagedSizingUsesStopRisk` 用 equity=100/available=100、entry=100、SL=98、TP=106、fee=5bps、slip=10bps、funding=10bps、step=0.001 编译，断言 `p.Risk<=0.5` 且 q 按 step 向下取整；不是之前的 q=4.918。`TestManagedSizingRejectsWeakRewardRisk` TP=100.5 必须拒绝。`TestManagedSizingNoMinimumUpRound` 最小量超预算拒绝，不上调。`TestFundingReserveLegacyDigest` 将本阶段修改前生成的原始历史 Plan JSON/hash 作为固定 fixture，读取/Verify 后 hash 原样不变。
  ```go
  loss, err := execution.Number(string(p.Risk)); if err != nil { t.Fatal(err) }
  if loss.Cmp(big.NewRat(1, 2)) > 0 { t.Fatalf("risk=%s exceeds 0.5 USDT", p.Risk) }
  if legacy.Hash != originalHash { t.Fatal("historical digest changed") }
  ```
- [ ] **Step 2: 运行确认失败。** `go test ./trading/risk ./planning/core ./planning/live -run 'TestManagedSizing|TestFundingReserve' -count=1`；预期新包/字段未定义。
- [ ] **Step 3: 实现上述接口。** Policy 默认值严格用 spec；自动政策仅可调得更紧，MaxLeverage 1..3、MinRewardRisk≥2、所有限额正数，Symbols 非空白名单。资金费预算 0..1000bps，托管默认 10；旧人工缺省0。托管 sizing policy 不使用 account_margin 放宽逻辑，风险约束先算后再取 margin/notional/深度最小值；编译后的最终计划再次验证预算。
- [ ] **Step 4: 运行确认 PASS。** 同 Step 2；`go test ./planning/core ./planning/live ./trading/risk -count=1`，检查原人工计划测试未被放宽或删除。
- [ ] **Step 5: 提交。** 仅暂存本任务文件；`git commit -m "feat(risk): 按止损风险定仓并计入资金费预留"`。

### Task 2: 可恢复权益账本和资金事件

**Files:**
- Create: `trading/risk/ledger.go`, `trading/risk/ledger_test.go`, `trading/risk/store.go`

**Interfaces:**
- Consumes: `execution.ManagedAccount/AccountEvent`、Task 1 Policy。
- Produces: `risk.NewStore(db *gorm.DB) (*Store,error)`；`(*Store).PutPolicy(ctx context.Context,p Policy) (Policy,error)`；`(*Store).Policy(ctx context.Context,user,accountKey string) (Policy,error)`；`(*Store).Observe(ctx context.Context,a execution.ManagedAccount,events []execution.AccountEvent,now time.Time) error`。
- Produces GORM 记录：`EquityState{AccountKey string; Day string; AdjustedEquity,DayStart,Peak,NetCashflow execution.Decimal; Complete bool; ObservedAt time.Time}`，`LedgerEvent{AccountKey,EventID,Kind string; Amount execution.Decimal; OccurredAt time.Time}`；`(AccountKey,EventID)` 唯一。
- 另存每个 observation 的时间/原始 equity/累计现金流，足以在延迟资金事件后回算当日基准和峰值；保留账本不原地抹历史。

- [ ] **Step 1: 写失败测试。** `TestRiskLedgerCashflowNeutral` 100→入金50→权益150 调整后仍100，出金同理。`TestRiskLedgerEventDedup` 同 funding/cashflow 重复两次只入账一次；equity 已含 fee/funding 不再次扣费。`TestRiskLedgerUTCRollover` 23:59:59→00:00:00 正确开新日，重启读到原峰值；延迟补录前一天 cashflow 后基准回算，已触发闩锁保留。`TestRiskLedgerIncompleteHistory` live 无日初或资金页缺失 => Complete=false，不允许新入场；Currency为空/非USDT未核实转换同样不完整。
  ```go
  if !execution.Equal(string(state.AdjustedEquity), "100") { t.Fatal("cashflow counted as profit") }
  if persistedEvents != 1 { t.Fatalf("events=%d, want 1", persistedEvents) }
  ```
- [ ] **Step 2: 运行确认失败。** `go test ./trading/risk -run '^TestRiskLedger' -count=1`。
- [ ] **Step 3: 实现事务入账。** 表名 `managed_risk_policies/managed_equity_states/managed_equity_observations/managed_ledger_events`；PolicyVersion 服务端生成。signed cashflow 验证数值格式与事件归属；账本订正不能自动解除已触发熔断。已有 GORM 数据库可用，新增表不修改旧成交表。daily baseline live 缺失返回明确状态，sandbox 可显式创建首个基准。
- [ ] **Step 4: 运行确认 PASS。** 同 Step 2；使用临时 SQLite 文件关闭重开 Store 后重跑同一现金流序列，余额/基准/去重数保持一致。
- [ ] **Step 5: 提交。** `git add trading/risk`；`git commit -m "feat(risk): 持久化权益基准与资金事件账本"`。

### Task 3: 原子预算预留与组合限额

**Files:**
- Create: `trading/risk/reservations.go`, `trading/risk/reservations_test.go`, `planning/live/risk_gate.go`, `planning/live/risk_gate_test.go`
- Modify: `planning/live/controller.go`, `planning/live/types.go`, `planning/live/check.go`

**Interfaces:**
- Consumes: Task 2 Store、Task 1 Reservation、现有 live.Execute 和 complete。
- Produces: `(*Store).ReserveTx(ctx context.Context,tx *gorm.DB,r Reservation,now time.Time) error`；`(*Store).ExposeTx(ctx context.Context,tx *gorm.DB,runID string) error`；`(*Store).ReleaseTx(ctx context.Context,tx *gorm.DB,runID string) error`。
- Produces: `Controller.Risk *risk.Store`（nil 保留非托管旧行为；托管 Run nil 必须拒绝）。Run 新增 `Managed bool; PolicyID,PolicyVersion,PortfolioID string`，这些字段后来纳入自动授权 hash。
- GORM `managed_risk_reservations` 的 RunID 唯一；`managed_portfolio_locks` 的 PortfolioID 主键，通过事务写同一 portfolio 行串行化双交易所预算。不用只读 SELECT 后无锁 INSERT。

- [ ] **Step 1: 写失败测试。** `TestRiskReservationConcurrent` 两账户各请求占组合风险上限 60%，两 goroutine 同起，恰有一笔通过。`TestRiskReservationDuplicate` 同 RunID 重试不重复占用；不同内容同 ID 拒绝。`TestRiskReservationUnknownPersists` 关闭重开 DB 后 UNKNOWN 仍占额度。`TestRiskGateStopsBeforePOST` 超额、任一组合快照超过5s、政策过期、DB错误时 Place 调用0。`TestRiskReleaseNeedsTerminalProof` partial/保护待确认/遗留开仓单不释放；权威 flat+全部订单终态才释放。
  ```go
  if allowed != 1 { t.Fatalf("concurrent reservations accepted=%d, want 1", allowed) }
  if afterRestart.State == "released" { t.Fatal("UNKNOWN lost its reservation") }
  ```
- [ ] **Step 2: 运行确认失败。** `go test ./trading/risk ./planning/live -run 'TestRiskReservation|TestRiskGate|TestRiskRelease' -count=1`。
- [ ] **Step 3: 实现原子风险门。** 保存计划入场意图和预留使用同一事务，COMMIT 后才可 Submit。ReserveTx 从服务端 Policy/ledger 读 owner/version，核对风险、可用 margin、gross、directional 和 account/portfolio 开放风险；预留+已成交只计一次。释放只由已验证 terminal 的 complete/unused approval 事务调用，UNKNOWN 保持占用。SQLite busy/事务失败返回拒绝；Postgres 同 portfolio 行锁，不降低隔离。
- [ ] **Step 4: 运行确认 PASS。** 同 Step 2；`go test -race ./trading/risk ./planning/live -run 'TestRiskReservation|TestRiskGate|TestRiskRelease' -count=1`，临时独立数据库，不触及运行目录。
- [ ] **Step 5: 提交。** 仅暂存本任务文件；`git commit -m "feat(risk): 原子预留账户与组合风险预算"`。

### Task 4: 持久化硬熔断与受控复位

**Files:**
- Create: `trading/risk/breaker.go`, `trading/risk/breaker_test.go`, `planning/live/breaker_test.go`
- Modify: `trading/risk/types.go`, `planning/live/risk_gate.go`, `planning/live/controller.go`

**Interfaces:**
- Consumes: EquityState、Policy、阶段1 `Controller.Flatten`。
- Produces: `(*Store).Evaluate(ctx context.Context,user,accountKey string,now time.Time) (Decision,error)`；`(*Store).Pause(ctx context.Context,user,accountKey,reason string) error`；`(*Store).Reset(ctx context.Context,user,accountKey,expectedVersion string,now time.Time) error`。
- `managed_risk_breakers` 持久化 scope(account|portfolio)、scopeID、action、reason、triggeredAt、version；触发 portfolio flatten 时所有成员账户分别退出，未触发账户不能继续加仓。

- [ ] **Step 1: 写失败测试。** `TestRiskBreakerThresholds` equity100→98 精确触发日亏2%；peak100→95 触发5%回撤，97.999等边界使用十进制。`TestRiskBreakerRestart` 重开进程后闩锁仍在。`TestRiskBreakerReduceAllowed` pause/flatten 阻止 entry、允许 sl/exit。`TestRiskBreakerResetGuard` wrong owner/version、有仓/挂单/未知预留/基准不完整拒绝；新 UTC 日不自动清除。
  ```go
  if decision.Action != "flatten" { t.Fatal("2% daily loss did not flatten") }
  if resetWhileExposedErr == nil { t.Fatal("reset accepted unresolved exposure") }
  ```
- [ ] **Step 2: 运行确认失败。** `go test ./trading/risk ./planning/live -run '^TestRiskBreaker' -count=1`。
- [ ] **Step 3: 实现 Evaluate 与 live 集成。** 不信任 AI 的置信度/临时数值，先校验账本完整度再评估；风险阈值达到即返回 flatten。live.step 在新入场之前查门，在已有持仓监督中执行减仓；避免持有 runLock 时递归调用再取锁的公开 Flatten，使用单一内部已锁函数。Reset 不删除历史事故/峰值；建立审计复位基准须记录 owner、旧新基准及版本。
- [ ] **Step 4: 运行确认 PASS。** 同 Step 2；`go test ./trading/risk ./planning/core ./planning/live -count=1`；`go vet ./trading/risk ./planning/live`。
- [ ] **Step 5: 提交。** 仅暂存本任务文件；`git commit -m "feat(risk): 增加日亏损回撤硬熔断与受控复位"`。

## 阶段验收与交接

使用100 USDT账户复现 SL=98/entry=100 的旧高风险案例，新政策最终预算≤0.5 USDT，不能仅在 UI 显示告警。并发与进程重开用例通过。保留人工旧计划 hash 兼容 fixture，不为通过测试放宽标准。

提交审查后进入 `2026-11-06-03-autonomous-runtime.md`；本阶段不能单独启用自动信号或 live。
