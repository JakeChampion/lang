# Ported from the Koka input of this benchmark. Apache-2.0.
app [main!] { pf: platform "https://github.com/roc-lang/basic-cli/releases/download/0.20.0/X73hGh05nNTkDHU06FHC0YfFaQB1pimX7gncRcao5mU.tar.br" }

import pf.Stdout
import pf.Arg

arg_or : List Arg.Arg, I64 -> I64
arg_or = |raw_args, default|
    args = List.map(raw_args, Arg.display)
    when List.get(args, 1) is
        Ok(s) -> Str.to_i64(s) |> Result.with_default(default)
        Err(_) -> default

Expr : [Val I64, Var Str, Add Expr Expr, Mul Expr Expr, Pow Expr Expr, Ln Expr]

main! = |raw_args|
    n = arg_or(raw_args, 10)
    x = Var("x")
    steps!(n, n, powr(x, x))?
    Stdout.line!("done")

steps! = |s, n, e|
    if n == 0 then
        Ok({})
    else
        next = d("x", e)
        Stdout.line!("${Num.to_str(s - n + 1)} count: ${Num.to_str(count(next))}")?
        steps!(s, (n - 1), next)

pown : I64, I64 -> I64
pown = |a, b|
    if b == 0 then
        1
    else if a == 0 or a == 1 then
        a
    else if a == -1 then
        if b % 2 == 0 then 1 else -1
    else if b < 0 then
        0
    else
        pown_loop(a, b, 1)

pown_loop : I64, I64, I64 -> I64
pown_loop = |a, b, result|
    if b <= 0 then
        result
    else if b % 2 != 0 then
        r2 = Num.mul_wrap(result, a)
        b2 = b - 1
        if b2 > 0 then pown_loop(Num.mul_wrap(a, a), (b2 // 2), r2) else r2
    else
        pown_loop(Num.mul_wrap(a, a), (b // 2), result)

add : Expr, Expr -> Expr
add = |left, right|
    when (left, right) is
        (Val(n), Val(m)) -> Val(Num.add_wrap(n, m))
        (Val(0), _) -> right
        (_, Val(0)) -> left
        (_, Val(m)) -> add(Val(m), left)
        (Val(n), Add(Val(m), f)) -> add(Val(Num.add_wrap(n, m)), f)
        (_, Add(Val(m), f)) -> add(Val(m), add(left, f))
        (Add(f, g), _) -> add(f, add(g, right))
        _ -> Add(left, right)

mul : Expr, Expr -> Expr
mul = |left, right|
    when (left, right) is
        (Val(n), Val(m)) -> Val(Num.mul_wrap(n, m))
        (Val(0), _) -> Val(0)
        (_, Val(0)) -> Val(0)
        (Val(1), _) -> right
        (_, Val(1)) -> left
        (_, Val(m)) -> mul(Val(m), left)
        (Val(n), Mul(Val(m), f)) -> mul(Val(Num.mul_wrap(n, m)), f)
        (_, Mul(Val(m), f)) -> mul(Val(m), mul(left, f))
        (Mul(f, g), _) -> mul(f, mul(g, right))
        _ -> Mul(left, right)

powr : Expr, Expr -> Expr
powr = |left, right|
    when (left, right) is
        (Val(m), Val(n)) -> Val(pown(m, n))
        (_, Val(0)) -> Val(1)
        (_, Val(1)) -> left
        (Val(0), _) -> Val(0)
        _ -> Pow(left, right)

ln : Expr -> Expr
ln = |e|
    when e is
        Val(1) -> Val(0)
        _ -> Ln(e)

d : Str, Expr -> Expr
d = |x, e|
    when e is
        Val(_) -> Val(0)
        Var(y) -> if x == y then Val(1) else Val(0)
        Add(f, g) -> add(d(x, f), d(x, g))
        Mul(f, g) -> add(mul(f, d(x, g)), mul(g, d(x, f)))
        Pow(f, g) -> mul(powr(f, g), add(mul(mul(g, d(x, f)), powr(f, Val(-1))), mul(ln(f), d(x, g))))
        Ln(f) -> mul(d(x, f), powr(f, Val(-1)))

count : Expr -> I64
count = |e|
    when e is
        Val(_) -> 1
        Var(_) -> 1
        Add(f, g) -> count(f) + count(g)
        Mul(f, g) -> count(f) + count(g)
        Pow(f, g) -> count(f) + count(g)
        Ln(f) -> count(f)
