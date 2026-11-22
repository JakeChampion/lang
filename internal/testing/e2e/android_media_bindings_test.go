package e2e

import "testing"

func TestAndroidMediaBindings(t *testing.T) {
	const src = `import "std/android/media";
pub function locate(e: usize): usize { return media.audio_uri(e, 100 as usize, "/music/test"); }
pub function write_file(e: usize): boolean { return media.write(e, 100 as usize, "/music/test", [1 as u8, 2 as u8]); }
pub function ask(ctx: usize): boolean { return media.ask_write(ctx, "/music/test"); }
`
	const check = `#include <assert.h>
#include <dlfcn.h>
#include <stdint.h>
#include <string.h>

static int frames, globals, pending, missing_request, found = 1;
static int cursors, streams, writes, dialogs, copy_failure;
static const char *fail;
static uintptr_t push(uintptr_t e, int capacity) { assert(!frames); frames++; return 0; }
static uintptr_t pop(uintptr_t e, uintptr_t keep) {
  assert(frames == 1 && !globals && !pending); frames--; return keep ? keep + 1 : 0;
}
static uintptr_t find(uintptr_t e, const char *name) { assert(frames && !pending); return 1; }
static uintptr_t global(uintptr_t e, uintptr_t ref) { globals++; return ref; }
static void release(uintptr_t e, uintptr_t ref) { assert(globals); globals--; }
static void drop(uintptr_t e, uintptr_t ref) {}
static uintptr_t exception(uintptr_t e) { return pending; }
static void clear(uintptr_t e) { pending = 0; }
static uintptr_t class_of(uintptr_t e, uintptr_t obj) { assert(obj); return 1; }
static const char *methods[] = {"getContentResolver","getContentUri","query","moveToFirst","getLong","close",
  "withAppendedId","singletonList","createWriteRequest","getIntentSender","startIntentSenderForResult",
  "openOutputStream","write","toString"};
static uintptr_t method(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  if (missing_request && !strcmp(name,"createWriteRequest")) { pending = 1; return 0; }
  for (unsigned i=0; i<sizeof(methods)/sizeof(*methods); i++)
    if (!strcmp(name,methods[i])) return (uintptr_t)methods[i];
  assert(0); return 0;
}
static uintptr_t invoke(uintptr_t e, uintptr_t obj, const char *name, const uintptr_t *args) {
  assert(frames && globals && obj && !pending);
  if (fail && !strcmp(name,fail)) { pending = 1; return 0; }
  if (!strcmp(name,"query")) { assert(args[0] && args[1] && args[2] && args[3] && !args[4]); return 201; }
  if (!strcmp(name,"moveToFirst")) return 0x100 | found;
  if (!strcmp(name,"getLong")) { assert(!args[0]); return 0x100000003ULL; }
  if (!strcmp(name,"withAppendedId")) { assert(args[1] == 0x100000003ULL); return 300; }
  if (!strcmp(name,"openOutputStream")) { assert(args[0] == 300); return 202; }
  if (!strcmp(name,"close")) { if (obj == 201) cursors++; else { assert(obj == 202); streams++; } }
  if (!strcmp(name,"write")) { assert(obj == 202 && args[1] == 0 && args[2] == 2); writes++; }
  if (!strcmp(name,"startIntentSenderForResult")) { assert(args[1] == 7301 && !args[2]); dialogs++; }
  if (!strcmp(name,"toString")) return 0;
  return 200;
}
static uintptr_t string(uintptr_t e, const uint16_t *s, int n) { return 200; }
static uintptr_t array(uintptr_t e, int n, uintptr_t cls, uintptr_t first) { assert(n == 1 && first); return 210; }
static uintptr_t bytes(uintptr_t e, int n) { assert(n == 2); return 211; }
static void region(uintptr_t e, uintptr_t array, int start, int n, const uint8_t *data) {
  assert(array == 211 && !start && n == 2 && data[0] == 1 && data[1] == 2);
  pending = copy_failure;
}
int main(int argc, char **argv) {
  void *lib=dlopen(argv[1], RTLD_NOW); assert(lib);
  uintptr_t (*locate)(uintptr_t)=dlsym(lib,"locate");
  int (*write_file)(uintptr_t)=dlsym(lib,"write_file"), (*ask)(uintptr_t)=dlsym(lib,"ask");
  assert(locate && write_file && ask);
  uintptr_t table[240] = {[6]=(uintptr_t)find,[15]=(uintptr_t)exception,[17]=(uintptr_t)clear,
    [19]=(uintptr_t)push,[20]=(uintptr_t)pop,[21]=(uintptr_t)global,[22]=(uintptr_t)release,[23]=(uintptr_t)drop,
    [31]=(uintptr_t)class_of,[33]=(uintptr_t)method,[36]=(uintptr_t)invoke,[39]=(uintptr_t)invoke,[54]=(uintptr_t)invoke,
    [63]=(uintptr_t)invoke,[113]=(uintptr_t)method,[116]=(uintptr_t)invoke,[163]=(uintptr_t)string,
    [172]=(uintptr_t)array,[176]=(uintptr_t)bytes,[208]=(uintptr_t)region};
  uintptr_t env=(uintptr_t)table, e=(uintptr_t)&env, activity[10]={[3]=100}, ctx[40]={[2]=(uintptr_t)activity,[4]=e,[23]=29};
  missing_request=1;
  assert(!ask((uintptr_t)ctx) && locate(e) == 301 && cursors == 1);
  found=0; assert(!locate(e) && cursors == 2);
  found=1; fail="getLong"; assert(!locate(e) && cursors == 3); fail=0;
  assert(write_file(e) && streams == 1 && writes == 1);
  copy_failure=1; assert(!write_file(e) && streams == 2 && writes == 1); copy_failure=0;
  fail="write"; assert(!write_file(e) && streams == 3 && writes == 1); fail=0;
  ctx[23]=30; missing_request=0; assert(ask((uintptr_t)ctx) && dialogs == 1);
  assert(!globals && !frames && !pending);
  return 0;
}
`
	androidJNIMockCheck(t, src, "locate,write_file,ask", check)
}
