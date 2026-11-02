#!/usr/bin/env python3
"""Writes the syscall numbers that Android's app seccomp filter permits.

Usage: android-app-syscalls.py OUT.json

bionic's genseccomp builds the app filter from SYSCALLS.TXT and four lists:
the syscalls that libc makes, plus SECCOMP_ALLOWLIST_COMMON.TXT and
SECCOMP_ALLOWLIST_APP.TXT, less SECCOMP_BLOCKLIST_COMMON.TXT and
SECCOMP_BLOCKLIST_APP.TXT. This script reads those files from the AOSP tag
below, and maps each name to its number with bionic's copies of the kernel
headers. OUT.json maps "arm64" and "x86_64" to sorted lists of numbers.
TestAndroidSeccompCorpus reads it.
"""

import base64
import json
import re
import sys
import urllib.request

TAG = "android-15.0.0_r1"
BASE = f"https://android.googlesource.com/platform/bionic/+/refs/tags/{TAG}/libc/"
LISTS_ADD = ("SYSCALLS.TXT", "SECCOMP_ALLOWLIST_COMMON.TXT", "SECCOMP_ALLOWLIST_APP.TXT")
LISTS_REMOVE = ("SECCOMP_BLOCKLIST_COMMON.TXT", "SECCOMP_BLOCKLIST_APP.TXT")
HEADERS = {
    "arm64": "kernel/uapi/asm-generic/unistd.h",
    "x86_64": "kernel/uapi/asm-x86/asm/unistd_64.h",
}
LP64 = ("arm64", "x86_64", "riscv64")


def fetch(path):
    with urllib.request.urlopen(BASE + path + "?format=TEXT", timeout=60) as r:
        return base64.b64decode(r.read()).decode()


def arches(spec):
    """The ABIs of a SYSCALLS.TXT arch column."""
    out = set()
    for a in spec.split(","):
        a = a.strip()
        if a == "all":
            out.update(("arm", "x86") + LP64)
        elif a == "lp64":
            out.update(LP64)
        elif a == "lp32":
            out.update(("arm", "x86"))
        elif a:
            out.add(a)
    return out


# return_type func[|alias...][:syscall[:socketcall]](args) arches
LINE = re.compile(r"^\s*[^(]*?\s+([\w|]+)(?::([\w]+))?(?::\w+)?\s*\(.*\)\s+([\w,]+)\s*$")


def names(text, arch):
    """The syscall names in a SYSCALLS.TXT-format list for one ABI."""
    out = set()
    for line in text.splitlines():
        line = line.split("#", 1)[0].strip()
        if not line:
            continue
        m = LINE.match(line)
        if m is None:
            raise ValueError(f"cannot read the SYSCALLS line {line!r}")
        func, syscall, spec = m.groups()
        if arch in arches(spec):
            out.add(syscall or func.split("|")[0])
    return out


def numbers(header):
    """__NR_name to number, through the __NR3264_ names and simple aliases."""
    defs = dict(re.findall(r"#define\s+(__NR3264_\w+|__NR_\w+)[ \t]+([^\n]+)", header))

    def value(name, depth=0):
        v = defs.get(name, "").strip()
        if v.isdigit():
            return int(v)
        if depth < 4 and re.fullmatch(r"__NR\w+", v):
            return value(v, depth + 1)
        return None

    out = {}
    for name in defs:
        if name.startswith("__NR_"):
            n = value(name)
            if n is not None:
                out[name[len("__NR_"):]] = n
    return out


def main(path):
    lists = {f: fetch(f) for f in LISTS_ADD + LISTS_REMOVE}
    result = {}
    for arch, header in HEADERS.items():
        table = numbers(fetch(header))
        allowed = set()
        for f in LISTS_ADD:
            allowed |= names(lists[f], arch)
        for f in LISTS_REMOVE:
            allowed -= names(lists[f], arch)
        missing = sorted(n for n in allowed if n not in table)
        nums = sorted({table[n] for n in allowed if n in table})
        if len(nums) < 150:
            raise ValueError(f"{arch}: only {len(nums)} syscalls; the lists were not read")
        print(f"{arch}: {len(nums)} syscalls; {len(missing)} names without a number: {missing}")
        result[arch] = nums
    with open(path, "w") as f:
        json.dump({"tag": TAG, "syscalls": result}, f, indent=1)


if __name__ == "__main__":
    main(sys.argv[1])
