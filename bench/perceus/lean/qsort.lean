-- A quick sort of a list of pseudo-random integers, with the head as the pivot.
inductive IList where
  | nil : IList
  | cons : Int → IList → IList

def make : Nat → Int → IList
  | 0, _ => .nil
  | n + 1, seed =>
    let next := (1103515245 * seed + 12345) % 2147483648
    .cons (next % 1000000) (make n next)

def partition (p : Int) : IList → IList → IList → IList × IList
  | .nil, lo, hi => (lo, hi)
  | .cons x rest, lo, hi => if x < p then partition p rest (.cons x lo) hi else partition p rest lo (.cons x hi)

def append : IList → IList → IList
  | .cons x rest, ys => .cons x (append rest ys)
  | .nil, ys => ys

partial def sort : IList → IList
  | .nil => .nil
  | .cons p rest =>
    let (lo, hi) := partition p rest .nil .nil
    append (sort lo) (.cons p (sort hi))

def digest : IList → Int → Int → Int × Int
  | .cons x rest, i, total => digest rest (i + 1) (total + (i + 1) * x)
  | .nil, i, total => (i, total)

def main (args : List String) : IO UInt32 := do
  let n := (args.headD "1000000").toNat!
  let (count, total) := digest (sort (make n 42)) 0 0
  IO.println s!"{count} {total}"
  return 0
