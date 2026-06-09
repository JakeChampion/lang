package e2e

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"testing"
	"time"
)

// httpHandlerBodyWriteProg is a bring-your-own wasi:http handler that writes a
// response body: it gets the outgoing-body + its output-stream
// (result<own<R>> returns), then `blocking-write-and-flush`es "hi". The stream
// write returns `result<_, stream-error>` — modeled as `Result[i64, i64]` so the
// canonical return area (a same-width-scalar Result allocs 16 bytes) safely
// covers the variant `stream-error` result (~12 bytes); the handler reads only
// the discriminant. (No `outgoing-body.finish` — the body still flushes on
// drop; finish needs the wider `result<_, error-code>` area, a follow-on.)
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
	var resp: own OutgoingResponse = response_new(fields_new());
	match (response_body(resp)) {
		Ok(body) => {
			match (body_write(body)) {
				Ok(stream) => {
					var bytes: u8[] = [104 as u8, 105 as u8];
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

// TestExportWasiHttpHandlerBodyWriteServes is the body-write payoff: a
// bring-your-own handler writes "hi" to the response body via
// output-stream.blocking-write-and-flush (a result<_, stream-error> return) and
// `wasmtime serve` delivers it (200 + body "hi").
func TestExportWasiHttpHandlerBodyWriteServes(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	out := buildHttpHandlerComponent(t, dir, httpHandlerBodyWriteProg)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	srv := exec.Command(wasmtime, "serve", "--addr", addr, out)
	var slog bytes.Buffer
	srv.Stdout = &slog
	srv.Stderr = &slog
	if err := srv.Start(); err != nil {
		t.Fatalf("start wasmtime serve: %v", err)
	}
	defer func() {
		_ = srv.Process.Kill()
		_, _ = srv.Process.Wait()
	}()

	url := fmt.Sprintf("http://%s/", addr)
	var resp *http.Response
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err = http.Get(url)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET %s never succeeded: %v\nserver log:\n%s", url, err, slog.String())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200\nserver log:\n%s", resp.StatusCode, slog.String())
	}
	if string(body) != "hi" {
		t.Fatalf("body = %q; want %q\nserver log:\n%s", string(body), "hi", slog.String())
	}
}
