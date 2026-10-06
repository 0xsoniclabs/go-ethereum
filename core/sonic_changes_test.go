package core

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

func TestCustomCodeSize_MaxInitCodeSizeIsEnforcedWhenSet(t *testing.T) {
	asPointer := func(v int) *int {
		return &v
	}

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
			expectedError: vm.ErrMaxInitCodeSizeExceeded,
		},
		"custom initCodeSize limit": {
			maxInitCodeSize: asPointer(customLimit),
			initCodeSize:    uint64(customLimit),
		},
		"custom initCodeSize above limit": {
			maxInitCodeSize: asPointer(customLimit),
			initCodeSize:    uint64(customLimit + 1),
			expectedError:   vm.ErrMaxInitCodeSizeExceeded,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			stateDB, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
			if err != nil {
				t.Fatalf("failed to create stateDB: %v", err)
			}

			chainConfig := &params.ChainConfig{
				LondonBlock:  big.NewInt(0),
				ShanghaiTime: new(uint64),
			}
			blockContext := vm.BlockContext{
				CanTransfer: func(vm.StateDB, common.Address, *uint256.Int) bool { return true },
				Transfer:    func(vm.StateDB, common.Address, common.Address, *uint256.Int, *params.Rules) {},
				BlockNumber: big.NewInt(10),
				BaseFee:     big.NewInt(0),
				Random:      &common.Hash{0x42},
			}
			config := vm.Config{
				MaxInitCodeSize:                 test.maxInitCodeSize,
				IgnoreGasFeeCap:                 true,
				InsufficientBalanceIsNotAnError: true,
			}
			evm := vm.NewEVM(blockContext, stateDB, chainConfig, config)

			message := &Message{
				GasLimit:              1_000_000,
				GasPrice:              big.NewInt(0),
				GasFeeCap:             big.NewInt(0),
				GasTipCap:             big.NewInt(0),
				Data:                  make([]byte, test.initCodeSize),
				To:                    nil, // contract creation
				SkipNonceChecks:       true,
				SkipTransactionChecks: true,
			}

			gasPool := NewGasPool(1_000_000)

			stateTransition := newStateTransition(evm, message, gasPool)

			_, err = stateTransition.execute()
			if !errors.Is(err, test.expectedError) {
				t.Errorf("unexpected error: got %v, want %v", err, test.expectedError)
			}
		})
	}
}

func TestUnfundedCreationSpendsNonce_SpendsTheNonceOnlyWhenEnabled(t *testing.T) {
	const balance = 1_000_000

	tests := map[string]struct {
		value       int64
		enabled     bool
		expectedErr error
		wantNonce   uint64
	}{
		"affordable":           {value: balance - 1, wantNonce: 1},
		"unaffordable":         {value: balance + 1, expectedErr: vm.ErrInsufficientBalance, wantNonce: 0},
		"unaffordable enabled": {value: balance + 1, enabled: true, expectedErr: vm.ErrInsufficientBalance, wantNonce: 1},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			sender := common.Address{0x42}

			stateDB, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
			if err != nil {
				t.Fatalf("failed to create stateDB: %v", err)
			}
			stateDB.CreateAccount(sender)
			stateDB.SetBalance(sender, uint256.NewInt(balance), tracing.BalanceChangeUnspecified)

			evm := vm.NewEVM(
				vm.BlockContext{
					CanTransfer: CanTransfer,
					Transfer:    Transfer,
					BlockNumber: big.NewInt(10),
					BaseFee:     big.NewInt(0),
					Random:      &common.Hash{0x42},
				},
				stateDB,
				&params.ChainConfig{LondonBlock: big.NewInt(0), ShanghaiTime: new(uint64)},
				vm.Config{
					IgnoreGasFeeCap:                 true,
					InsufficientBalanceIsNotAnError: true,
					UnfundedCreationSpendsNonce:     test.enabled,
				},
			)

			message := &Message{
				From:      sender,
				To:        nil, // contract creation
				Value:     big.NewInt(test.value),
				GasLimit:  1_000_000,
				GasPrice:  big.NewInt(0),
				GasFeeCap: big.NewInt(0),
				GasTipCap: big.NewInt(0),
			}

			result, err := newStateTransition(evm, message, NewGasPool(1_000_000)).execute()
			if err != nil {
				t.Fatalf("transaction must not be a consensus error: %v", err)
			}
			if !errors.Is(result.Err, test.expectedErr) {
				t.Errorf("unexpected execution error: got %v, want %v", result.Err, test.expectedErr)
			}
			if got := stateDB.GetNonce(sender); got != test.wantNonce {
				t.Errorf("nonce after the creation: got %d, want %d", got, test.wantNonce)
			}
		})
	}
}
