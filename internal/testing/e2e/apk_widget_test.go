package e2e

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// xmlElements walks a binary XML file. It gives the number of its start
// elements, and fails when a chunk does not fit, when the resource map is
// not in rising order, or when an element has its attributes out of the
// order of their names in the pool.
func xmlElements(t *testing.T, name string, b []byte) int {
	t.Helper()
	le := binary.LittleEndian
	if len(b) < 8 || le.Uint16(b) != 3 || int(le.Uint32(b[4:])) != len(b) {
		t.Fatalf("%s: not a binary XML file of %d bytes", name, len(b))
	}
	starts := 0
	for at := 8; at < len(b); {
		if at+8 > len(b) {
			t.Fatalf("%s: a chunk header at %d passes the end", name, at)
		}
		kind, size := le.Uint16(b[at:]), int(le.Uint32(b[at+4:]))
		if size < 8 || at+size > len(b) {
			t.Fatalf("%s: the chunk %#x at %d has the size %d", name, kind, at, size)
		}
		switch kind {
		case 0x180:
			last := uint32(0)
			for p := at + 8; p < at+size; p += 4 {
				if id := le.Uint32(b[p:]); id <= last {
					t.Errorf("%s: the resource map has %#x after %#x", name, id, last)
				} else {
					last = id
				}
			}
		case 0x102:
			starts++
			count := int(le.Uint16(b[at+28:]))
			last := int32(-1)
			for k := 0; k < count; k++ {
				attr := int32(le.Uint32(b[at+36+20*k+4:]))
				if attr <= last {
					t.Errorf("%s: an element has the attribute %d after %d", name, attr, last)
				}
				last = attr
			}
		}
		at += size
	}
	return starts
}

// fern -apk gives an app with a provider class and -apk-widget a widget:
// the receiver in the manifest, a resource table with the layout, the
// provider file and the IDs of the items, and the two XML files.
func TestApkWidget(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "widgettest.apk")
	app := e2eharness.RepoPath("tests/android/widget", "app.fern")
	build := func(extra ...string) ([]byte, error) {
		args := append([]string{"-apk", out, "-apk-package", "dev.fern.widgettest"}, extra...)
		cmd := exec.Command(e2eharness.SelfHostCLI(t), append(args, app, e2eharness.SelfHostStdlibRoot(t))...)
		cmd.Env = e2eharness.ChildEnv()
		return cmd.CombinedOutput()
	}
	if o, err := build("-apk-widget", "text:title:Now: ready,button:go:Go"); err != nil {
		t.Fatalf("fern -apk: %v\n%s", err, o)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	zr := zipOf(t, raw)
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if got := strings.Join(names, " "); got != "AndroidManifest.xml classes.dex resources.arsc res/layout/widget.xml res/xml/widget.xml lib/arm64-v8a/libapp.so lib/x86_64/libapp.so" {
		t.Errorf("entries = %s", got)
	}
	man := readZipEntry(t, zr, "AndroidManifest.xml")
	xmlElements(t, "AndroidManifest.xml", man)
	for _, s := range []string{"receiver", "dev.fern.widgettest.Row", "android.appwidget.action.APPWIDGET_UPDATE", "android.appwidget.provider", "resource"} {
		if !bytes.Contains(man, utf16le(s)) {
			t.Errorf("the manifest has no string %q", s)
		}
	}
	// The meta-data of the receiver names the provider file, 0x7f030000,
	// as a reference.
	if !bytes.Contains(man, []byte{8, 0, 0, 1, 0, 0, 3, 0x7f}) {
		t.Error("the manifest has no reference to the resource 0x7f030000")
	}
	table := readZipEntry(t, zr, "resources.arsc")
	if len(table)%4 != 0 || binary.LittleEndian.Uint32(table[4:]) != uint32(len(table)) {
		t.Errorf("the resource table has %d bytes and gives the size %d", len(table), binary.LittleEndian.Uint32(table[4:]))
	}
	for _, s := range []string{"res/layout/widget.xml", "res/xml/widget.xml", "layout", "xml", "id", "widget", "widget_info", "title", "go", "dev.fern.widgettest"} {
		if !bytes.Contains(table, utf16le(s)) {
			t.Errorf("the resource table has no string %q", s)
		}
	}
	layout := readZipEntry(t, zr, "res/layout/widget.xml")
	if n := xmlElements(t, "res/layout/widget.xml", layout); n != 3 {
		t.Errorf("the layout has %d elements, want the row and 2 items", n)
	}
	for _, s := range []string{"LinearLayout", "TextView", "Now: ready", "Go", "layout_weight"} {
		if !bytes.Contains(layout, utf16le(s)) {
			t.Errorf("the layout has no string %q", s)
		}
	}
	// The items have the IDs 0x7f040000 and 0x7f040001.
	for _, id := range [][]byte{{8, 0, 0, 1, 0, 0, 4, 0x7f}, {8, 0, 0, 1, 1, 0, 4, 0x7f}} {
		if !bytes.Contains(layout, id) {
			t.Errorf("the layout has no reference % x", id)
		}
	}
	info := readZipEntry(t, zr, "res/xml/widget.xml")
	if n := xmlElements(t, "res/xml/widget.xml", info); n != 1 {
		t.Errorf("the provider file has %d elements, want 1", n)
	}
	if !bytes.Contains(info, utf16le("appwidget-provider")) || !bytes.Contains(info, []byte{8, 0, 0, 1, 0, 0, 2, 0x7f}) {
		t.Error("the provider file does not name the layout 0x7f020000")
	}
	verifyApkV2(t, raw)

	for _, c := range []struct {
		items string
		want  string
	}{
		{"", "the widget has no items"},
		{"label:title:Now", "takes items such as text:title:Song"},
		{"text:Title:Now", "has small letters, digits and _ only"},
		{"text:a:A,button:a:B", "two items have the name \"a\""},
		{"text:a:A,text:b:B,text:c:C,text:d:D,text:e:E,text:f:F,text:g:G,text:h:H,text:i:I", "takes at most 8 items"},
	} {
		var o []byte
		var err error
		if c.items == "" {
			o, err = build()
		} else {
			o, err = build("-apk-widget", c.items)
		}
		if err == nil || !strings.Contains(string(o), c.want) {
			t.Errorf("-apk-widget %q: want a refusal with %q, got %v\n%s", c.items, c.want, err, o)
		}
	}
	// An app with no provider class takes no widget.
	args := []string{"-apk", filepath.Join(dir, "plain.apk"), "-apk-widget", "text:a:A", e2eharness.RepoPath(nativeApp, "app.fern"), e2eharness.SelfHostStdlibRoot(t)}
	cmd := exec.Command(e2eharness.SelfHostCLI(t), args...)
	cmd.Env = e2eharness.ChildEnv()
	if o, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(o), "needs one @jni class that extends android.appwidget.AppWidgetProvider") {
		t.Errorf("-apk-widget with no provider class: want a refusal, got %v\n%s", err, o)
	}
}
