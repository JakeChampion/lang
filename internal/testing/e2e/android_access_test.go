package e2e

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/tools/literate"
)

// The loop thread publishes a tree of two items. The UI thread adds the view
// that holds the tree, with its delegate and provider, and the provider gives
// a node for the view and for each item. An accessibility focus, a click and
// a hover pointer make the events that the service needs, and a click comes
// to the loop thread as a message. The same tree again sends no request.
// Each call releases each local reference that it makes.
func TestAndroidAccessTree(t *testing.T) {
	raw, err := os.ReadFile(e2eharness.RepoPath("internal", "stdlib", "std", "android.fern.md"))
	if err != nil {
		t.Fatal(err)
	}
	src, _, err := literate.Parse(string(raw)).Tangle()
	if err != nil {
		t.Fatal(err)
	}
	src += `
pub function access_ctx(activity: usize): usize { return new_context(activity); }
pub function message_fd(ctx: usize): i32 { unsafe { return Context.c_load_message_read(ctx); } }
pub function reply_fd(ctx: usize): i32 { unsafe { return Context.c_load_reply_write(ctx); } }
pub function ui_fd(ctx: usize): i32 { unsafe { return Context.c_load_ui_read(ctx); } }
pub function publish(ctx: usize): void {
  publish_tree(ctx, [AccessItem { id: 0, x: 0, y: 0, w: 100, h: 50, text: "Clock", actions: 0 }, AccessItem { id: 1, x: 0, y: 100, w: 200, h: 80, text: "Maps", actions: ACCESS_CLICK }]);
}
pub function hover_at(ctx: usize, x: i32, y: i32): void { hover(ctx, 9, x, y); }
pub function pump(ctx: usize): i32 { unsafe { return on_ui_request(Context.c_load_ui_read(ctx), 1, ctx); } }
pub function provider(env: usize, thiz: usize): usize { return access_provider(env, thiz); }
pub function node(env: usize, thiz: usize, id: i32): usize { return access_node(env, thiz, id); }
pub function act(env: usize, thiz: usize, id: i32, action: i32): i32 {
  if (access_action(env, thiz, id, action, 0 as usize)) {
    return 1;
  }
  return 0;
}
`
	const check = `#include <poll.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <dlfcn.h>

static int locals, globals, next = 1000, nnames, nstrings;
static char log_[16384];
static uintptr_t ctx_field;
static const char *names[256];
static struct { uintptr_t id; char text[64]; } strings[256];
static void require(int ok, const char *what) { if (!ok) { fprintf(stderr, "access: %s (locals=%d globals=%d)\nlog: %s\n", what, locals, globals, log_); exit(1); } }
static uintptr_t fresh(void) { locals++; return next++; }
static const char *string_of(uintptr_t id) {
  for (int k = 0; k < nstrings; k++) if (strings[k].id == id) return strings[k].text;
  return 0;
}
static void note_call(const char *name, const uint64_t *args, int n) {
  char line[256];
  int at = snprintf(line, sizeof line, "%s(", name);
  for (int k = 0; k < n; k++) {
    const char *s = string_of(args[k]);
    if (s) at += snprintf(line + at, sizeof line - at, "%s'%s'", k ? "," : "", s);
    else at += snprintf(line + at, sizeof line - at, "%s%ld", k ? "," : "", (long)(int32_t)args[k]);
  }
  snprintf(line + at, sizeof line - at, ");");
  require(strlen(log_) + strlen(line) < sizeof log_, "the log is full");
  strcat(log_, line);
}
static int arity(const char *sig) {
  int n = 0;
  for (const char *p = sig + 1; *p != ')'; p++) {
    while (*p == '[') p++;
    if (*p == 'L') while (*p != ';') p++;
    n++;
  }
  return n;
}
static int arities[256];
static uintptr_t method(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  require(nnames < 256, "too many method ids");
  arities[nnames] = arity(sig);
  names[nnames++] = name;
  return nnames;
}
static uintptr_t find_class(uintptr_t e, const char *name) { char line[160]; snprintf(line, sizeof line, "class %s;", name); strcat(log_, line); return fresh(); }
static uintptr_t get_class(uintptr_t e, uintptr_t obj) { return fresh(); }
static uintptr_t new_object(uintptr_t e, uintptr_t cls, uintptr_t ctor, const uint64_t *args) { note_call("new", args, arities[ctor - 1]); return fresh(); }
static uintptr_t object(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  const char *n = names[id - 1];
  note_call(n, args, arities[id - 1]);
  if (!strcmp(n, "loadClass")) {
    const char *s = string_of(args[0]);
    locals++;
    return !strcmp(s, "dev.fern.android.AccessDelegate") ? 3001 : 3002;
  }
  if (!strcmp(n, "getPackageName")) {
    uintptr_t s = fresh();
    strings[nstrings].id = s;
    strcpy(strings[nstrings++].text, "dev.fern.paper");
    return s;
  }
  return fresh();
}
static uintptr_t static_object(uintptr_t e, uintptr_t cls, uintptr_t id, const uint64_t *args) { note_call(names[id - 1], args, arities[id - 1]); return fresh(); }
static uintptr_t vcall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) { note_call(names[id - 1], args, arities[id - 1]); return 0; }
static uint8_t bcall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) { note_call(names[id - 1], args, arities[id - 1]); return 1; }
static uintptr_t new_global(uintptr_t e, uintptr_t ref) { globals++; return ref + 100000; }
static void delete_global(uintptr_t e, uintptr_t ref) { globals--; }
static uintptr_t new_local(uintptr_t e, uintptr_t ref) { locals++; return ref; }
static uintptr_t field_id(uintptr_t e, uintptr_t cls, const char *name, const char *sig) { require(!strcmp(name, "fern"), "the static field fern"); return 77; }
static uintptr_t get_long(uintptr_t e, uintptr_t cls, uintptr_t id) { return ctx_field; }
static void set_long(uintptr_t e, uintptr_t cls, uintptr_t id, int64_t v) { ctx_field = (uintptr_t)v; }
static uintptr_t new_string(uintptr_t e, const uint16_t *units, uintptr_t n) {
  uintptr_t s = fresh();
  require(n < 64 && nstrings < 256, "a short string");
  strings[nstrings].id = s;
  for (uintptr_t i = 0; i < n; i++) strings[nstrings].text[i] = (char)units[i];
  strings[nstrings++].text[n] = 0;
  return s;
}
static uintptr_t new_int_array(uintptr_t e, uintptr_t n) { return fresh(); }
static void int_region(uintptr_t e, uintptr_t array, uintptr_t start, uintptr_t n, int32_t *buf) { buf[0] = 10; buf[1] = 20; }
static uintptr_t none(uintptr_t e) { return 0; }
static void release(uintptr_t e, uintptr_t ref) { if (ref) locals--; }
static int ready(int fd) { struct pollfd p = { fd, POLLIN, 0 }; return poll(&p, 1, 0) == 1; }
static int has(const char *part) { return strstr(log_, part) != 0; }
int main(int argc, char **argv) {
  alarm(20);
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 2; }
  uintptr_t (*access_ctx)(uintptr_t) = dlsym(lib, "access_ctx");
  int (*message_fd)(uintptr_t) = dlsym(lib, "message_fd");
  int (*reply_fd)(uintptr_t) = dlsym(lib, "reply_fd");
  int (*ui_fd)(uintptr_t) = dlsym(lib, "ui_fd");
  void (*publish)(uintptr_t) = dlsym(lib, "publish");
  void (*hover_at)(uintptr_t, int, int) = dlsym(lib, "hover_at");
  int (*pump)(uintptr_t) = dlsym(lib, "pump");
  uintptr_t (*provider)(uintptr_t, uintptr_t) = dlsym(lib, "provider");
  uintptr_t (*node)(uintptr_t, uintptr_t, int) = dlsym(lib, "node");
  int (*act)(uintptr_t, uintptr_t, int, int) = dlsym(lib, "act");
  require(access_ctx && message_fd && reply_fd && ui_fd && publish && hover_at && pump && provider && node && act, "the entries");
  uintptr_t table[240] = {0}, activity[10] = {0};
  table[6] = (uintptr_t)find_class;
  table[15] = (uintptr_t)none;
  table[17] = (uintptr_t)none;
  table[21] = (uintptr_t)new_global;
  table[22] = (uintptr_t)delete_global;
  table[23] = (uintptr_t)release;
  table[25] = (uintptr_t)new_local;
  table[30] = (uintptr_t)new_object;
  table[31] = (uintptr_t)get_class;
  table[33] = (uintptr_t)method;
  table[36] = (uintptr_t)object;
  table[39] = (uintptr_t)bcall;
  table[63] = (uintptr_t)vcall;
  table[113] = (uintptr_t)method;
  table[116] = (uintptr_t)static_object;
  table[144] = (uintptr_t)field_id;
  table[151] = (uintptr_t)get_long;
  table[160] = (uintptr_t)set_long;
  table[163] = (uintptr_t)new_string;
  table[179] = (uintptr_t)new_int_array;
  table[203] = (uintptr_t)int_region;
  uintptr_t functions = (uintptr_t)table;
  uintptr_t env = (uintptr_t)&functions;
  activity[2] = env;
  activity[3] = 100;
  uintptr_t ctx = access_ctx((uintptr_t)activity);
  activity[7] = ctx;

  hover_at(ctx, 5, 5);
  require(!ready(ui_fd(ctx)), "a hover pointer before a tree sends no request");
  publish(ctx);
  require(log_[0] == 0, "a tree waits for the UI thread");
  require(pump(ctx) == 1 && locals == 0 && globals == 4, "the first tree adds the view");
  require(has("class android/view/View;new(100);") && has("setImportantForAccessibility(1);") && has("loadClass('dev.fern.android.AccessDelegate');") && has("loadClass('dev.fern.android.AccessTree');") && has("setAccessibilityDelegate(") && has("sendAccessibilityEvent(2048);"), "the view, its delegate and the content change");
  require(ctx_field == ctx, "the classes hold the context");
  publish(ctx);
  require(!ready(ui_fd(ctx)), "the same tree sends no request");

  log_[0] = 0;
  uintptr_t p = provider(env, 3001);
  require(p != 0 && locals == 1, "the delegate gives the provider as a local reference");
  release(env, p);
  uintptr_t host = node(env, p, -1);
  require(host != 0 && has("onInitializeAccessibilityNodeInfo(") && has("addChild(") && strstr(strstr(log_, "addChild("), "addChild(") && locals == 1, "the node of the view lists the items");
  require(has(",0);") && has(",1);"), "the node of the view has a child for each id");
  release(env, host);

  log_[0] = 0;
  uintptr_t maps = node(env, p, 1);
  require(maps != 0 && locals == 1, "the node of an item");
  release(env, maps);
  require(has("setText('Maps');") && has("setClassName('android.widget.Button');") && has("setPackageName('dev.fern.paper');") && has("new(0,100,200,180);") && has("offset(10,20);") && has("addAction(16);setClickable(1);") && has("addAction(64);") && !has("setAccessibilityFocused"), "the node of a button");
  log_[0] = 0;
  uintptr_t clock = node(env, p, 0);
  release(env, clock);
  require(has("setClassName('android.widget.TextView');") && !has("addAction(16)"), "the node of text");
  require(node(env, p, 7) == 0 && locals == 0, "no node for an id that is not in the tree");

  log_[0] = 0;
  require(act(env, p, 1, 64) == 1 && act(env, p, 1, 64) == 0 && locals == 0, "the accessibility focus moves once");
  require(has("obtain(32768);") && has("setSource(") && has("requestSendAccessibilityEvent("), "the focus sends its event");
  log_[0] = 0;
  maps = node(env, p, 1);
  release(env, maps);
  require(has("setAccessibilityFocused(1);addAction(128);"), "the focused node can lose the focus");

  char one = 1;
  require(write(reply_fd(ctx), &one, 1) == 1, "a reply");
  require(act(env, p, 1, 16) == 1 && locals == 0, "a click on a button");
  uint64_t msg[2];
  require(read(message_fd(ctx), msg, 16) == 16 && (int32_t)msg[0] == 27 && msg[1] == (1 << 8 | 16), "an Access message with the id and the action");
  require(act(env, p, 0, 16) == 0 && act(env, p, 1, 32) == 0 && !ready(message_fd(ctx)), "an item takes only its actions");

  log_[0] = 0;
  hover_at(ctx, 50, 150);
  require(pump(ctx) == 1 && locals == 0 && has("obtain(128);") && has("'Maps'"), "a hover pointer enters the item under it");
  log_[0] = 0;
  hover_at(ctx, 60, 160);
  require(pump(ctx) == 1 && log_[0] == 0, "a move in the same item sends no event");
  hover_at(ctx, 50, 20);
  require(pump(ctx) == 1 && has("obtain(256);") && has("obtain(128);") && has("'Clock'") && locals == 0, "a move to another item exits one and enters the other");
  return 0;
}
`
	androidJNIMockCheck(t, src, "access_ctx,message_fd,reply_fd,ui_fd,publish,hover_at,pump,provider,node,act", check)
}
