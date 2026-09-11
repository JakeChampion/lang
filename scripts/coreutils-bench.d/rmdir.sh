# One rmdir(2) per operand, so the whole cost is process startup — and the
# 200-operand row is that cost once against 200 syscalls, which is where the
# per-operand loop shows above the noise. The `mkdir` that reseeds each run
# comes from the GNU directory, so it is the same constant term for all three
# implementations, exactly as `yes`'s `head` is.
d="$out/rmdirbench"
mkdir -p "$d"
printf 'rmdir one directory\ty\t{gnu}/mkdir -p %s/one; {} %s/one\n' "$d" "$d"
printf 'rmdir 200 directories\ty\t{gnu}/mkdir -p %s; for n in %s; do {} %s/d.$n; done\n' "$(for n in $(seq 200); do printf '%s/d.%s ' "$d" "$n"; done)" "$(seq 200 | tr '\n' ' ')" "$d"
printf 'rmdir -p a 16-deep chain\ty\t{gnu}/mkdir -p %s/p/1/2/3/4/5/6/7/8/9/10/11/12/13/14/15; {} -p %s/p/1/2/3/4/5/6/7/8/9/10/11/12/13/14/15\n' "$d" "$d"
printf 'rmdir 200 missing directories\ty\tfor n in %s; do {} %s/gone.$n; done 2>/dev/null\n' "$(seq 200 | tr '\n' ' ')" "$d"
