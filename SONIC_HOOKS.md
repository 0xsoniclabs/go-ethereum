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

Every edit inside an upstream file carries a `// Sonic:` comment, so
`grep -rn 'Sonic:'` lists them all. A changed signature is documented on the
function, not at its call sites. Files that belong to Sonic alone are named
`sonic_*`.

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
| `vm.Config.ChargeExcessGas` | `stateTransition.chargeExcessGas`, called from `stateTransition.settleGas` at two mutually exclusive fork-gated sites (pre-Prague, before the EIP-3529 refund cap; post-Prague, after the EIP-7623 floor-gas adjustment) — [core/state_transition.go](core/state_transition.go) | `TestStateTransition_EnablingExcessGasChargingEnablesExcessGasCharging`, `TestStateTransition_ExcessiveGasChargesAreIgnoredForTheZeroSender` (both run against pre-Prague and Prague rules), `TestStateTransition_ExcessGasIsChargedAfterTheFloorGasAdjustment`, `TestStateTransition_PrePragueExcessGasIsChargedBeforeTheRefund` (the pre-Prague charge precedes the EIP-3529 refund, and `MaxUsedGas` is taken before the charge) |
| `vm.Config.IgnoreGasFeeCap` | `stateTransition.buyGas` — [core/state_transition.go](core/state_transition.go) | `TestStateTransition_GasFeeCapCanBeIgnored` |
| `vm.Config.InsufficientBalanceIsNotAnError` | two sites in [core/state_transition.go](core/state_transition.go): the balance check in `stateTransition.buyGas`, and the value-transfer check (clause 6) in `stateTransition.execute` | `TestStateTransition_InsufficientBalanceCheckCanBeDisabled` (buyGas), `TestStateTransition_InsufficientFundsForTransferCheckCanBeDisabled` (clause 6) |
| `vm.Config.SkipTipPaymentToCoinbase` | `stateTransition.execute` — [core/state_transition.go](core/state_transition.go) | `TestStateTransition_GasTipPaymentToCoinbaseCanBeSkipped` |
| `vm.Config.MaxTxGas` | `stateTransition.maxTxGas`, which replaces `params.MaxTxGas` at all of its sites: the EIP-7825 gas-limit cap in `stateTransition.preCheck` (under Osaka, before Amsterdam), the Amsterdam execution-gas reservation in `preCheck`, the Amsterdam intrinsic and calldata-floor cap in `stateTransition.execute`, and the Amsterdam regular-gas split in `stateTransition.initRuntimeGasBudget` — [core/state_transition.go](core/state_transition.go). `GasPool.Available` takes the cap from its caller — [core/gaspool.go](core/gaspool.go) | `TestStateTransition_MaxTxGasOverridesTheProtocolCap` (the Osaka cap and the Amsterdam intrinsic cap), `TestStateTransition_MaxTxGasBoundsTheAmsterdamExecutionGas` (the regular-gas split and the execution-gas reservation) |
| `vm.Config.MaxCodeSize` | `EVM.CheckCodeSize` ([core/vm/sonic_code_size.go](core/vm/sonic_code_size.go)), called from `EVM.initNewContract` once per fork branch (EIP-4762, Amsterdam, pre-Amsterdam default) — [core/vm/evm.go](core/vm/evm.go) | `TestCustomCodeSize_MaxCodeSizeIsEnforcedWhenSet` |
| `vm.Config.MaxInitCodeSize` | `EVM.CheckInitCodeSize` ([core/vm/sonic_code_size.go](core/vm/sonic_code_size.go)), called for creation transactions from `stateTransition.preCheck` ([core/state_transition.go](core/state_transition.go)) | `TestCustomCodeSize_MaxInitCodeSizeIsEnforcedWhenSet` (pre-check entry check, unaffected by `MaxInitCodeSizeVMCreate`) |
| `vm.Config.MaxInitCodeSizeVMCreate` | `EVM.CheckInitCodeSizeVMCreate` ([core/vm/sonic_code_size.go](core/vm/sonic_code_size.go)), called for the `CREATE`/`CREATE2` instructions from `gasCreateEip3860`, `gasCreate2Eip3860`, `gasCreateEip8037` and `gasCreate2Eip8037` ([core/vm/gas_table.go](core/vm/gas_table.go)) | `TestCustomCodeSize_CreateGasAllowsCustomMaxInitCodeSizeVMCreate` (`CREATE`/`CREATE2` gas under Shanghai and Amsterdam, through the active jump table), `TestCustomCodeSize_CreateOpcodesUseMaxInitCodeSizeVMCreate` (unaffected by `MaxInitCodeSize`) |
| `vm.Config.StatePrecompiles` | `EVM.statePrecompile` plus, in `EVM.Call`, the `sp.Run(...)` dispatch branch and the added `!isStatePrecompile` condition in the EIP-158 dead-account guard — [core/vm/evm.go](core/vm/evm.go) | `TestStatePrecompiles_RegisteredContractIsDispatchedTo`, `TestStatePrecompiles_UnregisteredAddressKeepsVanillaBehavior`, `TestStatePrecompiles_LookupOnAnUnsetMap` |
| `vm.Config.Interpreter`, `vm.Config.InterpreterForTracing` | `getInterpreter` ([core/vm/sonic_tosca_integration.go](core/vm/sonic_tosca_integration.go)), called from `EVM.Run` ([core/vm/interpreter.go](core/vm/interpreter.go)) | `TestGetInterpreter_ProducesInterpretersBasedOnConfiguration` |
| `vm.EVM.CallInterceptor` | five early returns covering the six call entry points: one each in `Call`, `CallCode`, `DelegateCall` and `StaticCall`, plus one in the shared creation frame `EVM.create`, which serves `Create`, `Create2` and the `CREATE`/`CREATE2` opcodes and picks the interceptor method by opcode — [core/vm/evm.go](core/vm/evm.go). Interface: `CallContextInterceptor` in [core/vm/sonic_tosca_integration.go](core/vm/sonic_tosca_integration.go) | `TestCallInterceptor_EveryEntryPointDispatchesToTheInterceptor` (all six, behaviorally), `TestCallInterceptor_CreateOpcodesDispatchToTheInterceptor` (the opcode path into `create`), `TestCallInterceptor_WithoutAnInterceptorTheNormalPathRuns` (control), `TestSonicHookRegistry_EveryCallEntryPointStillDispatches` (structurally, by entry-point name) |
| Transaction extra payload | `Transaction.extraPayload` — [core/types/transaction.go](core/types/transaction.go), accessors in [core/types/sonic_extensions.go](core/types/sonic_extensions.go) | `TestSonicPayload_IsThreadSafe`, `TestSonicPayload_TypedReturnsFalseIfMistyped`, `TestSonicPayload_CanStoreMultipleKeys`, `TestSonicPayload_ReturnsFalseIfNotFound` |
| RPC test APIs | `GetRpcApis` — [rpc/rpc_test_utils/sonic_rpc_test_utils.go](rpc/rpc_test_utils/sonic_rpc_test_utils.go) | `TestGetRpcApis` |

## Upstream behavior Sonic depends on

The two entries below are **not** Sonic modifications — both are upstream
features. Sonic relies on them, so they are pinned by the same suite, but a
failure here means upstream changed, not that a Sonic hook was lost.

| Behavior | Where | Pinned by |
|---|---|---|
| Nil gas pool tolerance | `ApplyMessage` substitutes `NewGasPool(msg.GasLimit)` for a nil pool; Sonic uses it for single-message RPC execution — [core/state_transition.go](core/state_transition.go) | `TestStateTransition_ANilGasPoolIsTolerated` |
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
| `stateTransition.maxTxGas` calls | 4 | `core` |
| `Config.MaxCodeSize` | 1 | `core`, `core/vm` |
| `Config.MaxInitCodeSize` | 1 | `core`, `core/vm` |
| `Config.MaxInitCodeSizeVMCreate` | 1 | `core`, `core/vm` |
| `EVM.CheckCodeSize` calls | 3 | `core`, `core/vm` |
| `EVM.CheckInitCodeSize` calls | 1 | `core`, `core/vm` |
| `EVM.CheckInitCodeSizeVMCreate` calls | 4 | `core`, `core/vm` |
| `CheckMaxCodeSize` calls | 1 (the fallback in `EVM.CheckCodeSize`) | `core`, `core/vm` |
| `CheckMaxInitCodeSize` calls | 1 (the fallback in `EVM.checkInitCodeSize`, shared by `EVM.CheckInitCodeSize` and `EVM.CheckInitCodeSizeVMCreate`) | `core`, `core/vm` |
| `Config.StatePrecompiles` | 2 | `core/vm` |
| `EVM.statePrecompile` calls | 1 | `core/vm` |
| `Config.Interpreter` | 2 (nil check, call) | `core/vm` |
| `Config.InterpreterForTracing` | 2 (nil check, call) | `core/vm` |
| `getInterpreter` calls | 1 | `core/vm` |
| `CallInterceptor` dispatches | 6, spread over 5 entry points (`create` dispatches to both `Create` and `Create2`), each entry point behind exactly one nil guard | `core/vm` |
| `discfilter.Banned` calls | 3 | `p2p`, `p2p/discover` |
| `AddSource(<x>.discv5.RandomNodes())` | 1 (upstream's default feed wraps the iterator and is not counted) | `p2p` |

Upstream's own `vm.CheckMaxCodeSize` / `vm.CheckMaxInitCodeSize` keep their
upstream signatures, so a call upstream adds compiles and silently checks the
protocol limit instead of Sonic's. Within `core` and `core/vm` the census
therefore allows only the fallback call in each Sonic wrapper; route any new call
site through `EVM.CheckCodeSize`, `EVM.CheckInitCodeSize` or
`EVM.CheckInitCodeSizeVMCreate`. The calls in
`core/txpool/validation.go` and `cmd/evm/internal/t8ntool/transaction.go` are
upstream call sites outside the census scope.

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
  every pinning test matches one of `sonicHookTestPrefixes`. `SONIC_HOOK_TESTS`
  in the [Makefile](Makefile) has to be kept equal to that list by hand.

## Known limitations

| Limitation | Detail |
|---|---|
| The nil-map check in `EVM.statePrecompile` is not testable | Reading from a nil map is legal in Go and yields `(nil, false)`, so deleting the explicit `if evm.Config.StatePrecompiles == nil` check changes no behavior. `TestStatePrecompiles_LookupOnAnUnsetMap` documents the contract but cannot detect the check's removal. The behavior it guards is covered by `TestStatePrecompiles_UnregisteredAddressKeepsVanillaBehavior`. |
| Removing a Sonic *field* fails as a build error | The registry compares field sets by reflection, so deleting for example `EVM.CallInterceptor` makes the registry test itself fail to compile rather than producing a readable message. The build error still names the field and still blocks the branch. |
| Some Sonic changes have no behavioral test | `EVM.Step` ([core/vm/interpreter.go](core/vm/interpreter.go)), `forkid.NewId`, the state-test `TestContextFactory` ([tests/state_test_util.go](tests/state_test_util.go)), the RPC execution time limit ([rpc/handler.go](rpc/handler.go)), the fixed `p2p` instance directory ([node/config.go](node/config.go)), the discovery filter and its call sites, the discv5-only dial source and resolver, and the static-peer warnings ([p2p/](p2p/)). The site census pins the three `discfilter.Banned` calls and the discv5 dial source structurally. |
| `tests.TestMatcher` is pinned by compilation only | [tests/sonic_matcher_wrapper.go](tests/sonic_matcher_wrapper.go) wraps upstream's `testMatcher`, so an upstream rename fails the build. The content of [tests/init_test_matcher.go](tests/init_test_matcher.go) has to be re-synced from upstream's `tests/init_test.go` by hand on every rebase. |
| No differential harness against vanilla upstream | Out of scope here, and impossible in-process: this fork keeps `module github.com/ethereum/go-ethereum`, so upstream and fork cannot be linked into one binary. Planned separately as a cross-process comparison over `cmd/evm statetest` output. |

## Adding a hook

When a new Sonic modification lands:

1. Mark every edit inside an upstream file with a `// Sonic:` comment. For a
   changed signature, mark the function only.
2. Add a liveness test that flips the hook and asserts a specific, quantified
   behavior difference. Mutation-prove it: delete or invert the hook, confirm the
   test fails, restore.
3. Register the field in `sonicConfigFields` or `sonicEvmFields` if it is one,
   the reference count in `sonicHookSites` if the hook has several call sites,
   and the test name in `sonicHookTests` — all in
   [core/vm/sonic_hook_registry_test.go](core/vm/sonic_hook_registry_test.go).
4. Add a row to the inventory above.
5. If the test name does not start with one of the existing
   `sonicHookTestPrefixes`, extend that list and `SONIC_HOOK_TESTS` in the
   [Makefile](Makefile). If its package is new, add it to `SONIC_HOOK_PACKAGES`
   and to the directories `TestSonicHookRegistry_EveryHookStillHasAPinningTest`
   scans.
