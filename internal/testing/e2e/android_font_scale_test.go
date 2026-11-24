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
	const check = androidBindingsMock + `
enum { RESOURCES = 200, CONFIGURATION = 300 };
static float value = 1.3f;
static uintptr_t get(uintptr_t obj, const char *name, const uintptr_t *args) {
  if (!strcmp(name, "getResources")) { assert(obj == ACTIVITY); return RESOURCES; }
  assert(!strcmp(name, "getConfiguration") && obj == RESOURCES);
  return CONFIGURATION;
}
static float scale_of(uintptr_t obj, const char *name) {
  assert(obj == CONFIGURATION && !strcmp(name, "fontScale"));
  return value;
}
int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  assert(lib);
  int32_t (*scale)(uintptr_t) = dlsym(lib, "scale");
  assert(scale);
  uintptr_t ctx = mock_ctx();
  on_object = get;
  on_float_field = scale_of;
  assert(scale(ctx) == 130); clean();
  value = 0.85f;
  assert(scale(ctx) == 85); clean();
  value = 0.0f;
  assert(scale(ctx) == 100); clean();
  return 0;
}
`
	androidJNIMockCheck(t, src, "scale", check)
}
