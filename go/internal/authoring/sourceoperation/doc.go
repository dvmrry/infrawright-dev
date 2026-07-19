// Package sourceoperation compiles the source-first v2 authoring
// artifact bundle. It intentionally owns no OpenAPI parsing: A2 emits only the
// ordinary absent-document state, while A3 may later supply a separately
// validated adapter result.
//
// A2 intentionally does not publish files. Its Bundle is sealed, fully
// compiled and validated in memory; the future command/orchestrator parcel
// owns all-or-nothing filesystem publication under a separately reviewed
// ownership and concurrency contract.
package sourceoperation
