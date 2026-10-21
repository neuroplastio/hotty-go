#!/bin/sh
# Tags a release of every library module at HEAD: vX.Y.Z for the root and
# <module>/vX.Y.Z for each of the others, one number for the repository
# (gov P-5 rule 6). It refuses unless the release is ready:
#
# - X.Y is the hotty version the SDK speaks: hotty.Version in hotty.go, and
#   the conformance vectors' "version";
# - the tree is clean, on main, and HEAD is origin/main;
# - every requirement between the repository's modules names vX.Y.Z
#   (make pin REV=vX.Y.Z, committed);
# - none of the tags exists yet, here or on origin: a pushed tag is
#   permanent, and Go's checksum database keeps what it named.
#
#   sh scripts/tag.sh v0.4.0
#
# It only tags. Pushing them is the release, and prints as the next step.
set -eu
version=${1:?usage: tag.sh vX.Y.Z}
repo=github.com/neuroplastio/hotty-go
# The library's modules besides the root; examples and internal/docgen's
# fixtures are not released.
modules="hottyedit hottyterm hottytea hottytest hottyvt"

fail() {
	echo "tag.sh: $*" >&2
	exit 1
}

echo "$version" | grep -qE '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' ||
	fail "$version is not vX.Y.Z"
spec=$(echo "$version" | sed -E 's/^v([0-9]+\.[0-9]+)\..*/\1/')
case $spec in
0.* | 1.*) ;;
*) fail "$version: a major of 2 or more moves the module paths to /v${spec%%.*}" ;;
esac

# The hotty version, as the SDK sends it and as the vectors are for.
speaks=$(sed -n 's/^[[:space:]]*Version = "\([^"]*\)"$/\1/p' hotty.go)
[ -n "$speaks" ] || fail "no Version in hotty.go"
[ "$speaks" = "$spec" ] || fail "$version is for hotty $spec, and hotty.go's Version is $speaks"
vectors=$(sed -n 's/^ "version": "\([^"]*\)",$/\1/p' testdata/conformance/vectors.json)
[ -n "$vectors" ] || fail "no top-level \"version\" in testdata/conformance/vectors.json"
[ "$vectors" = "$spec" ] || fail "$version is for hotty $spec, and the vectors are for $vectors"

# The tree: clean, main, pushed.
[ -z "$(git status --porcelain)" ] || fail "the tree is not clean"
branch=$(git symbolic-ref -q --short HEAD || true)
[ "$branch" = main ] || fail "not on main (${branch:-a detached HEAD})"
git fetch -q origin main
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || fail "HEAD is not origin/main: pull or push first"

# The requirements between the modules; a replace line has no version
# after the path.
for gomod in */go.mod; do
	for req in $(grep -oE "$repo(/[a-z]+)? v[^ ]+" "$gomod" | sed 's/ /@/'); do
		[ "${req#*@}" = "$version" ] || fail "$gomod requires ${req%@*} at ${req#*@}, not $version: make pin REV=$version"
	done
done

tags=$version
for m in $modules; do
	[ -f "$m/go.mod" ] || fail "no module $m"
	tags="$tags $m/$version"
done
for t in $tags; do
	! git rev-parse -q --verify "refs/tags/$t" >/dev/null || fail "$t is tagged already"
	[ -z "$(git ls-remote --tags origin "refs/tags/$t")" ] || fail "$t is on origin already"
done

git tag -a "$version" -m "hotty-go ${version#v}: hotty $spec"
for m in $modules; do
	git tag -a "$m/$version" -m "hotty-go/$m ${version#v}: hotty $spec"
done
echo "tagged $(git rev-parse --short HEAD): $tags"
echo "to release them: git push origin $tags"
