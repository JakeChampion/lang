// wasi:http/incoming-handler wrapper for the wasmbin backend.
//
// Mirrors the WAT path's `emitHttpHandlerWrapper`
// (internal/codegen/wasm/wasm.go, search for `__http_entry`): the
// host calls `wasi:http/incoming-handler@0.2.0#handle(req, out)`
// per request, the wrapper marshals the canonical-ABI incoming
// request into the user's `HttpRequest` struct, invokes the
// user-defined `handle(req: HttpRequest, plat: Platform):
// HttpResponse`, and streams the response back through
// `outgoing-body` before handing an `Ok(outgoing-response)` to
// `response-outparam.set`.
//
// HttpRequest / HttpResponse / HeaderMap / Platform are auto-
// injected at the checker level (see
// `internal/checker/checker.go` near line 240). The field offsets
// the wrapper hardcodes must stay in lockstep with the checker's
// `Param` ordering:
//
//	HttpRequest  (28 bytes): method@+0/+4, path@+8/+12,
//	                          body@+16/+20, headers@+24 (HeaderMap ptr)
//	HttpResponse (24 bytes): status@+0, body@+8/+12,
//	                          headers@+16 (HeaderMap ptr)
//	HeaderMap    (8 bytes):  names_ptr@+0, values_ptr@+4
//	Platform     (4 bytes):  version@+0
//
// Resource lifetime (matches the WAT path):
//   - `req` (incoming-request) is borrowed for `.method` /
//     `.path-with-query` / `.consume`, then explicitly dropped
//     once the body has been finished.
//   - `consume()` returns ownership of the incoming-body. After
//     reading the stream we hand the body to `[static]finish`
//     which transfers ownership and produces a future-trailers we
//     drop.
//   - `fields` constructed for the response are owned by the
//     outgoing-response constructor — no manual drop.
//   - `outgoing-body` from `outgoing-response.body()` is consumed
//     by `[static]finish` (skipped here — host seals on
//     `response-outparam.set`).
//   - `output-stream` from `outgoing-body.write()` IS dropped
//     manually before we hand the response to
//     response-outparam.set. The canonical-ABI rejects parent
//     drops with live children, same as TCP.
//   - `outgoing-response` and `response-outparam` are both
//     consumed by `[static]response-outparam.set`.
//
// Each canonical-ABI retptr fits in 64 bytes (the wrapper
// allocates a single 64-byte scratch via __lang_alloc at entry
// and reuses it).

package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// buildBytesToLangStringBody assembles __bytes_to_lang_string.
//
// Signature: (host_ptr, host_len) → (data, len) — a two-word
// heap-form string built from a host byte buffer.
//
// Always allocates a fresh n-byte heap buffer and memory.copys
// the host bytes in, then returns the (buf, n) pair. The WAT
// path has an SSO-inline fast path for n ≤ 7 that packs the
// bytes into the (data, len) i32 directly; the wasmbin slice
// keeps things simple with a uniform heap copy. Per-request
// arenas mean the overhead is reclaimed at request boundary.
//
// Locals (after the two params):
//
//	2: $buf — fresh heap buffer
func buildBytesToLangStringBody(idxs map[string]uint32) []byte {
	alloc := idxs["__lang_alloc"]

	var body []byte

	// if host_len == 0: return (0, 0).
	body = inst.InstLocalGet(body, 1)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, 0)
		body = inst.InstI32Const(body, 0)
		body = inst.InstReturn(body)
	}
	body = inst.InstEnd(body)

	// $buf = alloc(host_len); memory.copy($buf, host_ptr, host_len).
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 2)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = append(body, 0xFC, 0x0A, 0x00, 0x00) // memory.copy

	// return ($buf, host_len).
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 1)

	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildCabiReallocBody assembles `cabi_realloc`.
//
// Signature: (orig_ptr, orig_size, align, new_size) → i32 (new ptr).
//
// Canonical-ABI allocator the host invokes to materialise
// dynamically-sized return values (e.g. `list<u8>` for header
// names / values, `field-value` byte lists). Aligns the bump
// cursor up to `align` (power-of-two), then forwards to
// __lang_alloc. Today's callers only pass orig_ptr=0 and
// align ≤ 4, so the shrink / grow paths aren't implemented.
//
// Locals: none.
func buildCabiReallocBody(idxs map[string]uint32) []byte {
	alloc := idxs["__lang_alloc"]
	var body []byte

	// Align mem[40] up to $align: mem[40] = (mem[40] + align - 1) & ~(align - 1).
	body = inst.InstI32Const(body, allocCursorAddr)
	body = inst.InstI32Const(body, allocCursorAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalGet(body, 2) // align
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Sub(body)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalGet(body, 2) // align
	body = numeric.InstI32Sub(body)
	body = numeric.InstI32And(body)
	body = memory.InstI32Store(body, 2, 0)

	// return __lang_alloc(new_size)
	body = inst.InstLocalGet(body, 3) // new_size
	body = inst.InstCall(body, alloc)

	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildHttpEntryBody assembles `__http_entry`, the wrapper
// exported as `wasi:http/incoming-handler@0.2.0#handle`.
//
// Signature: (req: i32, out: i32) → () — the host passes in the
// incoming-request resource handle + the response-outparam
// resource handle.
//
// Pipeline:
//
//  1. Snapshot arena, alloc 64-byte retptr scratch.
//  2. Read method via incoming-request.method (variant: short
//     methods like GET/POST get a dispatch into pre-known
//     strings, OTHER falls through to a `__bytes_to_lang_string`
//     of the host bytes).
//  3. Read path-with-query (option<string>). None → empty string.
//  4. Capture the headers fields handle BEFORE consume (the
//     accessor may trap post-consume).
//  5. Read body via consume → stream → blocking-read accumulator
//     loop, then finish + drop.
//  6. Drop incoming-request.
//  7. Build the HttpRequest struct (28 bytes); populate the
//     HeaderMap from fields.entries via the lang-side
//     __method_HeaderMap_append.
//  8. Drop the fields handle.
//  9. Build a 4-byte Platform { version: 1 } and call user
//     `handle(req_struct, plat)`.
//  10. Read HttpResponse fields (status, body, headers).
//  11. Build outgoing-response: fields.new → fields.append for
//     each (name, value) in resp.headers → outgoing-response
//     constructor → set-status-code → body() → outgoing-body.write.
//  12. response-outparam.set(out, Ok(resp_handle)).
//  13. Drain body bytes through the output-stream chunked write.
//  14. Drop output-stream. Restore arena.
//
// For the request-side method dispatch, this slice keeps things
// simple: discard the discriminant entirely and rely on the
// canonical-ABI Other branch (slot 4/8 ptr/len) for every method.
// The WAT path hardcodes GET/POST/PUT/… via a br_table for
// pointer-eq speedups; the wasmbin slice uses a uniform
// __bytes_to_lang_string round-trip for now. Per-request arenas
// hide the cost. A future PR can land the inline-packed fast
// paths.
//
// Locals (after the 2 params):
//
//	 2: $retptr
//	 3: $method_data
//	 4: $method_len
//	 5: $path_data
//	 6: $path_len
//	 7: $body_data
//	 8: $body_len
//	 9: $body_handle
//	10: $body_stream
//	11: $host_ptr
//	12: $host_len
//	13: $body_buf
//	14: $body_size
//	15: $body_cur
//	16: $body_new_buf
//	17: $req_struct
//	18: $resp_struct
//	19: $status
//	20: $resp_handle
//	21: $headers
//	22: $out_body
//	23: $out_stream
//	24: $arena_handle
//	25: $plat_struct
//	26: $req_fields
//	27: $req_headers
//	28: $hm_names
//	29: $hm_values
//	30: $entries_data
//	31: $entries_count
//	32: $entry_i
//	33: $entry_addr
//	34: $resp_hm
//	35: $resp_hm_names
//	36: $resp_hm_values
//	37: $resp_hm_len
//	38: $resp_hm_i
//	39: $resp_name_addr
//	40: $resp_value_addr
//	41: $write_off
//	42: $write_chunk
//	43: $write_buf
//
// TODO: the WAT path emits a method-name br_table that pins the
// common HTTP verbs to pre-interned inline-form string constants
// for fast pointer-equality compares against user code's `req.method
// == "GET"`. The wasmbin slice shipped here goes through
// __bytes_to_lang_string for every method; the result is
// functionally equivalent but loses the SSO compare seam. Track
// in the wasi-http parity PR (next in the series).
func buildHttpEntryBody(idxs map[string]uint32) []byte {
	alloc := idxs["__lang_alloc"]
	arenaSave := idxs["__lang_arena_save"]
	arenaRestore := idxs["__lang_arena_restore"]
	bytesToStr := idxs["__bytes_to_lang_string"]
	hmAppend, hasHMAppend := idxs["__method_HeaderMap_append"]
	handleFn, hasHandle := idxs["handle"]
	reqMethod := idxs["wasi_http_request_method"]
	reqPath := idxs["wasi_http_request_path_with_query"]
	reqHeaders := idxs["wasi_http_request_headers"]
	reqConsume := idxs["wasi_http_request_consume"]
	reqDrop := idxs["wasi_http_request_drop"]
	bodyStream := idxs["wasi_http_incoming_body_stream"]
	bodyFinish := idxs["wasi_http_incoming_body_finish"]
	futureTrailersDrop := idxs["wasi_http_future_trailers_drop"]
	fieldsNew := idxs["wasi_http_fields_new"]
	fieldsEntries := idxs["wasi_http_fields_entries"]
	fieldsAppend := idxs["wasi_http_fields_append"]
	fieldsDrop := idxs["wasi_http_fields_drop"]
	respNew := idxs["wasi_http_response_new"]
	respSetStatus := idxs["wasi_http_response_set_status"]
	respBody := idxs["wasi_http_response_body"]
	outBodyWrite := idxs["wasi_http_outgoing_body_write"]
	outparamSet := idxs["wasi_http_response_outparam_set"]
	streamDrop := idxs["wasi_io_output_stream_drop"]
	inStreamDrop := idxs["wasi_io_input_stream_drop"]
	blockingRead := idxs["wasi_io_blocking_read"]
	blockingWrite := idxs["wasi_io_blocking_write_and_flush"]

	var body []byte

	// arena_handle = arena_save()
	body = inst.InstCall(body, arenaSave)
	body = inst.InstLocalSet(body, 24)

	// retptr = alloc(64)
	body = inst.InstI32Const(body, 64)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 2)

	// ================ Read method ================
	// incoming-request.method(req, retptr) -> variant at retptr.
	// Variant layout: disc@+0, payload string ptr@+4, payload string len@+8.
	// For "other" the payload string is at +4/+8; for the named
	// HTTP verbs the payload is empty (the verb is implied by the
	// discriminant). To keep the wasmbin slice compact we always
	// read the (ptr, len) slot and round-trip through
	// __bytes_to_lang_string. For Other (disc=9) the host filled
	// the ptr/len with the verb bytes; for known verbs the canonical
	// ABI still writes a 0-len pair (some hosts fill with the verb
	// text anyway). The dispatch-by-disc fast paths come in a
	// follow-up.
	body = inst.InstLocalGet(body, 0) // req
	body = inst.InstLocalGet(body, 2) // retptr
	body = inst.InstCall(body, reqMethod)

	// For each named verb (disc 0..8) we synthesise the canonical
	// string. The dispatch is a chain of if-eq tests, simpler than
	// br_table and fine for a 10-arm sparse switch.
	body = emitMethodDispatch(body, idxs)

	// ================ Read path-with-query ================
	// option<string>: disc@+0 (0=Some, 1=None), Some payload string
	// ptr@+4, string len@+8.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, reqPath)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, encode.ValtypeI32) // pushes data
	{
		// Some: host_ptr @ +4, host_len @ +8.
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 11) // $host_ptr
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 12) // $host_len
		body = inst.InstLocalGet(body, 11)
		body = inst.InstLocalGet(body, 12)
		body = inst.InstCall(body, bytesToStr)
		body = inst.InstLocalSet(body, 6) // $path_len
		body = inst.InstLocalSet(body, 5) // $path_data
		body = inst.InstI32Const(body, 0) // dummy result for if (i32)
	}
	body = inst.InstElse(body)
	{
		// None: empty path.
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 5)
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 6)
		body = inst.InstI32Const(body, 0) // dummy
	}
	body = inst.InstEnd(body)
	body = inst.InstDrop(body) // drop the dummy i32 the if produced

	// ================ Grab headers fields BEFORE consume ================
	body = inst.InstLocalGet(body, 0)
	body = inst.InstCall(body, reqHeaders)
	body = inst.InstLocalSet(body, 26) // $req_fields

	// ================ Read body via consume + stream ================
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, reqConsume)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		// Err: empty body.
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 7)
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 8)
	}
	body = inst.InstElse(body)
	{
		// body_handle = mem[retptr + 4]
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 9)

		// incoming-body.stream(body_handle, retptr) -> result<input-stream>
		body = inst.InstLocalGet(body, 9)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstCall(body, bodyStream)
		body = inst.InstLocalGet(body, 2)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			// Err: empty body.
			body = inst.InstI32Const(body, 0)
			body = inst.InstLocalSet(body, 7)
			body = inst.InstI32Const(body, 0)
			body = inst.InstLocalSet(body, 8)
		}
		body = inst.InstElse(body)
		{
			// body_stream = mem[retptr + 4]
			body = inst.InstLocalGet(body, 2)
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32Add(body)
			body = memory.InstI32Load(body, 2, 0)
			body = inst.InstLocalSet(body, 10)

			// body_buf = alloc(4096); body_size = 4096; body_cur = 0
			body = inst.InstI32Const(body, 4096)
			body = inst.InstCall(body, alloc)
			body = inst.InstLocalSet(body, 13)
			body = inst.InstI32Const(body, 4096)
			body = inst.InstLocalSet(body, 14)
			body = inst.InstI32Const(body, 0)
			body = inst.InstLocalSet(body, 15)

			// Loop blocking-read until error / 0 bytes.
			body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
			body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
			{
				// blocking-read(body_stream, 4096_u64, retptr)
				body = inst.InstLocalGet(body, 10)
				body = inst.InstI64Const(body, 4096)
				body = inst.InstLocalGet(body, 2)
				body = inst.InstCall(body, blockingRead)
				// if disc != 0: break (end-of-stream / err)
				body = inst.InstLocalGet(body, 2)
				body = memory.InstI32Load8U(body, 0, 0)
				body = inst.InstBrIf(body, 1)
				// host_len = mem[retptr + 8]; host_ptr = mem[retptr + 4]
				body = inst.InstLocalGet(body, 2)
				body = inst.InstI32Const(body, 8)
				body = numeric.InstI32Add(body)
				body = memory.InstI32Load(body, 2, 0)
				body = inst.InstLocalTee(body, 12) // host_len
				body = numeric.InstI32Eqz(body)
				body = inst.InstBrIf(body, 1) // 0 bytes → break
				body = inst.InstLocalGet(body, 2)
				body = inst.InstI32Const(body, 4)
				body = numeric.InstI32Add(body)
				body = memory.InstI32Load(body, 2, 0)
				body = inst.InstLocalSet(body, 11) // host_ptr

				// Grow body_buf until body_cur + host_len fits.
				body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
				body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
				{
					// if body_cur + host_len <= body_size: break grow.
					body = inst.InstLocalGet(body, 15)
					body = inst.InstLocalGet(body, 12)
					body = numeric.InstI32Add(body)
					body = inst.InstLocalGet(body, 14)
					body = numeric.InstI32LeU(body)
					body = inst.InstBrIf(body, 1)
					// body_size <<= 1
					body = inst.InstLocalGet(body, 14)
					body = inst.InstI32Const(body, 1)
					body = numeric.InstI32Shl(body)
					body = inst.InstLocalSet(body, 14)
					// body_new_buf = alloc(body_size)
					body = inst.InstLocalGet(body, 14)
					body = inst.InstCall(body, alloc)
					body = inst.InstLocalSet(body, 16)
					// memcpy(body_new_buf, body_buf, body_cur)
					body = inst.InstLocalGet(body, 16)
					body = inst.InstLocalGet(body, 13)
					body = inst.InstLocalGet(body, 15)
					body = append(body, 0xFC, 0x0A, 0x00, 0x00)
					body = inst.InstLocalGet(body, 16)
					body = inst.InstLocalSet(body, 13)
					body = inst.InstBr(body, 0)
				}
				body = inst.InstEnd(body)
				body = inst.InstEnd(body)

				// Append: memcpy(body_buf + body_cur, host_ptr, host_len)
				body = inst.InstLocalGet(body, 13)
				body = inst.InstLocalGet(body, 15)
				body = numeric.InstI32Add(body)
				body = inst.InstLocalGet(body, 11)
				body = inst.InstLocalGet(body, 12)
				body = append(body, 0xFC, 0x0A, 0x00, 0x00)
				body = inst.InstLocalGet(body, 15)
				body = inst.InstLocalGet(body, 12)
				body = numeric.InstI32Add(body)
				body = inst.InstLocalSet(body, 15)
				body = inst.InstBr(body, 0)
			}
			body = inst.InstEnd(body) // end loop
			body = inst.InstEnd(body) // end block

			// body_str = __bytes_to_lang_string(body_buf, body_cur)
			body = inst.InstLocalGet(body, 13)
			body = inst.InstLocalGet(body, 15)
			body = inst.InstCall(body, bytesToStr)
			body = inst.InstLocalSet(body, 8) // body_len
			body = inst.InstLocalSet(body, 7) // body_data

			// Drop input-stream.
			body = inst.InstLocalGet(body, 10)
			body = inst.InstCall(body, inStreamDrop)
		}
		body = inst.InstEnd(body)

		// finish(body_handle) -> future-trailers; drop trailers.
		body = inst.InstLocalGet(body, 9)
		body = inst.InstCall(body, bodyFinish)
		body = inst.InstCall(body, futureTrailersDrop)
	}
	body = inst.InstEnd(body)

	// Drop incoming-request.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstCall(body, reqDrop)

	// ================ Build HttpRequest (28 bytes) ================
	body = inst.InstI32Const(body, 28)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 17)
	body = inst.InstLocalGet(body, 3) // method_data
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 17)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 4) // method_len
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 17)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 5)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 17)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 6)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 17)
	body = inst.InstI32Const(body, 16)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 7)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 17)
	body = inst.InstI32Const(body, 20)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 8)
	body = memory.InstI32Store(body, 2, 0)

	// ================ Build HeaderMap with empty parallel arrays ================
	// names array: length-prefixed string[]; allocate 8 bytes (length
	// prefix + 4 bytes spare so the alignment is clean).
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 28)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, 0) // length=0 at +4
	body = memory.InstI32Store(body, 2, 0)

	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 29)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, 0)
	body = memory.InstI32Store(body, 2, 0)

	// HeaderMap struct (8 bytes): names_ptr@+0, values_ptr@+4.
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 27)
	body = inst.InstLocalGet(body, 28)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 27)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 29)
	body = memory.InstI32Store(body, 2, 0)

	// HttpRequest.headers (offset +24) = req_headers.
	body = inst.InstLocalGet(body, 17)
	body = inst.InstI32Const(body, 24)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 27)
	body = memory.InstI32Store(body, 2, 0)

	// ================ Populate HeaderMap from fields.entries ================
	// fields.entries(req_fields, retptr) → (data_ptr, count) at retptr[0..7].
	body = inst.InstLocalGet(body, 26)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, fieldsEntries)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 30) // entries_data
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 31) // entries_count

	// Each entry is 16 bytes: name_data@+0, name_len@+4,
	// value_data@+8, value_len@+12.
	if hasHMAppend {
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 32)
		body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
		body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstLocalGet(body, 32)
			body = inst.InstLocalGet(body, 31)
			body = numeric.InstI32GeS(body)
			body = inst.InstBrIf(body, 1)
			// entry_addr = entries_data + entry_i * 16
			body = inst.InstLocalGet(body, 30)
			body = inst.InstLocalGet(body, 32)
			body = inst.InstI32Const(body, 16)
			body = numeric.InstI32Mul(body)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalSet(body, 33)
			// __method_HeaderMap_append(headers, name_data, name_len, value_data, value_len)
			body = inst.InstLocalGet(body, 27) // headers struct ptr
			body = inst.InstLocalGet(body, 33) // entry_addr → name_data
			body = memory.InstI32Load(body, 2, 0)
			body = inst.InstLocalGet(body, 33)
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32Add(body)
			body = memory.InstI32Load(body, 2, 0)
			body = inst.InstLocalGet(body, 33)
			body = inst.InstI32Const(body, 8)
			body = numeric.InstI32Add(body)
			body = memory.InstI32Load(body, 2, 0)
			body = inst.InstLocalGet(body, 33)
			body = inst.InstI32Const(body, 12)
			body = numeric.InstI32Add(body)
			body = memory.InstI32Load(body, 2, 0)
			body = inst.InstCall(body, hmAppend)
			// entry_i++
			body = inst.InstLocalGet(body, 32)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalSet(body, 32)
			body = inst.InstBr(body, 0)
		}
		body = inst.InstEnd(body)
		body = inst.InstEnd(body)
	}

	// Drop fields handle.
	body = inst.InstLocalGet(body, 26)
	body = inst.InstCall(body, fieldsDrop)

	// ================ Build Platform { version: 1 } and call handle ================
	body = inst.InstI32Const(body, 4)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 25)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)

	if hasHandle {
		body = inst.InstLocalGet(body, 17)
		body = inst.InstLocalGet(body, 25)
		body = inst.InstCall(body, handleFn)
		body = inst.InstLocalSet(body, 18)
	} else {
		// No user `handle`: synthesise a 500 response struct. Same
		// fallback shape as the WAT path. status@+0=500; body
		// fields zeroed (empty string); headers ptr zeroed.
		body = inst.InstI32Const(body, 24)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalTee(body, 18)
		body = inst.InstI32Const(body, 500)
		body = memory.InstI32Store(body, 2, 0)
	}

	// Load (status, body_data, body_len) from resp_struct.
	body = inst.InstLocalGet(body, 18)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 19)
	body = inst.InstLocalGet(body, 18)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 7)
	body = inst.InstLocalGet(body, 18)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 8)

	// ================ Build outgoing-response ================
	body = inst.InstCall(body, fieldsNew)
	body = inst.InstLocalSet(body, 21)

	// Populate outgoing fields from resp.headers (+16).
	body = inst.InstLocalGet(body, 18)
	body = inst.InstI32Const(body, 16)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalTee(body, 34) // resp_hm
	// If resp_hm == 0 (uninitialised), skip the populate loop.
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		// resp_hm_names = mem[resp_hm + 0]; resp_hm_values = mem[resp_hm + 4]
		body = inst.InstLocalGet(body, 34)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 35)
		body = inst.InstLocalGet(body, 34)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 36)
		// length lives at names + 4 (the length prefix the lang
		// runtime stamps on each string[]).
		body = inst.InstLocalGet(body, 35)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 37)
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 38)
		body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
		body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstLocalGet(body, 38)
			body = inst.InstLocalGet(body, 37)
			body = numeric.InstI32GeS(body)
			body = inst.InstBrIf(body, 1)
			// name_addr = names + 8 + i*8
			body = inst.InstLocalGet(body, 35)
			body = inst.InstI32Const(body, 8)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalGet(body, 38)
			body = inst.InstI32Const(body, 8)
			body = numeric.InstI32Mul(body)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalSet(body, 39)
			// value_addr = values + 8 + i*8
			body = inst.InstLocalGet(body, 36)
			body = inst.InstI32Const(body, 8)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalGet(body, 38)
			body = inst.InstI32Const(body, 8)
			body = numeric.InstI32Mul(body)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalSet(body, 40)
			// fields.append(headers, name_data, name_len, value_data, value_len, retptr)
			body = inst.InstLocalGet(body, 21) // headers handle
			body = inst.InstLocalGet(body, 39)
			body = memory.InstI32Load(body, 2, 0)
			body = inst.InstLocalGet(body, 39)
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32Add(body)
			body = memory.InstI32Load(body, 2, 0)
			body = inst.InstLocalGet(body, 40)
			body = memory.InstI32Load(body, 2, 0)
			body = inst.InstLocalGet(body, 40)
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32Add(body)
			body = memory.InstI32Load(body, 2, 0)
			body = inst.InstLocalGet(body, 2)
			body = inst.InstCall(body, fieldsAppend)
			// i++
			body = inst.InstLocalGet(body, 38)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalSet(body, 38)
			body = inst.InstBr(body, 0)
		}
		body = inst.InstEnd(body)
		body = inst.InstEnd(body)
	}
	body = inst.InstEnd(body)

	// outgoing-response = response_new(headers)
	body = inst.InstLocalGet(body, 21)
	body = inst.InstCall(body, respNew)
	body = inst.InstLocalSet(body, 20)

	// set-status-code; drop the inline disc result.
	body = inst.InstLocalGet(body, 20)
	body = inst.InstLocalGet(body, 19)
	body = inst.InstCall(body, respSetStatus)
	body = inst.InstDrop(body)

	// body(resp_handle, retptr) -> result<outgoing-body>.
	body = inst.InstLocalGet(body, 20)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, respBody)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 22) // out_body

	// outgoing-body.write(out_body, retptr) -> result<output-stream>.
	body = inst.InstLocalGet(body, 22)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, outBodyWrite)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 23) // out_stream

	// ================ response-outparam.set(out, Ok(resp_handle)) ================
	body = inst.InstLocalGet(body, 1) // out
	body = inst.InstI32Const(body, 0) // disc = 0 (Ok)
	body = inst.InstLocalGet(body, 20)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI64Const(body, 0)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, 0)
	body = inst.InstCall(body, outparamSet)

	// ================ Stream response body bytes ================
	// SSO-normalize body string into a heap buffer (write_buf,
	// write_chunk reused as scratch). Reuse $hm_names and
	// related locals as norm scratch is risky; allocate fresh
	// indices via the layout above (41/42/43).
	body = emitStrNormalize(body, idxs, 7, 8, 43, 42, 41)

	// Write loop. write_off=0; while write_off < write_chunk
	// (write_chunk holds byte_len after normalize),
	// blocking-write-and-flush ≤4096 at a time.
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 41)
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 41)
		body = inst.InstLocalGet(body, 42)
		body = numeric.InstI32GeU(body)
		body = inst.InstBrIf(body, 1)
		// chunk_size = min(remaining, 4096); reuse local 40 for chunk.
		body = inst.InstLocalGet(body, 42)
		body = inst.InstLocalGet(body, 41)
		body = numeric.InstI32Sub(body)
		body = inst.InstLocalTee(body, 40)
		body = inst.InstI32Const(body, 4096)
		body = numeric.InstI32GtU(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstI32Const(body, 4096)
		body = inst.InstLocalSet(body, 40)
		body = inst.InstEnd(body)
		// blocking-write-and-flush(out_stream, write_buf + off, chunk, retptr)
		body = inst.InstLocalGet(body, 23)
		body = inst.InstLocalGet(body, 43)
		body = inst.InstLocalGet(body, 41)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 40)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstCall(body, blockingWrite)
		// If disc != 0 (err), break.
		body = inst.InstLocalGet(body, 2)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstBrIf(body, 1)
		// off += chunk
		body = inst.InstLocalGet(body, 41)
		body = inst.InstLocalGet(body, 40)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 41)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body)
	body = inst.InstEnd(body)

	// Drop output-stream.
	body = inst.InstLocalGet(body, 23)
	body = inst.InstCall(body, streamDrop)

	// Restore arena.
	body = inst.InstLocalGet(body, 24)
	body = inst.InstCall(body, arenaRestore)

	// 42 i32 locals after the 2 params (slots 2..43).
	locals := inst.PutLocalsOneGroup(nil, 42, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// emitMethodDispatch reads the method variant landed at retptr by
// incoming-request.method and writes (method_data, method_len) into
// locals 3 + 4. The WAT path uses a br_table over the variant
// discriminant to map onto pre-interned inline-form strings for the
// nine canonical HTTP verbs. wasmbin doesn't have a br_table
// convenience in inst/, so this slice runs the host bytes through
// __bytes_to_lang_string unconditionally — functionally equivalent,
// but the SSO compare seam is lost. Tracked in the parity TODO at
// the top of the wrapper.
//
// Stack on entry: empty (the caller just made the wasi-http method
// call; the variant is in mem[retptr..retptr+12]).
// Stack on exit:  empty.
func emitMethodDispatch(body []byte, idxs map[string]uint32) []byte {
	bytesToStr := idxs["__bytes_to_lang_string"]

	// host_ptr = mem[retptr + 4]; host_len = mem[retptr + 8]
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 11)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 12)
	body = inst.InstLocalGet(body, 11)
	body = inst.InstLocalGet(body, 12)
	body = inst.InstCall(body, bytesToStr)
	body = inst.InstLocalSet(body, 4) // method_len
	body = inst.InstLocalSet(body, 3) // method_data
	return body
}

