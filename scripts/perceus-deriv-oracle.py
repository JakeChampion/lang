#!/usr/bin/env python3
"""Reference/range audit, never a timed benchmark implementation.

Interned DAG nodes and memoization let Python's arbitrary-precision arithmetic
audit the expanded expression without allocating every repeated subtree.
Rewrite ordering follows pinned Koka deriv.kk; leaf counts include multiplicity.
"""

import argparse
import functools
import json
import sys

sys.setrecursionlimit(20000)
nodes = []
interned = {}
minimum = 0
maximum = 0
constant_powers = 0


def node(kind, a, b=None):
    key = (kind, a, b)
    if key not in interned:
        interned[key] = len(nodes)
        nodes.append(key)
    return interned[key]


def val(n):
    global minimum, maximum
    minimum = min(minimum, n)
    maximum = max(maximum, n)
    return node("val", n)


@functools.cache
def add(a, b):
    ak, av, ar = nodes[a]
    bk, bv, br = nodes[b]
    if ak == bk == "val":
        return val(av + bv)
    if ak == "val" and av == 0:
        return b
    if bk == "val" and bv == 0:
        return a
    if bk == "val":
        return add(b, a)
    if ak == "val" and bk == "add" and nodes[bv][0] == "val":
        return add(val(av + nodes[bv][1]), br)
    if bk == "add" and nodes[bv][0] == "val":
        return add(bv, add(a, br))
    if ak == "add":
        return add(av, add(ar, b))
    return node("add", a, b)


@functools.cache
def mul(a, b):
    ak, av, ar = nodes[a]
    bk, bv, br = nodes[b]
    if ak == bk == "val":
        return val(av * bv)
    if (ak == "val" and av == 0) or (bk == "val" and bv == 0):
        return val(0)
    if ak == "val" and av == 1:
        return b
    if bk == "val" and bv == 1:
        return a
    if bk == "val":
        return mul(b, a)
    if ak == "val" and bk == "mul" and nodes[bv][0] == "val":
        return mul(val(av * nodes[bv][1]), br)
    if bk == "mul" and nodes[bv][0] == "val":
        return mul(bv, mul(a, br))
    if ak == "mul":
        return mul(av, mul(ar, b))
    return node("mul", a, b)


@functools.cache
def power(a, b):
    global constant_powers
    ak, av, _ = nodes[a]
    bk, bv, _ = nodes[b]
    if ak == bk == "val":
        constant_powers += 1
        # Pinned Koka kklib/src/integer.c handles 0 and +/-1 before
        # truncating other negative powers to zero.
        if bv == 0:
            return val(1)
        if av in (0, 1):
            return val(av)
        if av == -1:
            return val(1 if bv % 2 == 0 else -1)
        return val(0 if bv < 0 else pow(av, bv))
    if bk == "val" and bv == 0:
        return val(1)
    if bk == "val" and bv == 1:
        return a
    if ak == "val" and av == 0:
        return val(0)
    return node("pow", a, b)


def logarithm(a):
    if nodes[a] == ("val", 1, None):
        return val(0)
    return node("ln", a)


@functools.cache
def derivative(a):
    kind, l, r = nodes[a]
    if kind == "val":
        return val(0)
    if kind == "var":
        return val(int(l == "x"))
    if kind == "add":
        return add(derivative(l), derivative(r))
    if kind == "mul":
        return add(mul(l, derivative(r)), mul(r, derivative(l)))
    if kind == "pow":
        return mul(power(l, r), add(mul(mul(r, derivative(l)), power(l, val(-1))),
                                    mul(logarithm(l), derivative(r))))
    if kind == "ln":
        return mul(derivative(l), power(l, val(-1)))
    raise ValueError(kind)


@functools.cache
def count(a):
    kind, l, r = nodes[a]
    if kind in ("val", "var"):
        return 1
    if kind == "ln":
        return count(l)
    return count(l) + count(r)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--iterations", type=int, required=True)
    args = parser.parse_args()
    if not 0 <= args.iterations <= 10:
        parser.error("iterations must be between 0 and the upstream full size 10")
    x = node("var", "x")
    expression = power(x, x)
    rows = []
    for i in range(args.iterations):
        expression = derivative(expression)
        rows.append({"iteration": i + 1, "leaves": count(expression),
                     "minimum_integer": minimum, "maximum_integer": maximum,
                     "interned_nodes": len(nodes), "constant_powers": constant_powers})
    print(json.dumps({"iterations": args.iterations, "rows": rows}, indent=2))


if __name__ == "__main__":
    main()
