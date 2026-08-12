// Imports + WASI-facing helpers for the wasmbin backend.
//
// The lang `print(s)` lowering eventually calls a synthetic
// __fern_print helper. The helper takes a (data, len) string,
// normalises it to a heap buffer (so inline-form strings work
// via the SSO seam), writes a single iovec to a fixed scratch
// region of linear memory, and invokes the imported WASI
// preview-1 fd_write.

package wasmbin

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/wasm/convert"
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// importSpec describes one imported function.
type importSpec struct {
	module  string
	name    string
	params  []byte
	results []byte
}

// importSpecs is the import registry. Each entry corresponds to
// one wasi_snapshot_preview1 (or similar) imported function.
var importSpecs = map[string]importSpec{
	"async_task_return": {
		// (value: i32) → (). The WASI Preview-3 component-model-async
		// `task.return` intrinsic, imported under the empty module name
		// `("", "task-return")` and provided by the component's
		// `canon task.return` (see component.BuildAsyncLiftedExportComponent).
		// An async-lifted export's core function calls this to deliver
		// its result, then returns void. Added to the import set when
		// BuildOptions.AsyncExportName is set.
		module:  "",
		name:    "task-return",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	// The WASI Preview-3 waitable-set / subtask intrinsics the async-import
	// await loop calls when the lowered call returns a STARTED (pending)
	// status. Imported under the empty module name; provided by the component
	// composer's canon waitable-set.* / subtask.drop. See
	// docs/WASI-PREVIEW3-ASYNC-PLAN.md.
	"async_ws_new": { // waitable-set.new: () -> i32 (set handle)
		module:  "",
		name:    "ws-new",
		params:  nil,
		results: []byte{encode.ValtypeI32},
	},
	"async_w_join": { // waitable.join: (waitable, set) -> ()
		module:  "",
		name:    "w-join",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"async_ws_wait": { // waitable-set.wait: (set, evtptr) -> i32 (event code)
		module:  "",
		name:    "ws-wait",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"async_subtask_drop": { // subtask.drop: (subtask) -> ()
		module:  "",
		name:    "subtask-drop",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"async_ws_drop": { // waitable-set.drop: (set) -> ()
		module:  "",
		name:    "ws-drop",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	// WASI Preview-3 stream-result intrinsics — the colorless `stream[T]` collect
	// loop (docs/STREAM-TYPE-SURFACE.md). Imported under "" and provided by the
	// composer's canon stream.read (trampolined over the consumer memory) /
	// stream.drop-readable.
	"async_stream_read": { // stream.read: (readable, ptr, count) -> i32 status
		module:  "",
		name:    "stream-read",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"async_stream_drop_readable": { // stream.drop-readable: (readable) -> ()
		module:  "",
		name:    "stream-drop-readable",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	// WASI Preview-3 stream-PARAM (produce) intrinsics — the colorless `stream[T]`
	// produce wrapper write-streams an eager array out (docs/STREAM-TYPE-SURFACE.md).
	// Provided by the composer's canon stream.new / stream.write (trampolined over
	// the consumer memory) / stream.drop-writable.
	"async_stream_new": { // stream.new: () -> i64 (packed readable<<? / writable handle pair)
		module:  "",
		name:    "stream-new",
		params:  nil,
		results: []byte{encode.ValtypeI64},
	},
	"async_stream_write": { // stream.write: (writable, ptr, count) -> i32 status
		module:  "",
		name:    "stream-write",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"async_stream_drop_writable": { // stream.drop-writable: (writable) -> ()
		module:  "",
		name:    "stream-drop-writable",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_fd_write": {
		// (fd, iovs_ptr, iovs_count, nwritten_ptr) → errno
		module:  "wasi_snapshot_preview1",
		name:    "fd_write",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_proc_exit": {
		// (exit_code: i32) → ! (never returns; the wasi spec
		// marks this as a "trap-like" abrupt termination, but
		// the wasm-level signature still says void return).
		module:  "wasi_snapshot_preview1",
		name:    "proc_exit",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_random_get": {
		// (buf_ptr, buf_len) → errno. Fills buf_ptr..+buf_len
		// with cryptographically-strong random bytes (per the
		// wasi spec; host may degrade in sandboxed environments).
		module:  "wasi_snapshot_preview1",
		name:    "random_get",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_random_get_u64_p2": {
		// Preview-2 variant: wasi:random/random::get-random-u64
		// () → u64. Same canonical-ABI shape as `() → i64` at
		// the core wasm level. Returns a single cryptographically-
		// strong random u64 instead of filling a host-side
		// buffer. Replaces wasi_random_get for the
		// __fern_random_i32 helper under EmitOptions.Preview2WASI.
		module:  "wasi:random/random@0.2.0",
		name:    "get-random-u64",
		params:  nil,
		results: []byte{encode.ValtypeI64},
	},
	"wasi_clock_time_get": {
		// (clock_id i32, precision i64, time_ptr i32) → errno i32.
		// Writes the current time as nanoseconds-since-epoch
		// (u64 little-endian) at time_ptr.
		module:  "wasi_snapshot_preview1",
		name:    "clock_time_get",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI64, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_monotonic_now_p2": {
		// Preview-2: wasi:clocks/monotonic-clock@0.2.0::now()
		// → instant (u64, lowers to i64 at core wasm). Returns
		// nanoseconds since some fixed reference, monotonically
		// non-decreasing. Replaces wasi_clock_time_get for the
		// __fern_monotonic_ns helper under EmitOptions.Preview2WASI.
		module:  "wasi:clocks/monotonic-clock@0.2.0",
		name:    "now",
		params:  nil,
		results: []byte{encode.ValtypeI64},
	},
	"wasi_wall_clock_now_p2": {
		// Preview-2: wasi:clocks/wall-clock@0.2.0::now() →
		// datetime { seconds: u64; nanoseconds: u32 }. The
		// record is returned via the canonical-ABI indirect
		// convention, so the lowered core import is
		// `(out_ptr i32) -> ()` — the host writes the 16-byte
		// datetime at out_ptr (u64 seconds at +0, u32
		// nanoseconds at +8). Replaces wasi_clock_time_get for
		// the realtime helpers (__fern_now_ns / __fern_now_unix_ms)
		// under EmitOptions.Preview2WASI.
		module:  "wasi:clocks/wall-clock@0.2.0",
		name:    "now",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_get_stdout_p2": {
		// Preview-2: wasi:cli/stdout@0.2.0::get-stdout() →
		// own<output-stream> (lowers to i32 handle). One-time
		// call per program; the result handle is cached in the
		// stdoutHandleAddr slot.
		module:  "wasi:cli/stdout@0.2.0",
		name:    "get-stdout",
		params:  nil,
		results: []byte{encode.ValtypeI32},
	},
	"wasi_get_directories_p2": {
		// Preview-2: wasi:filesystem/preopens@0.2.0::get-directories()
		// -> list<tuple<own<descriptor>, string>>. Lowered to
		// `(retptr: i32) -> ()`: retptr holds the list header (data
		// ptr @ +0, count @ +4). Each element is a 12-byte tuple —
		// descriptor handle @ +0, mount-path (ptr @ +4, len @ +8).
		module:  "wasi:filesystem/preopens@0.2.0",
		name:    "get-directories",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_descriptor_open_at_p2": {
		// Preview-2: wasi:filesystem/types@0.2.0::
		//   [method]descriptor.open-at lowered to 7×i32 ->():
		//   (self, path-flags, path_ptr, path_len, open-flags,
		//    descriptor-flags, retptr). retptr holds
		//   result<own<descriptor>, error-code>: disc @ +0 (0=ok),
		//   descriptor handle (ok) / error-code disc (err) @ +4.
		module: "wasi:filesystem/types@0.2.0",
		name:   "[method]descriptor.open-at",
		params: []byte{
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32,
		},
		results: nil,
	},
	"wasi_descriptor_read_via_stream_p2": {
		// Preview-2: wasi:filesystem/types@0.2.0::
		//   [method]descriptor.read-via-stream lowered to
		//   (self: i32, offset: i64, retptr: i32) -> (). retptr holds
		//   result<own<input-stream>, error-code>: disc @ +0,
		//   stream handle (ok) / error-code disc (err) @ +4.
		module:  "wasi:filesystem/types@0.2.0",
		name:    "[method]descriptor.read-via-stream",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI64, encode.ValtypeI32},
		results: nil,
	},
	"wasi_descriptor_write_via_stream_p2": {
		// Preview-2: wasi:filesystem/types@0.2.0::
		//   [method]descriptor.write-via-stream lowered to
		//   (self: i32, offset: i64, retptr: i32) -> (). retptr holds
		//   result<own<output-stream>, error-code>: disc @ +0,
		//   stream handle (ok) / error-code disc (err) @ +4.
		module:  "wasi:filesystem/types@0.2.0",
		name:    "[method]descriptor.write-via-stream",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI64, encode.ValtypeI32},
		results: nil,
	},
	"wasi_descriptor_append_via_stream_p2": {
		// Preview-2: wasi:filesystem/types@0.2.0::
		//   [method]descriptor.append-via-stream lowered to
		//   (self: i32, retptr: i32) -> (). Like write-via-stream but
		//   with no offset — the returned output-stream appends at EOF.
		module:  "wasi:filesystem/types@0.2.0",
		name:    "[method]descriptor.append-via-stream",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	// The path mutators share a lowering exactly:
	//   (self: i32, path_ptr: i32, path_len: i32, retptr: i32) -> ().
	// retptr holds `result<_, error-code>`, whose ok arm is empty — so
	// unlike open-at there is no payload at +4, only the discriminant
	// byte at +0 and, on the error arm, the error-code at +4.
	"wasi_descriptor_unlink_file_at_p2": {
		module:  "wasi:filesystem/types@0.2.0",
		name:    "[method]descriptor.unlink-file-at",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_descriptor_create_directory_at_p2": {
		module:  "wasi:filesystem/types@0.2.0",
		name:    "[method]descriptor.create-directory-at",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_descriptor_stat_at_p2": {
		// Preview-2: [method]descriptor.stat-at lowered to
		//   (self, path-flags, path_ptr, path_len, retptr) -> ().
		// retptr holds `result<descriptor-stat, error-code>`, 104
		// bytes: disc @ +0, then the 96-byte descriptor-stat at +8
		// (type @ +8, link-count @ +16, size @ +24) on the ok arm, or
		// the error-code at +8 on the err arm. Note the error-code is
		// at +8 here, not the +4 the single-word results use — the
		// record's 8-byte alignment pushes the payload out.
		module: "wasi:filesystem/types@0.2.0",
		name:   "[method]descriptor.stat-at",
		params: []byte{
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32,
		},
		results: nil,
	},
	"wasi_descriptor_read_directory_p2": {
		// Preview-2: [method]descriptor.read-directory lowered to
		//   (self, retptr) -> (). retptr holds
		//   result<own<directory-entry-stream>, error-code>:
		//   disc @ +0, stream handle (ok) / error-code (err) @ +4.
		module:  "wasi:filesystem/types@0.2.0",
		name:    "[method]descriptor.read-directory",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_dir_entry_stream_read_p2": {
		// Preview-2:
		//   [method]directory-entry-stream.read-directory-entry
		// lowered to (self, retptr) -> (). retptr holds
		//   result<option<directory-entry>, error-code>, 20 bytes:
		//   disc @ +0; on ok the option's disc @ +4 (0 = none = end of
		//   listing) and, when some, the entry at +8 — type @ +8, name
		//   ptr @ +12, name len @ +16. On err the error-code @ +4.
		module:  "wasi:filesystem/types@0.2.0",
		name:    "[method]directory-entry-stream.read-directory-entry",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_dir_entry_stream_drop_p2": {
		// Canonical resource.drop for the listing cursor. The guest
		// owns the handle read-directory hands back, so the loop must
		// release it — the component model has no scope-exit for this.
		module:  "wasi:filesystem/types@0.2.0",
		name:    "[resource-drop]directory-entry-stream",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_descriptor_remove_directory_at_p2": {
		// Preview-2: [method]descriptor.remove-directory-at, the same
		// (self, path_ptr, path_len, retptr) -> () lowering as the
		// other path mutators.
		module:  "wasi:filesystem/types@0.2.0",
		name:    "[method]descriptor.remove-directory-at",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_get_arguments_p2": {
		// Preview-2: wasi:cli/environment@0.2.0::get-arguments() ->
		// list<string>. Canonical-ABI lowered to `(retptr: i32) ->
		// ()`: retptr holds the list header (data ptr @ +0, element
		// count @ +4). Each element is a string (ptr @ +0, len @ +4)
		// — 8 bytes — already allocated in the user's memory through
		// cabi_realloc, so the args helpers read (ptr, len) directly
		// without the preview-1 NUL walk.
		module:  "wasi:cli/environment@0.2.0",
		name:    "get-arguments",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_get_environment_p2": {
		// Preview-2: wasi:cli/environment@0.2.0::get-environment() ->
		// list<tuple<string, string>>. Canonical-ABI lowered to
		// `(retptr: i32) -> ()`: retptr holds the list header (data
		// ptr @ +0, count @ +4). Each element is a 16-byte tuple —
		// key (ptr @ +0, len @ +4), value (ptr @ +8, len @ +12) —
		// already allocated in the user's memory through
		// cabi_realloc. Unlike preview-1's combined "KEY=VALUE"
		// NUL strings, the key/value are pre-split.
		module:  "wasi:cli/environment@0.2.0",
		name:    "get-environment",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_get_stderr_p2": {
		// Preview-2: wasi:cli/stderr@0.2.0::get-stderr() →
		// own<output-stream>. Mirror of get-stdout; the result
		// handle is cached in the stderrHandleAddr slot and
		// feeds the preview-2 eprint helper.
		module:  "wasi:cli/stderr@0.2.0",
		name:    "get-stderr",
		params:  nil,
		results: []byte{encode.ValtypeI32},
	},
	"wasi_get_stdin_p2": {
		// Preview-2: wasi:cli/stdin@0.2.0::get-stdin() →
		// own<input-stream> (lowers to i32 handle). One-time call
		// per program; the result handle is cached (handle+1, 0 =
		// uninit) in the readByteScratchAddr slot, which the
		// preview-1 iovec scratch otherwise owns — the two read
		// paths are mutually exclusive (selected by Preview2WASI).
		module:  "wasi:cli/stdin@0.2.0",
		name:    "get-stdin",
		params:  nil,
		results: []byte{encode.ValtypeI32},
	},
	"wasi_blocking_write_and_flush_p2": {
		// Preview-2: wasi:io/streams@0.2.0::
		//   [method]output-stream.blocking-write-and-flush(
		//     self: borrow<output-stream>,
		//     contents: list<u8>) -> result<_, stream-error>
		// Canonical-ABI lowered to:
		//   (self: i32, ptr: i32, len: i32, ret_ptr: i32) -> ()
		// — self is the borrow handle, contents is the list
		// lowered to (ptr, len), and the result is returned via
		// a 12-byte indirect-return area pointed to by ret_ptr.
		module:  "wasi:io/streams@0.2.0",
		name:    "[method]output-stream.blocking-write-and-flush",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_environ_sizes_get": {
		// (envc_ptr i32, env_buf_size_ptr i32) → errno.
		// Writes the environment-variable count + the total
		// byte size of the concatenated env strings into the
		// two output pointers.
		module:  "wasi_snapshot_preview1",
		name:    "environ_sizes_get",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_args_sizes_get": {
		// (argc_ptr i32, argv_buf_size_ptr i32) → errno.
		// Writes argv-count + total byte length of the
		// concatenated argv strings (NUL-separated) into the
		// two output pointers.
		module:  "wasi_snapshot_preview1",
		name:    "args_sizes_get",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_environ_get": {
		// (environ_ptr i32, environ_buf i32) → errno. Writes
		// argc i32 pointers into environ_ptr, followed by the
		// NUL-terminated "KEY=VALUE" strings into environ_buf.
		module:  "wasi_snapshot_preview1",
		name:    "environ_get",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_args_get": {
		// (argv_ptr i32, argv_buf i32) → errno. Writes argc i32
		// pointers into argv_ptr, followed by the NUL-terminated
		// argv strings into argv_buf.
		module:  "wasi_snapshot_preview1",
		name:    "args_get",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_fd_read": {
		// (fd, iovs_ptr, iovs_count, nread_ptr) → errno.
		// Reads up to sum(iov_len) bytes into the iovs[i].base
		// buffers. nread_ptr is written with the actual bytes
		// read; short reads / EOF set nread to less than the
		// requested total.
		module:  "wasi_snapshot_preview1",
		name:    "fd_read",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_path_open": {
		// (dirfd, dirflags, path_ptr, path_len, oflags,
		//  fs_rights_base i64, fs_rights_inheriting i64,
		//  fdflags, retptr_newfd) → errno.
		// Opens a file under a preopened directory descriptor
		// (`dirfd`); the path is interpreted relative to that
		// descriptor. `retptr_newfd` is written with the new
		// file descriptor on success.
		module: "wasi_snapshot_preview1",
		name:   "path_open",
		params: []byte{
			encode.ValtypeI32, // dirfd
			encode.ValtypeI32, // dirflags
			encode.ValtypeI32, // path_ptr
			encode.ValtypeI32, // path_len
			encode.ValtypeI32, // oflags
			encode.ValtypeI64, // fs_rights_base
			encode.ValtypeI64, // fs_rights_inheriting
			encode.ValtypeI32, // fdflags
			encode.ValtypeI32, // retptr_newfd
		},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_fd_close": {
		// (fd) → errno. Closes the file descriptor. Errors are
		// typically ignored — there's nothing useful for the
		// caller to do with them on close.
		module:  "wasi_snapshot_preview1",
		name:    "fd_close",
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_path_unlink_file": {
		// (dirfd, path_ptr, path_len) → errno. Unlinks a REGULAR
		// file under `dirfd`. Directories need path_remove_directory
		// instead — unlinking one is EISDIR — which is why
		// remove_dir_all dispatches on the entry's d_type.
		module:  "wasi_snapshot_preview1",
		name:    "path_unlink_file",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_path_remove_directory": {
		// (dirfd, path_ptr, path_len) → errno. Removes an EMPTY
		// directory under `dirfd`; a non-empty one is ENOTEMPTY, so
		// remove_dir_all has to drain the children first.
		module:  "wasi_snapshot_preview1",
		name:    "path_remove_directory",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_path_create_directory": {
		// (dirfd, path_ptr, path_len) → errno. Creates one directory
		// level under `dirfd`; the parent must already exist. EEXIST
		// when the name is taken, which is the signal temp_dir's
		// retry loop uses to pick another name.
		module:  "wasi_snapshot_preview1",
		name:    "path_create_directory",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_path_filestat_get": {
		// (dirfd, flags, path_ptr, path_len, buf_ptr) → errno.
		// Writes a 64-byte `filestat` at buf_ptr: dev@0, ino@8,
		// filetype@16 (1 byte), nlink@24, size@32 (u64), and three
		// timestamps. `stat` reads filetype and size; flags=1 is
		// SYMLINK_FOLLOW, matching the interpreter's os.Stat.
		module: "wasi_snapshot_preview1",
		name:   "path_filestat_get",
		params: []byte{
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32,
		},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_fd_readdir": {
		// (fd, buf_ptr, buf_len, cookie i64, bufused_ptr) → errno.
		// Fills buf with packed `dirent` records — next_cookie@0 (u64),
		// d_ino@8, d_namlen@16 (u32), d_type@20 (1 byte) — each
		// followed by d_namlen name bytes, unterminated. A full buffer
		// (bufused == buf_len) means "there may be more"; the helpers
		// here grow and retry rather than paginating by cookie, since
		// the entry list has to be materialised into one array anyway.
		module: "wasi_snapshot_preview1",
		name:   "fd_readdir",
		params: []byte{
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI64, encode.ValtypeI32,
		},
		results: []byte{encode.ValtypeI32},
	},

	// ---- wasi:sockets imports for the TCP helpers ----
	//
	// User-facing surface: tcp_listen / tcp_accept / tcp_recv /
	// tcp_send / tcp_close. The listener / connection structs each
	// live as a 12-byte heap allocation (tcp_socket, input_stream,
	// output_stream); listener sockets zero the stream slots. The
	// host doesn't need `wasmtime --tcp-listen=…` — the guest binds
	// the port itself via wasi:sockets.
	"wasi_sockets_instance_network": {
		// () → network handle. Used once per program; the
		// __network_handle accessor caches the result in low memory.
		module:  "wasi:sockets/instance-network@0.2.0",
		name:    "instance-network",
		params:  nil,
		results: []byte{encode.ValtypeI32},
	},
	"wasi_sockets_create_tcp_socket": {
		// (family: i32, retptr: i32). family=0 → ipv4. retptr
		// gets `result<tcp-socket, error-code>` (disc @ +0, payload
		// at +4 — either the socket handle (Ok) or the error-code
		// byte (Err)).
		module:  "wasi:sockets/tcp-create-socket@0.2.0",
		name:    "create-tcp-socket",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_sockets_tcp_start_bind": {
		// start-bind takes the canonical-ABI flattening of
		// `ip-socket-address` — a 1-i32 discriminant plus an
		// 11-i32 max payload (ipv4 uses 5 slots, ipv6 needs 11,
		// the variant joins them). Total params: self,
		// borrow<network>, disc, 11 flat slots, retptr = 15 i32.
		module: "wasi:sockets/tcp@0.2.0",
		name:   "[method]tcp-socket.start-bind",
		params: []byte{
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
		},
		results: nil,
	},
	"wasi_sockets_tcp_finish_bind": {
		// (self, retptr) → (). Result<_, error-code> written at retptr.
		module:  "wasi:sockets/tcp@0.2.0",
		name:    "[method]tcp-socket.finish-bind",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_sockets_tcp_start_listen": {
		// (self, retptr) → ().
		module:  "wasi:sockets/tcp@0.2.0",
		name:    "[method]tcp-socket.start-listen",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_sockets_tcp_finish_listen": {
		// (self, retptr) → ().
		module:  "wasi:sockets/tcp@0.2.0",
		name:    "[method]tcp-socket.finish-listen",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_sockets_tcp_start_connect": {
		// Outbound client. Same canonical-ABI flattening as
		// start-bind: self, borrow<network>, disc, 11 flat slots
		// (ipv4 uses port + 4 octets), retptr = 15 i32.
		module: "wasi:sockets/tcp@0.2.0",
		name:   "[method]tcp-socket.start-connect",
		params: []byte{
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
		},
		results: nil,
	},
	"wasi_sockets_tcp_finish_connect": {
		// (self, retptr) → (). retptr holds
		// `result<tuple<input-stream, output-stream>, error-code>`:
		// 1 disc byte at +0, 3 bytes pad, then (input, output) at
		// +4 / +8 (Ok) or the error-code at +4 (Err). 8-byte
		// payload; the caller's retptr is the shared 16-byte area.
		module:  "wasi:sockets/tcp@0.2.0",
		name:    "[method]tcp-socket.finish-connect",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_sockets_tcp_accept": {
		// (self, retptr) → (). retptr holds
		// `result<tuple<tcp-socket, input-stream, output-stream>,
		// error-code>`: 1 disc byte at +0, 3 bytes pad, then the
		// (sock, in, out) tuple at +4 (Ok) or the error-code byte
		// at +4 (Err). Total payload area: 12 bytes; allocate 16.
		module:  "wasi:sockets/tcp@0.2.0",
		name:    "[method]tcp-socket.accept",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_sockets_tcp_subscribe": {
		// (self) → pollable handle. Paired with pollable.block to
		// wait until a connection is ready before calling accept.
		module:  "wasi:sockets/tcp@0.2.0",
		name:    "[method]tcp-socket.subscribe",
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_sockets_tcp_socket_drop": {
		// (handle) → (). Drops a tcp-socket. Must come AFTER any
		// child input-stream / output-stream drops — the canonical
		// ABI rejects parent-drop with live children.
		module:  "wasi:sockets/tcp@0.2.0",
		name:    "[resource-drop]tcp-socket",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_io_pollable_block": {
		// (pollable) → (). Synchronously waits for the pollable
		// to become ready. Used by tcp_accept before invoking
		// the non-blocking accept.
		module:  "wasi:io/poll@0.2.0",
		name:    "[method]pollable.block",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_io_pollable_drop": {
		// (pollable) → (). Drops a pollable handle.
		module:  "wasi:io/poll@0.2.0",
		name:    "[resource-drop]pollable",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_clocks_subscribe_duration": {
		// (duration_ns: u64) → pollable handle. Returns an
		// own<pollable> that becomes ready after `duration_ns`
		// nanoseconds. The wasm reactor's timer primitive — the
		// pollable analog of the native timerfd. Crucially the
		// pollable is the SAME wasi:io/poll resource a socket's
		// subscribe yields, so the composer aliases it across the
		// clock/poll instance boundary (see WASM-REACTOR-PLAN.md).
		module:  "wasi:clocks/monotonic-clock@0.2.0",
		name:    "subscribe-duration",
		params:  []byte{encode.ValtypeI64},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_io_poll_poll": {
		// (list-ptr, list-len, retptr) → (). The reactor multiplexer:
		// poll(list<pollable>) -> list<u32>. The pollable list is
		// passed as (ptr, len) — a Fern i32[] is already a contiguous
		// handle array — and the ready-index list comes back through
		// the return area: retptr holds (data ptr @ +0, count @ +4).
		module:  "wasi:io/poll@0.2.0",
		name:    "poll",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_io_input_stream_drop": {
		// (handle) → (). Drops an input-stream resource. Required
		// before dropping the parent tcp-socket (canonical-ABI
		// resource-with-children rule).
		module:  "wasi:io/streams@0.2.0",
		name:    "[resource-drop]input-stream",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_io_output_stream_drop": {
		// (handle) → (). Drops an output-stream resource. Same
		// child-before-parent rule as input-stream.
		module:  "wasi:io/streams@0.2.0",
		name:    "[resource-drop]output-stream",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	// ---- wasi:sockets/udp (send-only) for udp_send. Mirrors the tcp
	// socket family: create → bind → stream(connect) → check-send →
	// send, plus the three datagram resource drops. ----
	"wasi_sockets_create_udp_socket": {
		// (family: i32, retptr: i32). family=0 → ipv4. retptr gets
		// result<udp-socket, error-code> (disc @ +0, handle @ +4 on Ok).
		module:  "wasi:sockets/udp-create-socket@0.2.0",
		name:    "create-udp-socket",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_sockets_udp_start_bind": {
		// Same 15-i32 ip-socket-address flattening as tcp start-bind
		// (self, borrow<network>, disc, 11 payload, retptr).
		module: "wasi:sockets/udp@0.2.0",
		name:   "[method]udp-socket.start-bind",
		params: []byte{
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
		},
		results: nil,
	},
	"wasi_sockets_udp_finish_bind": {
		// (self, retptr) → (). result<_, error-code> at retptr.
		module:  "wasi:sockets/udp@0.2.0",
		name:    "[method]udp-socket.finish-bind",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_sockets_udp_stream": {
		// stream(self, remote-address: option<ip-socket-address>) ->
		// result<tuple<incoming-datagram-stream, outgoing-datagram-stream>,
		// error-code>. The option flattens to 13 i32 (1 option disc + 1
		// ip-addr disc + 11 payload); + self + retptr = 15 i32. retptr
		// holds the result: disc @ +0, (incoming, outgoing) @ +4/+8 on Ok.
		module: "wasi:sockets/udp@0.2.0",
		name:   "[method]udp-socket.stream",
		params: []byte{
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
		},
		results: nil,
	},
	"wasi_sockets_udp_check_send": {
		// (self, retptr) → (). result<u64, error-code>: disc @ +0,
		// u64 permit count @ +8 on Ok.
		module:  "wasi:sockets/udp@0.2.0",
		name:    "[method]outgoing-datagram-stream.check-send",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_sockets_udp_outgoing_subscribe": {
		// (self) → pollable handle. Paired with pollable.block so the
		// sender waits until check-send permits ≥1 datagram before
		// calling send (wasmtime ≥45 rejects an over-permit send).
		module:  "wasi:sockets/udp@0.2.0",
		name:    "[method]outgoing-datagram-stream.subscribe",
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_sockets_udp_send": {
		// (self, datagrams_ptr, datagrams_len, retptr) → (). datagrams
		// is a list<outgoing-datagram>; each 60-byte record is
		// { data: (ptr@+0, len@+4), remote-address: option @ +8 }.
		// retptr holds result<u64, error-code>: disc @ +0, u64 sent
		// count @ +8 on Ok.
		module:  "wasi:sockets/udp@0.2.0",
		name:    "[method]outgoing-datagram-stream.send",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_sockets_udp_socket_drop": {
		// (handle) → (). Drops a udp-socket. After the datagram-stream
		// children are dropped.
		module:  "wasi:sockets/udp@0.2.0",
		name:    "[resource-drop]udp-socket",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_sockets_incoming_datagram_stream_drop": {
		module:  "wasi:sockets/udp@0.2.0",
		name:    "[resource-drop]incoming-datagram-stream",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_sockets_outgoing_datagram_stream_drop": {
		module:  "wasi:sockets/udp@0.2.0",
		name:    "[resource-drop]outgoing-datagram-stream",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_io_blocking_read": {
		// (handle: i32, len: u64, retptr: i32) → (). Reads up to
		// `len` bytes. retptr holds result<list<u8>, stream-error>:
		// 1 disc byte @ +0, 3 bytes pad, list-data ptr @ +4, list
		// length @ +8. Allocate 12 bytes for the retptr.
		module:  "wasi:io/streams@0.2.0",
		name:    "[method]input-stream.blocking-read",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI64, encode.ValtypeI32},
		results: nil,
	},
	// ---- wasi:http imports for the wasi:http/incoming-handler
	// wrapper. The wrapper marshals canonical-ABI incoming-request
	// → user's HttpRequest, calls handle(), then streams the
	// HttpResponse back through outgoing-body. See wasi_http.go.
	"wasi_http_request_method": {
		// (self, retptr) → (). Variant `method` written at retptr:
		// disc@+0 (0..8 for canonical verbs, 9 = OTHER), payload
		// string at +4/+8 (OTHER only; named verbs leave the slot
		// empty, but some hosts fill it with the canonical text).
		module:  "wasi:http/types@0.2.0",
		name:    "[method]incoming-request.method",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_http_request_path_with_query": {
		// (self, retptr) → (). option<string> at retptr: disc@+0,
		// string ptr@+4, len@+8.
		module:  "wasi:http/types@0.2.0",
		name:    "[method]incoming-request.path-with-query",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_http_request_headers": {
		// (self) → i32 (fields handle). Snapshot of the request
		// headers; the wrapper walks `fields.entries` on this and
		// populates the lang HeaderMap, then drops the fields.
		module:  "wasi:http/types@0.2.0",
		name:    "[method]incoming-request.headers",
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_http_request_consume": {
		// (self, retptr) → (). result<incoming-body, _>: disc@+0,
		// body handle@+4 on Ok. Transfers body ownership to caller.
		module:  "wasi:http/types@0.2.0",
		name:    "[method]incoming-request.consume",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_http_request_drop": {
		// (self) → (). Drops the incoming-request resource. Must
		// happen after every borrowing accessor + after consume's
		// returned body has been finished.
		module:  "wasi:http/types@0.2.0",
		name:    "[resource-drop]incoming-request",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_http_incoming_body_stream": {
		// (self, retptr) → (). result<input-stream, _>: disc@+0,
		// stream handle@+4 on Ok.
		module:  "wasi:http/types@0.2.0",
		name:    "[method]incoming-body.stream",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_http_incoming_body_finish": {
		// (this) → future-trailers handle. Consumes the body,
		// returns the trailers future. We drop the future
		// immediately — trailers aren't surfaced to user handlers
		// yet.
		module:  "wasi:http/types@0.2.0",
		name:    "[static]incoming-body.finish",
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_http_future_trailers_drop": {
		// (handle) → (). Drops the future-trailers without
		// polling it.
		module:  "wasi:http/types@0.2.0",
		name:    "[resource-drop]future-trailers",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_http_fields_new": {
		// () → i32 (fresh fields resource handle).
		module:  "wasi:http/types@0.2.0",
		name:    "[constructor]fields",
		params:  nil,
		results: []byte{encode.ValtypeI32},
	},
	"wasi_http_fields_entries": {
		// (self, retptr) → (). list<tuple<field-name, field-value>>
		// landed at retptr: (data_ptr, count) = 8 bytes. Each entry
		// is 16 bytes: name@+0/+4, value@+8/+12.
		module:  "wasi:http/types@0.2.0",
		name:    "[method]fields.entries",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_http_fields_append": {
		// (self, name_data, name_len, value_data, value_len,
		// retptr) → (). result<_, header-error>: 1 disc + 1
		// header-error disc = 2 i32s, lands at retptr (exceeds the
		// canonical max-flat-results=1 threshold for inline-result
		// returns).
		module:  "wasi:http/types@0.2.0",
		name:    "[method]fields.append",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_http_fields_drop": {
		// (handle) → (). Drops a fields resource. The incoming-
		// request headers fields gets dropped after the lang
		// HeaderMap is populated; outgoing-response takes ownership
		// of its construction-arg fields so no manual drop there.
		module:  "wasi:http/types@0.2.0",
		name:    "[resource-drop]fields",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_http_response_new": {
		// (headers) → outgoing-response handle. Consumes the
		// fields argument.
		module:  "wasi:http/types@0.2.0",
		name:    "[constructor]outgoing-response",
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_http_response_set_status": {
		// (self, status) → disc. result<_, _>: payload-less on
		// both arms, flattens to a single discriminant slot the
		// host returns inline (no retptr).
		module:  "wasi:http/types@0.2.0",
		name:    "[method]outgoing-response.set-status-code",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_http_response_body": {
		// (self, retptr) → (). result<outgoing-body, _>: disc@+0,
		// body handle@+4 on Ok.
		module:  "wasi:http/types@0.2.0",
		name:    "[method]outgoing-response.body",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_http_outgoing_body_write": {
		// (self, retptr) → (). result<output-stream, _>: disc@+0,
		// stream handle@+4.
		module:  "wasi:http/types@0.2.0",
		name:    "[method]outgoing-body.write",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_http_outgoing_body_finish": {
		// (self, option-trailers-disc, option-trailers-value,
		//  retptr) → (). Static method that closes the body —
		// must be called before the response is handed to
		// response-outparam.set so the parent has no live
		// outgoing-body child.
		module:  "wasi:http/types@0.2.0",
		name:    "[static]outgoing-body.finish",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_http_response_outparam_set": {
		// (outparam, disc, payload[7 slots, slot 2 = i64]) → ().
		// 9 params total: the response-outparam handle + the
		// flattened `result<outgoing-response, error-code>`.
		// Slot 2 of the payload is i64 because an error-code
		// case carries `option<u64>` (HTTP body-size) and the
		// canonical-ABI joins the variant width up to the wider
		// type.
		module:  "wasi:http/types@0.2.0",
		name:    "[static]response-outparam.set",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI64, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
}

// importNeeds is parallel to runtimeNeeds but for imports.
type importNeeds struct {
	order []string
	set   map[string]bool
}

func (in *importNeeds) add(name string) {
	if in.set == nil {
		in.set = map[string]bool{}
	}
	if in.set[name] {
		return
	}
	in.set[name] = true
	in.order = append(in.order, name)
}

// scanExternImports turns the program's `@import` declarations (extern
// WASM-component imports, P4 — docs/WIT-BRING-YOUR-OWN.md) into core wasm
// function imports. Only externs actually referenced by a call are emitted:
// an unused declaration costs nothing, and the component composer is only
// asked to wire imports the core module really has. Each used extern is added
// to `in` (so it gets an import funcidx, keyed by its Fern name) and returned
// in the spec overlay the import-section emitter consults.
func scanExternImports(prog *ir.Program, in *importNeeds, helpers *runtimeNeeds) (map[string]importSpec, map[string]runtimeHelperSpec, error) {
	specs := map[string]importSpec{}
	wrappers := map[string]runtimeHelperSpec{}
	if len(prog.Externs) == 0 {
		return specs, wrappers, nil
	}
	used := map[string]bool{}
	for _, fn := range prog.Funcs {
		for _, op := range fn.Ops {
			if op.Str != "" {
				used[op.Str] = true
			}
		}
	}
	for _, ex := range prog.Externs {
		// A lazily-iterated u8 stream import is reached only through its `f$open`
		// companion (the checker desugars `for x in f()` to `f$open()` +
		// `__stream_next_u8` + `__stream_drop`, never a bare `f()`), so the extern
		// is "used" if EITHER its own name or its `$open` companion appears.
		usedOpen := used[ex.Name+"$open"]
		if !used[ex.Name] && !usedOpen {
			continue
		}
		// Parameters must be scalar, `string`, a numeric array, or a record
		// (struct) of 32-/64-bit numeric fields — the memory params a wrapper
		// can lower below. A `string` normalizes its SSO pair to a heap buffer;
		// a numeric array (`u8[]`, `i32[]`, `f64[]`, …) passes its element
		// pointer + length-prefix directly (zero-copy, native stride); a record
		// flattens to its fields (loaded off the struct value). The lowerable
		// records were resolved to a field layout during IR lowering
		// (ex.ParamRecords); a struct param without one (sub-word / composite
		// fields, > 16 fields) is rejected here, as are bool arrays.
		hasStringParam, hasMemParam := false, false
		for i, p := range ex.Params {
			switch {
			case isStringType(p.Type):
				hasStringParam, hasMemParam = true, true
			case isScalarArrayParamType(p.Type):
				hasMemParam = true
			case isBoolArrayParamType(p.Type):
				// bool[]: byte-repacked to canonical list<bool> (1 byte/elem).
				hasMemParam = true
			case ex.ParamRecords[i] != nil:
				hasMemParam = true
			case ex.ParamEnums[i] != nil:
				// option/result: flattens to (disc, payload), read off the box.
				hasMemParam = true
			case ex.ParamPlainEnums[i]:
				// plain (payloadless) enum → WIT enum: a single i32 disc read off
				// the Fern sentinel/box pointer.
				hasMemParam = true
			case externScalarType(p.Type):
				// plain scalar — passes through, no wrapper needed for this param
			default:
				switch p.Type.(type) {
				case ast.StructType, ast.TupleType:
					return nil, nil, fmt.Errorf("@import %q (%s/%s): record/tuple parameter %q (type %s) is not lowerable yet — every field must be a 32-/64-bit integer or float and there must be at most %d of them (P4c)", ex.Name, ex.Iface, ex.WITName, p.Name, p.Type, 16)
				case ast.EnumType:
					return nil, nil, fmt.Errorf("@import %q (%s/%s): enum parameter %q (type %s) is not lowerable yet — only Option[T] / Result[T, E] with a 32-/64-bit numeric/float payload (Result's arms same width) are supported (P4c)", ex.Name, ex.Iface, ex.WITName, p.Name, p.Type)
				}
				return nil, nil, fmt.Errorf("@import %q (%s/%s): parameter %q has type %s; only scalar, string, numeric-array (u8[]/i32[]/f64[]/…), record, tuple, and option/result extern parameters are supported yet (P4c)", ex.Name, ex.Iface, ex.WITName, p.Name, p.Type)
			}
		}
		params, err := paramValtypes(ex.Params)
		if err != nil {
			return nil, nil, fmt.Errorf("@import %q (%s/%s): %w", ex.Name, ex.Iface, ex.WITName, err)
		}

		ret := ex.ReturnType
		isVoid := ret == nil
		if !isVoid {
			_, isVoid = ret.(ast.VoidType)
		}

		// An `@import ... async function` (WASI Preview-3 colorless async import,
		// docs/WASI-PREVIEW3-ASYNC-PLAN.md): the raw import is lowered with
		// `canon lower async`, whose core signature appends a return-area pointer
		// and returns an i32 status — `(scalar params…, retptr) -> i32`. The Fern
		// name resolves to a generated wrapper that allocates the return area,
		// calls the raw import, and (sync-completion case) drops the status and
		// reads the result inline, so the source-level call stays colorless. This
		// slice covers scalar params + a scalar result (the proven `dep(): i32`
		// shape); a string/array/composite async param or result is rejected until
		// its slice lands. The enclosing caller must be `async`-lifted (it owns the
		// task that awaits) — the `async function` keyword provides that.
		if ex.Async {
			// Every async-import wrapper runs the pending-await loop after the
			// lowered call, so it imports the waitable-set / subtask intrinsics
			// (provided by the composer's canon waitable-set.* / subtask.drop).
			// in.add is idempotent, so registering them per async import is fine.
			in.add("async_ws_new")
			in.add("async_w_join")
			in.add("async_ws_wait")
			in.add("async_subtask_drop")
			in.add("async_ws_drop")
			// A `stream[T]` PARAMETER (the checker rewrote it to `T[]` and recorded
			// StreamParamElems) is produced as a stream: the wrapper creates a stream,
			// passes the readable end to the lower, write-streams the eager array's
			// elements over the wire, drops-writable, then awaits the host's subtask
			// (docs/STREAM-TYPE-SURFACE.md). This slice supports exactly one stream
			// param + a scalar result; other shapes are rejected.
			if len(ex.StreamParamElems) > 0 {
				if !(len(ex.Params) == 1 && ex.StreamParamElems[0] != nil && externScalarType(ret)) {
					return nil, nil, fmt.Errorf("@import %q (%s/%s): an async stream[T] parameter is supported only as the sole parameter with a scalar result so far (docs/STREAM-TYPE-SURFACE.md)", ex.Name, ex.Iface, ex.WITName)
				}
				results, err := resultValtypes(ret)
				if err != nil {
					return nil, nil, fmt.Errorf("@import %q (%s/%s): %w", ex.Name, ex.Iface, ex.WITName, err)
				}
				in.add("async_stream_new")
				in.add("async_stream_write")
				in.add("async_stream_drop_writable")
				rawName := ex.Name + "$import"
				// raw lower: (stream readable handle, retptr) -> i32 status.
				specs[rawName] = importSpec{module: ex.Iface, name: ex.WITName, params: []byte{encode.ValtypeI32, encode.ValtypeI32}, results: []byte{encode.ValtypeI32}}
				in.add(rawName)
				wrappers[ex.Name] = runtimeHelperSpec{
					params:  params, // the Fern T[] arg (element pointer)
					results: results,
					body:    buildExternAsyncStreamParamWrapper(rawName, results[0], scalarArrayElemStride(ex.Params[0].Type)),
				}
				helpers.add(ex.Name)
				helpers.add("__fern_alloc")
				continue
			}
			// A `stream[T]` result (the checker rewrote ReturnType to `T[]` and set
			// StreamResultElem) is delivered incrementally: the raw `canon lower
			// async` returns the stream readable handle, and the collect-wrapper
			// drives `stream.read` + the await loop into a grow-on-demand Fern array
			// until EOF (docs/STREAM-TYPE-SURFACE.md). It reuses the waitable
			// intrinsics above plus stream.read / stream.drop-readable. Only scalar
			// params are supported alongside a stream result for now.
			if ex.StreamResultElem != nil {
				if hasMemParam {
					return nil, nil, fmt.Errorf("@import %q (%s/%s): an async stream[T] result with a composite (string/array/record) parameter is not supported yet (docs/STREAM-TYPE-SURFACE.md)", ex.Name, ex.Iface, ex.WITName)
				}
				in.add("async_stream_read")
				in.add("async_stream_drop_readable")
				rawName := ex.Name + "$import"
				rawParams := append(append([]byte{}, params...), encode.ValtypeI32) // scalar params + retptr
				specs[rawName] = importSpec{module: ex.Iface, name: ex.WITName, params: rawParams, results: []byte{encode.ValtypeI32}}
				in.add(rawName)
				helpers.add("__fern_alloc")
				// VALUE context (`var b: u8[] = f()`, or eager `for x in f()` over a
				// non-u8 stream): the collect-wrapper drains the whole stream to EOF
				// into a Fern array, materialised under the Fern name `f`.
				if used[ex.Name] {
					wrappers[ex.Name] = runtimeHelperSpec{
						params:  params,
						results: []byte{encode.ValtypeI32}, // Fern array (element pointer)
						body:    buildExternAsyncStreamResultWrapper(len(ex.Params), rawName, scalarArrayElemStride(ex.ReturnType)),
					}
					helpers.add(ex.Name)
				}
				// LAZY context (`for x in f()` over a scalar stream): the checker
				// desugared it to `f$open()` + `__stream_next` + `__stream_elem_<kind>`
				// + `__stream_drop` (docs/STREAM-TYPE-SURFACE.md, L2). Emit the
				// open-wrapper (collect prologue → cursor), the two generic helpers
				// (`__stream_next` / `__stream_drop`, registered once, idempotent across
				// stream imports), and the per-element-type value loader for THIS
				// import's element kind.
				if usedOpen {
					openName := ex.Name + "$open"
					wrappers[openName] = runtimeHelperSpec{
						params:  params,
						results: []byte{encode.ValtypeI32}, // cursor pointer
						body:    buildExternAsyncStreamOpenWrapper(len(ex.Params), rawName),
					}
					helpers.add(openName)
					wrappers["__stream_next"] = runtimeHelperSpec{
						params:  []byte{encode.ValtypeI32},
						results: []byte{encode.ValtypeI32}, // 1 = element read, 0 = EOF
						body:    buildStreamNext(),
					}
					helpers.add("__stream_next")
					wrappers["__stream_drop"] = runtimeHelperSpec{
						params:  []byte{encode.ValtypeI32},
						results: nil,
						body:    buildStreamDropReadable(),
					}
					helpers.add("__stream_drop")
					elemName := "__stream_elem_" + ast.StreamElemKind(ex.StreamResultElem)
					wrappers[elemName] = runtimeHelperSpec{
						params:  []byte{encode.ValtypeI32},
						results: []byte{externRecordFieldValtype(ex.StreamResultElem)},
						body:    buildStreamElem(ex.StreamResultElem),
					}
					helpers.add(elemName)
				}
				continue
			}
			if hasMemParam {
				// Composite ARGUMENT(s) to an async import. This slice supports any mix
				// of scalar / `string` / numeric-array (`u8[]`/`i32[]`/`f64[]`/…) params
				// with a scalar result — the multi-arg edge-handler shape (e.g.
				// `fetch(url: string, timeout: i32)`, `post(id: i32, body: u8[])`). The
				// wrapper marshals each arg to its canonical slot(s) in this module's
				// memory (which the callee reads via the lower's memory option): a scalar
				// passes through, a string is SSO-normalised to (ptr, len), a numeric
				// array forwards (elemPtr, count@ptr-4) directly. It then runs the async
				// lower `(canon params…, retptr) -> status` and reads the scalar result.
				// Records / bool[] / enum params, or a composite result alongside, are
				// not supported yet.
				// Composite ARGUMENT(s) to an async import. The async path now accepts
				// every parameter shape the sync @import path does — scalar / `string` /
				// numeric+bool array / record / tuple / option / result — via the shared
				// marshalling head (emitExternParamMarshal); unlowerable params were
				// already rejected by the param-validation loop above. The result may be
				// a scalar, a `string`, or a numeric `list<T>` (string/list results
				// materialise in this module's memory via the lower's realloc option, so
				// NeedsRealloc + cabi_realloc). A composite (record/tuple/option) result
				// is not supported yet.
				anyString := false
				for _, p := range ex.Params {
					if isStringType(p.Type) {
						anyString = true
					}
				}
				resScalar := externScalarType(ret)
				resString := isStringType(ret)
				resList := isScalarArrayParamType(ret)
				if !(resScalar || resString || resList) {
					return nil, nil, fmt.Errorf("@import %q (%s/%s): an async extern result must be a scalar, string, or numeric array so far (a composite record/tuple/option result is not supported) (docs/WASI-PREVIEW3-ASYNC-PLAN.md)", ex.Name, ex.Iface, ex.WITName)
				}
				rawParams, err := canonicalExternParamValtypes(ex)
				if err != nil {
					return nil, nil, fmt.Errorf("@import %q (%s/%s): %w", ex.Name, ex.Iface, ex.WITName, err)
				}
				rawParams = append(rawParams, encode.ValtypeI32) // trailing retptr
				rawName := ex.Name + "$import"
				// raw import: (canon params…, retptr) -> i32 status.
				specs[rawName] = importSpec{module: ex.Iface, name: ex.WITName, params: rawParams, results: []byte{encode.ValtypeI32}}
				in.add(rawName)
				var resKind asyncResultKind
				var wrapResults []byte
				var resultVT byte
				var stride uint32
				switch {
				case resString:
					resKind = asyncResString
					wrapResults = []byte{encode.ValtypeI32, encode.ValtypeI32} // Fern heap string (data, len)
				case resList:
					resKind = asyncResList
					wrapResults = []byte{encode.ValtypeI32} // Fern array (element pointer)
					stride = scalarArrayElemStride(ret)
				default:
					resKind = asyncResScalar
					results, err := resultValtypes(ret)
					if err != nil {
						return nil, nil, fmt.Errorf("@import %q (%s/%s): %w", ex.Name, ex.Iface, ex.WITName, err)
					}
					wrapResults = results
					resultVT = results[0]
				}
				wrappers[ex.Name] = runtimeHelperSpec{
					params:  params, // Fern flattening: string → (data, len); scalar/array → 1
					results: wrapResults,
					body:    buildExternAsyncMemParamWrapper(ex, rawName, resKind, resultVT, stride),
				}
				helpers.add(ex.Name)
				helpers.add("__fern_alloc")
				if anyString {
					helpers.add("__fern_str_len")
					helpers.add("__fern_str_byte")
				}
				if resString {
					helpers.add("__bytes_to_lang_string")
					helpers.add("cabi_realloc")
				}
				if resList {
					helpers.add("cabi_realloc")
				}
				continue
			}
			rawName := ex.Name + "$import"
			// raw import: (scalar params…, retptr) -> i32 status. The retptr
			// return area receives the result the host writes before the lowered
			// `canon lower async` call returns (sync-completion case).
			rawParams := append(append([]byte{}, params...), encode.ValtypeI32)
			switch {
			case externScalarType(ret):
				results, err := resultValtypes(ret)
				if err != nil {
					return nil, nil, fmt.Errorf("@import %q (%s/%s): %w", ex.Name, ex.Iface, ex.WITName, err)
				}
				specs[rawName] = importSpec{module: ex.Iface, name: ex.WITName, params: rawParams, results: []byte{encode.ValtypeI32}}
				in.add(rawName)
				wrappers[ex.Name] = runtimeHelperSpec{
					params:  params,
					results: results,
					body:    buildExternAsyncScalarResultWrapper(len(ex.Params), rawName, results[0]),
				}
				helpers.add(ex.Name)
				helpers.add("__fern_alloc")
			case isStringType(ret):
				// string / list<u8> async result: the return area holds the
				// canonical (ptr, len); the host materialises the bytes in this
				// module's memory via `canon lower async`'s realloc option (so the
				// module must export cabi_realloc). The wrapper allocs the return
				// area, calls the raw import, drops the status, and lifts (ptr,len)
				// into a Fern string — the async counterpart of the P4c
				// string-result extern wrapper.
				specs[rawName] = importSpec{module: ex.Iface, name: ex.WITName, params: rawParams, results: []byte{encode.ValtypeI32}}
				in.add(rawName)
				wrappers[ex.Name] = runtimeHelperSpec{
					params:  params,
					results: []byte{encode.ValtypeI32, encode.ValtypeI32}, // Fern heap string (data, len)
					body:    buildExternAsyncStringResultWrapper(len(ex.Params), rawName),
				}
				helpers.add(ex.Name)
				helpers.add("__fern_alloc")
				helpers.add("__bytes_to_lang_string")
				helpers.add("cabi_realloc")
			case isScalarArrayParamType(ret):
				// list<T> async result (numeric element) lifted into a Fern T[]: the
				// return area holds the canonical (ptr, len) the host materialises in
				// this module's memory via the lower's realloc option; the wrapper
				// drops the status and copies count*stride bytes past a length prefix
				// — the async counterpart of the P4c list-result extern wrapper.
				specs[rawName] = importSpec{module: ex.Iface, name: ex.WITName, params: rawParams, results: []byte{encode.ValtypeI32}}
				in.add(rawName)
				wrappers[ex.Name] = runtimeHelperSpec{
					params:  params,
					results: []byte{encode.ValtypeI32}, // Fern array (element pointer)
					body:    buildExternAsyncListResultWrapper(len(ex.Params), rawName, scalarArrayElemStride(ret)),
				}
				helpers.add(ex.Name)
				helpers.add("__fern_alloc")
				helpers.add("cabi_realloc")
			default:
				return nil, nil, fmt.Errorf("@import %q (%s/%s): async extern result %s is not supported yet — only a scalar (i32/i64/f32/f64), string, or numeric array (u8[]/i32[]/f64[]/…); void/bool-array/record/option results are not supported (docs/WASI-PREVIEW3-ASYNC-PLAN.md)", ex.Name, ex.Iface, ex.WITName, ret)
			}
			continue
		}

		// A memory parameter (`string` or `u8[]`, P4c): a generated wrapper
		// normalizes each to the canonical (ptr,len) before the raw import is
		// called. The wrapper's signature mirrors the Fern flattening (`params`:
		// string→2 slots, u8[]→1), while the raw import carries the host-facing
		// canonical flattening (`rawParams`: string→2, u8[]→2). Only a
		// scalar/void result is supported alongside memory params for now (a
		// composite result there would need both marshalling directions at once).
		if hasMemParam {
			// A composite (option/result) result alongside the memory param(s): the
			// import returns indirectly through a trailing canonical retptr, and the
			// wrapper both normalizes the mem params and reads the return area into a
			// Fern enum box (both marshalling directions at once). Other composite
			// results (records/tuples) here are still unsupported.
			if ex.ResultEnum != nil {
				rawParams, err := canonicalExternParamValtypes(ex)
				if err != nil {
					return nil, nil, fmt.Errorf("@import %q (%s/%s): %w", ex.Name, ex.Iface, ex.WITName, err)
				}
				rawParams = append(rawParams, encode.ValtypeI32) // trailing retptr
				rawName := ex.Name + "$import"
				specs[rawName] = importSpec{module: ex.Iface, name: ex.WITName, params: rawParams, results: nil}
				in.add(rawName)
				wrappers[ex.Name] = runtimeHelperSpec{
					params:  params,
					results: []byte{encode.ValtypeI32},
					body:    buildExternMemParamWrapper(ex, rawName, ex.ResultEnum),
				}
				helpers.add(ex.Name)
				helpers.add("__fern_alloc")
				helpers.add("cabi_realloc")
				if hasStringParam {
					helpers.add("__fern_str_len")
					helpers.add("__fern_str_byte")
				}
				continue
			}
			if !(isVoid || externScalarType(ret)) {
				return nil, nil, fmt.Errorf("@import %q (%s/%s): a string/u8[] parameter with a non-option/result composite result is not supported yet (P4c)", ex.Name, ex.Iface, ex.WITName)
			}
			results, err := resultValtypes(ret)
			if err != nil {
				return nil, nil, fmt.Errorf("@import %q (%s/%s): %w", ex.Name, ex.Iface, ex.WITName, err)
			}
			rawParams, err := canonicalExternParamValtypes(ex)
			if err != nil {
				return nil, nil, fmt.Errorf("@import %q (%s/%s): %w", ex.Name, ex.Iface, ex.WITName, err)
			}
			rawName := ex.Name + "$import"
			specs[rawName] = importSpec{module: ex.Iface, name: ex.WITName, params: rawParams, results: results}
			in.add(rawName)
			wrappers[ex.Name] = runtimeHelperSpec{
				params:  params,
				results: results,
				body:    buildExternMemParamWrapper(ex, rawName, nil),
			}
			helpers.add(ex.Name)
			helpers.add("__fern_alloc")
			if hasStringParam {
				// emitStrNormalize's extra dependencies.
				helpers.add("__fern_str_len")
				helpers.add("__fern_str_byte")
			}
			continue
		}

		switch {
		case isVoid || externScalarType(ret):
			// Scalar / void result: the Fern name resolves straight to the
			// import (P4b).
			results, err := resultValtypes(ret)
			if err != nil {
				return nil, nil, fmt.Errorf("@import %q (%s/%s): %w", ex.Name, ex.Iface, ex.WITName, err)
			}
			specs[ex.Name] = importSpec{module: ex.Iface, name: ex.WITName, params: params, results: results}
			in.add(ex.Name)
		case isStringType(ret):
			// string / list<u8> result (P4c): canonical return-area lowering.
			// The raw import gains a trailing return-area pointer and returns
			// nothing; the Fern name resolves to a wrapper that lifts the host
			// bytes into a Fern string via __bytes_to_lang_string.
			rawName := ex.Name + "$import"
			rawParams := append(append([]byte{}, params...), encode.ValtypeI32)
			specs[rawName] = importSpec{module: ex.Iface, name: ex.WITName, params: rawParams, results: nil}
			in.add(rawName)
			wrappers[ex.Name] = runtimeHelperSpec{
				params:  params,
				results: []byte{encode.ValtypeI32, encode.ValtypeI32},
				body:    buildExternStringResultWrapper(len(ex.Params), rawName),
			}
			helpers.add(ex.Name)
			// Lift + the canonical allocator the host calls back into.
			helpers.add("__fern_alloc")
			helpers.add("__bytes_to_lang_string")
			helpers.add("cabi_realloc")
		case isScalarArrayParamType(ret):
			// list<T> result (numeric element) lifted into a Fern T[] (P4c):
			// canonical return-area lowering, then the result wrapper allocates
			// the length-prefixed array and copies the host bytes (count*stride)
			// past the prefix. u8[] (stride 1) is the original case; i32[]/f64[]
			// etc. copy wider elements at native stride.
			rawName := ex.Name + "$import"
			rawParams := append(append([]byte{}, params...), encode.ValtypeI32)
			specs[rawName] = importSpec{module: ex.Iface, name: ex.WITName, params: rawParams, results: nil}
			in.add(rawName)
			wrappers[ex.Name] = runtimeHelperSpec{
				params:  params,
				results: []byte{encode.ValtypeI32},
				body:    buildExternListResultWrapper(len(ex.Params), rawName, scalarArrayElemStride(ret)),
			}
			helpers.add(ex.Name)
			helpers.add("__fern_alloc")
			helpers.add("cabi_realloc")
		case isBoolArrayParamType(ret):
			// list<bool> result lifted into a Fern bool[] (P4c): the canonical
			// element is 1 byte but a Fern bool array slot is 4 bytes, so the
			// wrapper byte-EXPANDS each host byte into a 4-byte i32 element
			// (vs the straight memory.copy the numeric-array wrapper uses).
			rawName := ex.Name + "$import"
			rawParams := append(append([]byte{}, params...), encode.ValtypeI32)
			specs[rawName] = importSpec{module: ex.Iface, name: ex.WITName, params: rawParams, results: nil}
			in.add(rawName)
			wrappers[ex.Name] = runtimeHelperSpec{
				params:  params,
				results: []byte{encode.ValtypeI32},
				body:    buildExternBoolListResultWrapper(len(ex.Params), rawName),
			}
			helpers.add(ex.Name)
			helpers.add("__fern_alloc")
			helpers.add("cabi_realloc")
		case ex.ResultRecord != nil && ex.ResultRecord.Direct:
			// single-field record/tuple result (P4c): flattens to exactly one
			// core value, so the canonical ABI returns it by value — the raw
			// import returns the field's valtype directly (no return area). The
			// wrapper materializes the one-field Fern struct/tuple from it.
			rawName := ex.Name + "$import"
			fieldVT := externRecordFieldValtype(ex.ResultRecord.Fields[0].Type)
			specs[rawName] = importSpec{module: ex.Iface, name: ex.WITName, params: params, results: []byte{fieldVT}}
			in.add(rawName)
			wrappers[ex.Name] = runtimeHelperSpec{
				params:  params,
				results: []byte{encode.ValtypeI32},
				body:    buildExternRecordResultDirectWrapper(len(ex.Params), rawName, ex.ResultRecord),
			}
			helpers.add(ex.Name)
			helpers.add("__fern_alloc")
		case ex.ResultRecord != nil:
			// record result (P4c): a multi-field record flattens to > 1 core
			// value, so the canonical ABI returns it indirectly — the raw import
			// gains a trailing return-area pointer and returns nothing. The
			// wrapper reads each field from the area and materializes a Fern
			// struct (rc header + field stores), returning its pointer.
			rawName := ex.Name + "$import"
			rawParams := append(append([]byte{}, params...), encode.ValtypeI32)
			specs[rawName] = importSpec{module: ex.Iface, name: ex.WITName, params: rawParams, results: nil}
			in.add(rawName)
			wrappers[ex.Name] = runtimeHelperSpec{
				params:  params,
				results: []byte{encode.ValtypeI32},
				body:    buildExternRecordResultWrapper(len(ex.Params), rawName, ex.ResultRecord),
			}
			helpers.add(ex.Name)
			helpers.add("__fern_alloc")
			helpers.add("cabi_realloc")
		case ex.ResultEnum != nil:
			// option/result result (P4c): flattens to (disc, payload) > 1 core
			// value, so it returns indirectly through a return-area pointer
			// (disc:u8 @0, payload @off). The wrapper reads them and materializes
			// a Fern enum box (tag remapped back for option).
			rawName := ex.Name + "$import"
			rawParams := append(append([]byte{}, params...), encode.ValtypeI32)
			specs[rawName] = importSpec{module: ex.Iface, name: ex.WITName, params: rawParams, results: nil}
			in.add(rawName)
			wrappers[ex.Name] = runtimeHelperSpec{
				params:  params,
				results: []byte{encode.ValtypeI32},
				body:    buildExternEnumResultWrapper(len(ex.Params), rawName, ex.ResultEnum),
			}
			helpers.add(ex.Name)
			helpers.add("__fern_alloc")
			helpers.add("cabi_realloc")
		case ex.ResultPlainEnumN > 0:
			// WIT `enum` result: the import returns a single i32 discriminant; the
			// wrapper maps it to the matching static per-tag sentinel (`[tag @0]`)
			// via __enum_sent, so a Fern payloadless enum value is produced with no
			// heap allocation (the sentinels are shared, immortal data cells).
			rawName := ex.Name + "$import"
			specs[rawName] = importSpec{module: ex.Iface, name: ex.WITName, params: params, results: []byte{encode.ValtypeI32}}
			in.add(rawName)
			wrappers[ex.Name] = runtimeHelperSpec{
				params:  params,
				results: []byte{encode.ValtypeI32},
				body:    buildExternPlainEnumResultWrapper(len(ex.Params), rawName),
			}
			helpers.add(ex.Name)
			helpers.add("__enum_sent")
		default:
			switch ret.(type) {
			case ast.StructType, ast.TupleType:
				return nil, nil, fmt.Errorf("@import %q (%s/%s): record/tuple result (type %s) is not lowerable yet — it must have 1..%d fields, each a 32-/64-bit integer or float (P4c)", ex.Name, ex.Iface, ex.WITName, ret, 16)
			case ast.EnumType:
				return nil, nil, fmt.Errorf("@import %q (%s/%s): enum result (type %s) is not lowerable yet — only Option[T] / Result[T, E] with a 32-/64-bit numeric/float payload (Result's arms same width) are supported (P4c)", ex.Name, ex.Iface, ex.WITName, ret)
			}
			return nil, nil, fmt.Errorf("@import %q (%s/%s): return type %s is not supported yet — only scalar, string, numeric-array (u8[]/i32[]/f64[]/…), record, tuple, and option/result results are (P4c)", ex.Name, ex.Iface, ex.WITName, ret)
		}
	}
	return specs, wrappers, nil
}

// scanImports decides which imports the module needs based on
// the helpers in use (and direct IR-op references in a future
// expansion). Each helper that wraps a WASI call adds its import
// here.
func scanImports(prog *ir.Program, helpers runtimeNeeds, opts EmitOptions) importNeeds {
	var in importNeeds
	if helpers.set["__fern_print"] {
		if opts.Preview2WASI {
			in.add("wasi_get_stdout_p2")
			in.add("wasi_blocking_write_and_flush_p2")
		} else {
			in.add("wasi_fd_write")
		}
	}
	if helpers.set["__fern_eprint"] {
		if opts.Preview2WASI {
			in.add("wasi_get_stderr_p2")
			in.add("wasi_blocking_write_and_flush_p2")
		} else {
			in.add("wasi_fd_write")
		}
	}
	if helpers.set["__fern_write"] {
		if opts.Preview2WASI {
			in.add("wasi_get_stdout_p2")
			in.add("wasi_blocking_write_and_flush_p2")
		} else {
			in.add("wasi_fd_write")
		}
	}
	if helpers.set["__fern_putchar"] {
		if opts.Preview2WASI {
			in.add("wasi_get_stdout_p2")
			in.add("wasi_blocking_write_and_flush_p2")
		} else {
			in.add("wasi_fd_write")
		}
	}
	if helpers.set["__fern_exit"] {
		in.add("wasi_proc_exit")
	}
	if helpers.set["__fern_random_i32"] {
		if opts.Preview2WASI {
			in.add("wasi_random_get_u64_p2")
		} else {
			in.add("wasi_random_get")
		}
	}
	if helpers.set["__fern_random_bytes"] {
		if opts.Preview2WASI {
			in.add("wasi_random_get_u64_p2")
		} else {
			in.add("wasi_random_get")
		}
	}
	if helpers.set["__fern_now_ns"] {
		if opts.Preview2WASI {
			in.add("wasi_wall_clock_now_p2")
		} else {
			in.add("wasi_clock_time_get")
		}
	}
	if helpers.set["__fern_now_unix_ms"] {
		if opts.Preview2WASI {
			in.add("wasi_wall_clock_now_p2")
		} else {
			in.add("wasi_clock_time_get")
		}
	}
	if helpers.set["__fern_monotonic_ns"] {
		if opts.Preview2WASI {
			in.add("wasi_monotonic_now_p2")
		} else {
			in.add("wasi_clock_time_get")
		}
	}
	if helpers.set["__fern_wasm_timer_pollable"] {
		// Preview-2-only: the timer pollable comes from
		// monotonic-clock.subscribe-duration.
		in.add("wasi_clocks_subscribe_duration")
	}
	if helpers.set["__fern_wasm_block"] {
		// Preview-2-only: block on a wasi:io/poll pollable.
		in.add("wasi_io_pollable_block")
	}
	if helpers.set["__fern_wasm_poll"] {
		// Preview-2-only: multiplex a list of pollables.
		in.add("wasi_io_poll_poll")
	}
	if helpers.set["__fern_wasm_pollable_drop"] {
		// Preview-2-only: drop a consumed pollable handle.
		in.add("wasi_io_pollable_drop")
	}
	if helpers.set["__fern_env_count"] {
		in.add("wasi_environ_sizes_get")
	}
	if helpers.set["__fern_arg_count"] {
		if opts.Preview2WASI {
			in.add("wasi_get_arguments_p2")
		} else {
			in.add("wasi_args_sizes_get")
		}
	}
	if helpers.set["__fern_arg_at"] {
		if opts.Preview2WASI {
			in.add("wasi_get_arguments_p2")
		} else {
			in.add("wasi_args_sizes_get")
			in.add("wasi_args_get")
		}
	}
	if helpers.set["__fern_args"] {
		if opts.Preview2WASI {
			in.add("wasi_get_arguments_p2")
		} else {
			in.add("wasi_args_sizes_get")
			in.add("wasi_args_get")
		}
	}
	if helpers.set["__fern_env_at"] {
		in.add("wasi_environ_sizes_get")
		in.add("wasi_environ_get")
	}
	if helpers.set["__fern_env"] {
		if opts.Preview2WASI {
			in.add("wasi_get_environment_p2")
		} else {
			in.add("wasi_environ_sizes_get")
			in.add("wasi_environ_get")
		}
	}
	if helpers.set["__fern_read_byte"] {
		if opts.Preview2WASI {
			in.add("wasi_get_stdin_p2")
			in.add("wasi_io_blocking_read")
		} else {
			in.add("wasi_fd_read")
		}
	}
	if helpers.set["__fern_read_file"] {
		if opts.Preview2WASI {
			in.add("wasi_get_directories_p2")
			in.add("wasi_descriptor_open_at_p2")
			in.add("wasi_descriptor_read_via_stream_p2")
			in.add("wasi_io_blocking_read")
		} else {
			in.add("wasi_path_open")
			in.add("wasi_fd_read")
			in.add("wasi_fd_close")
		}
	}
	if helpers.set["__fern_write_file"] {
		if opts.Preview2WASI {
			in.add("wasi_get_directories_p2")
			in.add("wasi_descriptor_open_at_p2")
			in.add("wasi_descriptor_write_via_stream_p2")
			in.add("wasi_blocking_write_and_flush_p2")
		} else {
			in.add("wasi_path_open")
			in.add("wasi_fd_write")
			in.add("wasi_fd_close")
		}
	}
	// Directory + metadata helpers (#6208). All five have preview-2
	// halves now.
	if helpers.set["__fern_remove_file"] {
		if opts.Preview2WASI {
			in.add("wasi_get_directories_p2")
			in.add("wasi_descriptor_unlink_file_at_p2")
		} else {
			in.add("wasi_path_unlink_file")
		}
	}
	if helpers.set["__fern_stat"] {
		if opts.Preview2WASI {
			in.add("wasi_get_directories_p2")
			in.add("wasi_descriptor_stat_at_p2")
		} else {
			in.add("wasi_path_filestat_get")
		}
	}
	if helpers.set["__fern_open_dir"] {
		if opts.Preview2WASI {
			in.add("wasi_get_directories_p2")
			in.add("wasi_descriptor_open_at_p2")
		} else {
			in.add("wasi_path_open")
		}
	}
	if helpers.set["__fern_read_dir_raw"] {
		if opts.Preview2WASI {
			in.add("wasi_descriptor_read_directory_p2")
			in.add("wasi_dir_entry_stream_read_p2")
			in.add("wasi_dir_entry_stream_drop_p2")
		} else {
			in.add("wasi_fd_readdir")
		}
	}
	if helpers.set["__fern_read_dir"] && !opts.Preview2WASI {
		// preview-1 closes the listing fd itself; the preview-2 body
		// drops the entry stream inside __fern_read_dir_raw instead.
		in.add("wasi_fd_close")
	}
	if helpers.set["__fern_rmdir_rec"] {
		if opts.Preview2WASI {
			in.add("wasi_get_directories_p2")
			in.add("wasi_descriptor_unlink_file_at_p2")
			in.add("wasi_descriptor_remove_directory_at_p2")
		} else {
			in.add("wasi_path_unlink_file")
			in.add("wasi_path_remove_directory")
			in.add("wasi_fd_close")
		}
	}
	if helpers.set["__fern_temp_dir"] || helpers.set["__fern_create_dir_all"] {
		if opts.Preview2WASI {
			in.add("wasi_get_directories_p2")
			in.add("wasi_descriptor_create_directory_at_p2")
		} else {
			in.add("wasi_path_create_directory")
		}
	}
	if helpers.set["__fern_open_reader"] {
		if opts.Preview2WASI {
			// open_reader opens via the get-directories → open-at →
			// read-via-stream chain and stores the stream handle in the
			// Reader; the Reader's read methods pull in blocking-read.
			in.add("wasi_get_directories_p2")
			in.add("wasi_descriptor_open_at_p2")
			in.add("wasi_descriptor_read_via_stream_p2")
		} else {
			in.add("wasi_path_open")
		}
	}
	if helpers.set["__fern_open_writer"] {
		if opts.Preview2WASI {
			// open_writer opens via get-directories → open-at →
			// write-via-stream and stores the output-stream handle in the
			// Writer; the Writer's write method pulls in
			// blocking-write-and-flush.
			in.add("wasi_get_directories_p2")
			in.add("wasi_descriptor_open_at_p2")
			in.add("wasi_descriptor_write_via_stream_p2")
		} else {
			in.add("wasi_path_open")
		}
	}
	if helpers.set["__fern_open_appender"] {
		if opts.Preview2WASI {
			// open_appender opens via get-directories → open-at(create,
			// no truncate) → append-via-stream and stores the EOF-
			// positioned output-stream handle in the Writer; the Writer's
			// write method pulls in blocking-write-and-flush.
			in.add("wasi_get_directories_p2")
			in.add("wasi_descriptor_open_at_p2")
			in.add("wasi_descriptor_append_via_stream_p2")
		} else {
			in.add("wasi_path_open")
		}
	}
	if helpers.set["__fern_stdin"] && opts.Preview2WASI {
		// Preview-2 stdin Reader holds the get-stdin input-stream handle.
		in.add("wasi_get_stdin_p2")
	}
	if helpers.set["__fern_reader_read_line_fd"] {
		if opts.Preview2WASI {
			in.add("wasi_io_blocking_read")
		} else {
			in.add("wasi_fd_read")
		}
	}
	if helpers.set["__fern_reader_read_chunk"] {
		if opts.Preview2WASI {
			in.add("wasi_io_blocking_read")
		} else {
			in.add("wasi_fd_read")
		}
	}
	if helpers.set["__fern_writer_write"] {
		if opts.Preview2WASI {
			in.add("wasi_blocking_write_and_flush_p2")
		} else {
			in.add("wasi_fd_write")
		}
	}
	// Preview-2 stdio Writers (stdout() / stderr()) hold the cached
	// output-stream handle from get-stdout / get-stderr, so importing
	// either getter is gated on the matching constructor being present.
	if opts.Preview2WASI && helpers.set["__fern_stdout"] {
		in.add("wasi_get_stdout_p2")
	}
	if opts.Preview2WASI && helpers.set["__fern_stderr"] {
		in.add("wasi_get_stderr_p2")
	}
	if helpers.set["__fern_reader_close_fd"] {
		if opts.Preview2WASI {
			// The Reader holds an own<input-stream> handle; close drops it
			// via canon resource.drop (the composer satisfies the import).
			in.add("wasi_io_input_stream_drop")
		} else {
			in.add("wasi_fd_close")
		}
	}
	if helpers.set["__fern_writer_close"] {
		if opts.Preview2WASI {
			// The Writer holds an own<output-stream> handle; close drops it.
			in.add("wasi_io_output_stream_drop")
		} else {
			in.add("wasi_fd_close")
		}
	}

	// TCP helpers. Each user-facing builtin (tcp_listen / tcp_accept /
	// tcp_recv / tcp_send / tcp_close) pulls in a different subset of
	// the wasi:sockets + wasi:io imports; we union them here so the
	// transitive close picks up everything needed.
	//
	// __network_handle (the lazy accessor over instance-network) is
	// pulled in by tcp_listen since that's the only call site that
	// needs the network borrow. The others reach for the cached
	// handle slot directly through the accessor.
	if helpers.set["__fern_tcp_listen"] {
		in.add("wasi_sockets_instance_network")
		in.add("wasi_sockets_create_tcp_socket")
		in.add("wasi_sockets_tcp_start_bind")
		in.add("wasi_sockets_tcp_finish_bind")
		in.add("wasi_sockets_tcp_start_listen")
		in.add("wasi_sockets_tcp_finish_listen")
	}
	if helpers.set["__fern_tcp_accept"] {
		in.add("wasi_sockets_tcp_accept")
		in.add("wasi_sockets_tcp_subscribe")
		in.add("wasi_io_pollable_block")
		in.add("wasi_io_pollable_drop")
	}
	if helpers.set["__fern_tcp_connect"] {
		in.add("wasi_sockets_instance_network")
		in.add("wasi_sockets_create_tcp_socket")
		in.add("wasi_sockets_tcp_start_connect")
		in.add("wasi_sockets_tcp_finish_connect")
		in.add("wasi_sockets_tcp_subscribe")
		in.add("wasi_io_pollable_block")
		in.add("wasi_io_pollable_drop")
	}
	if helpers.set["__fern_tcp_pollable"] {
		in.add("wasi_sockets_tcp_subscribe")
	}
	if helpers.set["__fern_tcp_recv"] {
		in.add("wasi_io_blocking_read")
	}
	if helpers.set["__fern_tcp_send"] {
		in.add("wasi_blocking_write_and_flush_p2")
	}
	if helpers.set["__fern_tcp_close"] {
		in.add("wasi_sockets_tcp_socket_drop")
		in.add("wasi_io_input_stream_drop")
		in.add("wasi_io_output_stream_drop")
	}
	// udp_send is one self-contained helper: create → bind → stream
	// (connect) → check-send → send → drop the three datagram resources.
	if helpers.set["__fern_udp_send"] {
		in.add("wasi_sockets_instance_network")
		in.add("wasi_sockets_create_udp_socket")
		in.add("wasi_sockets_udp_start_bind")
		in.add("wasi_sockets_udp_finish_bind")
		in.add("wasi_sockets_udp_stream")
		in.add("wasi_sockets_udp_check_send")
		in.add("wasi_sockets_udp_outgoing_subscribe")
		in.add("wasi_io_pollable_block")
		in.add("wasi_io_pollable_drop")
		in.add("wasi_sockets_udp_send")
		in.add("wasi_sockets_udp_socket_drop")
		in.add("wasi_sockets_incoming_datagram_stream_drop")
		in.add("wasi_sockets_outgoing_datagram_stream_drop")
	}

	// wasi:http wrapper. The single __http_entry helper pulls in
	// the full preview-2 wasi:http/types surface + the wasi:io
	// stream drop/read/write imports it needs for body marshalling.
	// Note: cabi_realloc is exported as a function (the host calls
	// it back for list<u8> allocations); we don't import it.
	if helpers.set["__http_entry"] {
		in.add("wasi_http_request_method")
		in.add("wasi_http_request_path_with_query")
		in.add("wasi_http_request_headers")
		in.add("wasi_http_request_consume")
		in.add("wasi_http_request_drop")
		in.add("wasi_http_incoming_body_stream")
		in.add("wasi_http_incoming_body_finish")
		in.add("wasi_http_future_trailers_drop")
		in.add("wasi_http_fields_new")
		in.add("wasi_http_fields_entries")
		in.add("wasi_http_fields_append")
		in.add("wasi_http_fields_drop")
		in.add("wasi_http_response_new")
		in.add("wasi_http_response_set_status")
		in.add("wasi_http_response_body")
		in.add("wasi_http_outgoing_body_write")
		in.add("wasi_http_outgoing_body_finish")
		in.add("wasi_http_response_outparam_set")
		in.add("wasi_io_blocking_read")
		in.add("wasi_blocking_write_and_flush_p2")
		in.add("wasi_io_input_stream_drop")
		in.add("wasi_io_output_stream_drop")
	}
	return in
}

// buildPrintBody assembles the wasm bytes for __fern_print.
//
// Signature: (param $data i32) (param $len i32) (result)
//
// Logical:
//
//	L   = __fern_str_len(data, len)
//	dst = __fern_alloc(L)
//	for i in 0..L: mem[dst+i] = __fern_str_byte(data, len, i)
//	mem[48..52] = dst   ; iov_base
//	mem[52..56] = L     ; iov_len
//	wasi_fd_write(1, 48, 1, 56)
//	drop result
//
// Wasm locals (after the two params):
//
//	2: $L
//	3: $dst
//	4: $i
func buildPrintBody(idxs map[string]uint32) []byte {
	return buildPrintBodyFd(idxs, 1, true)
}

// buildEprintBody — (data, len) → (). Same shape as
// buildPrintBody but writes to fd=2 (stderr) instead of fd=1.
// Also appends a trailing newline, matching the WAT path's
// fmt.Println-shaped pairing of print/eprint.
func buildEprintBody(idxs map[string]uint32) []byte {
	return buildPrintBodyFd(idxs, 2, true)
}

// buildWriteBody — (data, len) → (). Like buildPrintBody but
// without the trailing newline (fd=1, no `\n` append).
func buildWriteBody(idxs map[string]uint32) []byte {
	return buildPrintBodyFd(idxs, 1, false)
}

// buildPrintBodyFd is the fd-parametrised shared implementation
// of __fern_print / __fern_eprint / __fern_write. Same
// str-to-heap copy + fd_write path; the fd and the optional
// trailing-newline are the only deltas.
func buildPrintBodyFd(idxs map[string]uint32, fd int32, withNewline bool) []byte {
	strLen := idxs["__fern_str_len"]
	strByte := idxs["__fern_str_byte"]
	alloc := idxs["__fern_alloc"]
	fdWrite := idxs["wasi_fd_write"]
	var body []byte
	// L = __fern_str_len(data, len)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, strLen)
	body = inst.InstLocalSet(body, 2) // $L
	// dst = __fern_alloc(L + (1 if withNewline else 0)). The
	// trailing newline byte for print / eprint lives one byte
	// past the copied string content; write() skips it.
	body = inst.InstLocalGet(body, 2)
	if withNewline {
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
	}
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 3) // $dst
	// Copy loop: for i in 0..L: mem[dst+i] = __fern_str_byte(data, len, i).
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 4) // $i = 0
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 4)
		body = inst.InstLocalGet(body, 2)
		body = numeric.InstI32GeS(body)
		body = inst.InstBrIf(body, 1) // exit on $i >= $L
		// mem[dst + i] = __fern_str_byte(data, len, i)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 0)
		body = inst.InstLocalGet(body, 1)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstCall(body, strByte)
		body = memory.InstI32Store8(body, 0, 0)
		// $i++
		body = inst.InstLocalGet(body, 4)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 4)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // end loop
	body = inst.InstEnd(body) // end block
	if withNewline {
		// mem[dst + L] = '\n'  (the byte we reserved at alloc).
		// store8 takes (addr, value); push them in that order.
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 2)
		body = numeric.InstI32Add(body)
		body = inst.InstI32Const(body, '\n')
		body = memory.InstI32Store8(body, 0, 0)
		// $L = L + 1 (so the iov_len covers the newline too)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 2)
	}
	// mem[printIovecAddr] = dst (iov_base)
	body = inst.InstI32Const(body, printIovecAddr)
	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Store(body, 2, 0)
	// mem[printIovecAddr + 4] = L (iov_len)
	body = inst.InstI32Const(body, printIovecAddr+4)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Store(body, 2, 0)
	// wasi_fd_write(fd, iovec_addr, 1, ret_addr); drop result.
	body = inst.InstI32Const(body, fd)
	body = inst.InstI32Const(body, printIovecAddr)
	body = inst.InstI32Const(body, 1) // iovec count
	body = inst.InstI32Const(body, printRetAddr)
	body = inst.InstCall(body, fdWrite)
	body = inst.InstDrop(body)
	// Three i32 locals: $L, $dst, $i.
	locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildExitBody assembles the wasm bytes for __fern_exit.
//
// Signature: (param $code i32) (result)
//
// Body is a single call to wasi_proc_exit, which never returns.
// `unreachable` at the end satisfies the wasm verifier (every
// function body must structurally end somewhere even when
// execution can't actually reach it).
func buildExitBody(idxs map[string]uint32) []byte {
	procExit := idxs["wasi_proc_exit"]
	var body []byte
	body = inst.InstLocalGet(body, 0) // $code
	body = inst.InstCall(body, procExit)
	body = inst.InstUnreachable(body)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildRandomI32Body assembles the wasm bytes for __fern_random_i32.
//
// Signature: () → i32
//
// Body calls wasi_random_get(randomBufAddr, 4); ignores the
// returned errno; reads back the 4 bytes as an i32. Uses a
// fixed-address scratch instead of allocating to avoid leaking
// memory when called in a loop (the bump allocator never frees).
func buildRandomI32Body(idxs map[string]uint32) []byte {
	randomGet := idxs["wasi_random_get"]
	var body []byte
	body = inst.InstI32Const(body, randomBufAddr)
	body = inst.InstI32Const(body, 4)
	body = inst.InstCall(body, randomGet)
	body = inst.InstDrop(body) // ignore errno
	body = inst.InstI32Const(body, randomBufAddr)
	body = memory.InstI32Load(body, 2, 0)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildMapHashSeedBody assembles the wasm bytes for __fern_map_hash_seed.
//
// Signature: () → i32
//
// core/map mixes this into its FNV basis so attacker-supplied key strings
// cannot be precomputed into a colliding set offline (#6194). Per PROCESS,
// not per map: the draw goes through wasi_random_get, which a program
// creating maps freely must not pay repeatedly.
//
// The cached word IS the cache flag — zero means "not yet drawn" — so the
// drawn value is OR'd with 1. A zero seed is also core/map's "unseeded"
// sentinel, which would silently leave that map unseeded.
//
// Body: if mem[mapHashSeedAddr] == 0 { mem[..] = __fern_random_i32() | 1 };
// return mem[mapHashSeedAddr]. Works unchanged under preview 2 because it
// reaches the host only through __fern_random_i32, which has its own
// preview-2 override.
func buildMapHashSeedBody(idxs map[string]uint32) []byte {
	randomI32 := idxs["__fern_random_i32"]
	var body []byte
	body = inst.InstI32Const(body, mapHashSeedAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, mapHashSeedAddr)
	body = inst.InstCall(body, randomI32)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Or(body)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstEnd(body)
	body = inst.InstI32Const(body, mapHashSeedAddr)
	body = memory.InstI32Load(body, 2, 0)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// preview2HelperBodyOverrides maps helper names whose body bytecode
// changes under EmitOptions.Preview2WASI to the preview-2 variant.
// The override is selected at module-assembly time in
// EmitWithOptions; helpers not in the map keep their default
// body. Each override has the same (params, results) signature as
// the helper's runtimeHelperSpec — only the bytecode differs.
var preview2HelperBodyOverrides = map[string]func(map[string]uint32) []byte{
	"__fern_random_i32":          buildRandomI32BodyP2,
	"__fern_monotonic_ns":        buildMonotonicNsBodyP2,
	"__fern_random_bytes":        buildRandomBytesBodyP2,
	"__fern_print":               buildPrintBodyP2,
	"__fern_write":               buildWriteBodyP2,
	"__fern_eprint":              buildEprintBodyP2,
	"__fern_putchar":             buildPutcharBodyP2,
	"__fern_now_ns":              buildNowNsBodyP2,
	"__fern_now_unix_ms":         buildNowUnixMsBodyP2,
	"__fern_read_byte":           buildReadByteBodyP2,
	"__fern_arg_count":           buildArgCountBodyP2,
	"__fern_arg_at":              buildArgAtBodyP2,
	"__fern_args":                buildArgsBodyP2,
	"__fern_env":                 buildEnvBodyP2,
	"__fern_read_file":           buildReadFileBodyP2,
	"__fern_write_file":          buildWriteFileBodyP2,
	"__fern_stdin":               buildStdinBodyP2,
	"__fern_reader_read_line_fd": buildReaderReadLineFdBodyP2,
	"__fern_reader_read_chunk":   buildReaderReadChunkBodyP2,
	"__fern_open_reader":         buildOpenReaderBodyP2,
	"__fern_open_writer":         buildOpenWriterBodyP2,
	"__fern_open_appender":       buildOpenAppenderBodyP2,
	"__fern_writer_write":        buildWriterWriteBodyP2,
	"__fern_reader_close_fd":     buildReaderCloseFdBodyP2,
	"__fern_writer_close":        buildWriterCloseBodyP2,
	"__fern_stdout":              buildStdoutBodyP2,
	"__fern_stderr":              buildStderrBodyP2,
	"__fern_remove_file":         buildRemoveFileBodyP2,
	"__fern_create_dir_all":      buildCreateDirAllBodyP2,
	"__fern_temp_dir":            buildTempDirBodyP2,
	"__fern_stat":                buildStatBodyP2,
	"__fern_open_dir":            buildOpenDirBodyP2,
	"__fern_read_dir_raw":        buildReadDirRawBodyP2,
	"__fern_read_dir":            buildReadDirBodyP2,
	"__fern_rmdir_rec":           buildRmdirRecBodyP2,
}

// buildPrintBodyP2 is the preview-2 variant of buildPrintBody.
//
// Signature: (param $data i32) (param $len i32) (result)
//
// Logical:
//
//	if !mem[stdoutInitAddr]:
//	    mem[stdoutHandleAddr] = wasi:cli/stdout::get-stdout()
//	    mem[stdoutInitAddr]  = 1
//	L   = __fern_str_len(data, len)
//	dst = __fern_alloc(L + 1)
//	for i in 0..L: mem[dst+i] = __fern_str_byte(data, len, i)
//	mem[dst + L] = '\n'
//	retBuf = __fern_alloc(16)
//	wasi:io/streams::blocking-write-and-flush(
//	    mem[stdoutHandleAddr], dst, L+1, retBuf)
//	;; ignore the result<_, stream-error> (no error handling)
//
// Wasm locals (after the two params):
//
//	2: $L
//	3: $dst
//	4: $i
func buildPrintBodyP2(idxs map[string]uint32) []byte {
	return buildPrintLikeBodyP2(idxs, true, "wasi_get_stdout_p2", stdoutInitAddr, stdoutHandleAddr)
}

// buildWriteBodyP2 is the preview-2 variant of buildWriteBody.
// Same as buildPrintBodyP2 but skips the trailing newline. (The
// pair `print` / `write` mirrors Go's `fmt.Println` /
// `fmt.Print`.)
func buildWriteBodyP2(idxs map[string]uint32) []byte {
	return buildPrintLikeBodyP2(idxs, false, "wasi_get_stdout_p2", stdoutInitAddr, stdoutHandleAddr)
}

// buildEprintBodyP2 is the preview-2 variant of buildEprintBody.
// Same shape as buildPrintBodyP2 (newline appended) but writes
// via the stderr handle (wasi:cli/stderr::get-stderr cached in
// stderrHandleAddr) instead of stdout.
func buildEprintBodyP2(idxs map[string]uint32) []byte {
	return buildPrintLikeBodyP2(idxs, true, "wasi_get_stderr_p2", stderrInitAddr, stderrHandleAddr)
}

// buildPrintLikeBodyP2 is the shared body builder for the
// preview-2 print / write / eprint helpers. `withNewline`
// controls whether a trailing '\n' is appended; `getHandleSym`
// selects between get-stdout and get-stderr; `initAddr` /
// `handleAddr` point at the cache slots for the chosen handle.
func buildPrintLikeBodyP2(idxs map[string]uint32, withNewline bool, getHandleSym string, initAddr, handleAddr int32) []byte {
	strLen := idxs["__fern_str_len"]
	strByte := idxs["__fern_str_byte"]
	alloc := idxs["__fern_alloc"]
	getHandle := idxs[getHandleSym]
	write := idxs["wasi_blocking_write_and_flush_p2"]
	var body []byte
	// If !init: call get-<handle>, cache it, set init=1.
	body = inst.InstI32Const(body, initAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, handleAddr)
	body = inst.InstCall(body, getHandle)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstI32Const(body, initAddr)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstEnd(body)
	// L = __fern_str_len(data, len)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, strLen)
	body = inst.InstLocalSet(body, 2) // $L
	// dst = __fern_alloc($L + (1 if withNewline else 0)). The
	// trailing newline byte for print lives one byte past the
	// copied string content; write() skips it.
	body = inst.InstLocalGet(body, 2)
	if withNewline {
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
	}
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 3) // $dst
	// Copy loop: for i in 0..L: mem[dst+i] = __fern_str_byte(data, len, i).
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 4)
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 2)
	body = numeric.InstI32GeS(body)
	body = inst.InstBrIf(body, 1)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstCall(body, strByte)
	body = memory.InstI32Store8(body, 0, 0)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 4)
	body = inst.InstBr(body, 0)
	body = inst.InstEnd(body)
	body = inst.InstEnd(body)
	if withNewline {
		// mem[dst + L] = '\n'
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 2)
		body = numeric.InstI32Add(body)
		body = inst.InstI32Const(body, '\n')
		body = memory.InstI32Store8(body, 0, 0)
		// $L = L + 1 (newline included)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 2)
	}
	// blocking-write-and-flush(handle, dst, $L, retBuf).
	// handle = mem[handleAddr]; retBuf = __fern_alloc(16).
	body = inst.InstI32Const(body, handleAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstCall(body, write)
	// Result is in retBuf; we ignore it (no error handling yet).
	locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildRandomBytesBodyP2 is the preview-2 variant of
// buildRandomBytesBody.
//
// Signature: (n) → (data, len)
//
// Body:
//
//	if n == 0: return inline empty (0, 0x80000000)
//	padded = (n + 7) & ~7    -- round up to a multiple of 8
//	buf    = __fern_alloc(padded)
//	i      = 0
//	loop:
//	  if i >= padded: break
//	  i64.store(buf + i, get-random-u64())
//	  i += 8
//	return (buf, n)
//
// Allocates `padded` bytes (≤7 extra) so the trailing u64 store
// never spills past the allocation. The returned length is the
// original n — readers see exactly n bytes.
//
// Locals (after the n param):
//
//	1: $buf
//	2: $padded
//	3: $i
func buildRandomBytesBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	randomU64 := idxs["wasi_random_get_u64_p2"]
	var body []byte
	body = inst.InstLocalGet(body, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 7)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, int32(-8))
	body = numeric.InstI32And(body)
	body = inst.InstLocalSet(body, 2)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 1)
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 3)
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 2)
	body = numeric.InstI32GeU(body)
	body = inst.InstBrIf(body, 1)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 3)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, randomU64)
	body = memory.InstI64Store(body, 0, 0)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 3)
	body = inst.InstBr(body, 0)
	body = inst.InstEnd(body)
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 0)
	locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildWasmTimerPollableBody — () body for __fern_wasm_timer_pollable.
//
// Signature: (duration_ns: i64) → i32 (pollable handle)
//
// Just forwards the duration to
// wasi:clocks/monotonic-clock@0.2.0::subscribe-duration, which
// returns the own<pollable> handle directly. Preview-2-only (the
// native reactor uses timerfd; this is the wasm analog).
func buildWasmTimerPollableBody(idxs map[string]uint32) []byte {
	sub := idxs["wasi_clocks_subscribe_duration"]
	var body []byte
	body = inst.InstLocalGet(body, 0) // duration_ns
	body = inst.InstCall(body, sub)   // → pollable handle (i32)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildWasmBlockBody — body for __fern_wasm_block.
//
// Signature: (pollable: i32) → i32
//
// Blocks on the pollable via wasi:io/poll.pollable.block, then
// returns 0. Preview-2-only.
func buildWasmBlockBody(idxs map[string]uint32) []byte {
	block := idxs["wasi_io_pollable_block"]
	var body []byte
	body = inst.InstLocalGet(body, 0) // pollable handle
	body = inst.InstCall(body, block) // block until ready
	body = inst.InstI32Const(body, 0) // return 0
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildWasmPollableDropBody — body for __fern_wasm_pollable_drop.
//
// Signature: (pollable: i32) → i32
//
// Drops the pollable via wasi:io/poll.[resource-drop]pollable, then
// returns 0. Preview-2-only.
func buildWasmPollableDropBody(idxs map[string]uint32) []byte {
	drop := idxs["wasi_io_pollable_drop"]
	var body []byte
	body = inst.InstLocalGet(body, 0) // pollable handle
	body = inst.InstCall(body, drop)  // [resource-drop]pollable
	body = inst.InstI32Const(body, 0) // return 0
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildWasmPollBody — body for __fern_wasm_poll.
//
// Signature: (pollables: i32[]) → i32
//
// A Fern array is length-prefixed (count at ptr-4, contiguous i32
// elements at ptr+0), so the pollable list lowers directly to the
// canonical (ptr, len) list param. Calls wasi:io/poll.poll, which
// writes a list<u32> of ready indices into an 8-byte return area
// (data ptr @ +0, count @ +4). Returns the first ready index, or -1
// when the ready list is empty.
//
// Locals (param 0 = arr):
//
//	1: $retptr (8-byte return area)
func buildWasmPollBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	poll := idxs["wasi_io_poll_poll"]
	var body []byte
	// $retptr = alloc(8)
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 1)
	// poll(arr, len = i32.load(arr-4), retptr)
	body = inst.InstLocalGet(body, 0) // arr (list data ptr = first elem)
	body = inst.InstLocalGet(body, 0) // arr
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0) // len = count @ arr-4
	body = inst.InstLocalGet(body, 1)     // retptr
	body = inst.InstCall(body, poll)
	// count = i32.load(retptr+4)
	body = inst.InstLocalGet(body, 1)
	body = memory.InstI32Load(body, 2, 4)
	// if count != 0 { return ready[0] } else { return -1 }
	body = inst.InstIfStart(body, encode.ValtypeI32)
	body = inst.InstLocalGet(body, 1)
	body = memory.InstI32Load(body, 2, 0) // data ptr @ retptr+0
	body = memory.InstI32Load(body, 2, 0) // ready[0]
	body = inst.InstElse(body)
	body = inst.InstI32Const(body, -1)
	body = inst.InstEnd(body)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildMonotonicNsBodyP2 is the preview-2 variant of
// buildMonotonicNsBody.
//
// Signature: () → i64
//
// Body just calls `wasi:clocks/monotonic-clock@0.2.0::now()` which
// returns an `instant` (u64, lowered to i64 at core wasm). No
// scratch alloc, no clockID arg, no errno.
func buildMonotonicNsBodyP2(idxs map[string]uint32) []byte {
	monoNow := idxs["wasi_monotonic_now_p2"]
	var body []byte
	body = inst.InstCall(body, monoNow)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildNowNsBodyP2 is the preview-2 variant of buildNowNsBody.
//
// Signature: () → i64
//
// Allocates a 16-byte datetime out-buffer, calls
// wasi:clocks/wall-clock@0.2.0::now(buf), reads seconds (u64 at
// +0) and nanoseconds (u32 at +8), and returns
// seconds*1_000_000_000 + nanoseconds.
//
// Locals (no params):
//
//	0: $buf
func buildNowNsBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	wallNow := idxs["wasi_wall_clock_now_p2"]
	var body []byte
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 0) // $buf, leave on stack for the call
	body = inst.InstCall(body, wallNow)
	// seconds (i64 at $buf+0) * 1e9
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI64Load(body, 3, 0)
	body = inst.InstI64Const(body, 1_000_000_000)
	body = numeric.InstI64Mul(body)
	// + nanoseconds (u32 at $buf+8, zero-extended)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 8)
	body = convert.InstI64ExtendI32U(body)
	body = numeric.InstI64Add(body)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildNowUnixMsBodyP2 is the preview-2 variant of
// buildNowUnixMsBody.
//
// Signature: () → i64
//
// Same datetime read as buildNowNsBodyP2 but returns
// milliseconds: seconds*1000 + nanoseconds/1_000_000.
//
// Locals (no params):
//
//	0: $buf
func buildNowUnixMsBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	wallNow := idxs["wasi_wall_clock_now_p2"]
	var body []byte
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 0)
	body = inst.InstCall(body, wallNow)
	// seconds * 1000
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI64Load(body, 3, 0)
	body = inst.InstI64Const(body, 1000)
	body = numeric.InstI64Mul(body)
	// + nanoseconds / 1_000_000
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 8)
	body = convert.InstI64ExtendI32U(body)
	body = inst.InstI64Const(body, 1_000_000)
	body = numeric.InstI64DivU(body)
	body = numeric.InstI64Add(body)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildRandomI32BodyP2 is the preview-2 variant of buildRandomI32Body.
//
// Signature: () → i32
//
// Body calls `wasi:random/random@0.2.0::get-random-u64()` which
// returns a u64 (i64 at the core-wasm level), then truncates to
// i32 with i32.wrap_i64. No scratch buffer, no errno.
//
// Used in place of buildRandomI32Body when EmitOptions.Preview2WASI
// is on. Selected via preview2HelperBodyOverrides.
func buildRandomI32BodyP2(idxs map[string]uint32) []byte {
	randomU64 := idxs["wasi_random_get_u64_p2"]
	var body []byte
	body = inst.InstCall(body, randomU64)
	body = convert.InstI32WrapI64(body)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildNowNsBody assembles the wasm bytes for __fern_now_ns.
//
// Signature: () → i64
//
// Body:
//
//	buf = __fern_alloc(8)
//	wasi_clock_time_get(0 /* realtime */, 0 /* precision */, buf)
//	drop errno
//	return i64.load(buf)
//
// Allocates per call so the 8-byte target buffer doesn't clash
// with any other fixed-address scratch.
func buildNowNsBody(idxs map[string]uint32) []byte {
	return buildClockBody(idxs, 0, false)
}

// buildMonotonicNsBody — () → i64. Same as __fern_now_ns but
// uses CLOCK_MONOTONIC (clock_id=1) so the reading is
// monotonically non-decreasing across NTP adjustments.
func buildMonotonicNsBody(idxs map[string]uint32) []byte {
	return buildClockBody(idxs, 1, false)
}

// buildNowUnixMsBody — () → i64. Calls wasi_clock_time_get
// (CLOCK_REALTIME) and divides by 1_000_000 to get milliseconds.
func buildNowUnixMsBody(idxs map[string]uint32) []byte {
	return buildClockBody(idxs, 0, true)
}

// buildClockBody is the shared core: alloc 8 bytes, call
// wasi_clock_time_get(clockID, 0, buf), load i64, optionally
// divide by 1_000_000 for the ms variant.
func buildClockBody(idxs map[string]uint32, clockID int32, divideMs bool) []byte {
	alloc := idxs["__fern_alloc"]
	clockTime := idxs["wasi_clock_time_get"]
	var body []byte
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 0) // $buf
	body = inst.InstI32Const(body, clockID)
	body = inst.InstI64Const(body, 0) // precision = 0
	body = inst.InstLocalGet(body, 0)
	body = inst.InstCall(body, clockTime)
	body = inst.InstDrop(body) // ignore errno
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI64Load(body, 3, 0)
	if divideMs {
		body = inst.InstI64Const(body, 1_000_000)
		body = numeric.InstI64DivS(body)
	}
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildEnvCountBody assembles the wasm bytes for __fern_env_count.
//
// Signature: () → i32 (envc)
//
// Body:
//
//	buf = __fern_alloc(8)               ; two i32 output slots
//	wasi_environ_sizes_get(buf, buf + 4)
//	drop errno
//	return i32.load(buf)                ; envc lives at +0
func buildEnvCountBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	envSizes := idxs["wasi_environ_sizes_get"]
	var body []byte
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 0) // $buf
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, envSizes)
	body = inst.InstDrop(body)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildArgCountBody assembles the wasm bytes for __fern_arg_count.
//
// Signature: () → i32 (argc)
//
// Body:
//
//	buf = __fern_alloc(8)               ; two i32 output slots
//	wasi_args_sizes_get(buf, buf + 4)
//	drop errno
//	return i32.load(buf)                ; argc lives at +0
func buildArgCountBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	argsSizes := idxs["wasi_args_sizes_get"]
	var body []byte
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 0) // $buf
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, argsSizes)
	body = inst.InstDrop(body)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildArgAtBody assembles __fern_arg_at.
//
// Signature: (param $i i32) (result i32 i32) — (data, len) pair.
//
// Logic: lazily call wasi_args_sizes_get + wasi_args_get on first
// call, caching (count, argv_ptrs) in low memory. Each call walks
// argv_ptrs[i] until the NUL byte to recover the length, then
// returns (cstr, len) as a heap-form string (top bit of len = 0).
//
// Out-of-range i (signed-negative or i >= argc) returns (0, 0).
//
// Locals (after the one param):
//
//	1: $argc
//	2: $bufsize
//	3: $argv_ptrs
//	4: $argv_buf
//	5: $cstr
//	6: $len
func buildArgAtBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	argsSizes := idxs["wasi_args_sizes_get"]
	argsGet := idxs["wasi_args_get"]
	var body []byte
	body = inst.InstI32Const(body, argsInitAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, argsSizesArgcAddr)
		body = inst.InstI32Const(body, argsSizesBufAddr)
		body = inst.InstCall(body, argsSizes)
		body = inst.InstDrop(body)
		body = inst.InstI32Const(body, argsSizesArgcAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 1) // $argc
		body = inst.InstI32Const(body, argsSizesBufAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 2) // $bufsize
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Mul(body)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 3) // $argv_ptrs
		body = inst.InstLocalGet(body, 2)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 4) // $argv_buf
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstCall(body, argsGet)
		body = inst.InstDrop(body)
		body = inst.InstI32Const(body, argsCountAddr)
		body = inst.InstLocalGet(body, 1)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstI32Const(body, argsPtrsAddr)
		body = inst.InstLocalGet(body, 3)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstI32Const(body, argsInitAddr)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 0)
	}
	body = inst.InstEnd(body)
	// Bounds check via unsigned compare: rejects negatives + overshoot.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, argsCountAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32GeU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// cstr = mem[args_ptrs + i*4]
	body = inst.InstI32Const(body, argsPtrsAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Mul(body)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 5) // $cstr
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 6) // $len = 0
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 5)
		body = inst.InstLocalGet(body, 6)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load8U(body, 0, 0)
		body = numeric.InstI32Eqz(body)
		body = inst.InstBrIf(body, 1)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 6)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body)
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 6)
	locals := inst.PutLocalsOneGroup(nil, 6, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// appendArgsInitP2 appends the lazy-init for the preview-2 args
// cache: if not yet fetched, call get-arguments into an 8-byte
// retbuf (allocated into local `rbLocal`), then cache the element
// count (retbuf+4) at argsCountAddr and the list base pointer
// (retbuf+0) at argsPtrsAddr, and set argsInitAddr=1. Unlike the
// preview-1 path, the args_sizes scratch slots
// (argsSizesArgcAddr / argsSizesBufAddr) are left untouched here,
// so they're free for __fern_args's built-array cache without the
// preview-1 slot-aliasing hazard.
func appendArgsInitP2(body []byte, getArgs, alloc, rbLocal uint32) []byte {
	body = inst.InstI32Const(body, argsInitAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		// $rb = alloc(8); get-arguments($rb)
		body = inst.InstI32Const(body, 8)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, rbLocal)
		body = inst.InstLocalGet(body, rbLocal)
		body = inst.InstCall(body, getArgs)
		// mem[argsCountAddr] = mem[$rb + 4]
		body = inst.InstI32Const(body, argsCountAddr)
		body = inst.InstLocalGet(body, rbLocal)
		body = memory.InstI32Load(body, 2, 4)
		body = memory.InstI32Store(body, 2, 0)
		// mem[argsPtrsAddr] = mem[$rb + 0]
		body = inst.InstI32Const(body, argsPtrsAddr)
		body = inst.InstLocalGet(body, rbLocal)
		body = memory.InstI32Load(body, 2, 0)
		body = memory.InstI32Store(body, 2, 0)
		// mem[argsInitAddr] = 1
		body = inst.InstI32Const(body, argsInitAddr)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 0)
	}
	body = inst.InstEnd(body)
	return body
}

// buildArgCountBodyP2 is the preview-2 variant of buildArgCountBody
// — () → argc via wasi:cli/environment::get-arguments.
func buildArgCountBodyP2(idxs map[string]uint32) []byte {
	getArgs := idxs["wasi_get_arguments_p2"]
	alloc := idxs["__fern_alloc"]
	var body []byte
	body = appendArgsInitP2(body, getArgs, alloc, 0) // local 0 = $rb
	body = inst.InstI32Const(body, argsCountAddr)
	body = memory.InstI32Load(body, 2, 0)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildArgAtBodyP2 is the preview-2 variant of buildArgAtBody —
// (i) → (data, len). The canonical list elements are already
// (ptr, len) pairs (8 bytes each), so there's no NUL walk: element
// i lives at listBase + i*8. Out-of-range i returns (0, 0).
//
// Locals (after the one param):
//
//	1: $rb
//	2: $el
func buildArgAtBodyP2(idxs map[string]uint32) []byte {
	getArgs := idxs["wasi_get_arguments_p2"]
	alloc := idxs["__fern_alloc"]
	var body []byte
	body = appendArgsInitP2(body, getArgs, alloc, 1) // local 1 = $rb
	// Bounds check (unsigned): i >= count → (0, 0).
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, argsCountAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32GeU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// $el = mem[argsPtrsAddr] + i*8
	body = inst.InstI32Const(body, argsPtrsAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Mul(body)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 2)
	// return (mem[$el], mem[$el + 4])
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 4)
	locals := inst.PutLocalsOneGroup(nil, 2, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildEnvAtBody — mirror of buildArgAtBody, routed through
// wasi_environ_sizes_get + wasi_environ_get. Each returned
// (data, len) covers a full "KEY=VALUE" entry; user code splits
// on '=' if needed.
func buildEnvAtBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	strCopy := idxs["__fern_str_copy"]
	envSizes := idxs["wasi_environ_sizes_get"]
	envGet := idxs["wasi_environ_get"]
	var body []byte
	body = inst.InstI32Const(body, envInitAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, envSizesArgcAddr)
		body = inst.InstI32Const(body, envSizesBufAddr)
		body = inst.InstCall(body, envSizes)
		body = inst.InstDrop(body)
		body = inst.InstI32Const(body, envSizesArgcAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 1)
		body = inst.InstI32Const(body, envSizesBufAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 2)
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Mul(body)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 3)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 4)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstCall(body, envGet)
		body = inst.InstDrop(body)
		body = inst.InstI32Const(body, envCountAddr)
		body = inst.InstLocalGet(body, 1)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstI32Const(body, envPtrsAddr)
		body = inst.InstLocalGet(body, 3)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstI32Const(body, envInitAddr)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 0)
	}
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, envCountAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32GeU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	body = inst.InstI32Const(body, envPtrsAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Mul(body)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 5)
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 6)
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 5)
		body = inst.InstLocalGet(body, 6)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load8U(body, 0, 0)
		body = numeric.InstI32Eqz(body)
		body = inst.InstBrIf(body, 1)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 6)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body)
	body = inst.InstEnd(body)
	// Copy the (cstr, len) view into a fresh owned string so it carries
	// an rc header (the environ buffer slice has none).
	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstCall(body, strCopy)
	locals := inst.PutLocalsOneGroup(nil, 6, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildReadByteBody assembles __fern_read_byte.
//
// Signature: () → i32 — returns 0..255 for a byte read from
// stdin, or -1 for EOF / error.
//
// First call alloc(16) for the scratch region and caches the
// base addr in low memory (readByteScratchAddr). Layout within
// the scratch region: iov.base (4), iov.len (4), nread out (4),
// 1-byte buffer (rounded up to 4 = 4 bytes). Total 16 bytes.
//
//	scratch + 0..3:   iov.base (set per call to scratch+12)
//	scratch + 4..7:   iov.len = 1
//	scratch + 8..11:  nread output
//	scratch + 12..15: byte buffer (only [12] used)
//
// Each call: write iov struct + invoke fd_read(0, scratch, 1,
// scratch+8); on errno != 0 or nread == 0 → -1, otherwise load
// the byte at scratch+12.
//
// Locals (no params):
//
//	0: $scratch
//	1: $errno
func buildReadByteBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	fdRead := idxs["wasi_fd_read"]
	var body []byte
	// $scratch = mem[readByteScratchAddr]
	body = inst.InstI32Const(body, readByteScratchAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalTee(body, 0)
	// If zero, alloc(16) and store the pointer back.
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, 16)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 0)
		body = inst.InstI32Const(body, readByteScratchAddr)
		body = inst.InstLocalGet(body, 0)
		body = memory.InstI32Store(body, 2, 0)
	}
	body = inst.InstEnd(body)
	// iov.base = scratch + 12 (the byte buffer slot)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Store(body, 2, 0)
	// iov.len = 1
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	// fd_read(0 /* stdin */, scratch, 1, scratch+8)
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 1)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, fdRead)
	body = inst.InstLocalSet(body, 1) // $errno
	// If errno != 0, return -1.
	body = inst.InstLocalGet(body, 1)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, -1)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// If nread == 0 (EOF), return -1.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, -1)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// Read byte at scratch+12.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load8U(body, 0, 0)
	locals := inst.PutLocalsOneGroup(nil, 2, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildReadByteBodyP2 is the preview-2 variant of buildReadByteBody.
//
// Signature: () → i32 — returns 0..255 for a byte read from stdin,
// or -1 for EOF / error.
//
// Preview-2 has no fd_read; bytes come from
// `wasi:io/streams::[method]input-stream.blocking-read` on the
// `wasi:cli/stdin::get-stdin()` handle. The stdin handle is fetched
// once and cached in readByteScratchAddr as handle+1 (0 = uninit) —
// the +1 bias lets handle 0 (a legal resource index) round-trip
// through the zero sentinel without a separate init flag.
//
// Each call: blocking-read(handle, 1, retbuf) where retbuf is a
// 12-byte indirect-return area holding
// `result<list<u8>, stream-error>`:
//
//	retbuf + 0:      disc byte (0 = ok, 1 = err)
//	retbuf + 4..7:   list data ptr  (ok arm)
//	retbuf + 8..11:  list length    (ok arm)
//
// disc != 0 (any stream-error, incl. `closed` = EOF) → -1. An ok
// arm with zero length → -1. Otherwise return the byte at the list
// data pointer.
//
// Locals (no params):
//
//	0: $handle
//	1: $retbuf
func buildReadByteBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	getStdin := idxs["wasi_get_stdin_p2"]
	blockingRead := idxs["wasi_io_blocking_read"]
	var body []byte
	// $handle: load cached (handle+1); if 0, fetch + cache, else
	// decode by subtracting 1. Both branches leave $0 = handle.
	body = inst.InstI32Const(body, readByteScratchAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalTee(body, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		// mem[readByteScratchAddr] = get-stdin() + 1; $0 = handle.
		body = inst.InstI32Const(body, readByteScratchAddr)
		body = inst.InstCall(body, getStdin)
		body = inst.InstLocalTee(body, 0)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Store(body, 2, 0)
	}
	body = inst.InstElse(body)
	{
		// $0 = cached - 1.
		body = inst.InstLocalGet(body, 0)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Sub(body)
		body = inst.InstLocalSet(body, 0)
	}
	body = inst.InstEnd(body)
	// $retbuf = __fern_alloc(12)
	body = inst.InstI32Const(body, 12)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 1)
	// blocking-read(handle, 1 /* len u64 */, retbuf)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI64Const(body, 1)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, blockingRead)
	// If disc byte != 0 (stream-error / EOF), return -1.
	body = inst.InstLocalGet(body, 1)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, -1)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// If list length (retbuf+8) == 0, return -1.
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, -1)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// Return the byte at the list data pointer (retbuf+4).
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = memory.InstI32Load8U(body, 0, 0)
	locals := inst.PutLocalsOneGroup(nil, 2, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildPutcharBody — (b) → (). Writes the low byte of b to
// stdout via wasi_fd_write. Uses the print iovec scratch
// region (printIovecAddr=48..55) as a 1-byte buffer at
// printIovecAddr+0 with iovec base=printIovecAddr+0 (the
// next 4 bytes hold the byte) and iov_len=1; the iovec
// descriptor reuses printIovecAddr/+4. Reads back the same
// nwritten slot at printRetAddr.
//
// Memory layout per call:
//
//	mem[48..51]: iov_base = 52
//	mem[52]:     the byte (one byte; we use a 4-byte slot for alignment)
//	mem[56..59]: iov_len = 1 (overlaps printRetAddr — we overwrite
//	             after fd_write reads it)
//
// To avoid the iov_base/iov_len-overlap-with-the-byte-slot issue,
// shift the byte buffer to a dedicated slot inside the existing
// scratch region. We write the byte at printRetAddr (since the
// fd_write result is dropped anyway), iov_base=printRetAddr, iov_len=1,
// stored at printIovecAddr/+4.
func buildPutcharBody(idxs map[string]uint32) []byte {
	fdWrite := idxs["wasi_fd_write"]
	var body []byte
	// mem[printRetAddr] = b (low byte; we only read one)
	body = inst.InstI32Const(body, printRetAddr)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Store8(body, 0, 0)
	// mem[printIovecAddr] = printRetAddr (iov_base)
	body = inst.InstI32Const(body, printIovecAddr)
	body = inst.InstI32Const(body, printRetAddr)
	body = memory.InstI32Store(body, 2, 0)
	// mem[printIovecAddr + 4] = 1 (iov_len)
	body = inst.InstI32Const(body, printIovecAddr+4)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	// wasi_fd_write(1, iovec_addr, 1, ret_addr); drop result.
	// Note: ret_addr overlaps the byte buffer; that's fine since
	// we never read the byte back, and fd_write writes nwritten
	// after reading iov_base/iov_len.
	body = inst.InstI32Const(body, 1)
	body = inst.InstI32Const(body, printIovecAddr)
	body = inst.InstI32Const(body, 1)
	body = inst.InstI32Const(body, printRetAddr)
	body = inst.InstCall(body, fdWrite)
	body = inst.InstDrop(body)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildPutcharBodyP2 is the preview-2 variant of buildPutcharBody.
//
// Signature: (param $b i32) (result)
//
// Writes the low byte of $b to stdout via
// wasi:io/streams::blocking-write-and-flush, reusing the cached
// stdout handle (shared with print / write). The 1-byte buffer
// is heap-allocated per call (alloc(1)); a 16-byte result-return
// area is allocated alongside.
//
// Wasm locals (after the param):
//
//	1: $buf  (1-byte content buffer)
func buildPutcharBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	getStdout := idxs["wasi_get_stdout_p2"]
	write := idxs["wasi_blocking_write_and_flush_p2"]
	var body []byte
	// If !init: cache the stdout handle.
	body = inst.InstI32Const(body, stdoutInitAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, stdoutHandleAddr)
	body = inst.InstCall(body, getStdout)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstI32Const(body, stdoutInitAddr)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstEnd(body)
	// $buf = __fern_alloc(1); mem[$buf] = $b (low byte).
	body = inst.InstI32Const(body, 1)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 1)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Store8(body, 0, 0)
	// blocking-write-and-flush(stdout, $buf, 1, retBuf).
	body = inst.InstI32Const(body, stdoutHandleAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 1)
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstCall(body, write)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// random bytes via wasi_random_get. Returns the (data, len)
// pair in heap form (top bit of len clear).
//
//	n == 0:  return inline empty (0, 0x80000000).
//	n > 0:   data = alloc(n); random_get(data, n); return (data, n).
//
// Locals (after the one param):
//
//	1: $buf
func buildRandomBytesBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	randomGet := idxs["wasi_random_get"]
	var body []byte
	// if n == 0: return inline empty (0, 0x80000000)
	body = inst.InstLocalGet(body, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// $buf = __fern_alloc(n)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 1)
	// wasi_random_get($buf, n); drop errno.
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstCall(body, randomGet)
	body = inst.InstDrop(body)
	// Return ($buf, n)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 0)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildEnvBody — (name_data, name_len) → i32 (Option[string]
// box). Looks up an env variable by name in the cached
// environ_ptrs. Returns Some(value) on match, None otherwise.
//
// Layout matches __fern_read_line's Option[string] box:
//
//	Some(line): 16-byte alloc, tag=0 at +0, data at +8, len at +12.
//	None:       4-byte alloc, tag=1 at +0.
//
// Algorithm:
//   - Lazily init the env cache (shared with __fern_env_at).
//   - For each i in 0..envc:
//   - entry = environ_ptrs[i]  (NUL-terminated "KEY=VALUE")
//   - Walk j from 0:
//   - byte = mem[entry + j]
//   - If j == name_len AND byte == '=': match found
//   - If j == name_len OR byte != name[j]: no match, next i
//   - When match: value_start = entry + j + 1
//     value_len = strlen(value_start)
//     Build Some(value).
//   - If no entry matches: return None.
//
// Locals (after 2 params):
//
//	2: $i        — outer entry index
//	3: $entry    — current environ_ptrs[i]
//	4: $j        — byte offset within entry
//	5: $entry_b  — byte at entry+j
//	6: $name_b   — byte at name_data+j (looked up via __fern_str_byte)
//	7: $value    — value-start pointer (entry + j + 1) on match
//	8: $vlen     — value length (strlen)
//	9: $box      — Option box pointer for return
//	10: $name_real_len — strlen of name (via __fern_str_len)
func buildEnvBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocBox := idxs["__fern_alloc_box"]
	strLen := idxs["__fern_str_len"]
	strByte := idxs["__fern_str_byte"]
	strCopy := idxs["__fern_str_copy"]
	envSizes := idxs["wasi_environ_sizes_get"]
	envGet := idxs["wasi_environ_get"]
	var body []byte
	// Lazy init env cache (same shape as __fern_env_at).
	body = inst.InstI32Const(body, envInitAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, envSizesArgcAddr)
		body = inst.InstI32Const(body, envSizesBufAddr)
		body = inst.InstCall(body, envSizes)
		body = inst.InstDrop(body)
		body = inst.InstI32Const(body, envSizesArgcAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 2) // reuse $i slot temporarily for envc
		body = inst.InstI32Const(body, envSizesBufAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 3) // reuse $entry slot for bufsize
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Mul(body)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 4) // env_ptrs (reuse $j)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 5) // env_buf (reuse $entry_b)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstLocalGet(body, 5)
		body = inst.InstCall(body, envGet)
		body = inst.InstDrop(body)
		body = inst.InstI32Const(body, envCountAddr)
		body = inst.InstLocalGet(body, 2)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstI32Const(body, envPtrsAddr)
		body = inst.InstLocalGet(body, 4)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstI32Const(body, envInitAddr)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 0)
	}
	body = inst.InstEnd(body)
	// $name_real_len = __fern_str_len(name_data, name_len)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, strLen)
	body = inst.InstLocalSet(body, 10)
	// for i in 0..envc:
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 2)
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty) // outer
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// if $i >= envc: break (no match)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, envCountAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = numeric.InstI32GeU(body)
		body = inst.InstBrIf(body, 1)
		// $entry = mem[env_ptrs + i*4]
		body = inst.InstI32Const(body, envPtrsAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Mul(body)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 3)
		// Compare prefix of entry with name. $j = 0.
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 4)
		body = inst.InstBlockStart(body, inst.BlocktypeEmpty) // cmp block
		body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
		{
			// $entry_b = mem[entry + j]
			body = inst.InstLocalGet(body, 3)
			body = inst.InstLocalGet(body, 4)
			body = numeric.InstI32Add(body)
			body = memory.InstI32Load8U(body, 0, 0)
			body = inst.InstLocalSet(body, 5)
			// If $j == name_real_len: check entry_b == '='
			body = inst.InstLocalGet(body, 4)
			body = inst.InstLocalGet(body, 10)
			body = numeric.InstI32Eq(body)
			body = inst.InstIfStart(body, inst.BlocktypeEmpty)
			{
				// entry_b == '=' (61)?
				body = inst.InstLocalGet(body, 5)
				body = inst.InstI32Const(body, 61)
				body = numeric.InstI32Eq(body)
				body = inst.InstIfStart(body, inst.BlocktypeEmpty)
				{
					// Match: build Some(value). value starts at
					// entry + j + 1. strlen via NUL scan.
					body = inst.InstLocalGet(body, 3)
					body = inst.InstLocalGet(body, 4)
					body = numeric.InstI32Add(body)
					body = inst.InstI32Const(body, 1)
					body = numeric.InstI32Add(body)
					body = inst.InstLocalSet(body, 7) // $value
					// $vlen = 0; while mem[value + vlen] != 0: vlen++
					body = inst.InstI32Const(body, 0)
					body = inst.InstLocalSet(body, 8)
					body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
					body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
					{
						body = inst.InstLocalGet(body, 7)
						body = inst.InstLocalGet(body, 8)
						body = numeric.InstI32Add(body)
						body = memory.InstI32Load8U(body, 0, 0)
						body = numeric.InstI32Eqz(body)
						body = inst.InstBrIf(body, 1)
						body = inst.InstLocalGet(body, 8)
						body = inst.InstI32Const(body, 1)
						body = numeric.InstI32Add(body)
						body = inst.InstLocalSet(body, 8)
						body = inst.InstBr(body, 0)
					}
					body = inst.InstEnd(body)
					body = inst.InstEnd(body)
					// Copy the value view ($value, $vlen) into a fresh
					// owned string (the environ buffer slice has no rc
					// header). Overwrite $value / $vlen with the copy.
					body = inst.InstLocalGet(body, 7)
					body = inst.InstLocalGet(body, 8)
					body = inst.InstCall(body, strCopy)
					body = inst.InstLocalSet(body, 8) // $vlen := clen
					body = inst.InstLocalSet(body, 7) // $value := cdata
					// Build Some box: alloc(16), tag=0, data, len.
					// Phase 1e-runtime: alloc_box adds the 8-byte
					// static-sentinel rc header so enum-ii's inc/dec
					// no-op on this runtime-built Option box.
					body = inst.InstI32Const(body, 16)
					body = inst.InstCall(body, allocBox)
					body = inst.InstLocalSet(body, 9)
					body = inst.InstLocalGet(body, 9)
					body = inst.InstI32Const(body, 0)
					body = memory.InstI32Store(body, 2, 0)
					body = inst.InstLocalGet(body, 9)
					body = inst.InstI32Const(body, 8)
					body = numeric.InstI32Add(body)
					body = inst.InstLocalGet(body, 7)
					body = memory.InstI32Store(body, 2, 0)
					body = inst.InstLocalGet(body, 9)
					body = inst.InstI32Const(body, 12)
					body = numeric.InstI32Add(body)
					body = inst.InstLocalGet(body, 8)
					body = memory.InstI32Store(body, 2, 0)
					body = inst.InstLocalGet(body, 9)
					body = inst.InstReturn(body)
				}
				body = inst.InstEnd(body)
				// Mismatch (name fully consumed but no '=' yet).
				// Exit cmp loop, continue outer.
				body = inst.InstBr(body, 2) // break out of inner block (cmp)
			}
			body = inst.InstEnd(body)
			// If entry_b == 0 (premature NUL) or entry_b != name[j]:
			// not a match.
			body = inst.InstLocalGet(body, 5)
			body = numeric.InstI32Eqz(body)
			body = inst.InstBrIf(body, 1) // break cmp loop
			// $name_b = __fern_str_byte(name_data, name_len, j)
			body = inst.InstLocalGet(body, 0)
			body = inst.InstLocalGet(body, 1)
			body = inst.InstLocalGet(body, 4)
			body = inst.InstCall(body, strByte)
			body = inst.InstLocalSet(body, 6)
			// If entry_b != name_b: break cmp loop
			body = inst.InstLocalGet(body, 5)
			body = inst.InstLocalGet(body, 6)
			body = numeric.InstI32Ne(body)
			body = inst.InstBrIf(body, 1) // break cmp loop
			// $j++
			body = inst.InstLocalGet(body, 4)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalSet(body, 4)
			body = inst.InstBr(body, 0) // continue cmp loop
		}
		body = inst.InstEnd(body) // end cmp loop
		body = inst.InstEnd(body) // end cmp block
		// Next entry.
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 2)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // end outer loop
	body = inst.InstEnd(body) // end outer block
	// No match: return None. alloc(4), tag=1. Phase 1e-runtime:
	// alloc_box prepends the static-sentinel rc header.
	body = inst.InstI32Const(body, 4)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, 9)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 9)
	locals := inst.PutLocalsOneGroup(nil, 9, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildEnvBodyP2 is the preview-2 variant of buildEnvBody. Looks up
// an env var by name in the wasi:cli/environment::get-environment
// list<tuple<string, string>>. Unlike preview-1's combined
// "KEY=VALUE" NUL strings, get-environment returns pre-split
// (key, value) pairs with explicit lengths, so the match is a plain
// length + byte compare (no '=' scan, no strlen).
//
// Each list element is a 16-byte tuple: key (ptr @ +0, len @ +4),
// value (ptr @ +8, len @ +12). The list base + count are cached in
// the env slots on first call (envCountAddr, envPtrsAddr).
//
// Returns the same Option[string] box as buildEnvBody:
// Some(value) → alloc_box(16) {tag=0, data@+8, len@+12};
// None → alloc_box(4) {tag=1}.
//
// Locals (after 2 params): 2=$i, 3=$tuple, 4=$key_ptr, 5=$key_len,
// 6=$j, 7=$matched, 8=$name_len, 9=$rb/$box.
func buildEnvBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocBox := idxs["__fern_alloc_box"]
	strLen := idxs["__fern_str_len"]
	strByte := idxs["__fern_str_byte"]
	strCopy := idxs["__fern_str_copy"]
	getEnv := idxs["wasi_get_environment_p2"]
	var body []byte
	// Lazy init: get-environment into an 8-byte retbuf ($rb=local 9).
	body = inst.InstI32Const(body, envInitAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, 8)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 9)
		body = inst.InstLocalGet(body, 9)
		body = inst.InstCall(body, getEnv)
		// mem[envCountAddr] = mem[$rb + 4]
		body = inst.InstI32Const(body, envCountAddr)
		body = inst.InstLocalGet(body, 9)
		body = memory.InstI32Load(body, 2, 4)
		body = memory.InstI32Store(body, 2, 0)
		// mem[envPtrsAddr] = mem[$rb + 0]
		body = inst.InstI32Const(body, envPtrsAddr)
		body = inst.InstLocalGet(body, 9)
		body = memory.InstI32Load(body, 2, 0)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstI32Const(body, envInitAddr)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 0)
	}
	body = inst.InstEnd(body)
	// $name_len = __fern_str_len(name_data, name_len)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, strLen)
	body = inst.InstLocalSet(body, 8)
	// for i in 0..count:
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 2)
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty) // outer
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, envCountAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = numeric.InstI32GeU(body)
		body = inst.InstBrIf(body, 1) // i >= count → break (no match)
		// $tuple = mem[envPtrsAddr] + i*16
		body = inst.InstI32Const(body, envPtrsAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 16)
		body = numeric.InstI32Mul(body)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 3)
		// $key_ptr = mem[tuple+0], $key_len = mem[tuple+4]
		body = inst.InstLocalGet(body, 3)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 4)
		body = inst.InstLocalGet(body, 3)
		body = memory.InstI32Load(body, 2, 4)
		body = inst.InstLocalSet(body, 5)
		// if $key_len == $name_len: byte-compare
		body = inst.InstLocalGet(body, 5)
		body = inst.InstLocalGet(body, 8)
		body = numeric.InstI32Eq(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			// $matched = 1; $j = 0
			body = inst.InstI32Const(body, 1)
			body = inst.InstLocalSet(body, 7)
			body = inst.InstI32Const(body, 0)
			body = inst.InstLocalSet(body, 6)
			body = inst.InstBlockStart(body, inst.BlocktypeEmpty) // cmp block
			body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
			{
				body = inst.InstLocalGet(body, 6)
				body = inst.InstLocalGet(body, 8)
				body = numeric.InstI32GeU(body)
				body = inst.InstBrIf(body, 1) // j >= name_len → all matched
				// mem[key_ptr + j] != str_byte(name, j) → mismatch
				body = inst.InstLocalGet(body, 4)
				body = inst.InstLocalGet(body, 6)
				body = numeric.InstI32Add(body)
				body = memory.InstI32Load8U(body, 0, 0)
				body = inst.InstLocalGet(body, 0)
				body = inst.InstLocalGet(body, 1)
				body = inst.InstLocalGet(body, 6)
				body = inst.InstCall(body, strByte)
				body = numeric.InstI32Ne(body)
				body = inst.InstIfStart(body, inst.BlocktypeEmpty)
				{
					body = inst.InstI32Const(body, 0)
					body = inst.InstLocalSet(body, 7) // $matched = 0
					body = inst.InstBr(body, 2)       // break cmp loop
				}
				body = inst.InstEnd(body)
				body = inst.InstLocalGet(body, 6)
				body = inst.InstI32Const(body, 1)
				body = numeric.InstI32Add(body)
				body = inst.InstLocalSet(body, 6)
				body = inst.InstBr(body, 0)
			}
			body = inst.InstEnd(body) // cmp loop
			body = inst.InstEnd(body) // cmp block
			// if $matched: build Some(value) and return.
			body = inst.InstLocalGet(body, 7)
			body = inst.InstIfStart(body, inst.BlocktypeEmpty)
			{
				body = inst.InstI32Const(body, 16)
				body = inst.InstCall(body, allocBox)
				body = inst.InstLocalSet(body, 9)
				body = inst.InstLocalGet(body, 9)
				body = inst.InstI32Const(body, 0)
				body = memory.InstI32Store(body, 2, 0) // tag = 0 (Some)
				// Copy the value view (ptr @ tuple+8, len @ tuple+12)
				// into a fresh owned string ($cdata @ 10, $clen @ 11) —
				// the get-environment list buffer has no rc header.
				body = inst.InstLocalGet(body, 3)
				body = memory.InstI32Load(body, 2, 8)
				body = inst.InstLocalGet(body, 3)
				body = memory.InstI32Load(body, 2, 12)
				body = inst.InstCall(body, strCopy)
				body = inst.InstLocalSet(body, 11) // $clen
				body = inst.InstLocalSet(body, 10) // $cdata
				// data = $cdata
				body = inst.InstLocalGet(body, 9)
				body = inst.InstI32Const(body, 8)
				body = numeric.InstI32Add(body)
				body = inst.InstLocalGet(body, 10)
				body = memory.InstI32Store(body, 2, 0)
				// len = $clen
				body = inst.InstLocalGet(body, 9)
				body = inst.InstI32Const(body, 12)
				body = numeric.InstI32Add(body)
				body = inst.InstLocalGet(body, 11)
				body = memory.InstI32Store(body, 2, 0)
				body = inst.InstLocalGet(body, 9)
				body = inst.InstReturn(body)
			}
			body = inst.InstEnd(body)
		}
		body = inst.InstEnd(body)
		// next entry
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 2)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // outer loop
	body = inst.InstEnd(body) // outer block
	// No match: None box. alloc_box(4), tag=1.
	body = inst.InstI32Const(body, 4)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, 9)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 9)
	locals := inst.PutLocalsOneGroup(nil, 10, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}
