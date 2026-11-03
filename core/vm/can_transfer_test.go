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
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// Sonic passes the recipient to CanTransfer so a guard can refuse transfers
// to an account; a refused transfer fails the frame and keeps its gas.
func TestCanTransfer_ReceivesRecipientAndRefusalFailsFrame(t *testing.T) {
	caller := common.Address{0x1}
	target := common.Address{0x2}

	newEVM := func(refused common.Address, seen *[2]common.Address) *EVM {
		statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
		statedb.AddBalance(caller, uint256.NewInt(10), tracing.BalanceChangeUnspecified)
		return NewEVM(BlockContext{
			CanTransfer: func(_ StateDB, from, to common.Address, _ *uint256.Int) bool {
				*seen = [2]common.Address{from, to}
				return to != refused
			},
			Transfer: func(StateDB, common.Address, common.Address, *uint256.Int, *params.Rules) {},
		}, statedb, params.AllEthashProtocolChanges, Config{})
	}

	var seen [2]common.Address
	_, gas, err := newEVM(target, &seen).Call(caller, target, nil, 100_000, uint256.NewInt(1))
	if !errors.Is(err, ErrInsufficientBalance) || gas != 100_000 {
		t.Fatalf("Call to refused recipient: got (%d, %v), want (100000, %v)", gas, err, ErrInsufficientBalance)
	}
	if seen != [2]common.Address{caller, target} {
		t.Fatalf("Call: CanTransfer got %v, want sender and target", seen)
	}

	if _, _, err := newEVM(common.Address{}, &seen).CallCode(caller, target, nil, 100_000, uint256.NewInt(1)); err != nil {
		t.Fatalf("CallCode: unexpected error %v", err)
	}
	if seen != [2]common.Address{caller, caller} {
		t.Fatalf("CallCode: CanTransfer got %v, want the caller twice", seen)
	}

	created := crypto.CreateAddress(caller, 0)
	_, _, _, err = newEVM(created, &seen).Create(caller, nil, 100_000, uint256.NewInt(1))
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("Create of refused address: got %v, want %v", err, ErrInsufficientBalance)
	}
	if seen != [2]common.Address{caller, created} {
		t.Fatalf("Create: CanTransfer got %v, want sender and created address", seen)
	}
}
