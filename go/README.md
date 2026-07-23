# Infrawright Go implementation

This module contains the `iw` CLI and its internal packages. Build from the
repository root with `make dist/iw`; run the complete Go suite with
`cd go && go test ./...`.

Canonical JSON and generated-artifact tests cover the committed demo and
current integration corpora under `tests/fixtures/`. Provider metadata is
loaded from `packs/` and the selected `packs/*.packset.json` profile.
