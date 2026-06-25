.PHONY: demo

demo: ## Materialize the demo overlay tenant under demo/config/demo and demo/imports/demo
	@set -e; for rt in $$($(PYTHON) -c "from engine.registry import generated_types; print('\n'.join(generated_types()))"); do \
		src=$$($(PYTHON) -c "from engine.registry import derive_entry; d=derive_entry('$$rt'); print(d['from'] if d else '$$rt')"); \
		f="packs/_shared/zscaler/demo/$$src.json"; \
		test -f "$$f" || continue; \
		INFRAWRIGHT_DEPLOYMENT="$(DEPLOYMENT)" $(PYTHON) -m engine.transform "$$rt" "$$f" demo; \
	done
