# touch(1) is one open and one utimensat per operand, so the one-file
# rows are process startup plus that, and the 200-operand rows are
# where the per-operand cost shows. -d parses the date grammar once
# per run, -r stats the reference first, and -c on missing names is
# the path with neither open nor create.
d="$out/touchbench"
mkdir -p "$d"
i=0
while [ "$i" -lt 200 ]; do : > "$d/f$i"; i=$((i + 1)); done
files=$(for n in $(seq 0 199); do printf '%s/f%s ' "$d" "$n"; done)
printf 'touch one file\ty\t{} %s/f0\n' "$d"
printf 'touch 200 files in one run\ty\t{} %s\n' "$files"
printf 'touch -d relative 200 files\ty\tTZ=America/New_York {} -d "2024-11-03 01:30 EDT next monday 3 months ago" %s\n' "$files"
printf 'touch -t 200 files\ty\t{} -t 202406151234.56 %s\n' "$files"
printf 'touch -r -d 200 files\ty\t{} -r %s/f0 -d "-1 day" %s\n' "$d" "$files"
printf 'touch -a -m -h 200 files\ty\t{} -a -h %s\n' "$files"
printf 'touch -c 200 missing names\ty\t{} -c %s\n' "$(for n in $(seq 200); do printf '%s/missing.%s ' "$d" "$n"; done)"
printf 'touch create and remove 10 files\ty\t{} %s/n0 %s/n1 %s/n2 %s/n3 %s/n4 %s/n5 %s/n6 %s/n7 %s/n8 %s/n9 && rm -f %s/n?\n' "$d" "$d" "$d" "$d" "$d" "$d" "$d" "$d" "$d" "$d" "$d"
