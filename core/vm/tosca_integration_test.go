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
			contract := NewContract(common.Address{2}, address, uint256.NewInt(0), 20_000_000, nil)
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
			evm := NewEVM(BlockContext{}, stateDB, &params.ChainConfig{}, config)

			stack := newstack()
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

func asPointer[T any](v T) *T {
	return &v
}
