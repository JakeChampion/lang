package e2eharness

import (
	"bytes"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"
)

const HTTPByteSerializationProgram = `import "std/http";
import "std/stream";
function emit(data: u8[]): void {
    let w = stdout();
    match (w.write_bytes(data)) { Some(_) => { assert(false); }, None => {} }
}
function producer(data: u8[]): HttpResponse {
    return http.chunks(200, (i: i32): Option[u8[]] => {
        eprint("producer-call");
        if (i == 0) { return Some(data); }
        if (i == 1) { return Some([0 as u8, 255 as u8]); }
        return None;
    });
}
function main(): i32 {
    let all: u8[] = [];
    for i in 0..256 { all = all.append(i as u8); }
    let binary = http.bytes(200, all);
    emit(http.http_serialize_response_bytes(binary));
    emit(http.http_serialize_response_conn_bytes(binary, true));
    emit(http.http_serialize_response_to_bytes("HEAD", binary, false));
    emit(http.http_serialize_response_bytes(http.ok("aé𐐷z")));
    emit(http.http_serialize_response_bytes(http.stream(200, Stream { data: all, pos: 1 })));
    emit(http.http_serialize_response_bytes(producer(all)));
    emit(http.http_serialize_response_to_bytes("HEAD", producer(all), false));
    let empty: u8[] = [];
    emit(http.http_serialize_response_bytes(http.bytes(200, empty)));
    emit(http.http_serialize_response_bytes(http.file("/must-not-be-read")));
    let headers = binary.with_header("Content-Length", "999").with_header("Transfer-Encoding", "chunked").with_header("Connection", "wrong").with_header("X-Proof", "ok");
    emit(http.http_serialize_response_bytes(headers));
    for status in [101, 204, 304] {
        let no_body = HttpResponse { ...producer(all), status: status };
        emit(http.http_serialize_response_bytes(no_body));
        emit(http.http_serialize_response(no_body).bytes());
    }
    let bad = http.bytes(200, [255 as u8, 65 as u8, 128 as u8]);
    emit(http.http_serialize_response(bad).bytes());
    emit(http.http_serialize_response_conn(bad, true).bytes());
    emit(http.http_serialize_response_to("HEAD", bad, false).bytes());
    // Only one malformed byte per run, so the exact replacement oracle is
    // independent of how decoders group consecutive malformed sequences.
    emit(http.http_serialize_response(producer([65 as u8])).bytes());
    emit(http.http_serialize_response_to("HEAD", producer([65 as u8]), false).bytes());
    return 0;
}
`

func HTTPByteSerializationOutput() []byte {
	var out []byte
	add := func(status int, body []byte, headOnly, keepAlive, proof bool) {
		header := fmt.Sprintf("HTTP/1.1 %d %s\r\n", status, http.StatusText(status))
		if proof {
			header += "x-proof: ok\r\n"
		}
		bodiless := status < 200 || status == 204 || status == 304
		if !bodiless {
			header += fmt.Sprintf("Content-Length: %d\r\n", len(body))
		}
		connection := "close"
		if keepAlive {
			connection = "keep-alive"
		}
		header += "Connection: " + connection + "\r\n\r\n"
		out = append(out, header...)
		if !bodiless && !headOnly {
			out = append(out, body...)
		}
	}
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	add(200, all, false, false, false)
	add(200, all, false, true, false)
	add(200, all, true, false, false)
	add(200, []byte("aé𐐷z"), false, false, false)
	add(200, all[1:], false, false, false)
	chunks := append(bytes.Clone(all), 0, 255)
	add(200, chunks, false, false, false)
	add(200, chunks, true, false, false)
	add(200, nil, false, false, false)
	add(200, nil, false, false, false)
	add(200, all, false, false, true)
	for _, status := range []int{101, 204, 304} {
		add(status, nil, false, false, false)
		add(status, nil, false, false, false)
	}
	text := []byte("\uFFFDA\uFFFD")
	add(200, text, false, false, false)
	add(200, text, false, true, false)
	add(200, text, true, false, false)
	add(200, []byte("A\x00\uFFFD"), false, false, false)
	add(200, []byte("A\x00\uFFFD"), true, false, false)
	return out
}

func CheckHTTPByteSerialization(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()
	var out, diagnostic bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &diagnostic
	if err := cmd.Run(); err != nil {
		t.Fatalf("serialize: %v\n%s", err, &diagnostic)
	}
	if strings.Count(diagnostic.String(), "producer-call") != 12 || strings.Contains(diagnostic.String(), "fern-sanitizer:") {
		t.Fatalf("producer must run once per serialized body, and never for bodiless status:\n%s", &diagnostic)
	}
	want := HTTPByteSerializationOutput()
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("wire output differs:\ngot %q\nwant %q", out.Bytes(), want)
	}
	return diagnostic.String()
}
