NODE ?= node
NPM ?= npm
TF ?= terraform
OVERLAY ?= demo
_INFRAWRIGHT_IMPORTED_DEPLOYMENT := $(INFRAWRIGHT_DEPLOYMENT)
DEPLOYMENT ?= $(if $(strip $(_INFRAWRIGHT_IMPORTED_DEPLOYMENT)),$(_INFRAWRIGHT_IMPORTED_DEPLOYMENT),deployment.json)
PACK_PROFILE ?= packsets/full.json
PACK_CATALOG ?= packsets/full.json
ROOT_CATALOG ?= catalogs/zscaler-root-catalog.v1.json
DEMO_PACK_REQUIREMENTS ?= demo/pack-requirements.json
DEMO_DEPLOYMENT ?= demo/deployment.json
INFRAWRIGHT_CLI ?= $(NODE) dist/infrawright-cli.mjs
MODULE_DIR ?= $(shell INFRAWRIGHT_DEPLOYMENT="$(DEPLOYMENT)" $(INFRAWRIGHT_CLI) deployment module-dir)
OPTIONAL_TENANT_ARG = $(if $(filter undefined,$(origin TENANT)),,--tenant "$(TENANT)")

-include local.mk
ifneq ($(strip $(OVERLAY)),)
-include $(OVERLAY)/Makefile
-include $(OVERLAY)/local.mk
endif

# DEPLOYMENT is the single Make-level authority. An imported nonempty
# INFRAWRIGHT_DEPLOYMENT supplies its default, while command-line/overlay
# DEPLOYMENT values win and are exported coherently to every recipe and nested
# Make invocation.
override INFRAWRIGHT_DEPLOYMENT = $(DEPLOYMENT)
export INFRAWRIGHT_DEPLOYMENT

.PHONY: metadata-cli verify-runtime source-build-preflight check-demo check-examples check-modules check-tfvars-fmt check-pack check-pack-set root-catalog check-root-catalog deployment resources resources-reference-order gen-modules validate-modules demo-contract check check-node check-all check-core test test-node fetch fetch-diag gen-env transform adopt reconcile openapi-map source-operation-map source-evidence-eval provider-probe roots scope-paths plan-roots stage-imports unstage-imports plan clean-plans assert-clean assert-adoptable apply

dist/infrawright-cli.mjs:
	$(NPM) run build:metadata-cli

metadata-cli: ## Explicitly rebuild the generic CLI for development
	$(NPM) run build:metadata-cli

verify-runtime: ## Verify the prebuilt generic CLI without npm or Python
	$(NODE) scripts/verify-runtime-release.mjs "$(CURDIR)" --deployment "$(DEPLOYMENT)" --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)"

source-build-preflight: ## Diagnose whether the configured npm registry can rebuild the CLI
	$(NODE) scripts/build-environment-preflight.mjs --npm "$(NPM)"

check-demo: ## Fail if the shipped demo overlay drifts from pipeline output
	@INFRAWRIGHT_DEPLOYMENT="$(DEMO_DEPLOYMENT)" $(MAKE) OVERLAY=demo DEPLOYMENT="$(DEMO_DEPLOYMENT)" demo > /dev/null 2>&1
	@status="$$(git status --porcelain -- demo/config/demo demo/imports/demo)" || { \
		echo "check-demo: unable to inspect demo drift" >&2; exit 1; }; \
	test -z "$$status" || { echo "demo drift:"; echo "$$status"; exit 1; }

check-examples: dist/infrawright-cli.mjs ## Validate examples whose declared pack requirements are installed
	@set +e; output="$$( $(INFRAWRIGHT_CLI) check-pack-set --catalog "$(PACK_CATALOG)" --requirements "$(DEMO_PACK_REQUIREMENTS)" 2>&1 )"; status=$$?; set -e; \
	if [ $$status -eq 0 ]; then \
		echo "$$output"; $(MAKE) check-demo; \
	elif [ $$status -eq 3 ]; then \
		echo "check-examples: skip demo ($$output)"; \
	else \
		echo "$$output" >&2; exit $$status; \
	fi

check-modules: dist/infrawright-cli.mjs ## Generate every module into a temp deployment to catch generator regressions
	@tmp="$$(mktemp -d)"; trap 'rm -rf "$$tmp"' EXIT; \
	printf '{"module_dir": "%s/modules"}\n' "$$tmp" > "$$tmp/deployment.json"; \
	INFRAWRIGHT_DEPLOYMENT="$$tmp/deployment.json" $(INFRAWRIGHT_CLI) modules generate --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" --terraform "$(TF)" > /dev/null 2>&1; \
	$(INFRAWRIGHT_CLI) modules validate --out "$$tmp/modules" --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" > /dev/null

check-tfvars-fmt: dist/infrawright-cli.mjs ## Validate HCL tfvars formatting when deployment selects hcl
	@fmt="$$(INFRAWRIGHT_DEPLOYMENT="$(DEPLOYMENT)" $(INFRAWRIGHT_CLI) deployment tfvars-format)" || exit $$?; \
	if [ "$$fmt" = "json" ]; then echo "check-tfvars-fmt: skip (json tfvars)"; exit 0; fi; \
	if ! command -v "$(TF)" >/dev/null 2>&1; then echo "check-tfvars-fmt: skip (no terraform)"; exit 0; fi; \
	overlay="$$(INFRAWRIGHT_DEPLOYMENT="$(DEPLOYMENT)" $(INFRAWRIGHT_CLI) deployment overlay)" || exit $$?; \
	if [ "$$overlay" = "." ]; then config_dir="config"; else config_dir="$$overlay/config"; fi; \
	if [ ! -d "$$config_dir" ]; then echo "check-tfvars-fmt: no config dirs"; exit 0; fi; \
	"$(TF)" fmt -check -recursive "$$config_dir"

check-pack: dist/infrawright-cli.mjs ## Validate pack.json and registry.json metadata ([PACK=<name>])
	$(INFRAWRIGHT_CLI) check-pack $(if $(PACK),--pack "$(PACK)")

check-pack-set: dist/infrawright-cli.mjs ## Require the installed pack root to match PACK_PROFILE exactly
	$(INFRAWRIGHT_CLI) check-pack-set --catalog "$(PACK_CATALOG)" --profile "$(PACK_PROFILE)"

root-catalog: dist/infrawright-cli.mjs ## Regenerate the all-Zscaler compatibility root catalog
	$(INFRAWRIGHT_CLI) root-catalog --providers zcc,zia,zpa,ztc --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" --out "$(ROOT_CATALOG)"

check-root-catalog: dist/infrawright-cli.mjs ## Fail when the all-Zscaler compatibility root catalog is stale
	$(INFRAWRIGHT_CLI) root-catalog --providers zcc,zia,zpa,ztc --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" --check "$(ROOT_CATALOG)"

deployment: dist/infrawright-cli.mjs ## Query deployment metadata (DEPLOYMENT_QUERY=<verb> [TENANT=<label>])
	$(INFRAWRIGHT_CLI) deployment --deployment "$(DEPLOYMENT)" "$(or $(DEPLOYMENT_QUERY),overlay)" $(if $(TENANT),"$(TENANT)")

resources: dist/infrawright-cli.mjs ## List generated resources ([RESOURCE="<type|provider> ..."] [REFERENCE_ORDER=1])
	$(INFRAWRIGHT_CLI) resources $(if $(REFERENCE_ORDER),--order=references) --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" $(foreach rt,$(RESOURCE),--resource "$(rt)")

resources-reference-order: REFERENCE_ORDER=1
resources-reference-order: resources

gen-modules: dist/infrawright-cli.mjs ## Generate deployment modules ([RESOURCE="<type> ..."])
	$(INFRAWRIGHT_CLI) modules generate --deployment "$(DEPLOYMENT)" --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" --terraform "$(TF)" $(foreach rt,$(RESOURCE),--resource "$(rt)")

validate-modules: dist/infrawright-cli.mjs ## Validate deployment modules ([RESOURCE="<type> ..."])
	$(INFRAWRIGHT_CLI) modules validate --deployment "$(DEPLOYMENT)" --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" $(foreach rt,$(RESOURCE),--resource "$(rt)")

demo-contract: dist/infrawright-cli.mjs ## Credential-free demo artifact/module contract check
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
	INFRAWRIGHT_DEPLOYMENT="$(DEMO_DEPLOYMENT)" $(INFRAWRIGHT_CLI) modules validate --out "$$module_dir" --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" > /dev/null; \
	echo "demo-contract: committed demo config/imports and generated modules are in sync"
	@echo "demo-contract: live provider import/plan proof requires credentials and the adoption workflow"

check: check-pack-set test check-examples check-modules check-tfvars-fmt check-pack ## Active-distribution gate: exact pack set + selected tests/examples + generators + metadata

check-node: check ## Explicit Python-independent repository qualification gate

check-all: ## Run the active-distribution gate against the complete upstream pack catalog
	@INFRAWRIGHT_PACKS="$(CURDIR)/packs" $(MAKE) PACK_CATALOG="$(CURDIR)/packsets/full.json" PACK_PROFILE="$(CURDIR)/packsets/full.json" check
	@INFRAWRIGHT_PACKS="$(CURDIR)/packs" $(MAKE) PACK_CATALOG="$(CURDIR)/packsets/full.json" PACK_PROFILE="$(CURDIR)/packsets/full.json" check-root-catalog

check-core: ## Prove the pack-independent engine surface with an empty pack root
	@root="$$(mktemp -d)"; trap 'rm -rf "$$root"' EXIT; \
	INFRAWRIGHT_PACKS="$$root" $(MAKE) PACK_CATALOG="$(CURDIR)/packsets/full.json" PACK_PROFILE="$(CURDIR)/packsets/empty.json" \
		test check-pack check-modules

test: test-node ## Default repository tests use the Python-independent Node suite

test-node: check-pack-set ## Run the complete Node test suite for the active pack profile
	PACK_PROFILE="$(PACK_PROFILE)" PACK_CATALOG="$(PACK_CATALOG)" $(NPM) run test:node

fetch: dist/infrawright-cli.mjs ## Pull API JSON into pulls/<tenant> (TENANT=<name> [RESOURCE="<type|provider> ..."])
	@test -n "$(TENANT)" || { echo "usage: make fetch TENANT=<tenant> [RESOURCE=\"<type|provider> ...\"]"; exit 2; }
	$(INFRAWRIGHT_CLI) fetch --tenant "$(TENANT)" --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" $(if $(FETCH_CONCURRENCY),--concurrency "$(FETCH_CONCURRENCY)") $(foreach rt,$(RESOURCE),--resource "$(rt)")

fetch-diag: dist/infrawright-cli.mjs ## Probe TLS to the fetcher's hosts under system trust and +bundle
	$(INFRAWRIGHT_CLI) fetch-diag --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)"

gen-env: dist/infrawright-cli.mjs ## Generate env roots for a tenant (TENANT=<label> [BACKEND=azurerm] [RESOURCE="<type|provider> ..."])
	@test -n "$(TENANT)" || { echo "usage: make gen-env TENANT=<label> [BACKEND=azurerm] [RESOURCE=\"<type|provider> ...\"]"; exit 2; }
	$(INFRAWRIGHT_CLI) gen-env --tenant "$(TENANT)" --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" --terraform "$(TF)" $(if $(BACKEND),--backend "$(BACKEND)") $(foreach rt,$(RESOURCE),--resource "$(rt)")

transform: dist/infrawright-cli.mjs ## Transform pulled JSON for a tenant (IN=<dir> TENANT=<name> [RESOURCE="<type|provider> ..."])
	@test -n "$(IN)" -a -n "$(TENANT)" || { echo "usage: make transform IN=pulls/<tenant> TENANT=<tenant> [RESOURCE=\"<type|provider> ...\"]"; exit 2; }
	$(INFRAWRIGHT_CLI) transform --in "$(IN)" --tenant "$(TENANT)" --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" $(foreach rt,$(RESOURCE),--resource "$(rt)")

reconcile: dist/infrawright-cli.mjs ## Compare API JSON to Terraform schema (RESOURCE=<type> IN=<api.json> [SCHEMA=<schema.json>] [API_OPTIONS=<options.json>] [OPENAPI=<spec.json>] [OPENAPI_READ=<METHOD:/path>] [OPENAPI_WRITE="<METHOD:/path> ..."] [OVERRIDE=<override.json>] [OUT=<report.json>] [STRICT=1])
	@test -n "$(RESOURCE)" -a -n "$(IN)" || { echo "usage: make reconcile RESOURCE=<type> IN=<api.json> [SCHEMA=<schema.json>] [API_OPTIONS=<options.json>] [OPENAPI=<spec.json>] [OPENAPI_READ=<METHOD:/path>] [OPENAPI_WRITE=\"<METHOD:/path> ...\"] [OVERRIDE=<override.json>] [OUT=<report.json>] [STRICT=1]"; exit 2; }
	$(INFRAWRIGHT_CLI) reconcile "$(RESOURCE)" --api "$(IN)" $(if $(SCHEMA),--schema "$(SCHEMA)") $(if $(PROVIDER_SOURCE),--provider-source "$(PROVIDER_SOURCE)") $(if $(API_OPTIONS),--api-options "$(API_OPTIONS)") $(if $(OPENAPI),--openapi "$(OPENAPI)") $(if $(OPENAPI_READ),--openapi-read "$(OPENAPI_READ)") $(foreach op,$(OPENAPI_WRITE),--openapi-write "$(op)") $(if $(OVERRIDE),--override "$(OVERRIDE)") $(if $(OUT),--out "$(OUT)") $(if $(STRICT),--fail-on-unknown)

openapi-map: dist/infrawright-cli.mjs ## Map provider resources to OpenAPI CRUD endpoints (SCHEMA=<schema.json> OPENAPI=<spec.json> [PROVIDER_SOURCE=<addr>] [RESOURCE_PREFIX=<prefix>] [API_PREFIX=/api/] [REGISTRY=<registry.json>] [OUT=<report.json>])
	@test -n "$(SCHEMA)" -a -n "$(OPENAPI)" || { echo "usage: make openapi-map SCHEMA=<schema.json> OPENAPI=<spec.json> [PROVIDER_SOURCE=<addr>] [RESOURCE_PREFIX=<prefix>] [API_PREFIX=/api/] [REGISTRY=<registry.json>] [OUT=<report.json>]"; exit 2; }
	$(INFRAWRIGHT_CLI) openapi-map --schema "$(SCHEMA)" --openapi "$(OPENAPI)" $(if $(PROVIDER_SOURCE),--provider-source "$(PROVIDER_SOURCE)") $(if $(RESOURCE_PREFIX),--resource-prefix "$(RESOURCE_PREFIX)") $(if $(API_PREFIX),--api-prefix "$(API_PREFIX)") $(if $(REGISTRY),--registry "$(REGISTRY)") $(if $(OUT),--out "$(OUT)")

source-operation-map: dist/infrawright-cli.mjs ## Derive read registry from provider source OpenAPI operation calls (SCHEMA=<schema.json> OPENAPI=<spec.json> SOURCE_ROOT=<dir> [PROVIDER_SOURCE=<addr>] [RESOURCE_PREFIX=<prefix>] [RESOURCES=a,b] [SDK_ROOT=<dir>] [OUT=<registry.json>] [DIAGNOSTICS=<report.json>])
	@test -n "$(SCHEMA)" -a -n "$(OPENAPI)" -a -n "$(SOURCE_ROOT)" || { echo "usage: make source-operation-map SCHEMA=<schema.json> OPENAPI=<spec.json> SOURCE_ROOT=<dir> [PROVIDER_SOURCE=<addr>] [RESOURCE_PREFIX=<prefix>] [RESOURCES=a,b] [SDK_ROOT=<dir>] [OUT=<registry.json>] [DIAGNOSTICS=<report.json>]"; exit 2; }
	$(INFRAWRIGHT_CLI) source-operation-map --schema "$(SCHEMA)" --openapi "$(OPENAPI)" --source-root "$(SOURCE_ROOT)" $(if $(PROVIDER_SOURCE),--provider-source "$(PROVIDER_SOURCE)") $(if $(RESOURCE_PREFIX),--resource-prefix "$(RESOURCE_PREFIX)") $(if $(RESOURCES),--resources "$(RESOURCES)") $(if $(SDK_ROOT),--sdk-root "$(SDK_ROOT)") $(if $(OUT),--out "$(OUT)") $(if $(DIAGNOSTICS),--diagnostics "$(DIAGNOSTICS)")

source-evidence-eval: dist/infrawright-cli.mjs ## A/B evaluate text source scanning vs AST facts (SCHEMA=<schema.json> OPENAPI=<spec.json> SOURCE_ROOT=<dir> OUT_DIR=<dir> [SOURCE_FACTS=<facts.json>] [PROVIDER_SOURCE=<addr>] [RESOURCE_PREFIX=<prefix>] [RESOURCES=a,b] [FAIL_ON_REGRESSION=1])
	@test -n "$(SCHEMA)" -a -n "$(OPENAPI)" -a -n "$(SOURCE_ROOT)" -a -n "$(OUT_DIR)" || { echo "usage: make source-evidence-eval SCHEMA=<schema.json> OPENAPI=<spec.json> SOURCE_ROOT=<dir> OUT_DIR=<dir> [SOURCE_FACTS=<facts.json>] [PROVIDER_SOURCE=<addr>] [RESOURCE_PREFIX=<prefix>] [RESOURCES=a,b] [FAIL_ON_REGRESSION=1]"; exit 2; }
	$(INFRAWRIGHT_CLI) source-evidence-eval --schema "$(SCHEMA)" --openapi "$(OPENAPI)" --source-root "$(SOURCE_ROOT)" --out-dir "$(OUT_DIR)" $(if $(PROVIDER_SOURCE),--provider-source "$(PROVIDER_SOURCE)") $(if $(RESOURCE_PREFIX),--resource-prefix "$(RESOURCE_PREFIX)") $(if $(RESOURCES),--resources "$(RESOURCES)") $(if $(SOURCE_FACTS),--source-facts "$(SOURCE_FACTS)") $(if $(AST_TOOL_DIR),--ast-tool-dir "$(AST_TOOL_DIR)") $(if $(FAIL_ON_REGRESSION),--fail-on-regression)

adopt: dist/infrawright-cli.mjs ## Transform pulled JSON using Terraform/OpenTofu import oracle (IN=<dir> TENANT=<name> [RESOURCE="<type|provider> ..."] [POLICY=<file>])
	@test -n "$(IN)" -a -n "$(TENANT)" || { echo "usage: make adopt IN=pulls/<tenant> TENANT=<tenant> [RESOURCE=\"<type|provider> ...\"] [POLICY=<file>]"; exit 2; }
	$(INFRAWRIGHT_CLI) adopt --in "$(IN)" --tenant "$(TENANT)" --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" $(foreach rt,$(RESOURCE),--resource "$(rt)") $(if $(POLICY),--policy "$(POLICY)")

provider-probe: dist/infrawright-cli.mjs ## Run provider readiness probe (RECIPE=<recipe.json> [WORK_DIR=<dir>] [OUT=<summary.json>] [MARKDOWN=<summary.md>])
	@test -n "$(RECIPE)" || { echo "usage: make provider-probe RECIPE=<recipe.json> [WORK_DIR=<dir>] [OUT=<summary.json>] [MARKDOWN=<summary.md>]"; exit 2; }
	$(INFRAWRIGHT_CLI) provider-probe "$(RECIPE)" $(if $(WORK_DIR),--work-dir "$(WORK_DIR)") $(if $(OUT),--out "$(OUT)") $(if $(MARKDOWN),--markdown "$(MARKDOWN)")

roots: dist/infrawright-cli.mjs ## Emit root topology JSON ([TENANT=<label>] [RESOURCE=<type|provider>])
	@$(INFRAWRIGHT_CLI) roots $(OPTIONAL_TENANT_ARG) --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" $(foreach rt,$(RESOURCE),--resource "$(rt)")

scope-paths: dist/infrawright-cli.mjs ## Map changed paths to affected whole roots (PATHS_JSON=<file|->)
	@test -n "$(PATHS_JSON)" || { echo "usage: make scope-paths PATHS_JSON=<file|->"; exit 2; }
	@$(INFRAWRIGHT_CLI) scope-paths --paths-json "$(PATHS_JSON)" --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)"

plan-roots: dist/infrawright-cli.mjs ## Enumerate materialized env roots and plan artifacts ([TENANT=<label>] [RESOURCE=<type|provider>])
	@$(INFRAWRIGHT_CLI) plan-roots $(OPTIONAL_TENANT_ARG) --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" $(foreach rt,$(RESOURCE),--resource "$(rt)")

stage-imports: dist/infrawright-cli.mjs ## Copy import/moved blocks into env roots (TENANT=<label> [RESOURCE=<type|provider>] [STATE_AWARE=1] [BACKEND_CONFIG=<file>])
	@test -n "$(TENANT)" || { echo "usage: make stage-imports TENANT=<label> [RESOURCE=\"<type|provider> ...\"] [STATE_AWARE=1] [BACKEND_CONFIG=<file>]"; exit 2; }
	$(INFRAWRIGHT_CLI) stage-imports --tenant "$(TENANT)" --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" $(foreach rt,$(RESOURCE),--resource "$(rt)") $(if $(STATE_AWARE),--state-aware --terraform "$(TF)") $(if $(BACKEND_CONFIG),--backend-config "$(BACKEND_CONFIG)")

unstage-imports: dist/infrawright-cli.mjs ## Remove staged import/moved blocks from env roots (TENANT=<label> [RESOURCE=<type|provider>])
	@test -n "$(TENANT)" || { echo "usage: make unstage-imports TENANT=<label> [RESOURCE=\"<type|provider> ...\"]"; exit 2; }
	$(INFRAWRIGHT_CLI) unstage-imports --tenant "$(TENANT)" --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" $(foreach rt,$(RESOURCE),--resource "$(rt)")

plan: dist/infrawright-cli.mjs ## Terraform plan for tenant roots (TENANT=<label> [RESOURCE=<type|provider>] [IMPORTS_ONLY=1] [SAVE=1] [BACKEND_CONFIG=<file>])
	$(INFRAWRIGHT_CLI) plan --tenant "$(TENANT)" --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" --terraform "$(TF)" $(if $(IMPORTS_ONLY),--imports-only) $(if $(SAVE),--save) $(if $(BACKEND_CONFIG),--backend-config "$(BACKEND_CONFIG)") $(foreach rt,$(RESOURCE),--resource "$(rt)")

clean-plans: dist/infrawright-cli.mjs ## Delete saved tfplan artifacts ([TENANT=<label>] [RESOURCE=<type|provider>])
	$(INFRAWRIGHT_CLI) clean-plans $(OPTIONAL_TENANT_ARG) --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" $(foreach rt,$(RESOURCE),--resource "$(rt)")

assert-clean: dist/infrawright-cli.mjs ## Exit 0 only when every saved plan is no-op/import-only ([TENANT=<label>] [RESOURCE=<type|provider>] [BACKEND_CONFIG=<file>] [REPORT=<file>])
	@$(INFRAWRIGHT_CLI) assert-clean $(OPTIONAL_TENANT_ARG) --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" --terraform "$(TF)" $(if $(BACKEND_CONFIG),--backend-config "$(BACKEND_CONFIG)") $(if $(REPORT),--report "$(REPORT)") $(foreach rt,$(RESOURCE),--resource "$(rt)")

assert-adoptable: dist/infrawright-cli.mjs ## Classify saved plans with optional consumer drift policy ([TENANT=<label>] [RESOURCE=<type|provider>] [POLICY=<file>] [BACKEND_CONFIG=<file>] [REPORT=<file>])
	@$(INFRAWRIGHT_CLI) assert-adoptable $(OPTIONAL_TENANT_ARG) --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" --terraform "$(TF)" $(if $(POLICY),--policy "$(POLICY)") $(if $(BACKEND_CONFIG),--backend-config "$(BACKEND_CONFIG)") $(if $(REPORT),--report "$(REPORT)") $(foreach rt,$(RESOURCE),--resource "$(rt)")

apply: dist/infrawright-cli.mjs ## Apply saved plans ([TENANT=<label>] [RESOURCE=<type|provider>] [POLICY=<file>] [BACKEND_CONFIG=<file>] [ALLOW_DESTROY=1] [ALLOW_NON_MAIN=1] [ALLOW_PLAN_CHANGES=1])
	$(INFRAWRIGHT_CLI) apply $(OPTIONAL_TENANT_ARG) --profile "$(PACK_PROFILE)" --catalog "$(PACK_CATALOG)" --terraform "$(TF)" $(if $(POLICY),--policy "$(POLICY)") $(if $(BACKEND_CONFIG),--backend-config "$(BACKEND_CONFIG)") $(if $(ALLOW_DESTROY),--allow-destroy) $(if $(ALLOW_NON_MAIN),--allow-non-main) $(if $(ALLOW_PLAN_CHANGES),--allow-plan-changes) $(if $(MAIN_BRANCH),--main-branch "$(MAIN_BRANCH)") $(foreach rt,$(RESOURCE),--resource "$(rt)")
