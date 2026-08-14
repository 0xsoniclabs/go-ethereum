# Sonic hooks

This fork modifies upstream go-ethereum in places where upstream code has to
consult a Sonic-specific config field, dispatch to a Sonic-specific
implementation, or skip an upstream check. Those places are **hooks**.

A hook can be lost in an upstream rebase while the tree still compiles: an
`if evm.CallInterceptor != nil` early return disappears in a conflict
resolution, a `!st.evm.Config.IgnoreGasFeeCap` guard is dropped when upstream
restructures the surrounding function, a new `return` path bypasses
`CheckMaxCodeSize`. Neither the compiler nor upstream's own test suite catches
any of that — upstream tests run with a zero-valued `vm.Config`, where every
Sonic flag is off and every hook is inert by design.

The hook-liveness suite defends against that. Each test flips one hook and
asserts that observable behavior changes, so an inert hook fails a test.

## The rule

**After any upstream rebase, `make test-sonic-hooks` must pass before the branch
is considered mergeable.**

```
make test-sonic-hooks
```

A failure names the hook and where it is wired. Read the message, then either
restore the Sonic modification, or — if the change was intended — update the
registry in [core/vm/sonic_hook_registry_test.go](core/vm/sonic_hook_registry_test.go)
and justify it in the commit message. Do not silence a failure any other way.

## Hook inventory

| Hook | Wired in | Pinned by |
|---|---|---|
| `vm.Config.ChargeExcessGas` | `stateTransition.chargeExcessGas`, called from `stateTransition.execute` at two mutually exclusive fork-gated sites (pre-Prague; inside the Prague branch, after the EIP-7623 floor-gas adjustment) — [core/state_transition.go](core/state_transition.go) | `TestStateTransition_EnablingExcessGasChargingEnablesExcessGasCharging`, `TestStateTransition_ExcessiveGasChargesAreIgnoredForTheZeroSender` (both run against pre-Prague and Prague rules), `TestStateTransition_ExcessGasIsChargedAfterTheFloorGasAdjustment` |
| `vm.Config.IgnoreGasFeeCap` | `stateTransition.buyGas` — [core/state_transition.go](core/state_transition.go) | `TestStateTransition_GasFeeCapCanBeIgnored` |
| `vm.Config.InsufficientBalanceIsNotAnError` | two sites in [core/state_transition.go](core/state_transition.go): the balance check in `stateTransition.buyGas`, and the value-transfer check (clause 6) in `stateTransition.execute` | `TestStateTransition_InsufficientBalanceCheckCanBeDisabled` (buyGas), `TestStateTransition_InsufficientFundsForTransferCheckCanBeDisabled` (clause 6) |
| `vm.Config.SkipTipPaymentToCoinbase` | `stateTransition.execute` — [core/state_transition.go](core/state_transition.go) | `TestStateTransition_GasTipPaymentToCoinbaseCanBeSkipped` |
| `vm.Config.MaxTxGas` | the Osaka-gated EIP-7825 cap in `stateTransition.preCheck`, replacing `params.MaxTxGas` — [core/state_transition.go](core/state_transition.go) | `TestStateTransition_MaxTxGasOverridesTheProtocolCap` |
| `vm.Config.MaxCodeSize` | `EVM.CheckCodeSize` ([core/vm/sonic_code_size.go](core/vm/sonic_code_size.go)), called from `EVM.initNewContract` — [core/vm/evm.go](core/vm/evm.go) | `TestCustomCodeSize_MaxCodeSizeIsEnforcedWhenSet` |
| `vm.Config.MaxInitCodeSize` | `EVM.CheckInitCodeSize` ([core/vm/sonic_code_size.go](core/vm/sonic_code_size.go)), called at three sites: `stateTransition.execute` ([core/state_transition.go](core/state_transition.go)), `gasCreateEip3860` and `gasCreate2Eip3860` ([core/vm/gas_table.go](core/vm/gas_table.go)) | `TestCustomCodeSize_MaxInitCodeSizeIsEnforcedWhenSet` (state-transition entry check), `TestCustomCodeSize_GasCreateEip3860AllowsCustomMaxInitCodeSize` (EIP-3860 gas path) |
| `vm.Config.StatePrecompiles` | `EVM.statePrecompile` plus, in `EVM.Call`, the `sp.Run(...)` dispatch branch and the added `!isStatePrecompile` condition in the EIP-158 dead-account guard — [core/vm/evm.go](core/vm/evm.go) | `TestStatePrecompiles_RegisteredContractIsDispatchedTo`, `TestStatePrecompiles_UnregisteredAddressKeepsVanillaBehavior`, `TestStatePrecompiles_LookupOnAnUnsetMap` |
| `vm.Config.Interpreter`, `vm.Config.InterpreterForTracing` | `getInterpreter` ([core/vm/sonic_tosca_integration.go](core/vm/sonic_tosca_integration.go)), called from `EVM.Run` ([core/vm/interpreter.go](core/vm/interpreter.go)) | `TestGetInterpreter_ProducesInterpretersBasedOnConfiguration` |
| `vm.EVM.CallInterceptor` | six early returns, one per call entry point: `Call`, `CallCode`, `DelegateCall`, `StaticCall`, `Create`, `Create2` — [core/vm/evm.go](core/vm/evm.go). Interface: `CallContextInterceptor` in [core/vm/sonic_tosca_integration.go](core/vm/sonic_tosca_integration.go) | `TestCallInterceptor_EveryEntryPointDispatchesToTheInterceptor` (all six, behaviorally), `TestCallInterceptor_WithoutAnInterceptorTheNormalPathRuns` (control), `TestSonicHookRegistry_EveryCallEntryPointStillDispatches` (structurally, by entry-point name) |

## Upstream behavior Sonic depends on

The three entries below are **not** Sonic modifications — all three are upstream
`v1.17.2` features. Sonic relies on them, so they are pinned by the same suite,
but a failure here means upstream changed, not that a Sonic hook was lost.

| Behavior | Where | Pinned by |
|---|---|---|
| Nil gas pool tolerance | `ApplyMessage` substitutes `NewGasPool(msg.GasLimit)` for a nil pool; Sonic uses it for single-message RPC execution — [core/state_transition.go](core/state_transition.go) | `TestStateTransition_ANilGasPoolIsTolerated` |
| `vm.StateDB.EmitLogsForBurnAccounts` | interface method in [core/vm/interface.go](core/vm/interface.go); implementation in [core/state/statedb.go](core/state/statedb.go); forwarded by `hookedStateDB` in [core/state/statedb_hooked.go](core/state/statedb_hooked.go); called Amsterdam-gated at the end of `stateTransition.execute` ([core/state_transition.go](core/state_transition.go)) | `TestStateTransition_BurnAccountLogsAreEmittedUnderAmsterdamRules` (call site and its fork gate), `TestHookedStateDB_EmitLogsForBurnAccountsIsForwarded` (hooked forward) |
| `BlockContext.Transfer` rules parameter, Amsterdam ETH transfer log | `TransferFunc` carries `*params.Rules` — [core/vm/evm.go](core/vm/evm.go) | `TestEthTransferLogs` |

## Structural expectations

A behavioral test only proves that *some* path honors a hook. When a hook has
several call sites, dropping one of them still passes every test that happens to
exercise a surviving site. `TestSonicHookRegistry_HookSiteCountsAreUnchanged`
therefore parses the source and asserts the number of references per hook:

| Reference | Expected count | Scope |
|---|---|---|
| `Config.ChargeExcessGas` | 1 | `core` |
| `stateTransition.chargeExcessGas` calls | 2 | `core` |
| `Config.IgnoreGasFeeCap` | 1 | `core` |
| `Config.InsufficientBalanceIsNotAnError` | 2 | `core` |
| `Config.SkipTipPaymentToCoinbase` | 1 | `core` |
| `Config.MaxTxGas` | 2 (nil check, dereference) | `core` |
| `Config.MaxCodeSize` | 1 | `core`, `core/vm` |
| `Config.MaxInitCodeSize` | 1 | `core`, `core/vm` |
| `EVM.CheckCodeSize` calls | 1 | `core`, `core/vm` |
| `EVM.CheckInitCodeSize` calls | 3 | `core`, `core/vm` |
| `Config.StatePrecompiles` | 2 | `core/vm` |
| `EVM.statePrecompile` calls | 1 | `core/vm` |
| `Config.Interpreter` | 2 (nil check, call) | `core/vm` |
| `Config.InterpreterForTracing` | 2 (nil check, call) | `core/vm` |
| `getInterpreter` calls | 1 | `core/vm` |
| `EmitLogsForBurnAccounts` calls (upstream) | 1 | `core` |
| `EmitLogsForBurnAccounts` calls (upstream) | 1 | `core/state` |
| `CallInterceptor` dispatches | 6, one per entry point, each behind exactly one nil guard | `core/vm` |

Upstream's own `vm.CheckMaxCodeSize` / `vm.CheckMaxInitCodeSize` keep their
upstream signatures and are also called from `core/txpool/validation.go` and
`cmd/evm/internal/t8ntool/transaction.go`. Those are upstream call sites rather
than hooks and are out of the census scope.

Three further registry tests keep the registry itself honest:

- `TestSonicHookRegistry_ConfigCarriesExactlyTheExpectedFields` and
  `..._EvmCarriesExactlyTheExpectedFields` assert that `vm.Config` and `vm.EVM`
  carry exactly the listed Sonic fields plus exactly the listed upstream fields,
  with the expected types. An upstream field addition, a Sonic field removal, or
  a rename fails with the field named.
- `TestSonicHookRegistry_EveryHookStillHasAPinningTest` asserts that every test
  named in the registry still exists, so a hook cannot be removed together with
  its test unnoticed.
- `TestSonicHookRegistry_SuiteSelectorCoversEveryPinningTest` asserts that
  `SONIC_HOOK_TESTS` in the [Makefile](Makefile) still selects every pinning
  test.

## Known limitations

| Limitation | Detail |
|---|---|
| The nil-map check in `EVM.statePrecompile` is not testable | Reading from a nil map is legal in Go and yields `(nil, false)`, so deleting the explicit `if evm.Config.StatePrecompiles == nil` check changes no behavior. `TestStatePrecompiles_LookupOnAnUnsetMap` documents the contract but cannot detect the check's removal. The behavior it guards is covered by `TestStatePrecompiles_UnregisteredAddressKeepsVanillaBehavior`. |
| Removing a Sonic *field* fails as a build error | The registry compares field sets by reflection, so deleting for example `EVM.CallInterceptor` makes the registry test itself fail to compile rather than producing a readable message. The build error still names the field and still blocks the branch. |
| No differential harness against vanilla upstream | Out of scope here, and impossible in-process: this fork keeps `module github.com/ethereum/go-ethereum`, so upstream and fork cannot be linked into one binary. Planned separately as a cross-process comparison over `cmd/evm statetest` output. |

## Adding a hook

When a new Sonic modification lands:

1. Add a liveness test that flips the hook and asserts a specific, quantified
   behavior difference. Mutation-prove it: delete or invert the hook, confirm the
   test fails, restore.
2. Register the field in `sonicConfigFields` or `sonicEvmFields`, the reference
   count in `sonicHookSites`, and the test name in `sonicHookTests` — all in
   [core/vm/sonic_hook_registry_test.go](core/vm/sonic_hook_registry_test.go).
3. Add a row to the inventory above.
4. If the test name does not start with one of the existing
   `sonicHookTestPrefixes`, extend that list and `SONIC_HOOK_TESTS` in the
   [Makefile](Makefile).
