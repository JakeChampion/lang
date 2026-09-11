# Eleven algorithms, and they are not one workload: the default CRC
# and the two sum(1) checksums are a byte-at-a-time table loop where
# the eight digests are block kernels, so each of the three groups
# gets a throughput row. --raw and -c are the two paths that are not
# the same program as the *sum utilities: the first writes no line
# at all, the second chooses the algorithm off every line's tag.
big="$out/text-64m.txt"
[ -f "$big" ] || "$gnu/seq" 1 8000000 > "$big"
small="$out/sum-small.txt"
[ -f "$small" ] || "$gnu/seq" 1 100 > "$small"
many="$out/sum-many"
if [ ! -d "$many" ]; then
  mkdir -p "$many"
  for i in $(seq 500); do "$gnu/seq" 1 "$i" > "$many/f$i"; done
fi
cks="$out/cksum-many.txt"
[ -f "$cks" ] || (cd "$many" && "$gnu/cksum" -a sha256 f* > "$cks")
printf 'cksum of a 62 MiB file\tn\t{} %s\n' "$big"
printf 'cksum of a 62 MiB file from a pipe\ty\t{gnu}/cat %s | {}\n' "$big"
printf 'cksum -a sysv of a 62 MiB file\tn\t{} -a sysv %s\n' "$big"
printf 'cksum -a bsd of a 62 MiB file\tn\t{} -a bsd %s\n' "$big"
printf 'cksum -a sha256 of a 62 MiB file\tn\t{} -a sha256 %s\n' "$big"
printf 'cksum -a sm3 of a 62 MiB file\tn\t{} -a sm3 %s\n' "$big"
printf 'cksum -a blake2b of a 62 MiB file\tn\t{} -a blake2b %s\n' "$big"
printf 'cksum --untagged -a md5 of a 62 MiB file\tn\t{} --untagged -a md5 %s\n' "$big"
printf 'cksum --raw of a 62 MiB file\ty\t{} --raw %s > /dev/null\n' "$big"
printf 'cksum of a small file\tn\t{} %s\n' "$small"
printf 'cksum -a sha256 -c over 500 small files\ty\tcd %s && {} -c %s > /dev/null\n' "$many" "$cks"
