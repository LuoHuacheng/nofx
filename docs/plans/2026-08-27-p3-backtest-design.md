# P3 回测子系统设计与评估

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 `dev-plus` 上以当前 `kernel/` 架构为基准，分阶段落地一个可独立验证的历史回测子系统，不引入 nofxplus 的 `decision` 包双份维护。

**Architecture:** 回测核心（模拟账户、数据回放、权益、指标）完全自包含；AI 决策通过薄适配层复用当前 `kernel` 的 `StrategyEngine` / `GetFullDecisionWithStrategy`，而不是复制 `nofx/decision`。存储使用现有 GORM `store` 新增一张回测运行表，API 挂到现有 `/api` 分组，前端独立页面。LLM 自动反馈 / prompt 自进化排除在本阶段之外。

**Tech Stack:** Go 1.27、GORM、当前 `market/` 数据通道（历史数据 REST + 已有 K 线存储）、Gin、React/Vite。

**Spec:** 本文件即设计说明；实施顺序见「分阶段方案」。

## 0. 边界与非目标（本期）

- 不回测 tick 级微观结构，本期使用 **bar-on-close** 精度。
- 不复制 `nofx/decision` 包；回测 AI 决策统一走当前 `kernel`。
- 不实现 LLM 自动反馈、prompt 自动重写、因子自动寻优、合规自动执行。
- 不将回测结果自动应用到实盘策略。
- 不新增付费数据源；仅复用当前 `market/` 已支持的数据通道。
- 仅在 `dev-plus` 实施；不覆盖 `origin/dev` 现有功能。

## 1. 评估结论（2026-08-27）

### 1.1 nofxplus backtest 目录结构

28 个源文件，约 1.2 万行，可按依赖分层：

| 分层 | 文件 | 依赖 | 可否直接移植 |
|---|---|---|---|
| A. 自包含核心 | account.go, equity.go, metrics.go, lock.go, registry.go, retention.go, types.go, constants.go, utils.go, persistence_db.go | 仅 stdlib + logger | ✅ 低风险 |
| B. 数据层 | datafeed.go | market | ✅ 需适配当前 market API |
| C. 存储层 | storage.go, storage_db_impl.go, config.go | store（含不存在的 BacktestStore）| ⚠️ 需新增 store 表 |
| D. AI 决策执行 | runner.go, manager.go, ai_client.go, aicache.go | **nofx/decision**、mcp、store | ❌ 直接依赖 decision 包 |
| E. 调优/反馈 | feedback.go, prompt_optimizer.go, factor_optimizer.go, calibration*.go, compliance_tracker.go, smart_heuristics.go | decision、store、config | ❌ 高风险，且依赖 D |

### 1.2 最大障碍：`nofx/decision` 包

nofxplus 把当前项目的 `kernel` 拆成了独立 `decision` 包，并做了大量修改。直接移植 backtest 会让仓库出现两个几乎一样的决策引擎，后续任何 kernel 修改都要同步两份。关键签名差异：

- `NewStrategyEngine(config)` vs 我们 `NewStrategyEngine(config, claw402WalletKey ...string)`
- `GetFullDecisionWithStrategy(ctx, client, engine)` vs 我们 `(ctx, client, engine, variant)`
- decision.Context 新增 6 个字段（PerformanceFeedback、OptimizedWeights 等）并删除了我们独有的 `VergexDataMap` / `NetFlowRankingData` / `PriceRankingData`
- nofxplus 丢失了 `getAI500Coins`、`getHyperAllCoins`、`getVergexSignalCoins`、Language 系统、claw402 路由等我们已有能力

**结论：不复制 decision 包。** backtest 的 AI 决策统一走当前 `kernel`，丢失的能力（vergex、claw402、多语言）在回测里全部保留。

进一步评估表明（见第 1.6 节），`decision` 包的真正优点并非“更简洁的 engine”，而是 `trade_failure.go` 失败交易归因、`RecentOrder` 微观结构字段扩展、以及 `Context` 反馈字段闭环。这些优点完全可以在不合并整个 `decision` 包、不放弃 vergex/claw402/多语言的前提下，增量吸收到当前 `kernel` 中。因此 P3 阶段只需要保证回测 runner 能调用当前 `kernel` 即可，不需要把 `decision` 包作为依赖引入。

### 1.3 store 差异

- nofxplus 新增 `store/backtest.go`（725 行）：`BacktestStore`、`RunMetadata`、`EquityPoint`、`TradeEvent`、`PromptVariantData` 等
- nofxplus 新增 `store/trade_outcome.go`
- 我们的 `store.StrategyConfig` 与 nofxplus 存在字段差异（多了 `StrategyType`/`Language`/`PromptSections`/`GridConfig`，少了 `TradingMode`）

**结论：按我们的 schema 重新设计回测存储，不照搬 nofxplus 的 725 行 SQL 迁移。**

### 1.4 API 表面

nofxplus 挂了 18 个 `/api/backtest/*` 路由（start/pause/resume/stop/status/runs/equity/trades/metrics/analysis/trace/decisions/export/klines/prompt-variants/prompt-performance/prompt-activate）。prompt 相关 4 个属于 P3.5 之后的自动调优，本阶段不做。

### 1.5 风险分级

| 阶段 | 内容 | 风险 | 工作量 |
|---|---|---|---|
| P3.1 自包含核心 | account/equity/metrics/datafeed/lock/registry/retention | 低 | 1-2 天 |
| P3.2 存储层 | BacktestStore（按我们 schema 重写）+ 表迁移 | 中 | 1 天 |
| P3.3 kernel 适配层 | 把 runner 基于当前 `kernel` 重写，处理签名差异 | 中 | 1-2 天 |
| P3.4 API + 前端 | `/api/backtest/*` + 独立页面 | 中 | 2-3 天 |
| P3.5 后续（不做） | feedback/prompt_optimizer/factor_optimizer/compliance/calibration | 高 | 数周 |

### 1.6 补充评估：`nofx/decision` 包是否值得合并

#### 1.6.1 `decision` 包的真实优点

| 优点 | 当前 `kernel` 状态 | 价值 | 是否需要引入 |
|---|---|---|---|
| `trade_failure.go` 失败交易归因 | ❌ 缺失 | 高 | 是 |
| `RecentOrder` 微观结构字段扩展 | ❌ 缺失 | 高 | 是（配合 trade_failure） |
| `Context` 反馈字段（PerformanceFeedback、CalibratedThresholds 等） | ⚠️ 部分缺失 | 中高 | 可选 |
| 更简洁的 `StrategyEngine`（去掉 vergex/claw402 客户端） | ✅ 等价，只是功能更多 | 中 | 否 |
| 更统一的 prompt 入口 | ✅ 已有类似实现 | 中 | 否 |
| `threshold_calibrator.go` | ✅ 已存在，且去掉了 gonum 依赖 | 低 | 否 |
| `threshold_persistence.go` | ✅ 已存在 | 低 | 否 |
| 双语 schema / 多语言支持 | ✅ 已存在且中文更完整 | 低 | 否 |

#### 1.6.2 真正值得吸收的能力

1. **`trade_failure.go`**：基于规则的交易失败归因，输出 `PrimaryReason`、`ConfidenceScore`、`Evidence`、`Recommendation`。这是 decision 包区别于当前 kernel 的核心能力。
2. **`RecentOrder` 扩展字段**：`EntrySlippage`、`EntryDepth`、`MaxFavorableExcursion`、`StopDistanceVsATR`、`VolumeDeltaDuringTrade` 等，是 `trade_failure.go` 的数据基础。
3. **`Context` 反馈字段**：把 `ThresholdCalibrator` 的结果注入 prompt，实现“分析 → 反馈 → 决策”闭环。当前 kernel 有 `ThresholdCalibrator` 但缺少这个注入通道。

#### 1.6.3 不需要放弃的现有能力

`decision` 包放弃 vergex、claw402、多语言只是为了自身代码简洁，并不是因为这三项与它的优点冲突：

- **Vergex**：`trade_failure.go` 不依赖 Vergex；`Context` 的反馈字段可以和 `VergexDataMap` 共存。
- **Claw402**：只是 `NewStrategyEngine` 签名差异，完全可以在保留 claw402 路由的同时支持反馈字段。
- **多语言**：`decision` 的中文 prompt 内容被英文化，实际是削弱了多语言支持。

因此，**“为了 decision 的优点放弃这些功能”是一个伪二分法**。真正的问题是：是否愿意为了代码简洁而主动放弃这些业务功能。

#### 1.6.4 对 P3 的影响

P3 回测阶段**不需要**合并整个 `decision` 包，也不需要引入 `feedback.go` / `prompt_optimizer.go`（这些属于 P3.5 之后的自动调优）。

如果后续要吸收 `trade_failure.go` 等优点，建议在 P3 之后的独立阶段做：
- 把 `decision/trade_failure.go` 迁移到 `kernel/trade_failure.go`
- 扩展 `kernel.RecentOrder` 字段
- 在 trader 平仓时填充这些字段
- 可选地增加 `kernel.Context` 反馈字段，并在 prompt 中读取

这部分工作不属于 P3，但应在 P3 设计时预留 `BacktestTradeEvent` 等结构，使其未来能支持失败分析所需的字段。

## 2. 回测执行模型

### 2.1 精度与撮合

- **执行精度**：本期采用 `bar-on-close`。每个 bar 收盘后，根据该 bar 的 close 价格一次性撮合所有信号。不模拟 bar 内价格路径、不模拟滑点、不模拟部分成交。
- **预留接口**：在 `types.go` 中定义 `ExecutionModel` 接口，默认实现 `BarCloseExecution`。后续可新增 `TickExecution` / `SlippageExecution` 而不破坏 runner。
- **订单类型**：只支持 market order。limit / stop-limit 信号如果生成，按 bar close 价格以 market order 模拟成交。

### 2.2 手续费与资金费率

- **手续费**：从 `store.ExchangeConfig` 中读取用户配置的 maker/taker 费率；未配置时按交易所默认值（OKX taker 0.0005、Binance taker 0.001）兜底。所有回测手续费参数写入 `RunMetadata` 快照，保证结果可复现。
- **资金费率**：仅对永续合约仓位生效。从 `market/` 历史 funding rate 接口读取；数据缺失时按 0 处理，并在 trace 日志中标记。
- **保证金模式**：仅支持 `cross` 全仓模式。逐仓（isolated）本期不做。

### 2.3 多 symbol 时间推进

- **全局时间桶**：按 1 分钟为最小时间桶，对所有参与回测的 symbol 做外连接（outer join）。每个时间桶内，只要有任一 symbol 产生新 bar，就推进一次回测循环。
- **数据对齐**：如果某个 symbol 在当前时间桶没有 bar，使用其上一个已知 close 价格填充，保证仓位权益可以连续计算。
- **决策调用时机**：每个时间桶推进完成后，构造 `kernel.Context` 并调用一次 `GetFullDecisionWithStrategy`。决策结果中的订单在下一个时间桶开盘前完成撮合（即当前 bar close 价格成交）。

## 3. 分阶段方案

### P3.1：自包含回测核心（独立可测）

**Files:**
- Create: `backtest/account.go`, `backtest/equity.go`, `backtest/metrics.go`, `backtest/lock.go`, `backtest/registry.go`, `backtest/retention.go`, `backtest/types.go`, `backtest/constants.go`, `backtest/utils.go`, `backtest/datafeed.go`
- Test: 每个文件对应 `_test.go`，外加 `backtest/engine_test.go` 作为集成测试主线

**Interfaces:**
- `BacktestAccount`：OpenLong/OpenShort/CloseLong/CloseShort、余额、保证金、仓位、手续费、强平判定
- `DataFeed`：按时间顺序迭代 K 线、OIData、FundingRate
- `EquityCurve` / `Metrics`：收益率、胜率、Sharpe、最大回撤、ProfitFactor
- `RunRegistry`：runID 的创建/恢复/清理
- `RunLock`：并发防重入
- `ExecutionModel`：默认 `BarCloseExecution`，预留扩展点

**不做：** AI 调用、决策引擎、store 持久化（下一步）、任何 LLM 自动反馈。

### P3.2：存储层（按当前 schema 设计）

**Files:**
- Create: `store/backtest.go`（重写，不照搬 nofxplus）
- Modify: `store/store.go`（注册 BacktestStore）

**Schema:**

```go
// BacktestRun 回测运行元数据与最终指标
type BacktestRun struct {
    ID                string `gorm:"primaryKey"`
    Name              string
    Status            string // pending|running|paused|completed|failed|cancelled
    StrategyConfigJSON string `gorm:"type:text"` // 策略配置快照
    ModelConfigJSON    string `gorm:"type:text"` // 模型配置快照
    Symbols           string // JSON array
    StartTime         time.Time
    EndTime           time.Time
    Timeframe         string // 1m, 5m, 1h, etc.
    FeeMaker          float64
    FeeTaker          float64
    InitialBalance    float64
    FinalBalance      float64
    TotalReturnPct    float64
    MaxDrawdownPct    float64
    SharpeRatio       float64
    WinRate           float64
    ProfitFactor      float64
    TradeCount        int
    CreatedAt         time.Time
    UpdatedAt         time.Time
}

// BacktestEquityPoint 权益曲线采样点
type BacktestEquityPoint struct {
    ID      uint64 `gorm:"primaryKey"`
    RunID   string `gorm:"index"`
    Time    time.Time
    Balance float64
    Equity  float64 // 包含未实现盈亏
}

// BacktestTradeEvent 成交事件
type BacktestTradeEvent struct {
    ID        uint64 `gorm:"primaryKey"`
    RunID     string `gorm:"index"`
    Time      time.Time
    Symbol    string
    Side      string // buy/sell
    Position  string // open/close
    Price     float64
    Size      float64
    Fee       float64
    PnL       float64 // 平仓时有效
}

// BacktestDecisionLog 决策输入/输出快照（可选，用于复现）
type BacktestDecisionLog struct {
    ID        uint64 `gorm:"primaryKey"`
    RunID     string `gorm:"index"`
    Time      time.Time
    ContextJSON string `gorm:"type:text"`
    DecisionJSON string `gorm:"type:text"`
}
```

**约束：**
- 用 GORM AutoMigrate 而非手写 `ALTER TABLE`
- 兼容 SQLite + PostgreSQL
- `BacktestEquityPoint` 每 5 个时间桶采样一次，trade 事件出现时强制采样
- 不引入 nofxplus 的 `trade_outcome.go` 字段扩展（那是给 threshold_calibrator 用的，已在 kernel 完成）

### P3.3：kernel 适配层 + runner

**前置验证：** 在写 runner 之前，先写一个 50 行左右的适配层探针（`backtest/kernel_probe_test.go`），验证以下调用在空参数下不 panic：
- `kernel.NewStrategyEngine(strategyConfig, "")`
- `kernel.GetFullDecisionWithStrategy(ctx, client, engine, "")`
- 构造 `kernel.Context` 时不存在的字段（如 `PerformanceFeedback`）保持 nil

如果探针失败，必须先修 kernel 或调整适配方案，再进入 runner 实现。

**Files:**
- Create: `backtest/runner.go`, `backtest/manager.go`, `backtest/ai_client.go`, `backtest/aicache.go`
- Modify: 无（不动 kernel）

**Interfaces（适配后，全部基于当前 kernel）：**
- `kernel.NewStrategyEngine(strategyConfig, "")` — 回测时 claw402Key 传空
- `kernel.GetFullDecisionWithStrategy(ctx, client, engine, "")` — variant 传空
- `kernel.Context` 直接构建；不存在的字段（VergexDataMap 等）保持 nil 或按回测 symbol 预填 MarketDataMap 后走 `fetchMarketDataWithStrategy` 的既有跳过逻辑

**关键改动：**
- runner 中所有 `decision.X` → `kernel.X`
- `GetFullDecisionWithStrategy` 第三个参数 variant 传 `""`
- `NewStrategyEngine` 补 `claw402WalletKey` 参数
- AI client 复用当前 `mcp` 注册表（含 claw402/deepseek 等）

**不做：** `feedback.go` 的 LLM 反馈注入、`prompt_optimizer.go` 自动重写、`trade_failure.go` 的失败归因（这些属于 P3 之后可选吸收 decision 优点的独立阶段，见 1.6.4）。

### P3.4：API + 前端

**Files:**
- Create: `api/backtest.go`, `web/src/components/BacktestPage.tsx`（或复用现有页面结构）
- Modify: `api/server.go`（注册路由）、`api/route_registry.go`、`web/src/router/AppRoutes.tsx`

**路由：**

```
POST /api/backtest/start
POST /api/backtest/pause
POST /api/backtest/resume
POST /api/backtest/stop
POST /api/backtest/delete
GET  /api/backtest/status?run_id=
GET  /api/backtest/runs?page=1&page_size=20&status=
GET  /api/backtest/equity?run_id=&from=&to=&limit=5000
GET  /api/backtest/trades?run_id=&from=&to=&limit=1000
GET  /api/backtest/metrics?run_id=
GET  /api/backtest/trace?run_id=&limit=100
GET  /api/backtest/decisions?run_id=&limit=100
GET  /api/backtest/export?run_id=&format=csv|json
GET  /api/backtest/klines?symbol=&from=&to=&timeframe=
```

**分页与限制：**
- `runs` 默认 page_size=20，最大 100
- `equity` / `trades` / `decisions` / `trace` 默认 limit=100，最大 5000
- `export` 超过 10k 条时改为异步生成，返回下载链接

**前端约束：**
- 回测结果独立页面，不混入现有 Dashboard 关键路径
- 使用现有 `market` 数据获取，不新增付费数据源
- 不展示任何 prompt 自动演化控件

## 4. 资源护栏与并发约束

- **单用户并发限制**：每个用户同时最多 2 个 running 回测；超出时返回 `429 Too Many Requests`。
- **单回测规模限制**：
  - 最多 20 个 symbol
  - 最大时间跨度 90 天
  - 最小时间框 1 分钟
  - 最多保存 50 万条 `BacktestEquityPoint`
- **进程模型**：回测运行在 HTTP 服务的 in-process goroutine 中，由 `RunManager` 统一管理生命周期。runner panic 由 manager 捕获并标记 run 状态为 `failed`。
- **超时**：运行超过 30 分钟的回测自动标记为 `failed`。
- **数据保留**：已完成的回测结果默认保留 30 天，由 retention 任务清理。

## 5. 验证策略

- P3.1：
  - 每个核心模块单测
  - `backtest/engine_test.go`：跑一轮固定输入（symbol、K 线、固定决策）的回测，断言最终 balance/equity/trades/metrics
  - `go test ./backtest/`（不联网）
- P3.2：
  - GORM 内存 SQLite 迁移 + CRUD 单测
  - 验证 `BacktestRun` 快照字段 round-trip 一致
- P3.3：
  - 适配层探针测试先跑通
  - 用 fake AI client 返回固定 decision，跑完整回测并断言结果
  - 验证 `kernel.GetFullDecisionWithStrategy` 空 variant 行为与预期一致
- P3.4：
  - API handler 单测（start→status→metrics→export）
  - 前端构建 `tsc && vite build`
- 全局：
  - 每阶段单独 commit
  - 全程 `go build ./...` + `go test ./...` + `git diff --check`

## 6. 与实盘对齐的验证目标

- 在回测完成后，至少保留一种可复现的对齐手段：
  - 使用同一组 strategy/model config + 同一时间段，先跑回测，再跑 paper trading；
  - 定义允许偏差阈值：总收益率差异 < 5%，最大回撤差异 < 3%，交易次数差异 < 10%；
  - 若偏差持续超过阈值，回退到 P3.3 检查 execution model 与 kernel 适配层。
- 短期内无法做 paper trading 对齐时，先维护一组 **参考回测基线**（known strategy + known data），作为回归测试，任何改动导致基线变化必须人工 review。

## 7. 全局约束（复用）

- 仅在 `dev-plus` 实施；不覆盖 `origin/dev` 现有功能
- 不读取/提交 `.env`、API key、私钥
- 保留 REST fallback，不新增付费数据源
- 不让 LLM 自动生成或自动替换实盘 prompt
- 每个新增行为先写可失败测试，再写生产代码
