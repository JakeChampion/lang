package e2e

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestUploadServeInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	for _, streamed := range []bool{false, true} {
		src := writeUploadServeSource(t, streamed)
		addr, _ := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", src))
		checkUploadServe(t, addr)
	}
}

func TestUploadServeX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	for _, streamed := range []bool{false, true} {
		src := writeUploadServeSource(t, streamed)
		out := filepath.Join(filepath.Dir(src), "upload_serve.bin")
		if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, src).CombinedOutput(); err != nil {
			t.Fatalf("build failed: %v\n%s", err, o)
		}
		addr, _ := e2eharness.StartInheritedServer(t, runX86Bin(qemu, out))
		checkUploadServe(t, addr)
	}
}

func writeUploadServeSource(t *testing.T, streamed bool) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "upload_serve.fern")
	mode := "false"
	if streamed {
		mode = "true"
	}
	if err := os.WriteFile(src, []byte(strings.ReplaceAll(uploadServeSource, "@STREAM@", mode)), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

const uploadServeSource = `import "std/http";
import "std/http/form";
import "std/http/multipart";
import "std/platform";
import "std/serve";
import "core/int";

function upload(req: HttpRequest): Result[HttpResponse, multipart.MultipartError] {
  let out: string = "";
  let r: multipart.Reader = multipart.open(req, multipart.Limits { ...multipart.limits(), parts: 4 })?;
  let going: boolean = true;
  while (going) {
    match (r.next()?) {
      NextPart(part, r1) => {
        if (part.filename.len() > 0) {
          let got: (string, multipart.Reader) = sized(part, r1)?;
          out = out + got.0;
          r = got.1;
        } else {
          let got: (u8[], multipart.Reader) = r1.content()?;
          out = out + part.name + "=" + string_from_bytes_unchecked(got.0) + ";";
          r = got.1;
        }
      },
      NoMoreParts => {
        going = false;
      }
    }
  }
  return Ok(http.ok(out));
}

function sized(part: multipart.Part, r: multipart.Reader): Result[(string, multipart.Reader), multipart.MultipartError] {
  let bytes: i32 = 0;
  let sum: i32 = 0;
  let cur: multipart.Reader = r;
  let done: boolean = false;
  while (!done) {
    let piece: (u8[], boolean, multipart.Reader) = cur.read(1000)?;
    for b in piece.0 {
      sum = sum + b as i32;
    }
    bytes = bytes + piece.0.len();
    done = piece.1;
    cur = piece.2;
  }
  return Ok((part.name + "=" + part.filename + "/" + part.content_type + "/" + int.int_to_string(bytes) + "/" + int.int_to_string(sum) + ";", cur));
}

function login(req: HttpRequest): Result[HttpResponse, form.FormError] {
  let f: form.Form = form.parse(req)?;
  let user: string = "";
  match (f.get("user")) {
    Some(u) => {
      user = u;
    },
    None => {}
  }
  return Ok(http.ok("user=" + user + ";langs=" + f.get_all("lang").join(",")));
}

function handle(req: HttpRequest, plat: platform.Host): HttpResponse {
  if (req.path == "/upload") {
    return http.respond(upload(req));
  }
  if (req.path == "/login") {
    return http.respond(login(req));
  }
  return http.not_found();
}

function main(): i32 {
  let opts: serve.Config = serve.Config { ...serve.config(), workers: 1, stream_bodies: @STREAM@ };
  return serve.exit_code(serve.run(0, opts, handle));
}
`

func checkUploadServe(t *testing.T, addr string) {
	t.Helper()
	e2eharness.WaitServerReady(t, addr)
	file := make([]byte, 70000)
	sum := 0
	for i := range file {
		file[i] = byte(i % 253)
		sum += int(file[i])
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("title", "holiday"); err != nil {
		t.Fatal(err)
	}
	part, err := w.CreateFormFile("photo", "beach.png")
	if err != nil {
		t.Fatal(err)
	}
	part.Write(file)
	if err := w.WriteField("tags", "sea, sand"); err != nil {
		t.Fatal(err)
	}
	w.Close()
	resp := post(t, addr, "/upload", w.FormDataContentType(), body.Bytes())
	want := "title=holiday;photo=beach.png/application/octet-stream/70000/" + strconv.Itoa(sum) + ";tags=sea, sand;"
	if resp.status != 200 || resp.body != want {
		t.Errorf("multipart upload: %d %q, want 200 %q", resp.status, resp.body, want)
	}

	var many bytes.Buffer
	mw := multipart.NewWriter(&many)
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		mw.WriteField(name, "1")
	}
	mw.Close()
	resp = post(t, addr, "/upload", mw.FormDataContentType(), many.Bytes())
	if resp.status != 413 || !strings.Contains(resp.body, "more than 4 parts") {
		t.Errorf("too many parts: %d %q, want 413 naming the cap", resp.status, resp.body)
	}

	resp = post(t, addr, "/upload", "application/json", []byte("{}"))
	if resp.status != 415 {
		t.Errorf("json at /upload: %d %q, want 415", resp.status, resp.body)
	}

	resp = post(t, addr, "/login", "application/x-www-form-urlencoded", []byte("user=ann+b&lang=fern&lang=go"))
	if resp.status != 200 || resp.body != "user=ann b;langs=fern,go" {
		t.Errorf("form: %d %q", resp.status, resp.body)
	}
	resp = post(t, addr, "/login", "text/plain", []byte("user=ann"))
	if resp.status != 415 {
		t.Errorf("text at /login: %d %q, want 415", resp.status, resp.body)
	}
}

type posted struct {
	status int
	body   string
}

func post(t *testing.T, addr, path, contentType string, body []byte) posted {
	t.Helper()
	resp, err := http.Post("http://"+addr+path, contentType, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	return posted{resp.StatusCode, string(got)}
}
