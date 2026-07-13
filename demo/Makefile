.PHONY: demo demo-modules

demo-modules: metadata-cli ## Materialize generated local demo modules
	@INFRAWRIGHT_DEPLOYMENT="$(DEPLOYMENT)" $(INFRAWRIGHT_CLI) modules generate --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" --terraform "$(TF)" > /dev/null 2>&1

demo: demo-modules ## Materialize the demo overlay tenant and generated local demo modules
	@INFRAWRIGHT_DEPLOYMENT="$(DEPLOYMENT)" $(INFRAWRIGHT_CLI) transform --in packs/_shared/zscaler/demo --tenant demo --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)"
