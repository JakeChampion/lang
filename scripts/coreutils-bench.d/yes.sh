printf 'yes | head -c 1G\ty\t{} | {gnu}/head -c 1000000000 > /dev/null\n'
printf 'yes 70000-byte line | head -c 1G\ty\t{} %s | {gnu}/head -c 1000000000 > /dev/null\n' "$(printf 'z%.0s' $(seq 70000))"
