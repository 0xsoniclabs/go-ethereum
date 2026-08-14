package core

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/stateless"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// excessGasChargeSites enumerates fork rule sets that each select a different
// one of the two st.chargeExcessGas call sites in stateTransition.execute: the
// pre-Prague site, and the one inside the Prague branch after the EIP-7623
// floor-gas adjustment. Every ChargeExcessGas test runs against both so that a
// rebase dropping either call site fails a test.
var excessGasChargeSites = map[string]*params.ChainConfig{
	"pre-Prague": chainConfigUpTo(cancun),
	"Prague":     chainConfigUpTo(prague),
}

func TestStateTransition_EnablingExcessGasChargingEnablesExcessGasCharging(t *testing.T) {
	for name, chainConfig := range excessGasChargeSites {
		t.Run(name, func(t *testing.T) {
			msg := &Message{
				From:     common.Address{12},
				GasLimit: 100_000,
			}

			config := vm.Config{
				ChargeExcessGas: false,
			}

			resultWithoutCharge, err := runTestTransactionOnChain(msg, config, chainConfig)
			if err != nil {
				t.Fatalf("Error running transaction: %v", err)
			}

			config.ChargeExcessGas = true
			resultWithCharge, err := runTestTransactionOnChain(msg, config, chainConfig)
			if err != nil {
				t.Fatalf("Error running transaction: %v", err)
			}

			// When enabled, 10% of the excess gas is charged
			want := (msg.GasLimit - resultWithoutCharge.UsedGas) / 10
			diff := resultWithCharge.UsedGas - resultWithoutCharge.UsedGas
			if diff != want {
				t.Fatalf("Expected difference in gas usage to be %d, got %d; "+
					"the vm.Config.ChargeExcessGas hook (stateTransition.chargeExcessGas, "+
					"called from stateTransition.execute) is not effective under %s rules",
					want, diff, name)
			}
		})
	}
}

func TestStateTransition_ExcessiveGasChargesAreIgnoredForTheZeroSender(t *testing.T) {
	for name, chainConfig := range excessGasChargeSites {
		t.Run(name, func(t *testing.T) {
			msg := &Message{
				From:     common.Address{0},
				GasLimit: 100_000,
			}

			config := vm.Config{
				ChargeExcessGas: false,
			}

			resultWithoutCharge, err := runTestTransactionOnChain(msg, config, chainConfig)
			if err != nil {
				t.Fatalf("Error running transaction: %v", err)
			}

			config.ChargeExcessGas = true
			resultWithCharge, err := runTestTransactionOnChain(msg, config, chainConfig)
			if err != nil {
				t.Fatalf("Error running transaction: %v", err)
			}

			if resultWithCharge.UsedGas != resultWithoutCharge.UsedGas {
				t.Fatalf("Expected gas usage to be the same, got %d and %d; "+
					"the zero-sender exemption in stateTransition.chargeExcessGas was lost",
					resultWithoutCharge.UsedGas, resultWithCharge.UsedGas)
			}
		})
	}
}

// TestStateTransition_ExcessGasIsChargedAfterTheFloorGasAdjustment pins the
// ordering of the Prague-path st.chargeExcessGas call site relative to the
// EIP-7623 floor-gas adjustment. Charging before the adjustment would be
// silently absorbed by the floor whenever the floor exceeds the gas actually
// used, making the ChargeExcessGas hook inert for data-heavy transactions.
func TestStateTransition_ExcessGasIsChargedAfterTheFloorGasAdjustment(t *testing.T) {
	const gasLimit = uint64(100_000)

	// 100 non-zero data bytes: intrinsic gas 21000+100*16 = 22_600, while the
	// EIP-7623 floor is 21000+100*4*10 = 25_000. The floor therefore dominates.
	data := bytes.Repeat([]byte{1}, 100)

	chainConfig := chainConfigUpTo(prague)
	rules := chainConfig.Rules(big.NewInt(0), true, 0)

	var (
		from  = common.Address{12}
		to    = &common.Address{14}
		value = uint256.NewInt(0)
	)

	floorDataGas, err := FloorDataGas(rules, from, to, value, data, nil)
	if err != nil {
		t.Fatalf("failed to compute floor data gas: %v", err)
	}

	newMessage := func() *Message {
		return &Message{
			From:     from,
			GasLimit: gasLimit,
			Data:     data,
		}
	}

	intrinsicGas, err := IntrinsicGas(data, nil, nil, from, to, value, rules)
	if err != nil {
		t.Fatalf("failed to compute intrinsic gas: %v", err)
	}
	if intrinsicGas >= floorDataGas {
		t.Fatalf("test setup is ineffective: intrinsic gas %d must stay below the "+
			"floor data gas %d for the ordering to be observable", intrinsicGas, floorDataGas)
	}

	baseline, err := runTestTransactionOnChain(newMessage(), vm.Config{}, chainConfig)
	if err != nil {
		t.Fatalf("Error running transaction: %v", err)
	}
	if baseline.UsedGas != floorDataGas {
		t.Fatalf("expected the floor data gas %d to dominate, got %d", floorDataGas, baseline.UsedGas)
	}

	result, err := runTestTransactionOnChain(newMessage(), vm.Config{ChargeExcessGas: true}, chainConfig)
	if err != nil {
		t.Fatalf("Error running transaction: %v", err)
	}

	chargedAfterFloor := floorDataGas + (gasLimit-floorDataGas)/10
	chargedBeforeFloor := intrinsicGas + (gasLimit-intrinsicGas)/10

	if result.UsedGas == chargedBeforeFloor {
		t.Fatalf("gas usage %d shows excess gas being charged before the EIP-7623 "+
			"floor-gas adjustment; the second st.chargeExcessGas call in "+
			"stateTransition.execute must stay after the floor adjustment",
			result.UsedGas)
	}
	if result.UsedGas != chargedAfterFloor {
		t.Fatalf("expected gas usage %d (excess gas charged after the floor-gas "+
			"adjustment), got %d", chargedAfterFloor, result.UsedGas)
	}
}

// TestStateTransition_MaxTxGasOverridesTheProtocolCap pins the
// vm.Config.MaxTxGas hook in stateTransition.preCheck, which replaces the
// EIP-7825 params.MaxTxGas constant with a Sonic-configured value.
func TestStateTransition_MaxTxGasOverridesTheProtocolCap(t *testing.T) {
	const belowProtocolCap = uint64(1_000_000)
	aboveProtocolCap := 2 * params.MaxTxGas

	tests := map[string]struct {
		fork      fork
		maxTxGas  *uint64
		gasLimit  uint64
		wantError error
	}{
		"unset, at the protocol cap": {
			fork:     osaka,
			gasLimit: params.MaxTxGas,
		},
		"unset, above the protocol cap": {
			fork:      osaka,
			gasLimit:  params.MaxTxGas + 1,
			wantError: ErrGasLimitTooHigh,
		},
		"custom cap below the protocol cap, at the custom cap": {
			fork:     osaka,
			maxTxGas: asPointer(belowProtocolCap),
			gasLimit: belowProtocolCap,
		},
		"custom cap below the protocol cap, above the custom cap": {
			fork:      osaka,
			maxTxGas:  asPointer(belowProtocolCap),
			gasLimit:  belowProtocolCap + 1,
			wantError: ErrGasLimitTooHigh,
		},
		// This case proves that MaxTxGas replaces params.MaxTxGas rather than
		// being combined with it: the gas limit exceeds the protocol constant
		// but stays within the configured cap.
		"custom cap above the protocol cap, between the two caps": {
			fork:     osaka,
			maxTxGas: asPointer(aboveProtocolCap),
			gasLimit: params.MaxTxGas + 1,
		},
		"custom cap above the protocol cap, above the custom cap": {
			fork:      osaka,
			maxTxGas:  asPointer(aboveProtocolCap),
			gasLimit:  aboveProtocolCap + 1,
			wantError: ErrGasLimitTooHigh,
		},
		// Pre-Osaka the EIP-7825 cap does not exist at all, so neither the
		// protocol constant nor the override may be applied.
		"pre-Osaka, unset, above the protocol cap": {
			fork:     prague,
			gasLimit: params.MaxTxGas + 1,
		},
		"pre-Osaka, custom cap, above the custom cap": {
			fork:     prague,
			maxTxGas: asPointer(belowProtocolCap),
			gasLimit: belowProtocolCap + 1,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			msg := &Message{
				From:     common.Address{12},
				GasLimit: test.gasLimit,
			}
			config := vm.Config{
				MaxTxGas: test.maxTxGas,
			}

			_, err := runTestTransactionOnChain(msg, config, chainConfigUpTo(test.fork))
			if !errors.Is(err, test.wantError) {
				t.Fatalf("unexpected error: got %v, want %v; the vm.Config.MaxTxGas "+
					"hook in stateTransition.preCheck is not effective", err, test.wantError)
			}
		})
	}
}

// TestStateTransition_ANilGasPoolIsTolerated pins the nil-gas-pool substitution
// in ApplyMessage that Sonic relies on when executing a single message via RPC.
// Upstream adopted it in v1.17.5; the test stays to keep the behavior pinned.
func TestStateTransition_ANilGasPoolIsTolerated(t *testing.T) {
	const gasLimit = uint64(100_000)

	run := func(pool *GasPool) (result *ExecutionResult, err error) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ApplyMessage panicked with a nil gas pool: %v; the "+
					"gp == nil substitution in ApplyMessage was lost", r)
			}
		}()
		msg := &Message{
			From:     common.Address{12},
			GasLimit: gasLimit,
		}
		fillTestMessageDefaults(msg)
		evm := newTestEvm(vm.Config{}, &params.ChainConfig{}, &dummyStateDB{})
		return ApplyMessage(evm, msg, pool)
	}

	withNilPool, err := run(nil)
	if err != nil {
		t.Fatalf("Error running transaction with a nil gas pool: %v", err)
	}

	// The substituted pool must be sized exactly to the message gas limit, so
	// the outcome has to match an explicitly supplied pool of that size.
	withExactPool, err := run(NewGasPool(gasLimit))
	if err != nil {
		t.Fatalf("Error running transaction with an explicit gas pool: %v", err)
	}
	if withNilPool.UsedGas != withExactPool.UsedGas {
		t.Errorf("expected a nil gas pool to behave like NewGasPool(msg.GasLimit): "+
			"used gas %d != %d", withNilPool.UsedGas, withExactPool.UsedGas)
	}

	// One gas less does not suffice, which pins msg.GasLimit as the substituted
	// pool size rather than merely "large enough".
	if _, err := run(NewGasPool(gasLimit - 1)); !errors.Is(err, ErrGasLimitReached) {
		t.Errorf("expected a pool below msg.GasLimit to be rejected, got %v", err)
	}
}

func TestStateTransition_InsufficientBalanceCheckCanBeDisabled(t *testing.T) {
	msg := &Message{
		Value:    uint256.NewInt(2_000_000), // - all accounts have 1_000_000 in the dummy DB
		GasLimit: 100_000,
	}

	config := vm.Config{
		InsufficientBalanceIsNotAnError: false,
	}

	_, err := runTestTransaction(msg, config)
	if err == nil {
		t.Errorf("Expected error running transaction with not enough balance")
	}

	// When disabled, the transaction is still processed but reverted.
	config.InsufficientBalanceIsNotAnError = true
	result, err := runTestTransaction(msg, config)
	if err != nil {
		t.Errorf("Error running transaction: %v", err)
	}
	if errors.Is(result.Err, vm.ErrExecutionReverted) {
		t.Errorf("Expected error to be reverted, got %v", result.Err)
	}
}

// TestStateTransition_InsufficientFundsForTransferCheckCanBeDisabled covers the
// second of the two vm.Config.InsufficientBalanceIsNotAnError sites: clause 6 in
// stateTransition.execute. The first site, in stateTransition.buyGas, is covered
// by TestStateTransition_InsufficientBalanceCheckCanBeDisabled.
func TestStateTransition_InsufficientFundsForTransferCheckCanBeDisabled(t *testing.T) {
	msg := &Message{
		From:     common.Address{12},
		Value:    uint256.NewInt(1), // - affordable for buyGas, rejected by the transfer guard
		GasLimit: 100_000,
	}

	config := vm.Config{
		InsufficientBalanceIsNotAnError: false,
	}

	_, err := runTestTransactionWithFailingTransfers(msg, config)
	if !errors.Is(err, ErrInsufficientFundsForTransfer) {
		t.Fatalf("expected %v, got %v", ErrInsufficientFundsForTransfer, err)
	}

	// When disabled, the failing transfer is no longer a consensus error; the
	// transaction is processed and the transfer fails inside the EVM instead.
	config.InsufficientBalanceIsNotAnError = true
	result, err := runTestTransactionWithFailingTransfers(msg, config)
	if err != nil {
		t.Fatalf("Error running transaction: %v; the value-transfer check in "+
			"stateTransition.execute ignores vm.Config.InsufficientBalanceIsNotAnError", err)
	}
	if !errors.Is(result.Err, vm.ErrInsufficientBalance) {
		t.Errorf("expected the transfer to fail inside the EVM with %v, got %v",
			vm.ErrInsufficientBalance, result.Err)
	}
}

func TestStateTransition_GasFeeCapCanBeIgnored(t *testing.T) {
	msg := &Message{
		GasLimit:  100_000,
		GasFeeCap: uint256.NewInt(8),  // 1M in each account should be enough
		GasPrice:  uint256.NewInt(12), // 1M is not enough for this gas price
	}

	config := vm.Config{
		IgnoreGasFeeCap: false,
	}

	// By default, the gas fee cap is enforced and the transaction should pass.
	_, err := runTestTransaction(msg, config)
	if err != nil {
		t.Errorf("expected transaction to pass when enforcing gas fee cap")
	}

	// When ignoring the gas fee cap, the transaction should be too expensive.
	config.IgnoreGasFeeCap = true
	_, err = runTestTransaction(msg, config)
	if err == nil {
		t.Errorf("expected transaction to fail when ignoring gas fee cap")
	}
}

func TestStateTransition_GasTipPaymentToCoinbaseCanBeSkipped(t *testing.T) {
	msg := &Message{
		GasLimit: 100_000,
		GasPrice: uint256.NewInt(5),
	}

	config := vm.Config{
		SkipTipPaymentToCoinbase: false,
	}

	// By default, gas fees are send to the coinbase.
	result, balance, err := runTestTransactionAndGetBalance(msg, config, testCoinbase)
	if err != nil {
		t.Errorf("transaction failed: %v", err)
	}
	if got, want := balance.Uint64(), uint64(1_000_000+result.UsedGas*5); got != want {
		t.Errorf("expected coinbase balance to be %d, got %d", want, got)
	}

	// When disabled, transfers are skipped.
	config.SkipTipPaymentToCoinbase = true
	_, balance, err = runTestTransactionAndGetBalance(msg, config, testCoinbase)
	if err != nil {
		t.Errorf("transaction failed: %v", err)
	}
	if got, want := balance.Uint64(), uint64(1_000_000); got != want {
		t.Errorf("expected coinbase balance to be %d, got %d", want, got)
	}
}

///////////////////////////
// Helper functions

var testCoinbase = common.Address{15}

func runTestTransaction(msg *Message, config vm.Config) (*ExecutionResult, error) {
	return runTestTransactionOnChain(msg, config, &params.ChainConfig{})
}

// runTestTransactionOnChain runs a transaction under the given fork rules. Sonic
// hooks that are fork-gated - the two chargeExcessGas call sites and the
// Osaka-gated MaxTxGas cap - need this to pin every gated path.
func runTestTransactionOnChain(
	msg *Message,
	config vm.Config,
	chainConfig *params.ChainConfig,
) (*ExecutionResult, error) {
	return runTestTransactionOnStateDB(msg, config, chainConfig, &dummyStateDB{})
}

func runTestTransactionAndGetBalance(
	msg *Message,
	config vm.Config,
	address common.Address,
) (*ExecutionResult, *uint256.Int, error) {
	db := &dummyStateDB{accountToTrack: address}
	result, err := runTestTransactionOnStateDB(msg, config, &params.ChainConfig{}, db)
	return result, db.GetBalance(address), err
}

func runTestTransactionOnStateDB(
	msg *Message,
	config vm.Config,
	chainConfig *params.ChainConfig,
	db vm.StateDB,
) (*ExecutionResult, error) {
	fillTestMessageDefaults(msg)
	return ApplyMessage(newTestEvm(config, chainConfig, db), msg, NewGasPool(msg.GasLimit))
}

// runTestTransactionWithFailingTransfers runs the message in a block context
// whose CanTransfer guard rejects every transfer, which is what it takes to
// reach the value-transfer check in stateTransition.execute.
func runTestTransactionWithFailingTransfers(msg *Message, config vm.Config) (*ExecutionResult, error) {
	fillTestMessageDefaults(msg)
	evm := newTestEvm(config, &params.ChainConfig{}, &dummyStateDB{})
	evm.Context.CanTransfer = func(vm.StateDB, common.Address, *uint256.Int) bool { return false }
	return ApplyMessage(evm, msg, NewGasPool(msg.GasLimit))
}

func newTestEvm(config vm.Config, chainConfig *params.ChainConfig, db vm.StateDB) *vm.EVM {
	return vm.NewEVM(
		vm.BlockContext{
			Transfer:    func(vm.StateDB, common.Address, common.Address, *uint256.Int, *params.Rules) {},
			CanTransfer: func(vm.StateDB, common.Address, *uint256.Int) bool { return true },
			Coinbase:    testCoinbase,
			BlockNumber: big.NewInt(0),
			BaseFee:     big.NewInt(0),
			// A non-nil Random marks the chain as post-merge, without which
			// params.ChainConfig.Rules reports every timestamp fork as inactive.
			Random: &common.Hash{0x42},
		},
		db,
		chainConfig,
		config,
	)
}

func fillTestMessageDefaults(msg *Message) {
	if msg.To == nil {
		msg.To = &common.Address{14}
	}
	if msg.GasPrice == nil {
		msg.GasPrice = uint256.NewInt(0)
	}
	if msg.GasFeeCap == nil {
		msg.GasFeeCap = uint256.NewInt(0)
	}
	if msg.GasTipCap == nil {
		msg.GasTipCap = uint256.NewInt(0)
	}
	if msg.Value == nil {
		msg.Value = uint256.NewInt(0)
	}
}

///////////////////////////
// Fork configurations

// fork names a timestamp-activated fork used to select rule sets in tests.
type fork string

const (
	london    fork = "london"
	shanghai  fork = "shanghai"
	cancun    fork = "cancun"
	prague    fork = "prague"
	osaka     fork = "osaka"
	amsterdam fork = "amsterdam"
)

// forkOrder lists the supported forks in activation order.
var forkOrder = []fork{london, shanghai, cancun, prague, osaka, amsterdam}

// chainConfigUpTo returns a chain configuration in which all block-number forks
// and all timestamp forks up to and including the given fork are active, while
// all later forks remain inactive.
func chainConfigUpTo(target fork) *params.ChainConfig {
	zero := uint64(0)
	config := &params.ChainConfig{
		ChainID:                 big.NewInt(1),
		HomesteadBlock:          big.NewInt(0),
		EIP150Block:             big.NewInt(0),
		EIP155Block:             big.NewInt(0),
		EIP158Block:             big.NewInt(0),
		ByzantiumBlock:          big.NewInt(0),
		ConstantinopleBlock:     big.NewInt(0),
		PetersburgBlock:         big.NewInt(0),
		IstanbulBlock:           big.NewInt(0),
		BerlinBlock:             big.NewInt(0),
		LondonBlock:             big.NewInt(0),
		TerminalTotalDifficulty: big.NewInt(0),
	}
	for _, current := range forkOrder {
		switch current {
		case london: // enabled above, since it is block-number activated
		case shanghai:
			config.ShanghaiTime = &zero
		case cancun:
			config.CancunTime = &zero
		case prague:
			config.PragueTime = &zero
		case osaka:
			config.OsakaTime = &zero
		case amsterdam:
			config.AmsterdamTime = &zero
		}
		if current == target {
			return config
		}
	}
	panic(fmt.Sprintf("unknown fork %q, expected one of %v", target, forkOrder))
}

func asPointer[T any](value T) *T {
	return &value
}

///////////////////////////
// dummyStateDB

type dummyStateDB struct {
	vm.StateDB

	accountToTrack common.Address
	accountBalance *uint256.Int
}

func (*dummyStateDB) Exist(common.Address) bool {
	return true
}

func (db *dummyStateDB) GetBalance(addr common.Address) *uint256.Int {
	if addr == db.accountToTrack && db.accountBalance != nil {
		return db.accountBalance
	}
	return uint256.NewInt(1_000_000)
}

func (db *dummyStateDB) AddBalance(addr common.Address, value *uint256.Int, _ tracing.BalanceChangeReason) uint256.Int {
	if addr == db.accountToTrack {
		previous := db.GetBalance(addr)
		db.accountBalance = new(uint256.Int).Add(previous, value)
		return *previous
	}
	return uint256.Int{}
}

func (*dummyStateDB) SubBalance(common.Address, *uint256.Int, tracing.BalanceChangeReason) uint256.Int {
	// ignored
	return uint256.Int{}
}

func (*dummyStateDB) GetNonce(common.Address) uint64 {
	return 0
}

func (*dummyStateDB) SetNonce(common.Address, uint64, tracing.NonceChangeReason) {
	// ignored
}

func (*dummyStateDB) GetCodeHash(common.Address) common.Hash {
	return common.Hash{}
}

func (*dummyStateDB) GetCode(common.Address) []byte {
	return nil
}

func (*dummyStateDB) AddRefund(uint64) {
	// ignored
}
func (*dummyStateDB) SubRefund(uint64) {
	// ignored
}
func (*dummyStateDB) GetRefund() uint64 {
	return 0
}

func (*dummyStateDB) Prepare(params.Rules, common.Address, common.Address, *common.Address, []common.Address, types.AccessList) {
	// ignored
}

func (*dummyStateDB) Snapshot() int {
	return 0
}

func (*dummyStateDB) RevertToSnapshot(int) {
	// ignored
}

func (*dummyStateDB) Witness() *stateless.Witness {
	return nil
}
