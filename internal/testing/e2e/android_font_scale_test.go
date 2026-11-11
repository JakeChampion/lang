package e2e

import "testing"

// android.font_scale reads Configuration.fontScale of the resources of the
// activity, releases each local reference, and gives 1 for a scale that is
// not above 0.
func TestAndroidFontScale(t *testing.T) {
	const src = `import "std/android";
pub function scale(ctx: usize): i32 {
  return (android.font_scale(ctx) * 100.0).round() as i32;
}
`
	const check = `#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>

static int live;
static float value = 1.3f;
static void require(int ok) { if (!ok) { fprintf(stderr, "JNI contract: live=%d\n", live); exit(1); } }
static uintptr_t get_class(uintptr_t env, uintptr_t obj) {
  require(obj == 100 || obj == 200 || obj == 300);
  live++;
  return obj + 1000;
}
static uintptr_t method(uintptr_t env, uintptr_t cls, const char *name, const char *sig) {
  if (!strcmp(name, "getResources")) { require(cls == 1100 && !strcmp(sig, "()Landroid/content/res/Resources;")); return 1; }
  require(cls == 1200 && !strcmp(name, "getConfiguration") && !strcmp(sig, "()Landroid/content/res/Configuration;"));
  return 2;
}
static uintptr_t object(uintptr_t env, uintptr_t obj, uintptr_t id, uintptr_t args) {
  require((obj == 100 && id == 1) || (obj == 200 && id == 2));
  live++;
  return obj + 100;
}
static uintptr_t field(uintptr_t env, uintptr_t cls, const char *name, const char *sig) {
  require(cls == 1300 && !strcmp(name, "fontScale") && !strcmp(sig, "F"));
  return 7;
}
static float get_float(uintptr_t env, uintptr_t obj, uintptr_t id) {
  require(obj == 300 && id == 7);
  return value;
}
static void release(uintptr_t env, uintptr_t obj) {
  require(obj == 200 || obj == 300 || obj >= 1100);
  live--;
}
int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 2; }
  int32_t (*scale)(uintptr_t) = dlsym(lib, "scale");
  require(scale != NULL);
  uintptr_t table[128] = {0}, activity[8] = {0}, ctx[8] = {0};
  table[31] = (uintptr_t)get_class;
  table[33] = (uintptr_t)method;
  table[36] = (uintptr_t)object;
  table[94] = (uintptr_t)field;
  table[102] = (uintptr_t)get_float;
  table[23] = (uintptr_t)release;
  uintptr_t env = (uintptr_t)table;
  activity[3] = 100;
  ctx[2] = (uintptr_t)activity;
  ctx[4] = (uintptr_t)&env;
  require(scale((uintptr_t)ctx) == 130 && live == 0);
  value = 0.85f;
  require(scale((uintptr_t)ctx) == 85 && live == 0);
  value = 0.0f;
  require(scale((uintptr_t)ctx) == 100 && live == 0);
  return 0;
}
`
	androidJNIMockCheck(t, src, "scale", check)
}
