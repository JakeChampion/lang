-- Direct translation of the pinned Koka workload; no upstream Lean file exists.
abbrev Solution := List Int32
abbrev Solutions := List Solution

def safe (queen diag : Int32) : Solution → Bool
| [] => true
| q :: qs => queen != q && queen != q + diag && queen != q - diag && safe queen (diag + 1) qs

partial def appendSafe (queen : Int32) (xs : Solution) (xss : Solutions) : Solutions :=
  if queen <= 0 then xss
  else if safe queen 1 xs then appendSafe (queen - 1) xs ((queen :: xs) :: xss)
  else appendSafe (queen - 1) xs xss

def extend (queen : Int32) (acc : Solutions) : Solutions → Solutions
| [] => acc
| xs :: rest => extend queen (appendSafe queen xs acc) rest

partial def findSolutions (n queen : Int32) : Solutions :=
  if queen == 0 then [[]]
  else extend n [] (findSolutions n (queen - 1))

def main (args : List String) : IO UInt32 := do
  let n := Int32.ofInt (Int.ofNat ((args.headD "13").toNat!))
  IO.println ((findSolutions n n).length)
  return 0
