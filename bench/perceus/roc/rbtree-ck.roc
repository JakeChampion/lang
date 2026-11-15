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

Color : [Red, Black]

Tree : [Leaf, Node Color Tree I64 Bool Tree]

Trees : [NoTrees, ConsT Tree Trees]

main! = |raw_args|
    n = arg_or(raw_args, 4200000)
    trees = make_tree(5, n)
    if length(trees, 0) != n // 5 + 1 then
        Stdout.line!("bad length")
    else
        Stdout.line!(Num.to_str(count_head(trees)))

is_red : Tree -> Bool
is_red = |t|
    when t is
        Node(Red, _, _, _, _) -> Bool.true
        _ -> Bool.false

balance1 : I64, Bool, Tree, Tree -> Tree
balance1 = |kv, vv, t, s|
    when s is
        Leaf -> Leaf
        Node(_, Node(Red, l, kx, vx, r1), ky, vy, r2) ->
            Node(Red, Node(Black, l, kx, vx, r1), ky, vy, Node(Black, r2, kv, vv, t))

        Node(_, l1, ky, vy, Node(Red, l2, kx, vx, r)) ->
            Node(Red, Node(Black, l1, ky, vy, l2), kx, vx, Node(Black, r, kv, vv, t))

        Node(_, l, ky, vy, r) ->
            Node(Black, Node(Red, l, ky, vy, r), kv, vv, t)

balance2 : Tree, I64, Bool, Tree -> Tree
balance2 = |t, kv, vv, s|
    when s is
        Leaf -> Leaf
        Node(_, Node(Red, l, kx1, vx1, r1), ky, vy, r2) ->
            Node(Red, Node(Black, t, kv, vv, l), kx1, vx1, Node(Black, r1, ky, vy, r2))

        Node(_, l1, ky, vy, Node(Red, l2, kx2, vx2, r2)) ->
            Node(Red, Node(Black, t, kv, vv, l1), ky, vy, Node(Black, l2, kx2, vx2, r2))

        Node(_, l, ky, vy, r) ->
            Node(Black, t, kv, vv, Node(Red, l, ky, vy, r))

ins : Tree, I64, Bool -> Tree
ins = |t, kx, vx|
    when t is
        Leaf -> Node(Red, Leaf, kx, vx, Leaf)
        Node(Red, l, ky, vy, r) ->
            if kx < ky then
                Node(Red, ins(l, kx, vx), ky, vy, r)
            else if kx == ky then
                Node(Red, l, kx, vx, r)
            else
                Node(Red, l, ky, vy, ins(r, kx, vx))

        Node(Black, l, ky, vy, r) ->
            if kx < ky then
                if is_red(l) then
                    balance1(ky, vy, r, ins(l, kx, vx))
                else
                    Node(Black, ins(l, kx, vx), ky, vy, r)
            else if kx == ky then
                Node(Black, l, kx, vx, r)
            else if is_red(r) then
                balance2(l, ky, vy, ins(r, kx, vx))
            else
                Node(Black, l, ky, vy, ins(r, kx, vx))

set_black : Tree -> Tree
set_black = |t|
    when t is
        Node(_, l, k, v, r) -> Node(Black, l, k, v, r)
        Leaf -> Leaf

insert : Tree, I64, Bool -> Tree
insert = |t, k, v|
    if is_red(t) then
        set_black(ins(t, k, v))
    else
        ins(t, k, v)

fold : Tree, I64, (I64, Bool, I64 -> I64) -> I64
fold = |t, b, f|
    when t is
        Leaf -> b
        Node(_, l, k, v, r) -> fold(r, f(k, v, fold(l, b, f)), f)

make_tree_aux : I64, I64, Tree, Trees -> Trees
make_tree_aux = |freq, n, t, acc|
    if n <= 0 then
        ConsT(t, acc)
    else
        next = insert(t, n, n % 10 == 0)
        if n % freq == 0 then
            make_tree_aux(freq, (n - 1), next, ConsT(next, acc))
        else
            make_tree_aux(freq, (n - 1), next, acc)

make_tree : I64, I64 -> Trees
make_tree = |freq, n| make_tree_aux(freq, n, Leaf, NoTrees)

count_true : Tree -> I64
count_true = |t| fold(t, 0, |_, v, r| if v then r + 1 else r)

count_head : Trees -> I64
count_head = |trees|
    when trees is
        ConsT(t, _) -> count_true(t)
        NoTrees -> 0

length : Trees, I64 -> I64
length = |trees, n|
    when trees is
        ConsT(_, rest) -> length(rest, (n + 1))
        NoTrees -> n
