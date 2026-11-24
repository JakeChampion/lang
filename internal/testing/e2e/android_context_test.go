package e2e

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/tools/literate"
)

// The plain calls of std/android on the Context, the Activity and the
// classes of the platform go through the typed bindings. Each one leaves
// no local reference, no local frame and no global reference.
func TestAndroidContextCalls(t *testing.T) {
	raw, err := os.ReadFile(e2eharness.RepoPath("internal", "stdlib", "std", "android.fern.md"))
	if err != nil {
		t.Fatal(err)
	}
	src, _, err := literate.Parse(string(raw)).Tangle()
	if err != nil {
		t.Fatal(err)
	}
	src += `
pub function typed(ctx: usize): i32 { return key_char(ctx, 3, 29, 1); }
pub function extra(ctx: usize): i32 { return intent_extra(ctx, "name").len(); }
pub function action(ctx: usize, intent: usize): i32 { return intent_action(env(ctx), intent).len(); }
pub function dirs(ctx: usize): i32 { return files_dir(ctx).len() * 10000 + no_backup_dir(ctx).len() * 100 + cache_dir(ctx).len(); }
pub function allowed(ctx: usize): i32 {
  let camera = if (has_permission(ctx, "android.permission.CAMERA")) { 1 } else { 0 };
  let sound = if (has_permission(ctx, "android.permission.RECORD_AUDIO")) { 1 } else { 0 };
  return camera * 10 + sound;
}
pub function ask(ctx: usize): void { request_permissions(ctx, ["android.permission.CAMERA", "android.permission.RECORD_AUDIO"]); }
pub function zone(ctx: usize): i32 { return utc_offset_ms(ctx, 1700000000000i64); }
pub function tongues(ctx: usize): i32 {
  let all = languages(ctx);
  if (all.len() != 2 || all[0] != "en-GB" || all[1] != "fr") { return 0; }
  return 2;
}
pub function away(ctx: usize): void { to_background(ctx); }
`
	const check = androidBindingsMock + `
enum { MAP = 400, ZONE, INTENT, FILES, NO_BACKUP, CACHE, RESOURCES, CONFIGURATION, LOCALES };
static int asked, moved, throw_extra;
static uintptr_t statics(const char *cls, const char *name, const uintptr_t *args) {
  if (!strcmp(name, "load")) { assert(!strcmp(cls, "android/view/KeyCharacterMap") && (int)args[0] == 3); return MAP; }
  assert(!strcmp(name, "getDefault") && !strcmp(cls, "java/util/TimeZone"));
  return ZONE;
}
static int32_t ints(uintptr_t obj, const char *name, const uintptr_t *args) {
  if (!strcmp(name, "get")) { assert(obj == MAP && (int)args[0] == 29 && (int)args[1] == 1); return 65; }
  if (!strcmp(name, "getOffset")) { assert(obj == ZONE && (int64_t)args[0] == 1700000000000LL); return 3600000; }
  assert(!strcmp(name, "checkSelfPermission") && obj == ACTIVITY);
  return !strcmp(text(args[0]), "android.permission.CAMERA") ? 0 : -1;
}
static uintptr_t objects(uintptr_t obj, const char *name, const uintptr_t *args) {
  if (!strcmp(name, "getIntent")) { assert(obj == ACTIVITY); return INTENT; }
  if (!strcmp(name, "getStringExtra")) {
    assert(obj == INTENT && !strcmp(text(args[0]), "name"));
    if (throw_extra) { pending = 1; return 0; }
    return java_text("value");
  }
  if (!strcmp(name, "getAction")) { assert(obj == INTENT); return java_text("go"); }
  if (!strcmp(name, "getFilesDir")) { assert(obj == ACTIVITY); return FILES; }
  if (!strcmp(name, "getNoBackupFilesDir")) { assert(obj == ACTIVITY); return NO_BACKUP; }
  if (!strcmp(name, "getCacheDir")) { assert(obj == ACTIVITY); return CACHE; }
  if (!strcmp(name, "getAbsolutePath")) return java_text(obj == FILES ? "/data/app" : obj == NO_BACKUP ? "/data/nb" : "/c");
  if (!strcmp(name, "getResources")) { assert(obj == ACTIVITY); return RESOURCES; }
  if (!strcmp(name, "getConfiguration")) { assert(obj == RESOURCES); return CONFIGURATION; }
  if (!strcmp(name, "getLocales")) { assert(obj == CONFIGURATION); return LOCALES; }
  assert(!strcmp(name, "toLanguageTags") && obj == LOCALES);
  return java_text("en-GB,fr");
}
static void voids(uintptr_t obj, const char *name, const uintptr_t *args) {
  assert(!strcmp(name, "requestPermissions") && obj == ACTIVITY && args[0] == ARRAY && (int)args[1] == 1 && array_count == 2);
  assert(!strcmp(array_texts[0], "android.permission.CAMERA") && !strcmp(array_texts[1], "android.permission.RECORD_AUDIO"));
  asked++;
}
static int booleans(uintptr_t obj, const char *name, const uintptr_t *args) {
  assert(!strcmp(name, "moveTaskToBack") && obj == ACTIVITY && (args[0] & 255) == 1);
  moved++;
  return 1;
}
int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  assert(lib);
  int (*typed)(uintptr_t) = dlsym(lib, "typed"), (*extra)(uintptr_t) = dlsym(lib, "extra"), (*dirs)(uintptr_t) = dlsym(lib, "dirs");
  int (*allowed)(uintptr_t) = dlsym(lib, "allowed"), (*zone)(uintptr_t) = dlsym(lib, "zone"), (*tongues)(uintptr_t) = dlsym(lib, "tongues");
  int (*action)(uintptr_t, uintptr_t) = dlsym(lib, "action");
  void (*ask)(uintptr_t) = dlsym(lib, "ask"), (*away)(uintptr_t) = dlsym(lib, "away");
  assert(typed && extra && dirs && allowed && zone && tongues && action && ask && away);
  uintptr_t ctx = mock_ctx();
  on_static_object = statics; on_int = ints; on_object = objects; on_void = voids; on_boolean = booleans;
  assert(typed(ctx) == 65); clean();
  assert(extra(ctx) == 5); clean();
  // getStringExtra throws: no text, and no exception stays.
  throw_extra = 1;
  assert(extra(ctx) == 0); clean();
  assert(action(ctx, INTENT) == 2); clean();
  assert(dirs(ctx) == 90802); clean();
  assert(allowed(ctx) == 10); clean();
  ask(ctx); clean();
  assert(asked == 1);
  assert(zone(ctx) == 3600000); clean();
  assert(tongues(ctx) == 2); clean();
  away(ctx); clean();
  assert(moved == 1);
  return 0;
}
`
	androidJNIMockCheck(t, src, "typed,extra,action,dirs,allowed,ask,zone,tongues,away", check)
}
