from pathlib import Path
import re
import subprocess
root=Path(__file__).resolve().parent
fern=subprocess.check_output(['git', 'show', '8c5e704903500165dbddccf5e092ea5fbda373c8:examples/fractal/fractal.fern'], cwd=root.parents[3], text=True)
known=fern[fern.index('fip function known_interior'):fern.index('// Two independent f64x2 orbits')]
known=known[known.index('{')+1:known.rindex('}')]
body=fern[fern.index('fip function escape_batch'):fern.index('function color_pixel')]
body=body[body.index('{')+1:body.rindex('}')]
body=re.sub(r'  result = EscapeBatch \{.*?\n  \};', '\n'.join(f'  result.{k}{n} = {k}{n};' for k in ['magnitude','steps'] for n in range(4)),body,flags=re.S)
def cpp(text):
    text=re.sub(r'let (\w+): (\w+) =',r'\2 \1 =',text)
    for a,b in [('f64','double'),('i32','int'),('boolean','bool')]:
        text=re.sub(r'\b'+a+r'\b',b,text)
    return text
code='''#include <immintrin.h>
#include <chrono>
#include <cmath>
#include <cstdint>
#include <cstdio>
#include <cstring>
using f64x2 = double __attribute__((vector_size(16)));
inline f64x2 __f64x2_of(double a,double b) { return f64x2{a,b}; }
inline f64x2 __f64x2_splat(double a) { return f64x2{a,a}; }
inline double __f64x2_lane(f64x2 a,int b) { return a[b]; }
struct EscapeBatch {
  int limit;
  double magnitude0,magnitude1,magnitude2,magnitude3;
  int steps0,steps1,steps2,steps3;
};
bool known_interior(double real,double imaginary) {
'''+cpp(known)+'}\nEscapeBatch escape_batch(EscapeBatch result,double real0,double real1,double real2,double real3,double imaginary) {\n'+cpp(body)+'}\n'
bench=(root/'adapted/bench.cpp').read_text()
start=bench.index('void word(')
bench=bench[start:]
bench=bench.replace('  reference_x=tx; reference_y=ty;\n','')
bench=bench.replace('  Mandelbrot m(0,0,std::ldexp(4.0,-z-8),256,256);\n  int limit=256+64*z;\n  m.fastest_raw(limit);','''  int limit=256+64*z;
  double step=std::ldexp(4.0,-z-8);
  int counts[256*256]; double magnitudes[256*256];
  EscapeBatch result{.limit=limit};
  for(int row=0;row<256;row++) {
    double imaginary=2.0-(double(ty*256+row)+0.5)*step;
    for(int column=0;column<256;column+=4) {
      double r0=-2.5+(double(tx*256+column)+0.5)*step;
      double r1=-2.5+(double(tx*256+column+1)+0.5)*step;
      double r2=-2.5+(double(tx*256+column+2)+0.5)*step;
      double r3=-2.5+(double(tx*256+column+3)+0.5)*step;
      result=escape_batch(result,r0,r1,r2,r3,imaginary);
      int pos=row*256+column;
      counts[pos]=result.magnitude0<0?limit:result.steps0;
      counts[pos+1]=result.magnitude1<0?limit:result.steps1;
      counts[pos+2]=result.magnitude2<0?limit:result.steps2;
      counts[pos+3]=result.magnitude3<0?limit:result.steps3;
      magnitudes[pos]=result.magnitude0;
      magnitudes[pos+1]=result.magnitude1;
      magnitudes[pos+2]=result.magnitude2;
      magnitudes[pos+3]=result.magnitude3;
    }
  }''')
bench=bench.replace('std::min<uint32_t>(m.p[i],limit)','counts[i]')
bench=bench.replace('reference_magnitude[i]','magnitudes[i]')
code+=bench
(root/'same-kernel.cpp').write_text(code)
