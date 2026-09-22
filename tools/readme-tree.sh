#!/bin/sh
# Rewrites the architecture tree in README.md from the archtree instrument.
#
# The tree is GENERATED because a hand-written one is a claim about the code
# that nothing checks, and this repository exists to stop making those. Run it
# after any change to the package layout:
#
#   sh tools/readme-tree.sh
#
# With -check it writes nothing and exits non-zero when the README is stale,
# which is the form CI wants.
set -eu
cd "$(dirname "$0")/.."

BEGIN='<!-- archtree:begin -->'
END='<!-- archtree:end -->'

if ! grep -q "$BEGIN" README.md || ! grep -q "$END" README.md; then
	echo "readme-tree: README.md has no archtree markers" >&2
	exit 2
fi

tree=$(mktemp)
out=$(mktemp)
trap 'rm -f "$tree" "$out"' EXIT

go run ./tools/archtree -tree >"$tree"

awk -v begin="$BEGIN" -v end="$END" -v file="$tree" '
	index($0, begin) {
		print
		print "```text"
		while ((getline line < file) > 0) print line
		print "```"
		skip = 1
		next
	}
	index($0, end) { skip = 0 }
	!skip          { print }
' README.md >"$out"

if [ "${1:-}" = "-check" ]; then
	if cmp -s "$out" README.md; then
		exit 0
	fi
	echo "readme-tree: the README tree is stale; run: sh tools/readme-tree.sh" >&2
	diff README.md "$out" || true
	exit 1
fi

cat "$out" >README.md
echo "readme-tree: README.md updated"
