# 无人值守合约交易运维手册

适用：Binance USDⓈ-M 与 OKX USDT 线性永续，单向逐仓，最多 3x，专用子账户，单实例。

**自动交易默认关闭。** 本手册中的每一步都是显式操作；不存在"部署即开始交易"的路径。

## 1. 上线前置

1. 专用子账户：不得与手工交易、网格或旧 AutoTrader 共用。账户上存在任何人工持仓都会让主动启动拒绝接管（`external position not owned by this runtime`）。
2. 绑定物理账户：`POST /api/managed-accounts/bind` 提交 `exchange_id`、`venue_account_id`（交易所原生 UID）、`environment`、`confirm_dedicated_account`。
   - 绑定只在服务端通过签名只读查询核验后才置 `Enabled`；核验失败、UID 为空、venue 不支持时一律 `Enabled=false`，且没有"手工批准"绕过。
   - 凭据、testnet 标志或物理账户变更会**重建**绑定（新 ID、重新核验）；环境在 sandbox/live 之间翻转会移动同一行并重新核验。
3. 确认单实例：进程在 `data/` 下持有 `nofx-execution.lock`（SQLite）或 Postgres advisory lock。第二个实例启动即失败，不会双写订单。

## 2. 策略与预算

- 通过 `PUT /api/automation/:accountKey/policy` 写入 `risk.Policy`。`enabled` 默认 false。
- 服务端会强制：单笔风险 ≤ 50bps、账户/组合在险 ≤ 100bps、日亏 ≤ 200bps、回撤 ≤ 500bps、保证金 ≤ 3000bps、总名义 ≤ 15000bps、方向名义 ≤ 10000bps、杠杆 ≤ 3、最小盈亏比 ≥ 2、资金费预留 10bps。
- 已有策略只能**收紧**（`tightenOnly`），放宽需要新建策略对象。
- 版本 CAS：请求里的 `version` 必须是当前版本（或用当前内容重新推导出的版本），否则 409。

## 3. 启动、暂停、退出、恢复

| 动作 | 请求 | 说明 |
| --- | --- | --- |
| 启动 | `POST /api/automation/:accountKey/start` `{"environment","policy_version","confirm":"START AUTOMATION"}` | 环境必须等于绑定环境；版本必须等于当前版本；存在熔断闩锁时拒绝。启动会先做一次完整对账，不重发任何历史入场。 |
| 暂停 | `POST /api/automation/:accountKey/pause` `{"reason"}` | 只停新入场，**不动**已挂的止损/止盈。 |
| 退出 | `POST /api/automation/:accountKey/flatten` `{"reason"}` | 走控制器的减仓状态机：撤未成交入场 → reduce-only 市价平仓 → 回读确认 → 撤保护 → 完成。未知回执保持冻结，不重发。 |
| 复位 | `POST /api/automation/:accountKey/reset` `{"latch_version","reason"}` | 必须带当前闩锁版本；有未释放预留（未平仓/未终结订单）时拒绝；峰值权益永不降低，所以回撤事故复位后可能立刻重新熔断。 |
| 状态 | `GET /api/automation/:accountKey/status` | 见下。**HTTP 200 不代表可以入场。** |

`status` 字段含义：

- `state`：`running` / `frozen` / `paused`；`reason` 给出冻结或熔断原因。
- `protection_state`：来自最近一个 run 的保护判定（`pending` / `protected` / `exit_required` / `unknown`）。
- `ledger_complete`：当日权益账本是否完整。不完整时新的入场会被预算拒绝。
- `reserved_risk`：该账户未释放的预留风险额度。
- `last_rest` / `last_ws`：最近一次 REST 对账与私有事件时间。事件通道不可用时退回每 10s REST 补偿，不等于"账户不可读"。

## 4. 故障处置

| 症状 | 含义 | 处置 |
| --- | --- | --- |
| `state=frozen`，reason 含 `external position` | 交易所上存在本运行时没有持有的持仓 | 人工确认该仓位来源；平掉或建对应 run 后再启动。**不要**直接 reset。 |
| `state=frozen`，reason 含 `halted with an unresolved exit` | 退出回执未知 | 用原始 client ID 查询成交；确认后由控制器推进。禁止重发退出。 |
| `state=paused`，reason 含 `daily loss` | 当日亏损触阈 | 复盘后 `reset`（带闩锁版本），并检查策略参数。 |
| `state=paused`，reason 含 `drawdown` | 峰值回撤触阈 | 复位只对日亏损有效；回撤需要人工评估是否重建账户基准。 |
| `protection_state=exit_required` | 保护缺失或与计划不符 | 立即 `flatten`；这是唯一被允许的自动减仓入口。 |
| `ledger_complete=false` | 当日权益账本有未补页的窗口 | 保持只读，等待 REST 补偿补齐；不要手工改账本。若冻结原因含 `did not finish within one reconcile`，说明单次对账超过 200 页（约 20 万条 Binance income 或 2 万条 OKX 账单），REST 补偿永远追不上：先确认交易所账单接口是否异常，再按日志里的时间窗口人工补账，**不要**直接 reset。 |
| `last_ws` 长期不更新 | 私有事件通道掉线 | 正常：REST 每 10s 补偿仍在跑；检查日志中 `PrivateEvents` 能力与重连告警。 |

## 5. 凭据轮换

1. 在交易所创建新 API Key，权限保持"合约交易 + 只读"，**不加提现**。
2. 通过现有的交易所配置更新接口写入新凭据。服务端会同步刷新绑定：凭据变化会重建绑定并要求重新核验。
3. 核验通过后绑定恢复 `Enabled`；此前该账户视为未绑定，任何自动入场都会被拒绝。
4. 在交易所删除旧 Key。
5. 检查 `status`：`state` 应为 `running` 或按需 `paused`，`ledger_complete` 应恢复为 true。

## 6. 告警

- 告警先写入 `managed_alert_outbox` 再投递，重试 1s / 5s / 30s，之后每 60s。投递失败不阻塞保护，也不解除熔断。
- 目标只能是管理员配置的单一 https webhook；非 https、localhost、私网/链路本地、解析后指向私网的地址、以及重定向一律拒绝。连接时再次校验实际 IP。
- 消息在入队和发送时各脱敏一次（api_key / secret / passphrase / signature / authorization / bearer / jwt）。

## 7. 不得声称的事

- sandbox 完整周期（signal → entry → SL/TP → exit）尚未验证前，不得把"模型可以自动下单"当作上线资格。
- 交易所侧未取证的事实：OKX `acctLv=2` 的 `imr` 字段、OKX `amend-algos`、Binance 物理账户 UID。这些目前只有 HTTP fixture 证据。

## 8. 成本、资金费与清算口径

### 8.1 已计入的成本

`backtest/managed_costs.go` 的 `CalculateManagedCosts` 只接受一个已平仓回合，用 `math/big.Rat` 精确计价，不经过 float64，也不按两位小数取整。

- 手续费：开仓与平仓两条腿都按名义价值 `数量 × (开仓价 + 平仓价)` 计收，费率为预算中的 `FeeBPS`。当前两个 venue fixture 公布的 taker 费率均为 5bps。
- 滑点：同样按双边名义价值乘 `SlippageBPS`。用真实成交价回放时该值应为 0，因为成交价本身已经包含滑点；再计一次会重复收费。
- 资金费：只累加交易所**已结算**的事件（`AccountEvent.Kind == "funding"`），按交易所自己的时间戳与事件 ID 处理，不假设固定 8 小时周期。同一事件 ID 重复出现只计一次。策略里的「资金费预留 10bps」是计划额度，不是已发生成本，两者不可混用。
- 净额：`净额 = 毛盈亏 - 手续费 - 滑点 + 资金费`（资金费事件为负表示支付）。

以下任一情况会把结果的 `Complete` 置为 false，且**不会**被强行换算成 USDT：

- 资金费事件的币种不是 `USDT`（包括币种为空、未知）；
- 事件类型不是 `funding`（例如账单缺口 `funding_gap`）；
- 事件缺少交易所事件 ID（无法证明唯一性，重放时可能重复计数）。

此时报告必须保留「不完整」这一事实，由人工或后续补账处理，不能把差额抹平。

### 8.2 回放与已知差异

`trading/certification/cost_replay_test.go` 用 Task 1–2 的交易所 fixture 跑一次 signal → entry → exit 的确认成交，把同一份成交账单同时交给计价函数与 venue 侧权益变动，逐位比较差值。fixture 只上报成交价、成交量和公布的 taker 费率，不提供钱包余额，因此 venue 侧权益变动由这些已确认记录聚合得到。任何非零差异都会被明确报告（`t.Logf` + 失败断言），不会被悄悄调整到相等。

样本覆盖有限，当前只有：Binance USDⓈ-M 与 OKX USDT 线性永续、单向逐仓、单次往返、5bps taker、成交价回放（滑点计 0）、单个资金费事件。未覆盖：部分成交的多次聚合、maker 返佣、分层费率、跨日资金费、非 USDT 结算。

### 8.3 尚未建模的项目

- 清算罚金与强平手续费、保险基金、ADL；
- 资金费以外的利息/借币成本；
- 分层费率、maker 返佣、VIP 等级变化；
- 非 USDT 资金费或保证金的换汇成本；
- 组合保证金/跨资产抵押带来的保证金抵扣；
- 部分成交、订单排队与撤单延迟对实际成本的影响。

本任务**没有**实现完整的组合保证金模拟器，也不声称能复现交易所的保证金引擎。计价只覆盖上述已列明的显式成本项。

### 8.4 清算价格：venue 字段与旧模型

- 托管链路中的 `execution.ManagedPosition.Liquidation` 是交易所仓位接口**上报的**清算价，属于 venue 事实，应原样展示。
- 旧的 `backtest.Account` 使用零维持保证金的简化模型 `1/leverage`（多仓 `entry×(1-1/杠杆)`，空仓 `entry×(1+1/杠杆)`）。它只服务于旧回测，**不是**交易所清算价，不能用于托管交易的报告或风控判断。

两者不可互相替代：不要用旧模型的数字回答「这笔仓位离清算还有多远」，也不要用 venue 的清算价去校验旧回测的 golden 结果。
