package core

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

func TestCustomCodeSize_MaxInitCodeSizeIsEnforcedWhenSet(t *testing.T) {
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
