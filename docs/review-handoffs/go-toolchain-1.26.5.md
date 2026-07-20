# Go 1.26.5 release-prerequisite handoff

## Intent

- Pin the Go module to the current supported 1.26 patch release before release
  artifacts are produced.
- Run a fresh call-path vulnerability scan without adding the scanner to the
  module graph.
- Correct stale post-archive documentation that still described the completed
  Cobra command tree as containing an unported-command guard.

## Change

- `go/go.mod`: `toolchain go1.26.5` (the language version remains `go 1.26`).
- `go/internal/pyunicode/lower.go`: toolchain-version comment updated; the
  tested `unicode.Version == "15.0.0"` contract and tables are unchanged.
- `docs/go-cutover-roadmap.md`: the 1.26.5 release prerequisite is recorded as
  complete.
- `docs/go-post-archive-compatibility-cleanup.md`: native Cobra help, command
  inventory, completion, and removal of the old dispatch guard are recorded as
  already complete; the actual post-archive candidates remain explicit.

The official Go release history records Go 1.26.5 as released on 2026-07-07
with security fixes in `crypto/tls` and `os` plus compiler, runtime, command,
network, and syscall fixes:
<https://go.dev/doc/devel/release#go1.26.0>.

## Verification

- `go version` — `go version go1.26.5 darwin/arm64`.
- `go test -count=1 ./...` — pass.
- `go test -race -count=1 ./...` — pass.
- `go vet ./...` — pass.
- `go mod tidy -diff` — no diff.
- `go list -m all` — pass; the application module graph is unchanged.
- `go test -count=1 ./cmd/iw -run 'RootCatalog|Transform|Topology|Generation'`
  — all four standing byte gates pass.
- `go test -count=1 ./cmd/iw -run '^TestA6|^TestBlockD5'` — frozen Node
  authoring and adopt/apply differentials pass.
- `go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...` —
  `No vulnerabilities found.` The scanner was invoked at an explicit version
  and did not modify `go.mod` or `go.sum`.
- `gofmt` and `git diff --check` — clean.

No credential, provider API, remote backend, cluster, or live Terraform Apply
was used or made reachable by this change.
