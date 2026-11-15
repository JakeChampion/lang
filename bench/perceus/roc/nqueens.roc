# Ported from the Koka input of this benchmark. Apache-2.0.
app [main!] { pf: platform "https://github.com/roc-lang/basic-cli/releases/download/0.20.0/X73hGh05nNTkDHU06FHC0YfFaQB1pimX7gncRcao5mU.tar.br" }

import pf.Stdout
import pf.Arg

ConsList a : [Nil, Cons a (ConsList a)]

main! = |raw_args|
    args = List.map(raw_args, Arg.display)
    n : I64
    n =
        when List.get(args, 1) is
            Ok(s) -> Str.to_i64(s) |> Result.with_default(13)
            Err(_) -> 13
    Stdout.line!(Num.to_str(queens(n)))

queens : I64 -> I64
queens = |n| length(find_solutions(n, n))

find_solutions : I64, I64 -> ConsList (ConsList I64)
find_solutions = |n, k|
    if k <= 0 then
        Cons(Nil, Nil)
    else
        extend(n, Nil, find_solutions(n, (k - 1)))

extend : I64, ConsList (ConsList I64), ConsList (ConsList I64) -> ConsList (ConsList I64)
extend = |n, acc, solutions|
    when solutions is
        Nil -> acc
        Cons(soln, rest) -> extend(n, append_safe(n, soln, acc), rest)

append_safe : I64, ConsList I64, ConsList (ConsList I64) -> ConsList (ConsList I64)
append_safe = |k, soln, solns|
    if k <= 0 then
        solns
    else if safe(k, 1, soln) then
        append_safe((k - 1), soln, Cons(Cons(k, soln), solns))
    else
        append_safe((k - 1), soln, solns)

safe : I64, I64, ConsList I64 -> Bool
safe = |queen, diagonal, xs|
    when xs is
        Nil -> Bool.true
        Cons(q, t) ->
            if queen != q and queen != q + diagonal and queen != q - diagonal then
                safe(queen, (diagonal + 1), t)
            else
                Bool.false

length : ConsList a -> I64
length = |xs| length_help(xs, 0)

length_help : ConsList a, I64 -> I64
length_help = |xs, acc|
    when xs is
        Nil -> acc
        Cons(_, rest) -> length_help(rest, (1 + acc))
