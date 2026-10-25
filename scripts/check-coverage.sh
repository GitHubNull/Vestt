#!/usr/bin/env bash
# SPEC-14 §3.1 coverage gate: aggregates a `go test -coverprofile` output per
# package group and enforces the SPEC thresholds:
#   common/** >= 70%, common/engine/** >= 80%, common/models/** + common/torznab/** >= 85%
set -euo pipefail

if [ $# -ne 1 ] || [ ! -f "$1" ]; then
	echo "usage: $0 <coverprofile>" >&2
	exit 2
fi

awk '
/^mode:/ { next }
{
	# line: path:from,to numStatements count
	n = split($1, loc, ":")
	file = loc[1]
	stmts = $2 + 0
	count = $3 + 0
	if (file ~ /^vestt\/common\//) {
		cN += stmts; if (count > 0) cC += stmts
	}
	if (file ~ /^vestt\/common\/engine\//) {
		eN += stmts; if (count > 0) eC += stmts
	}
	if (file ~ /^vestt\/common\/models\// || file ~ /^vestt\/common\/torznab\//) {
		mN += stmts; if (count > 0) mC += stmts
	}
}
function pct(cov, total) { return total > 0 ? 100 * cov / total : 0 }
function check(label, cov, total, need) {
	p = pct(cov, total)
	printf "%-22s %6.2f%%  (threshold %d%%, %d/%d statements)\n", label, p, need, cov, total
	if (p < need) { print "FAIL: " label " coverage below threshold" > "/dev/stderr"; bad = 1 }
}
END {
	check("common/**", cC, cN, 70)
	check("common/engine/**", eC, eN, 80)
	check("models+torznab/**", mC, mN, 85)
	exit bad ? 1 : 0
}' "$1"
