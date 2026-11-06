# Claw402 model payments without Base ETH

The configured **fee wallet** pays in Base USDC using native x402 `upto`.
This wallet is separate from the Hyperliquid trading/agent wallet. No ETH top-up
or user-funded approval transaction is needed when the gateway advertises the
standard `eip2612GasSponsoring` extension.

The client validates the quote, chain, USDC asset, recipient, facilitator,
per-call cap and deadline. If Permit2 allowance is insufficient, it reads the
USDC permit nonce and checks the on-chain domain, then signs a bounded USDC
permit and the separate Permit2 witness. The facilitator submits the permit
with settlement. Existing sufficient allowance skips the extra signature.
No unlimited allowance, exact-price downgrade, or automatic paid retry is used.

Model calls sharing a fee wallet are serialized across client instances in one
process through response/receipt verification. Use separate fee wallets for
separate NOFX processes. Sharing with another process/browser can race USDC's
sequential permit nonce; an ambiguous paid failure must be reconciled, not
blindly re-signed. The client never assumes a successful signature is a payment.

Claw402 quotes and final charges may include the gateway's disclosed settlement
service fee as well as token usage. The total remains within the signed cap;
NOFX records verified actual settlement instead of reconstructing the bill from
an outdated token-price estimate. Keep enough USDC for the quoted ceiling.

### Payment failure fixes (2026-10-14)

- Permit2 nonces are emitted as decimal uint256 strings required by the
  facilitator schema. Exact EIP-3009 nonces remain bytes32 hex.
- Both model and data payments check the fee wallet's Base USDC balance against
  the live quote before sending any authorization. No cached flat model price
  or ETH balance substitutes for this check. RPC errors stop before submission.
- AI500 does not wrap paid requests in an outer retry, including malformed
  responses after a charge. Gateway verification rejection is surfaced separately
  from an unknown settlement, but neither is automatically re-signed.
- Automatic data-client resolution fails closed on a missing or invalid wallet;
  the strategy engine uses this resolver and never silently switches to the
  origin, even if a legacy NofxOS API key remains in the strategy configuration.
  Missing fee-wallet configuration now reports an error for paid data. Explicit
  standalone API-key integrations constructing `NewClient` are unchanged.
- Official aliases for deployed models resolve to the corresponding endpoint.
  Unknown/newer models are rejected instead of silently charging for GPT-5.6.
  This does not enable Fable 5.1 or GLM-5.3 on the gateway.

Existing NOFX installations must upgrade to receive these fixes. A gateway
deployment alone cannot update third-party binaries or fund empty fee wallets.

### Model compatibility update (2026-10-14)

- Added dedicated mappings for `gpt-6.1-sol`, `gpt-6-sol`, and `gpt-6-luna`.
  Release the matching Claw402 gateway routes **before** this NOFX selector.
  No silent model substitution is used if the gateway has not been upgraded.
- The DeepSeek Flash selection retains `deepseek-v4-flash` as a compatible
  client ID and displays V4.1 Flash; `deepseek-flash` also resolves correctly.
- Selector prices include the gateway's markup. They are indicative, not a
  payment quote: DeepSeek displays peak prices; GPT long-context multipliers
  and the $0.0012 per-request settlement fee are disclosed separately.
- Token-only cost estimates include the fee but do not reconstruct actual
  receipts or DeepSeek peak-boundary billing. The accounting path still
  records only verified settlement for Claw402.
- GPT-6 routes currently support text decisions, not tool-calling agent loops.
  Fable 5.1 and GLM-5.3 are not selectable without verified gateway access/pricing.

Offline tests use generated/unfunded keys and local mock RPCs, including the
same EIP-712 golden vector as the web client. A real zero-ETH, funded-USDC wallet
canary still requires its owner's authorization; compare payer and recipient
balance changes with the verified receipt. Passing unit tests does not prove a
live facilitator transaction or eliminate upstream/settlement failure costs.

### HTTPS paid-transport correction (2026-10-14)

Unsigned discovery may negotiate HTTP/2. A cloned one-shot paid connection must
not retain that transport's `h2` TLS ALPN advertisement while parsing HTTP/1.1.
The paid transport now pins both ALPN and protocol dispatch to HTTP/1.1, without
changing TLS certificate verification, the original client's configuration,
proxy/dialer, one-authorization limit, or redirect/replay protections.

Regression tests exercise a real local TLS server advertising HTTP/2 and
HTTP/1.1, including GET/POST, JSON/SSE receipts, disconnections and redirects.
An optional `NOFX_LIVE_X402_HTTP_TEST=1` test checks the same transport against
the public `/health` endpoint only; it never signs or purchases a request.

New unknown-payment errors do not expose raw transport bytes/URLs in the UI;
their typed cause remains available through `Unwrap`. Historical records are
formatted at display time without rewriting the audit database. Matrix retry
buttons are disabled while the previous payment outcome is unconfirmed.

Do not apply a local runtime update by restarting a live trading container
without the operator's confirmation: its saved configuration may automatically
resume trading. Passing transport tests is not a funded settlement canary.

References:
- https://github.com/x402-foundation/x402/blob/main/specs/schemes/upto/scheme_upto_evm.md
- https://github.com/x402-foundation/x402/blob/main/specs/extensions/eip2612_gas_sponsoring.md
