#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
go_root=$(dirname -- "$script_dir")
vendor_check_dir=$(mktemp -d)
trap 'rm -rf "$vendor_check_dir"' EXIT HUP INT TERM

cd "$go_root"
go mod vendor -o "$vendor_check_dir/vendor"
diff -qr vendor "$vendor_check_dir/vendor"
