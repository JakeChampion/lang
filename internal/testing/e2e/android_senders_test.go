package e2e

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/tools/literate"
)

// The loop keeps up to 8 senders. A callback finds the slot of a sender of
// the live loop by its number, a freed slot takes the next sender, and the
// number 0 is never a sender.
func TestAndroidSenders(t *testing.T) {
	raw, err := os.ReadFile(e2eharness.RepoPath("internal", "stdlib", "std", "android.fern.md"))
	if err != nil {
		t.Fatal(err)
	}
	src, _, err := literate.Parse(string(raw)).Tangle()
	if err != nil {
		t.Fatal(err)
	}
	src += `
pub function sender_ctx(activity: usize): usize { return new_context(activity); }
pub function add(ctx: usize, id: i64): i32 { return add_sender(ctx, id); }
pub function drop(ctx: usize, slot: i32): void { drop_sender(ctx, slot); }
pub function slot_of(e: usize, id: i64): i32 { return sender_slot(e, id); }
`
	const check = `#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>

static char live[32];
static int nstrings;
static struct { uintptr_t id; char text[64]; } strings[64];
static void require(int ok, const char *what) { if (!ok) { fprintf(stderr, "senders: %s\n", what); exit(1); } }
static uintptr_t text(const char *s) {
  uintptr_t id = 5000 + nstrings;
  strings[nstrings].id = id;
  strcpy(strings[nstrings++].text, s);
  if (nstrings == 64) nstrings = 0;
  return id;
}
static const char *string_of(uintptr_t id) {
  for (int k = 0; k < 64; k++) if (strings[k].id == id) return strings[k].text;
  return "";
}
static uintptr_t find_class(uintptr_t e, const char *name) { return 7; }
static uintptr_t method(uintptr_t e, uintptr_t cls, const char *name, const char *sig) { return 9; }
static uintptr_t property(uintptr_t e, uintptr_t cls, uintptr_t id, const uint64_t *args) {
  return text(strcmp(string_of(args[0]), "dev.fern.android.loop") ? "" : live);
}
static uintptr_t new_string(uintptr_t e, const uint16_t *units, uintptr_t n) {
  char s[64];
  for (uintptr_t i = 0; i < n && i < 63; i++) s[i] = (char)units[i];
  s[n < 63 ? n : 63] = 0;
  return text(s);
}
static int32_t utf_length(uintptr_t e, uintptr_t s) { return (int32_t)strlen(string_of(s)); }
static const char *utf_chars(uintptr_t e, uintptr_t s, uintptr_t copy) { return string_of(s); }
static void utf_release(uintptr_t e, uintptr_t s, const char *chars) {}
static uintptr_t none(uintptr_t e) { return 0; }
static void release(uintptr_t e, uintptr_t ref) {}
int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 2; }
  uintptr_t (*sender_ctx)(uintptr_t) = dlsym(lib, "sender_ctx");
  int32_t (*add)(uintptr_t, int64_t) = dlsym(lib, "add");
  void (*drop)(uintptr_t, int32_t) = dlsym(lib, "drop");
  int32_t (*slot_of)(uintptr_t, int64_t) = dlsym(lib, "slot_of");
  require(sender_ctx && add && drop && slot_of, "the entries");
  uintptr_t table[240] = {0}, activity[10] = {0};
  table[6] = (uintptr_t)find_class;
  table[15] = (uintptr_t)none;
  table[17] = (uintptr_t)none;
  table[23] = (uintptr_t)release;
  table[113] = (uintptr_t)method;
  table[116] = (uintptr_t)property;
  table[163] = (uintptr_t)new_string;
  table[168] = (uintptr_t)utf_length;
  table[169] = (uintptr_t)utf_chars;
  table[170] = (uintptr_t)utf_release;
  uintptr_t functions = (uintptr_t)table;
  uintptr_t env = (uintptr_t)&functions;
  activity[2] = env;
  uintptr_t ctx = sender_ctx((uintptr_t)activity);
  require(ctx != 0, "a context");

  require(slot_of(env, 77) == -1, "no loop is live");
  snprintf(live, sizeof live, "%lu", (unsigned long)ctx);
  require(add(ctx, 0) == -1, "0 is not a sender");
  require(add(ctx, 77) == 0 && add(ctx, 88) == 1, "the first free slots");
  require(slot_of(env, 88) == 1 && slot_of(env, 77) == 0 && slot_of(env, 99) == -1 && slot_of(env, 0) == -1, "the slot of a number");
  drop(ctx, 0);
  require(slot_of(env, 77) == -1 && slot_of(env, 88) == 1, "a freed slot");
  require(add(ctx, 99) == 0 && slot_of(env, 99) == 0, "a freed slot takes the next sender");
  for (int64_t id = 100; id < 106; id++) require(add(ctx, id) == (int32_t)(id - 98), "a slot up to 7");
  require(add(ctx, 200) == -1, "8 senders at most");
  drop(ctx, 8);
  drop(ctx, -1);
  require(slot_of(env, 105) == 7, "a slot out of the range changes nothing");
  return 0;
}
`
	androidJNIMockCheck(t, src, "sender_ctx,add,drop,slot_of", check)
}
