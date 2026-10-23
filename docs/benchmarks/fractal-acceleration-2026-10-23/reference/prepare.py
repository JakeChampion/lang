from pathlib import Path
import importlib.util
import json
import shutil
import subprocess
import urllib.request
out = Path(__file__).resolve().parent
root = out.parents[3]
original = out/'orange'
original.mkdir(exist_ok=True)
revision = '1051f96ee98ad347e153f56cc71dd4b8403e911a'
for name in ['Mandelbrot.h', 'MandelbrotStateEngineSimple.h', 'Mandelbrot_fastest_raw.cpp', 'LICENSE']:
    url = 'https://raw.githubusercontent.com/royward/optimized-mandelbrot/' + revision + '/' + name
    with urllib.request.urlopen(url) as response:
        (original/name).write_bytes(response.read())
def baseline(name):
    return subprocess.check_output(['git', 'show', '8c5e704903500165dbddccf5e092ea5fbda373c8:examples/fractal/' + name], cwd=root)

adapted = out/'adapted'
adapted.mkdir(exist_ok=True)
for name in ['Mandelbrot.h', 'MandelbrotStateEngineSimple.h', 'Mandelbrot_fastest_raw.cpp', 'LICENSE']:
    shutil.copyfile(original/name, adapted/name)
(adapted/'reference.h').write_text('#pragma once\nextern long long reference_x, reference_y;\nextern double reference_magnitude[256 * 257];\n')
engine = (adapted/'MandelbrotStateEngineSimple.h').read_text()
engine = '#include "reference.h"\n' + engine
engine = engine.replace('px = xs + w * inc;', 'px = -2.5 + (double(reference_x * 256 + w) + 0.5) * inc;')
engine = engine.replace('py = ys - h * inc;', 'py = 2.0 - (double(reference_y * 256 + h) + 0.5) * inc;')
(adapted/'MandelbrotStateEngineSimple.h').write_text(engine)
code = (adapted/'Mandelbrot_fastest_raw.cpp').read_text()
code = code.replace('sx * sx + sy * sy <= 4.0', 'sx * sx + sy * sy <= 65536.0')
code = code.replace('_mm256_set1_pd(4.0)', '_mm256_set1_pd(65536.0)')
code = code.replace('pp[dx[b] + dy[b] * width] = iterations - si;', 'pp[dx[b] + dy[b] * width] = iterations - si;\n            reference_magnitude[dx[b] + dy[b] * width] = sx * sx + sy * sy;')
start = code.index('\t__m256d s1b_temp_y')
end = code.index('\n}\n\nvoid Mandelbrot::fastest_raw', start)
body = ['\t(void)add_one_exp;']
for n in range(1,5):
    body.extend([f'\t__m256d r{n} = _mm256_mul_pd(s{n}_x, s{n}_x);',f'\t__m256d i{n} = _mm256_mul_pd(s{n}_y, s{n}_y);',f'\ts{n}_y = _mm256_add_pd(_mm256_mul_pd(_mm256_mul_pd(_mm256_set1_pd(2.0), s{n}_x), s{n}_y), s{n}_cy);',f'\ts{n}_x = _mm256_add_pd(_mm256_sub_pd(r{n}, i{n}), s{n}_cx);'])
code = code[:start]+'\n'.join(body)+code[end:]
(adapted/'Mandelbrot_fastest_raw.cpp').write_text(code)
spec=importlib.util.spec_from_file_location('bench',root/'docs/benchmarks/fractal-2026-10-23/compare.py')
module=importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
cases=module.CASES
(out/'cases.json').write_text(json.dumps(cases,indent=2)+'\n')
fern_dir=out/'fern'
fern_dir.mkdir(exist_ok=True)
(fern_dir/'binary.fern').write_bytes(baseline('binary.fern'))
code = baseline('fractal.fern').decode()
code += '''
struct EscapeImage { counts: i32[], magnitudes: i64[] }
function escape_image(z: i32, x: i64, y: i64): EscapeImage {
  let counts: i32[] = __alloc_i32(SIZE * SIZE);
  let magnitudes: i64[] = __alloc_i64(SIZE * SIZE);
  let limit: i32 = max_iterations(z);
  let result: EscapeBatch = EscapeBatch {
    limit: limit,
    magnitude0: -1.0, magnitude1: -1.0, magnitude2: -1.0, magnitude3: -1.0,
    steps0: 0, steps1: 0, steps2: 0, steps3: 0,
  };
  let step: f64 = 4.0 / ((1 as i64 << (z + 8) as i64) as f64);
  let row: i32 = 0;
  while (row < SIZE) {
    let imaginary: f64 = 2.0 - ((y * (SIZE as i64) + row as i64) as f64 + 0.5) * step;
    let column: i32 = 0;
    while (column < SIZE) {
      let real0: f64 = -2.5 + ((x * (SIZE as i64) + column as i64) as f64 + 0.5) * step;
      let real1: f64 = -2.5 + ((x * (SIZE as i64) + (column + 1) as i64) as f64 + 0.5) * step;
      let real2: f64 = -2.5 + ((x * (SIZE as i64) + (column + 2) as i64) as f64 + 0.5) * step;
      let real3: f64 = -2.5 + ((x * (SIZE as i64) + (column + 3) as i64) as f64 + 0.5) * step;
      result = escape_batch(result, real0, real1, real2, real3, imaginary);
'''
for n in range(4):
    code+=f'''      let count{n}: i32 = limit;
      if (result.magnitude{n} >= 0.0) {{ count{n} = result.steps{n}; }}
      counts = counts.with(row * SIZE + column + {n}, count{n});
      magnitudes = magnitudes.with(row * SIZE + column + {n}, f64_bits(result.magnitude{n}));
'''
code+='''      column = column + 4;
    }
    row = row + 1;
  }
  return EscapeImage { counts: counts, magnitudes: magnitudes };
}
function measure_kernel(name: string, z: i32, x: i64, y: i64, raw: boolean): void {
  let start: i64 = now_ns();
  let image: EscapeImage = escape_image(z, x, y);
  let ns: i64 = now_ns() - start;
  if (raw) {
    let bytes: u8[] = [];
    let index: i32 = 0;
    while (index < image.counts.len()) {
      bytes = binary.append_u32_be(bytes, image.counts[index] as u32);
      let bits: i64 = image.magnitudes[index];
      bytes = binary.append_u32_be(bytes, (bits >> 32) as u32);
      bytes = binary.append_u32_be(bytes, bits as u32);
      index = index + 1;
    }
    match (stdout().write_bytes(bytes)) { Ok(_) => {}, Err(_) => { exit(1); }, }
  } else {
    let sum: i64 = 0;
    for count in image.counts { sum = sum + count as i64; }
    print(name + "," + ns.to_string() + "," + sum.to_string());
  }
}
function main(): i32 {
  let raw: boolean = args().len() > 1;
'''
for name,z,x,y in cases:
    code+=f'  measure_kernel("{name}", {z}, {x}, {y}, raw);\n'
code+='  return 0;\n}\n'
(fern_dir/'kernel.fern').write_text(code)
cpp='''#include <chrono>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <algorithm>
#include "Mandelbrot.h"
#include "reference.h"
long long reference_x, reference_y;
double reference_magnitude[256 * 257];
Mandelbrot::Mandelbrot(double, double, double zoom, uint32_t w, uint32_t h) {
  width = w; height = h; p = new iterations_t[width * (height + 1)]();
  xs = ys = 0.0; inc = zoom;
}
Mandelbrot::~Mandelbrot() { delete[] p; }
void Mandelbrot::reset(iterations_t* pp, uint32_t stride, uint32_t w, uint32_t h) {
  for(uint32_t i=0; i<h; i++) memset(pp+i*stride,0,w*sizeof(iterations_t));
}
void word(uint32_t n) { for (int i=24; i>=0; i-=8) putchar((n>>i)&255); }
void measure(const char* name, int z, long long tx, long long ty, bool raw) {
  reference_x=tx; reference_y=ty;
  auto start=std::chrono::steady_clock::now();
  Mandelbrot m(0,0,std::ldexp(4.0,-z-8),256,256);
  int limit=256+64*z;
  m.fastest_raw(limit);
  auto ns=std::chrono::duration_cast<std::chrono::nanoseconds>(std::chrono::steady_clock::now()-start).count();
  uint64_t sum=0;
  for(int i=0; i<256*256; i++) {
    uint32_t count=std::min<uint32_t>(m.p[i],limit);
    double mag = count==uint32_t(limit)? -1.0 : reference_magnitude[i];
    if (raw) {
      uint64_t bits; memcpy(&bits,&mag,8);
      word(count); word(bits>>32); word(bits);
    }
    sum+=count;
  }
  if(!raw) printf("%s,%lld,%llu\\n",name,(long long)ns,(unsigned long long)sum);
}
int main(int argc,char**) {
  bool raw=argc>1;
'''
for name,z,x,y in cases:
    cpp+=f'  measure("{name}",{z},{x}LL,{y}LL,raw);\n'
cpp+='}\n'
(adapted/'bench.cpp').write_text(cpp)
print('Prepared the adapted AVX2 reference and a Fern kernel-only driver for nine tiles.')
