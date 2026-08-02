#!/bin/sh
# Regenerates the provider-double capture fixtures. Run manually after
# changing the provider double or the scenario configurations, with
# Terraform 1.15.4 on PATH. The Go test suite is the gate of record for
# these fixtures: if a regeneration is wrong, the focused plan-contract
# regressions fail.
set -eu

for terraform_args_variable in $(env | sed -n 's/^\(TF_CLI_ARGS[A-Za-z0-9_]*\)=.*/\1/p'); do
	printf '%s\n' "capture regeneration refuses inherited $terraform_args_variable" >&2
	exit 1
done

export TZ=UTC LANG=C LC_ALL=C
unset TF_CLI_CONFIG_FILE TF_WORKSPACE TF_DATA_DIR TF_LOG TF_LOG_PATH
for terraform_variable in $(env | sed -n 's/^\(TF_VAR_[A-Za-z0-9_]*\)=.*/\1/p'); do
	unset "$terraform_variable"
done

capture_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
go_root=$(CDPATH= cd -- "$capture_dir/../../../.." && pwd)
repo_root=$(CDPATH= cd -- "$go_root/.." && pwd)
provider_dir="$go_root/internal/plan/testdata/provider-double"
provider_bin_dir="$repo_root/.provider-double-bin"
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM

terraform_version=$(terraform version | sed -n '1p')
if [ "$terraform_version" != "Terraform v1.15.4" ]; then
	printf '%s\n' "capture regeneration requires Terraform v1.15.4, got: $terraform_version" >&2
	exit 1
fi

mkdir -p "$provider_bin_dir"
(cd "$provider_dir" && go build -o "$provider_bin_dir/terraform-provider-capture" .)
sed "s|/PROVIDER_DOUBLE_BIN_DIR|$provider_bin_dir|" \
	"$capture_dir/dev_overrides.tfrc" > "$work_dir/dev.tfrc"
export TF_CLI_CONFIG_FILE="$work_dir/dev.tfrc"
export TF_IN_AUTOMATION=1 TF_INPUT=0 CHECKPOINT_DISABLE=1
export INFRAWRIGHT_CAPTURE_ID_VERSION=v1

prepare() {
	rm -rf "$work_dir/$1"
	cp -R "$capture_dir/$1" "$work_dir/$1"
	cd "$work_dir/$1"
	terraform init -backend=false -input=false -no-color > init.log 2>&1
}

prepare initial_create
terraform plan -input=false -no-color -refresh=true -out=p.tfplan > p.log 2>&1
terraform show -json p.tfplan > "$capture_dir/initial_create/show.json"

prepare no_op
terraform apply -auto-approve -input=false -no-color > a.log 2>&1
terraform plan -input=false -no-color -refresh=true -out=p.tfplan > p.log 2>&1
terraform show -json p.tfplan > "$capture_dir/no_op/show.json"

prepare refresh_id_change
terraform apply -auto-approve -input=false -no-color > a.log 2>&1
INFRAWRIGHT_CAPTURE_ID_VERSION=v2 terraform plan -input=false -no-color -refresh=true -out=p.tfplan > p.log 2>&1
INFRAWRIGHT_CAPTURE_ID_VERSION=v2 terraform show -json p.tfplan > "$capture_dir/refresh_id_change/show.json"

prepare rekey_refusal
terraform apply -auto-approve -input=false -no-color > a.log 2>&1
terraform plan -input=false -no-color -refresh=true -var output_prefix=changed- -out=p.tfplan > p.log 2>&1
terraform show -json p.tfplan > "$capture_dir/rekey_refusal/show.json"

prepare empty_for_each
terraform plan -input=false -no-color -refresh=true -out=p.tfplan > p.log 2>&1
terraform show -json p.tfplan > "$capture_dir/empty_for_each/show.json"

prepare refresh_false
terraform apply -auto-approve -input=false -no-color > a.log 2>&1
INFRAWRIGHT_CAPTURE_ID_VERSION=v2 terraform plan -input=false -no-color -refresh=false -out=p.tfplan > p.log 2>&1
INFRAWRIGHT_CAPTURE_ID_VERSION=v2 terraform show -json p.tfplan > "$capture_dir/refresh_false/show.json"

prepare refresh_true
terraform apply -auto-approve -input=false -no-color > a.log 2>&1
INFRAWRIGHT_CAPTURE_ID_VERSION=v2 terraform plan -input=false -no-color -refresh=true -out=p.tfplan > p.log 2>&1
terraform show -json p.tfplan > "$capture_dir/refresh_true/show.json"

for scenario in initial_create no_op refresh_id_change rekey_refusal empty_for_each refresh_false refresh_true; do
	python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); sys.exit(0 if d.get("terraform_version")=="1.15.4" else 1)' \
		"$capture_dir/$scenario/show.json" || {
		printf '%s\n' "capture $scenario is not valid 1.15.4 show JSON" >&2
		exit 1
	}
done
printf '%s\n' 'ALL-CAPTURED'
