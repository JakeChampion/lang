package e2e

import (
	"testing"
)

// httpHandlerFinishProg is a bring-your-own wasi:http handler that writes the
// body AND calls outgoing-body.finish to seal it — exercising the last two
// extern shapes: an `option<own<trailers>>` parameter (passed None) and a
// `result<_, error-code>` return. error-code is a 39-case variant returned
// indirectly; the handler reads it discriminant-only by modeling the return as
// Result[i64, i64], and the result-return wrapper floors the canonical return
// area at 64 bytes (the canonical-ABI retptr bound) so the host can't overrun
// it (docs/WIT-BRING-YOUR-OWN.md).
const httpHandlerFinishProg = `@import("wasi:http/types@0.2.0", "incoming-request")
resource IncomingRequest;
@import("wasi:http/types@0.2.0", "response-outparam")
resource ResponseOutparam;
@import("wasi:http/types@0.2.0", "fields")
resource Fields;
@import("wasi:http/types@0.2.0", "outgoing-response")
resource OutgoingResponse;
@import("wasi:http/types@0.2.0", "outgoing-body")
resource OutgoingBody;
@import("wasi:http/types@0.2.0", "trailers")
resource Trailers;
@import("wasi:io/streams@0.2.0", "output-stream")
resource OutputStream;

@import("wasi:http/types@0.2.0", "[constructor]fields")
function fields_new(): own Fields;
@import("wasi:http/types@0.2.0", "[constructor]outgoing-response")
function response_new(headers: own Fields): own OutgoingResponse;
@import("wasi:http/types@0.2.0", "[method]outgoing-response.body")
function response_body(resp: borrow OutgoingResponse): Result[own OutgoingBody, u32];
@import("wasi:http/types@0.2.0", "[method]outgoing-body.write")
function body_write(body: borrow OutgoingBody): Result[own OutputStream, u32];
@import("wasi:io/streams@0.2.0", "[method]output-stream.blocking-write-and-flush")
function stream_write(s: borrow OutputStream, bytes: u8[]): Result[i64, i64];
@import("wasi:io/streams@0.2.0", "[resource-drop]output-stream")
function drop_stream(s: own OutputStream): void;
@import("wasi:http/types@0.2.0", "[static]outgoing-body.finish")
function body_finish(body: own OutgoingBody, trailers: Option[own Trailers]): Result[i64, i64];
@import("wasi:http/types@0.2.0", "[static]response-outparam.set")
function response_outparam_set(out: own ResponseOutparam, disc: i32, resp: own OutgoingResponse, a: i32, b: i64, c: i32, d: i32, e: i32, f: i32): void;

function set_response_ok(out: own ResponseOutparam, resp: own OutgoingResponse): void {
	response_outparam_set(out, 0, resp, 0, 0 as i64, 0, 0, 0, 0);
}

// write_hi writes "hi" to the body's output-stream, then explicitly drops the
// stream (a manual [resource-drop] — wasi:http traps finishing a body that still
// has a live child stream, so the stream must be gone before the caller finishes
// the body).
function write_hi(body: borrow OutgoingBody): void {
	match (body_write(body)) {
		Ok(stream) => {
			var bytes: u8[] = [104 as u8, 105 as u8];
			match (stream_write(stream, bytes)) {
				Ok(w) => {},
				Err(e) => {},
			}
			drop_stream(stream);
		},
		Err(e1) => {},
	}
}

@export("wasi:http/incoming-handler@0.2.0", "handle")
function on_request(request: own IncomingRequest, response_out: own ResponseOutparam): void {
	var resp: own OutgoingResponse = response_new(fields_new());
	match (response_body(resp)) {
		Ok(body) => {
			write_hi(body);
			match (body_finish(body, None)) {
				Ok(x) => {},
				Err(y) => {},
			}
		},
		Err(e0) => {},
	}
	set_response_ok(response_out, resp);
	return;
}`

// TestExportWasiHttpHandlerFinishServes proves the full response path: a handler
// writes "hi", calls outgoing-body.finish (option<own<trailers>> param +
// result<_, error-code> return), and `wasmtime serve` delivers 200 + body "hi".
func TestExportWasiHttpHandlerFinishServes(t *testing.T) {
	if got := serveHttpHandlerBody(t, httpHandlerFinishProg); got != "hi" {
		t.Fatalf("body = %q; want %q", got, "hi")
	}
}
