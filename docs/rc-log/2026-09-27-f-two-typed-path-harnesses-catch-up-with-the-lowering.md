# Two typed-path harnesses catch up with the lowering

Closes out the `Test e2e self-host` lane on main (#10426) with #10505, which
took the leak half: a construction's retain at a top-level move site was kept
by a gate that read the reclaim credit, and that credit is withheld at a move
site, so a fresh string or tuple stored into a literal at its last use leaked
one box per call on the AST lowering. The two tests left red after it were
harnesses that had not followed a lowering change.

## TestSelfHostSemanticSourceRC, every target, since #10445

The typed path routes a `Map[i32, i32]` onto core/map: `map_ints` and
`map_get_int` call `map_new_impl` and `__map_set_impl`. The semsource driver
parsed the program as one file, so no backend had those bodies: gcc and the
aarch64 linker refused the undefined references, and wasmtime refused
`call $map_new_impl`. The x86-64 leg did not pass either; the earlier reading
that only arm64 failed was wrong, all four legs were red.

The driver now takes the stdlib root as a third argument, resolves the
program's imports against it with `modloader.load_imports`, bundles them and
tree-shakes the result, in the order `fern.fern` does. The program imports
`core/map`, which the checker requires of any program with Map operations.
Without the tree-shake, core/map's higher-level verbs reach the AST-lowered
pass and `__mapm_extend` bails it on `Map.merge`; with it, the module carries
only the helpers the program reaches, as a CLI build does.

## TestSelfHostSSAPhysicalRCRejects, since #10446 and #10445

- A slice that dies in the frame that made it lowers to `str_slice frame:N`
  (ssaunits.frame_views). The driver looked for the heap form's `str_slice`
  and returned 48. It now pins the frame form; the view free still follows,
  and passes over the frame box's immortal rc, so that check stands.
- A `Map[i32, i32]` is routed, so its drop is `__map_drop_impl`, its insert a
  `__map_set_impl` call followed by a compare against the receiver and a
  release of the copy core/map's copy-on-write may have made, and its len and
  get `__map_len_impl` and `__map_get_impl`. The driver expected the runtime's
  `__fern_map_free`, `map_set`, `map_len` and `map_get`. The `Map[i32, string]`
  row keeps the runtime path and its `__fern_map_free_vs` check.

Neither change touches the compiler. The routed surface itself is measured by
`TestSelfHostRoutedMap*`.
