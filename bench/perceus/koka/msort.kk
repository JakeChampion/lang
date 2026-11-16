// A merge sort of a list of pseudo-random integers.
module msort
import std/os/env

type ilist
  INil
  ICons( head : int, tail : ilist )

fun make( n : int, seed : int ) : div ilist
  if n <= 0 then INil
  else
    val next = (1103515245 * seed + 12345) % 2147483648
    ICons(next % 1000000, make(n - 1, next))

fun split( xs : ilist, a : ilist, b : ilist ) : div (ilist, ilist)
  match xs
    ICons(x, rest) -> split(rest, b, ICons(x, a))
    INil           -> (a, b)

fun merge( xs : ilist, ys : ilist ) : div ilist
  match xs
    INil -> ys
    ICons(x, xt) -> match ys
      INil -> xs
      ICons(y, yt) -> if x <= y then ICons(x, merge(xt, ys)) else ICons(y, merge(xs, yt))

fun sort( xs : ilist ) : div ilist
  match xs
    INil -> INil
    ICons(_, INil) -> xs
    _ ->
      val (a, b) = split(xs, INil, INil)
      merge(sort(a), sort(b))

fun digest( xs : ilist, i : int, total : int ) : div (int, int)
  match xs
    ICons(x, rest) -> digest(rest, i + 1, total + (i + 1) * x)
    INil           -> (i, total)

pub fun main()
  val n = get-args().head("").parse-int.default(1000000)
  val (count, total) = digest(sort(make(n, 42)), 0, 0)
  println(count.show ++ " " ++ total.show)
