# Go runtime port — foundational packages

Slice 0 of the Go runtime port (see `docs/go-runtime-plan.md`): the
`canonjson` byte-compatibility spike. Each package documents the
Node source file whose frozen semantics it implements. Go is now the product
authority; the frozen Node v1 bundle remains a differential oracle only for
surfaces that are independent of singleton-state topology v2.

Gate for this slice: decode → re-render of every committed canonical JSON
artifact (`catalogs/zscaler-root-catalog.v1.json`,
`catalogs/zscaler-root-catalog.v2.json`, `demo/config/demo/*.json`)
must be byte-identical, and all ported test vectors must pass.
