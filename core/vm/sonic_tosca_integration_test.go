package vm

import (
	"errors"
	"testing"

	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

func TestGetInterpreter_ProducesInterpretersBasedOnConfiguration(t *testing.T) {
	var (
		a         = &evmInterpreter{}
		b         = &evmInterpreter{}
		none      InterpreterFactory
		useA      = func(*EVM) Interpreter { return a }
		useB      = func(*EVM) Interpreter { return b }
		A         = func(i Interpreter) bool { return i == a }
		B         = func(i Interpreter) bool { return i == b }
		Fresh     = func(i Interpreter) bool { return i != nil && i != a && i != b }
		noTracing = false
		Tracing   = true
	)

	// Defines a complete "truth" table for the GetInterpreter function.
	tests := []struct {
		tracing               bool
		interpreter           InterpreterFactory
		interpreterForTracing InterpreterFactory
		want                  func(Interpreter) bool
	}{
		// tracing, interpreter, interpreterForTracing, want
		{noTracing, none, none, Fresh},
		{noTracing, none, useA, Fresh},
		{noTracing, none, useB, Fresh},

		{noTracing, useA, none, A},
		{noTracing, useA, useA, A},
		{noTracing, useA, useB, A},

		{noTracing, useB, none, B},
		{noTracing, useB, useA, B},
		{noTracing, useB, useB, B},

		{Tracing, none, none, Fresh},
		{Tracing, none, useA, A},
		{Tracing, none, useB, B},

		{Tracing, useA, none, A},
		{Tracing, useA, useA, A},
		{Tracing, useA, useB, B},

		{Tracing, useB, none, B},
		{Tracing, useB, useA, A},
		{Tracing, useB, useB, B},
	}

	for i, test := range tests {
		config := Config{
			Interpreter:           test.interpreter,
			InterpreterForTracing: test.interpreterForTracing,
		}
		if test.tracing {
			config.Tracer = &tracing.Hooks{}
		}
		evm := &EVM{Config: config}
		got := getInterpreter(evm)
		if !test.want(got) {
			t.Errorf("unexpected interpreter, case %d -  isA: %t, isB: %t, isFresh: %t", i, A(got), B(got), Fresh(got))
		}
	}
}

func TestCustomCodeSize_MaxCodeSizeIsEnforcedWhenSet(t *testing.T) {
	customLimit := 50_000
	tests := map[string]struct {
		maxCodeSize   *int
		codeSize      uint64
		expectedError error
	}{
		"default codeSize 0": {
			codeSize: 0,
		},
		"default codeSize limit": {
			codeSize: params.MaxCodeSize,
		},
		"default codeSize above limit": {
			codeSize:      params.MaxCodeSize + 1,
			expectedError: ErrMaxCodeSizeExceeded,
		},
		"custom codeSize limit": {
			maxCodeSize: asPointer(customLimit),
			codeSize:    uint64(customLimit),
		},
		"custom codeSize above limit": {
			maxCodeSize:   asPointer(customLimit),
			codeSize:      uint64(customLimit + 1),
			expectedError: ErrMaxCodeSizeExceeded,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			stateDB, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
			if err != nil {
				t.Fatalf("failed to create stateDB: %v", err)
			}

			chainConfig := &params.ChainConfig{
				EIP158Block: big.NewInt(0),
			}
			blockContext := BlockContext{
				BlockNumber: big.NewInt(10),
				Random:      &common.Hash{0x42},
			}
			config := Config{
				MaxCodeSize: test.maxCodeSize,
			}
			evm := NewEVM(blockContext, stateDB, chainConfig, config)

			// The code to be deployed is the return of the init code,
			// so the init code just needs to return a byte array of the specified size.
			initCode := []byte{
				byte(PUSH3), byte(test.codeSize >> 16), // push code size
				byte(test.codeSize >> 8), byte(test.codeSize), // push code size
				byte(PUSH1), 0x00, // push memory offset
				byte(RETURN), // return
			}

			address := common.Address{1}
			contract := NewContract(common.Address{2}, address, uint256.NewInt(0), NewGasBudget(20_000_000, 0), nil)
			contract.SetCallCode(common.Hash{}, initCode)

			ret, err := evm.initNewContract(contract, address)
			if !errors.Is(err, test.expectedError) {
				t.Errorf("unexpected error: got %v, want %v", err, test.expectedError)
			}
			if err == nil && len(ret) != int(test.codeSize) {
				t.Errorf("unexpected code length: got %d, want %d", len(ret), test.codeSize)
			}
		})
	}
}

func TestCustomCodeSize_GasCreateEip3860AllowsCustomMaxInitCodeSize(t *testing.T) {
	customLimit := 100_000
	tests := map[string]struct {
		maxInitCodeSize *int
		initCodeSize    uint64
		expectedError   error
	}{
		"default initCodeSize limit": {
			initCodeSize: params.MaxInitCodeSize,
		},
		"default initCodeSize above limit": {
			initCodeSize:  params.MaxInitCodeSize + 1,
			expectedError: ErrMaxInitCodeSizeExceeded,
		},
		"custom initCodeSize limit": {
			maxInitCodeSize: asPointer(customLimit),
			initCodeSize:    uint64(customLimit),
		},
		"custom initCodeSize above limit": {
			maxInitCodeSize: asPointer(customLimit),
			initCodeSize:    uint64(customLimit + 1),
			expectedError:   ErrMaxInitCodeSizeExceeded,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			stateDB, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
			if err != nil {
				t.Fatalf("failed to create stateDB: %v", err)
			}
			config := Config{
				MaxInitCodeSize: test.maxInitCodeSize,
			}
			time := uint64(0)
			chainConfig := &params.ChainConfig{
				LondonBlock:  big.NewInt(0),
				ShanghaiTime: &time,
			}
			blockContext := BlockContext{
				BlockNumber: big.NewInt(10),
				Random:      &common.Hash{0x42},
			}
			evm := NewEVM(blockContext, stateDB, chainConfig, config)

			stack := newStackForTesting()
			stack.Push(uint256.NewInt(test.initCodeSize))
			stack.Push(uint256.NewInt(test.initCodeSize))
			stack.Push(uint256.NewInt(test.initCodeSize))

			_, err = gasCreateEip3860(evm, nil, stack, NewMemory(), 0)
			if !errors.Is(err, test.expectedError) {
				t.Errorf("unexpected error: got %v, want %v", err, test.expectedError)
			}

			_, err = gasCreate2Eip3860(evm, nil, stack, NewMemory(), 0)
			if !errors.Is(err, test.expectedError) {
				t.Errorf("unexpected error: got %v, want %v", err, test.expectedError)
			}
		})
	}
}

// The tests below pin vm.Config.StatePrecompiles, a Sonic addition that lets a
// Sonic-side contract be registered at an address and receive direct access to
// the state. Wiring:
//   - field:     Config.StatePrecompiles in core/vm/interpreter.go
//   - interface: PrecompiledStateContract in core/vm/contracts.go
//   - accessor:  EVM.statePrecompile in core/vm/evm.go
//   - dispatch:  the "isStatePrecompile" branch in EVM.Call

// statePrecompileStub is a local stand-in for a Sonic state precompile. It
// records the arguments it was handed and returns sentinel values.
type statePrecompileStub struct {
	calls int

	receivedStateDB  StateDB
	receivedBlockCtx BlockContext
	receivedTxCtx    TxContext
	receivedCaller   common.Address
	receivedInput    []byte
	receivedGas      GasBudget
}

var (
	statePrecompileOutput  = []byte("state-precompile-output")
	statePrecompileGasLeft = NewGasBudget(4_711, 0)
)

func (s *statePrecompileStub) Run(
	stateDB StateDB,
	blockCtx BlockContext,
	txCtx TxContext,
	caller common.Address,
	input []byte,
	suppliedGas GasBudget,
) ([]byte, GasBudget, error) {
	s.calls++
	s.receivedStateDB = stateDB
	s.receivedBlockCtx = blockCtx
	s.receivedTxCtx = txCtx
	s.receivedCaller = caller
	s.receivedInput = input
	s.receivedGas = suppliedGas
	return statePrecompileOutput, statePrecompileGasLeft, nil
}

func TestStatePrecompiles_RegisteredContractIsDispatchedTo(t *testing.T) {
	var (
		suppliedGas = NewGasBudget(100_000, 0)
		caller      = common.Address{0x01}
		precompile  = common.Address{0x42}
		input       = []byte("call-data")
	)

	tests := map[string]struct {
		// accountExists controls whether the precompile address is present in
		// the state. The non-existing case exercises the added
		// "!isStatePrecompile" condition in the EIP-158 dead-account guard,
		// without which a zero-value call to an absent address returns early as
		// a no-op and the precompile is never reached.
		accountExists bool
		value         *uint256.Int
	}{
		"existing account, zero value":     {accountExists: true, value: uint256.NewInt(0)},
		"existing account, non-zero value": {accountExists: true, value: uint256.NewInt(1)},
		"absent account, non-zero value":   {accountExists: false, value: uint256.NewInt(1)},
		"absent account, zero value":       {accountExists: false, value: uint256.NewInt(0)},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			stub := &statePrecompileStub{}
			evm := newStatePrecompileTestEvm(t, map[common.Address]PrecompiledStateContract{
				precompile: stub,
			})
			if !evm.chainRules.IsEIP158 {
				t.Fatal("test setup is ineffective: the dead-account guard requires EIP-158")
			}
			if test.accountExists {
				evm.StateDB.CreateAccount(precompile)
			}
			if evm.StateDB.Exist(precompile) != test.accountExists {
				t.Fatalf("expected the precompile account presence to be %t", test.accountExists)
			}

			ret, gasLeft, err := evm.Call(caller, precompile, input, suppliedGas, test.value)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if stub.calls != 1 {
				t.Fatalf("expected exactly 1 call to PrecompiledStateContract.Run, got %d; "+
					"the isStatePrecompile dispatch in EVM.Call is not effective", stub.calls)
			}
			if string(ret) != string(statePrecompileOutput) {
				t.Errorf("expected the state precompile output %q, got %q; EVM.Call does not "+
					"return the state precompile result", statePrecompileOutput, ret)
			}
			if gasLeft != statePrecompileGasLeft {
				t.Errorf("expected the state precompile leftover gas %d, got %d; EVM.Call does "+
					"not return the state precompile result", statePrecompileGasLeft, gasLeft)
			}

			// The arguments of the sp.Run(...) call site are part of the hook.
			if stub.receivedStateDB != evm.StateDB {
				t.Errorf("the state precompile was handed a foreign StateDB")
			}
			if stub.receivedBlockCtx.Coinbase != evm.Context.Coinbase {
				t.Errorf("the state precompile was handed a foreign BlockContext")
			}
			if stub.receivedTxCtx.Origin != evm.TxContext.Origin {
				t.Errorf("the state precompile was handed a foreign TxContext")
			}
			if stub.receivedCaller != caller {
				t.Errorf("expected caller %v, got %v", caller, stub.receivedCaller)
			}
			if string(stub.receivedInput) != string(input) {
				t.Errorf("expected input %q, got %q", input, stub.receivedInput)
			}
			if stub.receivedGas != suppliedGas {
				t.Errorf("expected supplied gas %d, got %d", suppliedGas, stub.receivedGas)
			}
		})
	}
}

// TestStatePrecompiles_UnregisteredAddressKeepsVanillaBehavior is the control
// for the dead-account guard: without a registration, a zero-value call to an
// absent address must still return early as a no-op.
func TestStatePrecompiles_UnregisteredAddressKeepsVanillaBehavior(t *testing.T) {
	precompiles := map[string]map[common.Address]PrecompiledStateContract{
		"nil map":                       nil,
		"empty map":                     {},
		"registered at another address": {common.Address{0x43}: &statePrecompileStub{}},
	}

	target := common.Address{0x42}

	for name, registered := range precompiles {
		t.Run(name, func(t *testing.T) {
			evm := newStatePrecompileTestEvm(t, registered)

			suppliedGas := NewGasBudget(100_000, 0)
			ret, gasLeft, err := evm.Call(common.Address{0x01}, target, []byte("call-data"), suppliedGas, uint256.NewInt(0))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(ret) != 0 {
				t.Errorf("expected no return data from a call to an absent account, got %q", ret)
			}
			if gasLeft != suppliedGas {
				t.Errorf("expected the supplied gas to be returned unchanged, got %v", gasLeft)
			}
			if evm.StateDB.Exist(target) {
				t.Errorf("expected the absent account not to be created")
			}
		})
	}
}

// TestStatePrecompiles_LookupOnAnUnsetMap pins the documented contract of
// EVM.statePrecompile for an unset map.
//
// Note: the explicit nil check inside statePrecompile is not observable from
// Go - reading from a nil map is legal and yields the zero value with ok=false -
// so this test documents the contract but cannot detect the check's removal. The
// behavior it guards is covered by
// TestStatePrecompiles_UnregisteredAddressKeepsVanillaBehavior instead.
func TestStatePrecompiles_LookupOnAnUnsetMap(t *testing.T) {
	evm := &EVM{}
	if evm.Config.StatePrecompiles != nil {
		t.Fatal("expected a zero-valued Config to carry no state precompiles")
	}

	contract, found := evm.statePrecompile(common.Address{0x42})
	if contract != nil || found {
		t.Errorf("expected EVM.statePrecompile to report (nil, false) for an unset map, got (%v, %t)",
			contract, found)
	}
}

func newStatePrecompileTestEvm(t *testing.T, precompiles map[common.Address]PrecompiledStateContract) *EVM {
	t.Helper()
	stateDB, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	if err != nil {
		t.Fatalf("failed to create stateDB: %v", err)
	}
	return NewEVM(
		BlockContext{
			BlockNumber: big.NewInt(10),
			Coinbase:    common.Address{0x0c},
			CanTransfer: func(StateDB, common.Address, *uint256.Int) bool { return true },
			Transfer:    func(StateDB, common.Address, common.Address, *uint256.Int, *params.Rules) {},
		},
		stateDB,
		&params.ChainConfig{EIP158Block: big.NewInt(0)},
		Config{StatePrecompiles: precompiles},
	)
}

func asPointer[T any](v T) *T {
	return &v
}
