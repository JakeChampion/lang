# A field at a time over a large table is what numfmt is for; the
# single-number rows are startup plus one long-double conversion.
printf 'numfmt --to=si 1000\tn\t{} --to=si 1000\n'
printf 'numfmt --from=si 1.5G\tn\t{} --from=si 1.5G\n'
printf 'numfmt --to=iec --format %%.3f\tn\t{} --to=iec --format %%.3f 1234567890\n'
printf 'numfmt 5000 operands\tn\t{} --to=si %s\n' "$(seq 1000 1000 5000000 | tr '\n' ' ')"
printf 'numfmt 200k lines from stdin\ty\t{gnu}/seq 1000 1000 200000000 | {} --to=si > /dev/null\n'
printf 'numfmt 200k lines in a field\ty\t{gnu}/seq 1000 1000 200000000 | {gnu}/sed "s/^/x /" | {} --field=2 --padding=12 --to=iec > /dev/null\n'
