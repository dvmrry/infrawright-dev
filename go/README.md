# Go runtime port — foundational packages

Slice 0 of the Go runtime port (see `docs/go-runtime-plan.md`): the
`canonjson` byte-compatibility spike. Stdlib-only. Each package documents the
Node source file whose frozen semantics it implements; the Node
implementation remains the differential oracle until the port is qualified.

Gate for this slice: decode → re-render of every committed canonical JSON
artifact (`catalogs/zscaler-root-catalog.v1.json`, `demo/config/demo/*.json`)
must be byte-identical, and all ported test vectors must pass.
