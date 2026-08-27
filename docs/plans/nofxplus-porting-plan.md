# NOFXPlus 功能移植实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 `dev-plus` 分支上，以当前 NOFX 架构为基准，按风险和收益优先级吸收 `https://github.com/jeffeehsiung/nofxplus` 中有实际价值且可验证的功能。

**Architecture:** 保留当前项目的 `kernel/`、`market/`、多用户加密配置和多交易所结构，不直接合并 nofxplus 的 `decision/` 包重构。移植功能必须先适配现有接口，再通过现有 REST/数据库/交易生命周期接入。实时数据采用 WebSocket 优先、已有 REST 路径 fallback；所有自动调优能力先保持显式配置或离线使用，不直接自动修改实盘 prompt。

**Tech Stack:** Go 1.27、Gin、GORM、SQLite/PostgreSQL、现有 `market/` 与 `kernel/`、gorilla/websocket、React/Vite 前端。

**Spec:** 本文件中的“评估结论”和“P1 移植范围”。

## Global Constraints

- 仅在 `dev-plus` 分支实施；不覆盖 `origin/dev` 的现有功能。
- 不移除 PostgreSQL、Telegram、Lighter 或当前已有交易所支持。
- 不读取或提交 `.env`、API key、私钥等凭据。
- 新增交易路径必须保留当前 REST fallback。
- 不让 LLM 自动生成或自动替换实盘 prompt；自动调优只能先离线或人工确认。
- 每个新增行为先写可失败测试，再写生产代码；验证至少包含 `gofmt`、`go build ./...`、`go test ./...`。
- 不原样复制 nofxplus 的硬编码 `nofxos.ai` URL、公共 auth key 或 nofxplus 品牌脚本。

## 当前基线

- 当前分支：`dev-plus`
- 已移植 commits：
  - `b53748f0`：`config/features.go`、`provider/coinglass/`
  - `efae6097`：阈值自适应、阈值持久化、无 gonum Beta 辅助
  - `a26fdbf`：Binance/Bybit WebSocket 客户端
  - `4fd3100`：WS K 线缓存优先、CoinAnk REST fallback
- 当前分支已通过此前的 `go build ./...` 和 `go test ./...`。
- nofxplus remote：`nofxplus/dev`
- 双方基线存在大量分叉，不能使用整分支 merge 作为移植方式。

## 已移植功能

### 低风险

- `config/features.go`：实验性和自适应行为开关，支持环境变量。
- `provider/coinglass/`：CoinGlass V4 HTTP 客户端及测试。
- `config/constants.go`：集中常量。
- Binance / Bybit WebSocket 客户端和 WebSocketManager。
- WS K 线缓存：缓存满足数量时优先使用 WS，否则继续使用原 CoinAnk REST。

### 中低风险

- `kernel/threshold_calibrator.go`：ROC 和贝叶斯阈值更新。
- `kernel/threshold_persistence.go`：阈值 JSON 持久化。
- `kernel/beta_dist.go`：替代 gonum 的 Gamma/Beta 采样与近似分位数。

## 未移植功能及作用

### P1：直接收益，优先适配

1. **Provider 抽象与 DIY 数据源**
   - 来源：`provider/data_provider.go`、`provider/diy_quant.go`、`provider/binance/`。
   - 作用：统一 K 线、OI、资金流、价格和情绪数据接口；当 CoinAnk 或其他服务不可用时，用 Binance 公共接口计算净流量、OI 变化、taker 买卖比例和市场情绪。
   - 当前差距：当前有多个独立 provider，但没有统一的数据 provider 合约；CoinAnk 是主要 K 线来源，单一数据源故障会影响分析。
   - 风险：低到中。要避免改变现有默认结果，先做显式 fallback 和只读接入。

2. **交易所真实成交价校准 PnL**
   - 来源：nofxplus commit `ba9c9c6a` 及相关 `trader/auto_trader.go`、`store/position.go` 改动。
   - 作用：以交易所实际成交价而不是本地估算价重新计算 PnL，修正滑点、部分成交和交易所撮合差异导致的收益统计偏差。
   - 当前差距：当前已有仓位和订单同步，但需要确认实际成交价是否贯穿本地 position/equity 统计。
   - 风险：中。不能覆盖原始审计数据，只能追加交易所校准字段或在统计读取层使用已确认的成交记录。

3. **自适应扫描间隔**
   - 来源：nofxplus commit `59ccc09b`。
   - 作用：低波动时降低 AI 扫描频率，高波动时提高响应速度，减少无效调用和成本。
   - 当前差距：当前扫描周期主要由固定配置控制。
   - 风险：中。默认必须保持现有扫描周期；增加上下限、冷却时间和异常回退。

4. **订单同步间隔可配置**
   - 来源：nofxplus commit `c8490e04`。
   - 作用：按交易所和运行状态调整订单/仓位同步频率，降低限流和 REST 压力。
   - 当前差距：需要确认各 trader sync loop 的固定 ticker 位置。
   - 风险：低到中。不能低于交易所安全频率，不能关闭关键订单状态同步。

### P2：数据质量和运行稳定性

5. **市场微结构分析**
   - 来源：`market/microstructure.go`。
   - 作用：计算 VWAP、Bid/Ask spread、盘口不平衡、买卖深度、滑点和市场冲击。
   - 当前差距：当前有 K 线、OI、资金费率，没有统一的盘口微结构模型。
   - 风险：中。需要盘口快照/增量合并，先只读展示和诊断，不直接触发下单。

6. **Order Book Monitor**
   - 来源：`market/order_book_monitor.go`。
   - 作用：检测盘口失衡、深度下降、spread 扩大、成交量异常和短时价格变化。
   - 当前差距：当前主要按固定周期扫描，不支持盘口异常触发告警。
   - 风险：中。先产生告警和日志，不直接控制交易。

7. **生产级 Kline WebSocket Manager**
   - 来源：`market/kline_websocket_manager.go`。
   - 作用：多连接池、每连接 stream 限制、活跃币种订阅、stale 数据检查、健康检查、handler 广播和 REST fallback。
   - 当前差距：当前是单 client + lazy subscription + cache，缺少大规模订阅管理。
   - 风险：中。当前实现适合少量币种；只有订阅量达到实际瓶颈后再替换或扩展。

8. **OKX 订单 WebSocket**
   - 来源：`market/okx_order_websocket.go`。
   - 作用：通过 WebSocket 接收 OKX 订单和成交事件，减少 REST 轮询。
   - 风险：中。仅在 OKX 是实际主力交易所时优先。

9. **CoinAnk 增强接口**
   - 来源：`provider/coinank/` 下的 instrument、清算、OI、净仓位、多空比和 WS 文件。
   - 作用：补充市场结构和衍生品数据，减少对单一 K 线接口的依赖。
   - 风险：中。当前已有 CoinAnk 封装，按数据缺口逐个移植，不能覆盖现有实现。

### P3：需要整体子系统设计

10. **完整回测系统**
    - 来源：`backtest/account.go`、`datafeed.go`、`runner.go`、`equity.go`、`metrics.go`、`storage.go`、`manager.go`、`registry.go` 等。
    - 作用：历史 K 线回放、模拟账户、权益曲线、收益/回撤统计、回测任务管理和结果持久化。
    - 当前差距：当前完全没有回测能力。
    - 风险：高。需要 API、数据表、策略执行隔离和前端页面，不能拆几个文件直接接入实盘。

11. **交易反馈分析**
    - 来源：`backtest/feedback.go`。
    - 作用：从历史交易归纳成功/失败模式、市场环境和交易质量。
    - 风险：中到高。规则统计可先做；LLM 反馈不能自动注入实盘 prompt。

12. **Prompt Variant / Prompt Lab**
    - 来源：`backtest/prompt_optimizer.go`、`web/src/components/PromptLabPage.tsx`、`PromptVariantLab.tsx`、`LiveTraderPromptLab.tsx`。
    - 作用：prompt 版本管理、A/B 测试、表现评分和候选版本生成。
    - 风险：高。必须采用“候选 → 回测 → 人工审批 → 激活”的流程。

13. **交易失败归因**
    - 来源：`decision/trade_failure.go`。
    - 作用：识别追单、假突破、流动性枯竭、止损过紧、regime 不匹配等失败原因。
    - 当前差距：`RecentOrder` 缺少 20+ 微结构字段，当前移植会读取零值。
    - 风险：中。必须先完成微结构数据采集和数据模型扩展。

14. **decision 包重构和双语 schema**
    - 来源：`decision/engine.go`、`formatter.go`、`schema.go`。
    - 作用：将当前 kernel 决策代码拆分，并增加交易数据字典、格式化和校验。
    - 评价：主要是架构重组，不是独立业务能力；直接移植会产生大量签名和调用链冲突，暂不移植。

### 不建议移植

- `config/api_constants.go`：硬编码 nofxos.ai 域名和公共 auth key。
- nofxplus `setup.sh` / `setup.bat` / `SETUP_GUIDE.md`：品牌和环境假设不同，且存在全局 API key 配置倾向。
- 移除 Lighter、PostgreSQL、Telegram 的改动：与当前功能目标相反。
- nofxplus 中多次 rollback、debug、临时验证和重复 README commit：不具备可移植价值。

## P1 实施任务

### Task 1：Provider 抽象与 DIY fallback

**Files:**
- Create or adapt: `provider/data_provider.go`
- Create or adapt: `provider/diy_quant.go`
- Create or adapt: `provider/binance/kline.go`
- Modify: `market/data_klines.go`
- Test: `provider/data_provider_test.go`, `provider/diy_quant_test.go`, `provider/binance/kline_test.go`

**Interface:**

```go
type DataProvider interface {
    GetKlines(ctx context.Context, symbol, interval string, limit int) ([]market.Kline, error)
    GetPrice(ctx context.Context, symbol string) (float64, error)
}

func GetWithProviderFallback(ctx context.Context, primary, fallback DataProvider, symbol, interval string, limit int) ([]market.Kline, error)
```

**Required behavior:**

- 当前 CoinAnk 和已接入的 WS 优先路径继续是默认路径。
- fallback 只在主数据源返回错误或空结果时触发。
- DIY provider 仅使用公共 Binance 数据，不读取凭据。
- 不把新的 DIY 情绪值直接注入实盘决策，先作为可选数据字段。
- 测试主 provider 成功不调用 fallback、主 provider 失败调用 fallback、两者都失败返回原始错误上下文。

### Task 2：真实成交价校准 PnL

**Files:**
- Inspect and modify: `trader/auto_trader.go`
- Inspect and modify: `store/position.go`, `store/order.go`, `store/equity.go`
- Test: `trader/auto_trader_pnl_test.go` or the nearest existing position accounting test file

**Required behavior:**

- 优先读取交易所同步的实际成交价。
- 部分成交必须按成交明细或加权平均价计算。
- 交易所没有新成交数据时保留当前本地计算结果。
- 原始订单和校准后的结果都可审计，不能覆盖历史原值。
- 测试本地价与交易所价不一致、部分成交、没有交易所数据三种情况。

### Task 3：自适应扫描间隔

**Files:**
- Inspect and modify: `trader/auto_trader_loop.go` and the owning scan configuration type
- Modify: `config/config.go` or the narrowest existing runtime config file
- Test: `trader/auto_trader_loop_test.go` or a new focused interval test

**Required behavior:**

- 保留固定 interval 作为默认和 fallback。
- 根据明确的波动/市场状态指标计算下一次 interval。
- 强制 `min_interval <= next_interval <= max_interval`。
- 指标异常、数据缺失或计算失败时返回固定 interval。
- 增加冷却，避免 interval 在相邻周期剧烈抖动。

### Task 4：订单同步间隔配置

**Files:**
- Inspect and modify: the existing trader order/position sync loop
- Modify: `config/config.go` or the narrowest existing runtime config file
- Test: the existing sync-loop test file or a new focused configuration test

**Required behavior:**

- 默认保持当前同步间隔。
- 支持环境变量覆盖，但解析失败、零值和负值必须回退默认值。
- 不影响订单状态事件的即时处理。
- 交易所最低安全间隔和最大允许间隔必须有边界。

### Task 5：P1 验证和提交

**Commands:**

```bash
gofmt -w <changed-go-files>
go build ./...
go test ./...
git diff --check
git status --short
```

**Commit rule:** 每个 Task 完成后单独提交，提交信息使用中文 emoji Conventional Commit；不得提交 `.env`、凭据或无关格式化文件。

## 后续进入条件

- Task 1-4 全部通过完整 Go 构建和测试后，才评估 P2。
- P2 的微结构分析必须先有真实盘口采集和只读测试。
- P3 回测系统必须先形成独立设计和数据库/API 方案，不能在当前 P1 commit 中混入。
- Prompt optimizer 和 LLM feedback 在有 dry-run、人工审批、版本回滚前不得接入实盘。
