# 无人值守合约交易运维手册

适用：Binance USDⓈ-M 与 OKX USDT 线性永续，单向逐仓，最多 3x，专用子账户，单实例。

**自动交易默认关闭。** 本手册中的每一步都是显式操作；不存在"部署即开始交易"的路径。

## 1. 上线前置

1. 专用子账户：不得与手工交易、网格或旧 AutoTrader 共用。账户上存在任何人工持仓都会让主动启动拒绝接管（`external position not owned by this runtime`）。
2. 绑定物理账户：`POST /api/managed-accounts/bindings` 提交 `exchange_id`、`venue_account_id`（Binance 合约余额接口的 `accountAlias`，OKX 的 `uid`）、`environment`、`confirm_dedicated_account`。
   - 绑定只在服务端通过签名只读查询核验后才置 `Enabled`；核验失败、账户标识为空或冲突、venue 不支持时一律 `Enabled=false`，且没有"手工批准"绕过。
   - 凭据、testnet 标志或物理账户变更会**重建**绑定（新 ID、重新核验）；环境在 sandbox/live 之间翻转会移动同一行并重新核验。
3. 确认单实例：进程在 `data/` 下持有 `nofx-execution.lock`（SQLite）或 Postgres advisory lock。第二个实例启动即失败，不会双写订单。

## 2. 策略与预算

- 通过 `PUT /api/automation/:accountKey/policy` 写入 `risk.Policy`。`enabled` 默认 false。
- 服务端会强制：单笔风险 ≤ 50bps、账户/组合在险 ≤ 100bps、日亏 ≤ 200bps、回撤 ≤ 500bps、保证金 ≤ 3000bps、总名义 ≤ 15000bps、方向名义 ≤ 10000bps、杠杆 ≤ 3、最小盈亏比 ≥ 2、资金费预留 10bps。
- 已有策略只能**收紧**（`tightenOnly`），放宽需要新建策略对象。
- 版本 CAS：请求里的 `version` 必须是当前版本（或用当前内容重新推导出的版本），否则 409。
- 信号按 UTC 每根 15 分钟 K 线收盘调度，白名单各币依次评估一次；首次启动只评估最新已收盘周期，不补跑历史周期。每账户/币种的检查点持久化在 `managed_signal_scans`，重启或请求失败不会重复评估同一收盘周期。每账户最多一个模型任务，账户、订单与风控仍约每 2 秒对账。

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
- `last_rest` / `last_ws`：最近一次 REST 对账与私有事件时间。事件通道不可用时退回约每 2s REST 补偿，不等于"账户不可读"。

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
- 交易所侧未取证的事实：OKX `acctLv=2` 的 `imr` 字段、OKX `amend-algos`。这些目前只有 HTTP fixture 证据。Binance 身份采用签名余额接口返回的 `accountAlias`，不把它声称为数字 UID；空值或多个不一致的标识会拒绝绑定。

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

## 9. Sandbox 验收与人工介入

本节只增加验收入口与操作规则，不改变第 1–8 节的任何结论。code-complete、sandbox-accepted、live-enabled 是三个独立状态：本节通过只说明 sandbox 已验收，**不**代表可以开实盘，也不代表策略有盈利能力。

### 9.1 验收入口

入口是一个测试，不是服务端接口：`trading/certification/sandbox_test.go` 的 `TestSandboxManagedLifecycle`。它复用正式托管运行时与正式交易所适配器，不新增任何下单代码。

这条命令的实际流程（每一步都发生在真实 sandbox 账户上）：

1. gate：从 `-sandbox-db` 副本解析绑定与交易所配置，核验 environment/testnet/凭据/endpoint/仓位与保证金模式。
2. 入口主体：`TestSandboxManagedLifecycle` 函数体检查账户快照，账户必须无持仓、无挂单/策略委托，否则在提交任何订单前终止。这项检查属于验收入口本身，不在 gate 的只读检查里。
3. 运行时：写入 `risk.DefaultPolicy` 策略（单笔风险 0.5% 权益、≤3x）、记录两次权益基线，用公开行情编译 `core.Plan`（限价入场、1% 止损、3% 目标），写入草稿，`live.New` 启动 `Controller` 并把 `Interval` 设为负值由测试逐 tick 驱动，再 `Approve`（口令 `APPROVE LIVE`）＋`Execute`。
4. 订单周期：反复 `Controller.StepContext`，直到运行态 `position_open` 且 `protection_state=protected`；随后测试用 `ManagedSnapshot` 直接读交易所，要求 1 个持仓 + 2 张保护单。
5. 部分减仓：测试在交易所用 reduce-only 市价单减半，再驱动运行时，要求保护状态不再为 `protected`（运行时发现持仓与计划成交不一致）。
6. 收尾：`Flatten` 后再读一次交易所，要求持仓为 0、无遗留订单，且该 run 的风险预留为 `released`；任一步失败时测试会先尝试 `Flatten` 再报错，避免把账户留在敞口状态。

**证明**：正式运行时在 sandbox 账户上完成「入场 → 保护单确认 → 部分减仓 → 平仓 → 无残留 → 预留释放」，且每一步的交易所侧状态与运行时判断一致。**不证明**：策略盈利能力、连续 7 天稳定性、实盘行为、Postgres 进程锁分支、网络分区场景。

```bash
# 只读副本 + 独立数据库，绝不指向 data/data.db
mkdir -p "$HOME/nofx-acceptance"
sqlite3 "$NOFX_DB" ".backup '$HOME/nofx-acceptance/binance-$(date +%Y%m%d).db'"

export NOFX_SANDBOX_BINDING_ID='<binding id>'
export NOFX_SANDBOX_DB_PATH="$HOME/nofx-acceptance/binance-$(date +%Y%m%d).db"
# 凭据在库里是密文，验收进程必须持有与运行时相同的密钥
export RSA_PRIVATE_KEY="$RUNTIME_RSA_PRIVATE_KEY"
export DATA_ENCRYPTION_KEY="$RUNTIME_DATA_ENCRYPTION_KEY"

go test ./trading/certification -run '^TestSandboxManagedLifecycle$' -count=1 \
  -args -sandbox-venue=binance \
        -sandbox-binding="$NOFX_SANDBOX_BINDING_ID" \
        -sandbox-db="$NOFX_SANDBOX_DB_PATH"
```

OKX 单独跑一次，把 `-sandbox-venue` 换成 `okx`，并使用 OKX 账户自己的绑定与独立的数据库副本。**不得**两个 venue 共用同一个验收数据库或同一个主账户。

不带这三个 flag 时该测试直接 skip，skip 文案会写明缺什么；其余离线测试矩阵（契约、故障、崩溃恢复、成本回放、gate）**不允许** skip。下列拒绝分两个阶段：本地检查（flag 重复注册、venue、数据库路径、绑定/交易所的 owner 与 environment、配置与凭据）在任何请求之前返回错误；账户自身的判定（快照 `environment`、单向、逐仓，以及账户非空或仍有挂单）必须先完成一次带签名的只读请求才能做出，因此这类拒绝发生在那一次读取之后、任何写请求之前：

- `-sandbox-venue` 只接受 `binance` / `okx`；
- 绑定行在 `-sandbox-db` 中能解析，`environment=sandbox`、`enabled=1`、已核验（`verified_at` 非零）；
- 绑定与交易所配置属于同一 `user_id`，`exchange_type` 等于请求 venue，配置 `enabled=1`、`testnet=1`、凭据完整（OKX 还需 passphrase）；
- 适配器实际钉住的 endpoint 是 sandbox：Binance HTTP = `https://testnet.binancefuture.com`、stream = `wss://stream.binancefuture.com/ws/`；OKX 的 demo 只由 `x-simulated-trading: 1` 头选择，所以以 demo 私有 stream `wss://wspap.okx.com:8443/ws/v5/private?brokerId=9999` 为证；
- 账户自身快照报告 `environment=sandbox`、单向（`one_way` / `net_mode`），且上报的保证金模式为逐仓（平仓空账户不上报保证金模式，gate 接受空值；一旦上报必须是 `isolated`）。

`-sandbox-db` 为空、是目录/非常规文件/不可写，或解析后落在仓库 `data/` 目录内时一律拒绝：绝对路径、`../../` 穿越、以及指向 `data/data.db` 的符号链接都算，错误信息会写明 runtime data directory。仓库根由测试文件向上查找 `go.mod` 得到，不依赖 go test 的工作目录；候选路径先做绝对化，存在时再做符号链接解析。这仍是路径判断：把生产库复制到别处后无法按内容识别，所以必须复制到独立目录。

离线对照测试逐项证明上述拒绝都发生在任何 POST 之前：`TestSandboxGateRejectsLive` 用本地 venue 模拟器跑两个 venue 的 gate 正/反用例（入场/保护/退出/撤单计数全为 0，且反例必须给出对应理由），`TestSandboxRejectsRuntimeDatabase` 覆盖绝对路径、相对穿越、符号链接、硬链接与大小写变体五种运行库写法（后两者依赖文件系统能力，unsupported 时记日志跳过），`TestSandboxRequireEndpointsRejectsLive` 直接证明 endpoint 校验会拒绝指向实盘主机的适配器，`TestSandboxLifecycleAssertions` 证明完整周期结束时仓位为 0、无遗留订单、风险预留已释放。这些离线用例两个 venue 都走真实适配器，不使用替身。

### 9.2 绑定身份核验

验收前逐行核对，不接受"看起来对"：

```bash
sqlite3 "$NOFX_SANDBOX_DB_PATH" \
  "SELECT id,user_id,exchange_id,venue,environment,venue_account_id,verified_at,enabled
     FROM managed_account_bindings WHERE id='$NOFX_SANDBOX_BINDING_ID';"
sqlite3 "$NOFX_SANDBOX_DB_PATH" \
  "SELECT id,user_id,exchange_type,testnet,enabled FROM exchanges WHERE id='<exchange_id>';"
```

判定规则：绑定必须恰好一行（表名 `managed_account_bindings`，见 `trader/execution/binding.go:33` 的 `TableName`）；`venue_account_id` 必须等于签名只读接口返回的原生账户标识（Binance 为 `/fapi/v3/balance` 各资产一致的 `accountAlias`，OKX 为账户配置的 `uid`；两者都不等于 API Key）。核验只绑定该交易环境中的账户，不能用网页显示的数字 UID 替代 Binance `accountAlias`；`exchange_id` 的 `user_id` 必须与绑定一致；`testnet` 必须为 1。任一项不匹配就停止验收，不要"先跑起来再说"。

### 9.3 模式与权限

- 仓位模式必须是单向（OKX `net_mode`，Binance `dualSidePosition=false`）；逐仓；杠杆按策略上限（≤3x）。
- API Key 权限：只勾选合约交易与只读，**不勾提现**，并绑定服务器出口 IP 白名单。
- 验收账户必须没有持仓、没有挂单/策略委托；这项检查由验收入口主体（`TestSandboxManagedLifecycle` 函数体）在提交任何订单前对账户快照完成，发现任一项即终止。它不在 gate 的只读检查里，也不适用于其他命令。
- 保护单确认延迟（从入场成交到 SL/TP 在交易所可见）按模板逐笔记录；超过 5s 保护宽限（`protectionGrace`）会被判为未保护，验收不通过。

### 9.4 数据库备份与恢复

- 备份：`sqlite3 "$NOFX_DB" ".backup '<path>'"`；`.backup` 在 WAL 下也是一致快照，不要在运行中用 `cp` 抓主库文件。
- 恢复：先停进程（确认没有第二个实例持有锁），把当前库整体移走保留，再放回备份；启动前执行 `sqlite3 '<path>' "PRAGMA integrity_check;"`，结果必须是 `ok`。
- 只有账户已 flat、所有订单已终结时才允许用备份回滚数据库；否则会与交易所真实状态分叉，之后只能人工对账。
- 验收与演练都用副本；生产库只在 9.10 的升级流程里按上述规则备份。

### 9.5 暂停与平仓

```bash
curl -sS -X POST "$NOFX_BASE/api/automation/$ACCOUNT_KEY/pause"  -H 'Content-Type: application/json' -d '{"reason":"manual pause"}'
curl -sS -X POST "$NOFX_BASE/api/automation/$ACCOUNT_KEY/flatten" -H 'Content-Type: application/json' -d '{"reason":"manual flatten"}'
curl -sS "$NOFX_BASE/api/automation/$ACCOUNT_KEY/status"
```

- 暂停只停新入场，**不动**已挂的保护单；盘面仍暴露时不要以为"暂停=安全"。
- 平仓完成的判据同时满足才算完成：交易所侧仓位为 0、无未成交入场单与遗留保护单、`status` 中 `reserved_risk` 已归零、`state` 不再是 `needs_attention`/`frozen`。
- 未知回执时平仓状态机会冻结等待，不会重发；此时按 9.6 处理。

### 9.6 UNKNOWN 与人工介入

回执未知（超时、断连、进程崩溃）时**一律不重发**，先用原始 client ID 去交易所查：

```bash
# Binance USDⓈ-M
GET /fapi/v1/order?symbol=<symbol>&origClientOrderId=<clientID>
# OKX
GET /api/v5/trade/order?instId=<instId>&clOrdId=<clientID>
```

查到已成交 → 让控制器推进（或按状态机 flatten）；查到不存在 → 视为未下单，由控制器按规则重新决定；查到状态不明 → 保持冻结，人工处置。禁止为了"继续跑"去删订单行、改状态或重建绑定。

**必须在交易所手工对账的一种情况（当前代码的已知边界）**：进程在"保护单行已持久化为 `submitting`"之后、"POST 保护单"之前崩溃，这一行会永远停在 `submitting`。`planning/live/protection.go` 的 client-ID 守卫会拒绝重发同一个 client ID（这是防重复下单的保护，不是缺陷），因此运行时无法自行恢复，只能：

1. 保持自动化暂停，不要 reset；
2. 用该 client ID 到交易所查（上面两条命令）；查到就按实际状态处置，没查到就**手工**在交易所补挂等价的止损/止盈，或直接手工平仓；
3. 交易所侧处理完成后记录到验收/事故模板，并明确该账户需要人工收尾——继续自动运行前必须先让仓位与保护单回到运行时能对账的状态。

不要手工把该行改成其它状态：目前没有受支持的"孤儿 `submitting` 行回收"命令，改库会让下一次对账建立在假设上。

### 9.7 API 失效与账单缺口

- 私有事件通道掉线：REST 每 10s 补偿，属正常降级；`last_ws` 长期不更新时检查告警，不需要人工。
- `ledger_complete=false` 或出现 `funding_gap`：当日权益账本有缺口，新入场会被预算拒绝。保持只读、等待 REST 补偿补齐；单次对账超过 200 页（约 20 万条 Binance income / 2 万条 OKX 账单）时补偿追不上，按第 4 节人工补账。补账只补交易所已结算事件，**不要**估算资金费。
- API 全部失效（签名错误、IP 变更、Key 被删）：入场与保护都会失败，保护失败会进入 `exit_required` → 人工 flatten；按 9.8 轮换凭据后再恢复。

### 9.8 凭据轮换

按第 5 节的五步执行。sandbox 验收期额外要求：轮换后重跑一次 `TestSandboxManagedLifecycle`（同一副本或新副本均可），确认 gate 仍能解析绑定并通过 endpoint 与模式检查，再恢复自动运行。任何"凭据变了但绑定没重建"的现象都按未核验处理。

### 9.9 进程锁

- SQLite 部署：锁文件是 `filepath.Dir(DBPath)` 下的 `nofx-execution.lock`；默认库路径 `data/data.db` 时即 `data/nofx-execution.lock`（`main.go:95`）。第二个进程启动即失败，不会双写订单。
- PostgreSQL 部署：使用 `pg_try_advisory_lock`。**认证测试只证明了 SQLite 文件锁分支**：`trading/certification/crash_recovery_test.go` 的崩溃恢复用例全部跑在 SQLite 上，Postgres advisory lock 分支没有被覆盖。使用 Postgres 时必须手工验证一次：在第一个实例运行时启动第二个实例，确认它拿不到锁并退出，再继续上线流程。
- 容器/编排层不得把同一份数据目录挂给两个副本；滚动发布必须先停旧实例再起新实例。

### 9.10 升级顺序与回滚边界

升级顺序（每一步都不可省略）：

1. `pause`，确认不再有新入场；
2. 视情况 `flatten` 或确认现有仓位与保护单可被新版本接管（有未终结订单时不要升级）；
3. 备份数据库（9.4）并记录当前可执行文件的指纹（`shasum -a 256`）；
4. 停旧实例，确认锁已释放；
5. 部署新版本，核对运行时指纹与构建产物一致；
6. 启动后看 `status`：`ledger_complete=true`、无 `needs_attention`，再恢复 `start`。

回滚边界：

- 回滚二进制是允许的，但数据库迁移是单向的（启动时 `AutoMigrate` 只加不减）；旧版本能否读新库没有保证，必须先在副本上验证。
- 用备份回滚数据库只在账户 flat、所有订单已终结时允许；否则先人工对账。
- 不要用"回滚 + 重放"来掩盖未知回执；未知回执按 9.6 处理。

### 9.11 验收门槛与三种状态

sandbox-accepted 需要同时满足：两个 venue 都跑通完整周期；离线故障矩阵全过；连续 7 个 sandbox 日没有未解决的保护/账户真值事故；每个异常都有自动处置的证据（日志、`status`、告警记录）。逐项填写 `docs/operations/unattended-futures-acceptance-template.md`。

- **code-complete**：代码与离线测试完成，未接触真实交易所。
- **sandbox-accepted**：上述门槛全部满足，且模板已归档。live 仍默认关闭。
- **live-enabled**：需要单独的用户授权与更小的实盘仓位/窗口；sandbox 结果不证明实盘盈利能力，也不免除第 7 节的限制。
