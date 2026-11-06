package permit2

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	ethTypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// VerifySettlement independently checks a confirmed Base transfer and the exact
// Permit2 authorization used by the transaction. It never writes to the chain.
func VerifySettlement(ctx context.Context, endpoint, tx string, auth Authorization, amount *big.Int) error {
	if strings.TrimSpace(endpoint) == "" {
		return fmt.Errorf("receipt RPC required")
	}
	hash, decodeErr := hex.DecodeString(strings.TrimPrefix(tx, "0x"))
	if !strings.HasPrefix(tx, "0x") || decodeErr != nil || len(hash) != 32 || common.BytesToHash(hash) == (common.Hash{}) {
		return fmt.Errorf("invalid settlement transaction hash")
	}
	if amount == nil || amount.Sign() <= 0 || amount.BitLen() > 256 {
		return fmt.Errorf("invalid settlement amount")
	}
	var chainHex string
	if err := readRPC(ctx, endpoint, "eth_chainId", nil, &chainHex); err != nil || chainHex != "0x2105" {
		return fmt.Errorf("receipt RPC chain mismatch or unavailable")
	}
	chain := big.NewInt(8453)
	var receipt *ethTypes.Receipt
	// Retry only read-only confirmation queries for the same transaction. A
	// recently settled Base transaction may not yet be indexed by this RPC.
	for attempt := 0; attempt < 5; attempt++ {
		err := readRPC(ctx, endpoint, "eth_getTransactionReceipt", []any{tx}, &receipt)
		if err == nil && receipt != nil {
			break
		}
		if attempt == 4 {
			return fmt.Errorf("upto settlement receipt unavailable")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	if err := CheckTransferReceipt(receipt, tx, auth, amount); err != nil {
		return err
	}
	var transaction *ethTypes.Transaction
	err := readRPC(ctx, endpoint, "eth_getTransactionByHash", []any{tx}, &transaction)
	if err != nil || transaction == nil || transaction.Hash() != receipt.TxHash || transaction.To() == nil || *transaction.To() != common.HexToAddress(UptoProxy) || transaction.ChainId().Cmp(chain) != 0 {
		return fmt.Errorf("upto settlement transaction is not a confirmed Permit2 proxy call")
	}
	sender, err := ethTypes.Sender(ethTypes.LatestSignerForChainID(chain), transaction)
	if err != nil || sender != common.HexToAddress(auth.Witness.Facilitator) {
		return fmt.Errorf("upto settlement transaction facilitator mismatch")
	}
	return CheckSettlementCall(transaction.Data(), auth, amount)
}

// CheckRPC is a read-only deployment preflight for chain ID and both contracts.
func CheckRPC(ctx context.Context, endpoint string) error {
	if strings.TrimSpace(endpoint) == "" {
		return fmt.Errorf("receipt RPC not configured")
	}
	var chain string
	if err := readRPC(ctx, endpoint, "eth_chainId", nil, &chain); err != nil || chain != "0x2105" {
		return fmt.Errorf("receipt RPC must be Base mainnet")
	}
	for _, address := range []string{Address, UptoProxy} {
		var code string
		if err := readRPC(ctx, endpoint, "eth_getCode", []any{address, "latest"}, &code); err != nil || len(code) <= 2 {
			return fmt.Errorf("Permit2 contract unavailable")
		}
	}
	return nil
}

// USDCBalance reads the quoted payment asset, not native ETH. An unavailable
// or wrong-chain RPC must not be interpreted as a funded wallet.
func USDCBalance(ctx context.Context, endpoint, owner string) (*big.Int, error) {
	if !ValidAddress(owner) {
		return nil, fmt.Errorf("invalid USDC payer")
	}
	var chain string
	if err := readRPC(ctx, endpoint, "eth_chainId", nil, &chain); err != nil || chain != "0x2105" {
		return nil, fmt.Errorf("cannot verify Base USDC balance")
	}
	data := "0x70a08231" + strings.Repeat("0", 24) + strings.TrimPrefix(strings.ToLower(owner), "0x")
	var result string
	if err := readRPC(ctx, endpoint, "eth_call", []any{map[string]string{"to": "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913", "data": data}, "latest"}, &result); err != nil {
		return nil, err
	}
	return Uint256(result)
}

// Allowance reads the paying EOA's existing USDC allowance. No approve or
// transaction-sending method is exposed here.
func Allowance(ctx context.Context, endpoint, owner string) (*big.Int, error) {
	if !ValidAddress(owner) {
		return nil, fmt.Errorf("invalid Permit2 payer")
	}
	var chain string
	if err := readRPC(ctx, endpoint, "eth_chainId", nil, &chain); err != nil || chain != "0x2105" {
		return nil, fmt.Errorf("cannot verify Base Permit2 allowance")
	}
	data := "0xdd62ed3e" + strings.Repeat("0", 24) + strings.TrimPrefix(strings.ToLower(owner), "0x") + strings.Repeat("0", 24) + strings.TrimPrefix(strings.ToLower(Address), "0x")
	var result string
	if err := readRPC(ctx, endpoint, "eth_call", []any{map[string]string{"to": "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913", "data": data}, "latest"}, &result); err != nil {
		return nil, err
	}
	return Uint256(result)
}

// Strictly read-only RPC methods; no dependency on a signer or sending client.
func readRPC(ctx context.Context, endpoint, method string, params []any, out any) error {
	switch method {
	case "eth_chainId", "eth_getCode", "eth_getTransactionReceipt", "eth_getTransactionByHash", "eth_call":
	default:
		return fmt.Errorf("unsupported receipt RPC method")
	}
	if params == nil {
		params = []any{}
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("invalid receipt RPC URL")
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("receipt RPC request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("receipt RPC returned status %d", resp.StatusCode)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 4*1024*1024)).Decode(&envelope); err != nil {
		return fmt.Errorf("invalid receipt RPC response")
	}
	if len(envelope.Error) > 0 && string(envelope.Error) != "null" || len(envelope.Result) == 0 {
		return fmt.Errorf("receipt RPC result unavailable")
	}
	return json.Unmarshal(envelope.Result, out)
}

func CheckTransferReceipt(receipt *ethTypes.Receipt, tx string, auth Authorization, amount *big.Int) error {
	if amount == nil || amount.Sign() <= 0 || amount.BitLen() > 256 {
		return fmt.Errorf("invalid settlement amount")
	}
	if receipt == nil || receipt.Status != ethTypes.ReceiptStatusSuccessful || receipt.BlockNumber == nil ||
		receipt.TxHash != common.HexToHash(tx) {
		return fmt.Errorf("upto settlement transaction not confirmed successfully")
	}
	transfer := crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	total := new(big.Int)
	for _, log := range receipt.Logs {
		if log == nil || log.Removed || log.Address != common.HexToAddress(auth.Permitted.Token) || len(log.Topics) != 3 ||
			log.Topics[0] != transfer || len(log.Data) != 32 {
			continue
		}
		from := common.BytesToAddress(log.Topics[1].Bytes())
		to := common.BytesToAddress(log.Topics[2].Bytes())
		if from == common.HexToAddress(auth.From) && to == common.HexToAddress(auth.Witness.To) {
			total.Add(total, new(big.Int).SetBytes(log.Data))
		}
	}
	if total.Cmp(amount) != 0 {
		return fmt.Errorf("upto settlement on-chain amount mismatch")
	}
	return nil
}
