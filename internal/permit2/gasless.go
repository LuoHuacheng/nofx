package permit2

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common/hexutil"
	gethmath "github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

const GasSponsorExtension = "eip2612GasSponsoring"
const BaseUSDC = "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"

type GaslessPermit struct {
	From      string `json:"from"`
	Asset     string `json:"asset"`
	Spender   string `json:"spender"`
	Amount    string `json:"amount"`
	Nonce     string `json:"nonce"`
	Deadline  string `json:"deadline"`
	Signature string `json:"signature"`
	Version   string `json:"version"`
}

// USDC permit is distinct from the Permit2 witness. Only the canonical Base
// USDC domain and Permit2 spender are accepted; the value is the call ceiling,
// never an unlimited allowance. No transaction is sent by this package.
func GaslessTypedData(owner string, amount, nonce, deadline *big.Int) (apitypes.TypedData, error) {
	if !ValidAddress(owner) {
		return apitypes.TypedData{}, fmt.Errorf("invalid permit owner")
	}
	for i, n := range []*big.Int{amount, nonce, deadline} {
		if n == nil || n.Sign() < 0 || n.BitLen() > 256 || (i != 1 && n.Sign() == 0) {
			return apitypes.TypedData{}, fmt.Errorf("invalid bounded permit value")
		}
	}
	return apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": {{Name: "name", Type: "string"}, {Name: "version", Type: "string"}, {Name: "chainId", Type: "uint256"}, {Name: "verifyingContract", Type: "address"}},
			"Permit":       {{Name: "owner", Type: "address"}, {Name: "spender", Type: "address"}, {Name: "value", Type: "uint256"}, {Name: "nonce", Type: "uint256"}, {Name: "deadline", Type: "uint256"}},
		},
		PrimaryType: "Permit",
		Domain:      apitypes.TypedDataDomain{Name: "USD Coin", Version: "2", ChainId: gethmath.NewHexOrDecimal256(8453), VerifyingContract: BaseUSDC},
		Message:     apitypes.TypedDataMessage{"owner": owner, "spender": Address, "value": amount.String(), "nonce": nonce.String(), "deadline": deadline.String()},
	}, nil
}

// SignGaslessPermit performs read-only nonce/domain checks, then signs the
// standard x402 sponsorship extension for an already validated payment cap.
func SignGaslessPermit(ctx context.Context, endpoint string, key *ecdsa.PrivateKey, amount, deadline *big.Int) (GaslessPermit, error) {
	if key == nil {
		return GaslessPermit{}, fmt.Errorf("missing permit signer")
	}
	owner := crypto.PubkeyToAddress(key.PublicKey).Hex()
	var chain string
	if err := readRPC(ctx, endpoint, "eth_chainId", nil, &chain); err != nil || chain != "0x2105" {
		return GaslessPermit{}, fmt.Errorf("gasless permit RPC must be Base mainnet")
	}
	read := func(data string) (string, error) {
		var result string
		err := readRPC(ctx, endpoint, "eth_call", []any{map[string]string{"to": BaseUSDC, "data": data}, "latest"}, &result)
		return result, err
	}
	nonceHex, err := read("0x7ecebe00" + strings.Repeat("0", 24) + strings.TrimPrefix(strings.ToLower(owner), "0x"))
	if err != nil {
		return GaslessPermit{}, err
	}
	nonce, err := Uint256(nonceHex)
	if err != nil {
		return GaslessPermit{}, err
	}
	td, err := GaslessTypedData(owner, amount, nonce, deadline)
	if err != nil {
		return GaslessPermit{}, err
	}
	domainHex, err := read("0x3644e515")
	if err != nil {
		return GaslessPermit{}, err
	}
	domain, err := hexutil.Decode(domainHex)
	if err != nil {
		return GaslessPermit{}, fmt.Errorf("invalid USDC domain")
	}
	expected, err := td.HashStruct("EIP712Domain", td.Domain.Map())
	if err != nil || !bytes.Equal(domain, expected) {
		return GaslessPermit{}, fmt.Errorf("Base USDC permit domain mismatch")
	}
	hash, _, err := apitypes.TypedDataAndHash(td)
	if err != nil {
		return GaslessPermit{}, err
	}
	sig, err := crypto.Sign(hash, key)
	if err != nil {
		return GaslessPermit{}, err
	}
	sig[64] += 27
	return GaslessPermit{From: owner, Asset: BaseUSDC, Spender: Address, Amount: amount.String(), Nonce: nonce.String(), Deadline: deadline.String(), Signature: hexutil.Encode(sig), Version: "1"}, nil
}
