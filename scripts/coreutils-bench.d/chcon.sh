# chcon never completes a context change on a machine without SELinux --
# neither implementation does -- so every row here measures the work AROUND
# that call: process startup, the option scan, and the -R walk.
#
# That is not a thin thing to measure. The walk is one lstat per entry plus
# a readdir per directory, and it is the same walk chmod -R and rm -r do;
# the diagnostic per entry is a formatted, shell-quoted name. The rows
# separate the three: one operand that is not there (startup and the
# option scan alone), a 400-file tree (the walk), and the same tree under
# -v (the walk plus a stdout line per entry).
#
# Every row exits 1 on both sides, which is chcon's ordinary outcome here
# rather than a failed measurement.
d="$out/chconbench"
mkdir -p "$d/t/a" "$d/t/b"
for n in $(seq 200); do
  : > "$d/t/a/f.$n"
  : > "$d/t/b/f.$n"
done
ctx=unconfined_u:object_r:user_home_t:s0
printf 'chcon startup and option scan\tn\t{} %s %s/nosuch 2>/dev/null\n' "$ctx" "$d"
printf 'chcon a long option scan\tn\t{} -R -H -P -v --preserve-root --no-preserve-root %s %s/nosuch 2>/dev/null\n' "$ctx" "$d"
printf 'chcon --reference refusal\tn\t{} --reference=%s/t %s/nosuch 2>/dev/null\n' "$d" "$d"
printf 'chcon -R a 400-file tree\ty\t{} -R %s %s/t 2>/dev/null\n' "$ctx" "$d"
printf 'chcon -Rv a 400-file tree\ty\t{} -Rv %s %s/t >/dev/null 2>&1\n' "$ctx" "$d"
printf 'chcon -R -L a 400-file tree\ty\t{} -R -L %s %s/t 2>/dev/null\n' "$ctx" "$d"
