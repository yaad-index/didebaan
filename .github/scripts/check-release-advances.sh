#!/usr/bin/env bash
# Fails unless the version a release PR proposes is strictly greater than the
# latest release tag. It says nothing about which increment is right; that is a
# judgement about commit types and belongs to review.
#
# Usage: check-release-advances.sh <proposed> <latest>
# Versions are MAJOR.MINOR.PATCH with an optional -prerelease and no leading v.
# An empty <latest> means nothing has been released yet, so anything advances.
set -euo pipefail

proposed="$1"
latest="${2:-}"

semver='^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'
if ! [[ "$proposed" =~ $semver ]]; then
  echo "proposed version '$proposed' is not MAJOR.MINOR.PATCH[-prerelease]"
  exit 1
fi
if [ -z "$latest" ]; then
  echo "no release tag yet; $proposed advances"
  exit 0
fi
if ! [[ "$latest" =~ $semver ]]; then
  echo "latest release '$latest' is not MAJOR.MINOR.PATCH[-prerelease]"
  exit 1
fi

# greater A B: exit 0 when A > B. Cores compare numerically. With equal cores, a
# release is greater than its own pre-release, and two pre-releases compare by
# version sort, which is close enough to semver for rc.N style suffixes.
greater() {
  local a="$1" b="$2"
  local ac="${a%%-*}" bc="${b%%-*}"
  local ap="" bp=""
  [[ "$a" == *-* ]] && ap="${a#*-}"
  [[ "$b" == *-* ]] && bp="${b#*-}"
  if [ "$ac" != "$bc" ]; then
    [ "$(printf '%s\n%s\n' "$ac" "$bc" | sort -V | tail -1)" = "$ac" ]
    return
  fi
  if [ -z "$ap" ] && [ -n "$bp" ]; then return 0; fi
  if [ -z "$ap" ] || [ -z "$bp" ]; then return 1; fi
  [ "$ap" != "$bp" ] && [ "$(printf '%s\n%s\n' "$ap" "$bp" | sort -V | tail -1)" = "$ap" ]
}

if greater "$proposed" "$latest"; then
  echo "$proposed advances past $latest"
  exit 0
fi
echo "the release proposes $proposed, which does not advance past the latest release $latest"
exit 1
