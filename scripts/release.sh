#!/usr/bin/env bash
# Tags a release: make release VERSION=x.y.z [DRY_RUN=1]
# Checks that main is clean and current, that the version is new, and that
# api/openapi.json's info.version names this release if the API changed since
# the last one. Runs make ci, then creates and pushes the tag vX.Y.Z.
set -euo pipefail

die() { echo "release: $*" >&2; exit 1; }
spec_version() { grep -m1 '"version":' | sed -E 's/.*"version": *"([^"]*)".*/\1/'; }

v="${VERSION:-}"
[[ "$v" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "usage: make release VERSION=x.y.z"
tag="v$v"

[[ "$(git branch --show-current)" == main ]] || die "not on main"
[[ -z "$(git status --porcelain)" ]] || die "uncommitted changes"
git fetch -q origin main --tags
[[ "$(git rev-parse HEAD)" == "$(git rev-parse origin/main)" ]] || die "main differs from origin/main; pull or push first"
git rev-parse -q --verify "refs/tags/$tag" >/dev/null && die "$tag already exists"

last="$(git tag --list 'v*' --sort=-v:refname | head -1)"
if [[ -n "$last" ]]; then
  newest="$(printf '%s\n%s\n' "${last#v}" "$v" | sort -t. -k1,1n -k2,2n -k3,3n | tail -1)"
  [[ "$newest" == "$v" ]] || die "$tag is not newer than $last"
fi

spec="$(spec_version < api/openapi.json)"
if [[ -z "$last" ]] || ! git diff --quiet "$last" HEAD -- api/openapi.json; then
  [[ "$spec" == "$v" ]] || die "the API changed since ${last:-the start}: set info.version in api/openapi.json to $v (it is $spec)"
fi

make ci

if [[ -n "${DRY_RUN:-}" ]]; then
  echo "release: checks passed; DRY_RUN set, not tagging $tag (API version $spec)"
  exit 0
fi
git tag -a "$tag" -m "mfide $tag"
git push -q origin "$tag"
echo "release: tagged and pushed $tag (API version $spec)"
