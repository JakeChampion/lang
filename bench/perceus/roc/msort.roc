# A merge sort of a list of pseudo-random integers.
app [main!] { pf: platform "https://github.com/roc-lang/basic-cli/releases/download/0.20.0/X73hGh05nNTkDHU06FHC0YfFaQB1pimX7gncRcao5mU.tar.br" }

import pf.Stdout
import pf.Arg

arg_or : List Arg.Arg, I64 -> I64
arg_or = |raw_args, default|
    args = List.map(raw_args, Arg.display)
    when List.get(args, 1) is
        Ok(s) -> Str.to_i64(s) |> Result.with_default(default)
        Err(_) -> default

IList : [INil, ICons I64 IList]

main! = |raw_args|
    n = arg_or(raw_args, 1000000)
    (count, total) = digest(sort(make(n, 42)), 0, 0)
    Stdout.line!("${Num.to_str(count)} ${Num.to_str(total)}")

make : I64, I64 -> IList
make = |n, seed|
    if n <= 0 then
        INil
    else
        next = (1103515245 * seed + 12345) % 2147483648
        ICons((next % 1000000), make((n - 1), next))

split : IList, IList, IList -> (IList, IList)
split = |xs, a, b|
    when xs is
        ICons(x, rest) -> split(rest, b, ICons(x, a))
        INil -> (a, b)

merge : IList, IList -> IList
merge = |xs, ys|
    when xs is
        INil -> ys
        ICons(x, xt) ->
            when ys is
                INil -> xs
                ICons(y, yt) ->
                    if x <= y then
                        ICons(x, merge(xt, ys))
                    else
                        ICons(y, merge(xs, yt))

sort : IList -> IList
sort = |xs|
    when xs is
        INil -> INil
        ICons(_, INil) -> xs
        _ ->
            (a, b) = split(xs, INil, INil)
            merge(sort(a), sort(b))

digest : IList, I64, I64 -> (I64, I64)
digest = |xs, i, total|
    when xs is
        ICons(x, rest) -> digest(rest, (i + 1), (total + (i + 1) * x))
        INil -> (i, total)
