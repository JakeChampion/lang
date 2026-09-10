# Startup, then the two things expr does that cost anything: wide
# integer arithmetic (GNU links GMP for it) and a regexp match
# (GNU calls glibc's).
printf 'expr 1 + 1\tn\t{} 1 + 1\n'
printf 'expr 40-digit product\tn\t{} 9999999999999999999999999999999999999999 %s 9999999999999999999999999999999999999999\n' '\*'
printf 'expr 400-digit product\tn\t{} %s %s %s\n' "$(printf '9%.0s' $(seq 400))" '\*' "$(printf '7%.0s' $(seq 400))"
printf "expr : extracts a suffix\tn\t{} /usr/local/lib/libfoo.so.1.2.3 : '.*\\\\.\\\\(.*\\\\)'\n"
printf 'expr : counts a 4000-byte match\tn\t{} %s : %s\n' "$(printf 'ab%.0s' $(seq 2000))" "'\\(ab\\)*'"
printf 'expr : anchored class over 4000 bytes\tn\t{} %s : %s\n' "$(printf 'ab%.0s' $(seq 2000))" "'[a-z]*x*[a-z]*'"
printf 'expr length of 100000 bytes\tn\t{} length %s\n' "$(printf 'x%.0s' $(seq 100000))"
