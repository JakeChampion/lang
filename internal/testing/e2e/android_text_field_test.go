package e2e

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/tools/literate"
)

// The loop thread asks the UI thread to show, change and hide the text field
// of the keyboard. The first request adds a hidden EditText to the window,
// with a TextField that watches it. A change of the text goes to the loop
// thread as a message, but a change that a request makes does not. Each
// request releases each local reference that it makes.
func TestAndroidTextFieldRequests(t *testing.T) {
	raw, err := os.ReadFile(e2eharness.RepoPath("internal", "stdlib", "std", "android.fern.md"))
	if err != nil {
		t.Fatal(err)
	}
	src, _, err := literate.Parse(string(raw)).Tangle()
	if err != nil {
		t.Fatal(err)
	}
	src += `
pub function field_ctx(activity: usize): usize { return new_context(activity); }
pub function message_fd(ctx: usize): i32 { unsafe { return Context.c_load_message_read(ctx); } }
pub function reply_fd(ctx: usize): i32 { unsafe { return Context.c_load_reply_write(ctx); } }
pub function show(ctx: usize): void { show_field(ctx, "ab", 1, 2); }
pub function set(ctx: usize): void { set_field(ctx, "xyz"); }
pub function set_at(ctx: usize): void { set_field_at(ctx, "xyz", 1); }
pub function show_lines(ctx: usize): void { show_field(ctx, "a", INPUT_LINES, 6); }
pub function hide(ctx: usize): void { hide_field(ctx); }
pub function pump(ctx: usize): i32 { unsafe { return on_ui_request(Context.c_load_ui_read(ctx), 1, ctx); } }
pub function changed(env: usize, thiz: usize, editable: usize): void { text_changed(env, thiz, editable); }
`
	const check = `#include <poll.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <dlfcn.h>

static int locals, globals;
static char log_[4096], text[64];
static uintptr_t fern_field, ctx_;
static void (*changed)(uintptr_t, uintptr_t, uintptr_t);
static uintptr_t env;
static const char *names[64];
static int nnames;
static void require(int ok, const char *what) { if (!ok) { fprintf(stderr, "text field: %s (locals=%d globals=%d)\nlog: %s\n", what, locals, globals, log_); exit(1); } }
static void note(const char *s) { strcat(log_, s); strcat(log_, ";"); }
static uintptr_t find_class(uintptr_t e, const char *name) {
  locals++;
  if (!strcmp(name, "android/widget/EditText")) return 2001;
  require(!strcmp(name, "android/view/ViewGroup$LayoutParams"), name);
  return 2002;
}
static uintptr_t get_class(uintptr_t e, uintptr_t obj) { locals++; return obj + 1000; }
static uintptr_t method(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  require(nnames < 64, "too many method ids");
  names[nnames++] = name;
  return nnames;
}
static const char *name_of(uintptr_t id) { return names[id - 1]; }
static uintptr_t new_object(uintptr_t e, uintptr_t cls, uintptr_t ctor, const uint64_t *args) {
  locals++;
  if (cls == 2001) { require(args[0] == 100, "an EditText for the activity"); note("new EditText"); return 300; }
  if (cls == 2002) { require(args[0] == 1 && args[1] == 1, "a field of one pixel"); return 301; }
  require(cls == 2003, "a TextField");
  note("new TextField");
  return 302;
}
static uintptr_t object(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  const char *n = name_of(id);
  locals++;
  if (!strcmp(n, "getClassLoader")) { require(obj == 100, "the class loader of the activity"); return 400; }
  if (!strcmp(n, "loadClass")) { require(obj == 400 && args[0] == 700, "loadClass of TextField"); return 2003; }
  if (!strcmp(n, "getSystemService")) { require(obj == 100 && args[0] == 700, "the input method service"); return 500; }
  if (!strcmp(n, "getWindowToken")) { require(obj == 900, "the token of the field"); return 600; }
  require(!strcmp(n, "toString") && obj == 800, "toString of the Editable");
  return 801;
}
static uintptr_t vcall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  const char *n = name_of(id);
  char line[128];
  if (!strcmp(n, "setText")) {
    require(obj == 900 && args[0] == 700, "setText on the field");
    snprintf(line, sizeof line, "setText %s", text);
    note(line);
    if (fern_field) changed(env, 302, 800);
    return 0;
  }
  snprintf(line, sizeof line, "%s %lu %lu", n, (unsigned long)obj, (unsigned long)args[0]);
  if (!strcmp(n, "clearFocus")) snprintf(line, sizeof line, "%s %lu", n, (unsigned long)obj);
  note(line);
  return 0;
}
static uint8_t bcall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  const char *n = name_of(id);
  char line[128];
  if (!strcmp(n, "requestFocus")) { snprintf(line, sizeof line, "requestFocus %lu", (unsigned long)obj); note(line); return 1; }
  require(obj == 500 && args[1] == 0, "a call on InputMethodManager");
  snprintf(line, sizeof line, "%s %lu", n, (unsigned long)args[0]);
  note(line);
  return 1;
}
static int32_t icall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  require(obj == 900 && !strcmp(name_of(id), "length"), "length of the field");
  return (int32_t)strlen(text);
}
static uintptr_t new_global(uintptr_t e, uintptr_t ref) { globals++; return ref == 300 ? 900 : ref + 5000; }
static void delete_global(uintptr_t e, uintptr_t ref) { globals--; }
static uintptr_t field_id(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  require(!strcmp(name, "fern") && !strcmp(sig, "J"), "the static field fern");
  return 77;
}
static uintptr_t get_long(uintptr_t e, uintptr_t cls, uintptr_t id) { return fern_field; }
static void set_long(uintptr_t e, uintptr_t cls, uintptr_t id, int64_t v) { require(cls == 2003, "the field of TextField"); fern_field = (uintptr_t)v; }
static uintptr_t new_string(uintptr_t e, const uint16_t *units, uintptr_t n) {
  if (n == 12 && units[0] == 'i') { locals++; return 700; }
  require(n < sizeof text, "a short text");
  for (uintptr_t i = 0; i < n; i++) text[i] = (char)units[i];
  text[n] = 0;
  locals++;
  return 700;
}
static int32_t utf_length(uintptr_t e, uintptr_t s) { require(s == 801, "the text of the Editable"); return (int32_t)strlen(text); }
static const char *utf_chars(uintptr_t e, uintptr_t s, uintptr_t copy) { return text; }
static void release_chars(uintptr_t e, uintptr_t s, const char *c) {}
static uintptr_t none(uintptr_t e) { return 0; }
static void release(uintptr_t e, uintptr_t ref) { if (ref) locals--; }
static int ready(int fd) { struct pollfd p = { fd, POLLIN, 0 }; return poll(&p, 1, 0) == 1; }
int main(int argc, char **argv) {
  alarm(20);
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 2; }
  uintptr_t (*field_ctx)(uintptr_t) = dlsym(lib, "field_ctx");
  int (*message_fd)(uintptr_t) = dlsym(lib, "message_fd");
  int (*reply_fd)(uintptr_t) = dlsym(lib, "reply_fd");
  void (*show)(uintptr_t) = dlsym(lib, "show");
  void (*set)(uintptr_t) = dlsym(lib, "set");
  void (*set_at)(uintptr_t) = dlsym(lib, "set_at");
  void (*show_lines)(uintptr_t) = dlsym(lib, "show_lines");
  void (*hide)(uintptr_t) = dlsym(lib, "hide");
  int (*pump)(uintptr_t) = dlsym(lib, "pump");
  changed = dlsym(lib, "changed");
  require(field_ctx && message_fd && reply_fd && show && set && hide && pump && changed, "the entries");
  uintptr_t table[240] = {0}, activity[10] = {0};
  table[6] = (uintptr_t)find_class;
  table[15] = (uintptr_t)none;
  table[17] = (uintptr_t)none;
  table[21] = (uintptr_t)new_global;
  table[22] = (uintptr_t)delete_global;
  table[23] = (uintptr_t)release;
  table[30] = (uintptr_t)new_object;
  table[31] = (uintptr_t)get_class;
  table[33] = (uintptr_t)method;
  table[36] = (uintptr_t)object;
  table[39] = (uintptr_t)bcall;
  table[51] = (uintptr_t)icall;
  table[63] = (uintptr_t)vcall;
  table[144] = (uintptr_t)field_id;
  table[151] = (uintptr_t)get_long;
  table[160] = (uintptr_t)set_long;
  table[163] = (uintptr_t)new_string;
  table[168] = (uintptr_t)utf_length;
  table[169] = (uintptr_t)utf_chars;
  table[170] = (uintptr_t)release_chars;
  uintptr_t functions = (uintptr_t)table;
  env = (uintptr_t)&functions;
  activity[2] = env;
  activity[3] = 100;
  ctx_ = field_ctx((uintptr_t)activity);
  activity[7] = ctx_;

  show(ctx_);
  require(log_[0] == 0, "a request waits for the UI thread");
  require(pump(ctx_) == 1 && locals == 0 && globals == 2, "the first request adds the field");
  require(!strcmp(log_, "new EditText;setSingleLine 300 1;addContentView 100 300;new TextField;addTextChangedListener 300 302;setOnEditorActionListener 300 302;setSingleLine 900 1;setInputType 900 1;setImeOptions 900 268435458;setText ab;setSelection 900 2;requestFocus 900;showSoftInput 900;"), "show");
  require(fern_field == ctx_ && !ready(message_fd(ctx_)), "a request changes the text with no message");

  log_[0] = 0;
  strcpy(text, "abc");
  char one = 1;
  require(write(reply_fd(ctx_), &one, 1) == 1, "a reply");
  changed(env, 302, 800);
  uint64_t msg[2];
  require(read(message_fd(ctx_), msg, 16) == 16 && (int32_t)msg[0] == 25, "a Text message");
  const uint8_t *buf = (const uint8_t *)msg[1];
  require(*(const int32_t *)buf == 3 && !memcmp(buf + 4, "abc", 3), "the text of the field");
  require(locals == 0 && log_[0] == 0, "a change makes no call on the field");

  set(ctx_);
  hide(ctx_);
  require(pump(ctx_) == 1 && locals == 0 && globals == 2, "two requests in one pump");
  require(!strcmp(log_, "setText xyz;setSelection 900 3;hideSoftInputFromWindow 600;clearFocus 900;"), "set and hide");
  require(!ready(message_fd(ctx_)), "set_field sends no Text message");

  log_[0] = 0;
  set_at(ctx_);
  show_lines(ctx_);
  require(pump(ctx_) == 1 && locals == 0, "a cursor and a field of lines");
  require(!strcmp(log_, "setText xyz;setSelection 900 1;setSingleLine 900 0;setInputType 900 131073;setImeOptions 900 268435462;setText a;setSelection 900 1;requestFocus 900;showSoftInput 900;"), "the cursor of set_field_at, and a field of more than one line");
  return 0;
}
`
	androidJNIMockCheck(t, src, "field_ctx,message_fd,reply_fd,show,set,set_at,show_lines,hide,pump,changed", check)
}
