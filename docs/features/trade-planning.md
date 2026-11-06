# NOFX one-time trade planning

This feature integrates Vergex SI's deterministic mathematics and bounded LLM
harness into NOFX. It is a user-initiated planning workspace, not a continuously
running strategy and not a historical backtesting requirement.

## Open the workspace

1. Initialize the pinned TradingView source before building:
   `git submodule update --init web/vendor/charting`.
2. Build/run NOFX using its existing instructions. `npm run build` and `npm run dev`
   copy vendor assets into generated `web/public/charting_library/`.
3. Log in and open **Frontier** (`/frontier`; `/planning` remains an alias) in the navigation.
4. Select an existing enabled Binance USDT perpetual account or Hyperliquid
   mainnet account. Hyperliquid uses NOFX's existing owner wallet, agent and
   builder authorization. Select an existing enabled NOFX AI configuration.
5. Open a native symbol (`BTCUSDT`, `BTC`, or `xyz:GOOGL` on its matching venue),
   choose a chart timeframe, then describe the trade to compare. Selecting one
   candidate clears the other candidates' chart overlays. Comparison displays
   up to three labeled hypotheses. Drawing positions in time are layout aids,
   not historical signals or future execution timestamps.
6. Review the prices, quantities, schedule, cost assumptions and account. Only
   clicking **Approve and execute live trade** in the review dialog locks and starts that plan; no typed phrase is required. No paper
   execution option exists in this feature.

No new model keys or wallet keys are needed. Model requests use NOFX's registered
provider clients, including their existing billing mechanism. Generating plans
can incur model/provider charges. Private keys never enter prompts or browser
responses. Sessions and discussion preferences are saved per logged-in user.
Preferences cannot lower the submitted hard risk limits.

## Supported execution and actual limits

- Real Binance perpetual limits and conditional exits; real Hyperliquid core
  perpetuals and `xyz` HIP-3 perpetuals. OKX public market data is implemented,
  but this workspace does not offer OKX live execution yet. There is no adapter
  fallback to another exchange or order type.
- **1–20x selected leverage, default 5x for new plans** (subject to venue limits), finite GTC limit orders only. `limit` is one entry; `ladder`
  accepts 2–8 weighted prices; `twap` accepts 2–24 fixed-price slices at 5–300
  second intervals, with total release time at most one hour. It does not chase
  prices or execute arbitrary generated code. Late TWAP slices expire rather
  than being released in a catch-up burst.
- All children share the approved stop/target; each child and the aggregate must
  pass exact-decimal fee/slippage, net reward/risk, tick/step, minimum quantity,
  minimum notional, budget and book-liquidity checks. Partial fills may leave
  quantities too small for an exchange protection order; failures require
  immediate manual intervention. Stops do not guarantee a maximum realized loss.
- Actual equity, available balance, fees (including Hyperliquid builder fees),
  fresh book and account identity are rechecked at execution. Existing positions
  or pending orders on the symbol are blocked. Automatic NOFX traders cannot
  start on a leased planning account, including manager/restoration launch paths.
  Binance identity currently uses its API key; two different API keys for the
  same Binance account require operator coordination.
- Entry fills are observed about every two seconds; corresponding reduce-only
  SL and TP orders are submitted for newly observed filled quantity. This is
  **not an atomic exchange bracket**: there is a fill-to-protection interval and
  a protection submission can fail. An acknowledgement is never a substitute
  for confirming the native exchange order. External position changes pause new
  entries. Inspect the real exchange immediately when the workspace says
  **Needs attention**.
- A plan expires after five minutes before execution. Once started, order
  monitoring continues to manage the already approved basket. Unfilled limit
  entries expire after a 15-minute window; TWAP's window ends two minutes after
  its last release. Stopping cancels unsent/pending entries; **it does not close
  filled positions or remove their active protection**.

## Approval, durability and recovery

The immutable approval digest binds the full plan, logged-in owner, configured
account and execution policy. AI tools cannot approve, submit, cancel, transfer,
or change credentials. Database ownership applies to drafts, runs and sessions.
Only one unresolved approved plan leases an account at a time.

Each child has a deterministic client order ID. Intent is persisted **before**
submission. Missing/time-out acknowledgements become `UNKNOWN`; they are queried
by client ID and never blindly resubmitted. Native receipts must match the
locked intent and monotonic filled quantity. Remaining protective orders must be
confirmed or canceled before releasing the lease; an unknown order retains it.

After restart, active runs become `needs_attention`; they do not resume new
entries or replay orders. Open **Previous executions → Reconcile exchange state** to resume reconciliation
and protection monitoring. Existing exchange-side stop/target orders continue
independently of the process. An outage/restart can delay protection of new
fills: verify positions and orders directly at the exchange before proceeding.
There is no automatic account unlocking for an unresolved order.

## Harness, chart and plugins

The bounded tool loop reads the selected native market, visible chart range and
comparison timeframes; it may propose, revise, draw, or run read-only data tools.
Data provenance is captured with the draft. History and preferences are scoped
by NOFX user; transient provider reasoning is not saved in chat JSON.

Public funding/open-interest sources and paid holder analytics are exposed
through the data-plugin registry. Holder cost/win-rate sources require Claw402
and only apply to their matching Hyperliquid market; they are never substituted
for Binance or OKX data. A profit source without a paid catalog endpoint remains
unavailable; there is no public-origin fallback. Open **Market data and indicators** to enable or disable individual sources for the same user
registry used by the AI, and explicitly read the selected market. Funding/OI
metrics, cost/liquidation distributions, labeled long/short win-rate heatmaps and
7/30-day profit tables are rendered from bounded typed data. Missing/incomplete
data is shown explicitly. There is no background paid-data polling. Switching
markets discards pending/stale visual responses.

On-site entry calculators use a bounded Starlark runtime, test cases, immutable
source hashes and session-scoped enabling. They can output proposed finite
allocations; they have no networking, shell, keys or order APIs. The normal
mathematical compiler and independent human approval remain mandatory.

TradingView runtime source is the user-requested
`tinkle-community/charting_library` repository, pinned as a Git submodule.
Generated runtime assets are ignored. Distribution of those assets is subject
to the applicable TradingView/library license; the upstream library is not modified.

## Verification boundary

Automated tests use local HTTP exchange fixtures and temporary databases, never
funded wallets or production order submission. Passing tests/builds does not
prove that an account has trading permission or that an actual live order has
succeeded. This integration must not claim such success without an authorized,
observed real transaction. Routine validation must not place real orders.

## Local integration with dev

The feature has been merged locally with upstream dev `294d0641`, including its
payment authorization, gasless approval, receipt verification and model updates.
Unknown paid-call outcomes retain their safe upstream classification and are
shown as requiring payment reconciliation, never as an instruction to resend.
Validation does not submit real transactions. The owner separately authorized
a direct merge to main after successful checks; no PR is created.

## Builder and Broker routing

The AI and connected-account configuration are NOFX's existing per-user records.
Approved plans now call NOFX's exchange traders through the shared
`trader/execution.Trader` contract. The planning bridge only forwards calls;
it contains no exchange SDK or signing/submission code. The original NOFX
`PlaceLimitOrder` entry points also call the same `SubmitOrder` implementation.
Binance uses the common credential/client factory (including NOFX hooks) without
changing position mode when opening the workspace. Hyperliquid uses the existing
trader, wallet authorization and Builder submission helper.

Single limits, weighted ladders and TWAP remain planning/scheduling choices:
only the approved basket is released into the shared order API. Approval,
durable intent, partial-fill protection, cancellation, account exclusion and
restart reconciliation remain in the finite controller. Existing planning run
JSON and historical client IDs remain readable; this refactor does not merge
planning runs into the automatic strategy decision history.

The existing limit API now retains stable client identity, honors maker-only
and reduce-only flags, fails closed on leverage errors, and returns actual
Hyperliquid order IDs instead of synthetic timestamps. Existing grid labels
are deterministically mapped to 128-bit client IDs; exact plan IDs pass through.
Hyperliquid status queries use native order status and share state decoding
with planning: missing orders are errors, not presumed fills. The status
endpoint does not supply weighted average fill price or commission; those
fields remain zero until filled-trade data supplies them.

Hyperliquid planning entry and protective orders carry the existing NOFX
`defaultBuilder` address and fee (50 tenths of a basis point = 0.05%). Binance
entry and protective orders use the same `x-KzrpZaP9` prefix as NOFX automatic
orders. The prefix is shared as a constant, and a lossless base64url encoding of
the durable 128-bit ID keeps the complete client ID within 32 characters. Native
routing IDs are persisted before sending, and reused for querying/canceling;
historical untagged orders keep their original lookup ID.

These fields establish requested routing, not verified earnings. Builder fees
belong to the configured builder, and Binance rebate eligibility belongs to the
registered Link/Broker account. Neither account ownership nor successful rebate
settlement has been verified here. No real order or fee receipt was produced by
the tests.


## Paid data, language and request completion

Planning UI, chart labels, prompts and module errors are English. Unicode venue
symbol compatibility is retained using escaped test fixtures, not translated
asset identifiers. Existing user-authored history is not rewritten.

The holder analytics decoder has no public-origin HTTP fallback. Its transport
is supplied by NOFX's per-user Claw402 wallet resolver and existing Vergex x402
client. Cost/liquidation uses `/api/v1/vergex/cost-liquidation-heatmap`; holder
win rate uses `/api/v1/vergex/holder-winrate-map`. Paid results and failures are
cached for five minutes per user, component and market, with no automatic
application-level retry. Funding and open interest remain native public reads.
The Claw402 public catalog checked during this change does not publish a profit
leaderboard endpoint. That component explicitly reports unavailable rather than
calling Hyperdata directly or inventing a paid route.

Binance charts expose the complete returned AI500 board with search and quick
selection through `/api/planning/ai500`, using the existing Claw402 data client
and `/api/v1/nofx/ai500/list`. Successful and ambiguous results are isolated by
user and retained for five minutes. UI refreshes within that window do not issue
another paid upstream request. AI500 membership does not guarantee a Binance
instrument is still listed; opening a symbol still validates venue metadata.

A planning turn has a 180-second wall-clock limit and each model round is capped
at 75 seconds. Progress remains visible with a Stop action. Context data is
bounded and repeated identical tool reads are cached; persistent read loops
finish with an explanation. Streams emit a terminal event on success or failure,
including model/turn timeouts. Unexpected EOF without a terminal outcome is a
visible error, never an automatic paid retry. No live paid call, payment transfer
or real trade was performed by the automated fixture tests.


## Public venue selection and receipt compatibility

Market venue selection is independent of execution-account selection. Binance
USDT perpetual charts and the Claw402 AI500 board are visible even when only a
Hyperliquid trading account is connected. Public planning drafts can use an
empty execution-account binding; approval remains disabled, and the backend
still requires an actual owned, enabled account before approval or execution.
Choose an account and regenerate the plan before trading; public drafts are
never silently rebound to an account.

JSON usage receipts accept either standard or legacy receipt headers and may
carry their settled amount inside the receipt. Missing auxiliary amount headers
do not erase that value; conflicting values remain invalid. Verification can
poll the same pending transaction receipt through read-only RPC. It never
resigns or resends a paid request and still verifies payer, amount, chain,
facilitator and Permit2 calldata. Unknown outcomes carry a fixed, sanitized
diagnostic category so the UI can distinguish receipt/RPC failures without
exposing provider payloads or credentials.

### Model tool capabilities

Planning reuses the configured NOFX model client and fee wallet. Native function
calls remain enabled for DeepSeek. Claw402 OpenAI routes that reject `tools` use
an explicit text-tool protocol selected before the request: schemas are supplied
as text, the model returns a bounded JSON envelope, and Go validates every tool
name and argument object before dispatching it. The available tools only read
market data, annotate charts, and create risk-validated drafts. Approval and order
execution are never model tools. Malformed envelopes or unauthorized tool names
fail without dispatch. A failed paid request is never automatically retried with
a different protocol or a new payment authorization.

A gateway HTTP 400 is not proof that no payment occurred. The UI retains only
allowlisted error classifications and never displays upstream response bodies.

### Planning progress and entry structures

A turn uses its existing round/tool budget for corrections and retains at most five unique drafts.
A partially accepted batch no longer prevents correction of rejected candidates
within that turn. Previously accepted ideas are deduplicated independently of
names and explanations, and an original selected draft is revised only once.
Risk limits, per-child validation and separate execution approval are unchanged.

The event stream publishes candidate validation starts/results and each structure's
rejection reason. Model waiting heartbeats arrive every two seconds. The Activity
panel follows these real events and measures elapsed time; it does not simulate
model reasoning or fabricate progress percentages. It stops following when the
user scrolls back to inspect earlier steps.

Selecting a TWAP draft displays every planned child quantity, common limit price
and release offset in an always-visible band attached to the chart. This band has
its own seconds axis: T+0 means execution start after approval, never a historical
candle timestamp. Ladder previews show distinct price levels placed together.
These previews are not submitted orders, and neither release nor fill is implied.

### Automatic account sizing

With a connected account, the server reads NOFX equity, free margin and fee rates
before planning and before the final order review. Monetary budgets are derived,
not entered: 1% of current equity is the planned stop-risk ceiling, and 95% of
free margin, after reserving estimated costs, sets the leveraged notional ceiling. Venue minimums,
liquidity, child rounding and stop risk can reduce size. This is not an instruction
to use the entire account or a guarantee of the realized maximum loss.

Review shows final quantity, planned loss, leverage and estimated initial margin.
Refreshing an old draft creates a new hash requiring the explicit review click;
approved orders are never silently resized. Execution rechecks the actual basket's
margin plus cost reserve rather than comparing the entire unused budget against
available balance. The shared NOFX Binance/Hyperliquid adapters set the locked
leverage before any entry. Failed leverage setup blocks orders. A conservative
stop-distance guard rejects plans consuming 80% of initial margin at the chosen
leverage; it is not a venue liquidation-price calculation.

### Frontier and unified account collateral

The navigation and workspace are named **Frontier**, at `/frontier`. Existing
`/planning` links redirect within the app, and authenticated API routes remain
compatible. The new address also avoids historical cached redirects from the
old chart asset directory that dropped nonstandard local ports. The conversation and live
activity share one scroll area, with the composer kept reachable. A request
rejected before the event stream starts does not accumulate phantom chat turns.
Expired drafts remain in the saved-plan disclosure rather than appearing as
ready-to-trade candidates.

The shared Hyperliquid execution snapshot follows NOFX's unified-account policy:
Spot USDC total is equity, and total minus authoritative hold is available
collateral. Core/xyz collateral views and unrealized PnL are not added again.
Values stay exact decimal strings. Missing or malformed reservation data fails
closed; separate accounts never spend Spot collateral through this path.

### Account-first trading workspace

Frontier now shows the selected NOFX account's complete position list above the
chart: direction, quantity, average fill, mark, unrealized PnL and leverage.
It reuses authenticated `/api/my-traders` and `/api/positions` with an exact
exchange-account binding. Reads refresh every ten seconds without overlap and
pause in hidden tabs. Read failures preserve explicitly stale values rather
than presenting an empty account. The only connected account is selected on
initial load unless the user already chose a different market.

Native `/api/open-orders` results are scoped to the named chart market. Exit
orders are displayed beside positions; native current order reads and persisted
Frontier execution records are labeled separately. Active executions are shown
across accounts so switching the market cannot hide an unresolved trade.

Buy / Long and Sell / Short open a manual limit/ladder/TWAP ticket without a
model request. The existing compiler, account sizing, review, approval and NOFX
execution path remain in use. Existing same-market positions must be closed
before another independent entry. Market entry orders are not implemented by
this ticket; the manual close action is a market close.

Each position has a close review with an explicit final click and no typed
phrase. It reuses NOFX's existing `/api/traders/:id/close-position` operation.
Matching Frontier entries are stopped first; the UI verifies their cancellation
before closing, rereads the position, and refuses a position that grew or changed
entry after review. It blocks manual close while account Autopilot is running,
and checks the current account binding again. An ambiguous close response is
never repeated automatically. Existing exchange/API authority checks remain the
server boundary; this UI does not add a new order execution adapter.

Data plugins and previous executions are collapsed. Plugin visuals are mounted
only when explicitly opened. Manual entry and close paths are covered with
mocked-API tests; development verification never submits a live order.

The position and open-order read routes resolve ownership from stored NOFX
accounts without loading an autonomous trader. An unresolved planning lease
therefore cannot hide an existing position. Binance inspection uses the existing
constructor that does not change account hedge mode. Hyperliquid order reads use
`frontendOpenOrders` with the exact core/xyz market and retain stop/take-profit
trigger prices and reduce-only direction. These reads do not submit orders.

Frontier keeps the chart and plan alternatives first, with the account position
table beneath them. Stored execution records are collapsed while review-needed
status remains visible. Fresh planning and manual-entry tickets default to 5x;
this does not rewrite approved plans or change leverage on existing positions.

## Private chart source in CI

The pinned chart library is private. Frontend and image workflows fetch its exact
Git revision with the Actions secret `CHARTING_LIBRARY_SSH_KEY`, a dedicated
read-only deploy key scoped to `tinkle-community/charting_library`. The initial
application checkout does not fetch private submodules, so Go-only checks do
not need that key. Checkout credentials are not persisted in the workspace.
The owner's local SSH private key is not reused or uploaded. Forked pull requests
do not receive repository secrets and cannot run the licensed-asset build
without a maintainer-authorized environment; there is no public mirror fallback.

## First-use setup and quick markets

Frontier keeps the chart open before configuration. A compact two-step setup
strip links directly to an in-workspace AI-wallet and exchange flow. Starting
an AI request without a configured/funded Claw402 model opens setup and retains
the idea; completing setup does not submit that request or any trade. Existing
owner-configured direct AI providers do not require a Claw402 deposit for model
calls. Paid AI500/holder sources still require their own fee-wallet path.

The new authenticated GET/POST `/api/planning/setup-wallet` endpoint only returns
public wallet details. Creation is explicit and idempotent for the current
owner. It requires encrypted storage, does not write process-global credentials
or .env, does not adopt other users' wallets, and does not export the private
key. Balance reads distinguish an unknown RPC result from zero. The deposit
QR/address uses Base USDC; exchange funds remain separate. Hyperliquid reuses
NOFX's existing wallet/Agent/Builder authorization UI. Binance reuses encrypted
NOFX API-key configuration, restricted here to mainnet Futures. Neither flow
starts Autopilot or submits an order. Live permissions, fees, balances and risk
remain subject to order review and execution preflight.

AI500 is visible in the market selector rather than hidden inside a collapsed
menu. Public Top volume, Stocks and All markets tabs work without a fee wallet
or trading account. Binance markets come from USD-M exchangeInfo and ticker/24hr,
including active PERPETUAL and TRADIFI_PERPETUAL USDT contracts. The stock filter
uses venue equity classifications, including international equity subclasses.
Hyperliquid discovers core and builder DEXs with perpDexs, perpCategories and
metaAndAssetCtxs. Ranking uses 24h notional volume, not base-asset quantities.
Native builder prefixes remain intact; a missing DEX/category read is reported
as partial coverage. The discovery cache lasts one minute and coalesces requests.
No paid fallback is used. Builders other than the currently integrated core/xyz
execution paths are labeled Chart only; discovery does not add order adapters
or bypass collateral/account validation.

The latest opt-in public-data smoke check observed 745 Binance markets (182
stock-classified contracts) and 333 Hyperliquid markets (128 stock-classified
contracts), without missing-coverage warnings. These are changing snapshots,
not a hardcoded universe or a promise of exchange-account eligibility.

### Frontier workspace layout

The workspace follows three steps: explore a market, compare plans, then review a trade.
Exchange, account and native-symbol selection share one toolbar. AI500, volume-ranked
markets and stock contracts remain visible in a compact market strip.

The main chart shows the selected plan. The right-hand assistant switches between
conversation and plan results without recreating the chart; fresh results open the
Plans view automatically. Idea starters only fill the composer. They do not call a
model, charge a wallet or submit an order. Generate plans uses the entered idea when
present. Model selection, chart-data plugins and advanced entry tools are optional
expanded sections.

Real positions and current-market exchange orders have a separate full-width account
section below the workspace, reachable with Positions & orders. Historical plans are
collapsed separately from valid drafts, and execution history lives with account
activity. The existing account review, approval and exchange execution guards remain
in place. Responsive layouts stack the assistant below the chart on narrow screens.

Existing positions and working orders do not prevent draft generation. Automatic
sizing uses remaining free margin and keeps its existing reserve and per-plan risk
ceiling. Starting a discussion from a locked trade creates a separate draft context;
it does not revise, revoke or execute the old trade. Execution still rejects mixing
an independent entry with existing same-market exposure. Position-aware scale-ins,
hedging and amendments require explicit execution support; generating a draft does
not grant that authority.
