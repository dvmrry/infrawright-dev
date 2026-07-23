.PHONY: demo demo-modules

demo-modules: dist/iw ## Materialize generated local demo modules
	@INFRAWRIGHT_DEPLOYMENT="$(DEPLOYMENT)" $(IW) modules generate --profile "$(PACK_PROFILE)" > /dev/null 2>&1

demo: demo-modules ## Materialize the demo overlay tenant and generated local demo modules
	@INFRAWRIGHT_DEPLOYMENT="$(DEPLOYMENT)" $(IW) transform --in packs/_shared/zscaler/demo --tenant demo --profile "$(PACK_PROFILE)"
