# `Ok(())` produces, and the filesystem leaves take the typed path

2026-09-25. Self-host typed path. Step 1 of retiring the AST lowering,
continued from `2026-09-25-e-runtime-helpers-define-drop-helpers.md`.

## `Ok(())` in user code

`semsource.variant` compared a literal's written arguments against its
payload types, and a `void` payload has none: `Ok(())` writes one argument,
`()`, so every function returning `Result[void, E]` through that literal was
refused ("unsupported variant literal"), along with every caller. The
payload table already marks such a variant `unit`; the literal now drops its
lone `()` argument before the count is compared.

Pinned by the production row `unit-result-literal`: a function returning
`Ok(())` and `Err(NotFound(…))`, matched both ways, produces whole on every
backend, wasm included, with a clean sanitize leg.

## The filesystem leaves

Twenty path-taking and credential leaves are retyped: `remove_file`,
`create_dir`, `remove_dir`, `chdir`, `chroot`, `create_link`,
`create_symlink`, `rename`, `chmod`, `chmod_at`, `truncate`, `mknod`,
`chown_at`, `set_file_times`, `access`, `set_priority`, `signal_send`,
`set_process_group`, `setuid` / `setgid` and `setgroups`. They share one
shape (NUL-terminate the path into a `__raw_alloc` block, one syscall,
classify a negative result through `__fern_io_error`), so the rewrite was
mechanical: the block is a `usize`, each non-literal syscall operand gains
`as i64`, the result `as i32`, and a stored string byte `as i32`.

Every one returns `Ok(())`, which is why the literal fix came first.

## Measured

- Row `fs-leaves-take-the-typed-path`: a program that creates, renames,
  links and removes files in a scratch directory and reads two `NotFound`
  errors back. The module and the whole bundle produce; the answer matches
  the AST leg on x86-64 and arm64, and native's; the sanitize leg reclaims
  everything.
- Helper sources that check on their own: 63 of 128, up from 42.
