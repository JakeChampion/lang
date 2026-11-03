#!/usr/bin/env python3
"""Writes internal/stdlib/std/opus/pvq_table.fern.

Usage: gen-opus-pvq.py

The module holds U(N, K), the counts that the PVQ codewords of CELT use, in
the layout of CELT_PVQ_U_DATA in the cwrs.c file of libopus 1.4. Row N holds
U(N, K) for K from N up to the last K of the row, at ROW[N] + K. The script
calculates each value from the recurrence of cwrs.c, so it needs no copy of
libopus.
"""

from pathlib import Path

OUT = Path(__file__).resolve().parent.parent / "internal/stdlib/std/opus/pvq_table.fern"

# The last K of each row N, as in libopus. A standard Opus mode splits a band
# into at most 176 bins, and each later row stops at the last value that fits
# in 32 bits.
LAST_K = [176, 176, 176, 176, 176, 176, 96, 54, 37, 28, 24, 19, 18, 16, 14]


def table():
    """U(N, K) from U(N, K) = U(N - 1, K) + U(N, K - 1) + U(N - 1, K - 1)."""
    size = max(LAST_K) + 2
    u = [[0] * size for _ in range(size)]
    u[0][0] = 1
    for n in range(1, size):
        for k in range(1, size):
            u[n][k] = u[n - 1][k] + u[n][k - 1] + u[n - 1][k - 1]
    return u


def main():
    u = table()
    data = []
    rows = []
    for n, last in enumerate(LAST_K):
        rows.append(len(data) - n)
        for k in range(n, last + 1):
            assert u[n][k] < 1 << 32, (n, k)
            data.append(u[n][k])
    out = [
        "// U(N, K) for the PVQ codewords of CELT, in the layout of the cwrs.c file",
        "// of libopus 1.4. scripts/gen-opus-pvq.py writes this file. Do not edit it.",
        "",
        "// The index in DATA of U(N, 0) for row N. Row N holds the values for K",
        "// from N up to the last K of the row.",
        "pub const ROW: i32[] = [" + ", ".join(str(r) for r in rows) + "];",
        "",
        "pub const DATA: u32[] = [",
    ]
    for i in range(0, len(data), 8):
        out.append("  " + ", ".join(f"{v}u32" for v in data[i:i + 8]) + ",")
    out.append("];")
    OUT.write_text("\n".join(out) + "\n")


if __name__ == "__main__":
    main()
