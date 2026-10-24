package e2ecompiler

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"image/png"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostShowcaseAssets(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node 22.15 or newer is required for asset codec tests")
	}
	cmd := exec.Command(node, "--test", "../../../examples/showcase/tests/prepare.test.mjs")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("asset encoders: %v\n%s", err, out)
	}
}

func TestSelfHostShowcaseHTTP(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("live server gate runs on x86-64 Linux")
	}
	cli := buildSelfHostCLI(t)
	root, err := filepath.Abs("../../../examples")
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(root, "showcase/server.fern.md")
	cmd := runX86_64Bin(cli.runner, cli.x86Binary(t, entry), root)
	cmd.Env = append(cmd.Environ(), "FERN_ORIGINAL_ACCEPT_ENCODING=Netlify-Original-Accept-Encoding")
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	base := "http://" + addr
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 10 * time.Second, Jar: jar, Transport: &http.Transport{DisableCompression: true}, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	t.Cleanup(client.CloseIdleConnections)
	request := func(method, path, body string, headers map[string]string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return response, data
	}
	for _, path := range []string{"/", "/fractal/", "/rally/", "/mosaic/", "/wave/", "/notes/", "/pulse/", "/_src/showcase/rally.fern"} {
		response, body := request("GET", path, "", nil)
		if response.StatusCode != 200 || !bytes.Contains(body, []byte("<html")) {
			t.Fatalf("%s: status %d, %s", path, response.StatusCode, body)
		}
	}
	response, body := request("GET", "/_src/showcase/server.fern", "", nil)
	if !bytes.Contains(body, []byte("One native server")) || !bytes.Contains(body, []byte("href=\"/_src/showcase/rally.fern.md\"")) {
		t.Fatal("woven lesson or import links absent")
	}
	response, _ = request("GET", "/fractal", "", nil)
	if response.Header.Get("Location") != "/fractal/" {
		t.Fatal("fractal mount redirect differs")
	}
	var config struct {
		Version string `json:"render_version"`
	}
	_, body = request("GET", "/fractal/api/config", "", nil)
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	response, body = request("GET", "/fractal/tile/"+config.Version+"/2/0/0", "", nil)
	if response.StatusCode != 200 {
		t.Fatalf("mounted tile: %d %s", response.StatusCode, body)
	}
	if _, err := png.Decode(bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}

	t.Run("resumable-shared-canvas", func(t *testing.T) {
		req, _ := http.NewRequest("GET", base+"/mosaic/events?room=http-test", nil)
		req.Header.Set("Accept-Encoding", "gzip, zstd")
		stream, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Body.Close()
		if stream.Header.Get("Content-Encoding") != "" {
			t.Fatal("SSE stream was compressed")
		}
		reader := bufio.NewReader(stream.Body)
		readEvent := func() string {
			t.Helper()
			var event strings.Builder
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					t.Fatal(err)
				}
				event.WriteString(line)
				if line == "\n" {
					return event.String()
				}
			}
		}
		if event := readEvent(); !strings.Contains(event, "event: snapshot") {
			t.Fatalf("initial event: %s", event)
		}
		response, body := request("POST", "/mosaic/paint?room=http-test", `{"color":7,"points":[0,1,65]}`, map[string]string{"Content-Type": "application/json"})
		if response.StatusCode != 200 {
			t.Fatalf("paint: %d %s", response.StatusCode, body)
		}
		if event := readEvent(); !strings.Contains(event, "event: paint") || !strings.Contains(event, "id: 1") {
			t.Fatalf("paint event: %s", event)
		}
		stream.Body.Close()
		req, _ = http.NewRequest("GET", base+"/mosaic/events?room=http-test", nil)
		req.Header.Set("Last-Event-ID", "0")
		stream, err = client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Body.Close()
		reader = bufio.NewReader(stream.Body)
		if event := readEvent(); !strings.Contains(event, "event: paint") {
			t.Fatalf("resumed event: %s", event)
		}
		_, data := request("GET", "/mosaic/export.png?room=http-test", "", nil)
		image, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		r, g, b, _ := image.At(0, 0).RGBA()
		if r>>8 != 0xc7 || g>>8 != 0x56 || b>>8 != 0x56 {
			t.Fatal("PNG did not include the shared stroke")
		}
	})
	t.Run("native-wav", func(t *testing.T) {
		response, data := request("GET", "/wave/render.wav?bpm=120", "", nil)
		if response.StatusCode != 200 || len(data) != 96044 || string(data[:4]) != "RIFF" || binary.LittleEndian.Uint32(data[24:]) != 24000 {
			t.Fatal("invalid PCM WAV")
		}
		if bytes.Count(data[44:6044], []byte{0}) == 6000 {
			t.Fatal("waveform is silent")
		}
		cached, _ := request("GET", "/wave/render.wav?bpm=120", "", map[string]string{"If-None-Match": response.Header.Get("ETag")})
		if cached.StatusCode != 304 {
			t.Fatal("audio validator did not produce 304")
		}
	})
	t.Run("signed-html-form", func(t *testing.T) {
		_, data := request("GET", "/notes/", "", nil)
		token := regexp.MustCompile(`name="token" value="([^"]+)"`).FindSubmatch(data)
		if len(token) != 2 {
			t.Fatal("no form token")
		}
		values := url.Values{"name": {"A <guest>"}, "message": {"<script>alert(1)</script>"}, "token": {string(token[1])}}
		for _, invalid := range []string{"control\x00", "noncharacter\ufffe", "noncharacter\uffff"} {
			for _, field := range []string{"name", "message"} {
				valid := values.Get(field)
				values.Set(field, invalid)
				response, _ := request("POST", "/notes/", values.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
				if response.StatusCode != 422 {
					t.Fatalf("invalid Atom text in %s: status %d", field, response.StatusCode)
				}
				values.Set(field, valid)
			}
		}
		response, _ := request("POST", "/notes/", values.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
		if response.StatusCode != 303 || !strings.HasPrefix(response.Header.Get("Location"), "/notes/?posted=1#note-") {
			t.Fatalf("form status %d", response.StatusCode)
		}
		_, data = request("GET", "/notes/", "", nil)
		if !bytes.Contains(data, []byte("&lt;script&gt;alert(1)&lt;/script&gt;")) || bytes.Contains(data, []byte("<script>")) {
			t.Fatal("user text was not escaped")
		}
		response, data = request("GET", "/notes/feed.xml", "", nil)
		var feed struct {
			XMLName xml.Name
			Entries []struct {
				Title string `xml:"title"`
			} `xml:"entry"`
		}
		if err := xml.Unmarshal(data, &feed); err != nil || feed.XMLName.Local != "feed" || len(feed.Entries) != 1 {
			t.Fatalf("invalid Atom feed: %v %s", err, data)
		}
		values.Set("token", "forged")
		response, _ = request("POST", "/notes/", values.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
		if response.StatusCode != 403 {
			t.Fatal("forged form token was accepted")
		}
	})
	t.Run("live-process-metrics", func(t *testing.T) {
		_, data := request("GET", "/pulse/snapshot", "", nil)
		var snapshot struct {
			Requests int
			Memory   struct {
				RSS     *int64 `json:"rss"`
				Threads *int64 `json:"threads"`
			}
			Strokes  int
			Rendered int `json:"rendered_tiles"`
		}
		if err := json.Unmarshal(data, &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.Requests < 10 || snapshot.Memory.RSS == nil || *snapshot.Memory.RSS <= 0 || snapshot.Memory.Threads == nil || snapshot.Strokes != 1 || snapshot.Rendered != 1 {
			t.Fatalf("metrics do not describe actual work: %s", data)
		}
		_, data = request("GET", "/pulse/metrics", "", nil)
		if !bytes.Contains(data, []byte("process_resident_memory_bytes")) {
			t.Fatal("RSS gauge absent")
		}
	})
	t.Run("gzip-and-netlify-negotiation", func(t *testing.T) {
		response, data := request("GET", "/notes/", "", map[string]string{"Accept-Encoding": "gzip"})
		if response.Header.Get("Content-Encoding") != "gzip" {
			t.Fatal("native gzip absent")
		}
		if !strings.Contains(response.Header.Get("Vary"), "Netlify-Original-Accept-Encoding") {
			t.Fatal("proxy header must vary even when absent from this request")
		}
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := io.ReadAll(reader)
		reader.Close()
		if err != nil || !bytes.Contains(decoded, []byte("<html")) {
			t.Fatal("invalid native gzip")
		}
		response, data = request("GET", "/notes/", "", map[string]string{"Accept-Encoding": "gzip", "Netlify-Original-Accept-Encoding": "zstd"})
		if response.Header.Get("Content-Encoding") != "zstd" || len(data) < 4 || !bytes.Equal(data[:4], []byte{0x28, 0xb5, 0x2f, 0xfd}) {
			t.Fatal("original browser encoding did not select zstd")
		}
		vary := response.Header.Get("Vary")
		if !strings.Contains(vary, "Accept-Encoding") || !strings.Contains(vary, "Netlify-Original-Accept-Encoding") {
			t.Fatalf("Vary: %s", vary)
		}
		response, _ = request("GET", "/notes/", "", map[string]string{"Accept-Encoding": "gzip", "Netlify-Original-Accept-Encoding": "identity"})
		if response.Header.Get("Content-Encoding") != "" {
			t.Fatal("identity preference was ignored")
		}
	})
}
