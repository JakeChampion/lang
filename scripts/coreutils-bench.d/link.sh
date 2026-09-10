# One link(2) and nothing else, so the whole cost is process startup —
# and the 200-operand row is that cost 200 times, which is where the
# margin is visible above the noise. The `rm` that clears the previous
# run's links comes from the GNU directory, so it is the same constant
# term for all three implementations, exactly as `yes`'s `head` is.
d="$out/linkbench"
mkdir -p "$d"; : > "$d/src"
printf 'link one hard link\ty\t{gnu}/rm -f %s/l; {} %s/src %s/l\n' "$d" "$d" "$d"
printf 'link 200 hard links\ty\t{gnu}/rm -f %s/l.*; for n in %s; do {} %s/src %s/l.$n; done\n' "$d" "$(seq 200 | tr '\n' ' ')" "$d" "$d"
