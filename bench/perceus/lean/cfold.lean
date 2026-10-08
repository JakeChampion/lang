-- Direct translation of the pinned Koka workload; no upstream Lean file exists.
inductive Expr
| Var : Int → Expr
| Val : Int → Expr
| Add : Expr → Expr → Expr
| Mul : Expr → Expr → Expr
open Expr

def mkExpr : Nat → Int → Expr
| 0, v => if v == 0 then Var 1 else Val v
| n+1, v => Expr.Add (mkExpr n (v+1)) (mkExpr n (max (v-1) 0))

def appendAdd : Expr → Expr → Expr
| Expr.Add a b, c => Expr.Add a (appendAdd b c)
| a, b => Expr.Add a b

def appendMul : Expr → Expr → Expr
| Expr.Mul a b, c => Expr.Mul a (appendMul b c)
| a, b => Expr.Mul a b

def reassoc : Expr → Expr
| Expr.Add a b => appendAdd (reassoc a) (reassoc b)
| Expr.Mul a b => appendMul (reassoc a) (reassoc b)
| e => e

def cfold : Expr → Expr
| Expr.Add a b =>
  let l := cfold a
  let r := cfold b
  match l with
  | Val x => match r with
    | Val y => Val (x+y)
    | Expr.Add f (Val y) => Expr.Add (Val (x+y)) f
    | Expr.Add (Val y) f => Expr.Add (Val (x+y)) f
    | _ => Expr.Add l r
  | _ => Expr.Add l r
| Expr.Mul a b =>
  let l := cfold a
  let r := cfold b
  match l with
  | Val x => match r with
    | Val y => Val (x*y)
    | Expr.Mul f (Val y) => Expr.Mul (Val (x*y)) f
    | Expr.Mul (Val y) f => Expr.Mul (Val (x*y)) f
    | _ => Expr.Mul l r
  | _ => Expr.Mul l r
| e => e

def eval : Expr → Int
| Var _ => 0
| Val v => v
| Expr.Add a b => eval a + eval b
| Expr.Mul a b => eval a * eval b

def main (args : List String) : IO UInt32 := do
  let n := (args.headD "20").toNat!
  let e := mkExpr n 1
  let v1 := eval e
  let v2 := eval (cfold (reassoc e))
  IO.println v1
  IO.println v2
  return 0
