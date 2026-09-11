# stat is a syscall and a formatter, so the workloads separate the two.
# One operand is almost pure startup, which is where a static binary with no
# dynamic loader should win outright. Four thousand operands in one process is
# the per-file cost — one stat(2) and one pass over the format each — and the
# widening formats say what the format engine charges per directive.
#
# The three formats that need more than the record are singled out because
# each pays for something different: %U and %G read /etc/passwd and
# /etc/group, %y builds a civil time out of the local zone, and %m
# canonicalizes the operand and then walks up the tree stat'ing each parent.
#
# The many-operand rows are shell workloads so the glob is expanded by the
# shell rather than baked into a 200 KB command line, and every
# implementation pays the same shell.
#
# The default block, --terse and --file-system are absent: this build refuses
# them for want of a birth time and three statfs fields (JakeChampion/lang
# issues 9096 and 9097), so there is nothing to time against GNU there.
tree="$out/stat-tree"
if [ ! -d "$tree" ]; then
  mkdir -p "$tree"
  i=0
  while [ "$i" -lt 4000 ]; do : > "$tree/f$i"; i=$((i + 1)); done
  ln -sf f0 "$tree/link"
  sync
fi
printf 'one operand, one directive\tn\t{} -c %%s %s/f0\n' "$tree"
printf 'one operand, sixteen directives\tn\t{} -c %%a-%%b-%%B-%%d-%%D-%%f-%%g-%%h-%%i-%%o-%%r-%%R-%%s-%%t-%%T-%%u %s/f0\n' "$tree"
printf '4000 operands, %%s\ty\t{} -c %%s %s/f*\n' "$tree"
printf '4000 operands, %%n\ty\t{} -c %%n %s/f*\n' "$tree"
printf '4000 operands, the numeric alphabet\ty\t{} -c %%a-%%b-%%B-%%d-%%D-%%f-%%g-%%h-%%i-%%o-%%r-%%R-%%s-%%t-%%T-%%u %s/f*\n' "$tree"
printf '4000 operands, %%A and %%F\ty\t{} -c %%A-%%F %s/f*\n' "$tree"
printf '4000 operands, %%N quoted\ty\t{} -c %%N %s/f*\n' "$tree"
printf '4000 operands, widths and precisions\ty\t{} -c "[%%12.4s][%%-12s][%%08i][%%#12a]" %s/f*\n' "$tree"
printf '4000 operands, user and group names\ty\t{} -c %%U:%%G %s/f*\n' "$tree"
printf '4000 operands, a local timestamp\ty\t{} -c %%y %s/f*\n' "$tree"
printf '4000 operands, epoch seconds and nanoseconds\ty\t{} -c %%Y.%%.9Y %s/f*\n' "$tree"
printf '4000 operands, the mount point\ty\t{} -c %%m %s/f*\n' "$tree"
printf '4000 operands, --printf\ty\t{} --printf "%%s\\\\n" %s/f*\n' "$tree"
printf '4000 operands under -L\ty\t{} -L -c %%i %s/f*\n' "$tree"
