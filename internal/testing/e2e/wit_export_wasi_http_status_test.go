package e2e

import (
	"net/http"
	"testing"
)

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
	let resp: own OutgoingResponse = new_response();
	let ignore: i32 = set_status(resp, 404);
	set_response_ok(response_out, resp);
	return;
}`

func TestExportWasiHttpHandlerStatusServes(t *testing.T) {
	if got := serveHttpHandlerStatus(t, httpHandlerStatusProg); got != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", got)
	}
}
