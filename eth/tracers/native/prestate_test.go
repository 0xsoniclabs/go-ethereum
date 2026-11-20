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

package native_test

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/tracers"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"
)

// TestPrestateTracerDiffMode_OmitsAccountsThatNeverExisted checks that diff mode
// does not report the recipient and coinbase as modified when neither exists
// before or after the transaction, although GetCodeHash reports a zero hash for them.
func TestPrestateTracerDiffMode_OmitsAccountsThatNeverExisted(t *testing.T) {
	statedb, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	require.NoError(t, err)
	from := common.Address{1}
	statedb.SetBalance(from, uint256.NewInt(1), tracing.BalanceChangeUnspecified)

	tr, err := tracers.DefaultDirectory.New("prestateTracer", &tracers.Context{}, json.RawMessage(`{"diffMode":true}`), params.MainnetChainConfig)
	require.NoError(t, err)
	to := common.Address{2}
	env := &tracing.VMContext{Coinbase: common.Address{3}, BlockNumber: big.NewInt(0), StateDB: statedb}
	tr.OnTxStart(env, types.NewTx(&types.LegacyTx{To: &to}), from)
	tr.OnTxEnd(&types.Receipt{}, nil)

	res, err := tr.GetResult()
	require.NoError(t, err)
	require.JSONEq(t, `{"post":{},"pre":{}}`, string(res))
}
