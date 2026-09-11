# Small numbers in bulk (trial division), then the 64-bit path
# where Miller-Rabin and Pollard rho do the work.
nums="$out/factor-nums.txt"
[ -f "$nums" ] || "$gnu/seq" 1 200000 > "$nums"
semi="$out/factor-semiprimes.txt"
[ -f "$semi" ] || python3 - "$semi" <<'PY'
import sys
# Products of two primes just under 2^32, so each needs rho over the
# full 64-bit range rather than trial division.
primes = [4294967291, 4294967279, 4294967231, 4294967197, 4294967189,
          4294967161, 4294967143, 4294967111, 4294967087, 4294967029]
with open(sys.argv[1], "w") as f:
    for i, p in enumerate(primes):
        for q in primes[i:]:
            f.write("%d\n" % (p * q))
PY
printf 'factor 1..200000 from stdin\ty\t{} < %s > /dev/null\n' "$nums"
printf 'factor 55 64-bit semiprimes\ty\t{} < %s > /dev/null\n' "$semi"
printf 'factor one 64-bit semiprime\tn\t{} 18446744065119617029\n'
