package rpc_test_utils

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetRpcApis(t *testing.T) {
	require.NotEmpty(t, GetRpcApis())
}
