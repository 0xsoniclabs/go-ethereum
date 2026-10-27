package vm

import "fmt"

// CheckCodeSize checks the size of contract code against the limit configured
// for this EVM, falling back to the protocol-defined limit when none is set.
func (evm *EVM) CheckCodeSize(size uint64) error {
	limit := evm.Config.MaxCodeSize
	if limit == nil {
		return CheckMaxCodeSize(&evm.chainRules, size)
	}
	if size > uint64(*limit) {
		return fmt.Errorf("%w: code size %v limit %v", ErrMaxCodeSizeExceeded, size, *limit)
	}
	return nil
}

// CheckInitCodeSize checks the size of a creation transaction's initcode
// against the limit configured for this EVM, falling back to the
// protocol-defined limit when none is set.
func (evm *EVM) CheckInitCodeSize(size uint64) error {
	return evm.checkInitCodeSize(evm.Config.MaxInitCodeSize, size)
}

// CheckInitCodeSizeVMCreate checks the size of the initcode of a CREATE or
// CREATE2 instruction against the limit configured for this EVM, falling back
// to the protocol-defined limit when none is set.
func (evm *EVM) CheckInitCodeSizeVMCreate(size uint64) error {
	return evm.checkInitCodeSize(evm.Config.MaxInitCodeSizeVMCreate, size)
}

func (evm *EVM) checkInitCodeSize(limit *int, size uint64) error {
	if limit == nil {
		return CheckMaxInitCodeSize(&evm.chainRules, size)
	}
	if size > uint64(*limit) {
		return fmt.Errorf("%w: init code size %v limit %v", ErrMaxInitCodeSizeExceeded, size, *limit)
	}
	return nil
}
