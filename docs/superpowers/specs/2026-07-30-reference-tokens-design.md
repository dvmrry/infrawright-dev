# Reference tokens: commit reference fields as keys, not tenant IDs — design

> Status: READY FOR PLANNING — value-pipeline trace baked in, type boundary
> measured against real Terraform, §5 comparison surfaces enumerated with
> verdicts, downstream review (issue #296) incorporated.

## Invariant (hard) and mechanism (open)

**Hard:** committed config never contains a raw tenant ID for a declared
reference field whose referent's key is known. IDs live in exactly two
places: tfstate, and the lookup sidecar the engine keeps for itself.

**Open (resolved by this spec, not by fiat):** the token spelling. Bare
stable key (`"dns"`) is a nice-to-have, not a requirement; a qualified
`type.key` form (`"zia_firewall_filtering_network_service.dns"`) is equally
acceptable to the requester.

Everything else — probe, fallback, notes — is settled by the
2026-07-30 backend-probe design and unchanged: when the referent's state is
absent, gen-env substitutes the ID from the sidecar at generation time; the
committed shape never changes for fallback reasons again.

**Tenant scoping (explicit, per the requester):** all reference resolution
is intra-tenant. A token resolves against the applying tenant's own state
(`<tenant>/<label>.tfstate`) and that tenant's sidecar — nothing ever
crosses tenants, and no cross-tenant resolution may be built. "Portability
across zs2/zs3" means *promotion*: the same token-shaped config can move
between tenant config directories and resolve within whichever tenant
applies it — **provided the referent derives the same stable key in both
tenants, which the engine does not and cannot guarantee** (keys are
name-derived; alignment across tenants is operator governance). What the
engine guarantees is the failure mode: a promoted token with no matching
key in the target tenant fails loudly at derivation or plan time with the
key named — never a silent wrong ID. Raw IDs made promotion meaningless
everywhere; tokens make it possible exactly where naming is aligned.

## Why this is the root fix

Adopt projects provider readback verbatim, so a declared reference field
lands in `.auto.tfvars.json` as the raw ID; the stable key exists only in
the expression overlay and the sidecar. Consequences today: committed config
is unreviewable by humans, not portable across tenants (zs2/zs3), and the
"no direct ID references" goal holds only in *emitted roots*, never in the
committed artifact. Tokenising at the source fixes all three and makes the
expression overlay derivable (a token carries exactly what the binding
expression needs).

## Verified value pipeline (line numbers at worktree HEAD)

Two producers converge on one artifact writer:

- **transform**: pulled JSON → `transform.TransformLoadedItems` →
  `executeTransform` — the raw ID enters the item at the references
  unwrap (`transform/overrides.go:214-229` → `coerce.go:182`), the item key
  derives at `overrides.go:65`, and the projected items flow to
  `tfrender.WriteTransformArtifacts` (`transformrun/runner.go:637`).
  Referent-first Kahn ordering (`transform/selection.go:151`) guarantees a
  referent's lookup sidecar is published before any referrer compiles.
- **adopt**: provider state → `ProjectProviderState`
  (`adopt/state_project.go:854`) — adopt **bypasses the projection kernel
  entirely**; its reference values are the raw IDs Terraform state reports.
  Converges at the same `WriteTransformArtifacts` (`adopt/runner.go:458`).

Inside `tfrender.CompileTransformArtifacts:1528`:

1. `:1533` lookup sidecar compiles (`key_by_id[id]=key`, from
   originals+projected).
2. `:1542` imports/moves render **from `Originals`**, never `Items`.
3. `:1579` config bytes freeze (`renderDeploymentTfvars`).
4. `:1588` `key_by_id` maps load; `:1592` `DeriveGeneratedBindings` derives
   the expression overlay **from the same live item objects** the config
   renderer read (`recordFromItems:1441` is a shallow re-wrap).

## Design

### Insertion point: P1 — `CompileTransformArtifacts`, before config render

Hoist the `lookupKeyMaps` load above `renderDeploymentTfvars` and rewrite
`Items` in place: any declared reference field whose value hits
`key_by_id` becomes a token. This is the only point that satisfies all of:

- covers **both** producers (transform and adopt funnel through it);
- runs **after** the sidecar compiles, so `key_by_id` keeps mapping real
  IDs — the decoder survives tokenisation;
- leaves imports/moves untouched by construction (they read `Originals`);
- lets `DeriveGeneratedBindings` consume tokens directly: the ID→key hop in
  `resolve` (`transform_artifacts.go:738-762`) collapses — the expression
  only ever needed the key, so a token is strictly closer to the output
  than an ID was. The binding becomes a pure function of the committed
  value.

Rejected alternatives: the projection kernel (adopt bypasses it — the two
commands would disagree); `renderDeploymentTfvars` alone (config and
bindings would spell the same fact two ways); the HCL comments layer
(HCL-only, top-level-fields-only).

### Token spelling: qualified `"<referent_type>.<key>"`

Both spellings were live options; the trace resolves the choice on
correctness grounds, not taste:

- **Self-describing where it matters most.** The JSON tfvars branch has no
  comments at all — today reference values there have zero human-readable
  decoration. A qualified token is its own documentation, in the form
  (`type.name`) any Terraform reader already parses by eye.
- **Validatable.** `DeriveGeneratedBindings` can check the token's prefix
  against `spec.Referent` and emit a precise diagnostic; a bare key that
  misses the map is indistinguishable from a tenant literal (today's blunt
  `id_absent` counter).
- **The dotted-key hazard dissolves.** Item keys may themselves contain
  dots, so a *general* split rule would be a real parsing decision — but no
  consumer ever needs one: the field's referent type is always declared by
  the pack edge, so reading a token is exactly "strip the known
  `<referent>.` prefix"; anything else is a loud mismatch.
- **Sentinel-safe.** `ANY`/`NONE`-class system constants
  (`systemConstant:1337`) and `0`-sentinels remain untokenised and cannot
  collide with a dotted, type-prefixed namespace.

Rules: tokens must pass the existing interpolation guard (`${`/`%{`);
substitution applies only to values present in `key_by_id` (unknown IDs and
sentinels stay literal, exactly today's mixing rule, applied one layer
earlier); self-references stay untouched (existing rule).

### Type boundary — a design constraint, not a validation line-item

Measured downstream (2026-07-30) against real Terraform: generated modules
declare reference attributes straight from the provider schema
(`id = optional(set(number))` for ZIA), so a token in committed tfvars
fails **at variable decoding** — before plan, before any engine validator:

```
Error: Invalid value for input variable
number is required.
```

Tokens therefore cannot be allowed to reach a schema-typed variable, which
shapes the mechanism in three parts — **and parts 1 and 3 are the already-
shipped architecture, verified and measured (2026-07-30, Terraform
v1.15.4):**

1. **The root variable already admits tokens.** gen-env emits every root
   items variable as `type = any` with the comment "opaque at the root;
   the module enforces the strict type"
   (`environment_generator.go:467-470`). Measured end-to-end: a JSON
   tfvars carrying `"zia_firewall_filtering_network_service.dns"` decodes
   through the `any` root, the expression local rewrites the leaf, and the
   provider-strict module (`optional(set(number))`) plans clean. **JSON
   tfvars are confirmed viable; no HCL migration is needed, and none would
   help — tfvars are values-only in both syntaxes and both decode against
   the same variable types.** The downstream failure measurement was the
   module-direct shape, which is the guard working as designed.
2. **gen-env's expression local becomes total over tokenised leaves —
   this is the engine work.** Today the
   `infrawright_expression_bound_items` local exists only where bindings
   exist (`environment_generator.go:458`: roots without bindings pass
   `var.<name>` straight to the module); with tokens, every tokenised
   leaf must be rewritten before the module boundary — to the
   state-lookup expression when the referent's state is usable, to the
   sidecar ID on fallback. An unresolvable token (no expression, no
   sidecar entry) aborts generation loudly with the token named.
3. **Module variables keep provider-strict types, deliberately.** The
   module boundary is the final guard, and it is loud: measured — an
   unresolved token reaching the module fails plan with
   `Invalid value for input variable` naming the exact module argument
   line.

Downstream also independently corroborated the premise (an outside reviewer
of an open PR flagged the hard-coded ID list as unreviewable) and declined a
bespoke-output-per-consumer alternative for the right reason: referents
cannot know their consumers, and the canonical nested
`infrawright_reference_ids` output stays the one lookup surface.

### New couplings this creates (named, not hidden)

- **Referent rename now also touches referrers.** Renames were always
  churn (the referent's moves blocks and HCL display comments already
  rewrite); tokens widen the same churn class to referrer tfvars, which
  under raw IDs stayed byte-identical through a rename. Accepted as-is by
  the requester — same class, wider and more honest blast radius; release
  notes mention it, nothing guards against it.
- **The sidecar becomes load-bearing for committed content.**
  `RemoveLookupWhenAbsent` can today delete a referent's sidecar; after
  tokenisation that would strand committed tokens with no decoder. Removal
  must refuse (or the book must be retained) while any committed artifact
  in the tree holds tokens for that referent.
- **Conditional tokenisation.** A referent outside the active profile has
  no sidecar, so its fields stay IDs — the invariant is "when the key is
  known", and the same field can be an ID in one tree and a token in
  another. Diagnostics must make the untokenised case visible, not silent.

### Idempotency

Transform and adopt regenerate config purely from pulls/state plus the
three committed inputs (imports, moves, lookup) — they never read committed
tfvars. Tokens therefore re-derive identically iff `deriveKey` is stable,
which is the same stability the whole adoption scheme already rests on.
Adopt re-derives from provider state each run, so the shared insertion
point is also what prevents transform/adopt flapping.

## §5 Comparison surfaces — enumerated, with verdicts

The committed tfvars' *values* turn out to be read by remarkably little of
the engine — verified by enumerating every `.auto.tfvars*` reader:

| surface | evidence | verdict |
|---|---|---|
| envgen binding validation (`validateBindingsAgainstConfig`, `environment_generator.go:596-619`) | JSON-only; extracts items and runs `ApplyExpressionBindings` — **existence-based** leaf checks, no type checks | indifferent; re-exercised anyway by the total-local work |
| assessment (`assessment/inputs.go:191-193`) | selects tfvars *paths* as `-var-file` inputs; never inspects values | indifferent |
| plan fingerprints (`plan/fingerprint_files.go:129-130`) | content hashing by suffix; tokens change bytes → fingerprints change, which is correct behaviour for content hashes | indifferent |
| configcheck (`configcheck/fetchable.go:75`) | extension enumeration only | indifferent |
| adopt oracle (`oracle_transaction.go`) | stages only `main.tf` + `imports.tf`; never reads tfvars; import IDs come from `Originals` | unaffected by construction |
| assert-adoptable / assert-clean / drift / set-membership change kind | classify **plan JSON**, where reference leaves arrive already resolved (expression results or fallback IDs — numbers, byte-identical planned values to today) | indifferent |
| transform-adopt parity (`commands_authoring_core.go:470`) | both sides produce artifacts through the shared compiler post-substitution → tokens appear identically | preserved by construction; parity **fixtures regenerate** in the PR |
| transform kernel coercion (`coerceItem`/`filterItem`) | run before the insertion point; never see tokens | indifferent |
| modulesgen sample tfvars (`generator.go:50`) | schema-derived samples, not tenant data | indifferent (confirm in PR tests) |
| HCL display comments (`deriveHclComments`/`displayFor`) | receives the field value as its lookup key — a token misses the `by_id` map and renders `<unknown>` | **must-change**: resolve token → key → display via the sidecar (or drop the referent-name half, now redundant) |
| Terraform variable decoding | mechanism section above; measured | resolved: `any` root (shipped) + total local (the work) + strict module guard (shipped) |

Net implementation surface: (1) the P1 substitution in
`CompileTransformArtifacts` (incl. set-block fields, list mixing,
sentinels); (2) `DeriveGeneratedBindings.resolve` consuming tokens
directly with prefix validation; (3) envgen total expression local +
sidecar-ID fallback rewrite + loud unresolvable-token abort; (4) the
sidecar-removal guard; (5) `deriveHclComments` token-aware display; (6)
fixture regeneration (parity, goldens), CHANGELOG, docs.

## Migration

One-time re-transform/re-adopt regenerates every downstream tfvars with
tokens — reviewable churn; `assert-adoptable` must stay green through it.
No compat window inside the engine: old-shape (ID) values remain valid
inputs forever by the conditional-tokenisation rule; they are simply what
the next transform/adopt run rewrites.

## Out of scope

- Referents with no Terraform resource (users, groups, departments): no
  edge, no token — raw IDs remain, and portability for those fields is
  explicitly not solved here. A possible later lane is provider
  data-source expressions (the binding grammar already admits `data.*`),
  at the cost of a tenant-API dependency at plan time.
- Folding `.generated.expressions.json` out of the committed surface and
  relocating `lookup.json` — falls out naturally after this lands and is
  specified separately.
