package e2e

import "testing"

func TestAndroidLauncherShortcuts(t *testing.T) {
	const src = `import "std/android/shortcuts";
pub function publish(ctx: usize): i32 {
  let items = [shortcuts.Shortcut { id: "a", label: "A", action: "play" }, shortcuts.Shortcut { id: "b", label: "B", action: "open" }];
  return if (shortcuts.set(ctx, items)) { 1 } else { 0 };
}
pub function pin(ctx: usize): i32 {
  let item = shortcuts.Shortcut { id: "p", label: "Pin", action: "play" };
  return if (shortcuts.pin(ctx, item)) { 1 } else { 0 };
}
pub function consume(ctx: usize): i32 {
  let name = shortcuts.action(ctx);
  if (name == "play") { return 1; }
  return if (name == "") { 0 } else { -1 };
}
pub function incoming(e: usize): void { shortcuts.keep_intent(e, 100 as usize, 200 as usize); }
`
	const check = `
#include <assert.h>
#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

static int frames, globals, added, published, accept_list = 1, accept_item = 1;
static int pin_available = 1, accept_pin = 1, pinned, pending;
static uintptr_t intent, action;
static int push(uintptr_t e, int n) { frames++; assert(frames <= 2); return 0; }
static uintptr_t pop(uintptr_t e, uintptr_t keep) { assert(frames > 0); frames--; return 0; }
static uintptr_t exception(uintptr_t e) { return pending; }
static void clear(uintptr_t e) { pending = 0; }
static uintptr_t find(uintptr_t e, const char *s) { assert(frames); return 1; }
static uintptr_t global(uintptr_t e, uintptr_t ref) { globals++; return ref; }
static void release(uintptr_t e, uintptr_t ref) { assert(globals); globals--; }
static void local(uintptr_t e, uintptr_t ref) {}
static const char *methods[] = {"<init>", "getComponentName", "getIntent", "setIntent", "getSystemService",
  "getAction", "setAction", "setComponent", "build", "setShortLabel", "setDynamicShortcuts", "add"};
static uintptr_t method(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  if (!strcmp(name, "requestPinShortcut")) {
    assert(!strcmp(sig, "(Landroid/content/pm/ShortcutInfo;Landroid/content/IntentSender;)Z"));
    if (!pin_available) { pending = 600; return 0; }
    return (uintptr_t)"requestPinShortcut";
  }
  for (int i=0; i<sizeof(methods)/sizeof(*methods); i++) if (!strcmp(methods[i],name)) return (uintptr_t)methods[i];
  abort();
}
static uintptr_t construct(uintptr_t e, uintptr_t cls, uintptr_t id, const uintptr_t *args) { return 300; }
static uintptr_t object(uintptr_t e, uintptr_t obj, uintptr_t id, const uintptr_t *args) {
  const char *name = (const char *)id;
  if (!strcmp(name, "getIntent")) { assert(obj == 100); return intent; }
  if (!strcmp(name, "getAction")) { assert(obj == 200); return action; }
  if (!strcmp(name, "setAction")) { assert(obj == 200 && args[0] == 0); action=0; return obj; }
  return 300;
}
static uintptr_t set_intent(uintptr_t e, uintptr_t obj, uintptr_t id, const uintptr_t *args) {
  assert(!strcmp((const char *)id, "setIntent") && obj == 100); intent=args[0]; return 0;
}
static uintptr_t boolean(uintptr_t e, uintptr_t obj, uintptr_t id, const uintptr_t *args) {
  if (!strcmp((const char *)id, "add")) { assert(frames == 2); added++; return 0x100 | accept_item; }
  if (!strcmp((const char *)id, "requestPinShortcut")) {
    assert(frames == 1 && obj == 300 && args[0] == 300 && args[1] == 0);
    pinned++; return 0x100 | accept_pin;
  }
  assert(!strcmp((const char *)id,"setDynamicShortcuts") && frames == 1);
  published++; return 0x100 | accept_list;
}
static uintptr_t string(uintptr_t e, const uint16_t *s, int n) { assert(frames); return 400; }
static int length(uintptr_t e, uintptr_t s) { assert(s == 500); return 4; }
static const char *chars(uintptr_t e, uintptr_t s, uintptr_t copy) { return "play"; }
static void done(uintptr_t e, uintptr_t s, const char *p) {}
static void clean(void) { assert(!frames && !globals && !pending); }

int main(int argc, char **argv) {
  void *lib=dlopen(argv[1], RTLD_NOW);
  assert(lib);
  int (*publish)(uintptr_t)=dlsym(lib,"publish"), (*consume)(uintptr_t)=dlsym(lib,"consume");
  void (*incoming)(uintptr_t)=dlsym(lib,"incoming");
  int (*pin)(uintptr_t)=dlsym(lib,"pin");
  assert(publish && consume && incoming && pin);
  uintptr_t table[240] = {[6]=(uintptr_t)find, [15]=(uintptr_t)exception, [17]=(uintptr_t)clear, [19]=(uintptr_t)push,
    [20]=(uintptr_t)pop, [21]=(uintptr_t)global, [22]=(uintptr_t)release, [23]=(uintptr_t)local,
    [30]=(uintptr_t)construct, [33]=(uintptr_t)method, [36]=(uintptr_t)object, [39]=(uintptr_t)boolean,
    [63]=(uintptr_t)set_intent, [163]=(uintptr_t)string, [168]=(uintptr_t)length,
    [169]=(uintptr_t)chars, [170]=(uintptr_t)done};
  uintptr_t activity[10] = {[3]=100}, env=(uintptr_t)table, ctx[40] = {[2]=(uintptr_t)activity, [4]=(uintptr_t)&env};
  assert(pin((uintptr_t)ctx) && pinned == 1); clean();
  accept_pin = 0;
  assert(!pin((uintptr_t)ctx) && pinned == 2); clean();
  pin_available = 0;
  assert(!pin((uintptr_t)ctx) && pinned == 2); clean();
  assert(publish((uintptr_t)ctx) && added==2 && published==1); clean();
  accept_list=0;
  assert(!publish((uintptr_t)ctx) && added==4 && published==2); clean();
  accept_item=0;
  assert(!publish((uintptr_t)ctx) && added==5 && published==2); clean();
  incoming((uintptr_t)&env); clean();
  action=500;
  assert(consume((uintptr_t)ctx)==1 && action==0); clean();
  assert(consume((uintptr_t)ctx)==0); clean();
  return 0;
}
`
	androidJNIMockCheck(t, src, "publish,consume,incoming,pin", check)
}
