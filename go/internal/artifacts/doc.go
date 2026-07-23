// Package artifacts provides bounded, stable filesystem reads and private
// saved-plan snapshots. It ports the security-sensitive contract in
// node-src/io/bounded-files.ts.
//
// The Go port deliberately fails closed outside the documented Linux/macOS
// amd64/arm64 release targets, including Android, iOS, and 32-bit aliases, even
// though the Node helper itself has no platform gate. All seven production
// source consumers are reached only through CLI commands whose
// supported-platform check runs before dispatch; Node's predicate rejects
// Windows, while the product contract documents Linux for production and macOS
// for development and testing. Go independently enforces that narrower
// boundary so future callers cannot silently lose the no-follow, ownership,
// extended-ACL, and identity checks proved only on those release targets.
package artifacts
