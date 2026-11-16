// A persistent red-black map whose key is two words, compared in order.
module pmap
import std/os/env

struct key( hi : int, lo : int )

type color
  Red
  Black

type pmap
  Leaf
  Node( color : color, left : pmap, key : key, value : int, right : pmap )

fun compare-key( a : key, b : key ) : int
  if a.hi != b.hi then (if a.hi < b.hi then -1 else 1)
  elif a.lo != b.lo then (if a.lo < b.lo then -1 else 1)
  else 0

fun red-node( t : pmap ) : bool
  match t
    Node(Red) -> True
    _         -> False

fun balance-left( l : pmap, k : key, v : int, r : pmap ) : pmap
  match l
    Node(_, Node(Red, lx, kx, vx, rx), ky, vy, ry) -> Node(Red, Node(Black, lx, kx, vx, rx), ky, vy, Node(Black, ry, k, v, r))
    Node(_, ly, ky, vy, Node(Red, lx, kx, vx, rx)) -> Node(Red, Node(Black, ly, ky, vy, lx), kx, vx, Node(Black, rx, k, v, r))
    Node(_, lx, kx, vx, rx)                        -> Node(Black, Node(Red, lx, kx, vx, rx), k, v, r)
    Leaf                                           -> Leaf

fun balance-right( l : pmap, k : key, v : int, r : pmap ) : pmap
  match r
    Node(_, Node(Red, lx, kx, vx, rx), ky, vy, ry) -> Node(Red, Node(Black, l, k, v, lx), kx, vx, Node(Black, rx, ky, vy, ry))
    Node(_, lx, kx, vx, Node(Red, ly, ky, vy, ry)) -> Node(Red, Node(Black, l, k, v, lx), kx, vx, Node(Black, ly, ky, vy, ry))
    Node(_, lx, kx, vx, rx)                        -> Node(Black, l, k, v, Node(Red, lx, kx, vx, rx))
    Leaf                                           -> Leaf

fun ins( t : pmap, k : key, v : int ) : pmap
  match t
    Node(Red, l, kx, vx, r) ->
      val c = compare-key(k, kx)
      if c < 0 then Node(Red, ins(l, k, v), kx, vx, r)
      elif c > 0 then Node(Red, l, kx, vx, ins(r, k, v))
      else Node(Red, l, k, v, r)
    Node(Black, l, kx, vx, r) ->
      val c = compare-key(k, kx)
      if c < 0 then (if red-node(l) then balance-left(ins(l, k, v), kx, vx, r) else Node(Black, ins(l, k, v), kx, vx, r))
      elif c > 0 then (if red-node(r) then balance-right(l, kx, vx, ins(r, k, v)) else Node(Black, l, kx, vx, ins(r, k, v)))
      else Node(Black, l, k, v, r)
    Leaf -> Node(Red, Leaf, k, v, Leaf)

fun set-black( t : pmap ) : pmap
  match t
    Node(_, l, k, v, r) -> Node(Black, l, k, v, r)
    _                   -> t

fun insert( t : pmap, k : key, v : int ) : pmap
  set-black(ins(t, k, v))

fun find( t : pmap, k : key ) : div int
  match t
    Node(_, l, kx, vx, r) ->
      val c = compare-key(k, kx)
      if c < 0 then find(l, k) elif c > 0 then find(r, k) else vx
    Leaf -> -1

fun next( x : int ) : int
  (1103515245 * x + 12345) % 2147483648

fun key-of( x : int ) : key
  Key(x % 1024, x / 1024)

fun build( m : pmap, i : int, n : int, x : int ) : div pmap
  if i >= n then m
  else
    val y = next(x)
    build(insert(m, key-of(y), i), i + 1, n, y)

fun look( m : pmap, i : int, n : int, x : int, hits : int, sum : int ) : div (int, int)
  if i >= n then (hits, sum)
  else
    val y = next(x)
    val v = find(m, key-of(y))
    if v >= 0 then look(m, i + 1, n, y, hits + 1, sum + v) else look(m, i + 1, n, y, hits, sum)

pub fun main()
  val n = get-args().head("").parse-int.default(1000000)
  val m = build(Leaf, 0, n, 42)
  val (hits, sum) = look(m, 0, n, 42, 0, 0)
  val (other-hits, other-sum) = look(m, 0, n, 7, 0, 0)
  println(hits.show ++ " " ++ sum.show ++ " " ++ other-hits.show ++ " " ++ other-sum.show)
