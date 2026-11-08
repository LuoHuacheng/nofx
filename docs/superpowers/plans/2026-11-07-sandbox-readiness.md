# Binance 模拟账户托管就绪

目标：完成 Binance Testnet 2 的交易所账户身份核验及托管绑定，隔离历史策略的启动标记，然后验证服务安全启动。用户已授权上述两项工作；本次不启用自动交易、不提交订单。

## 范围与实现

1. 为 Binance adapter 实现 `execution.AccountIdentity`。使用签名只读余额查询返回的原生 `accountAlias`，不用 API Key 哈希或本地生成值代替身份。所有余额行必须提供相同且非空的标识，否则拒绝绑定。沿用现有 SDK、环境隔离和错误脱敏。
2. 为正常、多资产、空值、冲突、交易所错误编写 HTTP fixture 回归测试；同步操作文档中的身份字段及绑定路径。
3. 从已加密的配置加载 Binance Testnet 2，只读查询账户身份及空仓空单条件；备份数据库后，通过现有 `BindManagedAccount` 保存已核验 sandbox 绑定。保持风控 policy 默认关闭。
4. 核对本地无运行进程，备份数据库，将历史 trader 的 `is_running` 清零，并停用其旧交易所配置，保留凭据和策略。首次启动复核发现 `cleanupIncompleteExchangeConfigs` 会重新启用完整但被停用的账户，造成旧客户端初始化请求；为此补上失败回归，删除该自动启用分支。重新停用旧账户并重启，检查健康端点、运行日志、绑定、policy、历史 trader 状态及无订单/持仓条件。

## 验证与回滚

- 定向 Go 测试、`go vet`、`gofmt`、`git diff --check`。
- 实际签名 GET 证明账户标识可读；绑定信息、策略停用清单与数据库快照保存在私有临时目录，不输出密钥。
- 失败时保持自动交易关闭并停止本次新启动进程；保留备份供恢复。恢复旧运行标记必须经过用户再次决定，避免意外带起旧策略。

## 进度

- [x] 身份 adapter 与回归测试：9 个身份边界用例及相关绑定 API 测试通过。
- [x] 实际身份验证及绑定：两次签名 GET 返回一致身份，回读 sandbox 绑定通过，空仓空单，自动交易未启用。
- [x] 历史策略隔离与服务启动验证：两条旧 trader 的运行标记为 0，旧账户停用，重启后加载旧 trader 数量为 0，未再次初始化旧账户。

## 完成验证

- `go test ./trader/binance ./trader/execution ./api -run 'TestManaged|TestPlanningBrokerUsesBoundSandboxEnvironment' -count=1` 通过。
- `go test ./store ./trader/binance ./trader/execution -count=1` 全部通过。
- `go vet ./store ./trader/binance ./api`、构建、`gofmt` 和 `git diff --check` 通过。
- 服务在 `http://127.0.0.1:8080` 运行，健康接口和带认证的托管状态接口均为 200，`environment=sandbox`、`ledger_complete=true`。
- 风险政策数与执行 run 数均为 0；`state=frozen` 的原因为 `no risk policy for this account`，自动入场仍被禁止。
- 重启后正式 adapter 再次核验：交易所身份与绑定一致，单向账户，权益及可用余额 `5004.48604296` USDT，持仓和普通／保护挂单均为 0。
- 现有用户配置和凭据保留，修改前数据库快照及核验收据位于私有目录。未提交订单，完整交易闭环未执行。

## 2026-11-08 自动交易启用记录

用户授权开启 Binance Testnet 2，并明确选择：BTCUSDT / ETHUSDT、最高 1x、单笔风险 0.1%、账户与组合在险 0.2%、日亏 0.5%、回撤 1%，使用已有 custom AI / gpt-5.6-terra。

启动检查发现并修复三处问题：custom provider 未在客户端工厂注册；StartAccount 在刷新交易所权益前检查旧基线，导致 REST 两次采样之间被错误拒绝；未配置告警目标时缺少 managed_alert_targets 表。三项故障复现测试及人工暂停保持测试均通过。automation / risk / live / execution 全包、相关 API 测试、go vet 与构建通过。

政策保存及 start 接口曾返回 200 / running，但首轮信号的实际模型调用失败。只读 GET https://api.a6api.com/v1/models 确认 HTTP 401 / invalid_api_key。已将政策 enabled 关闭，防止反复失败请求；不能把这次接口成功报告为自动交易已可用。最终回读：执行 run 为 0，旧 trader 均关闭，模拟账户持仓与普通／保护挂单均为 0。

用户选择在配置页更新模型，随后确认配置完成。新配置为 custom AI / deepseek-v4.1-flash（https://api.onlytest.top/v1）；只读模型列表认证通过，实际行情与模型信号预检通过。

本次联调额外修复并验证：模型返回后、预留风险前重新对账，避免权益基线被模型耗时耗尽；信号的 30 秒有效期从严格校验完成的生成结果开始计时；core.Compile 的时钟在行情请求返回后采样，避免正常网络延迟把新行情误判为来自未来。过期信号、5 秒行情／权益时效检查保持执行。

发现配置页的模拟账户余额查询会调用有副作用的 NewFuturesTrader，重新启用双向模式。api/exchange_account_state.go 改用只读的 sandbox 适配器；HTTP fixture 证明余额探测不发送账户设置写请求。真实空仓／空挂单状态下，将专用模拟账户设为单向，BTC/ETH 逐仓、1x，并回读 VerifyManagedSettings 通过；之后调用账户余额查询，再回读仍为单向。

所有相关 automation / risk / live / execution 测试、相关 API 测试、go vet 与构建通过。最终服务已加载修复，低风险政策 enabled=true，start/status 返回 sandbox / running、ledger_complete=true。首轮真实运行观察未通过：模型请求完成，但编译候选因入场价与盘口偏离或净收益无法覆盖费用等校验拒绝，执行 runs=0。连续失败后状态变为 frozen（信号冻结），政策 enabled 保持；代码仍按原频率读取后续信号，健康信号或明确的空信号可清除此类冻结，不清除人工暂停与风险熔断。公开盘口与最近闭合 K 线核对正常，未见 10% 级别环境价差。期间出现过 Binance 时钟同步及 -1021 暂态错误，后续模型循环已继续；不把接口启动成功或模型预检成功认定为运行验收通过。后续周期仅做限时只读观察，不修改交易风险阈值，不为取得成交绕过校验；入场、保护、退出的成交闭环仍未完成。

## 2026-11-08 信号与监督修复后的最终运行验证

上述首轮失败已按 `2026-11-08-signal-market-fixes.md` 修复：服务端标的随信号传递；模型获得盘口、tick及真实手续费/滑点/资金费和净RR约束；公开行情GET仅一次有界瞬时重试。额外消除慢模型阻塞监督和10秒轮询超过5秒权益时效的问题：每账户唯一异步模型任务，正常对账间隔2秒，结果在锁内重新检查取消与安全冻结；同一用户共享模型源串行访问。

最终代码7包470测试条目通过、2项显式在线smoke/lifecycle跳过；定向并发检测、相关API、go vet和构建通过。切换前禁用入场、保存一致备份、确认0执行记录，旧进程SIGTERM并释放执行锁后启动最终服务 `bc00ad5da`，恢复用户原低风险政策。

最终限时自然观察 `automation_observation_cadence_fix_20261108.json` 返回 `healthy_hold_cycle`：2次模型请求，60次状态采样全部running，24次不同REST对账，最大权益年龄2.583736秒，账本完整。模型返回健康空信号，draft与run均为0。观察后再做签名SDK及认证HTTP回读：sandbox/running、政策enabled、身份匹配、单向、权益及可用5004.48604296 USDT、持仓和普通/保护挂单0、历史策略运行0。完整收据 `automation_final_verified_20261108.json` 保存于私有目录。自动模拟交易继续按原BTC/ETH、最高1x及原风险阈值运行；未强制取得成交，成交/保护/退出完整闭环仍未验收。
