# pathchk does one lstat(2) per name and nothing else when the name exists,
# so the default-mode rows are startup plus a syscall and the 200-operand ones
# are where the per-name cost shows above it. The -p rows never touch the
# filesystem at all — they are the character scan and the two length walks over
# the string — and the last default-mode row is the only shape that reaches the
# per-directory statfs walk: a path that does not exist with a component longer
# than POSIX's own minimum.
long=$(printf 'a%.0s' $(seq 200))
wide=$(seq 20 | sed "s|.*|$long|" | paste -sd/ -)
deep=$(seq 40 | sed 's|.*|/nodir&|' | tr -d '\n')

printf 'pathchk one existing path\tn\t{} /usr/local/lib/libfoo.so.1.2.3\n'
printf 'pathchk 200 existing paths\tn\t{} %s\n' "$(seq 200 | sed 's|.*|/usr/lib|' | tr '\n' ' ')"
printf 'pathchk 200 missing paths\tn\t{} %s\n' "$(seq 200 | sed 's|.*|/nodir/x&|' | tr '\n' ' ')"
printf 'pathchk -p 200 operands\tn\t{} -p %s\n' "$(seq 200 | sed 's|.*|abc/def/ghi&|' | tr '\n' ' ')"
printf 'pathchk --portability one 4 KiB name\tn\t{} --portability %s\n' "$wide"
printf 'pathchk the component walk\tn\t{} %s/%s\n' "$deep" "$long"
