# Provider-double capture fixtures

These seven `show.json` files are raw, unmodified `terraform show -json` output
from Terraform 1.15.4. They exercise the saved-plan reference-output contract
against the test-only provider in the sibling `provider-double` Go module.

The provider exposes one data source, `capture_item`, with a required string
`name` argument and a computed string `id` attribute. Its read response derives
a deterministic ID from `INFRAWRIGHT_CAPTURE_ID_VERSION` and the requested
name. The scenario modules instantiate the data source with `for_each` and
publish the sensitive `iw_reference_ids` output.

## Regenerate

From this directory, export the repository-local Go build directories and run:

```sh
export GOCACHE="$PWD/../../../../../.gocache"
export GOTMPDIR="$PWD/../../../../../.gotmp"
./gen-captures.sh
```

The script builds the nested provider module into the local, un-staged
`<worktree>/.provider-double-bin/` directory, verifies that `terraform version`
is exactly `Terraform v1.15.4`, creates a temporary Terraform
CLI development override and scenario workspace, and writes only the seven
`show.json` results back here. Terraform/provider logs and temporary state stay
under the temporary workspace and are removed on exit.

The provider module remains a separate Go module at
`../provider-double/`; do not move its `go.mod` into the engine module.

## Scenarios

| Scenario | Contract shape |
| --- | --- |
| `initial_create` | Nonempty refreshed data instances and output creation. |
| `refresh_id_change` | The provider-observed ID changes from the applied v1 value to a v2 value during plan. |
| `rekey_refusal` | The output key projection changes while data IDs stay stable; this is a committed negative and must be refused because the output key-to-ID binding changed. |
| `no_op` | The output action is `no-op`; the existing output needs no new authorization. |
| `empty_for_each` | The refreshed data instance set and created output map are both empty. |
| `stale_refresh_false` | An applied v1 state is planned under provider v2 with `-refresh=false`; the stale data evidence must be refused. |
| `fresh_after_stale` | The same applied v1 state is first planned stale, then replanned under provider v2 with `-refresh=true`; the final v2 evidence is accepted. |

The captures document the Terraform 1.15.4 container rule used by the engine:
data reads during plan appear in `prior_state.values.root_module`, not in
`planned_values.root_module`; the refreshed prior-state IDs are the
provider-observed evidence for a changed `iw_reference_ids` output. Tests read
each JSON file directly without rewriting or transplanting any field.
