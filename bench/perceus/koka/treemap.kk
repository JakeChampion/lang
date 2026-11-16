// A map over a tree that rebuilds each node. A tree with one count is rebuilt
// in place.
module treemap
import std/os/env

type tree
  Nil
  Node( item : int, left : tree, right : tree )

fun make( item : int, depth : int ) : div tree
  if depth == 0 then Node(item, Nil, Nil)
  else Node(item, make(2 * item - 1, depth - 1), make(2 * item, depth - 1))

fun bump( t : tree ) : div tree
  match t
    Node(item, l, r) -> Node(item + 1, bump(l), bump(r))
    Nil              -> Nil

fun total( t : tree ) : div int
  match t
    Node(item, l, r) -> item + total(l) + total(r)
    Nil              -> 0

fun rounds( t : tree, k : int ) : div tree
  if k <= 0 then t else rounds(bump(t), k - 1)

pub fun main()
  val n = get-args().head("").parse-int.default(20)
  println(total(rounds(make(1, n), 20)).show)
