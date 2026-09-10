# Startup-bound like sleep: what a script pays per condition. The
# bracket form carries its `]`; a string compare, an integer compare
# (digit strings, any length), a stat, and a long and/or chain.
close=""; [ "$1" = '[' ] && close=" ]"
printf '%s string equality\tn\t{} abc = abc%s\n' "$1" "$close"
printf '%s integer compare\tn\t{} 12345678901234567890 -gt 1234567890123456789%s\n' "$1" "$close"
printf '%s -f on a file\tn\t{} -f /etc/passwd%s\n' "$1" "$close"
printf '%s -nt on two files\tn\t{} /etc/passwd -nt /etc/group%s\n' "$1" "$close"
printf '%s 40-term and/or chain\tn\t{} %s%s\n' "$1" "$(for i in $(seq 20); do printf 'a = a -a %d -eq %d -o ' "$i" "$i"; done)x = x" "$close"
