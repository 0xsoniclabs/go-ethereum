package vm

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/params"
)

// This file is the registry of Sonic's hooks into upstream go-ethereum, and the
// tripwire that keeps that registry honest.
//
// Every other test in this suite flips one hook and asserts that behavior
// changes. Those tests are a fixed list, and a fixed list silently stops being
// complete: a rebase that renames a Sonic field, adds a new one, or deletes a
// hook together with its test breaks nothing that anybody notices.
//
// The tests here close that gap in two ways:
//
//  1. A field-set assertion over vm.Config and vm.EVM. The Sonic-added fields
//     and the upstream fields are both listed explicitly, so any addition,
//     removal or rename on either side fails with a message naming the field.
//  2. A hook-site census. For each hook, the number of places in the source tree
//     that reference it is asserted. A rebase that drops one of two call sites -
//     which still compiles and still passes every behavioral test that happens
//     to exercise the surviving site - fails here.
//
// Both are maintenance points by design. When a failure is the intended
// consequence of a deliberate change, update the expectation in this file and
// say why in the commit message.

const registryMaintenanceHint = "If this change is intended, update the registry in " +
	"core/vm/sonic_hook_registry_test.go and justify it in the commit message; " +
	"otherwise restore the Sonic modification."

///////////////////////////
// Part 1: field sets

// sonicConfigFields are the fields Sonic adds to vm.Config. Each one is a hook:
// upstream code was modified to consult it.
var sonicConfigFields = map[string]reflect.Type{
	"StatePrecompiles":                reflect.TypeOf(map[common.Address]PrecompiledStateContract(nil)),
	"Interpreter":                     reflect.TypeOf(InterpreterFactory(nil)),
	"InterpreterForTracing":           reflect.TypeOf(InterpreterFactory(nil)),
	"ChargeExcessGas":                 reflect.TypeOf(false),
	"IgnoreGasFeeCap":                 reflect.TypeOf(false),
	"InsufficientBalanceIsNotAnError": reflect.TypeOf(false),
	"SkipTipPaymentToCoinbase":        reflect.TypeOf(false),
	"MaxTxGas":                        reflect.TypeOf((*uint64)(nil)),
	"MaxCodeSize":                     reflect.TypeOf((*int)(nil)),
	"MaxInitCodeSize":                 reflect.TypeOf((*int)(nil)),
}

// upstreamConfigFields are the vm.Config fields this fork inherits unchanged.
// They are listed so that the total field count can be pinned: an upstream
// addition shows up here rather than being mistaken for a Sonic field.
var upstreamConfigFields = map[string]reflect.Type{
	"Tracer":                  reflect.TypeOf((*tracing.Hooks)(nil)),
	"NoBaseFee":               reflect.TypeOf(false),
	"EnablePreimageRecording": reflect.TypeOf(false),
	"ExtraEips":               reflect.TypeOf([]int(nil)),
}

// sonicEvmFields are the fields Sonic adds to vm.EVM. CallInterceptor is a field
// on the EVM rather than on Config because it captures per-execution call
// routing rather than configuration.
var sonicEvmFields = map[string]reflect.Type{
	"CallInterceptor": reflect.TypeOf((*CallContextInterceptor)(nil)).Elem(),
}

// upstreamEvmFields are the vm.EVM fields this fork inherits unchanged.
var upstreamEvmFields = map[string]reflect.Type{
	"Context":     reflect.TypeOf(BlockContext{}),
	"TxContext":   reflect.TypeOf(TxContext{}),
	"StateDB":     reflect.TypeOf((*StateDB)(nil)).Elem(),
	"table":       reflect.TypeOf((*JumpTable)(nil)),
	"depth":       reflect.TypeOf(0),
	"chainConfig": reflect.TypeOf((*params.ChainConfig)(nil)),
	"chainRules":  reflect.TypeOf(params.Rules{}),
	"Config":      reflect.TypeOf(Config{}),
	"abort":       reflect.TypeOf(atomic.Bool{}),
	"callGasTemp": reflect.TypeOf(uint64(0)),
	"precompiles": reflect.TypeOf(map[common.Address]PrecompiledContract(nil)),
	"jumpDests":   reflect.TypeOf((*JumpDestCache)(nil)).Elem(),
	"readOnly":    reflect.TypeOf(false),
	"returnData":  reflect.TypeOf([]byte(nil)),
}

func TestSonicHookRegistry_ConfigCarriesExactlyTheExpectedFields(t *testing.T) {
	assertFieldSet(t, reflect.TypeOf(Config{}), "vm.Config", sonicConfigFields, upstreamConfigFields)
}

func TestSonicHookRegistry_EvmCarriesExactlyTheExpectedFields(t *testing.T) {
	assertFieldSet(t, reflect.TypeOf(EVM{}), "vm.EVM", sonicEvmFields, upstreamEvmFields)
}

func assertFieldSet(
	t *testing.T,
	structType reflect.Type,
	structName string,
	sonicFields map[string]reflect.Type,
	upstreamFields map[string]reflect.Type,
) {
	t.Helper()

	present := map[string]reflect.Type{}
	for i := range structType.NumField() {
		field := structType.Field(i)
		present[field.Name] = field.Type
	}

	for _, name := range slices.Sorted(maps.Keys(sonicFields)) {
		want := sonicFields[name]
		got, found := present[name]
		if !found {
			t.Errorf("the Sonic-added field %s.%s is gone; the hook consulting it is "+
				"either removed or renamed. %s", structName, name, registryMaintenanceHint)
			continue
		}
		if got != want {
			t.Errorf("the Sonic-added field %s.%s changed type from %v to %v; the hook "+
				"consulting it may no longer behave as intended. %s",
				structName, name, want, got, registryMaintenanceHint)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(upstreamFields)) {
		want := upstreamFields[name]
		got, found := present[name]
		if !found {
			t.Errorf("the upstream field %s.%s is gone; upstream removed it. %s",
				structName, name, registryMaintenanceHint)
			continue
		}
		if got != want {
			t.Errorf("the upstream field %s.%s changed type from %v to %v. %s",
				structName, name, want, got, registryMaintenanceHint)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(present)) {
		_, isSonic := sonicFields[name]
		_, isUpstream := upstreamFields[name]
		if !isSonic && !isUpstream {
			t.Errorf("the field %s.%s is in neither the Sonic nor the upstream registry; "+
				"if upstream added it, list it in upstreamFields - if Sonic added it, list it "+
				"in sonicFields and add a liveness test that pins the hook consulting it",
				structName, name)
		}
	}

	if got, want := len(present), len(sonicFields)+len(upstreamFields); got != want {
		t.Errorf("%s has %d fields, expected %d (%d upstream + %d Sonic). %s",
			structName, got, want, len(upstreamFields), len(sonicFields), registryMaintenanceHint)
	}
}

///////////////////////////
// Part 2: hook-site census

// Package directories scanned by the census, relative to this package.
const (
	coreVmDir    = "."
	coreDir      = ".."
	coreStateDir = "../state"
)

// hookSite states how many places in the source tree reference a given hook.
type hookSite struct {
	// hook names the Sonic modification, by field or function name. It doubles
	// as the sub-test name and therefore has to be unique.
	hook string
	// wiring says where the references are expected, for the failure message.
	wiring string
	// dirs are the package directories to scan.
	dirs []string
	// count is the expected number of matching references.
	count int
	// match reports whether a node is such a reference.
	match func(ast.Node) bool
}

var sonicHookSites = []hookSite{
	{
		hook:   "Config.ChargeExcessGas",
		wiring: "consulted once, in stateTransition.chargeExcessGas (core/state_transition.go)",
		dirs:   []string{coreDir},
		count:  1,
		match:  sonicFieldRef("ChargeExcessGas"),
	},
	{
		hook: "stateTransition.chargeExcessGas",
		wiring: "called twice from stateTransition.execute (core/state_transition.go), once on " +
			"the pre-Prague path and once inside the Prague branch after the EIP-7623 " +
			"floor-gas adjustment",
		dirs:  []string{coreDir},
		count: 2,
		match: callTo("chargeExcessGas"),
	},
	{
		hook:   "Config.IgnoreGasFeeCap",
		wiring: "consulted once, in stateTransition.buyGas (core/state_transition.go)",
		dirs:   []string{coreDir},
		count:  1,
		match:  sonicFieldRef("IgnoreGasFeeCap"),
	},
	{
		hook: "Config.InsufficientBalanceIsNotAnError",
		wiring: "consulted twice in core/state_transition.go: the balance check in " +
			"stateTransition.buyGas and the value-transfer check (clause 6) in " +
			"stateTransition.execute",
		dirs:  []string{coreDir},
		count: 2,
		match: sonicFieldRef("InsufficientBalanceIsNotAnError"),
	},
	{
		hook:   "Config.SkipTipPaymentToCoinbase",
		wiring: "consulted once, in stateTransition.execute (core/state_transition.go)",
		dirs:   []string{coreDir},
		count:  1,
		match:  sonicFieldRef("SkipTipPaymentToCoinbase"),
	},
	{
		hook: "Config.MaxTxGas",
		wiring: "consulted twice - a nil check and a dereference - in the Osaka-gated " +
			"EIP-7825 cap in stateTransition.preCheck (core/state_transition.go)",
		dirs:  []string{coreDir},
		count: 2,
		match: sonicFieldRef("MaxTxGas"),
	},
	{
		hook: "Config.MaxCodeSize",
		wiring: "consulted once, in EVM.CheckCodeSize (core/vm/sonic_code_size.go), which " +
			"falls back to upstream's CheckMaxCodeSize when unset",
		dirs:  []string{coreDir, coreVmDir},
		count: 1,
		match: sonicFieldRef("MaxCodeSize"),
	},
	{
		hook: "Config.MaxInitCodeSize",
		wiring: "consulted once, in EVM.CheckInitCodeSize (core/vm/sonic_code_size.go), which " +
			"falls back to upstream's CheckMaxInitCodeSize when unset",
		dirs:  []string{coreDir, coreVmDir},
		count: 1,
		match: sonicFieldRef("MaxInitCodeSize"),
	},
	{
		hook: "EVM.CheckCodeSize",
		wiring: "called once, on the deploy result in EVM.initNewContract (core/vm/evm.go). " +
			"A second return path in the deployment flow that bypasses this call would let " +
			"Sonic's custom code-size limit apply on some paths only",
		dirs:  []string{coreDir, coreVmDir},
		count: 1,
		match: callTo("CheckCodeSize"),
	},
	{
		hook: "EVM.CheckInitCodeSize",
		wiring: "called three times: from stateTransition.execute (core/state_transition.go) " +
			"and from gasCreateEip3860 and gasCreate2Eip3860 (core/vm/gas_table.go). The " +
			"upstream call sites in core/txpool and cmd/evm go to CheckMaxInitCodeSize " +
			"directly and are therefore not Sonic hooks",
		dirs:  []string{coreDir, coreVmDir},
		count: 3,
		match: callTo("CheckInitCodeSize"),
	},
	{
		hook:   "Config.StatePrecompiles",
		wiring: "consulted twice, both in EVM.statePrecompile (core/vm/evm.go)",
		dirs:   []string{coreVmDir},
		count:  2,
		match:  sonicFieldRef("StatePrecompiles"),
	},
	{
		hook: "EVM.statePrecompile",
		wiring: "called once, in EVM.Call (core/vm/evm.go). Its result feeds both the " +
			"dispatch branch and the added !isStatePrecompile condition in the EIP-158 " +
			"dead-account guard",
		dirs:  []string{coreVmDir},
		count: 1,
		match: callTo("statePrecompile"),
	},
	{
		hook:   "Config.Interpreter",
		wiring: "consulted twice - a nil check and a call - in getInterpreter (core/vm/sonic_tosca_integration.go)",
		dirs:   []string{coreVmDir},
		count:  2,
		match:  sonicFieldRef("Interpreter"),
	},
	{
		hook:   "Config.InterpreterForTracing",
		wiring: "consulted twice - a nil check and a call - in getInterpreter (core/vm/sonic_tosca_integration.go)",
		dirs:   []string{coreVmDir},
		count:  2,
		match:  sonicFieldRef("InterpreterForTracing"),
	},
	{
		hook: "getInterpreter",
		wiring: "called once, in EVM.Run (core/vm/interpreter.go). This is the single point " +
			"where the Tosca interpreter replaces Geth's",
		dirs:  []string{coreVmDir},
		count: 1,
		match: callTo("getInterpreter"),
	},
	// EmitLogsForBurnAccounts is an upstream v1.17.2 feature, not a Sonic
	// modification. It is censused here because Sonic depends on it; a failure
	// means upstream changed, not that a Sonic hook was lost.
	{
		hook: "StateDB.EmitLogsForBurnAccounts (upstream, call site in core)",
		wiring: "called once from core, in the Amsterdam-gated tail of " +
			"stateTransition.execute (core/state_transition.go)",
		dirs:  []string{coreDir},
		count: 1,
		match: callTo("EmitLogsForBurnAccounts"),
	},
	{
		hook: "StateDB.EmitLogsForBurnAccounts (upstream, forward in core/state)",
		wiring: "forwarded once from core/state, by hookedStateDB.EmitLogsForBurnAccounts " +
			"(core/state/statedb_hooked.go), which delegates to the inner state",
		dirs:  []string{coreStateDir},
		count: 1,
		match: callTo("EmitLogsForBurnAccounts"),
	},
}

func TestSonicHookRegistry_HookSiteCountsAreUnchanged(t *testing.T) {
	for _, site := range sonicHookSites {
		t.Run(site.hook, func(t *testing.T) {
			found := 0
			for _, dir := range site.dirs {
				for _, file := range parsePackageSources(t, dir) {
					ast.Inspect(file, func(node ast.Node) bool {
						if node != nil && site.match(node) {
							found++
						}
						return true
					})
				}
			}
			if found != site.count {
				t.Errorf("expected %d reference(s) to the Sonic hook %s, found %d.\n"+
					"Expected wiring: %s.\n%s",
					site.count, site.hook, found, site.wiring, registryMaintenanceHint)
			}
		})
	}
}

// expectedCallInterceptorDispatches maps each EVM method that must hand control
// to the interceptor to the CallContextInterceptor method it must delegate to.
// Adding a seventh entry point means adding one row here and one row in
// evmCallEntryPoints in sonic_call_interceptor_test.go.
var expectedCallInterceptorDispatches = map[string]string{
	"Call":         "Call",
	"CallCode":     "CallCode",
	"DelegateCall": "DelegateCall",
	"StaticCall":   "StaticCall",
	"Create":       "Create",
	"Create2":      "Create2",
}

// TestSonicHookRegistry_EveryCallEntryPointStillDispatches checks the
// EVM.CallInterceptor wiring structurally, so that losing the branch in a single
// entry point is reported by name. The behavioral counterpart lives in
// TestCallInterceptor_EveryEntryPointDispatchesToTheInterceptor.
func TestSonicHookRegistry_EveryCallEntryPointStillDispatches(t *testing.T) {
	dispatches := map[string]string{}
	guards := map[string]int{}

	for _, file := range parsePackageSources(t, coreVmDir) {
		for _, decl := range file.Decls {
			function, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(function, func(node ast.Node) bool {
				if method, ok := interceptorDispatch(node); ok {
					dispatches[function.Name.Name] = method
				}
				if isInterceptorNilGuard(node) {
					guards[function.Name.Name]++
				}
				return true
			})
		}
	}

	for _, entryPoint := range slices.Sorted(maps.Keys(expectedCallInterceptorDispatches)) {
		want := expectedCallInterceptorDispatches[entryPoint]
		got, found := dispatches[entryPoint]
		if !found {
			t.Errorf("EVM.%s no longer dispatches to the call interceptor; restore\n"+
				"\tif evm.CallInterceptor != nil {\n"+
				"\t\treturn evm.CallInterceptor.%s(...)\n"+
				"\t}\n"+
				"at the top of EVM.%s in core/vm/evm.go. %s",
				entryPoint, want, entryPoint, registryMaintenanceHint)
			continue
		}
		if got != want {
			t.Errorf("EVM.%s dispatches to CallContextInterceptor.%s, expected .%s. %s",
				entryPoint, got, want, registryMaintenanceHint)
		}
		if guards[entryPoint] != 1 {
			t.Errorf("EVM.%s has %d \"evm.CallInterceptor != nil\" guard(s), expected 1; "+
				"an unguarded dispatch panics whenever no interceptor is installed. %s",
				entryPoint, guards[entryPoint], registryMaintenanceHint)
		}
	}

	for _, entryPoint := range slices.Sorted(maps.Keys(dispatches)) {
		if _, expected := expectedCallInterceptorDispatches[entryPoint]; !expected {
			t.Errorf("EVM.%s dispatches to CallContextInterceptor.%s but is not in the "+
				"registry; add it to expectedCallInterceptorDispatches and to "+
				"evmCallEntryPoints in sonic_call_interceptor_test.go so the new entry "+
				"point is covered by a liveness test",
				entryPoint, dispatches[entryPoint])
		}
	}
}

///////////////////////////
// Part 3: the liveness tests themselves

// sonicHookTestPrefixes are the test-name prefixes that make up the hook-liveness
// suite. The -run pattern of the `make test-sonic-hooks` target must select
// exactly these; TestSonicHookRegistry_SuiteSelectorCoversEveryPinningTest keeps
// the two in sync.
var sonicHookTestPrefixes = []string{
	"TestCallInterceptor_",
	"TestCustomCodeSize_",
	"TestEthTransferLogs",
	"TestGetInterpreter_",
	"TestHookedStateDB_",
	"TestSonicHookRegistry_",
	"TestStatePrecompiles_",
	"TestStateTransition_",
}

// sonicHookTests maps each Sonic hook to the tests that pin it. It closes the
// last gap in the completeness guard: a rebase that removes a hook together with
// the test covering it leaves both the behavioral tests and the site census
// silent, but fails here.
var sonicHookTests = map[string][]string{
	"Config.ChargeExcessGas": {
		"TestStateTransition_EnablingExcessGasChargingEnablesExcessGasCharging",
		"TestStateTransition_ExcessiveGasChargesAreIgnoredForTheZeroSender",
		"TestStateTransition_ExcessGasIsChargedAfterTheFloorGasAdjustment",
	},
	"Config.IgnoreGasFeeCap": {
		"TestStateTransition_GasFeeCapCanBeIgnored",
	},
	"Config.InsufficientBalanceIsNotAnError": {
		"TestStateTransition_InsufficientBalanceCheckCanBeDisabled",
		"TestStateTransition_InsufficientFundsForTransferCheckCanBeDisabled",
	},
	"Config.SkipTipPaymentToCoinbase": {
		"TestStateTransition_GasTipPaymentToCoinbaseCanBeSkipped",
	},
	"Config.MaxTxGas": {
		"TestStateTransition_MaxTxGasOverridesTheProtocolCap",
	},
	"Config.MaxCodeSize": {
		"TestCustomCodeSize_MaxCodeSizeIsEnforcedWhenSet",
	},
	"Config.MaxInitCodeSize": {
		"TestCustomCodeSize_MaxInitCodeSizeIsEnforcedWhenSet",
		"TestCustomCodeSize_GasCreateEip3860AllowsCustomMaxInitCodeSize",
	},
	"Config.StatePrecompiles": {
		"TestStatePrecompiles_RegisteredContractIsDispatchedTo",
		"TestStatePrecompiles_UnregisteredAddressKeepsVanillaBehavior",
		"TestStatePrecompiles_LookupOnAnUnsetMap",
	},
	"Config.Interpreter and Config.InterpreterForTracing": {
		"TestGetInterpreter_ProducesInterpretersBasedOnConfiguration",
	},
	"EVM.CallInterceptor": {
		"TestCallInterceptor_EveryEntryPointDispatchesToTheInterceptor",
		"TestCallInterceptor_WithoutAnInterceptorTheNormalPathRuns",
		"TestSonicHookRegistry_EveryCallEntryPointStillDispatches",
	},
	// The three entries below pin upstream v1.17.2 behavior that Sonic depends
	// on rather than Sonic modifications of upstream.
	"ApplyMessage nil gas pool tolerance (upstream)": {
		"TestStateTransition_ANilGasPoolIsTolerated",
	},
	"StateDB.EmitLogsForBurnAccounts (upstream)": {
		"TestStateTransition_BurnAccountLogsAreEmittedUnderAmsterdamRules",
		"TestHookedStateDB_EmitLogsForBurnAccountsIsForwarded",
	},
	"BlockContext.Transfer rules parameter and the Amsterdam ETH transfer log (upstream)": {
		"TestEthTransferLogs",
	},
}

func TestSonicHookRegistry_EveryHookStillHasAPinningTest(t *testing.T) {
	present := map[string]bool{}
	for _, dir := range []string{coreVmDir, coreDir, coreStateDir} {
		for _, file := range parseTestSources(t, dir) {
			for _, decl := range file.Decls {
				if function, ok := decl.(*ast.FuncDecl); ok && function.Recv == nil {
					present[function.Name.Name] = true
				}
			}
		}
	}

	for _, hook := range slices.Sorted(maps.Keys(sonicHookTests)) {
		for _, test := range sonicHookTests[hook] {
			if !present[test] {
				t.Errorf("%s is gone, leaving the Sonic hook %s unpinned; restore the test, "+
					"or - if the hook itself was intentionally removed - drop both from the "+
					"registry and say why in the commit message", test, hook)
			}
		}
	}
}

func TestSonicHookRegistry_SuiteSelectorCoversEveryPinningTest(t *testing.T) {
	for _, hook := range slices.Sorted(maps.Keys(sonicHookTests)) {
		for _, test := range sonicHookTests[hook] {
			matched := slices.ContainsFunc(sonicHookTestPrefixes, func(prefix string) bool {
				return strings.HasPrefix(test, prefix)
			})
			if !matched {
				t.Errorf("%s (pinning %s) matches none of the sonicHookTestPrefixes, so "+
					"`make test-sonic-hooks` would not run it; either rename the test or add "+
					"its prefix here and to SONIC_HOOK_TESTS in the Makefile", test, hook)
			}
		}
	}
}

///////////////////////////
// AST helpers

// sonicFieldRef matches a reference to a Sonic-added field of the given name.
// References qualified by the params package are excluded, so that for example
// Config.MaxCodeSize is not confused with the params.MaxCodeSize constant it
// overrides.
func sonicFieldRef(name string) func(ast.Node) bool {
	return func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != name {
			return false
		}
		base, ok := selector.X.(*ast.Ident)
		return !ok || base.Name != "params"
	}
}

// callTo matches a call of a function or method with the given name, regardless
// of the receiver or package it is reached through.
func callTo(name string) func(ast.Node) bool {
	return func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return false
		}
		switch callee := call.Fun.(type) {
		case *ast.Ident:
			return callee.Name == name
		case *ast.SelectorExpr:
			return callee.Sel.Name == name
		}
		return false
	}
}

// interceptorDispatch reports the CallContextInterceptor method invoked by a
// node of the shape "<receiver>.CallInterceptor.<Method>(...)".
func interceptorDispatch(node ast.Node) (string, bool) {
	call, ok := node.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	method, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	field, ok := method.X.(*ast.SelectorExpr)
	if !ok || field.Sel.Name != "CallInterceptor" {
		return "", false
	}
	return method.Sel.Name, true
}

// isInterceptorNilGuard matches the "<receiver>.CallInterceptor != nil" test
// that guards each dispatch.
func isInterceptorNilGuard(node ast.Node) bool {
	comparison, ok := node.(*ast.BinaryExpr)
	if !ok || comparison.Op != token.NEQ {
		return false
	}
	field, ok := comparison.X.(*ast.SelectorExpr)
	if !ok || field.Sel.Name != "CallInterceptor" {
		return false
	}
	nilLiteral, ok := comparison.Y.(*ast.Ident)
	return ok && nilLiteral.Name == "nil"
}

// parsePackageSources parses the non-test Go sources of the package in the given
// directory, which is resolved relative to this package's directory.
func parsePackageSources(t *testing.T, dir string) []*ast.File {
	return parseSources(t, dir, func(name string) bool {
		return !strings.HasSuffix(name, "_test.go")
	})
}

// parseTestSources parses the test sources of the package in the given directory.
func parseTestSources(t *testing.T, dir string) []*ast.File {
	return parseSources(t, dir, func(name string) bool {
		return strings.HasSuffix(name, "_test.go")
	})
}

func parseSources(t *testing.T, dir string, include func(name string) bool) []*ast.File {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read %s: %v", dir, err)
	}

	fileSet := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || !include(name) {
			continue
		}
		file, err := parser.ParseFile(fileSet, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("failed to parse %s: %v", filepath.Join(dir, name), err)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		t.Fatalf("found no matching Go sources in %s; the registry cannot verify anything", dir)
	}
	return files
}
