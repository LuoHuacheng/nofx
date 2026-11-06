package payment

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"nofx/internal/permit2"
)

func TestGaslessFeeWalletUsesBoundedPermitAndNoTransactionRPC(t *testing.T) {
	key, _ := crypto.GenerateKey()
	owner := crypto.PubkeyToAddress(key.PublicKey).Hex()
	opt := X402AcceptOption{Scheme: "upto", Network: BaseNetwork, Asset: BaseUSDCContract, Amount: "11099", PayTo: "0x1111111111111111111111111111111111111111", MaxTimeoutSeconds: 660, Extra: map[string]string{"facilitatorAddress": "0x2222222222222222222222222222222222222222"}}
	required := X402v2PaymentRequired{X402Version: 2, Accepts: []X402AcceptOption{opt}, Extensions: map[string]json.RawMessage{permit2.GasSponsorExtension: json.RawMessage(`{"info":{"version":"1"}}`)}}
	td, _ := permit2.GaslessTypedData(owner, big.NewInt(11099), big.NewInt(7), big.NewInt(1))
	domain, _ := td.HashStruct("EIP712Domain", td.Domain.Map())
	for _, mode := range []string{"gasless", "existing", "domain-mismatch", "wrong-chain", "rpc-error", "no-extension"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var request struct {
					Method string            `json:"method"`
					Params []json.RawMessage `json:"params"`
				}
				json.NewDecoder(r.Body).Decode(&request)
				result := "0x0"
				switch request.Method {
				case "eth_chainId":
					result = "0x2105"
					if mode == "wrong-chain" {
						result = "0x1"
					}
				case "eth_call":
					var call struct {
						To   string `json:"to"`
						Data string `json:"data"`
					}
					json.Unmarshal(request.Params[0], &call)
					if !strings.EqualFold(call.To, BaseUSDCContract) {
						t.Errorf("wrong contract %s", call.To)
					}
					switch {
					case strings.HasPrefix(call.Data, "0x70a08231"):
						result = "0xf4240"
					case strings.HasPrefix(call.Data, "0xdd62ed3e"):
						if mode == "existing" {
							result = "0xf4240"
						}
					case strings.HasPrefix(call.Data, "0x7ecebe00"):
						result = "0x7"
					case call.Data == "0x3644e515":
						result = hexutil.Encode(domain)
						if mode == "domain-mismatch" {
							result = "0x00"
						}
					default:
						t.Errorf("unexpected contract call %s", call.Data)
					}
				default:
					t.Errorf("must not query ETH balance or send transactions: %s", request.Method)
				}
				if mode == "rpc-error" {
					w.WriteHeader(503)
					return
				}
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":%q}`, result)
			}))
			defer rpc.Close()
			t.Setenv("NOFX_X402_RECEIPT_RPC_URL", rpc.URL)
			challenge := required
			if mode == "no-extension" {
				challenge.Extensions = nil
			}
			header, _ := json.Marshal(challenge)
			encoded, err := (&Claw402Client{privateKey: key}).signPayment(base64.StdEncoding.EncodeToString(header))
			if mode != "gasless" && mode != "existing" {
				if err == nil || encoded != "" {
					t.Fatal("unsafe signature returned")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := X402DecodeHeader(encoded)
			var signed struct {
				Payload    permit2.Payload `json:"payload"`
				Extensions map[string]struct {
					Info permit2.GaslessPermit `json:"info"`
				} `json:"extensions"`
			}
			if err = json.Unmarshal(raw, &signed); err != nil {
				t.Fatal(err)
			}
			if err = permit2.Verify(8453, signed.Payload); err != nil {
				t.Fatal(err)
			}
			if mode == "existing" {
				if len(signed.Extensions) != 0 || calls.Load() != 4 {
					t.Fatal("unnecessary permit")
				}
				return
			}
			info := signed.Extensions[permit2.GasSponsorExtension].Info
			if info.Amount != opt.Amount || info.Spender != permit2.Address || info.Asset != BaseUSDCContract || info.Nonce != "7" || info.Version != "1" || info.Deadline != signed.Payload.Authorization.Deadline {
				t.Fatalf("wrong bounded permit: %+v", info)
			}
			deadline, _ := permit2.Uint256(info.Deadline)
			permit, _ := permit2.GaslessTypedData(owner, big.NewInt(11099), big.NewInt(7), deadline)
			hash, _, _ := apitypes.TypedDataAndHash(permit)
			sig, _ := hexutil.Decode(info.Signature)
			if len(sig) != 65 {
				t.Fatal("invalid signature")
			}
			sig[64] -= 27
			pub, err := crypto.SigToPub(hash, sig)
			if err != nil || crypto.PubkeyToAddress(*pub).Hex() != owner {
				t.Fatal("permit signer mismatch")
			}
		})
	}
}

func TestFeeWalletLockSerializesSharedWalletAndCleansUp(t *testing.T) {
	var active, peak atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release := lockFeeWallet("0xABC")
			defer release()
			n := active.Add(1)
			if n > peak.Load() {
				peak.Store(n)
			}
			time.Sleep(time.Millisecond)
			active.Add(-1)
		}()
	}
	wg.Wait()
	if peak.Load() != 1 {
		t.Fatal("shared fee wallet raced")
	}
	release := lockFeeWallet("0xABC")
	otherDone := make(chan struct{})
	go func() { unlock := lockFeeWallet("0xDEF"); unlock(); close(otherDone) }()
	select {
	case <-otherDone:
	case <-time.After(time.Second):
		t.Fatal("different wallet blocked")
	}
	release()
	feeWalletLocks.Lock()
	defer feeWalletLocks.Unlock()
	if len(feeWalletLocks.entries) != 0 {
		t.Fatal("wallet locks leaked")
	}
}
