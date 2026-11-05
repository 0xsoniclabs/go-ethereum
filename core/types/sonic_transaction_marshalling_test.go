package types

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/holiman/uint256"
)

func TestSonicBlobTx_DecodesFromJSONWithoutBlobHashes(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := SignNewTx(key, NewCancunSigner(big.NewInt(1)), &BlobTx{ChainID: uint256.NewInt(1)})
	if err != nil {
		t.Fatal(err)
	}

	// Sonic's RPC encodes the missing hashes as an empty list, which
	// go-ethereum's own encoder omits.
	data, err := json.Marshal(tx)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["blobVersionedHashes"] = []common.Hash{}
	if data, err = json.Marshal(fields); err != nil {
		t.Fatal(err)
	}

	var decoded Transaction
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to decode a blob transaction without blob hashes: %v", err)
	}
	if decoded.Hash() != tx.Hash() {
		t.Fatalf("decoded transaction hash %v, want %v", decoded.Hash(), tx.Hash())
	}
}
