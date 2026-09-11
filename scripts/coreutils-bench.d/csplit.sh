# csplit's cost is a scan for the break line plus the copy. The
# line-count form never looks at a byte outside the newline scan;
# the regexp forms run the pattern over every line, so they are
# where the engine in lib/bre.fern shows — a literal and a pattern
# with a literal prefix are byte scans, an alternation is the
# simulation (#8820).
big="$out/text-64m.txt"
[ -f "$big" ] || "$gnu/seq" 1 8000000 > "$big"
pieces="$out/csplit-pieces"
mkdir -p "$pieces"
printf 'csplit at line 4000000 of a 62 MiB file\tn\t{} -s -f %s/xx %s 4000000\n' "$pieces" "$big"
printf 'csplit at /4000000/ of a 62 MiB file\tn\t{} -s -f %s/xx %s /4000000/\n' "$pieces" "$big"
printf 'csplit at /^4000000$/ of a 62 MiB file\tn\t{} -s -f %s/xx %s %s\n' "$pieces" "$big" "'/^4000000\$/'"
printf 'csplit at a never-matching regexp\tn\t{} -s -f %s/xx %s /nomatchanywhere/\n' "$pieces" "$big"
printf 'csplit into 80 pieces of a 62 MiB file\tn\t{} -s -z -b %%06d -f %s/xx %s 100000 {*}\n' "$pieces" "$big"
printf 'csplit at a literal-prefixed class\tn\t{} -s -f %s/xx %s %s\n' "$pieces" "$big" "'/^4000000[0-9]*\$/'"
printf 'csplit at an alternation\tn\t{} -s -f %s/xx %s %s\n' "$pieces" "$big" "'/4000000\\|zzzz/'"
