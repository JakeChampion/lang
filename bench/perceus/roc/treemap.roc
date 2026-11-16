# A map over a tree that rebuilds each node. A tree with one count is rebuilt
# in place.
app [main!] { pf: platform "https://github.com/roc-lang/basic-cli/releases/download/0.20.0/X73hGh05nNTkDHU06FHC0YfFaQB1pimX7gncRcao5mU.tar.br" }

import pf.Stdout
import pf.Arg

arg_or : List Arg.Arg, I64 -> I64
arg_or = |raw_args, default|
    args = List.map(raw_args, Arg.display)
    when List.get(args, 1) is
        Ok(s) -> Str.to_i64(s) |> Result.with_default(default)
        Err(_) -> default

Tree : [Nil, Node I64 Tree Tree]

main! = |raw_args|
    n = arg_or(raw_args, 20)
    Stdout.line!(Num.to_str(total(rounds(make(1, n), 20))))

make : I64, I64 -> Tree
make = |item, depth|
    if depth == 0 then
        Node(item, Nil, Nil)
    else
        Node(item, make((2 * item - 1), (depth - 1)), make((2 * item), (depth - 1)))

bump : Tree -> Tree
bump = |t|
    when t is
        Node(item, l, r) -> Node((item + 1), bump(l), bump(r))
        Nil -> Nil

total : Tree -> I64
total = |t|
    when t is
        Node(item, l, r) -> item + total(l) + total(r)
        Nil -> 0

rounds : Tree, I64 -> Tree
rounds = |t, k|
    if k <= 0 then
        t
    else
        rounds(bump(t), (k - 1))
