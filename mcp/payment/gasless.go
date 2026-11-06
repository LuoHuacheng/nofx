package payment

import (
	"context"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"nofx/internal/permit2"
)

// USDC permit nonces are sequential. Keep calls sharing a fee wallet serial
// through receipt verification, including across client instances, so two
// inference calls cannot race on one approval nonce. Different wallets do not
// block each other. Cross-process sharing still requires distinct wallets.
var feeWalletLocks = struct {
	sync.Mutex
	entries map[string]*feeWalletLock
}{entries: make(map[string]*feeWalletLock)}

type feeWalletLock struct {
	sync.Mutex
	refs int
}

func lockFeeWallet(owner string) func() {
	owner = strings.ToLower(owner)
	feeWalletLocks.Lock()
	entry := feeWalletLocks.entries[owner]
	if entry == nil {
		entry = &feeWalletLock{}
		feeWalletLocks.entries[owner] = entry
	}
	entry.refs++
	feeWalletLocks.Unlock()
	entry.Lock()
	return func() {
		entry.Unlock()
		feeWalletLocks.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(feeWalletLocks.entries, owner)
		}
		feeWalletLocks.Unlock()
	}
}

// signSponsoredPayment validates the entire payment before granting any
// allowance. The configured fee-wallet key signs off-chain only. CDP submits
// the permit together with settlement; the wallet needs USDC but no Base ETH.
func signSponsoredPayment(key *ecdsa.PrivateKey, owner string, option X402AcceptOption, required X402v2PaymentRequired) (string, error) {
	signed, err := SignX402Payment(key, owner, option, required.Resource)
	if err != nil {
		return signed, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	amount, _ := permit2.Uint256(option.Amount) // validated by SignX402Payment
	balance, err := permit2.USDCBalance(ctx, receiptRPC(), owner)
	if err != nil {
		return "", fmt.Errorf("cannot check quoted USDC payment balance; no payment submitted: %w", err)
	}
	if balance.Cmp(amount) < 0 {
		balanceUSD, _ := new(big.Rat).SetFrac(balance, big.NewInt(1_000_000)).Float64()
		neededUSD, _ := new(big.Rat).SetFrac(amount, big.NewInt(1_000_000)).Float64()
		return "", &ErrInsufficientFunds{Address: owner, Balance: balanceUSD, Needed: neededUSD}
	}
	if option.Scheme != "upto" {
		return signed, nil
	}
	allowance, err := permit2.Allowance(ctx, receiptRPC(), owner)
	if err != nil {
		return "", fmt.Errorf("cannot check Permit2 allowance; no payment submitted: %w", err)
	}
	if allowance.Cmp(amount) >= 0 {
		return signed, nil
	}
	var declaration struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := json.Unmarshal(required.Extensions[permit2.GasSponsorExtension], &declaration); err != nil || declaration.Info.Version != "1" {
		return "", fmt.Errorf("gateway does not offer gasless Permit2 sponsorship; no payment submitted (Base ETH is not required by this client)")
	}
	raw, err := X402DecodeHeader(signed)
	if err != nil {
		return "", err
	}
	var envelope map[string]json.RawMessage
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return "", err
	}
	var payload permit2.Payload
	if err = json.Unmarshal(envelope["payload"], &payload); err != nil {
		return "", err
	}
	deadline, err := permit2.Uint256(payload.Authorization.Deadline)
	if err != nil {
		return "", err
	}
	info, err := permit2.SignGaslessPermit(ctx, receiptRPC(), key, amount, deadline)
	if err != nil {
		return "", fmt.Errorf("cannot prepare gasless allowance; no payment submitted: %w", err)
	}
	if !deadline.IsInt64() || time.Now().Unix() >= deadline.Int64() {
		return "", fmt.Errorf("gasless payment authorization expired; no payment submitted")
	}
	envelope["extensions"], err = json.Marshal(map[string]any{permit2.GasSponsorExtension: map[string]any{"info": info}})
	if err != nil {
		return "", err
	}
	raw, err = json.Marshal(envelope)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}
