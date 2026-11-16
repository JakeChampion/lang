/*
 * Write the character classes and the case mappings of the libc of the host
 * as a Fern module.
 *
 * The sed port reads UTF-8 as GNU sed does. Thus it needs the classes and
 * the case mappings of the libc that GNU sed runs on. GNU sed reads them
 * with wctype, iswctype, towupper and towlower. This program asks the same
 * functions for each code point. It takes the locale from the
 * environment, as GNU sed does with setlocale (LC_ALL, ""). Run it with
 * LC_ALL=C.UTF-8, the locale of the UTF-8 cases of TestSedParity.
 *
 * On glibc it writes coreutils/lib/glibc_ctype.fern. On Darwin it writes
 * coreutils/lib/darwin_ctype.fern. The output depends only on the libc.
 *
 * Usage:
 *   cc -O2 -o gen-libc-ctype scripts/gen-libc-ctype.c
 *   LC_ALL=C.UTF-8 ./gen-libc-ctype > coreutils/lib/glibc_ctype.fern
 */
#include <langinfo.h>
#include <locale.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <wctype.h>

#if defined(__GLIBC__)
#include <gnu/libc-version.h>
#elif defined(__APPLE__)
#include <sys/types.h>
#include <sys/sysctl.h>
#else
#error "gen-libc-ctype: the host libc is not glibc or Darwin"
#endif

#define TOP 0x110000

static const char *classes[] = {"alpha", "digit", "alnum", "upper", "lower", "space", "blank", "punct", "print", "graph", "cntrl", "xdigit"};

static void die(const char *msg, const char *arg) {
  fprintf(stderr, "gen-libc-ctype: %s%s\n", msg, arg);
  exit(1);
}

/* A growable list of int. */
struct ints {
  int *v;
  size_t n, cap;
};

static void push(struct ints *a, int x) {
  if (a->n == a->cap) {
    a->cap = a->cap ? 2 * a->cap : 1024;
    a->v = realloc(a->v, a->cap * sizeof(int));
    if (!a->v) {
      die("out of memory", "");
    }
  }
  a->v[a->n++] = x;
}

/* The values, eight to a line, each line after the indent and closed by a
 * comma. The layout is the layout of fern -fmt. */
static void lines(const struct ints *a, const char *indent) {
  for (size_t i = 0; i < a->n; i += 8) {
    fputs(indent, stdout);
    for (size_t j = i; j < a->n && j < i + 8; j++) {
      printf(j == i ? "%d" : ", %d", a->v[j]);
    }
    fputs(",\n", stdout);
  }
}

static int map(int upper, int cp) {
  return (int)(upper ? towupper((wint_t)cp) : towlower((wint_t)cp));
}

/* The runs of code points that a mapping moves by one delta, with a step of
 * 1 or 2 between them, as the quadruples lo, hi, step, delta. */
static struct ints runs(int upper) {
  struct ints out = {0};
  for (int cp = 0; cp < TOP; cp++) {
    int d = map(upper, cp) - cp;
    if (d == 0) {
      continue;
    }
    if (out.n > 0) {
      int *r = out.v + out.n - 4;
      int hi = r[1], step = r[2];
      if (r[3] == d && (step ? hi + step == cp : cp - hi == 1 || cp - hi == 2)) {
        r[1] = cp;
        r[2] = step ? step : cp - hi;
        continue;
      }
    }
    push(&out, cp);
    push(&out, cp);
    push(&out, 0);
    push(&out, d);
  }
  for (size_t k = 2; k < out.n; k += 4) {
    if (out.v[k] == 0) {
      out.v[k] = 1;
    }
  }
  return out;
}

static int by_value(const void *a, const void *b) {
  int x = *(const int *)a, y = *(const int *)b;
  return (x > y) - (x < y);
}

/* The name of the libc in the header, and the owner of the case functions
 * in their comments. */
static void libc_names(char *header, size_t n, const char **owner) {
#if defined(__GLIBC__)
  snprintf(header, n, "glibc %s", gnu_get_libc_version());
  *owner = "glibc";
#else
  char ver[64];
  size_t len = sizeof ver;
  if (sysctlbyname("kern.osproductversion", ver, &len, NULL, 0) != 0) {
    die("sysctl kern.osproductversion failed", "");
  }
  /* Only the major version: a minor update of the runner keeps the tables. */
  ver[strcspn(ver, ".")] = '\0';
  snprintf(header, n, "the libc of macOS %s", ver);
  *owner = "Darwin";
#endif
}

int main(void) {
  if (!setlocale(LC_ALL, "")) {
    const char *name = getenv("LC_ALL");
    die("the locale of the environment does not load: LC_ALL=", name ? name : "");
  }
  const char *locale = setlocale(LC_CTYPE, NULL);
  if (strcmp(nl_langinfo(CODESET), "UTF-8") != 0) {
    die("the codeset of the locale is not UTF-8: ", nl_langinfo(CODESET));
  }
  /* upper and lower in the module map ASCII without the tables. */
  for (int cp = 0; cp < 128; cp++) {
    int u = cp >= 'a' && cp <= 'z' ? cp - 32 : cp;
    int l = cp >= 'A' && cp <= 'Z' ? cp + 32 : cp;
    if (map(1, cp) != u || map(0, cp) != l) {
      die("the case of ASCII is not the case of the C locale in ", locale);
    }
  }
  char header[128];
  const char *owner;
  libc_names(header, sizeof header, &owner);

  printf("// The character classes and the case mappings of the locale %s of\n", locale);
  printf("// %s. scripts/gen-libc-ctype.c writes this file from the libc of\n", header);
  printf("// the host. Do not edit it by hand.\n");
  printf("\n");
  printf("// The code points of a class, as pairs of the first and the last of each\n");
  printf("// range.\n");
  printf("// fern-lint: allow cyclomatic-complexity\n");
  printf("pub function class_ranges(name: string): i32[] {\n");
  printf("  return match (name) {\n");
  for (size_t c = 0; c < sizeof classes / sizeof classes[0]; c++) {
    wctype_t t = wctype(classes[c]);
    if (t == 0) {
      die("wctype knows no class ", classes[c]);
    }
    struct ints rs = {0};
    int start = -1;
    for (int cp = 0; cp < TOP; cp++) {
      if (iswctype((wint_t)cp, t)) {
        if (start < 0) {
          start = cp;
        }
      } else if (start >= 0) {
        push(&rs, start);
        push(&rs, cp - 1);
        start = -1;
      }
    }
    if (start >= 0) {
      push(&rs, start);
      push(&rs, TOP - 1);
    }
    if (rs.n == 0) {
      printf("    \"%s\" => [],\n", classes[c]);
    } else {
      printf("    \"%s\" => [\n", classes[c]);
      lines(&rs, "      ");
      printf("    ],\n");
    }
    free(rs.v);
  }
  printf("    _ => [],\n");
  printf("  };\n");
  printf("}\n");
  printf("\n");
  for (int upper = 1; upper >= 0; upper--) {
    struct ints r = runs(upper);
    if (upper) {
      printf("// The runs of towupper: the first and the last code point, the step\n");
      printf("// between them, and the difference to the result.\n");
    } else {
      printf("// The runs of towlower, in the form of upper_runs.\n");
    }
    printf("function %s_runs(): i32[] {\n", upper ? "upper" : "lower");
    printf("  return [\n");
    lines(&r, "    ");
    printf("  ];\n");
    printf("}\n");
    printf("\n");
    free(r.v);
  }
  struct ints pts = {0};
  for (int cp = 0; cp < TOP; cp++) {
    int u = map(1, cp), l = map(0, cp);
    if (u != cp || l != cp) {
      push(&pts, cp);
      push(&pts, u);
      push(&pts, l);
    }
  }
  qsort(pts.v, pts.n, sizeof(int), by_value);
  struct ints uniq = {0};
  for (size_t i = 0; i < pts.n; i++) {
    if (i == 0 || pts.v[i] != pts.v[i - 1]) {
      push(&uniq, pts.v[i]);
    }
  }
  printf("// Each code point that towupper or towlower changes or gives, in order.\n");
  printf("pub function case_points(): i32[] {\n");
  printf("  return [\n");
  lines(&uniq, "    ");
  printf("  ];\n");
  printf("}\n");
  printf("\n");
  printf("// The result of a mapping from its runs, by a binary search.\n"
         "function mapped(runs: i32[], cp: i32): i32 {\n"
         "  let lo = 0;\n"
         "  let hi = runs.len() / 4;\n"
         "  while (lo < hi) {\n"
         "    let mid = (lo + hi) / 2;\n"
         "    if (runs[4 * mid + 1] < cp) {\n"
         "      lo = mid + 1;\n"
         "    } else {\n"
         "      hi = mid;\n"
         "    }\n"
         "  }\n"
         "  if (lo < runs.len() / 4) {\n"
         "    let k = 4 * lo;\n"
         "    if (runs[k] <= cp && (cp - runs[k]) %% runs[k + 2] == 0) {\n"
         "      return cp + runs[k + 3];\n"
         "    }\n"
         "  }\n"
         "  return cp;\n"
         "}\n"
         "\n");
  printf("// %s's towupper in %s.\n", owner, locale);
  printf("pub function upper(cp: i32): i32 {\n"
         "  if (cp < 128) {\n"
         "    return if (cp >= 97 && cp <= 122) { cp - 32 } else { cp };\n"
         "  }\n"
         "  return mapped(upper_runs(), cp);\n"
         "}\n"
         "\n");
  printf("// %s's towlower in %s.\n", owner, locale);
  printf("pub function lower(cp: i32): i32 {\n"
         "  if (cp < 128) {\n"
         "    return if (cp >= 65 && cp <= 90) { cp + 32 } else { cp };\n"
         "  }\n"
         "  return mapped(lower_runs(), cp);\n"
         "}\n");
  if (fflush(stdout) != 0 || ferror(stdout)) {
    die("cannot write the module", "");
  }
  return 0;
}
