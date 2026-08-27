# P3 回测子系统设计与评估

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 `dev-plus` 上以当前 `kernel/` 架构为基准，分阶段落地一个可独立验证的历史回测子系统，不引入 nofxplus 的 `decision` 包双份维护。

**Architecture:** 回测核心（模拟账户、数据回放、权益、指标）完全自包含；AI 决策通过薄适配层复用当前 `kernel` 的 `StrategyEngine` / `GetFullDecisionWithStrategy`，而不是复制 `nofx/decision`。存储使用现有 GORM `store` 新增一张回测运行表，API 挂到现有 `/api` 分组，前端独立页面。LLM 自动反馈 / prompt 自进化排除在本阶段之外。

**Tech Stack:** Go 1.27、GORM、当前 `market/` 数据通道（WS 优先 + REST fallback）、Gin、React/Vite。

**Spec:** 本文件即设计说明；实施顺序见「分阶段方案」。

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
| P3.3 kernel 适配层 | 把 runner 的 `decision.*` 改指 `kernel.*`，处理 3 个签名差异 | 中高 | 2-3 天 |
| P3.4 API + 前端 | `/api/backtest/*` + 独立页面 | 中 | 2-3 天 |
| P3.5 后续（不做） | feedback/prompt_optimizer/factor_optimizer/compliance/calibration | 高 | 数周 |

## 2. 分阶段方案

### P3.1：自包含回测核心（独立可测）

**Files:**
- Create: `backtest/account.go`, `backtest/equity.go`, `backtest/metrics.go`, `backtest/lock.go`, `backtest/registry.go`, `backtest/retention.go`, `backtest/types.go`, `backtest/constants.go`, `backtest/utils.go`, `backtest/datafeed.go`
- Test: 每个文件对应 `_test.go`

**Interfaces:**
- `BacktestAccount`：OpenLong/OpenShort/CloseLong/CloseShort、余额、保证金、仓位、手续费、强平判定
- `DataFeed`：按时间顺序迭代 K 线、OIData、FundingRate
- `EquityCurve` / `Metrics`：收益率、胜率、Sharpe、最大回撤、ProfitFactor
- `RunRegistry`：runID 的创建/恢复/清理
- `RunLock`：并发防重入

**不做：** AI 调用、决策引擎、store 持久化（下一步）、任何 LLM 自动反馈。

### P3.2：存储层（按当前 schema 设计）

**Files:**
- Create: `store/backtest.go`（重写，不照搬 nofxplus）
- Modify: `store/store.go`（注册 BacktestStore）

**Interfaces:**
- `RunMetadata` / `EquityPoint` / `TradeEvent` / `RunIndexEntry` 四张表
- `SaveCheckpoint` / `AppendEquityPoint` / `AppendTradeEvent` / `SaveMetrics` / `UpdateProgress` / `ListRunIDs` / `DeleteRun`

**约束：**
- 用 GORM AutoMigrate 而非手写 `ALTER TABLE`
- 兼容 SQLite + PostgreSQL
- 不引入 nofxplus 的 `trade_outcome.go` 字段扩展（那是给 threshold_calibrator 用的，已在 kernel 完成）

### P3.3：kernel 适配层 + runner

**Files:**
- Create: `backtest/runner.go`, `backtest/manager.go`, `backtest/ai_client.go`, `backtest/aicache.go`
- Modify: 无（不动 kernel）

**Interfaces（适配后，全部基于当前 kernel）：**
- `kernel.NewStrategyEngine(strategyConfig, claw402Key)` — 回测时 claw402Key 传空
- `kernel.GetFullDecisionWithStrategy(ctx, client, engine, "")` — variant 传空
- `kernel.Context` 直接构建；不存在的字段（VergexDataMap 等）保持 nil 或按回测 symbol 预填 MarketDataMap 后走 `fetchMarketDataWithStrategy` 的既有跳过逻辑

**关键改动：**
- runner 中所有 `decision.X` → `kernel.X`
- `GetFullDecisionWithStrategy` 第三个参数 variant 传 `""`
- `NewStrategyEngine` 补 `claw402WalletKey` 参数
- AI client 复用当前 `mcp` 注册表（含 claw402/deepseek 等）

**不做：** `feedback.go` 的 LLM 反馈注入、`prompt_optimizer.go` 自动重写。

### P3.4：API + 前端

**Files:**
- Create: `api/backtest.go`, `web/src/components/BacktestPage.tsx`（或复用现有页面结构）
- Modify: `api/server.go`（注册路由）、`api/route_registry.go`、`web/src/router/AppRoutes.tsx`

**路由（去掉 prompt 相关 4 个）：**
```
POST /api/backtest/start|pause|resume|stop|delete
GET  /api/backtest/status|runs|equity|trades|metrics|trace|decisions|export|klines
```

**前端约束：**
- 回测结果独立页面，不混入现有 Dashboard 关键路径
- 使用现有 `market` 数据获取，不新增付费数据源
- 不展示任何 prompt 自动演化控件

### P3.5：明确不做（本期）

- feedback.go LLM 反馈自动注入
- prompt_optimizer.go prompt 自动重写 / A/B
- factor_optimizer.go 自动因子寻优
- compliance_tracker.go 合规规则自动执行
- 把 backtest 结果自动应用到实盘策略

这些是后续独立设计，需要人工审批 / dry-run / 回滚护栏，不在 P3 内。

## 3. 验证策略

- P3.1：每个核心模块单测 + `go test ./backtest/`（不联网）
- P3.2：GORM 内存 SQLite 迁移 + CRUD 单测
- P3.3：用 `httptest` + 假 AI client 跑一轮最小回测（1 个 symbol、少量 K 线、空决策），断言账户/权益/指标一致
- P3.4：API handler 单测（start→status→metrics），前端构建 `tsc && vite build`
- 每阶段单独 commit；全程 `go build ./...` + `go test ./...` + `git diff --check`

## 4. 全局约束（复用）

- 仅在 `dev-plus` 实施；不覆盖 `origin/dev` 现有功能
- 不读取/提交 `.env`、API key、私钥
- 保留 REST fallback，不新增付费数据源
- 不让 LLM 自动生成或自动替换实盘 prompt
- 每个新增行为先写可失败测试，再写生产代码
