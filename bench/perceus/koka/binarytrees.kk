// The binary-trees workload of the Computer Language Benchmarks Game, in the
// form where each node holds an item.
module binarytrees
import std/os/env

type tree
  Nil
  Node( item : int, left : tree, right : tree )

fun make( item : int, depth : int ) : div tree
  if depth == 0 then Node(item, Nil, Nil)
  else Node(item, make(2 * item - 1, depth - 1), make(2 * item, depth - 1))

fun check( t : tree ) : div int
  match t
    Node(item, l, r) -> item + check(l) - check(r)
    Nil              -> 0

fun sweep( i : int, iterations : int, depth : int, total : int ) : div int
  if i > iterations then total
  else sweep(i + 1, iterations, depth, total + check(make(i, depth)) + check(make(0 - i, depth)))

fun levels( depth : int, low : int, high : int ) : <console,div> ()
  if depth <= high then
    val iterations = pow(2, high - depth + low)
    println((2 * iterations).show ++ " trees of depth " ++ depth.show ++ " check: " ++ sweep(1, iterations, depth, 0).show)
    levels(depth + 2, low, high)

pub fun main()
  val n = get-args().head("").parse-int.default(21)
  val low = 4
  val high = if n < low + 2 then low + 2 else n
  println("stretch tree of depth " ++ (high + 1).show ++ " check: " ++ check(make(0, high + 1)).show)
  val long-lived = make(0, high)
  levels(low, low, high)
  println("long lived tree of depth " ++ high.show ++ " check: " ++ check(long-lived).show)
