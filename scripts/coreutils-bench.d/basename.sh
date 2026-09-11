printf 'basename one path\tn\t{} /usr/local/lib/libfoo.so.1.2.3 .3\n'
printf 'basename -a 200 operands\tn\t{} -a -s .o %s\n' "$(seq 200 | sed 's|.*|/usr/lib/x86_64-linux-gnu/lib&.o|' | tr '\n' ' ')"
