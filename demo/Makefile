.PHONY: demo demo-modules

demo-modules: metadata-cli ## Materialize generated local demo modules
	@INFRAWRIGHT_DEPLOYMENT="$(DEPLOYMENT)" $(INFRAWRIGHT_CLI) modules generate --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" --terraform "$(TF)" > /dev/null 2>&1

demo: demo-modules ## Materialize the demo overlay tenant and generated local demo modules
	@set -e; for rt in $$($(PYTHON) -c "from engine.registry import generated_types; print('\n'.join(generated_types()))"); do \
		src=$$($(PYTHON) -c "from engine.registry import derive_entry; d=derive_entry('$$rt'); print(d['from'] if d else '$$rt')"); \
		f="packs/_shared/zscaler/demo/$$src.json"; \
		test -f "$$f" || continue; \
		INFRAWRIGHT_DEPLOYMENT="$(DEPLOYMENT)" $(PYTHON) -m engine.transform "$$rt" "$$f" demo; \
	done
