PYTHON ?= python3
TF ?= terraform

.PHONY: demo check-demo check-modules check test fetch fetch-diag gen-env transform stage-imports unstage-imports plan clean-plans assert-clean apply

demo: ## Materialize the demo tenant under config/demo and imports/demo
	@set -e; for rt in $$($(PYTHON) -c "from engine.registry import generated_types; print('\n'.join(generated_types()))"); do \
		src=$$($(PYTHON) -c "from engine.registry import derive_entry; d=derive_entry('$$rt'); print(d['from'] if d else '$$rt')"); \
		f="packs/_shared/zscaler/demo/$$src.json"; \
		test -f "$$f" || continue; \
		$(PYTHON) -m engine.transform "$$rt" "$$f" demo; \
	done

check-demo: ## Fail if the committed demo tenant drifts from pipeline output
	$(MAKE) demo > /dev/null 2>&1
	@test -z "$$(git status --porcelain -- config/demo imports/demo)" || { \
		echo "demo drift:"; git status --porcelain -- config/demo imports/demo; exit 1; }

check-modules: ## Fail if generated modules drift from committed output
	$(PYTHON) -m engine.gen_module > /dev/null 2>&1
	@test -z "$$(git status --porcelain -- modules)" || { \
		echo "modules drifted from generator output:"; \
		git status --porcelain -- modules; \
		echo "Run 'python -m engine.gen_module' and commit (or fix the regression)."; exit 1; }

check: test check-demo check-modules ## Full gate: unit tests + demo + module byte-identity

test: ## Run engine unit tests
	$(PYTHON) -m unittest discover -s tests -t . -v

fetch: ## Pull API JSON into pulls/<tenant> (TENANT=<name> [RESOURCE="<type|provider> ..."])
	@test -n "$(TENANT)" || { echo "usage: make fetch TENANT=<tenant> [RESOURCE=\"<type|provider> ...\"]"; exit 2; }
	$(PYTHON) -m collectors.rest "$(TENANT)" $(RESOURCE)

fetch-diag: ## Probe TLS to the fetcher's hosts under system trust and +bundle
	$(PYTHON) -m collectors.rest --diag

gen-env: ## Generate env roots for a tenant (TENANT=<label> [BACKEND=azurerm] [RESOURCE="<type|provider> ..."])
	@test -n "$(TENANT)" || { echo "usage: make gen-env TENANT=<label> [BACKEND=azurerm] [RESOURCE=\"<type|provider> ...\"]"; exit 2; }
	$(PYTHON) -m engine.gen_env "$(TENANT)" $(if $(BACKEND),--backend "$(BACKEND)") $(RESOURCE)

transform: ## Transform pulled JSON for a tenant (IN=<dir> TENANT=<name> [RESOURCE="<type|provider> ..."])
	@test -n "$(IN)" -a -n "$(TENANT)" || { echo "usage: make transform IN=pulls/<tenant> TENANT=<tenant> [RESOURCE=\"<type|provider> ...\"]"; exit 2; }
	@set -e; resources="$$($(PYTHON) -m engine.ops resources $(RESOURCE))"; failed=""; for rt in $$resources; do \
		src=$$($(PYTHON) -c "from engine.registry import derive_entry; d=derive_entry('$$rt'); print(d['from'] if d else '$$rt')"); \
		f="$(IN)/$$src.json"; \
		if [ -f "$$f" ]; then \
			$(PYTHON) -m engine.transform "$$rt" "$$f" "$(TENANT)" || failed="$$failed $$rt"; \
		else \
			echo "skip $$rt (no $$f)"; \
		fi; \
	done; \
	test -z "$$failed" || { echo ""; echo "transform FAILED for:$$failed"; exit 1; }

stage-imports: ## Copy import/moved blocks into env roots (TENANT=<label> [RESOURCE=<type|provider>] [STATE_AWARE=1] [BACKEND_CONFIG=<file>])
	$(PYTHON) -m engine.ops stage-imports --tenant "$(TENANT)" $(if $(STATE_AWARE),--state-aware) $(if $(BACKEND_CONFIG),--backend-config "$(BACKEND_CONFIG)") $(RESOURCE)

unstage-imports: ## Remove staged import/moved blocks from env roots (TENANT=<label> [RESOURCE=<type|provider>])
	$(PYTHON) -m engine.ops unstage-imports --tenant "$(TENANT)" $(RESOURCE)

plan: ## Terraform plan for tenant roots (TENANT=<label> [RESOURCE=<type|provider>] [IMPORTS_ONLY=1] [SAVE=1] [BACKEND_CONFIG=<file>])
	$(PYTHON) -m engine.ops plan --tenant "$(TENANT)" $(if $(IMPORTS_ONLY),--imports-only) $(if $(SAVE),--save) $(if $(BACKEND_CONFIG),--backend-config "$(BACKEND_CONFIG)") $(RESOURCE)

clean-plans: ## Delete saved tfplan artifacts ([TENANT=<label>] [RESOURCE=<type|provider>])
	$(PYTHON) -m engine.ops clean-plans $(if $(TENANT),--tenant "$(TENANT)") $(RESOURCE)

assert-clean: ## Exit 0 only when every saved plan is no-op/import-only ([TENANT=<label>] [RESOURCE=<type|provider>])
	$(PYTHON) -m engine.ops assert-clean $(if $(TENANT),--tenant "$(TENANT)") $(RESOURCE)

apply: ## Apply saved plans ([TENANT=<label>] [RESOURCE=<type|provider>] [BACKEND_CONFIG=<file>] [ALLOW_DESTROY=1] [ALLOW_NON_MAIN=1])
	$(PYTHON) -m engine.ops apply $(if $(TENANT),--tenant "$(TENANT)") $(if $(BACKEND_CONFIG),--backend-config "$(BACKEND_CONFIG)") $(if $(ALLOW_DESTROY),--allow-destroy) $(if $(ALLOW_NON_MAIN),--allow-non-main) $(if $(MAIN_BRANCH),--main-branch "$(MAIN_BRANCH)") $(RESOURCE)
