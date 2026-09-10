printf 'echo hello world\tn\t{} hello world\n'
printf 'echo -e with escapes\tn\t{} -e a\\tb\\nc\\x41\\0101\n'
printf 'echo 200 operands\tn\t{} %s\n' "$(seq 200 | tr '\n' ' ')"
