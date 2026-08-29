#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
adr_dir="$repo_root/docs/adr"
index="$adr_dir/README.md"
root_readme="$repo_root/README.md"

front_matter_status() {
  awk '
    NR == 1 && $0 != "---" { exit }
    NR > 1 && $0 == "---" { exit }
    /^status:/ { sub(/^status:[[:space:]]*/, ""); print; exit }
  ' "$1"
}

index_status() {
  grep -F "($1)" "$index" | sed -n 's/.*(\(.*\))[[:space:]]*$/\1/p'
}

word_to_count() {
  local word="${1,,}"
  local ones=(zero one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen)
  local tens=(zero ten twenty thirty forty fifty sixty seventy eighty ninety)
  local tens_word="$word"
  local ones_word=""
  if [[ "$word" == *-* ]]; then
    tens_word="${word%%-*}"
    ones_word="${word#*-}"
  fi
  local tens_value="" ones_value=0
  local i
  for i in "${!tens[@]}"; do
    if [ "${tens[$i]}" = "$tens_word" ]; then
      tens_value=$((i * 10))
    fi
  done
  if [ -n "$ones_word" ]; then
    ones_value=""
    for i in "${!ones[@]}"; do
      if [ "${ones[$i]}" = "$ones_word" ]; then
        ones_value="$i"
      fi
    done
  fi
  if [ -z "$tens_value" ] && [ -n "$ones_word" ]; then
    return 1
  fi
  if [ -z "$tens_value" ]; then
    for i in "${!ones[@]}"; do
      if [ "${ones[$i]}" = "$word" ]; then
        echo "$i"
        return 0
      fi
    done
    return 1
  fi
  if [ -z "$ones_value" ]; then
    return 1
  fi
  echo $((tens_value + ones_value))
}

records="$(find "$adr_dir" -maxdepth 1 -name '[0-9][0-9][0-9][0-9]-*.md' -exec basename {} \; | sort)"
linked="$(grep -oE '\(([0-9]{4}-[^)]+\.md)\)' "$index" | tr -d '()' | sort -u)"

missing_from_index=""
for f in $records; do
  if ! printf '%s\n' "$linked" | grep -qxF "$f"; then
    missing_from_index="$missing_from_index$f"$'\n'
  fi
done

dangling_index_entry=""
for l in $linked; do
  if [ ! -f "$adr_dir/$l" ]; then
    dangling_index_entry="$dangling_index_entry$l"$'\n'
  fi
done

status_drift=""
for l in $linked; do
  if [ ! -f "$adr_dir/$l" ]; then
    continue
  fi
  recorded="$(front_matter_status "$adr_dir/$l")"
  indexed="$(index_status "$l")"
  if [ -z "$recorded" ]; then
    status_drift="$status_drift$l declares no status in its front matter"$'\n'
  elif [ "$recorded" != "$indexed" ]; then
    status_drift="$status_drift$l records \"$recorded\" but the index says \"$indexed\""$'\n'
  fi
done

record_count="$(printf '%s\n' "$records" | grep -c . || true)"
readme_count_word="$(grep -oE '[A-Za-z]+(-[A-Za-z]+)? architecture decision records' "$root_readme" | head -n1 | awk '{print $1}' || true)"
readme_count=""
if [ -n "$readme_count_word" ]; then
  readme_count="$(word_to_count "$readme_count_word" || true)"
fi

count_drift=""
if [ -z "$readme_count_word" ]; then
  count_drift="README.md states no architecture decision record count to check"$'\n'
elif [ -z "$readme_count" ]; then
  count_drift="README.md's record count word \"$readme_count_word\" could not be parsed"$'\n'
elif [ "$record_count" -ne "$readme_count" ]; then
  count_drift="README.md says \"$readme_count_word\" ($readme_count) architecture decision records but $record_count exist"$'\n'
fi

status=0
if [ -n "$missing_from_index" ]; then
  echo "ADR files not linked in docs/adr/README.md:"
  printf '%s' "$missing_from_index" | sed 's/^/  - /'
  status=1
fi
if [ -n "$dangling_index_entry" ]; then
  echo "Index entries pointing to missing files:"
  printf '%s' "$dangling_index_entry" | sed 's/^/  - /'
  status=1
fi
if [ -n "$status_drift" ]; then
  echo "ADR statuses that disagree with docs/adr/README.md:"
  printf '%s' "$status_drift" | sed 's/^/  - /'
  status=1
fi
if [ -n "$count_drift" ]; then
  echo "ADR record count drift:"
  printf '%s' "$count_drift" | sed 's/^/  - /'
  status=1
fi

if [ "$status" -eq 0 ]; then
  echo "ADR index is in sync."
fi
exit "$status"
