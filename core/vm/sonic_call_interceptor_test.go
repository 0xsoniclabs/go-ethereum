package vm

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// This file pins the EVM.CallInterceptor hook, a Sonic addition that lets Tosca
// take over the EVM's call routing. Each of the EVM's six call entry points
// carries an early return that hands control to the interceptor; every one of
// them is covered below, because a rebase dropping the branch in a single entry
// point still compiles and silently bypasses Tosca for that call kind.
//
// The two creating entry points share their guard: Create, Create2 and the
// CREATE/CREATE2 opcodes all reach the interceptor through EVM.create, which
// picks the interceptor method by opcode.
//
// Wiring:
//   - interface: CallContextInterceptor in core/vm/sonic_tosca_integration.go
//   - field:     EVM.CallInterceptor in core/vm/evm.go
//   - hook sites: the "if evm.CallInterceptor != nil" early returns in
//     EVM.Call, CallCode, DelegateCall, StaticCall and create.

// Sentinel values returned by callInterceptorStub. They are chosen so that no
// regular EVM execution can produce them, which is what makes an intercepted
// call distinguishable from one that fell through to the normal path.
var (
	errIntercepted            = errors.New("intercepted by the test interceptor")
	interceptedGas            = NewGasBudget(424_242, 0)
	interceptedCreateAddress  = common.Address{0xAA, 0xBB, 0xCC}
	interceptorSuppliedGas    = NewGasBudget(1_000_000, 0)
	interceptorCaller         = common.Address{0x01}
	interceptorOriginalCaller = common.Address{0x02}
	interceptorTarget         = common.Address{0x03}
)

func interceptedReturnData(entryPoint string) []byte {
	return []byte("intercepted-by-" + entryPoint)
}

// callInterceptorStub is a local stand-in for Tosca's interceptor. It records
// which entry points were invoked, along with the EVM it was handed, and returns
// per-entry-point sentinel values.
type callInterceptorStub struct {
	calls        map[string]int
	receivedEnvs []*EVM
	// err is what every intercepted call reports. It defaults to the sentinel
	// error; clearing it lets the intercepted frame look successful to the
	// caller, which is how the created address becomes observable.
	err error
}

func newCallInterceptorStub() *callInterceptorStub {
	return &callInterceptorStub{calls: map[string]int{}, err: errIntercepted}
}

func (s *callInterceptorStub) record(env *EVM, entryPoint string) {
	s.calls[entryPoint]++
	s.receivedEnvs = append(s.receivedEnvs, env)
}

func (s *callInterceptorStub) totalCalls() int {
	total := 0
	for _, count := range s.calls {
		total += count
	}
	return total
}

func (s *callInterceptorStub) Call(env *EVM, _ common.Address, _ common.Address, _ []byte, _ GasBudget, _ *uint256.Int) ([]byte, GasBudget, error) {
	s.record(env, "Call")
	return interceptedReturnData("Call"), interceptedGas, s.err
}

func (s *callInterceptorStub) CallCode(env *EVM, _ common.Address, _ common.Address, _ []byte, _ GasBudget, _ *uint256.Int) ([]byte, GasBudget, error) {
	s.record(env, "CallCode")
	return interceptedReturnData("CallCode"), interceptedGas, s.err
}

func (s *callInterceptorStub) DelegateCall(env *EVM, _ common.Address, _ common.Address, _ []byte, _ GasBudget) ([]byte, GasBudget, error) {
	s.record(env, "DelegateCall")
	return interceptedReturnData("DelegateCall"), interceptedGas, s.err
}

func (s *callInterceptorStub) StaticCall(env *EVM, _ common.Address, _ common.Address, _ []byte, _ GasBudget) ([]byte, GasBudget, error) {
	s.record(env, "StaticCall")
	return interceptedReturnData("StaticCall"), interceptedGas, s.err
}

func (s *callInterceptorStub) Create(env *EVM, _ common.Address, _ []byte, _ GasBudget, _ *uint256.Int) ([]byte, common.Address, GasBudget, error) {
	s.record(env, "Create")
	return interceptedReturnData("Create"), interceptedCreateAddress, interceptedGas, s.err
}

func (s *callInterceptorStub) Create2(env *EVM, _ common.Address, _ []byte, _ GasBudget, _ *uint256.Int, _ *uint256.Int) ([]byte, common.Address, GasBudget, error) {
	s.record(env, "Create2")
	return interceptedReturnData("Create2"), interceptedCreateAddress, interceptedGas, s.err
}

// evmCallEntryPoint describes one of the EVM's call entry points, normalised to
// a single signature so that all of them can be checked identically. Adding a
// seventh entry point means adding one row here.
type evmCallEntryPoint struct {
	name string
	// invoke calls the entry point on the given EVM. Entry points that do not
	// produce an address report the zero address.
	invoke func(evm *EVM) (ret []byte, address common.Address, leftOverGas GasBudget, err error)
	// createsAddress marks entry points whose result includes a contract address.
	createsAddress bool
}

var evmCallEntryPoints = []evmCallEntryPoint{
	{
		name: "Call",
		invoke: func(evm *EVM) ([]byte, common.Address, GasBudget, error) {
			ret, gas, err := evm.Call(interceptorCaller, interceptorTarget, nil, interceptorSuppliedGas, uint256.NewInt(0))
			return ret, common.Address{}, gas, err
		},
	},
	{
		name: "CallCode",
		invoke: func(evm *EVM) ([]byte, common.Address, GasBudget, error) {
			ret, gas, err := evm.CallCode(interceptorCaller, interceptorTarget, nil, interceptorSuppliedGas, uint256.NewInt(0))
			return ret, common.Address{}, gas, err
		},
	},
	{
		name: "DelegateCall",
		invoke: func(evm *EVM) ([]byte, common.Address, GasBudget, error) {
			ret, gas, err := evm.DelegateCall(interceptorOriginalCaller, interceptorCaller, interceptorTarget, nil, interceptorSuppliedGas, uint256.NewInt(0))
			return ret, common.Address{}, gas, err
		},
	},
	{
		name: "StaticCall",
		invoke: func(evm *EVM) ([]byte, common.Address, GasBudget, error) {
			ret, gas, err := evm.StaticCall(interceptorCaller, interceptorTarget, nil, interceptorSuppliedGas)
			return ret, common.Address{}, gas, err
		},
	},
	{
		name:           "Create",
		createsAddress: true,
		invoke: func(evm *EVM) ([]byte, common.Address, GasBudget, error) {
			return evm.Create(interceptorCaller, nil, interceptorSuppliedGas, uint256.NewInt(0))
		},
	},
	{
		name:           "Create2",
		createsAddress: true,
		invoke: func(evm *EVM) ([]byte, common.Address, GasBudget, error) {
			return evm.Create2(interceptorCaller, nil, interceptorSuppliedGas, uint256.NewInt(0), uint256.NewInt(0))
		},
	},
}

func TestCallInterceptor_EveryEntryPointDispatchesToTheInterceptor(t *testing.T) {
	for _, entryPoint := range evmCallEntryPoints {
		t.Run(entryPoint.name, func(t *testing.T) {
			interceptor := newCallInterceptorStub()
			evm := newCallInterceptorTestEvm(t)
			evm.CallInterceptor = interceptor

			ret, address, gas, err := entryPoint.invoke(evm)

			// The interceptor must be consulted, exactly once, and by the
			// matching entry point only.
			if got, want := interceptor.calls[entryPoint.name], 1; got != want {
				t.Errorf("expected %d call(s) to CallContextInterceptor.%s, got %d; "+
					"the \"if evm.CallInterceptor != nil\" early return on the path of "+
					"EVM.%s was lost", want, entryPoint.name, got, entryPoint.name)
			}
			if got := interceptor.totalCalls(); got != 1 {
				t.Errorf("expected exactly 1 interceptor call in total, got %d: %v",
					got, interceptor.calls)
			}

			// The interceptor's result must be what the EVM returns, which is
			// what proves the normal execution path was short-circuited rather
			// than merely consulted.
			if want := interceptedReturnData(entryPoint.name); string(ret) != string(want) {
				t.Errorf("expected the interceptor's return data %q, got %q; EVM.%s does not "+
					"return the interceptor result", want, ret, entryPoint.name)
			}
			if gas != interceptedGas {
				t.Errorf("expected the interceptor's leftover gas %d, got %d; EVM.%s does not "+
					"return the interceptor result", interceptedGas, gas, entryPoint.name)
			}
			if !errors.Is(err, errIntercepted) {
				t.Errorf("expected the interceptor's error %v, got %v; EVM.%s does not "+
					"return the interceptor result", errIntercepted, err, entryPoint.name)
			}
			if entryPoint.createsAddress && address != interceptedCreateAddress {
				t.Errorf("expected the interceptor's contract address %v, got %v; EVM.%s does not "+
					"return the interceptor result", interceptedCreateAddress, address, entryPoint.name)
			}

			// The interceptor needs the EVM it is intercepting for.
			for _, env := range interceptor.receivedEnvs {
				if env != evm {
					t.Errorf("EVM.%s handed the interceptor a foreign EVM instance", entryPoint.name)
				}
			}
		})
	}
}

// TestCallInterceptor_WithoutAnInterceptorTheNormalPathRuns is the control for
// the test above: it confirms the sentinel values cannot be produced by regular
// EVM execution, so observing them really does prove interception.
func TestCallInterceptor_WithoutAnInterceptorTheNormalPathRuns(t *testing.T) {
	for _, entryPoint := range evmCallEntryPoints {
		t.Run(entryPoint.name, func(t *testing.T) {
			evm := newCallInterceptorTestEvm(t)
			if evm.CallInterceptor != nil {
				t.Fatal("expected a freshly constructed EVM to carry no interceptor")
			}

			ret, address, gas, err := entryPoint.invoke(evm)

			if want := interceptedReturnData(entryPoint.name); string(ret) == string(want) {
				t.Errorf("the unintercepted path returns the sentinel data %q, "+
					"which makes the interception test meaningless", want)
			}
			if gas == interceptedGas {
				t.Errorf("the unintercepted path returns the sentinel gas %d, "+
					"which makes the interception test meaningless", interceptedGas)
			}
			if errors.Is(err, errIntercepted) {
				t.Errorf("the unintercepted path returns the sentinel error %v, "+
					"which makes the interception test meaningless", errIntercepted)
			}
			if entryPoint.createsAddress && address == interceptedCreateAddress {
				t.Errorf("the unintercepted path returns the sentinel address %v, "+
					"which makes the interception test meaningless", interceptedCreateAddress)
			}
		})
	}
}

// TestCallInterceptor_CreateOpcodesDispatchToTheInterceptor covers the CREATE
// and CREATE2 opcodes. They do not go through EVM.Create/Create2: since the
// contract address is derived and the account creation charged in the parent
// frame, the opcodes enter the shared creation frame EVM.create directly, and
// that is where they have to meet the interceptor.
func TestCallInterceptor_CreateOpcodesDispatchToTheInterceptor(t *testing.T) {
	tests := []struct {
		opcode string
		// execute runs the opcode handler.
		execute func(pc *uint64, evm *EVM, scope *ScopeContext) ([]byte, error)
		// operands is the number of stack items the opcode consumes.
		operands int
		// interceptorMethod is the method the opcode has to be routed to.
		interceptorMethod string
	}{
		{opcode: "CREATE", execute: opCreate, operands: 3, interceptorMethod: "Create"},
		{opcode: "CREATE2", execute: opCreate2, operands: 4, interceptorMethod: "Create2"},
	}

	for _, test := range tests {
		t.Run(test.opcode, func(t *testing.T) {
			interceptor := newCallInterceptorStub()
			// Report success so that the opcode forwards the interceptor's
			// address to the stack instead of the zero address it pushes for a
			// failed creation.
			interceptor.err = nil
			evm := newCallInterceptorTestEvm(t)
			evm.CallInterceptor = interceptor

			stack := NewStack()
			for range test.operands {
				stack.Push(uint256.NewInt(0))
			}
			scope := &ScopeContext{
				Memory:   NewMemory(),
				Stack:    stack,
				Contract: NewContract(interceptorCaller, interceptorTarget, uint256.NewInt(0), interceptorSuppliedGas, nil),
			}

			pc := uint64(0)
			if _, err := test.execute(&pc, evm, scope); err != nil {
				t.Fatalf("the %s opcode failed: %v", test.opcode, err)
			}

			if got, want := interceptor.calls[test.interceptorMethod], 1; got != want {
				t.Errorf("expected %d call(s) to CallContextInterceptor.%s, got %d; the "+
					"%s opcode no longer reaches the interceptor through EVM.create",
					want, test.interceptorMethod, got, test.opcode)
			}
			if got := interceptor.totalCalls(); got != 1 {
				t.Errorf("expected exactly 1 interceptor call in total, got %d: %v",
					got, interceptor.calls)
			}
			if got := stack.pop(); common.Address(got.Bytes20()) != interceptedCreateAddress {
				t.Errorf("expected the interceptor's contract address %v on the stack, got %v; "+
					"the %s opcode does not return the interceptor result",
					interceptedCreateAddress, common.Address(got.Bytes20()), test.opcode)
			}
		})
	}
}

func newCallInterceptorTestEvm(t *testing.T) *EVM {
	t.Helper()
	stateDB, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	if err != nil {
		t.Fatalf("failed to create stateDB: %v", err)
	}
	return NewEVM(
		BlockContext{
			BlockNumber: big.NewInt(0),
			CanTransfer: func(StateDB, common.Address, *uint256.Int) bool { return true },
			Transfer:    func(StateDB, common.Address, common.Address, *uint256.Int, *params.Rules) {},
		},
		stateDB,
		&params.ChainConfig{},
		Config{},
	)
}
