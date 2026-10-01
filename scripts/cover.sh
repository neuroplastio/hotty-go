#!/bin/sh
# Coverage by package, from a profile written by go test -coverprofile, and
# the gate: every public package (not internal, not a command) covers at
# least MIN percent of its statements. A public package with no tests is 0%.
# The examples are a module of their own, which ./... does not reach.
#
#   GO="mise x -- go" sh scripts/cover.sh coverage.out 85
set -eu
profile=${1:-coverage.out}
min=${2:-85}
GO=${GO:-go}

table=$(awk -F'[: ]' '
	NR == 1 && /^mode:/ { next }
	{
		file = $1
		pkg = file; sub(/\/[^\/]*$/, "", pkg)
		n = $(NF-1); hit = $NF
		total[pkg] += n
		if (hit > 0) covered[pkg] += n
	}
	END {
		for (p in total) printf "%s %.1f\n", p, (total[p] ? 100 * covered[p] / total[p] : 100)
	}' "$profile" | sort)

public=$($GO list -f '{{if ne .Name "main"}}{{.ImportPath}}{{end}}' ./... |
	grep -v -e '/internal/' -e '/internal$' || true)

printf '%-48s %8s\n' package coverage
echo "$table" | while read -r pkg pct; do
	[ -n "$pkg" ] && printf '%-48s %7s%%\n' "$pkg" "$pct"
done

fail=""
for pkg in $public; do
	pct=$(echo "$table" | awk -v p="$pkg" '$1 == p { print $2 }')
	pct=${pct:-0}
	if awk -v c="$pct" -v m="$min" 'BEGIN { exit !(c < m) }'; then
		fail="$fail  $pkg $pct%
"
	fi
done
if [ -n "$fail" ]; then
	printf 'coverage under %s%%:\n%s' "$min" "$fail" >&2
	exit 1
fi
echo "every public package covers at least $min%"
