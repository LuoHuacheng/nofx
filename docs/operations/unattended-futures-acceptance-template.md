# 无人值守合约交易验收记录模板

按 `docs/operations/unattended-futures.md` 第 9 节执行，本文件只记录结果，不替代操作手册。**任何情况下不记录 API Key、Secret、Passphrase、私钥、加密密钥或解密后的凭据片段。**

一次验收 = 一个 venue 的一份记录；两个 venue 各填一份。

## 1. 版本与时间

| 项目 | 值 |
| --- | --- |
| Git HEAD（完整 SHA） | |
| 分支 | |
| 构建产物指纹（`shasum -a 256`） | |
| 验收开始 / 结束（UTC） | |
| 交易所官方文档查阅日期（区间/费率/错误码来源） | |
| 操作人 | |

## 2. 环境

| 项目 | 值 |
| --- | --- |
| venue | binance / okx |
| 网络环境 | sandbox（testnet / demo） |
| 账户类型 | 专用子账户 |
| 绑定 ID（account key） | |
| `venue_account_id`（交易所原生 UID） | |
| 交易所配置 `exchange_id` | |
| `environment` / `enabled` / `verified_at` | |
| 验收数据库路径（副本，非 `data/data.db`） | |
| 仓位模式 / 保证金模式 / 杠杆 | one-way / isolated / ≤3x |
| API Key 权限（合约交易 + 只读，无提现，IP 白名单） | 已确认 / 未确认 |

## 3. 生命周期证据

每个回合一行；`client ID` 必须是运行时实际发出的那个，不能事后编造。

| # | 信号时间 | 入场 order ID / client ID | 成交价与数量 | SL order ID | TP order ID | 保护确认延迟（成交→交易所可见） | 减仓/退出 order ID | 平仓后仓位 | 遗留订单数 | 预留风险状态 | 备注 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | | | | | | | | 0 | 0 | released | |
| 2 | | | | | | | | | | | |

判定：仓位为 0、遗留订单为 0、`reserved_risk` 归零三者同时满足，该回合才算 settled。保护确认延迟超过 5s 保护宽限的回合记为失败，不得计入通过样本。

## 4. 风险与账户真值

| 项目 | 值 |
| --- | --- |
| 当日最大亏损（bps，来自 `status`/账本） | |
| 当日最大回撤（bps） | |
| 是否触发日亏熔断（200bps） | 是 / 否 |
| 是否触发回撤熔断（500bps） | 是 / 否 |
| `ledger_complete` 是否全程为 true | 是 / 否（若否，列出缺口窗口） |
| 账户权益/可用（起止） | |
| 资金费事件是否完整（无 `funding_gap`） | 是 / 否 |

## 5. 异常与处置

每个异常一行；必须写清是否**自动**处置，以及证据位置（日志/告警/`status` 快照）。

| # | 时间 | 现象 | 分类（保护缺失/未知回执/账单缺口/API 失效/账户真值/其它） | 是否自动处置 | 处置动作 | 证据位置 | 是否完全解决 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | | | | | | | |

未解决项为 0 才能进入 sandbox-accepted；有未解决项时只能停留在 code-complete。

## 6. 已知限制与未取证事实

勾选本次验收**没有**证明的内容：

- [ ] 崩溃恢复只在 SQLite 文件锁上验证过；PostgreSQL advisory lock 分支未覆盖（Postgres 部署需另做双实例验证）。
- [ ] "保护单行 `submitting` 后崩溃" 需要交易所侧人工对账，运行时不会重发（`planning/live/protection.go` 的 client-ID 守卫）。
- [ ] OKX `acctLv=2` 的 `imr` 字段、OKX `amend-algos`、Binance 物理账户 UID 仍只有 HTTP fixture 证据。
- [ ] 其他（写明）：

sandbox 通过不证明实盘盈利能力；live 默认关闭，开启需单独授权并减小仓位与窗口。

## 7. 结论

| 状态 | 勾选 |
| --- | --- |
| code-complete | |
| sandbox-accepted（两 venue 完整周期 + 离线矩阵 + 连续 7 个 sandbox 日无未解决事故） | |
| live-enabled（单独授权） | |

| 项目 | 值 |
| --- | --- |
| 连续 sandbox 天数 | |
| 未解决事故数 | |
| 验收人 / 复核人 | |
| 归档位置 | |
