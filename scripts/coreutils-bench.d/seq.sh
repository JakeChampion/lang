# The integer engine, the equal-width one and the float one, each
# over enough numbers that startup is not what is measured.
printf 'seq 1 1000000\tn\t{} 1000000\n'
printf 'seq -s, 1 1000000\tn\t{} -s, 1000000\n'
printf 'seq -w 1 1000000\tn\t{} -w 1000000\n'
printf 'seq 0 0.001 1000\tn\t{} 0 0.001 1000\n'
printf 'seq -f %%.3f 0 0.001 1000\tn\t{} -f %%.3f 0 0.001 1000\n'
