# Managed-resource create captures

These are raw, unmodified `terraform show -json` outputs captured with
Terraform 1.15.4 using the built-in `terraform_data` managed resource. No
external provider, credentials, network service, or work configuration is used.
Unlike the data-source captures and the synthetic output-action fixture,
these plans contain actual managed-resource create actions with unknown IDs.

- `initial_create/show.json`: creates the first instance. The reference output
  has no planned `value`; `output_changes.after` contains an empty type map,
  and its nested `after_unknown` mask identifies the new instance.
- `mixed_create/show.json`: after applying the first instance to temporary
  local state, adds another instance. The existing ID remains known in
  `output_changes.after`; only the new instance is marked unknown. The planned
  output still omits `value` because the complete output is not yet known.

The module/resource addresses follow the engine's managed reference contract:
`module.terraform_data.terraform_data.this["existing"]` and `["new"]`.
The sensitive root output is `iw_reference_ids`, keyed by type and instance.
Its `module.<type>.items` projection matches `renderReferenceOutput` in
`go/internal/envgen/environment_generator.go`.

## Capture again

With Terraform 1.15.4 and Python 3 on PATH, run from the repository root:

```sh
python3 go/internal/plan/testdata/managed_create_capture/capture.py
```

The script copies the included HCL to a temporary directory, clears inherited
`TF_*` options, uses a temporary CLI configuration, and runs init, plan, a
local-only apply, and a second plan. The apply creates only `terraform_data`
state in that directory. Temporary state and binary plans are removed on exit;
only the two raw JSON captures are written here. UUIDs and timestamps change
on recapture, so any regenerated fixture diff still requires review.

Normal Go tests read the committed captures and do not execute Terraform.
These captures establish Terraform's managed-create JSON shape; they do not
claim to qualify Zscaler provider behavior or a production Apply.
