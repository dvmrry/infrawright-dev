PYTHON ?= python3
NODE ?= node
NPM ?= npm
TF ?= terraform
OVERLAY ?= demo
DEPLOYMENT ?= deployment.json
PACK_PROFILE ?= packsets/full.json
PACK_CATALOG ?= packsets/full.json
DEMO_PACK_REQUIREMENTS ?= demo/pack-requirements.json
# Every engine invocation must see the selected deployment. ?= keeps an
# explicitly exported INFRAWRIGHT_DEPLOYMENT from the caller authoritative;
# recipe-level overrides (check-demo, check-modules) still win per-command.
export INFRAWRIGHT_DEPLOYMENT ?= $(DEPLOYMENT)
DEMO_DEPLOYMENT ?= demo/deployment.json
MODULE_DIR ?= $(shell INFRAWRIGHT_DEPLOYMENT="$(DEPLOYMENT)" $(PYTHON) -m engine.deployment module-dir)
INFRAWRIGHT_CLI ?= $(NODE) dist/infrawright-cli.mjs
METADATA_CLI_INPUTS := $(shell find node-src scripts -type f \( -name '*.ts' -o -name 'build-metadata-cli.mjs' \) -print)
OPTIONAL_TENANT_ARG = $(if $(filter undefined,$(origin TENANT)),,--tenant "$(TENANT)")

-include local.mk
ifneq ($(strip $(OVERLAY)),)
-include $(OVERLAY)/Makefile
-include $(OVERLAY)/local.mk
endif

.PHONY: metadata-cli check-demo check-examples check-modules check-tfvars-fmt check-pack check-pack-set audit-vendor-boundary demo-contract check check-all check-core test fetch fetch-diag gen-env transform adopt reconcile openapi-map source-operation-map source-evidence-eval provider-probe roots scope-paths plan-roots stage-imports unstage-imports plan clean-plans assert-clean assert-adoptable apply

dist/infrawright-cli.mjs: package.json package-lock.json tsconfig.json $(METADATA_CLI_INPUTS)
	$(NPM) run build:metadata-cli

metadata-cli: dist/infrawright-cli.mjs

check-demo: ## Fail if the shipped demo overlay drifts from pipeline output
	@INFRAWRIGHT_DEPLOYMENT="$(DEMO_DEPLOYMENT)" $(MAKE) OVERLAY=demo DEPLOYMENT="$(DEMO_DEPLOYMENT)" demo > /dev/null 2>&1
	@status="$$(git status --porcelain -- demo/config/demo demo/imports/demo)" || { \
		echo "check-demo: unable to inspect demo drift" >&2; exit 1; }; \
	test -z "$$status" || { echo "demo drift:"; echo "$$status"; exit 1; }

check-examples: metadata-cli ## Validate examples whose declared pack requirements are installed
	@set +e; output="$$( $(INFRAWRIGHT_CLI) check-pack-set --catalog "$(PACK_CATALOG)" --requirements "$(DEMO_PACK_REQUIREMENTS)" 2>&1 )"; status=$$?; set -e; \
	if [ $$status -eq 0 ]; then \
		echo "$$output"; $(MAKE) check-demo; \
	elif [ $$status -eq 3 ]; then \
		echo "check-examples: skip demo ($$output)"; \
	else \
		echo "$$output" >&2; exit $$status; \
	fi

check-modules: ## Generate every module into a temp deployment to catch generator regressions
	@tmp="$$(mktemp -d)"; trap 'rm -rf "$$tmp"' EXIT; \
	printf '{"module_dir": "%s/modules"}\n' "$$tmp" > "$$tmp/deployment.json"; \
	INFRAWRIGHT_DEPLOYMENT="$$tmp/deployment.json" $(PYTHON) -m engine.gen_module > /dev/null 2>&1; \
	$(PYTHON) -m engine.gen_module --check-output "$$tmp/modules" > /dev/null

check-tfvars-fmt: metadata-cli ## Validate HCL tfvars formatting when deployment selects hcl
	@fmt="$$(INFRAWRIGHT_DEPLOYMENT="$(DEPLOYMENT)" $(INFRAWRIGHT_CLI) deployment tfvars-format)" || exit $$?; \
	if [ "$$fmt" = "json" ]; then echo "check-tfvars-fmt: skip (json tfvars)"; exit 0; fi; \
	if ! command -v "$(TF)" >/dev/null 2>&1; then echo "check-tfvars-fmt: skip (no terraform)"; exit 0; fi; \
	overlay="$$(INFRAWRIGHT_DEPLOYMENT="$(DEPLOYMENT)" $(INFRAWRIGHT_CLI) deployment overlay)" || exit $$?; \
	if [ "$$overlay" = "." ]; then config_dir="config"; else config_dir="$$overlay/config"; fi; \
	if [ ! -d "$$config_dir" ]; then echo "check-tfvars-fmt: no config dirs"; exit 0; fi; \
	"$(TF)" fmt -check -recursive "$$config_dir"

check-pack: metadata-cli ## Validate pack.json and registry.json metadata ([PACK=<name>])
	$(INFRAWRIGHT_CLI) check-pack $(if $(PACK),--pack "$(PACK)")

check-pack-set: metadata-cli ## Require the installed pack root to match PACK_PROFILE exactly
	$(INFRAWRIGHT_CLI) check-pack-set --catalog "$(PACK_CATALOG)" --profile "$(PACK_PROFILE)"

audit-vendor-boundary: ## Audit vendor-specific tokens in engine source
	$(PYTHON) -m engine.audit_vendor_boundary

demo-contract: metadata-cli ## Credential-free demo artifact/module contract check
	@echo "demo-contract: materializing demo overlay without credentials"
	@INFRAWRIGHT_DEPLOYMENT="$(DEMO_DEPLOYMENT)" $(MAKE) OVERLAY=demo DEPLOYMENT="$(DEMO_DEPLOYMENT)" demo > /dev/null 2>&1
	@status="$$(git status --porcelain -- demo/config/demo demo/imports/demo)" || { \
		echo "demo-contract: unable to inspect demo drift" >&2; exit 1; }; \
	test -z "$$status" || { \
		echo "demo-contract: demo config/import artifacts drifted:"; \
		echo "$$status"; exit 1; }
	@test -z "$$(find demo/imports/demo -name '*_moves.tf' -print)" || { \
		echo "demo-contract: stale demo moved-block files found:"; \
		find demo/imports/demo -name '*_moves.tf' -print; exit 1; }
	@module_dir="$$(INFRAWRIGHT_DEPLOYMENT="$(DEMO_DEPLOYMENT)" $(INFRAWRIGHT_CLI) deployment module-dir)"; \
	INFRAWRIGHT_DEPLOYMENT="$(DEMO_DEPLOYMENT)" $(PYTHON) -m engine.gen_module --check-output "$$module_dir" > /dev/null; \
	echo "demo-contract: committed demo config/imports and generated modules are in sync"
	@echo "demo-contract: live provider import/plan proof requires credentials and the adoption workflow"

check: check-pack-set test check-examples check-modules check-tfvars-fmt check-pack audit-vendor-boundary ## Active-distribution gate: exact pack set + selected tests/examples + generators + metadata

check-all: ## Run the active-distribution gate against the complete upstream pack catalog
	@INFRAWRIGHT_PACKS="$(CURDIR)/packs" $(MAKE) PACK_CATALOG="$(CURDIR)/packsets/full.json" PACK_PROFILE="$(CURDIR)/packsets/full.json" check

check-core: ## Prove the pack-independent engine surface with an empty pack root
	@root="$$(mktemp -d)"; trap 'rm -rf "$$root"' EXIT; \
	INFRAWRIGHT_PACKS="$$root" $(MAKE) PACK_CATALOG="$(CURDIR)/packsets/full.json" PACK_PROFILE="$(CURDIR)/packsets/empty.json" \
		test check-pack check-modules audit-vendor-boundary

test: check-pack-set ## Run core tests plus tests whose declared pack requirements are installed
	$(PYTHON) -m tests.run --catalog "$(PACK_CATALOG)" -v

fetch: ## Pull API JSON into pulls/<tenant> (TENANT=<name> [RESOURCE="<type|provider> ..."])
	@test -n "$(TENANT)" || { echo "usage: make fetch TENANT=<tenant> [RESOURCE=\"<type|provider> ...\"]"; exit 2; }
	$(PYTHON) -m engine.collectors.rest "$(TENANT)" $(RESOURCE)

fetch-diag: ## Probe TLS to the fetcher's hosts under system trust and +bundle
	$(PYTHON) -m engine.collectors.rest --diag

gen-env: ## Generate env roots for a tenant (TENANT=<label> [BACKEND=azurerm] [RESOURCE="<type|provider> ..."])
	@test -n "$(TENANT)" || { echo "usage: make gen-env TENANT=<label> [BACKEND=azurerm] [RESOURCE=\"<type|provider> ...\"]"; exit 2; }
	$(PYTHON) -m engine.gen_env "$(TENANT)" $(if $(BACKEND),--backend "$(BACKEND)") $(RESOURCE)

transform: ## Transform pulled JSON for a tenant (IN=<dir> TENANT=<name> [RESOURCE="<type|provider> ..."])
	@test -n "$(IN)" -a -n "$(TENANT)" || { echo "usage: make transform IN=pulls/<tenant> TENANT=<tenant> [RESOURCE=\"<type|provider> ...\"]"; exit 2; }
	@set -e; resources="$$($(PYTHON) -m engine.ops resources --order=references $(RESOURCE))"; failed=""; for rt in $$resources; do \
		src=$$($(PYTHON) -c "from engine.registry import derive_entry; d=derive_entry('$$rt'); print(d['from'] if d else '$$rt')"); \
		f="$(IN)/$$src.json"; \
		if [ -f "$$f" ]; then \
			$(PYTHON) -m engine.transform "$$rt" "$$f" "$(TENANT)" || failed="$$failed $$rt"; \
		else \
			echo "skip $$rt (no $$f)"; \
		fi; \
	done; \
	test -z "$$failed" || { echo ""; echo "transform FAILED for:$$failed"; exit 1; }

reconcile: ## Compare API JSON to Terraform schema (RESOURCE=<type> IN=<api.json> [SCHEMA=<schema.json>] [API_OPTIONS=<options.json>] [OPENAPI=<spec.json>] [OPENAPI_READ=<METHOD:/path>] [OPENAPI_WRITE="<METHOD:/path> ..."] [OVERRIDE=<override.json>] [OUT=<report.json>] [STRICT=1])
	@test -n "$(RESOURCE)" -a -n "$(IN)" || { echo "usage: make reconcile RESOURCE=<type> IN=<api.json> [SCHEMA=<schema.json>] [API_OPTIONS=<options.json>] [OPENAPI=<spec.json>] [OPENAPI_READ=<METHOD:/path>] [OPENAPI_WRITE=\"<METHOD:/path> ...\"] [OVERRIDE=<override.json>] [OUT=<report.json>] [STRICT=1]"; exit 2; }
	$(PYTHON) -m engine.reconcile_schema_api "$(RESOURCE)" --api "$(IN)" $(if $(SCHEMA),--schema "$(SCHEMA)") $(if $(PROVIDER_SOURCE),--provider-source "$(PROVIDER_SOURCE)") $(if $(API_OPTIONS),--api-options "$(API_OPTIONS)") $(if $(OPENAPI),--openapi "$(OPENAPI)") $(if $(OPENAPI_READ),--openapi-read "$(OPENAPI_READ)") $(foreach op,$(OPENAPI_WRITE),--openapi-write "$(op)") $(if $(OVERRIDE),--override "$(OVERRIDE)") $(if $(OUT),--out "$(OUT)") $(if $(STRICT),--fail-on-unknown)

openapi-map: ## Map provider resources to OpenAPI CRUD endpoints (SCHEMA=<schema.json> OPENAPI=<spec.json> [PROVIDER_SOURCE=<addr>] [RESOURCE_PREFIX=<prefix>] [API_PREFIX=/api/] [REGISTRY=<registry.json>] [OUT=<report.json>])
	@test -n "$(SCHEMA)" -a -n "$(OPENAPI)" || { echo "usage: make openapi-map SCHEMA=<schema.json> OPENAPI=<spec.json> [PROVIDER_SOURCE=<addr>] [RESOURCE_PREFIX=<prefix>] [API_PREFIX=/api/] [REGISTRY=<registry.json>] [OUT=<report.json>]"; exit 2; }
	$(PYTHON) -m engine.openapi_resource_map --schema "$(SCHEMA)" --openapi "$(OPENAPI)" $(if $(PROVIDER_SOURCE),--provider-source "$(PROVIDER_SOURCE)") $(if $(RESOURCE_PREFIX),--resource-prefix "$(RESOURCE_PREFIX)") $(if $(API_PREFIX),--api-prefix "$(API_PREFIX)") $(if $(REGISTRY),--registry "$(REGISTRY)") $(if $(OUT),--out "$(OUT)")

source-operation-map: ## Derive read registry from provider source OpenAPI operation calls (SCHEMA=<schema.json> OPENAPI=<spec.json> SOURCE_ROOT=<dir> [PROVIDER_SOURCE=<addr>] [RESOURCE_PREFIX=<prefix>] [RESOURCES=a,b] [SDK_ROOT=<dir>] [OUT=<registry.json>] [DIAGNOSTICS=<report.json>])
	@test -n "$(SCHEMA)" -a -n "$(OPENAPI)" -a -n "$(SOURCE_ROOT)" || { echo "usage: make source-operation-map SCHEMA=<schema.json> OPENAPI=<spec.json> SOURCE_ROOT=<dir> [PROVIDER_SOURCE=<addr>] [RESOURCE_PREFIX=<prefix>] [RESOURCES=a,b] [SDK_ROOT=<dir>] [OUT=<registry.json>] [DIAGNOSTICS=<report.json>]"; exit 2; }
	$(PYTHON) -m engine.source_operation_map --schema "$(SCHEMA)" --openapi "$(OPENAPI)" --source-root "$(SOURCE_ROOT)" $(if $(PROVIDER_SOURCE),--provider-source "$(PROVIDER_SOURCE)") $(if $(RESOURCE_PREFIX),--resource-prefix "$(RESOURCE_PREFIX)") $(if $(RESOURCES),--resources "$(RESOURCES)") $(if $(SDK_ROOT),--sdk-root "$(SDK_ROOT)") $(if $(OUT),--out "$(OUT)") $(if $(DIAGNOSTICS),--diagnostics "$(DIAGNOSTICS)")

source-evidence-eval: ## A/B evaluate text source scanning vs AST facts (SCHEMA=<schema.json> OPENAPI=<spec.json> SOURCE_ROOT=<dir> OUT_DIR=<dir> [SOURCE_FACTS=<facts.json>] [PROVIDER_SOURCE=<addr>] [RESOURCE_PREFIX=<prefix>] [RESOURCES=a,b] [FAIL_ON_REGRESSION=1])
	@test -n "$(SCHEMA)" -a -n "$(OPENAPI)" -a -n "$(SOURCE_ROOT)" -a -n "$(OUT_DIR)" || { echo "usage: make source-evidence-eval SCHEMA=<schema.json> OPENAPI=<spec.json> SOURCE_ROOT=<dir> OUT_DIR=<dir> [SOURCE_FACTS=<facts.json>] [PROVIDER_SOURCE=<addr>] [RESOURCE_PREFIX=<prefix>] [RESOURCES=a,b] [FAIL_ON_REGRESSION=1]"; exit 2; }
	$(PYTHON) -m engine.source_evidence_eval --schema "$(SCHEMA)" --openapi "$(OPENAPI)" --source-root "$(SOURCE_ROOT)" --out-dir "$(OUT_DIR)" $(if $(PROVIDER_SOURCE),--provider-source "$(PROVIDER_SOURCE)") $(if $(RESOURCE_PREFIX),--resource-prefix "$(RESOURCE_PREFIX)") $(if $(RESOURCES),--resources "$(RESOURCES)") $(if $(SOURCE_FACTS),--source-facts "$(SOURCE_FACTS)") $(if $(AST_TOOL_DIR),--ast-tool-dir "$(AST_TOOL_DIR)") $(if $(FAIL_ON_REGRESSION),--fail-on-regression)

adopt: ## Transform pulled JSON using Terraform/OpenTofu import oracle (IN=<dir> TENANT=<name> [RESOURCE="<type|provider> ..."] [POLICY=<file>])
	@test -n "$(IN)" -a -n "$(TENANT)" || { echo "usage: make adopt IN=pulls/<tenant> TENANT=<tenant> [RESOURCE=\"<type|provider> ...\"] [POLICY=<file>]"; exit 2; }
	@set -e; resources="$$($(PYTHON) -m engine.ops resources --order=references $(RESOURCE))"; failed=""; for rt in $$resources; do \
		src=$$($(PYTHON) -c "from engine.registry import derive_entry; d=derive_entry('$$rt'); print(d['from'] if d else '$$rt')"); \
		f="$(IN)/$$src.json"; \
		if [ -f "$$f" ]; then \
			$(PYTHON) -m engine.adopt "$$rt" "$$f" "$(TENANT)" $(if $(POLICY),--policy "$(POLICY)") || failed="$$failed $$rt"; \
		else \
			echo "skip $$rt (no $$f)"; \
		fi; \
	done; \
	test -z "$$failed" || { echo ""; echo "adopt FAILED for:$$failed"; exit 1; }

provider-probe: ## Run provider readiness probe (RECIPE=<recipe.json> [WORK_DIR=<dir>] [OUT=<summary.json>] [MARKDOWN=<summary.md>])
	@test -n "$(RECIPE)" || { echo "usage: make provider-probe RECIPE=<recipe.json> [WORK_DIR=<dir>] [OUT=<summary.json>] [MARKDOWN=<summary.md>]"; exit 2; }
	$(PYTHON) -m engine.provider_probe "$(RECIPE)" $(if $(WORK_DIR),--work-dir "$(WORK_DIR)") $(if $(OUT),--out "$(OUT)") $(if $(MARKDOWN),--markdown "$(MARKDOWN)")

roots: ## Emit root topology JSON ([TENANT=<label>] [RESOURCE=<type|provider>])
	@$(PYTHON) -m engine.ops roots --json $(OPTIONAL_TENANT_ARG) $(RESOURCE)

scope-paths: ## Map changed paths to affected whole roots (PATHS_JSON=<file|->)
	@test -n "$(PATHS_JSON)" || { echo "usage: make scope-paths PATHS_JSON=<file|->"; exit 2; }
	@$(PYTHON) -m engine.ops scope-paths --json --paths-json "$(PATHS_JSON)"

plan-roots: ## Enumerate materialized env roots and plan artifacts ([TENANT=<label>] [RESOURCE=<type|provider>])
	@$(PYTHON) -m engine.ops plan-roots --json $(OPTIONAL_TENANT_ARG) $(RESOURCE)

stage-imports: ## Copy import/moved blocks into env roots (TENANT=<label> [RESOURCE=<type|provider>] [STATE_AWARE=1] [BACKEND_CONFIG=<file>])
	$(PYTHON) -m engine.ops stage-imports --tenant "$(TENANT)" $(if $(STATE_AWARE),--state-aware) $(if $(BACKEND_CONFIG),--backend-config "$(BACKEND_CONFIG)") $(RESOURCE)

unstage-imports: ## Remove staged import/moved blocks from env roots (TENANT=<label> [RESOURCE=<type|provider>])
	$(PYTHON) -m engine.ops unstage-imports --tenant "$(TENANT)" $(RESOURCE)

plan: ## Terraform plan for tenant roots (TENANT=<label> [RESOURCE=<type|provider>] [IMPORTS_ONLY=1] [SAVE=1] [BACKEND_CONFIG=<file>])
	$(PYTHON) -m engine.ops plan --tenant "$(TENANT)" $(if $(IMPORTS_ONLY),--imports-only) $(if $(SAVE),--save) $(if $(BACKEND_CONFIG),--backend-config "$(BACKEND_CONFIG)") $(RESOURCE)

clean-plans: ## Delete saved tfplan artifacts ([TENANT=<label>] [RESOURCE=<type|provider>])
	$(PYTHON) -m engine.ops clean-plans $(OPTIONAL_TENANT_ARG) $(RESOURCE)

assert-clean: ## Exit 0 only when every saved plan is no-op/import-only ([TENANT=<label>] [RESOURCE=<type|provider>] [BACKEND_CONFIG=<file>] [REPORT=<file>])
	@$(PYTHON) -m engine.ops assert-clean $(OPTIONAL_TENANT_ARG) $(if $(BACKEND_CONFIG),--backend-config "$(BACKEND_CONFIG)") $(if $(REPORT),--report "$(REPORT)") $(RESOURCE)

assert-adoptable: ## Classify saved plans with optional consumer drift policy ([TENANT=<label>] [RESOURCE=<type|provider>] [POLICY=<file>] [BACKEND_CONFIG=<file>] [REPORT=<file>])
	@$(PYTHON) -m engine.ops assert-adoptable $(OPTIONAL_TENANT_ARG) $(if $(POLICY),--policy "$(POLICY)") $(if $(BACKEND_CONFIG),--backend-config "$(BACKEND_CONFIG)") $(if $(REPORT),--report "$(REPORT)") $(RESOURCE)

apply: ## Apply saved plans ([TENANT=<label>] [RESOURCE=<type|provider>] [POLICY=<file>] [BACKEND_CONFIG=<file>] [ALLOW_DESTROY=1] [ALLOW_NON_MAIN=1] [ALLOW_PLAN_CHANGES=1])
	$(PYTHON) -m engine.ops apply $(OPTIONAL_TENANT_ARG) $(if $(POLICY),--policy "$(POLICY)") $(if $(BACKEND_CONFIG),--backend-config "$(BACKEND_CONFIG)") $(if $(ALLOW_DESTROY),--allow-destroy) $(if $(ALLOW_NON_MAIN),--allow-non-main) $(if $(ALLOW_PLAN_CHANGES),--allow-plan-changes) $(if $(MAIN_BRANCH),--main-branch "$(MAIN_BRANCH)") $(RESOURCE)
