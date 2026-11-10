#!/usr/bin/env bash
# Prints the tag that the commits since the last release call for, or nothing
# when there is nothing to release. Rules: CLAUDE.md, "Releases".
set -euo pipefail

last=$(git tag --list 'v[0-9]*' --sort=-v:refname | sed -n 1p)
if [ -z "$last" ]; then
  echo "no v* tag to start from" >&2
  exit 1
fi

# A newer commit was released first: this one is already covered.
git merge-base --is-ancestor "$last" HEAD || exit 0

log=$(git log --format='%s%n%b' "$last..HEAD")

if grep -qE '^[a-z]+(\([^)]*\))?!:|^BREAKING[ -]CHANGE:' <<<"$log"; then
  echo "::warning::breaking change since $last: majors are an owner call, tag by hand" >&2
  exit 0
fi

IFS=. read -r major minor patch <<<"${last#v}"
if grep -qE '^feat(\([^)]*\))?:' <<<"$log"; then
  echo "v$major.$((minor + 1)).0"
elif grep -qE '^(fix|perf)(\([^)]*\))?:' <<<"$log"; then
  echo "v$major.$minor.$((patch + 1))"
fi
