package e2e

import (
	"testing"
)

const httpHandlerBodyWriteProg = `@import("wasi:http/types@0.2.0", "incoming-request")
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
@import("wasi:io/streams@0.2.0", "[method]output-stream.blocking-write-and-flush")
function stream_write(s: borrow OutputStream, bytes: u8[]): Result[i64, i64];
@import("wasi:http/types@0.2.0", "[static]response-outparam.set")
function response_outparam_set(out: own ResponseOutparam, disc: i32, resp: own OutgoingResponse, a: i32, b: i64, c: i32, d: i32, e: i32, f: i32): void;

function set_response_ok(out: own ResponseOutparam, resp: own OutgoingResponse): void {
	response_outparam_set(out, 0, resp, 0, 0 as i64, 0, 0, 0, 0);
}

@export("wasi:http/incoming-handler@0.2.0", "handle")
function on_request(request: own IncomingRequest, response_out: own ResponseOutparam): void {
	let resp: own OutgoingResponse = response_new(fields_new());
	match (response_body(resp)) {
		Ok(body) => {
			match (body_write(body)) {
				Ok(stream) => {
					let bytes: u8[] = [104 as u8, 105 as u8];
					match (stream_write(stream, bytes)) {
						Ok(w) => {},
						Err(e) => {},
					}
				},
				Err(e1) => {},
			}
		},
		Err(e0) => {},
	}
	set_response_ok(response_out, resp);
	return;
}`

func TestExportWasiHttpHandlerBodyWriteServes(t *testing.T) {
	if got := serveHttpHandlerBody(t, httpHandlerBodyWriteProg); got != "hi" {
		t.Fatalf("body = %q; want %q", got, "hi")
	}
}
