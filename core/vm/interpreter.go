// Copyright 2014 The go-ethereum Authors
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
	"fmt"
	gomath "math"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/holiman/uint256"
)

// Config are the configuration options for the Interpreter
type Config struct {
	Tracer *tracing.Hooks

	NoBaseFee                bool  // Forces the EIP-1559 baseFee to 0 (needed for 0 price calls)
	EnablePreimageRecording  bool  // Enables recording of SHA3/keccak preimages
	ExtraEips                []int // Additional EIPS that are to be enabled
	DisableParallelExecution bool  // Disable parallel block processing

	// -- Sonic specific configuration options --
	// Every field below is a Sonic addition. At its zero value the EVM behaves as
	// the vanilla Ethereum EVM; the Sonic main-net behavior needs all of the flags
	// enabled. Future networks may have different configurations. Each field is
	// pinned by a hook-liveness test - see SONIC_HOOKS.md.

	StatePrecompiles map[common.Address]PrecompiledStateContract // Custom precompiled contracts with state access

	Interpreter           InterpreterFactory // The interpreter implementation to use for non-tracing executions. If nil, EVMInterpreter will be used.
	InterpreterForTracing InterpreterFactory // The interpreter implementation to use for tracing executions. If nil, Interpreter will be used.

	ChargeExcessGas                 bool // if enabled, 10% of excessive gas is charged for the execution
	IgnoreGasFeeCap                 bool // if enabled, gas fee cap is ignored
	InsufficientBalanceIsNotAnError bool // if enabled, insufficient balance is treated as a revert, not an execution error on the top level
	SkipTipPaymentToCoinbase        bool // if enabled, tip payment is not made to the coinbase address

	// MaxTxGas is the maximum gas allowed per transaction.
	// If nil, this is interpreted as "not set" and the default params value is used.
	MaxTxGas *uint64

	MaxCodeSize     *int // Maximum code size allowed for a contract. If nil, the default params value is used.
	MaxInitCodeSize *int // Maximum init code size allowed for a contract. If nil, the default params value is used.
}

// ScopeContext contains the things that are per-call, such as stack and memory,
// but not transients like pc and gas
type ScopeContext struct {
	Memory   *Memory
	Stack    *Stack
	Contract *Contract
}

// MemoryData returns the underlying memory slice. Callers must not modify the contents
// of the returned data.
func (ctx *ScopeContext) MemoryData() []byte {
	if ctx.Memory == nil {
		return nil
	}
	return ctx.Memory.Data()
}

// StackData returns the stack data. Callers must not modify the contents
// of the returned data.
func (ctx *ScopeContext) StackData() []uint256.Int {
	if ctx.Stack == nil {
		return nil
	}
	return ctx.Stack.Data()
}

// Caller returns the current caller.
func (ctx *ScopeContext) Caller() common.Address {
	return ctx.Contract.Caller()
}

// Address returns the address where this scope of execution is taking place.
func (ctx *ScopeContext) Address() common.Address {
	return ctx.Contract.Address()
}

// CallValue returns the value supplied with this call.
func (ctx *ScopeContext) CallValue() *uint256.Int {
	return ctx.Contract.Value()
}

// CallInput returns the input/calldata with this call. Callers must not modify
// the contents of the returned data.
func (ctx *ScopeContext) CallInput() []byte {
	return ctx.Contract.Input
}

// ContractCode returns the code of the contract being executed.
func (ctx *ScopeContext) ContractCode() []byte {
	return ctx.Contract.Code
}

// Run loops and evaluates the contract's code with the given input data and returns
// the return byte-slice and an error if one occurred.
//
// It's important to note that any errors returned by the interpreter should be
// considered a revert-and-consume-all-gas operation except for
// ErrExecutionReverted which means revert-and-keep-gas-left.
func (evm *EVM) Run(contract *Contract, input []byte, readOnly bool) (ret []byte, err error) {
	// Sonic: upstream's loop lives in evmInterpreter.runSteps below. Run only
	// re-directs to the configured interpreter implementation, which is the hook-in
	// point for alternative interpreters such as Tosca's (core/vm/sonic_tosca_integration.go).
	return getInterpreter(evm).Interpret(contract, input, readOnly)
}

// Sonic: evmInterpreter wraps upstream's interpreter loop as an Interpreter, so
// that it can be selected through Config.Interpreter like any other.
type evmInterpreter struct {
	evm *EVM
}

func (i evmInterpreter) Interpret(contract *Contract, input []byte, readOnly bool) (ret []byte, err error) {
	state := InterpreterState{
		Contract: contract,
		Stack:    i.evm.arena.stack(),
		Memory:   NewMemory(),
		Input:    input,
		ReadOnly: readOnly,
	}
	defer func() {
		state.Stack.release()
		state.Memory.Free()
	}()
	return i.runSteps(&state, gomath.MaxUint64)
}

// Sonic: runSteps is upstream's EVM.Run body, reworked so that stack, memory and
// program counter live in a caller-owned InterpreterState and execution can stop
// after maxSteps. This is what backs EVM.Step and Tosca's conformance tests; the
// individual departures from upstream are marked below.
func (i evmInterpreter) runSteps(state *InterpreterState, maxSteps uint64) (ret []byte, err error) {
	evm := i.evm
	contract := state.Contract
	input := state.Input
	readOnly := state.ReadOnly

	// Increment the call depth which is restricted to 1024
	evm.depth++
	defer func() { evm.depth-- }()

	// Make sure the readOnly is only set if we aren't in readOnly yet.
	// This also makes sure that the readOnly flag isn't removed for child calls.
	if readOnly && !evm.readOnly {
		evm.readOnly = true
		defer func() { evm.readOnly = false }()
	}

	// Sonic: upstream resets evm.returnData to nil here. Resuming a stepped
	// execution has to restore the previous call's return data instead; in a
	// regular run context LastCallReturnData is nil and the effect is the same.
	evm.returnData = state.LastCallReturnData

	// Don't bother with the execution if there's no code.
	if len(contract.Code) == 0 {
		// Sonic: report no code as STOP to the caller of a stepped execution.
		state.Error = errStopToken
		return nil, nil
	}
	contract.Input = input

	// Sonic: memory and stack come from the caller-owned state instead of being
	// allocated here, so an execution can be suspended and resumed. Upstream
	// releases both in a deferred function here; Interpret does so instead, so
	// that a suspended execution keeps them.
	scope := &ScopeContext{
		Memory:   state.Memory,
		Stack:    state.Stack,
		Contract: contract,
	}

	// The generated fast path resolves its fork gates at generate time, so it
	// cannot see table changes made by ExtraEips. The same goes for verkle, which
	// the generator gets no lane for at all. Those configs run the table
	// loop, as do tracing and the differential test via forceTableLoop.
	// Sonic: the generated loop can neither resume at state.Pc nor stop after
	// maxSteps, so stepped executions run the table loop as well.
	if state.Pc != 0 || maxSteps != gomath.MaxUint64 || evm.forceTableLoop || evm.chainRules.IsEIP4762 || len(evm.Config.ExtraEips) > 0 || evm.Config.Tracer != nil {
		return evm.execTraced(scope, state, maxSteps)
	}
	return evm.execUntraced(scope)
}

// execTraced is the old table-walking interpreter loop. It dispatches every
// opcode through the per-fork operation table and emits the tracing hooks
// exactly as before. It stays hand-written so trace output does not change.
//
// The differential test also runs this loop without a tracer and compares it
// against the generated execUntraced, which keeps the two from drifting
// apart.
//
// Sonic: execution starts at state.Pc and stops after maxSteps, and the resume
// point and stop reason are handed back through state; see runSteps.
func (evm *EVM) execTraced(scope *ScopeContext, state *InterpreterState, maxSteps uint64) (ret []byte, err error) {
	var (
		op          OpCode     // current opcode
		jumpTable   *JumpTable = evm.table
		mem                    = scope.Memory
		stack                  = scope.Stack
		contract               = scope.Contract
		callContext            = scope
		// For optimisation reason we're using uint64 as the program counter.
		// It's theoretically possible to go above 2^64. The YP defines the PC
		// to be uint256. Practically much less so feasible.
		pc = state.Pc // program counter

		execCost  uint64 // execution dimension of the current opcode's cost
		stateCost uint64 // state dimension of the current opcode's cost

		// copies used by tracer
		pcCopy    uint64    // needed for the deferred EVMLogger
		gasCopy   GasBudget // budget before the opcode, for the tracer hooks
		logged    bool      // deferred EVMLogger should ignore already logged steps
		res       []byte    // result of the opcode execution function
		debug     = evm.Config.Tracer != nil
		isEIP4762 = evm.chainRules.IsEIP4762
	)
	if debug {
		defer func() { // this deferred method handles exit-with-error
			if err == nil {
				return
			}
			if !logged && evm.Config.Tracer.HasOpcodeHook() {
				evm.Config.Tracer.EmitOpcode(pcCopy, byte(op), gasCopy.AsTracing(), tracing.Gas{Execution: execCost, State: stateCost}, callContext, evm.returnData, evm.depth, VMErrorFromErr(err))
			}
			if logged && evm.Config.Tracer.HasFaultHook() {
				evm.Config.Tracer.EmitFault(pcCopy, byte(op), gasCopy.AsTracing(), tracing.Gas{Execution: execCost, State: stateCost}, callContext, evm.depth, VMErrorFromErr(err))
			}
		}()
	}
	// The Interpreter main run loop (contextual). This loop runs until either an
	// explicit STOP, RETURN or SELFDESTRUCT is executed, an error occurred during
	// the execution of one of the operations or until the done flag is set by the
	// parent context.
	// Sonic: the loop is additionally bounded by maxSteps, which is what lets Tosca's
	// conformance tests execute a fixed number of steps. A regular run passes
	// math.MaxUint64 and is unaffected.
	_ = jumpTable[0] // nil-check the jumpTable out of the loop
	for steps := uint64(0); steps < maxSteps; steps++ {
		if debug {
			// Capture pre-execution values for tracing.
			logged, pcCopy, gasCopy = false, pc, contract.Gas
		}
		if err = contract.chargeVerkleCodeChunkGas(evm, pc, isEIP4762); err != nil {
			return nil, err
		}

		// Get the operation from the jump table and validate the stack to ensure there are
		// enough stack items available to perform the operation.
		op = contract.GetOp(pc)
		operation := jumpTable[op]
		execCost, stateCost = operation.constantGas, 0 // For tracing
		// Validate stack
		if sLen := stack.len(); sLen < operation.minStack {
			return nil, &ErrStackUnderflow{stackLen: sLen, required: operation.minStack}
		} else if sLen > operation.maxStack {
			return nil, &ErrStackOverflow{stackLen: sLen, limit: operation.maxStack}
		}
		// for tracing: this gas consumption event is emitted below in the debug section.
		if !contract.Gas.ChargeExecutionOnly(execCost) {
			return nil, ErrOutOfGas
		}

		// All ops with a dynamic memory usage also has a dynamic gas cost. Size the
		// memory and charge dynamic gas here.
		var memorySize uint64
		var dynamicCost GasCosts
		memorySize, dynamicCost, err = contract.meterDynamicGas(operation, evm, stack, mem)
		// cost is explicitly set so that the capture state defer method can get the proper cost
		execCost, stateCost = execCost+dynamicCost.ExecutionGas, dynamicCost.StateGas
		if err != nil {
			return nil, err
		}

		// Do tracing before potential memory expansion
		if debug {
			if evm.Config.Tracer.HasGasHook() {
				// TODO(rjl493456442): it's broken with EIP4762, please fix it
				// when it lands.
				evm.Config.Tracer.EmitGasChange(
					gasCopy.AsTracing(),
					contract.Gas.AsTracing(),
					tracing.GasChangeCallOpCode,
				)
			}
			if evm.Config.Tracer.HasOpcodeHook() {
				evm.Config.Tracer.EmitOpcode(pc, byte(op), gasCopy.AsTracing(), tracing.Gas{Execution: execCost, State: stateCost}, callContext, evm.returnData, evm.depth, VMErrorFromErr(err))
				logged = true
			}
		}
		if memorySize > 0 {
			mem.Resize(memorySize)
		}

		// execute the operation
		res, err = operation.execute(&pc, evm, callContext)
		if err != nil {
			break
		}
		pc++
	}

	// Sonic: hand the resume point and the stop reason back to the caller.
	state.Pc = pc
	state.Error = err

	if err == errStopToken {
		err = nil // clear stop token error
	}

	return res, err
}

// computeMemorySize runs an operation's memorySize function and word-aligns the
// result, guarding overflow. The traced loop and the default case call it, and the
// direct-call ops splice its body with memFn bound to the opcode's own function.
func computeMemorySize(memFn memorySizeFunc, stack *Stack) (uint64, error) {
	memSize, overflow := memFn(stack)
	if overflow {
		return 0, ErrGasUintOverflow
	}
	// memory is expanded in words of 32 bytes. Gas is also calculated in words.
	size, overflow := math.SafeMul(toWordSize(memSize), 32)
	if overflow {
		return 0, ErrGasUintOverflow
	}
	return size, nil
}

// chargeDynamicGas runs an operation's dynamicGas function, treats a computation
// failure as out of gas, and charges the cost. It returns the cost so the traced
// loop can report it. The traced loop and the default case call it, and the
// direct-call ops splice its body with dynFn bound to the opcode's own function.
func (contract *Contract) chargeDynamicGas(dynFn gasFunc, evm *EVM, stack *Stack, mem *Memory, memorySize uint64) (GasCosts, error) {
	dynamicCost, gerr := dynFn(evm, contract, stack, mem, memorySize)
	if gerr != nil {
		return dynamicCost, fmt.Errorf("%w: %v", ErrOutOfGas, gerr)
	}
	// A execution-only deduction when there is no state gas, otherwise the full
	// multidimensional charge through the reservoir.
	if dynamicCost.StateGas == 0 {
		if !contract.Gas.ChargeExecutionOnly(dynamicCost.ExecutionGas) {
			return dynamicCost, ErrOutOfGas
		}
	} else if !contract.Gas.charge(dynamicCost) {
		return dynamicCost, ErrOutOfGas
	}
	return dynamicCost, nil
}

// meterDynamicGas sizes the memory an operation needs and charges its dynamic
// gas, returning the size to expand to and the charged cost (which the traced
// loop reports). The caller expands the memory afterward. computeMemorySize and
// chargeDynamicGas do the work, guarded here for the generic table paths. The
// direct-call ops splice those two by name and skip the guards.
func (contract *Contract) meterDynamicGas(operation *operation, evm *EVM, stack *Stack, mem *Memory) (memorySize uint64, dynamicCost GasCosts, err error) {
	if operation.dynamicGas != nil {
		if operation.memorySize != nil {
			if memorySize, err = computeMemorySize(operation.memorySize, stack); err != nil {
				return memorySize, dynamicCost, err
			}
		}
		if dynamicCost, err = contract.chargeDynamicGas(operation.dynamicGas, evm, stack, mem, memorySize); err != nil {
			return memorySize, dynamicCost, err
		}
	}
	return memorySize, dynamicCost, nil
}

// chargeVerkleCodeChunkGas charges EIP-4762 (verkle) witness gas for the code chunk the
// pc sits in. It is a no-op outside verkle and for deployment or system calls.
// isEIP4762 is passed in so the caller can hoist evm.chainRules.IsEIP4762 out of
// the loop. The traced loop calls it, the generated loop splices its body.
func (contract *Contract) chargeVerkleCodeChunkGas(evm *EVM, pc uint64, isEIP4762 bool) error {
	// if the PC ends up in a new "chunk" of verkleized code, charge the
	// associated costs.
	if isEIP4762 && !contract.IsDeployment && !contract.IsSystemCall {
		contractAddr := contract.Address()
		consumed, wanted := evm.TxContext.AccessEvents.CodeChunksRangeGas(contractAddr, pc, 1, uint64(len(contract.Code)), false, contract.Gas.ExecutionGas)
		contract.chargeExecution(consumed, evm.Config.Tracer, tracing.GasChangeWitnessCodeChunk)
		if consumed < wanted {
			return ErrOutOfGas
		}
	}
	return nil
}
