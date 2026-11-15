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

Expr : [Var I64, Val I64, Add Expr Expr, Mul Expr Expr]

main! = |raw_args|
    n = arg_or(raw_args, 20)
    e = mk_expr(n, 1)
    before = eval(e)
    after = eval(cfold(reassoc(e)))
    Stdout.line!(Num.to_str(before))?
    Stdout.line!(Num.to_str(after))

mk_expr : I64, I64 -> Expr
mk_expr = |n, v|
    if n == 0 then
        if v == 0 then Var(1) else Val(v)
    else
        previous = if v - 1 < 0 then 0 else v - 1
        Add(mk_expr((n - 1), (v + 1)), mk_expr((n - 1), previous))

append_add : Expr, Expr -> Expr
append_add = |e0, e3|
    when e0 is
        Add(e1, e2) -> Add(e1, append_add(e2, e3))
        _ -> Add(e0, e3)

append_mul : Expr, Expr -> Expr
append_mul = |e0, e3|
    when e0 is
        Mul(e1, e2) -> Mul(e1, append_mul(e2, e3))
        _ -> Mul(e0, e3)

reassoc : Expr -> Expr
reassoc = |e|
    when e is
        Add(e1, e2) -> append_add(reassoc(e1), reassoc(e2))
        Mul(e1, e2) -> append_mul(reassoc(e1), reassoc(e2))
        _ -> e

cfold : Expr -> Expr
cfold = |e|
    when e is
        Add(e1, e2) ->
            left = cfold(e1)
            right = cfold(e2)
            when left is
                Val(a) ->
                    when right is
                        Val(b) -> Val(Num.add_wrap(a, b))
                        Add(f, Val(b)) -> Add(Val(Num.add_wrap(a, b)), f)
                        Add(Val(b), f) -> Add(Val(Num.add_wrap(a, b)), f)
                        _ -> Add(left, right)

                _ -> Add(left, right)

        Mul(e1, e2) ->
            left = cfold(e1)
            right = cfold(e2)
            when left is
                Val(a) ->
                    when right is
                        Val(b) -> Val(Num.mul_wrap(a, b))
                        Mul(f, Val(b)) -> Mul(Val(Num.mul_wrap(a, b)), f)
                        Mul(Val(b), f) -> Mul(Val(Num.mul_wrap(a, b)), f)
                        _ -> Mul(left, right)

                _ -> Mul(left, right)

        _ -> e

eval : Expr -> I64
eval = |e|
    when e is
        Var(_) -> 0
        Val(v) -> v
        Add(l, r) -> Num.add_wrap(eval(l), eval(r))
        Mul(l, r) -> Num.mul_wrap(eval(l), eval(r))
