#!/bin/sh
set -eu

# Refuse and strip EVERY TF_CLI_ARGS variant: Terraform applies
# TF_CLI_ARGS_<subcommand> per command, and this script runs apply, plan,
# init, and show — an inherited TF_CLI_ARGS_apply could alter the state the
# captures are built from.
for terraform_args_variable in $(env | sed -n 's/^\(TF_CLI_ARGS[A-Za-z0-9_]*\)=.*/\1/p'); do
	printf '%s\n' "capture regeneration refuses inherited $terraform_args_variable" >&2
	exit 1
done

export TZ=UTC
export LANG=C
export LC_ALL=C
unset TF_CLI_CONFIG_FILE TF_WORKSPACE TF_DATA_DIR TF_LOG TF_LOG_PATH
for terraform_variable in $(env | sed -n 's/^\(TF_VAR_[A-Za-z0-9_]*\)=.*/\1/p'); do
	unset "$terraform_variable"
done

capture_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
go_root=$(CDPATH= cd -- "$capture_dir/../../../.." && pwd)
repo_root=$(CDPATH= cd -- "$go_root/.." && pwd)
provider_dir="$go_root/internal/plan/testdata/provider-double"
provider_bin_dir="$repo_root/.provider-double-bin"
provider_bin="$provider_bin_dir/terraform-provider-capture"
work_dir=$(mktemp -d "$capture_dir/.capture-run.XXXXXX")
stage_dir="$work_dir/captures"
scenarios='initial_create no_op refresh_id_change rekey_refusal empty_for_each refresh_false refresh_true'

cleanup() {
	status=$?
	rm -rf "$work_dir"
	if [ -n "${backup_dir:-}" ] && [ -d "${backup_dir:-/nonexistent}" ] && [ "$status" -ne 0 ]; then
		printf '%s\n' "capture promotion may be incomplete; previous fixtures preserved in $backup_dir (see TRANSACTION for state)" >&2
	fi
}
trap cleanup EXIT HUP INT TERM

mkdir -p "$provider_bin_dir" "$stage_dir"
(cd "$provider_dir" && go build -o "$provider_bin" .)

terraform_version=$(terraform version | sed -n '1p')
if [ "$terraform_version" != "Terraform v1.15.4" ]; then
	printf '%s\n' "capture regeneration requires Terraform v1.15.4, got: $terraform_version" >&2
	exit 1
fi

sed "s|/PROVIDER_DOUBLE_BIN_DIR|$provider_bin_dir|" \
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
	mkdir -p "$stage_dir/$scenario"
	cd "$work_dir/$scenario"
	terraform init -backend=false -input=false -no-color > init.log 2>&1
}

capture_show() {
	scenario=$1
	plan=$2
	terraform show -json "$plan" > "$stage_dir/$scenario/show.json"
}

prepare initial_create
terraform plan -input=false -no-color -refresh=true -out=p.tfplan > p.log 2>&1
capture_show initial_create p.tfplan

prepare no_op
terraform apply -auto-approve -input=false -no-color > a.log 2>&1
terraform plan -input=false -no-color -refresh=true -out=p.tfplan > p.log 2>&1
capture_show no_op p.tfplan

prepare refresh_id_change
terraform apply -auto-approve -input=false -no-color > a.log 2>&1
INFRAWRIGHT_CAPTURE_ID_VERSION=v2 terraform plan -input=false -no-color -refresh=true -out=p.tfplan > p.log 2>&1
capture_show refresh_id_change p.tfplan

prepare rekey_refusal
terraform apply -auto-approve -input=false -no-color > a.log 2>&1
terraform plan -input=false -no-color -refresh=true -var output_prefix=changed- -out=p.tfplan > p.log 2>&1
capture_show rekey_refusal p.tfplan

prepare empty_for_each
terraform plan -input=false -no-color -refresh=true -out=p.tfplan > p.log 2>&1
capture_show empty_for_each p.tfplan

prepare refresh_false
terraform apply -auto-approve -input=false -no-color > a.log 2>&1
INFRAWRIGHT_CAPTURE_ID_VERSION=v2 terraform plan -input=false -no-color -refresh=false -out=p.tfplan > p.log 2>&1
capture_show refresh_false p.tfplan

prepare refresh_true
terraform apply -auto-approve -input=false -no-color > a.log 2>&1
INFRAWRIGHT_CAPTURE_ID_VERSION=v2 terraform plan -input=false -no-color -refresh=false -out=refresh-false.tfplan > refresh-false.log 2>&1
INFRAWRIGHT_CAPTURE_ID_VERSION=v2 terraform plan -input=false -no-color -refresh=true -out=p.tfplan > p.log 2>&1
capture_show refresh_true p.tfplan

# Validate the COMPLETE staged set semantically before any tracked fixture
# changes: JSON-parseable, complete, not errored, the qualified Terraform
# version, and refresh-pair byte identity modulo the single timestamp.
for scenario in $scenarios; do
	if [ ! -s "$stage_dir/$scenario/show.json" ]; then
		printf '%s\n' "capture regeneration produced an empty $scenario/show.json" >&2
		exit 1
	fi
	python3 - "$stage_dir/$scenario/show.json" <<'PYEOF'
import json, sys
d = json.load(open(sys.argv[1]))
assert d.get("complete") is True, "capture is not complete"
assert d.get("errored") is False, "capture errored"
assert d.get("terraform_version") == "1.15.4", "capture terraform_version is not 1.15.4"
PYEOF
done
python3 - "$stage_dir/refresh_false/show.json" "$stage_dir/refresh_true/show.json" <<'PYEOF'
import json, re, sys
def norm(p):
	raw = open(p).read()
	matches = re.findall(r'"timestamp":"[^"]*"', raw)
	assert len(matches) == 1, "expected exactly one timestamp"
	return re.sub(r'"timestamp":"[^"]*"', '"timestamp":"<t>"', raw)
assert norm(sys.argv[1]) == norm(sys.argv[2]), "refresh pair differs beyond timestamp"
PYEOF

# The backup set and transaction record live OUTSIDE the trap-deleted work
# directory so an interruption mid-promotion leaves deterministic recovery
# state instead of a mixed fixture set with no record.
backup_dir="$capture_dir/.capture-backup.$$"
mkdir -p "$backup_dir"
for scenario in $scenarios; do
	target="$capture_dir/$scenario/show.json"
	backup="$backup_dir/$scenario/show.json"
	missing="$backup_dir/$scenario/missing"
	mkdir -p "$backup_dir/$scenario"
	if [ -e "$target" ]; then
		cp -p "$target" "$backup"
	else
		touch "$missing"
	fi
done

printf 'state=promoting\nscenarios=%s\n' "$scenarios" > "$backup_dir/TRANSACTION"
promote_failed=0
for scenario in $scenarios; do
	if ! mv "$stage_dir/$scenario/show.json" "$capture_dir/$scenario/show.json"; then
		promote_failed=1
		break
	fi
	printf 'promoted=%s\n' "$scenario" >> "$backup_dir/TRANSACTION"
done

if [ "$promote_failed" -ne 0 ]; then
	printf '%s\n' 'capture promotion failed; restoring the previous complete fixture set' >&2
	for scenario in $scenarios; do
		target="$capture_dir/$scenario/show.json"
		backup="$backup_dir/$scenario/show.json"
		missing="$backup_dir/$scenario/missing"
		if [ -e "$backup" ]; then
			cp -p "$backup" "$target"
		elif [ -e "$missing" ]; then
			rm -f "$target"
		fi
	done
	exit 1
fi

rm -rf "$backup_dir"
printf '%s\n' 'ALL-CAPTURED'
