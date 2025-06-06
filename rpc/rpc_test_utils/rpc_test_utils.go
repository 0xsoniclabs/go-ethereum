package rpc_test_utils

import (
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/eth/tracers"
	"github.com/ethereum/go-ethereum/internal/ethapi"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
)

// GetRpcApis returns a list of RPC APIs for testing
func GetRpcApis() []rpc.API {
	backend := &dummyBackend{}
	apis := ethapi.GetAPIs(backend)
	apis = append(apis, tracers.APIs(nil)...)
	return apis
}

type dummyBackend struct {
	ethapi.Backend
}

func (b *dummyBackend) ChainConfig() *params.ChainConfig {
	return &params.ChainConfig{}
}

func (b *dummyBackend) AccountManager() *accounts.Manager {
	return nil
}
