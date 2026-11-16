# A persistent red-black map whose key is two words, compared in order.
app [main!] { pf: platform "https://github.com/roc-lang/basic-cli/releases/download/0.20.0/X73hGh05nNTkDHU06FHC0YfFaQB1pimX7gncRcao5mU.tar.br" }

import pf.Stdout
import pf.Arg

arg_or : List Arg.Arg, I64 -> I64
arg_or = |raw_args, default|
    args = List.map(raw_args, Arg.display)
    when List.get(args, 1) is
        Ok(s) -> Str.to_i64(s) |> Result.with_default(default)
        Err(_) -> default

Key : { hi : I64, lo : I64 }

Color : [Red, Black]

PMap : [Leaf, Node Color PMap Key I64 PMap]

main! = |raw_args|
    n = arg_or(raw_args, 1000000)
    m = build(Leaf, 0, n, 42)
    (hits, sum) = look(m, 0, n, 42, 0, 0)
    (other_hits, other_sum) = look(m, 0, n, 7, 0, 0)
    Stdout.line!("${Num.to_str(hits)} ${Num.to_str(sum)} ${Num.to_str(other_hits)} ${Num.to_str(other_sum)}")

compare_key : Key, Key -> I64
compare_key = |a, b|
    if a.hi != b.hi then
        if a.hi < b.hi then -1 else 1
    else if a.lo != b.lo then
        if a.lo < b.lo then -1 else 1
    else
        0

is_red : PMap -> Bool
is_red = |t|
    when t is
        Node(Red, _, _, _, _) -> Bool.true
        _ -> Bool.false

balance_left : PMap, Key, I64, PMap -> PMap
balance_left = |l, k, v, r|
    when l is
        Node(_, Node(Red, lx, kx, vx, rx), ky, vy, ry) ->
            Node(Red, Node(Black, lx, kx, vx, rx), ky, vy, Node(Black, ry, k, v, r))

        Node(_, ly, ky, vy, Node(Red, lx, kx, vx, rx)) ->
            Node(Red, Node(Black, ly, ky, vy, lx), kx, vx, Node(Black, rx, k, v, r))

        Node(_, lx, kx, vx, rx) ->
            Node(Black, Node(Red, lx, kx, vx, rx), k, v, r)

        Leaf -> Leaf

balance_right : PMap, Key, I64, PMap -> PMap
balance_right = |l, k, v, r|
    when r is
        Node(_, Node(Red, lx, kx, vx, rx), ky, vy, ry) ->
            Node(Red, Node(Black, l, k, v, lx), kx, vx, Node(Black, rx, ky, vy, ry))

        Node(_, lx, kx, vx, Node(Red, ly, ky, vy, ry)) ->
            Node(Red, Node(Black, l, k, v, lx), kx, vx, Node(Black, ly, ky, vy, ry))

        Node(_, lx, kx, vx, rx) ->
            Node(Black, l, k, v, Node(Red, lx, kx, vx, rx))

        Leaf -> Leaf

ins : PMap, Key, I64 -> PMap
ins = |t, k, v|
    when t is
        Node(Red, l, kx, vx, r) ->
            c = compare_key(k, kx)
            if c < 0 then
                Node(Red, ins(l, k, v), kx, vx, r)
            else if c > 0 then
                Node(Red, l, kx, vx, ins(r, k, v))
            else
                Node(Red, l, k, v, r)

        Node(Black, l, kx, vx, r) ->
            c = compare_key(k, kx)
            if c < 0 then
                if is_red(l) then
                    balance_left(ins(l, k, v), kx, vx, r)
                else
                    Node(Black, ins(l, k, v), kx, vx, r)
            else if c > 0 then
                if is_red(r) then
                    balance_right(l, kx, vx, ins(r, k, v))
                else
                    Node(Black, l, kx, vx, ins(r, k, v))
            else
                Node(Black, l, k, v, r)

        Leaf -> Node(Red, Leaf, k, v, Leaf)

set_black : PMap -> PMap
set_black = |t|
    when t is
        Node(_, l, k, v, r) -> Node(Black, l, k, v, r)
        Leaf -> Leaf

find : PMap, Key -> I64
find = |t, k|
    when t is
        Node(_, l, kx, vx, r) ->
            c = compare_key(k, kx)
            if c < 0 then
                find(l, k)
            else if c > 0 then
                find(r, k)
            else
                vx

        Leaf -> -1

next : I64 -> I64
next = |x| (1103515245 * x + 12345) % 2147483648

key_of : I64 -> Key
key_of = |x| { hi: x % 1024, lo: x // 1024 }

build : PMap, I64, I64, I64 -> PMap
build = |m, i, n, x|
    if i >= n then
        m
    else
        y = next(x)
        build(set_black(ins(m, key_of(y), i)), (i + 1), n, y)

look : PMap, I64, I64, I64, I64, I64 -> (I64, I64)
look = |m, i, n, x, hits, sum|
    if i >= n then
        (hits, sum)
    else
        y = next(x)
        v = find(m, key_of(y))
        if v >= 0 then
            look(m, (i + 1), n, y, (hits + 1), (sum + v))
        else
            look(m, (i + 1), n, y, hits, sum)
