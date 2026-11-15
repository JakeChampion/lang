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

Tree : [Leaf, Node Color Tree I32 Bool Tree]

main! = |raw_args|
    n = arg_or(raw_args, 4200000)
    Stdout.line!(Num.to_str(count_true(make_tree(Num.to_i32(n)))))

is_red : Tree -> Bool
is_red = |t|
    when t is
        Node(Red, _, _, _, _) -> Bool.true
        _ -> Bool.false

balance_left : Tree, I32, Bool, Tree -> Tree
balance_left = |l, k, v, r|
    when l is
        Node(_, Node(Red, lx, kx, vx, rx), ky, vy, ry) ->
            Node(Red, Node(Black, lx, kx, vx, rx), ky, vy, Node(Black, ry, k, v, r))

        Node(_, ly, ky, vy, Node(Red, lx, kx, vx, rx)) ->
            Node(Red, Node(Black, ly, ky, vy, lx), kx, vx, Node(Black, rx, k, v, r))

        Node(_, lx, kx, vx, rx) ->
            Node(Black, Node(Red, lx, kx, vx, rx), k, v, r)

        Leaf -> Leaf

balance_right : Tree, I32, Bool, Tree -> Tree
balance_right = |l, k, v, r|
    when r is
        Node(_, Node(Red, lx, kx, vx, rx), ky, vy, ry) ->
            Node(Red, Node(Black, l, k, v, lx), kx, vx, Node(Black, rx, ky, vy, ry))

        Node(_, lx, kx, vx, Node(Red, ly, ky, vy, ry)) ->
            Node(Red, Node(Black, l, k, v, lx), kx, vx, Node(Black, ly, ky, vy, ry))

        Node(_, lx, kx, vx, rx) ->
            Node(Black, l, k, v, Node(Red, lx, kx, vx, rx))

        Leaf -> Leaf

ins : Tree, I32, Bool -> Tree
ins = |t, k, v|
    when t is
        Node(Red, l, kx, vx, r) ->
            if k < kx then
                Node(Red, ins(l, k, v), kx, vx, r)
            else if k > kx then
                Node(Red, l, kx, vx, ins(r, k, v))
            else
                Node(Red, l, k, v, r)

        Node(Black, l, kx, vx, r) ->
            if k < kx then
                if is_red(l) then
                    balance_left(ins(l, k, v), kx, vx, r)
                else
                    Node(Black, ins(l, k, v), kx, vx, r)
            else if k > kx then
                if is_red(r) then
                    balance_right(l, kx, vx, ins(r, k, v))
                else
                    Node(Black, l, kx, vx, ins(r, k, v))
            else
                Node(Black, l, k, v, r)

        Leaf -> Node(Red, Leaf, k, v, Leaf)

set_black : Tree -> Tree
set_black = |t|
    when t is
        Node(_, l, k, v, r) -> Node(Black, l, k, v, r)
        Leaf -> Leaf

insert : Tree, I32, Bool -> Tree
insert = |t, k, v| set_black(ins(t, k, v))

fold : Tree, I64, (I32, Bool, I64 -> I64) -> I64
fold = |t, b, f|
    when t is
        Node(_, l, k, v, r) -> fold(r, f(k, v, fold(l, b, f)), f)
        Leaf -> b

make_tree_aux : I32, Tree -> Tree
make_tree_aux = |n, t|
    if n <= 0 then
        t
    else
        n1 = n - 1
        make_tree_aux(n1, insert(t, n1, n1 % 10 == 0))

make_tree : I32 -> Tree
make_tree = |n| make_tree_aux(n, Leaf)

count_true : Tree -> I64
count_true = |t| fold(t, 0, |_, v, r| if v then r + 1 else r)
