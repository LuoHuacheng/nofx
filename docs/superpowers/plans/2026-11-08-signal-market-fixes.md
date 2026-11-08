# 托管信号与行情故障修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. 不派发实现子代理。

**Goal:** 修复真实模拟运行中行情瞬时失败、模型缺少执行约束、信号丢失标的导致的持续拒绝与错误归因；保留原风险政策。

**Architecture:** 复用现有 ModelSource、core.Compile、ManagedAccountLimits 和行情客户端。服务端选择的 symbol 随 Signal 传到编译器，不让模型提供执行权限；向模型提供盘口、价格步长和与服务器一致的净成本公式。仅为公开行情 GET 添加一次有界瞬时故障重试。

**Tech Stack:** Go、net/http、现有 GORM/SQLite、Go testing。

**Spec:** ../specs/2026-11-06-unattended-futures-design.md；用户本轮“解决已有的问题”。

## Global Constraints

- 保留 BTCUSDT/ETHUSDT、1x、单笔10bps、账户/组合20bps、日亏50bps、回撤100bps、净RR≥2、funding10bps。
- 资金、杠杆、仓位继续由服务端计算；不通过改模型价格、抬远目标或降低阈值取得成交。
- 5秒行情/账户时效、30秒生成信号有效期、人工暂停和风险熔断继续生效。
- 不新增依赖，不提交、推送、操作实盘；保留既有工作区改动。

## Read-only Evidence

修复前实际 deepseek-v4.1-flash 候选：BTC 入场75760、止损75650、目标75980。毛收益220，但服务端双边手续费/滑点及资金费预留合计303.37，因此被拒绝；原提示确认未包含 fee_bps 或 min_reward_risk。只读诊断没有提交订单，收据 `signal_boundary_diagnostic_20261108.json` 保存于私有接入目录。

切换前已通过版本化 API 禁用新入场，保存一致数据库备份 `data-before-signal-market-fix-20261108.db`。实际 SDK 回读：sandbox、one_way、身份匹配、权益5004.48604296 USDT、持仓0、全部普通/保护挂单0；历史策略运行数0。

## Review Focus

- 非白名单或缺失标的必须拒绝，不能按绝对价格猜测另一标的。
- 候选拒绝必须保留对应标的原始错误，不能被另一个盘口的错误覆盖。
- 模型只在图表支持且净收益满足成本约束时提议交易，否则返回空 ideas。
- 行情重试最多2次请求，只适用于GET超时/临时传输故障/429/5xx；取消、4xx、错误JSON和POST不重试。
- 模拟验证既可接受健康 HOLD，也可接受有效的托管计划；不强制下单。

### Task 1: 信号标的与模型上下文

**Files:** trading/automation/source.go、runtime.go、source_test.go、activation_test.go，以及构造 Signal 的既有测试。

- [x] 补提示包含盘口、tick、fee5bps/slippage10bps/funding政策值、minRR政策值与净收益公式的回归，先看到失败。
- [x] 补信号保留服务端标的、编译只读该标的、缺失/非白名单标的拒绝的回归，先看到失败。
- [x] Signal 增加服务端 Symbol；compileIdea 接受完整 Signal，只编译已授权标的。更新所有内部调用与 fixture。
- [x] signalPrompt 接受 Policy 与 Snapshot，复用 live.ManagedFeeBPS/ManagedSlippageBPS，说明净成本及健康空信号语义；不扩大模型返回字段白名单。
- [x] 相关 automation 测试通过（automation 7.155s、market 1.134s；`/tmp/nofx-signal-market-green.log`）。

### Task 2: 行情瞬时故障

**Files:** planning/market/client.go、request_retry_test.go。

- [x] 本地 HTTP fixture 复现第一次503/429/超时、第二次成功；持续失败只调用2次。
- [x] 验证上下文取消、400、错误JSON、POST不能重试。
- [x] 用标准库实现一次有界 GET 重试，保留请求超时、JSON读取上限和错误传播。
- [x] market 包通过。

### Task 3: 本地模拟切换与验收

- [x] 运行相关 market/core/automation/risk/live/execution、相关 API 测试和 go vet，审查 diff。7个包466个测试条目通过，2项显式在线 smoke/lifecycle 测试跳过；相关API、go vet和构建通过。产物 `/tmp/nofx-signal-market-final-tests.jsonl`、`/tmp/nofx-signal-market-{api,vet,build}.log`。
- [x] 保存一致备份；确认账户/挂单/执行状态后安全切换本地服务，保留监督，不重放旧入场。新服务任务 `b628ff446`，8080；旧服务通过SIGTERM停止且确认锁释放。API恢复同版本政策并POST/start返回running，收据 `automation_enabled_signal_market_fix_20261108.json`；历史策略仍加载0。
- [x] 实际 custom AI / deepseek-v4.1-flash 的最终自然周期观察通过，结果 healthy_hold_cycle：2次模型请求、60次状态采样全部running、24次不同REST对账，最大观测权益年龄2.583736s（小于5s）。候选为空，0draft/0run，未强制下单。收据 `automation_observation_cadence_fix_20261108.json`。
- [x] 观察结束后再次核对：health/status200、sandbox/running、reason空、账本完整、政策enabled且仍BTC/ETH最高1x；SDK原生身份匹配、单向、权益/可用5004.48604296 USDT、持仓及全部普通/保护挂单0；旧策略运行0。收据 `automation_final_verified_20261108.json`。完整成交、保护和退出闭环仍未验证；未执行实盘。

### Task 4: 实际观察新增的调度根因

实际SDK显示账户仍空仓；API在模型轮次之间周期性paused，原因是权益时效5s而原轮询10s，模型还在监督协程里同步等待。

- [x] 新增阻塞模型与对账时效回归，原实现分别失败为`pending model blocks account reconciliation`、`reconciliation every 10s exceeds equity freshness 5s`。
- [x] 生产监督使用每账户最多一个异步模型任务；结果回到账户锁内检查取消/安全冻结后才执行；Shutdown等待任务，正常对账每2s推进。保留原Tick同步调用语义用于独立执行/测试，不延长5s有效期。
- [x] automation全包通过，新增晚返回模型不能绕过安全冻结的回归。
- [x] 检查同一用户共享ModelSource/模型客户端并发，已用源级互斥串行化共享源，锁后检查取消，保持游标和模型请求安全。回归先失败`shared model client used concurrently: no trade signal`，修复后与慢模型/晚返回安全冻结/启动用例一起通过`go test -race`（`/tmp/nofx-final-cadence-race.log`）。
- [x] 最终调度代码通过定向`-race`、7包470测试条目（2项在线验收跳过）、相关API、go vet和构建；日志 `/tmp/nofx-final-cadence-{race,tests,api,vet,build}.*`。已保存一致备份`data-before-supervision-cadence-fix-20261108.db`，0执行记录下禁用入场、SIGTERM旧PID9346、确认锁释放后启动`bc00ad5da`；保留旧二进制`nofx-server-before-cadence-fix`。最终自然周期验收及账户回读已通过，见Task 3；服务保留自动模拟运行，未将健康HOLD认定为成交闭环。
