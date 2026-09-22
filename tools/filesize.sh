#!/bin/sh
# INSTRUMENT: production file length.
#
# Answers: how many lines each non-test Go file under internal/ and cmd/ has.
#
# BLIND TO, and this matters more than the number it prints:
#   - complexity. A tight 400 line file can be easier to hold in your head than
#     a sprawling 200 line one. Lines are a proxy for the property, not the
#     property.
#   - generated code, which it counts exactly like authored code.
#   - test files, excluded on purpose, so a 3000 line test file is invisible
#     here and this instrument will never say so.
#   - whether a long file is long for a STATED REASON. The house rule permits
#     that, and this instrument cannot read a reason.
#
# It emits the JSON a criterion selects from. It does NOT decide pass or fail.
# The threshold lives in the criterion, frozen before the run, so the same
# instrument serves a project whose rule is 500 lines without being edited.
set -eu
cd "$(dirname "$0")/.."

files=$(find internal cmd -name '*.go' ! -name '*_test.go' 2>/dev/null | sort)
count=$(printf '%s\n' "$files" | grep -c . || true)
max=$(printf '%s\n' "$files" | while read -r f; do [ -n "$f" ] && wc -l < "$f"; done | tr -d ' ' | sort -rn | head -1)

printf '{\n  "unit": "lines",\n  "population": "production-go-files",\n'
printf '  "count": %s,\n  "max_lines": %s,\n  "files": [\n' "$count" "${max:-0}"
sep=""
printf '%s\n' "$files" | while read -r f; do
  [ -z "$f" ] && continue
  n=$(wc -l < "$f" | tr -d ' ')
  printf '%s    {"path": "%s", "lines": %s}' "$sep" "$f" "$n"
  sep=",
"
done
printf '\n  ]\n}\n'
