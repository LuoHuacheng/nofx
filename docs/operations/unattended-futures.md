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
