package e2e

import "testing"

// The media browser of std/android/browse on a fake JNI. A request with no
// loop detaches its result and gets an empty answer at once. An answer
// gives a MediaItem for each item, browsable or playable, and sends the
// list. parent and media_id read the ids of the requests and of a play.
// connect gives the service the token of the session. Each call ends each
// local frame that it starts and deletes each global reference that it
// takes over.
func TestAndroidBrowse(t *testing.T) {
	const src = `import "std/android";
import "std/android/browse";
pub function ask(e: usize, parent: usize, result: usize): void {
  browse.children(e, 77 as usize, parent, result);
}
pub function reply(ctx: usize, held: usize): i32 {
  let items = [browse.Item { id: "artist:1", title: "Band", subtitle: "3 albums", playable: false }, browse.Item { id: "track:9", title: "Song", subtitle: "Band", playable: true }];
  return if (browse.answer(ctx, browse.Request { held: held }, items)) { 1 } else { 0 };
}
pub function parent_size(ctx: usize, held: usize): i32 {
  return browse.parent(ctx, browse.Request { held: held }).len();
}
pub function id_size(ctx: usize, held: usize): i32 {
  return browse.media_id(ctx, browse.MediaId { held: held }).len();
}
pub function join(ctx: usize, held: usize, token: usize): i32 {
  return if (browse.connect(ctx, browse.Service { held: held }, token)) { 1 } else { 0 };
}
pub function kind(k: i32): i32 {
  return match (browse.event(android.Posted(k, 5 as usize))) {
    Some(browse.Connected(_)) => 1,
    Some(browse.Asked(_)) => 2,
    Some(browse.PlayFrom(_)) => 3,
    None => 0,
  };
}
`
	const check = `#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>

static int frames, globals, next = 5000, nnames, nstrings;
static char log_[16384];
static const char *names[256];
static int arities[256];
static struct { uintptr_t id; char text[96]; } strings[256];
static struct { uintptr_t id; uintptr_t items[4]; } arrays[16];
static int narrays;
static void require(int ok, const char *what) { if (!ok) { fprintf(stderr, "browse: %s (frames=%d globals=%d)\nlog: %s\n", what, frames, globals, log_); exit(1); } }
static uintptr_t fresh(void) { return next++; }
static const char *string_of(uintptr_t id) {
  for (int k = 0; k < nstrings; k++) if (strings[k].id == id) return strings[k].text;
  return 0;
}
static uintptr_t text(const char *s) {
  uintptr_t id = fresh();
  strings[nstrings].id = id;
  strcpy(strings[nstrings++].text, s);
  return id;
}
static void note(const char *name, const uint64_t *args, int n) {
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
static uintptr_t member(const char *name, const char *sig) {
  for (int k = 0; k < nnames; k++) if (!strcmp(names[k], name) && arities[k] == arity(sig)) return k + 1;
  arities[nnames] = arity(sig);
  names[nnames++] = strdup(name);
  return nnames;
}
static uintptr_t method(uintptr_t e, uintptr_t cls, const char *name, const char *sig) { return member(name, sig); }
static const char *name_of(uintptr_t id) { return names[id - 1]; }
static uintptr_t *array_of(uintptr_t id) {
  for (int k = 0; k < narrays; k++) if (arrays[k].id == id) return arrays[k].items;
  return 0;
}
static uintptr_t find_class(uintptr_t e, const char *name) { return fresh(); }
static uintptr_t get_class(uintptr_t e, uintptr_t obj) { return fresh(); }
static uintptr_t new_object(uintptr_t e, uintptr_t cls, uintptr_t ctor, const uint64_t *args) {
  note("new", args, arities[ctor - 1]);
  return fresh();
}
static uintptr_t object(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  note(name_of(id), args, arities[id - 1]);
  return fresh();
}
static uintptr_t static_object(uintptr_t e, uintptr_t cls, uintptr_t id, const uint64_t *args) {
  note(name_of(id), args, arities[id - 1]);
  if (!strcmp(name_of(id), "getProperty")) return text("");
  return fresh();
}
static uintptr_t bcall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) { note(name_of(id), args, arities[id - 1]); return 0x301; }
static uintptr_t vcall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) { note(name_of(id), args, arities[id - 1]); return 0; }
static uintptr_t new_array(uintptr_t e, int32_t n, uintptr_t cls, uintptr_t init) {
  require(narrays < 16 && n <= 4, "a short array");
  arrays[narrays].id = fresh();
  for (int k = 0; k < 4; k++) arrays[narrays].items[k] = init;
  return arrays[narrays++].id;
}
static uintptr_t get_element(uintptr_t e, uintptr_t array, int32_t index) { uintptr_t *a = array_of(array); require(a != 0, "an array"); return a[index]; }
static void set_element(uintptr_t e, uintptr_t array, int32_t index, uintptr_t value) { uintptr_t *a = array_of(array); require(a != 0, "an array"); a[index] = value; }
static uintptr_t new_global(uintptr_t e, uintptr_t ref) { globals++; return ref; }
static void delete_global(uintptr_t e, uintptr_t ref) { require(globals > 0, "a global reference deleted twice"); globals--; }
static uintptr_t new_string(uintptr_t e, const uint16_t *units, uintptr_t n) {
  char s[96];
  require(n < 96, "a short string");
  for (uintptr_t i = 0; i < n; i++) s[i] = (char)units[i];
  s[n] = 0;
  return text(s);
}
static int32_t utf_length(uintptr_t e, uintptr_t s) { return (int32_t)strlen(string_of(s)); }
static const char *utf_chars(uintptr_t e, uintptr_t s, uintptr_t copy) { return string_of(s); }
static void utf_release(uintptr_t e, uintptr_t s, const char *chars) {}
static int32_t push(uintptr_t e, int32_t n) { frames++; return 0; }
static uintptr_t pop(uintptr_t e, uintptr_t keep) { frames--; return 0; }
static uintptr_t none(uintptr_t e) { return 0; }
static void release(uintptr_t e, uintptr_t ref) {}
static int has(const char *part) { return strstr(log_, part) != 0; }
int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 2; }
  void (*ask)(uintptr_t, uintptr_t, uintptr_t) = dlsym(lib, "ask");
  int32_t (*reply)(uintptr_t, uintptr_t) = dlsym(lib, "reply");
  int32_t (*parent_size)(uintptr_t, uintptr_t) = dlsym(lib, "parent_size");
  int32_t (*id_size)(uintptr_t, uintptr_t) = dlsym(lib, "id_size");
  int32_t (*join)(uintptr_t, uintptr_t, uintptr_t) = dlsym(lib, "join");
  int32_t (*kind)(int32_t) = dlsym(lib, "kind");
  require(ask && reply && parent_size && id_size && join && kind, "the entries");
  static uintptr_t table[240];
  static uintptr_t env;
  uintptr_t ctx[40] = {0};
  table[6] = (uintptr_t)find_class;
  table[15] = (uintptr_t)none;
  table[17] = (uintptr_t)none;
  table[19] = (uintptr_t)push;
  table[20] = (uintptr_t)pop;
  table[21] = (uintptr_t)new_global;
  table[22] = (uintptr_t)delete_global;
  table[23] = (uintptr_t)release;
  table[30] = (uintptr_t)new_object;
  table[31] = (uintptr_t)get_class;
  table[33] = (uintptr_t)method;
  table[36] = (uintptr_t)object;
  table[39] = (uintptr_t)bcall;
  table[63] = (uintptr_t)vcall;
  table[113] = (uintptr_t)method;
  table[116] = (uintptr_t)static_object;
  table[163] = (uintptr_t)new_string;
  table[168] = (uintptr_t)utf_length;
  table[169] = (uintptr_t)utf_chars;
  table[170] = (uintptr_t)utf_release;
  table[172] = (uintptr_t)new_array;
  table[173] = (uintptr_t)get_element;
  table[174] = (uintptr_t)set_element;
  env = (uintptr_t)table;
  ctx[4] = (uintptr_t)&env;
  uintptr_t c = (uintptr_t)ctx;

  require(kind(39) == 1 && kind(40) == 2 && kind(41) == 3 && kind(42) == 0, "the kinds of the events");

  ask((uintptr_t)&env, text("root"), 900);
  require(has("detach();") && has("sendResult(") && !has("setMediaId(") && frames == 0 && globals == 0, "a request with no loop gets an empty answer at once");

  log_[0] = 0;
  uintptr_t pair = new_array(0, 2, 0, 0);
  array_of(pair)[0] = text("artists");
  array_of(pair)[1] = 901;
  globals++;
  require(parent_size(c, pair) == 7 && frames == 0, "parent reads the id of the parent");
  require(reply(c, pair) == 1 && frames == 0 && globals == 0, "answer sends the items and lets the request go");
  require(has("setMediaId('artist:1');setTitle('Band');setSubtitle('3 albums');build();new(") && has(",1);") && has("setMediaId('track:9');") && has(",2);") && has("sendResult("), "a browsable artist and a playable track");

  globals++;
  require(id_size(c, text("track:9")) == 7 && globals == 0, "media_id reads the id and lets it go");
  log_[0] = 0;
  globals++;
  require(join(c, 78, 79) == 1 && has("setSessionToken(79);") && globals == 0 && frames == 0, "connect gives the service the token");
  return 0;
}
`
	androidJNIMockCheck(t, src, "ask,reply,parent_size,id_size,join,kind", check)
}
