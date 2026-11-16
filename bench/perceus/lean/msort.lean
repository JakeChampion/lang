-- A merge sort of a list of pseudo-random integers.
inductive IList where
  | nil : IList
  | cons : Int → IList → IList

def make : Nat → Int → IList
  | 0, _ => .nil
  | n + 1, seed =>
    let next := (1103515245 * seed + 12345) % 2147483648
    .cons (next % 1000000) (make n next)

def split : IList → IList → IList → IList × IList
  | .cons x rest, a, b => split rest b (.cons x a)
  | .nil, a, b => (a, b)

partial def merge : IList → IList → IList
  | .nil, ys => ys
  | xs, .nil => xs
  | .cons x xt, .cons y yt =>
    if x <= y then .cons x (merge xt (.cons y yt)) else .cons y (merge (.cons x xt) yt)

partial def sort : IList → IList
  | .nil => .nil
  | .cons x .nil => .cons x .nil
  | xs =>
    let (a, b) := split xs .nil .nil
    merge (sort a) (sort b)

def digest : IList → Int → Int → Int × Int
  | .cons x rest, i, total => digest rest (i + 1) (total + (i + 1) * x)
  | .nil, i, total => (i, total)

def main (args : List String) : IO UInt32 := do
  let n := (args.headD "1000000").toNat!
  let (count, total) := digest (sort (make n 42)) 0 0
  IO.println s!"{count} {total}"
  return 0
