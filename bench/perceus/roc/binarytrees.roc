# The binary-trees workload of the Computer Language Benchmarks Game, in the
# form where each node holds an item.
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
    n = arg_or(raw_args, 21)
    low = 4
    high = if n < low + 2 then low + 2 else n
    Stdout.line!("stretch tree of depth ${Num.to_str(high + 1)} check: ${Num.to_str(check(make(0, (high + 1))))}")?
    long_lived = make(0, high)
    levels!(low, low, high)?
    Stdout.line!("long lived tree of depth ${Num.to_str(high)} check: ${Num.to_str(check(long_lived))}")

levels! = |depth, low, high|
    if depth > high then
        Ok({})
    else
        iterations = Num.shift_left_by(1, Num.to_u8(high - depth + low))
        Stdout.line!("${Num.to_str(2 * iterations)} trees of depth ${Num.to_str(depth)} check: ${Num.to_str(sweep(1, iterations, depth, 0))}")?
        levels!((depth + 2), low, high)

make : I64, I64 -> Tree
make = |item, depth|
    if depth == 0 then
        Node(item, Nil, Nil)
    else
        Node(item, make((2 * item - 1), (depth - 1)), make((2 * item), (depth - 1)))

check : Tree -> I64
check = |t|
    when t is
        Node(item, l, r) -> item + check(l) - check(r)
        Nil -> 0

sweep : I64, I64, I64, I64 -> I64
sweep = |i, iterations, depth, total|
    if i > iterations then
        total
    else
        sweep((i + 1), iterations, depth, (total + check(make(i, depth)) + check(make((0 - i), depth))))
