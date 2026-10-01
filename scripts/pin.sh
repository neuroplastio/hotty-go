#!/bin/sh
# Pins the requirements between the repository's modules to one version of
# them all. Inside the repository the modules build against each other's
# directories (replace); these requirements are what a program that uses
# the SDK gets, and each must be a version that exists: a program's
# `go mod tidy` fetches, say, hottytest at the version hottytea requires.
#
#   sh scripts/pin.sh v0.1.0       # a release: tag every module (v0.1.0,
#                                  # term/v0.1.0, …) at the commit pinning it
#   sh scripts/pin.sh origin/main  # before there is one: a pushed commit
set -eu
rev=${1:?usage: pin.sh VERSION|COMMIT}
GO=${GO:-go}
repo=github.com/neuroplastio/hotty-go

case $rev in
v[0-9]*)
	version=$rev
	;;
*)
	sha=$(git rev-parse --verify "$rev^{commit}")
	if [ -z "$(git branch -r --contains "$sha")" ]; then
		echo "pin.sh: $rev is not pushed: a program could not fetch it" >&2
		exit 1
	fi
	# A pseudo-version: the commit's time in UTC and its hash.
	when=$(TZ=UTC git log -1 --format=%cd --date=format-local:%Y%m%d%H%M%S "$sha")
	version=v0.0.0-$when-$(git rev-parse --short=12 "$sha")
	;;
esac

for gomod in */go.mod; do
	dir=${gomod%/go.mod}
	# The repository's modules this one requires; a replace line has no
	# version after the path.
	for path in $(grep -oE "$repo(/[a-z]+)? v[^ ]+" "$gomod" | cut -d' ' -f1 | sort -u); do
		(cd "$dir" && $GO mod edit -require="$path@$version")
	done
	echo "$dir: $version"
done
