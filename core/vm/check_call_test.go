// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package vm

import (
	"bytes"
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// Sonic's CheckCall can revert a successful call: its state is undone, the
// revert data is returned, and the caller keeps the remaining gas.
func TestCheckCall_RevertsSuccessfulCall(t *testing.T) {
	caller := common.Address{0x1}
	target := common.Address{0x2}
	revertData := []byte{0xde, 0xad}

	for name, check := range map[string]CallCheckFunc{
		"no check": nil,
		"allows":   func(StateDB, common.Address, int) []byte { return nil },
		"reverts": func(_ StateDB, addr common.Address, _ int) []byte {
			if addr == target {
				return revertData
			}
			return nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
			// PUSH1 1 PUSH1 0 SSTORE STOP: stores 1 in slot 0.
			statedb.SetCode(target, []byte{0x60, 0x01, 0x60, 0x00, 0x55, 0x00}, tracing.CodeChangeUnspecified)

			evm := NewEVM(BlockContext{
				CanTransfer: func(StateDB, common.Address, *uint256.Int) bool { return true },
				Transfer:    func(StateDB, common.Address, common.Address, *uint256.Int, *params.Rules) {},
				CheckCall:   check,
			}, statedb, params.AllEthashProtocolChanges, Config{})

			ret, gas, err := evm.Call(caller, target, nil, 100_000, new(uint256.Int))
			stored := statedb.GetState(target, common.Hash{})
			if name != "reverts" {
				if err != nil || stored != common.BigToHash(common.Big1) {
					t.Fatalf("got error %v and slot %v, want success and slot 1", err, stored)
				}
				return
			}
			if !errors.Is(err, ErrExecutionReverted) || !bytes.Equal(ret, revertData) {
				t.Fatalf("got (%x, %v), want (%x, %v)", ret, err, revertData, ErrExecutionReverted)
			}
			if stored != (common.Hash{}) {
				t.Fatalf("reverted call kept its state: slot 0 is %v", stored)
			}
			if gas == 0 {
				t.Fatalf("reverted call consumed all gas")
			}
		})
	}
}
