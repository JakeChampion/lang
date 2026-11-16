// A quick sort of a list of pseudo-random integers, with the head as the pivot.
module qsort
import std/os/env

type ilist
  INil
  ICons( head : int, tail : ilist )

fun make( n : int, seed : int ) : div ilist
  if n <= 0 then INil
  else
    val next = (1103515245 * seed + 12345) % 2147483648
    ICons(next % 1000000, make(n - 1, next))

fun partition( p : int, xs : ilist, lo : ilist, hi : ilist ) : div (ilist, ilist)
  match xs
    INil -> (lo, hi)
    ICons(x, rest) -> if x < p then partition(p, rest, ICons(x, lo), hi) else partition(p, rest, lo, ICons(x, hi))

fun append( xs : ilist, ys : ilist ) : div ilist
  match xs
    ICons(x, rest) -> ICons(x, append(rest, ys))
    INil           -> ys

fun sort( xs : ilist ) : div ilist
  match xs
    INil -> INil
    ICons(p, rest) ->
      val (lo, hi) = partition(p, rest, INil, INil)
      append(sort(lo), ICons(p, sort(hi)))

fun digest( xs : ilist, i : int, total : int ) : div (int, int)
  match xs
    ICons(x, rest) -> digest(rest, i + 1, total + (i + 1) * x)
    INil           -> (i, total)

pub fun main()
  val n = get-args().head("").parse-int.default(1000000)
  val (count, total) = digest(sort(make(n, 42)), 0, 0)
  println(count.show ++ " " ++ total.show)
