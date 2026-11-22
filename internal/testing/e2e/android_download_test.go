package e2e

import "testing"

// A download goes to the DownloadManager with its headers, its title, its
// notification and its file name in the external files directory, and the
// ID of enqueue comes back. The state of a download reads the columns of the
// row of its ID, and an ID with no row is GONE. A jboolean is its low 8
// bits. Each call closes its cursor and releases its global class references
// before popping the local frame, including when the service is unavailable.
func TestAndroidDownload(t *testing.T) {
	const src = `import "std/android/download";
pub function start_one(ctx: usize): i64 {
  return download.start(ctx, "https://den.example/library/parts/9/file.mkv?download=1", [("X-Plex-Token", "tok")], "9.mkv", "Film");
}
pub function start_wifi(ctx: usize): i64 {
  return download.start_with_network(ctx, "https://den.example/file", [], "file", "Film", true);
}
pub function status_of(ctx: usize, id: i64): i32 { return download.progress(ctx, id).status; }
pub function done_of(ctx: usize, id: i64): i64 { return download.progress(ctx, id).done; }
pub function total_of(ctx: usize, id: i64): i64 { return download.progress(ctx, id).total; }
pub function remove_one(ctx: usize, id: i64): void { download.remove(ctx, id); }
pub function dir_is(ctx: usize): i32 {
  if (download.dir(ctx) == "/sdcard/files") {
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

static int frames, next = 1000, nnames, nstrings, rows = 1, closes, service = 1;
static int64_t region;
static char log_[8192];
static const char *names[256];
static int arities[256];
static struct { uintptr_t id; char text[96]; } strings[256];
static void require(int ok, const char *what) { if (!ok) { fprintf(stderr, "download: %s (frames=%d)\nlog: %s\n", what, frames, log_); exit(1); } }
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
  require(nnames < 256, "too many method ids");
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
  if (!strcmp(name_of(id), "getSystemService") && !service) return 0;
  if (!strcmp(name_of(id), "getAbsolutePath")) return text("/sdcard/files");
  if (!strcmp(name_of(id), "query") && !rows) return 0;
  return fresh();
}
static uintptr_t static_object(uintptr_t e, uintptr_t cls, uintptr_t id, const uint64_t *args) { note_call(name_of(id), args, arities[id - 1]); return fresh(); }
// A jboolean leaves the bits above the low 8 of the register undefined.
static uintptr_t bcall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) { note_call(name_of(id), args, arities[id - 1]); return rows == 2 ? 0x100 : 0x501; }
static int32_t icall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  note_call(name_of(id), args, arities[id - 1]);
  if (!strcmp(name_of(id), "getColumnIndex")) {
    const char *s = string_of(args[0]);
    return !strcmp(s, "status") ? 1 : !strcmp(s, "bytes_so_far") ? 2 : !strcmp(s, "total_size") ? 3 : -1;
  }
  if (!strcmp(name_of(id), "getInt")) return args[0] == 1 ? 2 : 0;
  return 1;
}
static int64_t lcall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  note_call(name_of(id), args, arities[id - 1]);
  if (!strcmp(name_of(id), "enqueue")) return 42;
  if (!strcmp(name_of(id), "getLong")) return args[0] == 2 ? 1000 : args[0] == 3 ? 4000 : 0;
  return 0;
}
static uintptr_t vcall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  note_call(name_of(id), args, arities[id - 1]);
  if (!strcmp(name_of(id), "close")) closes++;
  return 0;
}
static uintptr_t new_string(uintptr_t e, const uint16_t *units, uintptr_t n) {
  char s[96];
  require(n < 96 && nstrings < 256, "a short string");
  for (uintptr_t i = 0; i < n; i++) s[i] = (char)units[i];
  s[n] = 0;
  return text(s);
}
static int32_t utf_length(uintptr_t e, uintptr_t s) { return (int32_t)strlen(string_of(s)); }
static const char *utf_chars(uintptr_t e, uintptr_t s, uintptr_t copy) { return string_of(s); }
static void utf_release(uintptr_t e, uintptr_t s, const char *chars) {}
static uintptr_t new_long_array(uintptr_t e, uintptr_t n) { require(n == 1, "one id"); return fresh(); }
static void long_region(uintptr_t e, uintptr_t array, uintptr_t start, uintptr_t n, const int64_t *buf) { require(start == 0 && n == 1, "the region of one id"); region = buf[0]; }
static int32_t push(uintptr_t e, int32_t n) { frames++; return 0; }
static int globals;
static uintptr_t global_ref(uintptr_t e, uintptr_t ref) { globals++; return ref; }
static void drop_global(uintptr_t e, uintptr_t ref) { require(globals > 0, "live global"); globals--; }
static uintptr_t pop(uintptr_t e, uintptr_t keep) { require(globals == 0, "class references released before pop"); frames--; return 0; }
static uintptr_t none(uintptr_t e) { return 0; }
static void release(uintptr_t e, uintptr_t ref) {}
static int has(const char *part) { return strstr(log_, part) != 0; }
int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 2; }
  int64_t (*start_one)(uintptr_t) = dlsym(lib, "start_one");
  int64_t (*start_wifi)(uintptr_t) = dlsym(lib, "start_wifi");
  int32_t (*status_of)(uintptr_t, int64_t) = dlsym(lib, "status_of");
  int64_t (*done_of)(uintptr_t, int64_t) = dlsym(lib, "done_of");
  int64_t (*total_of)(uintptr_t, int64_t) = dlsym(lib, "total_of");
  void (*remove_one)(uintptr_t, int64_t) = dlsym(lib, "remove_one");
  int32_t (*dir_is)(uintptr_t) = dlsym(lib, "dir_is");
  require(start_one && start_wifi && status_of && done_of && total_of && remove_one && dir_is, "the entries");
  uintptr_t table[240] = {0}, activity[10] = {0}, ctx[40] = {0};
  table[6] = (uintptr_t)find_class;
  table[15] = (uintptr_t)none;
  table[17] = (uintptr_t)none;
  table[19] = (uintptr_t)push;
  table[20] = (uintptr_t)pop;
  table[21] = (uintptr_t)global_ref;
  table[22] = (uintptr_t)drop_global;
  table[23] = (uintptr_t)release;
  table[30] = (uintptr_t)new_object;
  table[31] = (uintptr_t)get_class;
  table[33] = (uintptr_t)method;
  table[36] = (uintptr_t)object;
  table[39] = (uintptr_t)bcall;
  table[51] = (uintptr_t)icall;
  table[54] = (uintptr_t)lcall;
  table[63] = (uintptr_t)vcall;
  table[113] = (uintptr_t)method;
  table[116] = (uintptr_t)static_object;
  table[163] = (uintptr_t)new_string;
  table[168] = (uintptr_t)utf_length;
  table[169] = (uintptr_t)utf_chars;
  table[170] = (uintptr_t)utf_release;
  table[180] = (uintptr_t)new_long_array;
  table[212] = (uintptr_t)long_region;
  uintptr_t env = (uintptr_t)table;
  activity[3] = 1234;
  ctx[2] = (uintptr_t)activity;
  ctx[4] = (uintptr_t)&env;

  require(start_one((uintptr_t)ctx) == 42 && frames == 0, "start gives the id of enqueue");
  require(has("getSystemService('download');") && has("class android/app/DownloadManager$Request;") && has("parse('https://den.example/library/parts/9/file.mkv?download=1');"), "the manager and the address");
  require(has("addRequestHeader('X-Plex-Token','tok');") && has("setTitle('Film');") && has("setNotificationVisibility(1);") && has("setDestinationInExternalFilesDir(1234,0,'9.mkv');") && has("enqueue("), "the request");

  require(!has("setAllowedNetworkTypes("), "default download keeps all networks");
  log_[0] = 0;
  require(start_wifi((uintptr_t)ctx) == 42 && frames == 0 && has("setAllowedNetworkTypes(2);"), "restricted download uses Wi-Fi");

  log_[0] = 0;
  require(status_of((uintptr_t)ctx, 42) == 2 && region == 42 && frames == 0 && closes == 1, "the status of a running download");
  require(has("class android/app/DownloadManager$Query;") && has("setFilterById(") && has("moveToFirst();") && has("getColumnIndex('status');getInt(1);"), "the query of one id");
  require(done_of((uintptr_t)ctx, 42) == 1000 && total_of((uintptr_t)ctx, 42) == 4000 && closes == 3, "the bytes that came and the size");

  rows = 0;
  require(status_of((uintptr_t)ctx, 7) == 0 && total_of((uintptr_t)ctx, 7) == -1 && frames == 0, "an id with no row is gone");
  rows = 2;
  require(status_of((uintptr_t)ctx, 7) == 0 && frames == 0, "a false moveToFirst with high bits is false");
  rows = 1;

  log_[0] = 0;
  remove_one((uintptr_t)ctx, 42);
  require(has("remove(") && region == 42 && frames == 0, "remove names the id");
  require(dir_is((uintptr_t)ctx) == 1 && has("getExternalFilesDir(0);") && frames == 0, "the external files directory");
  service = 0;
  require(start_one((uintptr_t)ctx) == -1 && frames == 0, "missing service refuses enqueue");
  return 0;
}
`
	androidJNIMockCheck(t, src, "start_one,start_wifi,status_of,done_of,total_of,remove_one,dir_is", check)
}
