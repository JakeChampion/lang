package e2e

import "testing"

// The rows of Android TV. Below API level 26, or with no leanback feature,
// the module makes no row. A channel is a preview channel with its name and
// link, its ID is the last segment of the URI of its row, and Android is
// asked to show it. show_channel deletes the programs of the channel and
// adds each new one with the ID of the channel and a 2:3 poster. A program
// that does not go in makes it false. A Watch Next program goes in its own
// table. Each call ends each local frame that it starts, and deletes each
// global reference to a class that the typed wrappers made.
func TestAndroidTvRows(t *testing.T) {
	const src = `import "std/android/watchnext";
pub function has_rows(ctx: usize): i32 {
  if (watchnext.home_rows(ctx)) {
    return 1;
  }
  return 0;
}
pub function add_one(ctx: usize): i64 {
  return watchnext.add_channel(ctx, "Fern for Plex", "intent:x");
}
pub function show_two(ctx: usize, channel: i64): i32 {
  let a = watchnext.Program { id: "501", title: "Film", kind: watchnext.MOVIE, position_ms: 0i64, duration_ms: 6000i64, poster: "https://p/1.jpg", link: "intent:a" };
  let b = watchnext.Program { id: "600", title: "Show", kind: watchnext.SERIES, position_ms: 0i64, duration_ms: 0i64, poster: "", link: "intent:b" };
  if (watchnext.show_channel(ctx, channel, [a, b])) {
    return 1;
  }
  return 0;
}
pub function keep_one(ctx: usize): i32 {
  if (watchnext.keep(ctx, watchnext.Program { id: "503", title: "Pilot", kind: watchnext.EPISODE, position_ms: 120i64, duration_ms: 900i64, poster: "", link: "intent:c" })) {
    return 1;
  }
  return 0;
}
`
	const check = `#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>

static int frames, globals, next = 1000, nnames, nstrings, tv = 1, inserts = 1;
static const char *reject_key;
static uintptr_t pending;
static int fail_global_after = -1;
static char log_[16384];
static const char *names[512];
static int arities[512];
static struct { uintptr_t id; char text[96]; } strings[512];
static void require(int ok, const char *what) { if (!ok) { fprintf(stderr, "tvrows: %s (frames=%d)\nlog: %s\n", what, frames, log_); exit(1); } }
static uintptr_t fresh(void) { return next++; }
static const char *string_of(uintptr_t id) {
  for (int k = 0; k < nstrings; k++) if (strings[k].id == id) return strings[k].text;
  return 0;
}
static uintptr_t text(const char *s) {
  require(nstrings < 512, "too many strings");
  uintptr_t id = fresh();
  strings[nstrings].id = id;
  strcpy(strings[nstrings++].text, s);
  return id;
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
static uintptr_t method(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  require(cls != 0, "method lookup on a null class");
  require(nnames < 512, "too many method ids");
  arities[nnames] = arity(sig);
  names[nnames++] = strdup(name);
  return nnames;
}
static const char *name_of(uintptr_t id) { return names[id - 1]; }
static uintptr_t find_class(uintptr_t e, const char *name) { char line[160]; snprintf(line, sizeof line, "class %s;", name); strcat(log_, line); return fresh(); }
static uintptr_t get_class(uintptr_t e, uintptr_t obj) { return fresh(); }
static uintptr_t new_object(uintptr_t e, uintptr_t cls, uintptr_t ctor, const uint64_t *args) { note_call("new", args, arities[ctor - 1]); return fresh(); }
static uintptr_t object(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  note_call(name_of(id), args, arities[id - 1]);
  if (!strcmp(name_of(id), "toString")) return text("write failed");
  if (!strcmp(name_of(id), "getLastPathSegment")) return text("77");
  if (!strcmp(name_of(id), "insert") && !inserts) return 0;
  return fresh();
}
static uintptr_t static_object(uintptr_t e, uintptr_t cls, uintptr_t id, const uint64_t *args) { note_call(name_of(id), args, arities[id - 1]); return fresh(); }
static uintptr_t static_void(uintptr_t e, uintptr_t cls, uintptr_t id, const uint64_t *args) { note_call(name_of(id), args, arities[id - 1]); return 0; }
// A jboolean leaves the bits above the low 8 of the register undefined.
static uintptr_t bcall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) { note_call(name_of(id), args, arities[id - 1]); return tv ? 0x301 : 0x200; }
static int32_t icall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) { note_call(name_of(id), args, arities[id - 1]); return 1; }
static uintptr_t vcall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  note_call(name_of(id), args, arities[id - 1]);
  if (reject_key && !strcmp(name_of(id), "put") && !strcmp(string_of(args[0]), reject_key)) pending = fresh();
  return 0;
}
static uintptr_t new_array(uintptr_t e, int32_t n, uintptr_t cls, uintptr_t init) { uint64_t args[1] = {(uint64_t)n}; note_call("array", args, 1); return fresh(); }
static void set_element(uintptr_t e, uintptr_t array, int32_t index, uintptr_t value) { uint64_t args[1] = {value}; note_call("element", args, 1); }
static uintptr_t new_global(uintptr_t e, uintptr_t ref) {
  if (globals == fail_global_after) return 0;
  globals++; return ref;
}
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
static uintptr_t occurred(uintptr_t e) { return pending; }
static void clear(uintptr_t e) { pending = 0; }
static void release(uintptr_t e, uintptr_t ref) {}
static int has(const char *part) { return strstr(log_, part) != 0; }
static int count(const char *part) {
  int n = 0;
  for (const char *p = strstr(log_, part); p; p = strstr(p + 1, part)) n++;
  return n;
}
int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 2; }
  int32_t (*has_rows)(uintptr_t) = dlsym(lib, "has_rows");
  int64_t (*add_one)(uintptr_t) = dlsym(lib, "add_one");
  int32_t (*show_two)(uintptr_t, int64_t) = dlsym(lib, "show_two");
  int32_t (*keep_one)(uintptr_t) = dlsym(lib, "keep_one");
  require(has_rows && add_one && show_two && keep_one, "the entries");
  uintptr_t table[240] = {0}, activity[10] = {0}, ctx[40] = {0};
  table[6] = (uintptr_t)find_class;
  table[15] = (uintptr_t)occurred;
  table[17] = (uintptr_t)clear;
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
  table[51] = (uintptr_t)icall;
  table[63] = (uintptr_t)vcall;
  table[113] = (uintptr_t)method;
  table[116] = (uintptr_t)static_object;
  table[143] = (uintptr_t)static_void;
  table[163] = (uintptr_t)new_string;
  table[168] = (uintptr_t)utf_length;
  table[169] = (uintptr_t)utf_chars;
  table[170] = (uintptr_t)utf_release;
  table[172] = (uintptr_t)new_array;
  table[174] = (uintptr_t)set_element;
  uintptr_t env = (uintptr_t)table;
  activity[3] = 1234;
  ctx[2] = (uintptr_t)activity;
  ctx[4] = (uintptr_t)&env;

  ctx[23] = 25;
  require(has_rows((uintptr_t)ctx) == 0 && add_one((uintptr_t)ctx) == -1 && show_two((uintptr_t)ctx, 77) == 0 && log_[0] == 0, "no rows below API level 26");
  ctx[23] = 26;
  tv = 0;
  require(has_rows((uintptr_t)ctx) == 0 && add_one((uintptr_t)ctx) == -1 && frames == 0 && globals == 0, "no rows with no leanback");
  require(has("getPackageManager();hasSystemFeature('android.software.leanback');") && !has("insert("), "the feature of the rows");
  tv = 1;
  fail_global_after = 1;
  require(add_one((uintptr_t)ctx) == -1 && frames == 0 && globals == 0, "class promotion failure releases earlier classes and frame");
  fail_global_after = -1;
  require(has_rows((uintptr_t)ctx) == 1 && frames == 0, "a jboolean is its low 8 bits");

  log_[0] = 0;
  require(add_one((uintptr_t)ctx) == 77 && frames == 0 && globals == 0, "add_channel gives the ID of the new row");
  require(has("put('type','TYPE_PREVIEW');put('display_name','Fern for Plex');put('app_link_intent_uri','intent:x');") && has("parse('content://android.media.tv/channel');"), "the channel");
  require(has("insert(") && has("getLastPathSegment();") && has("getLastPathSegment();requestChannelBrowsable(1234,77);"), "the ID and the request to show the channel");

  log_[0] = 0;
  require(show_two((uintptr_t)ctx, 77) == 1 && frames == 0 && globals == 0, "show_channel adds the programs");
  require(has("parse('content://android.media.tv/preview_program');class java/lang/String;array(1);element('77');delete(") && has("'channel_id=?'"), "the delete of the programs before");
  require(count("valueOf(77);") == 2 && count("parse('content://android.media.tv/preview_program');") == 3 && count("insert(") == 2, "two programs in the channel");
  require(has("put('title','Film');put('internal_provider_id','501');put('intent_uri','intent:a');put('poster_art_uri','https://p/1.jpg');valueOf(4);put('poster_art_aspect_ratio',") && has("valueOf(6000);put('duration_millis',"), "a program with its poster and its length");
  require(has("valueOf(1);") && has("put('title','Show');put('internal_provider_id','600');put('intent_uri','intent:b');") && count("poster_art_uri") == 1 && count("duration_millis") == 1, "a program of a series with no poster and no length");
  inserts = 0;
  require(show_two((uintptr_t)ctx, 77) == 0 && frames == 0 && globals == 0, "a removed channel makes show_channel false");
  inserts = 1;

  log_[0] = 0;
  require(keep_one((uintptr_t)ctx) == 1 && frames == 0 && globals == 0, "keep adds a Watch Next program");
  require(has("parse('content://android.media.tv/watch_next_program');") && has("'internal_provider_id=?'") && has("put('title','Pilot');put('internal_provider_id','503');put('intent_uri','intent:c');"), "the Watch Next program");
  require(has("valueOf(120);put('last_playback_position_millis',") && has("valueOf(900);put('duration_millis',") && has("put('watch_next_type',") && !has("preview_program"), "the place of the Watch Next program");
  log_[0] = 0;
  reject_key = "display_name";
  require(add_one((uintptr_t)ctx) == -1 && !has("insert(") && !has("requestChannelBrowsable("), "a rejected channel name aborts publication");
  log_[0] = 0;
  reject_key = "duration_millis";
  require(show_two((uintptr_t)ctx, 77) == 0 && !has("insert("), "a rejected preview duration aborts the row");
  log_[0] = 0;
  require(keep_one((uintptr_t)ctx) == 0 && !has("delete(") && !has("insert("), "a rejected replacement keeps the existing Watch Next row");
  require(!pending && frames == 0 && globals == 0, "failed writes release JNI resources");
  return 0;
}
`
	androidJNIMockCheck(t, src, "has_rows,add_one,show_two,keep_one", check)
}
