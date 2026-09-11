# A 100k-edge DAG over 50k names, generated once per run. Acyclic so
# the workload is the queue phase, not the loop report.
graph="$out/tsort-100k.txt"
[ -f "$graph" ] || python3 - "$graph" <<'PY'
import random, sys
rnd = random.Random(8288)
with open(sys.argv[1], "w") as f:
    for _ in range(100000):
        i = rnd.randrange(0, 49999)
        j = rnd.randrange(i + 1, 50000)
        f.write("n%d n%d\n" % (i, j))
PY
printf 'tsort 100k-edge DAG from a file\tn\t{} %s\n' "$graph"
