# CI-specific targets. Included by the main Makefile.
# This file is the single source of truth for all CI quality gates.

.PHONY: ci ci-static ci-test ci-chart ci-fmt ci-vet ci-lint

ci: ci-static ci-test ci-chart

ci-static: ci-fmt ci-vet ci-lint

ci-test: envtest setup-envtest
	@echo "==> Running unit/integration tests with coverage..."
	KUBEBUILDER_ASSETS="$$($(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" \
		go test $$(go list -f '{{if .TestGoFiles}}{{.ImportPath}}{{end}}' ./... | grep -v /e2e) -coverprofile=cover.out
	@echo "==> Coverage summary:"
	@go tool cover -func=cover.out | tail -1
	@echo "==> Enforcing 100% coverage (excluding cmd, zz_generated)..."
	@grep -v -E '(zz_generated|/cmd/)' cover.out > cover.filtered.out || true
	@total=$$(go tool cover -func=cover.filtered.out | tail -1 | awk '{print $$NF}' | tr -d '%'); \
		echo "Filtered coverage: $${total}%"; \
		threshold=100; \
		if [ $$(echo "$$total < $$threshold" | bc) -eq 1 ]; then \
			echo "Error: coverage is $${total}%, minimum is $${threshold}%"; \
			go tool cover -func=cover.filtered.out | grep -v "100.0%"; \
			exit 1; \
		fi
	@rm -f cover.filtered.out

ci-chart: helm-lint helm-template helm-unittest helm-schema helm-docs-check api-docs-check

ci-fmt:
	@echo "==> Checking formatting..."
	@test -z "$$(gofmt -l .)" || (echo "gofmt found unformatted files:" && gofmt -l . && exit 1)

ci-vet:
	@echo "==> Running go vet..."
	go vet ./...

ci-lint: golangci-lint
	@echo "==> Running linter..."
	"$(GOLANGCI_LINT)" run

.PHONY: helm-template
helm-template: ## Render chart templates and validate output.
	@echo "==> Rendering chart templates..."
	helm template pacto-operator charts/pacto-operator --debug > /dev/null
	@echo "==> Rendering with dashboard disabled..."
	helm template pacto-operator charts/pacto-operator --set dashboard.enabled=false > /dev/null
	@echo "==> Rendering with ingress enabled..."
	helm template pacto-operator charts/pacto-operator --set dashboard.ingress.enabled=true > /dev/null
	@echo "==> Rendering with metrics disabled..."
	helm template pacto-operator charts/pacto-operator --set metrics.enabled=false > /dev/null

.PHONY: helm-unittest
helm-unittest: $(HELM_UNITTEST) ## Run Helm unit tests.
	@echo "==> Running Helm unit tests..."
	"$(HELM_UNITTEST)" charts/pacto-operator

.PHONY: helm-schema
helm-schema: ## Validate values.yaml against values.schema.json.
	@echo "==> Validating chart schema..."
	@python3 -c "import json; json.load(open('charts/pacto-operator/values.schema.json'))" || \
		{ echo "Error: values.schema.json is not valid JSON." >&2; exit 1; }
	@command -v check-jsonschema >/dev/null 2>&1 || pip install check-jsonschema --quiet
	@check-jsonschema --schemafile charts/pacto-operator/values.schema.json charts/pacto-operator/values.yaml

.PHONY: helm-docs-check
helm-docs-check: ## Check that helm-docs output matches committed README.
	@echo "==> Checking helm-docs drift..."
	@command -v helm-docs >/dev/null 2>&1 || { echo "Error: helm-docs not installed. Install with: go install github.com/norwoodj/helm-docs/cmd/helm-docs@latest" >&2; exit 1; }
	@helm-docs --chart-search-root charts
	@git diff --exit-code charts/pacto-operator/README.md || \
		{ echo "Error: Helm chart README is out of date. Run 'make helm-docs' and commit." >&2; exit 1; }

.PHONY: api-docs-check
api-docs-check: api-docs ## Check that API reference docs match committed output.
	@echo "==> Checking API docs drift..."
	@git diff --exit-code docs/api-reference.md || \
		{ echo "Error: API reference docs are out of date. Run 'make api-docs' and commit." >&2; exit 1; }
