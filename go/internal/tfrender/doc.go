// Package tfrender ports three Node artifact-rendering/writing sources for
// Wave 4 of the Go runtime port (docs/go-runtime-plan.md, Slice 3):
//
//   - node-src/domain/hcl-tfvars.ts: the byte-exact HCL tfvars renderer
//     (hcl_tfvars.go).
//   - node-src/json/canonical-import-blocks.ts: the closed, canonical
//     import{} block grammar parser (import_blocks.go). Note: this source
//     file has no node-tests/*.test.ts coverage as of this port (no test
//     file imports it) -- see import_blocks_test.go's doc comment for the
//     probe-derived vectors used in its place.
//   - node-src/domain/transform-artifacts.ts: artifact assembly and the
//     transactional filesystem write path (tfvars in json/hcl format,
//     imports files, lookup sidecars, generated-bindings sidecars, and
//     batch publish/rollback semantics) (transform_artifacts.go).
//
// # Local dependency ports (pending integration)
//
// transform-artifacts.ts imports several functions from
// node-src/domain/import-moves.ts (renderHclQuotedString, parseHclQuotedString
// [transitively, via parseGeneratedImports], renderGeneratedImports,
// parseGeneratedImports, deriveImportMoves, renderMovedBlocks) that are not
// among this slice's three named source files. node-src/domain/import-moves.ts
// as a whole belongs to the future internal/adopt package (docs/go-runtime-plan.md's
// "import staging/moves", Slice 6) and is not committed anywhere in this Go
// module yet. Rather than leave transform_artifacts.go uncompilable, or
// invent behavior, import_moves.go ports the exact subset of
// node-src/domain/import-moves.ts that transform-artifacts.ts actually calls
// (RenderHclQuotedString, ParseHclQuotedString, GeneratedImportPair,
// RenderGeneratedImports, ParseGeneratedImports, ImportMove,
// ImportMoveSuppression, ImportMoveDerivation, DeriveImportMoves,
// RenderMovedBlocks) as LOCAL, package-private-by-convention duplicates
// (exported only because transform_artifacts.go, in a different file of the
// same package, needs them -- no symbol here is meant to be a stable public
// API of this package). filterGeneratedImports/parseHclQuotedString's
// unused-by-transform-artifacts.ts helpers are deliberately not ported here.
// When internal/adopt lands, that package's port of import-moves.ts should
// become the single source of truth and this file's copy should be deleted
// in favor of importing it.
//
// deployment.ts itself, unlike import-moves.ts, is NOT locally forked here:
// go/internal/deployment (a full, tested port of node-src/domain/deployment.ts,
// including deploymentConfigDir, deploymentImportsDir, deploymentTfvarsFormat,
// and the Deployment/RootProviderConfig shapes from node-src/domain/types.ts)
// landed after this package's transform_artifacts.go was first drafted.
// transform_artifacts.go imports that package directly
// (deployment.Deployment, deployment.DeploymentConfigDir,
// deployment.DeploymentImportsDir, deployment.DeploymentTfvarsFormat,
// deployment.ReferenceBindingMode) rather than forking any part of
// deployment.ts a second time; an earlier, now-deleted local deployment.go
// in this package (a minimal Overlay/TfvarsFormat-only placeholder,
// predating go/internal/deployment) has been reconciled away in favor of
// the real package -- see this port's task report for the reconciliation
// note.
//
// PullTransformResult (in transform_artifacts.go) is similarly a LOCAL
// minimal port of the interface of the same name in
// node-src/domain/pull-transform.ts, whose full port belongs to the sibling
// finisher's go/internal/transform package for this wave. Only the three
// fields transform-artifacts.ts's write path reads or structurally carries
// (Items, Originals, Drops) are ported.
//
// # expression-bindings.ts is out of scope
//
// node-src/domain/transform-artifacts.ts does not import
// node-src/domain/expression-bindings.ts (confirmed by grep: that source's
// only importer anywhere in node-src/ is node-src/domain/environment-generator.ts).
// The "binding context" logic in transform_artifacts.go
// (BindingContext/TransformReferenceSpec/DeriveGeneratedBindings and their
// helpers) is a wholly self-contained port of logic that lives directly in
// transform-artifacts.ts itself, not a consumer of expression-bindings.ts.
// No subset of that other source is ported by this package.
//
// # Value model
//
// Every JSON-shaped value in this package (tfvars items, originals, lookup
// data) uses go/internal/canonjson's dynamic Value model directly: nil,
// bool, string, json.Number (a lossless source-text numeric lexeme, the Go
// analogue of the Node source's lossless-json LosslessNumber), float64 (a
// plain, natively-constructed number), []any, and map[string]any. This
// mirrors the Node source's own unknown-typed JSON tree walking and this
// port's Slice 0 "dynamic tree, not structs" design decision
// (docs/go-runtime-plan.md).
package tfrender
