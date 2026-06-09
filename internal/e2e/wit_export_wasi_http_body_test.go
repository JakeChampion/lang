package e2e

import (
	"net/http"
	"testing"
)

// httpHandlerBodyProg is a bring-your-own wasi:http handler that obtains the
// response body + its output-stream handles — `outgoing-response.body()` and
// `outgoing-body.write()` both return `result<own<R>>` (a resource handle
// wrapped in a result, returned indirectly), the new capability this slice adds
// (a handle is a valid single-scalar result payload). It then serves an empty
// 200. (Writing bytes via output-stream.blocking-write-and-flush needs the
// variant-err `result<_, stream-error>` return — a distinct follow-on, see
// docs/WIT-BRING-YOUR-OWN.md.)
const httpHandlerBodyProg = `@import("wasi:http/types@0.2.0", "incoming-request")
resource IncomingRequest;
@import("wasi:http/types@0.2.0", "response-outparam")
resource ResponseOutparam;
@import("wasi:http/types@0.2.0", "fields")
resource Fields;
@import("wasi:http/types@0.2.0", "outgoing-response")
resource OutgoingResponse;
@import("wasi:http/types@0.2.0", "outgoing-body")
resource OutgoingBody;
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
@import("wasi:http/types@0.2.0", "[static]response-outparam.set")
function response_outparam_set(out: own ResponseOutparam, disc: i32, resp: own OutgoingResponse, a: i32, b: i64, c: i32, d: i32, e: i32, f: i32): void;

function set_response_ok(out: own ResponseOutparam, resp: own OutgoingResponse): void {
	response_outparam_set(out, 0, resp, 0, 0 as i64, 0, 0, 0, 0);
}

@export("wasi:http/incoming-handler@0.2.0", "handle")
function on_request(request: own IncomingRequest, response_out: own ResponseOutparam): void {
	var resp: own OutgoingResponse = response_new(fields_new());
	match (response_body(resp)) {
		Ok(body) => {
			match (body_write(body)) {
				Ok(stream) => {},
				Err(e1) => {},
			}
		},
		Err(e0) => {},
	}
	set_response_ok(response_out, resp);
	return;
}`

// TestExportWasiHttpHandlerBodyServes proves a bring-your-own handler can obtain
// the response body + output-stream handles via result<own<R>>-returning
// @import externs and serve (200) under `wasmtime serve`.
func TestExportWasiHttpHandlerBodyServes(t *testing.T) {
	if got := serveHttpHandlerStatus(t, httpHandlerBodyProg); got != http.StatusOK {
		t.Fatalf("status = %d; want 200", got)
	}
}
