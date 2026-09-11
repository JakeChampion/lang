# The dump walks the whole vector and writes it; a named lookup
# is startup plus one scan. The pipeline partner keeps the
# environment the same size on both sides.
printf 'printenv\tn\t{}\n'
printf 'printenv -0\tn\t{} -0\n'
printf 'printenv PATH\tn\t{} PATH\n'
printf 'printenv four names\tn\t{} PATH HOME LANG TERM\n'
