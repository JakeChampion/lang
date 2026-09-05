# #8210 and #8119 were one defect: the wasm struct-drop string[] arm

`TestSelfHostWasmWholeCompilerShardedLink` failed with the wasm-hosted compiler
emitting a function name as two binary bytes (`(func $^D^@`). Bisected to
`4c168d99b` (#8119) — the commit that moved `emit_wasm_struct_drop_body` onto
`asmcore.struct_drop_field_ops` — which fixed it a day before #8210 was filed,
under a different symptom.

## The mechanism

wasm carried its own `__struct_drop_<T>` field classifier, and it routed every
`string[]` field to `$__fern_arr_dec_ptr` unconditionally, where the register
backends' shared classifier takes the deep arm only under the `strfldok:arr:<T>`
admission. `$__fern_arr_dec_ptr` walks the elements when the buffer is at its
last reference, so:

```
closure_ret_fns_of:
  gfns  = [fn.name for fn in funcs]   # elements BORROWED from the AST, uncounted
  gset  = sig_reg_of(gfns)            # SigReg.rows retains the buffer -> rc 2
  ...
  exit:  dec gfns              -> rc 1
         __struct_drop_SigReg  -> arr_dec_ptr(rows) at rc 1: frees every element
```

The freed elements are the `FuncDecl.name` strings the parsed AST still owns.
The size-class freelist hands the blocks back on the next allocation, so a
function name later read as a recycled block's header: `[len=2][cap=4]` reads
as the two-byte string `\x04\x00`, which is exactly what landed in the WAT.

Two names for one array is what made it visible, not what made it wrong — the
same ungated arm cost #8119 3.8 GB of linear memory on `function main(): i32 {
return 0; }` through the compiler's own `FnSigs` base copy. x86-64 and arm64
were correct throughout: their classifier already required the admission, which
is why the whole-compiler x86-64 emit was byte-identical with and without the
shape.

## Reproducer

`strarr-field-caller-array-co-owner` in
`internal/e2eselfhost/self_host_strarr_field_buffer_release_test.go`. 30 ms per
leg, against ~2.5 min for one whole-compiler emit + link + hosted run. Measured
on `29f56065e` (the commit before the fix): wasm exits 97 (six of ten labels
corrupted), x86-64 exits 0.

## Trap: the over-release counter reads 0 here

`__rc_underflow()` is clean on the failing program, on purpose. The elements
were uncounted borrows sitting at rc 1, so the deep walk's dec on each is a
*legitimate-looking* free, not a decrement past zero. The detector is the wrong
oracle for an ungated deep release; the reachable-value comparison after a churn
is the one that sees it.
