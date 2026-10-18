# Fern Core — the instruction set

Status: normative index, gated by `TestSelfHostCoreSpecMatchesTheIR` in
`internal/testing/e2ecompiler`.

`grammar.ebnf` says what a Fern program looks like. `diagnostics.md`
says which programs are refused. `semantics.md` indexes the handful of
promises the policy docs make about the ones that are accepted. None of
them says what a Fern program *is*.

That definition, when it is written, will not be written over the
surface language. It will be written over **Fern Core** — and Fern Core
already exists: the stack IR in `compiler/ir.fern` is the target-agnostic
form every backend consumes (the SSA lift on x86-64 and arm64, `wasm_ir.fern`
on wasm), and the surface language is defined by lowering into it.
`docs/SPECIFICATION-RESEARCH.md` §Layer 3 lays out the three pieces that
need to follow, in order: the op set, its typing rules, and a small-step
semantics over a store.

**This file is the first of the three, and only the first.** It
enumerates the instructions and says what each one does to the operand
stack. It says nothing about what any of them *means*.

## Why the op set is worth writing down on its own

Because it is the part that is already true and is otherwise unreadable.
The instruction set exists as an id registry (`kind_name` / `kind_id`) and
a set of id-keyed classifiers in `compiler/ir.fern`, and a reader who wants
to know what `call_indirect` expects on the stack has to reconstruct it from
those and from three emitters. That is the same position the syntax was in
before `grammar.ebnf`: a description of *a* program, not of the language.

It is also the part a specification cannot start without. A typing rule
is a rule about an op, and a reduction rule reduces one — so the op set
is the vocabulary both are written in, and an op set with a hole in it
produces a semantics with a hole in it that nothing can detect.

## The stack model

Three uniformities of the lowering make the model a count of slots rather
than a typed stack:

- **Every value is one slot**, on every target. A string is a single box
  pointer on the register backends and a single i32 block pointer on wasm.
- **Every scope is void.** The lowering opens `block`, `loop` and `if` with
  block type 0 only; a value-producing `if` routes its result through a
  local, so a scope leaves the stack at the height it found it and a branch
  carries nothing.
- **Every function has exactly one result**, `void` ones included.

Every op that is an expression pushes exactly one slot — statement-position
ops push a dummy the caller drops. The exceptions are the ops that consume a
value outright (`store_local`, `drop`, `arr_set`, `struct_set`, `tuple_set`),
the `line` marker, and structured control flow.

The table deliberately says nothing about operand *kind*. Whether a slot
holds an integer or a float is a property of the op that produced it, and
the composite reads (`struct_get`, `tuple_get`, `arr_get`, `opt_payload`)
carry a width but not a class. `compiler/irverifystack.fern` makes the same
decision in code.

Scopes and branches follow wasm: `block` is a forward target, `loop` a
backward one, `if` runs its then-arm when the popped value is non-zero, and
`br` / `brif` address scopes by relative depth, 0 innermost.

## The instructions

One row per registered op kind, in id order. `Id` is the stable integer tag
the backends dispatch on; an id whose op was retired is not reused and has
no row. `Pops` is the number of slots the op consumes: `n` means the count
is the op's `i32_imm` (`arr_make`, `struct_make`, `tuple_make`,
`call_direct`), `n+1` that it is the immediate plus the funcref on top
(`call_indirect`), and `—` that the stack model does not cover the op, so
the verifier stops tracking the function at it rather than guess. `Pushes`
is the number of slots it leaves.

<!-- BEGIN ir_effects_run -->
| Op | Id | Pops | Pushes |
| --- | --- | --- | --- |
| `const_i32` | 1 | 0 | 1 |
| `const_i32_text` | 2 | 0 | 1 |
| `const_i64` | 3 | 0 | 1 |
| `const_f64` | 4 | 0 | 1 |
| `const_str` | 5 | 0 | 1 |
| `const_func` | 6 | 0 | 1 |
| `int_extend` | 7 | 1 | 1 |
| `int_wrap` | 8 | 1 | 1 |
| `int_cast` | 9 | 1 | 1 |
| `u32_wrap` | 10 | 1 | 1 |
| `i32_to_f64` | 11 | 1 | 1 |
| `f64_to_i32` | 12 | 1 | 1 |
| `f64_to_i64` | 13 | 1 | 1 |
| `f32_bits` | 14 | 1 | 1 |
| `f32_from_bits` | 15 | 1 | 1 |
| `f64_bits` | 16 | 1 | 1 |
| `f64_from_bits` | 17 | 1 | 1 |
| `load_local` | 18 | 0 | 1 |
| `store_local` | 19 | 1 | 0 |
| `tee_local` | 20 | 1 | 1 |
| `add` | 21 | 2 | 1 |
| `sub` | 22 | 2 | 1 |
| `mul` | 23 | 2 | 1 |
| `div_s` | 24 | 2 | 1 |
| `div_u` | 25 | 2 | 1 |
| `rem_s` | 26 | 2 | 1 |
| `rem_u` | 27 | 2 | 1 |
| `and` | 28 | 2 | 1 |
| `or` | 29 | 2 | 1 |
| `xor` | 30 | 2 | 1 |
| `shl` | 31 | 2 | 1 |
| `shr_s` | 32 | 2 | 1 |
| `shr_u` | 33 | 2 | 1 |
| `not` | 34 | 1 | 1 |
| `eq` | 35 | 2 | 1 |
| `ne` | 36 | 2 | 1 |
| `lt_s` | 37 | 2 | 1 |
| `le_s` | 38 | 2 | 1 |
| `gt_s` | 39 | 2 | 1 |
| `ge_s` | 40 | 2 | 1 |
| `lt_u` | 41 | 2 | 1 |
| `le_u` | 42 | 2 | 1 |
| `gt_u` | 43 | 2 | 1 |
| `ge_u` | 44 | 2 | 1 |
| `fadd` | 45 | 2 | 1 |
| `fsub` | 46 | 2 | 1 |
| `fmul` | 47 | 2 | 1 |
| `fdiv` | 48 | 2 | 1 |
| `feq` | 49 | 2 | 1 |
| `fne` | 50 | 2 | 1 |
| `flt` | 51 | 2 | 1 |
| `fle` | 52 | 2 | 1 |
| `fgt` | 53 | 2 | 1 |
| `fge` | 54 | 2 | 1 |
| `fneg` | 55 | 1 | 1 |
| `fabs` | 56 | 1 | 1 |
| `fsqrt` | 57 | 1 | 1 |
| `fceil` | 58 | 1 | 1 |
| `ffloor` | 59 | 1 | 1 |
| `ftrunc` | 60 | 1 | 1 |
| `fround` | 61 | 1 | 1 |
| `load` | 67 | — | 1 |
| `store` | 68 | — | 1 |
| `load_i32` | 69 | 1 | 1 |
| `load_i64` | 70 | 1 | 1 |
| `load_ptr` | 71 | 1 | 1 |
| `store_i32` | 72 | 2 | 1 |
| `store_ptr` | 73 | 2 | 1 |
| `memcpy` | 74 | 3 | 1 |
| `memset` | 75 | 3 | 1 |
| `alloc` | 76 | 1 | 1 |
| `alloc_u8` | 77 | 1 | 1 |
| `free` | 78 | 2 | 1 |
| `ptr_width` | 79 | 0 | 1 |
| `heap_bump_bytes` | 80 | 0 | 1 |
| `raw_alloc` | 81 | 1 | 1 |
| `raw_store8` | 82 | 3 | 1 |
| `raw_load8` | 83 | 2 | 1 |
| `raw_store_ptr` | 84 | 3 | 1 |
| `raw_load_ptr` | 85 | 2 | 1 |
| `raw_string` | 86 | 2 | 1 |
| `arr_make` | 87 | n | 1 |
| `arr_get` | 88 | 2 | 1 |
| `arr_set` | 89 | 3 | 0 |
| `arr_len` | 90 | 1 | 1 |
| `arr_rc` | 91 | 1 | 1 |
| `arr_push` | 92 | 2 | 1 |
| `arr_push_owned` | 93 | 2 | 1 |
| `arr_push_i64` | 94 | 2 | 1 |
| `arr_push_f64` | 95 | 2 | 1 |
| `arr_slice` | 96 | 3 | 1 |
| `str_len` | 97 | 1 | 1 |
| `str_concat` | 98 | 2 | 1 |
| `str_eq` | 99 | 2 | 1 |
| `str_cmp` | 100 | 2 | 1 |
| `str_index` | 101 | 2 | 1 |
| `str_slice` | 102 | 3 | 1 |
| `str_from_bytes` | 103 | 1 | 1 |
| `str_bytes` | 104 | 1 | 1 |
| `str_split` | 105 | 2 | 1 |
| `str_to_upper` | 106 | 1 | 1 |
| `str_to_lower` | 107 | 1 | 1 |
| `str_repeat` | 108 | 2 | 1 |
| `str_trim` | 109 | 1 | 1 |
| `tcp_connect` | 110 | 2 | 1 |
| `str_replace` | 111 | 3 | 1 |
| `tcp_pollable` | 112 | 1 | 1 |
| `str_lines` | 113 | 1 | 1 |
| `tcp_send` | 114 | 2 | 1 |
| `tcp_recv` | 115 | 2 | 1 |
| `tcp_close` | 116 | 1 | 1 |
| `opt_make` | 120 | 1 | 1 |
| `opt_tag` | 122 | 1 | 1 |
| `opt_payload` | 123 | 1 | 1 |
| `struct_make` | 138 | n | 1 |
| `struct_get` | 139 | 1 | 1 |
| `struct_set` | 140 | 2 | 0 |
| `struct_set_shape` | 141 | 1 | 1 |
| `tuple_make` | 142 | n | 1 |
| `tuple_get` | 143 | 1 | 1 |
| `tuple_set` | 144 | 2 | 0 |
| `variant_is` | 145 | 1 | 1 |
| `dyn_box` | 146 | 1 | 1 |
| `dyn_dispatch` | 147 | 0 | 1 |
| `wasm_timer_pollable` | 148 | 1 | 1 |
| `call_direct` | 149 | n | 1 |
| `call_indirect` | 150 | n+1 | 1 |
| `call_closure_direct` | 151 | — | 1 |
| `block` | 152 | 0 | 0 |
| `loop` | 153 | 0 | 0 |
| `if` | 154 | 1 | 0 |
| `else` | 155 | 0 | 0 |
| `end` | 156 | 0 | 0 |
| `br` | 157 | 0 | 0 |
| `brif` | 158 | 1 | 0 |
| `drop` | 159 | 1 | 0 |
| `return` | 160 | 1 | 0 |
| `wasm_pollable_drop` | 161 | 1 | 1 |
| `wasm_block` | 162 | 1 | 1 |
| `print_str` | 163 | 1 | 1 |
| `eprint_str` | 164 | 1 | 1 |
| `putchar` | 165 | 1 | 1 |
| `exit` | 166 | 1 | 1 |
| `wasm_poll` | 167 | 1 | 1 |
| `read_line` | 168 | 1 | 1 |
| `read_file` | 169 | 1 | 1 |
| `write_file` | 170 | 2 | 1 |
| `read_dir` | 171 | 1 | 1 |
| `remove_dir_all` | 172 | 1 | 1 |
| `remove_file` | 173 | 1 | 1 |
| `temp_dir` | 174 | 1 | 1 |
| `stat` | 175 | 1 | 1 |
| `env` | 176 | 1 | 1 |
| `subprocess` | 177 | 3 | 1 |
| `read_chunk` | 178 | 2 | 1 |
| `reader_close` | 179 | 1 | 1 |
| `args` | 180 | 0 | 1 |
| `sleep_ms` | 181 | 1 | 1 |
| `monotonic_ns` | 182 | 0 | 1 |
| `now_ns` | 183 | 0 | 1 |
| `now_unix_ms` | 184 | 0 | 1 |
| `random_bytes` | 185 | 1 | 1 |
| `random_i32` | 186 | 0 | 1 |
| `poll` | 187 | 2 | 1 |
| `timer_fd` | 188 | 1 | 1 |
| `store_i64` | 199 | 2 | 1 |
| `tcp_listen` | 200 | 1 | 1 |
| `tcp_accept` | 201 | 1 | 1 |
| `writer_write` | 202 | 2 | 1 |
| `open_file` | 203 | 1 | 1 |
| `syscall3` | 204 | 4 | 1 |
| `raw_scratch` | 205 | 1 | 1 |
| `syscall4` | 206 | 5 | 1 |
| `raw_environ` | 207 | 0 | 1 |
| `udp_send` | 208 | 3 | 1 |
| `proc_fork` | 211 | 0 | 1 |
| `proc_waitpid` | 212 | 1 | 1 |
| `proc_exec` | 213 | 2 | 1 |
| `arr_push_shared_count` | 216 | 0 | 1 |
| `arr_push_shared_bytes` | 217 | 0 | 1 |
| `const_struct` | 218 | 0 | 1 |
| `clz` | 219 | 1 | 1 |
| `ctz` | 220 | 1 | 1 |
| `popcount` | 221 | 1 | 1 |
| `map_hash_seed` | 222 | 0 | 1 |
| `raw_addr` | 223 | 2 | 1 |
| `raw_arr_box` | 224 | 1 | 1 |
| `memchr` | 225 | 3 | 1 |
| `write_file_exec` | 226 | 2 | 1 |
| `ascii_run` | 227 | 2 | 1 |
| `syscall5` | 228 | 6 | 1 |
| `heap_mark` | 229 | 0 | 1 |
| `heap_release_to` | 230 | 1 | 1 |
| `create_dir_all` | 231 | 1 | 1 |
| `isatty` | 232 | 1 | 1 |
| `read_file_bytes` | 233 | 1 | 1 |
| `rmemchr` | 234 | 3 | 1 |
| `count_byte` | 235 | 2 | 1 |
| `lstat` | 236 | 1 | 1 |
| `access` | 237 | 2 | 1 |
| `geteuid` | 238 | 0 | 1 |
| `getegid` | 239 | 0 | 1 |
| `hostname` | 240 | 0 | 1 |
| `fd_stat` | 241 | 1 | 1 |
| `reader_seek` | 242 | 3 | 1 |
| `signal_ignore` | 243 | 1 | 1 |
| `signal_default` | 244 | 1 | 1 |
| `mismatch` | 245 | 5 | 1 |
| `getuid` | 246 | 0 | 1 |
| `getgid` | 247 | 0 | 1 |
| `getgroups` | 248 | 0 | 1 |
| `environ` | 249 | 0 | 1 |
| `uname_field` | 250 | 1 | 1 |
| `getcwd` | 251 | 0 | 1 |
| `cpu_count` | 252 | 0 | 1 |
| `sum_bytes` | 253 | 1 | 1 |
| `create_dir` | 260 | 2 | 1 |
| `remove_dir` | 261 | 1 | 1 |
| `create_link` | 262 | 2 | 1 |
| `create_symlink` | 263 | 2 | 1 |
| `read_link` | 264 | 1 | 1 |
| `umask` | 265 | 1 | 1 |
| `sleep_ns` | 266 | 1 | 1 |
| `rename` | 267 | 2 | 1 |
| `chmod` | 268 | 2 | 1 |
| `set_file_times` | 269 | 6 | 1 |
| `statfs` | 270 | 1 | 1 |
| `process_alive` | 271 | 1 | 1 |
| `rlimit_nofile` | 272 | 0 | 1 |
| `truncate` | 273 | 2 | 1 |
| `mknod` | 274 | 4 | 1 |
| `chown_at` | 275 | 4 | 1 |
| `window_size` | 276 | 1 | 1 |
| `chdir` | 277 | 1 | 1 |
| `signal_mask` | 278 | 2 | 1 |
| `signal_disposition` | 279 | 1 | 1 |
| `proc_exec_as` | 280 | 3 | 1 |
| `writer_truncate` | 281 | 2 | 1 |
| `priority` | 282 | 0 | 1 |
| `set_priority` | 283 | 1 | 1 |
| `set_process_group` | 284 | 2 | 1 |
| `signal_send` | 285 | 2 | 1 |
| `proc_waitpid_nohang` | 286 | 1 | 1 |
| `raw_cover` | 287 | 0 | 1 |
| `line` | 288 | 0 | 0 |
| `buf_new` | 291 | 1 | 1 |
| `buf_push` | 292 | 2 | 1 |
| `buf_push_range` | 293 | 4 | 1 |
| `buf_push_byte` | 294 | 2 | 1 |
| `buf_len` | 295 | 1 | 1 |
| `buf_take` | 296 | 1 | 1 |
| `buf_free` | 297 | 1 | 1 |
| `sync` | 298 | 0 | 1 |
| `fd_fsync` | 299 | 1 | 1 |
| `fd_fdatasync` | 300 | 1 | 1 |
| `fd_syncfs` | 301 | 1 | 1 |
| `arr_push_owned_i64` | 302 | 2 | 1 |
| `arr_push_owned_f64` | 303 | 2 | 1 |
| `open_reader_with` | 304 | 2 | 1 |
| `open_writer_with` | 305 | 2 | 1 |
| `buf_push_u64` | 306 | 2 | 1 |
| `fd_flags` | 307 | 1 | 1 |
| `write_some` | 308 | 2 | 1 |
| `crc32_cksum` | 309 | 2 | 1 |
| `read_dir_all` | 310 | 1 | 1 |
| `fd_dup_onto` | 311 | 2 | 1 |
| `termios_get` | 312 | 1 | 1 |
| `termios_set` | 313 | 3 | 1 |
| `set_window_size` | 314 | 3 | 1 |
| `heap_alloc_count` | 315 | 0 | 1 |
| `chroot` | 316 | 1 | 1 |
| `setuid` | 317 | 1 | 1 |
| `setgid` | 318 | 1 | 1 |
| `setgroups` | 319 | 1 | 1 |
| `tcp_local_port` | 320 | 1 | 1 |
| `chmod_at` | 321 | 3 | 1 |
| `scale_f64` | 322 | 2 | 1 |
| `syscall6` | 323 | 7 | 1 |
| `scan_set` | 324 | 3 | 1 |
| `buf_push_mapped` | 325 | 3 | 1 |
| `buf_push_filtered` | 326 | 3 | 1 |
| `count_runs` | 327 | 3 | 1 |
| `buf_push_expanded` | 328 | 3 | 1 |
| `bsd_sum` | 329 | 2 | 1 |
| `reader_splice` | 330 | 3 | 1 |
| `raw_splice_pipe` | 331 | 0 | 1 |
| `tcp_listen_with` | 332 | 4 | 1 |
| `tcp_socket_ctl` | 333 | 3 | 1 |
| `udp_bind` | 334 | 2 | 1 |
| `udp_connect` | 335 | 3 | 1 |
| `udp_sendto` | 336 | 4 | 1 |
| `udp_recvfrom` | 337 | 3 | 1 |
| `tcp_connect_with` | 338 | 3 | 1 |
| `unix_listen` | 339 | 2 | 1 |
| `unix_connect` | 340 | 1 | 1 |
| `reactor_new` | 341 | 0 | 1 |
| `reactor_ctl` | 342 | 4 | 1 |
| `reactor_wait` | 343 | 3 | 1 |
| `tcp_recv_into` | 344 | 2 | 1 |
| `str_data` | 345 | 1 | 1 |
| `tcp_sendfile` | 346 | 3 | 1 |
| `buf_take_bytes` | 347 | 1 | 1 |
| `read_chunk_bytes` | 348 | 2 | 1 |
| `write_bytes` | 349 | 2 | 1 |
| `write_some_bytes` | 350 | 2 | 1 |
| `buf_push_bytes_range` | 351 | 4 | 1 |
| `write_file_bytes` | 352 | 2 | 1 |
| `tcp_send_bytes` | 353 | 2 | 1 |
| `udp_send_bytes` | 354 | 3 | 1 |
| `udp_sendto_bytes` | 355 | 4 | 1 |
| `rename_noreplace` | 356 | 2 | 1 |
| `rename_exchange` | 357 | 2 | 1 |
| `memchr_bytes` | 358 | 3 | 1 |
| `count_byte_bytes` | 359 | 2 | 1 |
| `rmemchr_bytes` | 360 | 3 | 1 |
| `scan_set_bytes` | 361 | 3 | 1 |
| `getxattr` | 362 | 2 | 1 |
| `lgetxattr` | 363 | 2 | 1 |
| `setxattr` | 364 | 3 | 1 |
| `lsetxattr` | 365 | 3 | 1 |
| `fd_drop_cache` | 366 | 3 | 1 |
| `raw_task_state` | 367 | 0 | 1 |
| `sum_bytes_array` | 368 | 1 | 1 |
| `bsd_sum_bytes` | 369 | 2 | 1 |
| `buf_push_bytes_mapped` | 370 | 3 | 1 |
| `buf_push_bytes_filtered` | 371 | 3 | 1 |
| `buf_push_bytes_expanded` | 372 | 3 | 1 |
| `crc32_cksum_array` | 373 | 2 | 1 |
| `getxattr_bytes` | 374 | 2 | 1 |
| `lgetxattr_bytes` | 375 | 2 | 1 |
| `setxattr_bytes` | 376 | 3 | 1 |
| `lsetxattr_bytes` | 377 | 3 | 1 |
| `mismatch_bytes` | 378 | 5 | 1 |
| `count_runs_bytes` | 379 | 3 | 1 |
| `str_from_bytes_range` | 380 | 3 | 1 |
| `outer_mul_f64` | 381 | 2 | 1 |
| `inner_mul_add_f64` | 382 | 6 | 1 |
| `arr_reserve` | 383 | 1 | 1 |
| `ret_word` | 384 | 1 | 1 |
| `call_word` | 385 | 0 | 1 |
| `buf_clear` | 386 | 1 | 1 |
| `tcp_send_buf` | 387 | 3 | 1 |
| `clock_set` | 388 | 2 | 1 |
| `clock_resolution` | 389 | 0 | 1 |
| `str_hash` | 390 | 2 | 1 |
| `read_dir_ino` | 391 | 1 | 1 |
| `signal_catch` | 392 | 1 | 1 |
| `signal_taken` | 393 | 1 | 1 |
| `signal_catch_interrupting` | 394 | 1 | 1 |
| `signal_raise` | 395 | 1 | 1 |
| `disable_core_dumps` | 396 | 0 | 1 |
| `proc_waitpid_status` | 397 | 1 | 1 |
| `mounts` | 398 | 0 | 1 |
| `ct_mark` | 399 | 1 | 1 |
| `vg_request` | 400 | 1 | 1 |
| `sysctl` | 401 | 1 | 1 |
| `aes_expand_key` | 402 | 1 | 1 |
| `aes_ctr32` | 403 | 3 | 1 |
| `ghash` | 404 | 3 | 1 |
| `clone_file` | 405 | 2 | 1 |
| `set_extproc` | 406 | 2 | 1 |
| `reader_copy_range` | 407 | 3 | 1 |
| `pipe` | 408 | 0 | 1 |
| `sha256_hw` | 409 | 0 | 1 |
| `sha256_hw_blocks` | 410 | 2 | 1 |
| `bswap` | 411 | 1 | 1 |
| `open_dir` | 420 | 1 | 1 |
| `dir_open` | 421 | 2 | 1 |
| `dir_entries` | 422 | 1 | 1 |
| `dir_stat` | 423 | 2 | 1 |
| `dir_access` | 424 | 3 | 1 |
| `dir_unlink` | 425 | 2 | 1 |
| `dir_chmod` | 426 | 4 | 1 |
| `dir_chown` | 427 | 5 | 1 |
| `mulhi_u` | 430 | 2 | 1 |
| `writer_splice_forever` | 431 | 2 | 1 |
| `read_dir_ino_all` | 432 | 1 | 1 |
| `utf8_blocks` | 433 | 3 | 1 |
| `ring_new` | 434 | 1 | 1 |
| `ring_ctl` | 435 | 4 | 1 |
| `ring_wait` | 436 | 3 | 1 |
| `tcp_ktls` | 437 | 4 | 1 |
| `ring_recv` | 438 | 3 | 1 |
| `ring_send_zc` | 439 | 4 | 1 |
| `sha1_hw` | 440 | 0 | 1 |
| `sha1_hw_blocks` | 441 | 2 | 1 |
| `udp_bind_with` | 442 | 3 | 1 |
| `raw_vdso` | 443 | 0 | 1 |
| `aes_gcm_seal` | 444 | 5 | 1 |
| `aes_gcm_open` | 445 | 5 | 1 |
| `mismatch_bytes_str` | 446 | 5 | 1 |
<!-- END ir_effects_run -->

## How this is kept true

`TestSelfHostCoreSpecMatchesTheIR` builds `compiler/drivers/ir_effects_run.fern`
with the self-host compiler and requires the table above to be its output,
row for row. The driver sweeps every id `kind_name` names and reads the
effects from `ir.op_pops` and `ir.op_pushes` — the functions the stack half
of the IR verifier (`irverifystack.fern`) reads when it checks real lowered
programs. So a row here is tied to the same code that is checked against
compiler output, not to a second transcription that could agree with this
table and with nothing else. A new op without a row, a row for a retired op,
and a changed effect all fail; `FERN_UPDATE_GOLDEN=1` rewrites the table.

`TestSelfHostIRKindRegistry` pins the id numbering itself.

## What this does not say

Everything that matters. This file says `call_direct` pops its arguments and
pushes one slot; it does not say that a call to a release helper decrements a
count, that reaching zero frees, what freeing does to a value's fields, or in
what order, and nothing about when the callee runs. It says `arr_set` pops
three slots and says nothing about which array changes.

Those are the store semantics — Layer 3's second and third pieces — and
they are where the bugs actually are. `docs/ALLOCATION-OBSERVABLE.md` is
the only part of the store that is currently pinned at all, and it pins
one shape: that a reclaiming loop does not grow the arena without bound.

Reading this file as a definition of Fern Core would be the same mistake
as reading a wasm opcode table as a definition of WebAssembly. It is the
vocabulary. The sentences are not written yet.
