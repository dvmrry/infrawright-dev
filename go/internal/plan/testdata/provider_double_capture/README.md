# Provider-double capture fixtures

These seven `show.json` files are raw, unmodified `terraform show -json` output
from Terraform 1.15.4. They exercise the saved-plan reference-output contract
against the test-only provider in the sibling `provider-double` Go module.

The provider exposes one data source, `capture_item`, with a required string
`name` argument and a computed string `id` attribute. Its read response returns
the requested `name` and derives a deterministic ID from
`INFRAWRIGHT_CAPTURE_ID_VERSION` and that name. The generated data-module
postcondition checks that the returned name is an exact, case-sensitive match
for the requested name. The scenario modules instantiate the data source with
`for_each` and publish the sensitive `iw_reference_ids` output.

## Regenerate

From this directory, export the repository-local Go build directories and run:

```sh
export GOCACHE="$PWD/../../../../../.gocache"
export GOTMPDIR="$PWD/../../../../../.gotmp"
./gen-captures.sh
```

The script builds the nested provider module into the local, un-staged
`<worktree>/.provider-double-bin/` directory, verifies that `terraform version`
is exactly `Terraform v1.15.4`, creates a temporary Terraform CLI development
override and scenario workspace, and captures every `show.json` into a staging
directory. It validates that the complete staged set is non-empty, then moves
the seven results into place only after every scenario succeeds; a promotion
failure restores the prior set. Terraform/provider logs, temporary state, and
the staging directory stay under the temporary workspace and are removed on
exit. The environment is normalized to `TZ=UTC LANG=C`; regeneration refuses
to run if `TF_CLI_ARGS` or `TF_CLI_ARGS_plan` is already set and clears
caller-provided Terraform variable/workspace/data-directory inputs.

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
| `refresh_false` | An applied v1 state is planned under provider v2 with `-refresh=false`; Terraform 1.15.4 still reads the known-input data source, so the genuinely provider-observed v2 evidence is accepted. |
| `refresh_true` | The same applied v1 state is first planned with `-refresh=false`, then replanned with `-refresh=true`; the final v2 evidence is accepted. Its semantic output is identical to `refresh_false` apart from the timestamp. |

The two refresh variants are the refresh-flag-independence proof, not a stale
data threat model. Terraform 1.15.4 reads known-input data sources during both
plans, so the byte-identical semantic captures (modulo `timestamp`) are the
empirical basis for accepting the `-refresh=false` evidence. Both captures
still document the Terraform 1.15.4 container rule used by the engine: data
reads during plan appear in `prior_state.values.root_module`, not in
`planned_values.root_module`; the refreshed prior-state IDs are the
provider-observed evidence for a changed `iw_reference_ids` output. Tests read
each JSON file directly without rewriting or transplanting any field.

The attestation validator accepts exactly Terraform `1.15.4` output (the
only capture-qualified release; widening requires re-running this matrix) and
records either refresh flag. The committed capture matrix qualifies only
Terraform 1.15.4; widening that version range requires re-running the full
capture matrix under the new version before the evidence is promoted.
