PYTHON ?= python3
TF ?= terraform

.PHONY: demo check-demo test transform

demo: ## Materialize the demo tenant via the zscaler pack (config/demo + imports/demo)
	@set -e; for rt in $$($(PYTHON) -c "from engine.registry import generated_types; print('\n'.join(generated_types()))"); do \
		src=$$($(PYTHON) -c "from engine.registry import derive_entry; d=derive_entry('$$rt'); print(d['from'] if d else '$$rt')"); \
		f="packs/zscaler/demo/$$src.json"; \
		test -f "$$f" || continue; \
		$(PYTHON) -m engine.transform "$$rt" "$$f" demo; \
	done

check-demo: ## Fail if the committed demo tenant drifts from pipeline output
	$(MAKE) demo > /dev/null 2>&1
	@test -z "$$(git status --porcelain -- config/demo imports/demo)" || { \
		echo "demo drift:"; git status --porcelain -- config/demo imports/demo; exit 1; }

test: ## Run engine unit tests
	$(PYTHON) -m unittest discover -s tests -t . -v

transform: ## Transform pulled JSON for a tenant (IN=<dir> TENANT=<name>)
	@test -n "$(IN)" -a -n "$(TENANT)" || { echo "usage: make transform IN=pulls/<tenant> TENANT=<tenant>"; exit 2; }
	@for rt in $$($(PYTHON) -c "from engine.registry import generated_types; print('\n'.join(generated_types()))"); do \
		src=$$($(PYTHON) -c "from engine.registry import derive_entry; d=derive_entry('$$rt'); print(d['from'] if d else '$$rt')"); \
		f="$(IN)/$$src.json"; \
		test -f "$$f" || continue; \
		$(PYTHON) -m engine.transform "$$rt" "$$f" "$(TENANT)"; \
	done
