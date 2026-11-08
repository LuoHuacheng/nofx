# NOFX 无人值守双交易所合约系统设计

状态：待审阅；本文件及关联计划不授权修改运行账户、启用实盘、读取凭据或部署。
基线：`4d11af37b3273220674fc657e0c619439fb04d77`，2026-11-06。本地 `.gitignore`、`kernel/engine.go` 和 `provider/localpool/` 是既有改动，不纳入实施提交。

## 目标与范围

将 NOFX 做成可在 Binance USDⓈ-M 与 OKX USDT 线性永续上独立运行、自动执行信号、自动管理保护与恢复的系统。无人值守是正常运行和可判定故障无需人工逐笔审批；交易所完全不可达、账实冲突等不可判定故障必须自动冻结新增风险并告警，不承诺任何故障下都能成交或恢复。

首版范围是本设计的建议选择，并非用户已确认的资金参数：

- 单向持仓、逐仓、仅 USDT 保证金；Binance sandbox 映射 Futures Testnet，OKX sandbox 映射模拟盘。
- 每个已绑定账户最多一个活动计划/持仓，复用现有账户 Lease；两个交易所可各运行一个，组合风控共同约束。
- 首批白名单 BTC/USDT、ETH/USDT，Binance 原生 `BTCUSDT`/`ETHUSDT`，OKX 原生 `BTC-USDT-SWAP`/`ETH-USDT-SWAP`。
- 独立专用子账户，不允许其他机器人并行写同一账户；人工紧急减仓允许，但必须立即触发对账。
- Linux/macOS 单实例、一个共享数据库、单个受管理数据目录。禁止把此版当成跨主机或多个独立数据库的互斥方案。
- 现有人工作业继续保留；新托管账户一旦绑定，旧 Autopilot/Grid/其他订单写入路径必须拒绝启动。旧策略只可通过新的信号适配器提供 Idea，不得直接下单。
- 首版不做双向、全仓、币本位、跨所套利、自动划转、马丁、多机 HA、任意脚本策略或新的前端交易工作台。

## 当前基础及缺口

复用 `trader/execution.Trader`、`execution.Decimal`、`planning/core.Compile`、`planning/live.Controller`、`planning/live.Run`/`Lease`、`planning.DraftStore`。保留已保存人工计划的摘要和行为，不给历史计划重新计算 hash。

当前 `planning/live/controller.go` 有持久化意图和未知回执查询，但失败需要人工介入、启动恢复依赖人工动作、服务懒加载；`planning/live/sizing.go:32` 的 `MaxRisk=Equity` 不适合无人值守。旧 Autopilot 的单向平仓、止损替换、虚构 RR、硬熔断和未知请求问题均未解决，不能直接开放它。

不新建第二个交易引擎。扩展有限订单篮子的执行监督；新增 `trading/risk` 与 `trading/automation` 两个业务包。依赖方向：`automation -> live -> risk -> execution`；`core -> execution`。`execution` 不得反向 import `core`、`live`、`automation` 或交易所包；API 负责组装工厂。

## 安全不变量

1. 所有增加风险的外部写入之前，必须有持久化意图、有效账户执行权、未过期政策和事务内风险预留。
2. UNKNOWN 不等于未提交或已成交；按同一原始订单 ID 查询，不更换 ID 重试。没有权威未受理证据就不重放。
3. 平仓只能减少风险。数量与实际仓位核对；仓位已平后任何重复退出都不得反向开仓。
4. 部分成交立即纳入风险和保护。SL 是必要保护；TP 异常停止新增风险并保留 SL，不能为补 TP 撤掉 SL。
5. 新止损未被确认前不撤有效旧止损；不支持安全替换时不移动止损，保留旧单并告警。价格已穿越止损则进入减仓退出。
6. 不确定、过期或读取失败的行情/账户状态禁止新增风险；减仓与保护仍可使用最近持久化意图和可核验的新账户读数。
7. 风险预算、熔断、权益峰值、日初基准、资金事件与风险预留均持久化；重启不清零。
8. 正常停止策略不等于平仓：区分 `pause_entries` 与 `flatten`。程序退出不撤交易所已有保护。
9. 人工运行必须保留原审批边界；自动审批必须绑定 owner、政策版本、环境、账户、计划 hash 和 signal ID，不伪造 `APPROVE LIVE`。
10. 数据库写失败时禁止新入场。如果已有仓位且无法持久化新保护/退出意图，不发无法追踪的订单：保留已有交易所保护、冻结账户并发紧急告警。

## 首版政策数值

这些值是待审阅的保守工程默认值，不是收益承诺，默认禁用自动交易。

| 字段 | 默认/界限 |
|---|---|
| enabled | false；sandbox 和 live 分别显式启用 |
| PerTradeRiskBPS | 50，即权益 0.5%，含手续费、滑点和资金费预留 |
| AccountOpenRiskBPS / PortfolioOpenRiskBPS | 100 / 100，即各自权益 1%；预留与已成交风险共同计入 |
| DailyLossBPS / DrawdownBPS | 200 / 500，即 2% / 5%；达到阈值触发 flatten，手动复位 |
| MaxMarginBPS / MaxGrossBPS / MaxDirectionalBPS | 3000 / 15000 / 10000，即 30% / 150% / 100%；账户和组合均检查 |
| MaxLeverage / MinRewardRisk | 3 / `"2"`；自动政策不得设置 RR=0 |
| FundingReserveBPS | 10；按入场名义金额加入计划亏损与扣减净收益；不是未来资金费保证 |
| MaxSnapshotAge / MaxSignalAge | 5s / 30s；未来时间偏移超过 1s 拒绝 |
| ProtectionDeadline / RESTReconcileInterval | 5s / 10s；以观察到首次成交的持久化时间计时 |
| RequestTimeout / RetryBackoff | 托管执行 HTTP 2s；仅只读查询重试 250ms、500ms、1s，单轮总 4s 截止 |
| MaxHold / SignalInterval / MaxConsecutiveSourceErrors | 24h / 按15分钟K线收盘各币一次 / 3；达到持仓期限自动退出，连续源错误暂停信号但继续监督；收盘检查点持久去重，失败不重复付费调用 |
| AlertRetry / DedupWindow | 1s、5s、30s，之后每 60s；相同账户/故障/阶段 60s 去重，发送失败不阻塞保护 |

保护期限是响应目标，不是网络或交易所的保证：5s 内不能确认有效 SL 时停止入场并启动减仓；如果未知 SL 请求或退出请求仍在途，先查询、只用减仓语义退出，不裸重试。查询/退出仍无法完成则进入 `halted_unknown`，保留风险预留及执行权，持续受限重试和告警。

初始入场只开放 `limit`，固定限价 30s 过期撤余单；不开放无人值守 market/ladder/twap，待后续单独验收。应急平仓使用交易所减仓市价；不承诺价格上限。原人工 Frontier 的已有订单类型保留。

## 核心接口与数据约定

以下为拟新增公开接口；现有 `execution.Trader` 不增必需方法，避免破坏 Bitget/Hyperliquid 与测试 mock。只有 Binance、OKX 必须实现 `execution.ManagedTrader`。

```go
// trader/execution/managed.go
// Quantity、Filled 始终为 base asset；只有适配器内部处理 OKX contracts。
type Environment string // "sandbox" | "live"，空字符串非法
type ManagedPosition struct {
    Symbol, Side string // Side: long|short
    Quantity, Entry, Mark, Liquidation, Margin Decimal
    StopPrice Decimal // 经保护查询确认；未知为空，不推算
}
type ManagedAccount struct {
    AccountKey, Venue string
    Environment Environment
    PositionMode, MarginMode string // one_way / isolated
    Equity, Available, MarginUsed Decimal
    Positions []ManagedPosition
    Orders []Order
    ObservedAt time.Time
}
type AccountEvent struct {
    ID, AccountKey, Kind, Symbol string // order|fill|funding|cashflow|reconcile_required
    Order Order
    Amount Decimal // funding/cashflow 是有符号金额
    Currency string // 必须明确；首版仅 USDT 可计入权益，未知币种标记账本不完整
    OccurredAt time.Time
}
type Capabilities struct { SafeStopAmend, PrivateEvents bool }
type ManagedTrader interface {
    Trader
    RouteClientID(string) (string,error)
    PrepareExecutionLeverage(context.Context,string,int) error
    ManagedSnapshot(context.Context) (ManagedAccount, error)
    VerifyManagedSettings(context.Context, string, int) error
    ManagedCapabilities() Capabilities
    AmendStop(context.Context, Order, Decimal) (Order, error)
    Events(context.Context, func(AccountEvent) error) error
}
```

`WithBinding` 必须透传 RouteClientID 与 PrepareExecutionLeverage，防止包装后丢失交易所路由和杠杆能力。`VerifyManagedSettings` 只验证单向、逐仓与目标杠杆；不擅自转换有仓账户。空仓模式设置必须作为显式账户准备动作并重新查询，普通启动验证失败即拒绝。`AmendStop` 不支持时返回 `execution.ErrUnsupported` 并保留旧止损；API 受理不算修改确认。

托管账户绑定新增 GORM 表 `managed_account_bindings`：`ID/UserID/ExchangeID/Venue/Environment/VenueAccountID/VerifiedAt/Enabled`，`(Venue,Environment,VenueAccountID)` 全局唯一，非敏感账户标识经签名账户接口核实；若当前 API 无可靠 ID，必须经初始化人工核对绑定后才允许启用，禁止退回 API Key hash 当物理账户身份。首版同一物理账户只允许绑定一份凭据配置，第二配置拒绝，轮换更新原配置；跨独立部署及未声明的密钥副本仍需运维保证专用账户。API 工厂通过 `execution.WithBinding(t ManagedTrader,b Binding) ManagedTrader` 包装只读快照和事件，将 AccountKey 统一标记为 Binding.ID，adapter 不猜测数据库身份。

```go
// trading/risk/types.go；Decimal = execution.Decimal
// 所有 BPS 为整数，价格/金额只用 Decimal + math/big.Rat。
type Policy struct {
    ID, UserID, AccountKey, PortfolioID, Version string
    Venue string; Environment execution.Environment
    Symbols []string; Enabled bool
    PerTradeRiskBPS, AccountOpenRiskBPS, PortfolioOpenRiskBPS int
    DailyLossBPS, DrawdownBPS, MaxMarginBPS, MaxGrossBPS, MaxDirectionalBPS int
    MaxLeverage, FundingReserveBPS int
    MinRewardRisk execution.Decimal
}
type Reservation struct {
    UserID, PolicyID, RunID, AccountKey, PortfolioID, Symbol, Side, PolicyVersion string
    Risk, Notional, Margin execution.Decimal
    State string // reserved|exposed|released；UNKNOWN 不释放
}
type Decision struct { Action, Reason string } // allow|pause_entries|flatten
```

`Policy` 规范化排序 Symbols 后以 JSON 的 SHA-256 生成 `Version`，金额与时长固定来自本设计首版配置；政策变更使旧自动审批失效。新增 `core.Constraints.FundingReserveBPS Decimal`（JSON omitempty，旧值缺省为 0），计划 hash 包含新非零字段但保持历史零字段 hash。

资金与权益：初次启用必须取得完整 UTC 日初以来的权益/资金证据；拿不到就拒绝 live 自动入场，不从当前余额伪造。归一化现金流后的权益 `adjusted = exchangeEquity - cumulativeNetCashflow`。日亏损以 UTC 日初 adjusted 为基准，回撤以持久化 adjusted 峰值为基准；启动当日无法从签名资金历史恢复日初则禁止 live 自动入场，sandbox 可显式创建首个基准。交易所权益已计入手续费/资金费时不再重复扣减；事件仅用于归属、审计及去重；手续费/资金费事件必须保留 Currency，非 USDT 且无法核实转换的事件使账本不完整。不同交易所不能互相抵扣 available margin，组合预算只汇总 USDT 权益和风险。

## 自动运行与恢复

`trading/automation` 产生或接收候选，调用统一风控、`core.Compile`、持久化 Draft、`Controller.AuthorizePolicy`、`Execute`。`AuthorizePolicy` 读取服务端政策，不接受调用方传入 enabled=true 绕过数据库。每个 signal `(AccountKey, SignalID)` 唯一，模型失败不得生成替代强制交易。

信号接口：`IdeaSource.Next(ctx context.Context, policy risk.Policy) (Signal,error)`；`Signal{ID string; Idea core.Idea; CreatedAt,ExpiresAt time.Time}`。复用 `planning/model.ChatPlanner` 与 `planning/market`，新增严格 JSON Idea 适配器；旧 `harness` 明确是人工一次性模式，不修改其 system prompt 来伪装自动授权。空 ideas/hold 均不交易，数量和杠杆只能由政策决定。

启动主动初始化服务，不能等待用户访问 planning API。先取得进程/账户执行权，扫描 Run 与 risk 预留，再通过 REST 拉取所有持仓、普通单、保护单和账单。只恢复可证明归属的保护与减仓；旧的入场计划不重放，过期信号作废。已暂停但仓位已核实且保护有效的运行可自动继续监督；未知状态持续冻结。用户显式 pause 或风控 flatten 的闩锁不会因重启/AI 恢复自动清除。

WebSocket 是唤醒与增量来源，REST 快照及交易所唯一事件 ID 是补偿基础。保存回放游标、重叠窗口、去重表；不假设两所存在统一全局序列。发现时间倒退、断连、事件缺口立即标记账户不新鲜，REST 对账成功后才恢复入场。

## 运维、授权和回滚

- 复用现有认证路由；政策更改、启动、暂停、flatten、复位全部 owner 校验。绑定不开放到公共 planning/chat 工具。
- 保留加密凭据存储；日志/告警不含密钥、签名、JWT 或完整私有 HTTP 请求。
- 账户执行权可用性以单实例部署约束和数据库 Lease 保证；SQLite 用进程级文件锁拒绝同一数据目录第二实例，锁不得依赖 PID 文件是否存在；Postgres 用专用连接持有数据库 advisory lock，失锁冻结新增风险。
- 持久化告警 outbox，最小交付为结构化本地日志及可选单一 HTTPS webhook；此轮不绑定第三方消息厂商。
- 仅添加业务表/可空字段；不删除、改写旧 Run 的 PlanJSON、审批摘要或历史成交。不运行生产迁移。
- 回滚自动交易先 pause_entries，确认无新入场，再处理仓位；有仓或 UNKNOWN 时继续运行保护监督。不能直接降级旧二进制，因为旧版本不认识托管绑定和自动授权。

## 分阶段交付与完成标准

1. 执行安全：两所 sandbox、托管账户身份/模式、保护与安全退出、旧路径封锁；仍无自动开仓。
2. 硬风控：止损风险定仓、事务预留、组合预算、持久化熔断；人工入口复用可配置托管风控。
3. 自动运行：服务端政策授权、信号源、主动启动、自动对账、事件流与告警；默认 disabled、先 sandbox。
4. 验收：两所同一故障矩阵、进程崩溃恢复、回放成本模型、运维记录；默认离线测试不触网，live 仍需用户另行授权。

每阶段可独立测试/提交；按顺序执行，不并行修改共享 controller/contract。上线资格来自故障验收，不以测试数量或收益曲线替代。收益、容量和交易信号有效性另作策略验证，不属于安全机制的成功承诺。
