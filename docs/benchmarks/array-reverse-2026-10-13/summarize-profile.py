"""Count RC header writes, not attempted retains/releases, in x86-64 Ir dumps.

Usage: python3 summarize-profile.py PROFILE_BINARY CALLGRIND_OUT[.gz]
Requires GNU objdump and a Callgrind --dump-instr=yes, Ir-only profile.
"""

import argparse
import gzip
import json
import re
import subprocess
from collections import defaultdict
from pathlib import Path


def instruction_counts(path):
    opener = gzip.open if path.suffix == ".gz" else open
    counts = defaultdict(int)
    address = 0
    inclusive = False
    total = None
    with opener(path, "rt") as profile:
        for line in profile:
            if line.startswith("events:") and line.strip() != "events: Ir":
                raise ValueError("expected Ir-only events")
            if line.startswith("positions:") and line.strip() != "positions: instr line":
                raise ValueError("expected instruction and line positions")
            if line.startswith("summary:"):
                total = int(line.split()[1])
            elif line.startswith("calls="):
                inclusive = True
            elif line.startswith("fn="):
                address = 0
            elif re.match(r"^(?:0x[0-9a-f]+|[+\-][0-9]+|\*) ", line):
                token, _, cost = line.split()
                if token != "*":
                    value = int(token, 0) if "x" in token else int(token)
                    address = address + value if token[0] in "+-" else value
                if inclusive:
                    inclusive = False
                else:
                    counts[address] += int(cost)
    if sum(counts.values()) != total:
        raise ValueError("self instruction totals do not match summary")
    return counts, total


def header_stores(binary):
    output = subprocess.check_output(["objdump", "-d", str(binary)], text=True)
    kinds = {}
    previous = ""
    for line in output.splitlines():
        match = re.match(r"^\s*([0-9a-f]+):\s+[^\t]+\t(.*)$", line)
        if not match:
            continue
        address, instruction = int(match[1], 16), match[2].strip()
        if "-0x8(%" in instruction:
            if re.match(r"addl\s+\$0x1,-0x8\(%", instruction):
                kinds[address] = "increment"
            elif re.match(r"subl\s+\$0x1,-0x8\(%", instruction):
                kinds[address] = "decrement"
            elif re.match(r"movl\s+\$0x0,-0x8\(%", instruction):
                kinds[address] = "zero"
            elif re.match(r"mov\s+%ecx,-0x8\(%", instruction):
                if re.match(r"add\s+\$0x1,%ecx", previous):
                    kinds[address] = "increment"
                elif re.match(r"sub\s+\$0x1,%ecx", previous):
                    kinds[address] = "decrement"
        previous = instruction
    if set(kinds.values()) != {"increment", "decrement", "zero"}:
        raise ValueError("missing expected RC header store shapes")
    return kinds


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("profile", type=Path)
    args = parser.parse_args()
    counts, total = instruction_counts(args.profile)
    kinds = header_stores(args.binary)
    traffic = {
        kind: sum(counts[address] for address, found in kinds.items() if found == kind)
        for kind in ("increment", "decrement", "zero")
    }
    print(json.dumps({"instructions": total, "rc_header_stores": traffic}))


if __name__ == "__main__":
    main()
