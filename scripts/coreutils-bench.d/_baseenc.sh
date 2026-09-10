# Two regimes, and where they cross is the interesting number. A
# static binary with no loader starts in a fraction of the time,
# so a small file is over before GNU has finished linking; a
# byte-at-a-time transform in Fern runs behind a gcc -O2 loop, so
# a large one is not. Both sizes are here rather than only the one
# that flatters.
small="$out/base-16k.bin"
[ -f "$small" ] || "$gnu/head" -c 16384 /dev/urandom > "$small"
big="$out/base-64m.bin"
[ -f "$big" ] || "$gnu/head" -c 67108864 /dev/urandom > "$big"
case "$1" in
  base64|base32)
    smallenc="$out/base-16k.$1"
    [ -f "$smallenc" ] || "$gnu/$1" "$small" > "$smallenc"
    bigenc="$out/base-64m.$1"
    [ -f "$bigenc" ] || "$gnu/$1" "$big" > "$bigenc"
    printf '%s a 16 KiB file\tn\t{} %s\n' "$1" "$small"
    printf '%s -d a 16 KiB file\tn\t{} -d %s\n' "$1" "$smallenc"
    printf '%s a 64 MiB file\ty\t{} %s > /dev/null\n' "$1" "$big"
    printf '%s -w0 a 64 MiB file\ty\t{} -w0 %s > /dev/null\n' "$1" "$big"
    printf '%s -d a 64 MiB file\ty\t{} -d %s > /dev/null\n' "$1" "$bigenc"
    ;;
  basenc)
    # One row per encoding: they share a codec, so what differs is
    # the block shape and how much output a byte turns into.
    b16="$out/base-64m.b16"
    [ -f "$b16" ] || "$gnu/basenc" --base16 -w0 "$big" > "$b16"
    printf 'basenc --base64 a 16 KiB file\tn\t{} --base64 %s\n' "$small"
    printf 'basenc --base64 -w0 a 64 MiB file\ty\t{} --base64 -w0 %s > /dev/null\n' "$big"
    printf 'basenc --base32hex -w0 a 64 MiB file\ty\t{} --base32hex -w0 %s > /dev/null\n' "$big"
    printf 'basenc --base16 -w0 a 64 MiB file\ty\t{} --base16 -w0 %s > /dev/null\n' "$big"
    printf 'basenc --base2msbf -w0 an 8 MiB slice\ty\t{gnu}/head -c 8388608 %s | {} --base2msbf -w0 > /dev/null\n' "$big"
    printf 'basenc --z85 a 64 MiB file\ty\t{} --z85 %s > /dev/null\n' "$big"
    printf 'basenc --base16 -d a 128 MiB stream\ty\t{} --base16 -d %s > /dev/null\n' "$b16"
    ;;
esac
