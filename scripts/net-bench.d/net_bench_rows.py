#!/usr/bin/env python3
"""The report rows for one net-bench load.

scripts/net-bench calls this once per load — `oha` for the HTTP/1.1 and
two-fetch rows (from oha's JSON), `h2load` for the HTTP/3 rows (from
h2load's summary and per-request log) — passing the server process
group's user and system clock ticks before (U0 S0) and after (U1 S1) the
load. Both families divide those ticks by the same quantity, the requests
the server handled, so the server-CPU-per-request rows mean the same
thing across protocols. scripts/net-bench.d/test_net_bench_rows.py covers
the parsing and that denominator.
"""
import json
import os
import re
import sys

JIFFY_US = 1e6 / os.sysconf("SC_CLK_TCK")


def cpu_rows(prefix, u0, s0, u1, s1, done):
    # utime/stime are whole clock ticks (10 ms), so each process's share is
    # quantised to a tick and the group total carries up to one tick per
    # process of rounding — finer than the 0.01 µs the figure prints.
    if done <= 0:
        return []
    return [
        f"{prefix}.server_user_us_per_req\t{(u1 - u0) * JIFFY_US / done:.2f}",
        f"{prefix}.server_sys_us_per_req\t{(s1 - s0) * JIFFY_US / done:.2f}",
    ]


def oha_rows(name, n, path, u0, s0, u1, s1):
    j = json.load(open(path))
    s = j["summary"]
    p = j["latencyPercentiles"]
    codes = j.get("statusCodeDistribution", {})
    errs = j.get("errorDistribution", {})
    failed = sum(v for k, v in codes.items() if k != "200")
    failed += sum(v for k, v in errs.items() if "deadline" not in k)
    done = sum(codes.values()) + sum(errs.values())
    row = f"net/{name}/c{n}"
    return [
        f"{row}.rps\t{int(s['requestsPerSec'])}",
        f"{row}.p50_us\t{int(p['p50'] * 1e6)}",
        f"{row}.p99_us\t{int(p['p99'] * 1e6)}",
        f"{row}.failed\t{failed}",
    ] + cpu_rows(row, u0, s0, u1, s1, done)


def h2load_rows(name, n, m, out, log, u0, s0, u1, s1):
    text = open(out).read()
    rps = re.search(r"finished in [^,]*, ([0-9.]+) req/s", text)
    req = re.search(r"requests: (\d+) total, \d+ started, (\d+) done, (\d+) succeeded, (\d+) failed, (\d+) errored, (\d+) timeout", text)
    if not rps or not req:
        sys.exit("net-bench: no h2load summary for %s c%sm%s:\n%s" % (name, n, m, text))
    times = sorted(int(line.split()[2]) for line in open(log) if len(line.split()) == 3)
    def pct(p):
        return times[min(len(times) - 1, int(len(times) * p))] if times else 0
    done = int(req.group(2))
    failed = int(req.group(4)) + int(req.group(5)) + int(req.group(6))
    row = f"net/{name}/c{n}m{m}"
    return [
        f"{row}.rps\t{int(float(rps.group(1)))}",
        f"{row}.p50_us\t{pct(0.50)}",
        f"{row}.p99_us\t{pct(0.99)}",
        f"{row}.failed\t{failed}",
    ] + cpu_rows(row, u0, s0, u1, s1, done)


def build(argv):
    kind = argv[1]
    if kind == "oha":
        name, n, path = argv[2], argv[3], argv[4]
        u0, s0, u1, s1 = (int(x) for x in argv[5:9])
        return oha_rows(name, n, path, u0, s0, u1, s1)
    if kind == "h2load":
        name, n, m, out, log = argv[2], argv[3], argv[4], argv[5], argv[6]
        u0, s0, u1, s1 = (int(x) for x in argv[7:11])
        return h2load_rows(name, n, m, out, log, u0, s0, u1, s1)
    sys.exit("net_bench_rows.py: unknown load kind %r" % kind)


if __name__ == "__main__":
    print("\n".join(build(sys.argv)))
