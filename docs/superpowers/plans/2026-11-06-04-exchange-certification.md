# NOFX Exchange Certification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用相同故障矩阵验证 Binance/OKX 的保护、对账与风险不变量，生成可审阅的运行证据和上线门槛。

**Architecture:** 默认完全离线，使用本地交易所 HTTP/WS fake 和独立临时数据库；独立 sandbox opt-in 测试证明真实接口兼容性。收益验证与执行安全分开，不把合成回测当盈利证明。

**Tech Stack:** Go testing/httptest/os/exec、现有适配器与backtest结构、精确Decimal、Markdown验收记录；不新增依赖。

**Spec:** `../specs/2026-11-06-unattended-futures-design.md`；前置计划01、02、03全部通过。

## Global Constraints

- 默认测试不触网、不读.env、不连接真实交易所、不运行main、不使用运行库；fixture显式本地占位密钥。
- sandbox只能经用户单独授权及专用绑定运行，禁止live回退；本计划不授权实盘或部署。
- 两交易所使用相同安全不变量，数量差异仅在adapter；保护5s响应目标不被描述为网络保证。
- 执行授权、资金政策、owner和环境均必须可审计；性能/盈利结果不能代替故障验收。
- 不降低lint/typecheck/vet/测试门槛，不为了让旧测试通过删除保护检查。

## Review Focus

- HTTP fake 与交易所实际回执差异：Task 1真实格式fixture＋Task 4 sandbox核验，不只mock Broker。
- 数据库提交后、网络请求前后各个崩溃点：Task 2真实子进程退出后重开，不只调用构造器两次。
- OHLC同根同时穿SL/TP：Task 3采用不利成交顺序，避免回测优先止盈。
- 资金费周期变更/资金费币种未知：Task 3保留原事件时间与币种，无法换算则报告不完整而非0。
- 回滚旧程序后失去托管保护：Task 4运维流程禁止有仓或UNKNOWN时直接切回旧二进制。

---

## 文件与接口地图

新增 `trading/certification` 仅用于合同/端到端故障测试，fixture采用原生API结构。`backtest/managed_costs.go` 为托管策略验证提供保守成本模型，不改写既有账户算法。`docs/operations/unattended-futures.md` 和验收模板保存命令、提交、环境、结果及残余限制。

### Task 1: 两所共用的故障验收矩阵

**Files:**
- Create: `trading/certification/fake_exchange_test.go`, `trading/certification/managed_contract_test.go`, `trading/certification/testdata/binance_receipts.json`, `trading/certification/testdata/okx_receipts.json`

**Interfaces:**
- Consumes: 阶段1两个 NewManagedAccountTrader、阶段3 Runtime、阶段2风险 Store；测试把HTTP/WS transport指向本地server，禁止真实端点。
- Produces test helpers: `newExchangeFixture(t *testing.T,venue string) *exchangeFixture`；`(*exchangeFixture).Scenario(name string)`；`AssertFlatAndNoRiskOrders(t *testing.T)`；`AssertAtMostOneEntry(t *testing.T,signalID string)`。fixture持有独立temp DB、固定clock、native orders/fills和请求计数。

- [ ] **Step 1: 写失败测试。** `TestManagedContractFaultMatrix` 每venue遍历以下scenario并断言：
  ```text
  accepted_response_lost -> entry POST一次，按原ID发现成交并补SL
  partial_fill_restart -> 按实际量保护，预算不释放，无补发入场
  sl_accepted_response_lost -> 原ID查到后不重复SL
  sl_rejected -> 到5s启用reduce-only退出，flat后才终结
  tp_rejected -> SL不撤、新入场暂停
  cancel_races_fill -> 把撤单期间新增成交纳入保护/退出
  manual_close_races_sl -> 不反向开仓，余单清理可核验
  stale_snapshot_and_ws_gap -> REST补偿前不新开仓
  rate_limit_and_clock_skew -> 受限读取重试，写单不盲目重试
  db_commit_failure -> 没有持久化意图就没有外部POST
  ```
  ```go
  if entryPOSTs != 1 { t.Fatalf("entry POSTs=%d, want 1", entryPOSTs) }
  if reversePositionCreated { t.Fatal("risk-reducing action opened reverse position") }
  ```
- [ ] **Step 2: 运行确认失败。** `go test ./trading/certification -run '^TestManagedContractFaultMatrix$' -count=1`；先让未完成场景产生明确FAIL，禁止用Skip占位。
- [ ] **Step 3: 实现最小fixture。** native字段与执行时官方文档/既有adapter测试一致，HTTP实际走签名与序列化；确定性时钟推进，不使用长sleep。模拟订单接受和响应发送作为两个独立动作，才能测响应丢失。发现业务缺陷回修归属文件并补其单元用例，不在fake里替真实系统“纠错”。
- [ ] **Step 4: 运行确认 PASS。** 同 Step 2；`go test -race ./trading/certification -run '^TestManagedContractFaultMatrix$' -count=1`，20个venue×scenario组合均实际运行。
- [ ] **Step 5: 提交。** `git add trading/certification`；`git commit -m "test(trading): 增加双交易所执行故障合同验收"`。

### Task 2: 崩溃恢复与单实例竞争

**Files:**
- Create: `trading/certification/crash_recovery_test.go`, `trading/certification/ownership_test.go`
- Modify only if defects found: `planning/live/controller.go`, `trading/automation/recovery.go`, `trading/risk/reservations.go`, `trader/execution/process_lock.go`

**Interfaces:**
- Consumes: Task1 fixture、阶段3 AcquireProcessLock/Runtime.Recover。
- Produces: `TestManagedCrashHelperProcess(t *testing.T)` 只在特定testing子进程参数下启动临时runtime，不调用产品main；父进程在fixture观察到指定持久化/HTTP事件后强制终止该测试子进程。

- [ ] **Step 1: 写失败测试。** `TestManagedCrashRecovery` 覆盖 intent commit前、commit后POST前、交易所受理后ACK前、fill保存后SL前、SL受理后保存前、exit受理后保存前；重启后原signal entry POST≤1、UNKNOWN不释放、已成交量有保护或进入退出。`TestManagedSecondProcessRejected` 两子进程争同dataDir/DB只有一个能进入信号循环。`TestManagedFundingCursorRestart` 最后一页commit前退出，重拉不双计资金。
  ```go
  if postCrashEntryCount > 1 { t.Fatal("crash caused duplicate entry") }
  if unknownReservationReleased { t.Fatal("crash erased unresolved risk") }
  ```
- [ ] **Step 2: 运行确认失败。** `go test ./trading/certification -run 'TestManagedCrashRecovery|TestManagedSecondProcessRejected|TestManagedFundingCursorRestart' -count=1`。
- [ ] **Step 3: 实现进程测试。** 子进程只继承测试所需白名单环境，避免继承真实交易凭据；入口参数只有fixture地址和临时DB路径。使用测试mock/注入transport报告断点，不在生产交易代码加可绕过校验的故障开关。重启观察最终交易所和数据库事实，不只看进程exit code。
- [ ] **Step 4: 运行确认 PASS。** 同 Step 2；确认无遗留子进程、无仓库根DB副产物。再运行 `go test ./trading/certification -count=1`。
- [ ] **Step 5: 提交。** 仅暂存本任务文件和必要缺陷回修；`git commit -m "test(trading): 验证崩溃断点恢复与执行权竞争"`。

### Task 3: 保守成本与持仓回放验证

**Files:**
- Create: `backtest/managed_costs.go`, `backtest/managed_costs_test.go`, `trading/certification/cost_replay_test.go`
- Modify: `docs/operations/unattended-futures.md`

**Interfaces:**
- Produces in backtest: `ManagedCostInput{Side string; Quantity,Entry,Exit,FeeBPS,SlippageBPS execution.Decimal; Funding []execution.AccountEvent}`；`ManagedCostResult{Gross,Fees,Slippage,Funding,Net execution.Decimal; Complete bool}`。
- Produces: `CalculateManagedCosts(in ManagedCostInput) (ManagedCostResult,error)`；`ResolveManagedBarExit(side string,stop,target,open,high,low execution.Decimal) (price execution.Decimal,reason string,err error)`。
- Consumes: 真实确认成交的AccountEvent，预估与实收单独列，不把预留资金费视为实收费用；保留原 backtest.Account 行为及历史baseline。

- [ ] **Step 1: 写失败测试。** `TestManagedCostsExact` long q1/entry100/exit110/fee5bps/slip0/funding=-0.01 => gross10、fees0.105、funding=-0.01、net9.885；重复资金事件不双计，非USDT未知资金费标记incomplete（AccountEvent.Currency 已由阶段1定义）。`TestManagedBarExitConservative` 同bar高低都穿目标/止损则SL先；long跳空open95低于SL98按95、short对称。`TestManagedCostReplay` fixture成交账单重放与汇总权益变化一致，差异非零必须报告而非强行平账。`TestManagedFundingIntervals` 同周期变更事件在0h/1h/5h分别-0.01/-0.02/+0.005，资金费合计-0.025、上述交易net=9.87，不套固定8h周期。
  ```go
  if !execution.Equal(string(result.Net), "9.885") { t.Fatalf("wrong net: %s", result.Net) }
  if exitReason != "stop_loss" { t.Fatal("same-bar ambiguity preferred profit") }
  ```
- [ ] **Step 2: 运行确认失败。** `go test ./backtest ./trading/certification -run '^TestManaged(Cost|BarExit|Funding)' -count=1`。
- [ ] **Step 3: 实现独立成本函数。** 所有运算Decimal/big.Rat。复用阶段1的 `execution.AccountEvent.Currency`，核对阶段3两所历史适配器有完整填充；unknown currency不得默认为USDT。沿用实际资金费时间，不假设永远8小时。文档区分交易所真实强平字段与旧简化1/leverage模型；此任务不伪造完整组合保证金模拟器。收益验证报告必须列手续费、资金费、滑点、未建模项和样本覆盖。
- [ ] **Step 4: 运行确认 PASS。** 同 Step 2及 `go test ./trading/risk ./trading/automation -count=1`，确保资金事件回放兼容，托管资金账本仍拒绝未知币种。
- [ ] **Step 5: 提交。** 仅暂存本任务涉及文件；`git commit -m "test(backtest): 增加托管交易保守成本与成交回放验证"`。

### Task 4: sandbox 验收入口与上线操作手册

**Files:**
- Create: `trading/certification/sandbox_test.go`, `docs/operations/unattended-futures-acceptance-template.md`
- Modify: `docs/operations/unattended-futures.md`

**Interfaces:**
- Consumes: Task1 fixture 守卫、阶段3 Runtime、阶段1 Binding/Environment，现有已授权sandbox隔离库中的加密配置。
- Produces: `TestSandboxManagedLifecycle(t *testing.T)`；显式 flags `-sandbox-venue=binance|okx`、`-sandbox-binding=<verified-id>`、`-sandbox-db=<isolated-path>`。
- 不提供live测试旗标；未给flags默认Skip（只适用于本sandbox测试，离线矩阵不允许Skip）。读取已授权隔离sandbox库中的加密专用凭据，不扫描.env/用户凭据目录。

- [ ] **Step 1: 写失败测试。** 先用本地fixture测试flags守卫：`TestSandboxGateRejectsLive` binding或HTTP环境为live、路径为运行DB、owner不符、模式非逐仓单向均拒绝且POST=0。`TestSandboxLifecycleAssertions` 验证计划→成交→SL/TP确认→减仓→flat→无余单→预算释放；中途失败保留状态和证据，不自动删除账户数据。
  ```go
  if liveBindingErr == nil || unsafePOSTs != 0 { t.Fatal("sandbox gate accepted unsafe account") }
  if !execution.Equal(finalPosition, "0") || remainingOrders != 0 { t.Fatal("sandbox lifecycle not settled") }
  ```
- [ ] **Step 2: 运行确认失败。** `go test ./trading/certification -run 'TestSandboxGate|TestSandboxLifecycleAssertions' -count=1`；尚未实现守卫/入口时FAIL。
- [ ] **Step 3: 实现入口和运行手册。** sandbox入口只复用正式runtime，不另写下单代码。手册包含：绑定身份核验、模式/权限检查、数据库备份与恢复、pause与flatten、UNKNOWN处置、API失效/资金事件缺口、凭据轮换、进程锁、升级顺序和回滚限制。模板记录HEAD/官方文档日期/环境/订单ID/保护确认耗时/日亏回撤/异常/遗留限制，禁止记录凭据。
- [ ] **Step 4: 离线验证并记录验收门槛。** 守卫测试必须PASS；`go vet ./...` 在受控离线环境执行，缺模块如实报告，不能自动安装；不直接运行可能触真实端点的旧全仓测试。sandbox实际命令如下，先设置非敏感变量 NOFX_SANDBOX_BINDING_ID/NOFX_SANDBOX_DB_PATH 为已核验绑定和隔离库路径；只有用户明确授权该隔离环境后才运行：
  ```bash
  go test ./trading/certification -run '^TestSandboxManagedLifecycle$' -count=1 -args -sandbox-venue=binance -sandbox-binding="$NOFX_SANDBOX_BINDING_ID" -sandbox-db="$NOFX_SANDBOX_DB_PATH"
  # OKX 单独执行同一命令，替换 venue 与 binding；不得复用主账户数据库。
  ```
  首版建议门槛：两所各完整周期通过、离线故障矩阵全部通过、sandbox连续7天无未解决保护/账实冲突事故、每次异常有自动处置证据。live默认仍关闭；小资金live额度和时段须用户另行确认，sandbox结果不证明实盘收益。
- [ ] **Step 5: 提交。** `git add trading/certification/sandbox_test.go docs/operations/unattended-futures.md docs/operations/unattended-futures-acceptance-template.md`；`git commit -m "docs(trading): 增加双交易所验收入口与上线操作手册"`。

## 最终完成条件

离线合同测试全部通过且没有跳过；sandbox实际执行证据单独列明（未授权则明确未执行）。自动交易默认关闭、所有政策默认保守、未知状态不复位。代码完成、sandbox验收、live启用是三种不同状态，交付报告分别说明。

这四份计划完成不自动授权真实资金交易，也不代表信号有正期望；策略盈利需独立的样本外与小资金验证。
