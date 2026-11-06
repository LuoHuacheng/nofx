package permit2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

func TestGaslessPermitMatchesBrowserGoldenVector(t *testing.T) {
	// Public unfunded fixture key. Never used for network calls.
	key, _ := crypto.HexToECDSA("0000000000000000000000000000000000000000000000000000000000000001")
	owner := crypto.PubkeyToAddress(key.PublicKey).Hex()
	td, err := GaslessTypedData(owner, big.NewInt(11099), big.NewInt(7), big.NewInt(1791936660))
	if err != nil {
		t.Fatal(err)
	}
	hash, _, err := apitypes.TypedDataAndHash(td)
	if err != nil || hexutil.Encode(hash) != "0x94e4c631945a8640ab16dfeabd05377c11c1b2a4a1b3de7645e6dbd31fd5953d" {
		t.Fatalf("browser digest differs: %x (%v)", hash, err)
	}
	for _, amount := range []*big.Int{nil, big.NewInt(-1), new(big.Int), new(big.Int).Lsh(big.NewInt(1), 256)} {
		if _, err := GaslessTypedData(owner, amount, big.NewInt(7), big.NewInt(1791936660)); err == nil {
			t.Fatal("invalid permit amount accepted")
		}
	}
}
