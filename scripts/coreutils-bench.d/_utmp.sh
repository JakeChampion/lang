# The login-accounting database `users`, `who` and `pinky` read, built
# here because the real one cannot be measured over: /var/run/utmp is
# the machine's live login state, a container has none at all, and a
# workload whose input is "however many people happen to be logged in"
# is not a workload. `users` and `who` take the database as an operand,
# so a synthesized file measures the same code path the real one does.
#
# Two sizes: one login, which is startup plus a one-record read, and 4000,
# which is where the per-record work and (for `users`) the sort show up.
#
# The record's TAIL is per-architecture: glibc keeps ut_session and ut_tv
# at their 32-bit widths where __WORDSIZE_TIME64_COMPAT32 is 1 (x86-64,
# 384 bytes) and uses a `long` and a real `struct timeval` where it is 0
# (arm64, 400). Writing one width everywhere would hand GNU a database it
# reads at the wrong stride, and the numbers would not be comparable.
utmp_one="$out/utmp-one"
utmp_many="$out/utmp-many"
if [ ! -f "$utmp_many" ]; then
  python3 - "$utmp_one" "$utmp_many" <<'PY'
import platform, struct, sys

compat32 = platform.machine() in ("x86_64", "amd64", "i386", "i686")
SIZE = 384 if compat32 else 400

def rec(typ, pid, line, user, host, sec):
    out = struct.pack("<hxxi", typ, pid)
    out += line.encode().ljust(32, b"\0")[:32]
    out += line.encode()[-4:].ljust(4, b"\0")
    out += user.encode().ljust(32, b"\0")[:32]
    out += host.encode().ljust(256, b"\0")[:256]
    out += struct.pack("<hh", 0, 0)
    if compat32:
        out += struct.pack("<iii", 0, sec, 0)
    else:
        out += struct.pack("<qqq", 0, sec, 0)
    out += b"\0" * (SIZE - len(out))
    assert len(out) == SIZE, len(out)
    return out

USER_PROCESS, BOOT_TIME = 7, 2
one, many = sys.argv[1:3]
open(one, "wb").write(rec(USER_PROCESS, 1001, "pts/0", "alice", "10.0.0.5", 1614834367))
buf = [rec(BOOT_TIME, 0, "~", "reboot", "", 1614834000)]
for i in range(4000):
    buf.append(rec(USER_PROCESS, 2000 + i, "pts/%d" % (i % 64), "user%04d" % (i * 7919 % 4000),
                   "host%d.example.com" % (i % 97), 1614834367 + i))
open(many, "wb").write(b"".join(buf))
PY
fi
