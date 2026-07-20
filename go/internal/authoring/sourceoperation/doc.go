// Package sourceoperation compiles the source-first v2 authoring
// artifact bundle. It intentionally owns no OpenAPI parsing: qualified
// compilation accepts only a separately sealed adapter result and validates
// its detached diagnostics against the exact source report before bundling.
//
// A2 intentionally does not publish files. Its Bundle is sealed, fully
// compiled and validated in memory; the future command/orchestrator parcel
// owns all-or-nothing filesystem publication under a separately reviewed
// ownership and concurrency contract.
package sourceoperation
