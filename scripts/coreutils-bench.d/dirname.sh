printf 'dirname one path\tn\t{} /usr/local/lib/libfoo.so.1.2.3\n'
printf 'dirname 200 operands\tn\t{} %s\n' "$(seq 200 | sed 's|.*|/usr/lib/x86_64-linux-gnu/lib&.so.1.2.3|' | tr '\n' ' ')"
