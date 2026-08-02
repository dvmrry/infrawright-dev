#!/bin/sh
set -eu

capture_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
go_root=$(CDPATH= cd -- "$capture_dir/../../../.." && pwd)
repo_root=$(CDPATH= cd -- "$go_root/.." && pwd)
provider_dir="$go_root/internal/plan/testdata/provider-double"
provider_bin_dir="$repo_root/.provider-double-bin"
provider_bin="$provider_bin_dir/terraform-provider-capture"
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/infrawright-provider-double.XXXXXX")

cleanup() {
	rm -rf "$work_dir"
}
trap cleanup EXIT HUP INT TERM

mkdir -p "$provider_bin_dir"
(cd "$provider_dir" && go build -o "$provider_bin" .)

sed "s|/PROVIDER_DOUBLE_BIN|$provider_bin|" \
	"$capture_dir/dev_overrides.tfrc" > "$work_dir/dev.tfrc"
export TF_CLI_CONFIG_FILE="$work_dir/dev.tfrc"
export TF_IN_AUTOMATION=1
export TF_INPUT=0
export CHECKPOINT_DISABLE=1
export INFRAWRIGHT_CAPTURE_ID_VERSION=v1

prepare() {
	scenario=$1
	rm -rf "$work_dir/$scenario"
	cp -R "$capture_dir/$scenario" "$work_dir/$scenario"
	cd "$work_dir/$scenario"
	terraform init -backend=false -input=false -no-color > init.log 2>&1
}

prepare initial_create
terraform plan -input=false -no-color -out=p.tfplan > p.log 2>&1
terraform show -json p.tfplan > "$capture_dir/initial_create/show.json"

prepare no_op
terraform apply -auto-approve -input=false -no-color > a.log 2>&1
terraform plan -input=false -no-color -out=p.tfplan > p.log 2>&1
terraform show -json p.tfplan > "$capture_dir/no_op/show.json"

prepare refresh_id_change
terraform apply -auto-approve -input=false -no-color > a.log 2>&1
INFRAWRIGHT_CAPTURE_ID_VERSION=v2 terraform plan -input=false -no-color -out=p.tfplan > p.log 2>&1
INFRAWRIGHT_CAPTURE_ID_VERSION=v2 terraform show -json p.tfplan > "$capture_dir/refresh_id_change/show.json"

prepare output_only_change
terraform apply -auto-approve -input=false -no-color > a.log 2>&1
terraform plan -input=false -no-color -var output_prefix=changed- -out=p.tfplan > p.log 2>&1
terraform show -json p.tfplan > "$capture_dir/output_only_change/show.json"

prepare empty_for_each
terraform plan -input=false -no-color -out=p.tfplan > p.log 2>&1
terraform show -json p.tfplan > "$capture_dir/empty_for_each/show.json"

printf '%s\n' 'ALL-CAPTURED'
