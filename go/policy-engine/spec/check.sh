#!/usr/bin/env bash
# Model-checks the specifications with TLC, then checks that each seeded bug in mutants.txt makes
# TLC report the invariant it names. A mutant TLC does not catch means an invariant has gone vacuous.
#
#   spec/check.sh              (downloads tla2tools.jar into .cache/ on first use; needs Java 11+)
set -euo pipefail
cd "$(dirname "$0")"
JAR="$(realpath -m "${TLA2TOOLS:-../../../.cache/tla2tools.jar}")"
if [ ! -f "$JAR" ]; then
  mkdir -p "$(dirname "$JAR")"
  curl -sSfL -o "$JAR" https://github.com/tlaplus/tlaplus/releases/download/v1.8.0/tla2tools.jar
fi
tlc() { java -XX:+UseParallelGC -cp "$JAR" tlc2.TLC -workers auto -cleanup -metadir "$1" -config "$2" "$3" 2>&1; }
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

for spec in Decide Approvals; do
  out=$(tlc "$work/meta-$spec" "$spec.cfg" "$spec.tla") || true
  if ! grep -q "No error has been found" <<<"$out"; then
    echo "$out" | tail -30
    echo "FAIL $spec"
    exit 1
  fi
  echo "ok   $spec: $(grep -o '[0-9,]* distinct states found' <<<"$out")"
done

caught=0
total=0
while IFS='|' read -r name invariant from to; do
  [[ -z "$name" || "$name" == \#* ]] && continue
  total=$((total + 1))
  mkdir -p "$work/$name"
  cp Approvals.cfg "$work/$name/"
  python3 -c '
import sys
src = open("Approvals.tla").read()
old, new = (a.replace("<NL>", "\n") for a in sys.argv[1:3])
assert old in src, f"mutant does not apply: {old!r}"
sys.stdout.write(src.replace(old, new, 1))
' "$from" "$to" >"$work/$name/Approvals.tla"
  out=$( (cd "$work/$name" && tlc meta Approvals.cfg Approvals.tla) ) || true
  if grep -q "Invariant $invariant is violated" <<<"$out"; then
    caught=$((caught + 1))
    echo "ok   mutant $name: caught by $invariant"
  else
    echo "MISS mutant $name: $invariant was not violated"
  fi
done <mutants.txt
echo "$caught of $total seeded bugs caught"
[ "$caught" -eq "$total" ]
