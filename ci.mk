# CI-specific targets. Included by the main Makefile.
# This file is the single source of truth for all CI quality gates.

.PHONY: ci ci-static ci-test ci-fmt ci-vet ci-lint

ci: ci-static ci-test

ci-static: ci-fmt ci-vet ci-lint

ci-test:
	@echo "==> Running unit/integration tests with coverage..."
	KUBEBUILDER_ASSETS="$$($(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" \
		go test $$(go list ./... | grep -v /e2e) -coverprofile=cover.out
	@echo "==> Coverage summary:"
	@go tool cover -func=cover.out | tail -1

ci-fmt:
	@echo "==> Checking formatting..."
	@test -z "$$(gofmt -l .)" || (echo "gofmt found unformatted files:" && gofmt -l . && exit 1)

ci-vet:
	@echo "==> Running go vet..."
	go vet ./...

ci-lint: golangci-lint
	@echo "==> Running linter..."
	"$(GOLANGCI_LINT)" run
