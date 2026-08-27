# 参考回测基线（§6）

> 与实盘 paper-trading 对齐短期无法执行时，本基线作为回归测试锚点：
> **任何改动导致基线数值变化，必须人工 review**（执行模型 / kernel 适配层）。

## 基线 1：固定决策引擎集成测试

- 测试：`backtest/engine_test.go` → `TestEngineFixedDecider`
- 数据：BTCUSDT 5m K 线，100 → 110 单调上行后走平（24 根 bar）
- 决策序列：第 1 次决策 open_long（size=500, lev=2），价格 ≥110 时 close_long
- 费用：taker 0.001（Binance 默认兜底），maker 0.0002
- **期望终值：equity = 1048.90055**（开仓费 0.5、保证金 250、平仓费 0.54945、净利 49.40055）
- 断言：2 笔成交（open+close）、close PnL = 49.40055、TradeCount=1、WinRate=100、总收益 4.890055%

## 基线 2：kernel 适配层端到端

- 测试：`backtest/runner_test.go` → `TestKernelRunnerEndToEnd`
- 与基线 1 相同数据/费用；AI 决策由 fake client 依次返回 open → hold → close
- **期望终值：equity = 1048.90055**（与基线 1 一致，验证 kernel 适配不改变撮合语义）

## 基线 3：AI 决策缓存去重

- 测试：`backtest/runner_test.go` → `TestKernelRunnerCacheDedup`
- 数据：60 根平盘 bar（价格恒 100），fake client 恒返回 hold
- 断言：LLM 调用次数 < bar 数量（kline 窗口饱和后全部缓存命中）

## 变更流程

任何修改触碰以下逻辑时，必须运行并 review 上述基线：

- `backtest/account.go`（费用 / 保证金 / 强平）
- `backtest/engine.go`（撮合 / 决策时机 / SL-TP）
- `backtest/runner.go`（kernel.Context 构建 / 决策转换）
- `market.BuildDataFromKlines`（指标序列）
