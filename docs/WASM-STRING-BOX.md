# A wasm string is a `[data, len]` box

Status: [plan] for #15740. The owner chose this shape on 2026-11-23.

## The decision

On the native targets a string is a box of two words, `[data, len]`. On
wasm32 a string is one `[len][bytes]` block. A view on wasm32 is one of two
other forms: a counted `{len | 1 << 31, data}` descriptor
(`$__fern_str_borrow`), or a `{data, len}` window in the shadow-stack frame,
tagged 3 (`frame_window_wat`). A kernel that takes a string or a view decodes
all three forms (`view_string_decode`, `byte_view_decode`).

A string slice on wasm32 copies its bytes (`$__fern_substr`), because most
string readers know only the block. The program of #15740 makes 10
allocations on wasm32 and 0 on the native targets.

Each wasm32 string becomes the native box. A view is then a box with another
data pointer, as on the native targets. A slice that goes to a call then needs
no copy. The three forms, their decoders and the tag test go.

## The layout

| Form | Words | Bytes |
|---|---|---|
| Owned string | `[data, len]`, with `data` = the box + 8 | after the box, in the same block |
| Literal | `[data, len]` in the data segment, below the heap base | after the box, in the data segment |
| View | `[data, len]`, with `data` in the source | in the source |

The count header stays where it is: `[rc][size]` at the box - 8. A view
holds no unit of its source. The compiler keeps the source alive while the
view lives, as on the native targets: a view is lent, and a stored view is a
copy (#15760).

## The phases

1. **Accessors, with no change in the output.** Each read of the length or the
   bytes of a string in the wasm backend calls one of two emitters: the
   length of a string, and the address of its first byte. Each creation of a
   string calls one allocator emitter. The Fern sources in
   `compiler/runtime/wasm/` use two functions for the same reads. After this
   phase, the emitted WAT is the same as before for each program of the
   corpus.

2. **The new layout.** The emitters, the allocator, the literals, the release
   of an owned string, the hand-built blocks and the canonical-ABI boundary
   change together. This phase must land as one change, because two layouts
   cannot exist at one time.

3. **Views as boxes.** `K_STR_SLICE` and its frame form give a box on wasm32.
   `$__fern_substr` remains only where a slice must own its bytes.
   `view_string_decode`, `byte_view_decode` for strings, the borrowed
   descriptor and the frame window go. `owned_frame_views` goes for wasm32.

4. **Tests.** The expectations that pin the copy change to the native values,
   and the zero-allocation guard of #15740 runs on wasm32.

## The inventory at `a8d712bd`

- WAT helpers that read a string: about 70. Of these, 18 are `str_*`
  helpers, about 32 are file, path and environment helpers, and 10 are buffer
  helpers. Only about 15 decode a view.

- Sites that create a string: 32 calls of `$__fern_str_box (4 + n)`, 14 empty
  strings, 4 one-byte strings, the integer formatters and `string_from_bytes`.
  Two helpers build a block without `$__fern_str_box`:
  `__fern_str_grow_fresh_bytes` and the path of `remove_dir_all`.
  `$__fern_str_box` is also the allocator of other boxes, so the string
  allocator must be a new emitter.

- Literals: `(data (i32.const off) le32(len) bytes)`, with no count header,
  4-byte aligned (`str_offset`, `placed_strs`).

- Inline ops: `K_STR_LEN`, `K_STR_DATA`, `K_STR_INDEX`, `K_STR_SLICE` and
  `K_RAW_STRING` in `emit_ir_op` and `emit_str_op_wat`.

- Fern sources compiled for wasm32: 8 files in `compiler/runtime/wasm/`
  read string memory. `compiler/rtwasm.fern` embeds copies of them.

- Boundaries: WASI preview 1 and 2, and the component-model lift and lower of
  strings (`xstr_new`, `extern_marshal_arg`, `build_export_wrapper`,
  `extern_copy_string`, `extern_wrappers`).

- Cache tokens in `ircore.fern`: `counted-string-views-1` and
  `wasm-string-align4-1` change in phase 2.

- Tests that pin the copy include `self_host_frame_view_test.go`,
  `self_host_frame_view_release_test.go`,
  `self_host_wasm_units_test.go` (`wasmRuntimeStringViewProbe`) and
  `self_host_wasm_ioerror_layout_test.go`.
