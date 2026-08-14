package state

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// TestHookedStateDB_EmitLogsForBurnAccountsIsForwarded pins the one-line
// forwarding method hookedStateDB.EmitLogsForBurnAccounts in
// core/state/statedb_hooked.go.
//
// EmitLogsForBurnAccounts is a Sonic addition to the vm.StateDB interface. The
// hooked wrapper has to forward it to the inner state, and because the method is
// a single line in a file upstream regularly restructures, it is easy to lose in
// a rebase - with no compile error, since an empty body still satisfies the
// interface, and no visible symptom other than missing burn logs when tracing.
func TestHookedStateDB_EmitLogsForBurnAccountsIsForwarded(t *testing.T) {
	address := common.Address{0xaa}

	inner, err := New(types.EmptyRootHash, NewDatabaseForTesting())
	if err != nil {
		t.Fatalf("failed to create stateDB: %v", err)
	}
	inner.SetTxContext(common.Hash{0x01}, 0)

	// Set up an account that self-destructed and then received funds again,
	// which is the corner case EmitLogsForBurnAccounts reports on.
	inner.AddBalance(address, uint256.NewInt(100), tracing.BalanceChangeUnspecified)
	inner.CreateContract(address)
	inner.SelfDestruct(address)
	inner.AddBalance(address, uint256.NewInt(50), tracing.BalanceChangeUnspecified)

	hooked := NewHookedState(inner, &tracing.Hooks{})
	hooked.EmitLogsForBurnAccounts()

	logs := inner.Logs()
	if len(logs) != 1 {
		t.Fatalf("expected the inner state to have recorded 1 burn log, got %d; "+
			"hookedStateDB.EmitLogsForBurnAccounts does not forward to the inner state",
			len(logs))
	}
	if got, want := logs[0].Topics[0], params.EthBurnLogEvent; got != want {
		t.Errorf("expected an ETH burn log with topic %v, got %v", want, got)
	}
	if got, want := logs[0].Topics[1], common.BytesToHash(address.Bytes()); got != want {
		t.Errorf("expected the burn log to name %v, got %v", want, got)
	}
}
