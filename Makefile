# This Makefile is meant to be used by people that do not usually work
# with Go source code. If you know what GOPATH is then you probably
# don't need to bother with make.

.PHONY: geth evm all test lint fmt clean devtools help

GOBIN = ./build/bin
GO ?= latest
GORUN = go run

#? geth: Build geth.
geth:
	$(GORUN) build/ci.go install ./cmd/geth
	@echo "Done building."
	@echo "Run \"$(GOBIN)/geth\" to launch geth."

#? evm: Build evm.
evm:
	$(GORUN) build/ci.go install ./cmd/evm
	@echo "Done building."
	@echo "Run \"$(GOBIN)/evm\" to launch evm."

#? all: Build all packages and executables.
all:
	$(GORUN) build/ci.go install

#? test: Run the tests.
test: all
	$(GORUN) build/ci.go test

#? lint: Run certain pre-selected linters.
lint: ## Run linters.
	$(GORUN) build/ci.go lint

#? fmt: Ensure consistent code formatting.
fmt:
	gofmt -s -w $(shell find . -name "*.go")

#? clean: Clean go cache, built executables, and the auto generated folder.
clean:
	go clean -cache
	rm -fr build/_workspace/pkg/ $(GOBIN)/*

# The devtools target installs tools required for 'go generate'.
# You need to put $GOBIN (or $GOPATH/bin) in your PATH to use 'go generate'.

#? devtools: Install recommended developer tools.
devtools:
	env GOBIN= go install golang.org/x/tools/cmd/stringer@latest
	env GOBIN= go install github.com/fjl/gencodec@latest
	env GOBIN= go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	env GOBIN= go install ./cmd/abigen
	@type "solc" 2> /dev/null || echo 'Please install solc'
	@type "protoc" 2> /dev/null || echo 'Please install protoc'

#? help: Get more info on make commands.
help: Makefile
	@echo ''
	@echo 'Usage:'
	@echo '  make [target]'
	@echo ''
	@echo 'Targets:'
	@sed -n 's/^#?//p' $< | column -t -s ':' |  sort | sed -e 's/^/ /'

# --- Sonic additions -------------------------------------------------------

.PHONY: test-sonic-hooks

# The Sonic hook-liveness suite. Each of its tests flips one Sonic modification
# and asserts that observable behavior changes, so that an upstream rebase which
# drops a hook while still compiling fails here. SONIC_HOOK_TESTS must stay equal
# to sonicHookTestPrefixes in core/vm/sonic_hook_registry_test.go, which asserts
# that every hook's pinning test is selected by this pattern.
SONIC_HOOK_PACKAGES = ./core ./core/vm ./core/state
SONIC_HOOK_TESTS = ^(TestCallInterceptor_|TestCustomCodeSize_|TestEthTransferLogs|TestGetInterpreter_|TestHookedStateDB_|TestSonicHookRegistry_|TestStatePrecompiles_|TestStateTransition_)

#? test-sonic-hooks: Verify every Sonic modification is still wired in - run after any upstream rebase.
test-sonic-hooks:
	@echo "Running the Sonic hook-liveness suite over $(SONIC_HOOK_PACKAGES)"
	@echo
	@if go test -count=1 -run '$(SONIC_HOOK_TESTS)' $(SONIC_HOOK_PACKAGES); then \
		echo; \
		echo "PASS  Every Sonic hook in the registry is still wired in and effective."; \
	else \
		echo; \
		echo "FAIL  At least one Sonic modification is no longer effective."; \
		echo "      Read the failure message above: it names the hook and where it is wired."; \
		echo "      SONIC_HOOKS.md lists the full inventory and the test pinning each hook."; \
		echo "      Re-run a single case with:"; \
		echo "        go test ./core/... -run '<TestName>' -v"; \
		exit 1; \
	fi
