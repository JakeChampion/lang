"""Summarize a successful reverse fixture's FERN_RC_TRACE stderr.

Usage: python3 summarize-trace.py TRACE.stderr
The fixture must separately exit zero with empty stdout and leak checking on.
"""

import argparse
import json
from pathlib import Path


def summarize(path):
    live = {}
    peak = allocated = allocations = frees = current = 0
    with path.open() as trace:
        for line in trace:
            if not line.startswith("rctrace "):
                continue
            _, action, pointer, size, *_ = line.split()
            size = int(size, 16)
            if action == "a":
                if pointer in live:
                    raise ValueError("allocation overwrites live pointer")
                live[pointer] = size
                allocated += size
                current += size
                allocations += 1
                peak = max(peak, current)
            elif action == "f":
                current -= live.pop(pointer)
                frees += 1
            else:
                raise ValueError(f"unknown trace action: {action}")
    if not allocations or live or current or allocations != frees:
        raise ValueError("missing trace or unbalanced allocation/free events")
    return {
        "allocations": allocations,
        "frees": frees,
        "allocated_bytes": allocated,
        "peak_live_bytes": peak,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("trace", type=Path)
    args = parser.parse_args()
    print(json.dumps(summarize(args.trace)))


if __name__ == "__main__":
    main()
