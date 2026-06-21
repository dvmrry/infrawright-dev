#!/usr/bin/env bash
# Publish a tagged release of this (private, high-churn) dev repo to the public
# release repo as ONE clean commit per tag. Tracked files only — working-tree
# cruft and the dev history never ship. The public repo accumulates a clean
# release-only timeline (0.1, 0.1.1, 0.2, ...).
#
# Usage: scripts/release.sh <tag> <public-repo-url>
#   scripts/release.sh 0.1 git@github.com:dvmrry/infrawright.git
#
# The tag is AUTO-CREATED on the latest origin/main if it doesn't exist yet, so
# `release.sh <tag> <url>` is the whole release. The public repo must already
# exist (create it empty on GitHub first). The script STOPS before pushing the
# public tree — review it, then push yourself.
set -euo pipefail

TAG="${1:?usage: scripts/release.sh <tag> <public-repo-url>}"
PUBLIC_URL="${2:?need the public release repo URL (e.g. git@github.com:dvmrry/infrawright.git)}"
DEV_ROOT="$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"
STAGE="$(mktemp -d)/public"

# Auto-tag: a release IS a tag, so cutting one shouldn't need a separate manual
# step. If the tag doesn't exist yet, create it on the latest merged main and
# push it to the dev repo. Idempotent — an existing tag is reused untouched.
if ! git -C "$DEV_ROOT" rev-parse "refs/tags/$TAG" >/dev/null 2>&1; then
  git -C "$DEV_ROOT" fetch --quiet origin main
  echo "tag '$TAG' absent — creating on origin/main ($(git -C "$DEV_ROOT" rev-parse --short origin/main))"
  git -C "$DEV_ROOT" tag -a "$TAG" origin/main -m "infrawright $TAG"
  git -C "$DEV_ROOT" push --quiet origin "$TAG"
  echo "tagged + pushed $TAG"
fi

# 1. Clone the existing public repo (to accumulate history), or init a fresh one.
if git clone --quiet "$PUBLIC_URL" "$STAGE" 2>/dev/null \
   && [ -n "$(ls -A "$STAGE" 2>/dev/null | grep -v '^\.git$' || true)" ]; then
  echo "cloned existing public repo -> $STAGE"
  find "$STAGE" -mindepth 1 -maxdepth 1 ! -name .git -exec rm -rf {} +   # wipe old tree, keep .git
else
  echo "public repo empty/new -> initializing fresh history"
  rm -rf "$STAGE"; mkdir -p "$STAGE"
  git -C "$STAGE" init -q
  git -C "$STAGE" remote add origin "$PUBLIC_URL" 2>/dev/null || true
fi

# 2. Lay down the tag's TRACKED tree (git archive = tracked files only; no .git, no cruft).
git -C "$DEV_ROOT" archive "$TAG" | tar -x -C "$STAGE"
echo "staged $TAG tree: $(find "$STAGE" -type f ! -path '*/.git/*' | wc -l | tr -d ' ') files"

# 3. Self-containment guard — a release missing the shared pack or engine is broken.
#    (This is the exact failure mode a fresh clone would hit; catch it before publish.)
for must in packs/_shared/zscaler/collector.py engine/transform.py packs/zia/registry.json LICENSE README.md; do
  test -f "$STAGE/$must" || { echo "FATAL: release is missing $must — aborting"; exit 2; }
done
echo "self-containment guard: OK"

# 4. One clean commit + tag on the public repo. No push (that's your call).
cd "$STAGE"
git add -A
git commit -q -m "infrawright $TAG"
git tag -f "$TAG" >/dev/null

cat <<EOF

Public release staged (clean tree, release-only history) at:
  $STAGE

Review it, then publish:
  git -C "$STAGE" push origin HEAD:main
  git -C "$STAGE" push origin "$TAG"

(Stopped before push — that's your button.)
EOF
