package e2e

import (
	"net/http"
	"testing"
)

// httpHandlerStatusProg is a bring-your-own wasi:http handler that sets a
// non-default status (404) on its response — exercising set-status-code (a
// resource method whose WIT param is `status-code: u16` — cmd/fern/wit/deps/http/types.wit
// — and result<_,_> return, both flattening to a single i32 core value at the
// canonical ABI) on top of the response constructors — and hands the response
// back via a `set_response_ok` helper that hides response-outparam.set's 9
// flattened canonical params behind a 2-arg Ok call. This is the ergonomic
// shape of a bring-your-own handler (docs/WIT-BRING-YOUR-OWN.md). The Fern-
// side @import binding below declares the param as `i32`, not `u16`: i8/i16/u16
// were removed from the language (#4408), so a Fern function signature can no
// longer mirror a WIT u16 byte-for-byte — only the wire-compatible canonical
// i32 core type is expressible now. The .wit file itself keeps the real WASI
// spec type (`u16`) unchanged; only the Fern-side signature widened.
const httpHandlerStatusProg = `@import("wasi:http/types@0.2.0", "incoming-request")
resource IncomingRequest;
@import("wasi:http/types@0.2.0", "response-outparam")
resource ResponseOutparam;
@import("wasi:http/types@0.2.0", "fields")
resource Fields;
@import("wasi:http/types@0.2.0", "outgoing-response")
resource OutgoingResponse;

@import("wasi:http/types@0.2.0", "[constructor]fields")
function fields_new(): own Fields;
@import("wasi:http/types@0.2.0", "[constructor]outgoing-response")
function response_new(headers: own Fields): own OutgoingResponse;
@import("wasi:http/types@0.2.0", "[method]outgoing-response.set-status-code")
function set_status(resp: borrow OutgoingResponse, status: i32): i32;
@import("wasi:http/types@0.2.0", "[static]response-outparam.set")
function response_outparam_set(out: own ResponseOutparam, disc: i32, resp: own OutgoingResponse, a: i32, b: i64, c: i32, d: i32, e: i32, f: i32): void;

// new_response builds an empty 200 response. set_response_ok hides
// response-outparam.set's canonical result<own<outgoing-response>, error-code>
// flattening (9 core values; slot 4 an i64) behind a 2-arg Ok call.
function new_response(): own OutgoingResponse {
	return response_new(fields_new());
}
function set_response_ok(out: own ResponseOutparam, resp: own OutgoingResponse): void {
	response_outparam_set(out, 0, resp, 0, 0 as i64, 0, 0, 0, 0);
}

@export("wasi:http/incoming-handler@0.2.0", "handle")
function on_request(request: own IncomingRequest, response_out: own ResponseOutparam): void {
	var resp: own OutgoingResponse = new_response();
	var ignore: i32 = set_status(resp, 404);
	set_response_ok(response_out, resp);
	return;
}`

// TestExportWasiHttpHandlerStatusServes proves a bring-your-own handler can set
// a response status (404) and serve it — set-status-code + the ergonomic
// new_response / set_response_ok helpers, end-to-end under `wasmtime serve`.
func TestExportWasiHttpHandlerStatusServes(t *testing.T) {
	if got := serveHttpHandlerStatus(t, httpHandlerStatusProg); got != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", got)
	}
}
