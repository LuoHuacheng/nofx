// Package permit2 implements the x402 EVM upto signing envelope.
// Protocol: x402-foundation/x402 specs/schemes/upto/scheme_upto_evm.md.
// It never sends transactions. Gas sponsorship signs a bounded USDC permit
// for the facilitator to submit with settlement; it never grants unlimited allowance.
package permit2

import (
	"bytes"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	gethmath "github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

const Address = "0x000000000022D473030F116dDEE9F6B43aC78BA3"

// CheckSettlementCall binds a mined transaction to this exact authorization,
// including its nonce. A matching Transfer log alone could be an old payment.
func CheckSettlementCall(data []byte, a Authorization, amount *big.Int) error {
	if len(data) < 4 {
		return fmt.Errorf("missing Permit2 settlement calldata")
	}
	start := 4
	settle := crypto.Keccak256([]byte("settle(((address,uint256),uint256,uint256),uint256,address,(address,address,uint256),bytes)"))[:4]
	withPermit := crypto.Keccak256([]byte("settleWithPermit((uint256,uint256,bytes32,bytes32,uint8),((address,uint256),uint256,uint256),uint256,address,(address,address,uint256),bytes)"))[:4]
	switch {
	case bytes.Equal(data[:4], settle):
	case bytes.Equal(data[:4], withPermit):
		start += 5 * 32
	default:
		return fmt.Errorf("unrecognized Permit2 settlement method")
	}
	if len(data) < start+10*32 || amount == nil || amount.Sign() < 0 {
		return fmt.Errorf("invalid Permit2 settlement calldata")
	}
	cap, err := Uint256(a.Permitted.Amount)
	if err != nil {
		return err
	}
	nonce, err := Uint256(a.Nonce)
	if err != nil {
		return err
	}
	deadline, err := Uint256(a.Deadline)
	if err != nil {
		return err
	}
	after, err := Uint256(a.Witness.ValidAfter)
	if err != nil {
		return err
	}
	expected := [][]byte{
		common.LeftPadBytes(common.HexToAddress(a.Permitted.Token).Bytes(), 32),
		common.LeftPadBytes(cap.Bytes(), 32), common.LeftPadBytes(nonce.Bytes(), 32), common.LeftPadBytes(deadline.Bytes(), 32),
		common.LeftPadBytes(amount.Bytes(), 32), common.LeftPadBytes(common.HexToAddress(a.From).Bytes(), 32),
		common.LeftPadBytes(common.HexToAddress(a.Witness.To).Bytes(), 32),
		common.LeftPadBytes(common.HexToAddress(a.Witness.Facilitator).Bytes(), 32), common.LeftPadBytes(after.Bytes(), 32),
	}
	for i, word := range expected {
		if !bytes.Equal(word, data[start+i*32:start+(i+1)*32]) {
			return fmt.Errorf("Permit2 settlement authorization mismatch")
		}
	}
	return nil
}

const UptoProxy = "0x4020A4f3b7b90ccA423B9fabCc0CE57C6C240002"

type Permissions struct {
	Token  string `json:"token"`
	Amount string `json:"amount"`
}
type Witness struct {
	To          string `json:"to"`
	Facilitator string `json:"facilitator"`
	ValidAfter  string `json:"validAfter"`
}
type Authorization struct {
	From      string      `json:"from"`
	Permitted Permissions `json:"permitted"`
	Spender   string      `json:"spender"`
	Nonce     string      `json:"nonce"`
	Deadline  string      `json:"deadline"`
	Witness   Witness     `json:"witness"`
}
type Payload struct {
	Signature     string        `json:"signature"`
	Authorization Authorization `json:"permit2Authorization"`
}

func ValidAddress(s string) bool {
	return common.IsHexAddress(s) && common.HexToAddress(s) != (common.Address{})
}
func Uint256(s string) (*big.Int, error) {
	base := 10
	if strings.HasPrefix(s, "0x") {
		base = 16
		s = s[2:]
	}
	if s == "" {
		return nil, fmt.Errorf("missing uint256")
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9') && !(base == 16 && (c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F')) {
			return nil, fmt.Errorf("invalid uint256")
		}
	}
	n, ok := new(big.Int).SetString(s, base)
	if !ok || n.Sign() < 0 || n.BitLen() > 256 {
		return nil, fmt.Errorf("invalid uint256")
	}
	return n, nil
}

func TypedData(chainID int64, a Authorization) (apitypes.TypedData, error) {
	if chainID <= 0 || !ValidAddress(a.From) || !ValidAddress(a.Permitted.Token) ||
		!ValidAddress(a.Witness.To) || !ValidAddress(a.Witness.Facilitator) ||
		!strings.EqualFold(a.Spender, UptoProxy) {
		return apitypes.TypedData{}, fmt.Errorf("invalid Permit2 domain or address")
	}
	nums := make([]string, 4)
	for i, s := range []string{a.Permitted.Amount, a.Nonce, a.Deadline, a.Witness.ValidAfter} {
		n, err := Uint256(s)
		if err != nil {
			return apitypes.TypedData{}, err
		}
		nums[i] = n.String()
	}
	return apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain":              {{Name: "name", Type: "string"}, {Name: "chainId", Type: "uint256"}, {Name: "verifyingContract", Type: "address"}},
			"PermitWitnessTransferFrom": {{Name: "permitted", Type: "TokenPermissions"}, {Name: "spender", Type: "address"}, {Name: "nonce", Type: "uint256"}, {Name: "deadline", Type: "uint256"}, {Name: "witness", Type: "Witness"}},
			"TokenPermissions":          {{Name: "token", Type: "address"}, {Name: "amount", Type: "uint256"}},
			"Witness":                   {{Name: "to", Type: "address"}, {Name: "facilitator", Type: "address"}, {Name: "validAfter", Type: "uint256"}},
		},
		PrimaryType: "PermitWitnessTransferFrom",
		Domain:      apitypes.TypedDataDomain{Name: "Permit2", ChainId: gethmath.NewHexOrDecimal256(chainID), VerifyingContract: Address},
		Message: apitypes.TypedDataMessage{
			"permitted": map[string]interface{}{"token": a.Permitted.Token, "amount": nums[0]},
			"spender":   a.Spender, "nonce": nums[1], "deadline": nums[2],
			"witness": map[string]interface{}{"to": a.Witness.To, "facilitator": a.Witness.Facilitator, "validAfter": nums[3]},
		},
	}, nil
}
func Sign(key *ecdsa.PrivateKey, chainID int64, a Authorization) (Payload, error) {
	if key == nil || !strings.EqualFold(crypto.PubkeyToAddress(key.PublicKey).Hex(), a.From) {
		return Payload{}, fmt.Errorf("Permit2 signer mismatch")
	}
	// Facilitator JSON schemas require decimal text for this uint256.
	// EIP-3009 exact nonces remain bytes32 hex in their separate signer.
	nonce, err := Uint256(a.Nonce)
	if err != nil {
		return Payload{}, err
	}
	a.Nonce = nonce.String()
	td, err := TypedData(chainID, a)
	if err != nil {
		return Payload{}, err
	}
	hash, _, err := apitypes.TypedDataAndHash(td)
	if err != nil {
		return Payload{}, err
	}
	sig, err := crypto.Sign(hash, key)
	if err != nil {
		return Payload{}, err
	}
	sig[64] += 27
	return Payload{Signature: hexutil.Encode(sig), Authorization: a}, nil
}
func Verify(chainID int64, p Payload) error {
	td, err := TypedData(chainID, p.Authorization)
	if err != nil {
		return err
	}
	hash, _, err := apitypes.TypedDataAndHash(td)
	if err != nil {
		return err
	}
	sig, err := hexutil.Decode(p.Signature)
	if err != nil || len(sig) != crypto.SignatureLength {
		return fmt.Errorf("invalid Permit2 signature")
	}
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	pub, err := crypto.SigToPub(hash, sig)
	if err != nil {
		return err
	}
	if !strings.EqualFold(crypto.PubkeyToAddress(*pub).Hex(), p.Authorization.From) {
		return fmt.Errorf("Permit2 signer mismatch")
	}
	return nil
}
