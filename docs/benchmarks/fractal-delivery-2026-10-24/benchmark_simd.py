#!/usr/bin/env python3
"""Compare explicit 128/256-bit SIMD widths with identical lane-refill algorithms."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import statistics
import subprocess
import tempfile

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
spec = importlib.util.spec_from_file_location("tile_bench", HERE.parent / "fractal-2026-10-23/compare.py")
tile_bench = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tile_bench)

HEADER = r'''
#include <immintrin.h>
#include <algorithm>
#include <bit>
#include <chrono>
#include <cmath>
#include <cstdint>
#include <cstdio>
#include <string>
#include <vector>
using V2 = double __attribute__((vector_size(16)));
using V4 = double __attribute__((vector_size(32)));
struct View { int z; long long x,y; double step; int limit; };
struct Pixels { std::vector<int> counts; std::vector<double> magnitudes; };
struct Lane { bool active; int column; double parameter; int born,stop; double saved_real,saved_imaginary; int checkpoint; };
bool interior(double real,double imaginary) {
  double y2=imaginary*imaginary, x=real-0.25, bulb=real+1.0;
  double q=x*x+y2;
  return q*(q+real-0.25)<=0.25*y2 || bulb*bulb+y2<=0.0625;
}
Lane seed(const View &view,double imaginary,int column,int round) {
  for(int at=column;at<256;++at) {
    double parameter=-2.5+(double(view.x*256+at)+0.5)*view.step;
    if(!interior(parameter,imaginary)) return {true,at,parameter,round,round+(view.limit+3)/4,0,0,16};
  }
  return {false,256,0,round,2000000000,0,0,16};
}
'''


def kernel(width, streams):
    lanes, vector = width // 64, "V" + str(width // 64)
    count = lanes * streams
    full = (1 << count) - 1
    lines = [f'extern "C" __attribute__((noinline)) void kernel_{width}_{streams}(const View &view,Pixels &pixels) {{',
        'for(int row=0;row<256;++row) {',
        'double imaginary=2.0-(double(view.y*256+row)+0.5)*view.step;', 'int round=0, active=0, next_stop=2000000000;']
    for lane in range(count):
        column = "0" if lane == 0 else f"lane{lane-1}.column+1"
        lines += [f'Lane lane{lane}=seed(view,imaginary,{column},0);',
            f'if(lane{lane}.active) active|={1<<lane};', f'next_stop=std::min(next_stop,lane{lane}.stop);']
    lines += [f'int next=lane{count-1}.column+1;', f'{vector} cy={vector}{{'+','.join(['imaginary']*lanes)+'};']
    for stream in range(streams):
        fields = ",".join(f"lane{stream*lanes+k}.parameter" for k in range(lanes))
        lines += [f'{vector} parameter{stream}={vector}{{{fields}}};', f'{vector} real{stream}={{}},imaginary{stream}={{}};']
    lines += ['while(active) {']
    for stream in range(streams):
        lines += [f'{vector} first_real{stream}=real{stream},first_imaginary{stream}=imaginary{stream};']
    for _ in range(4):
        lines += ['{']
        for stream in range(streams):
            lines += [f'{vector} rr{stream}=real{stream}*real{stream}, ii{stream}=imaginary{stream}*imaginary{stream};',
                f'imaginary{stream}=(real{stream}+real{stream})*imaginary{stream}+cy;',
                f'real{stream}=rr{stream}-ii{stream}+parameter{stream};']
        lines += ['}']
    lines += ['++round; int finished=0,periodic=0;']
    for stream in range(streams):
        radius = vector + "{" + ",".join(["65536.0"] * lanes) + "}"
        mask = f'_mm_movemask_pd((__m128d)(magnitude{stream}>{radius}))' if width == 128 else f'_mm256_movemask_pd((__m256d)(magnitude{stream}>{radius}))'
        lines += [f'{vector} magnitude{stream}=real{stream}*real{stream}+imaginary{stream}*imaginary{stream};', f'finished|=({mask})<<{stream*lanes};']
    lines += ['finished&=active;', 'if(round>=next_stop) {']
    for lane in range(count): lines += [f'if(lane{lane}.active && round>=lane{lane}.stop) finished|={1<<lane};']
    lines += ['}', 'if((round&3)==0) {']
    for lane in range(count):
        stream, element = divmod(lane, lanes)
        lines += [f'if((active&{1<<lane}) && !(finished&{1<<lane})) {{',
            f'double zr=real{stream}[{element}],zi=imaginary{stream}[{element}];',
            f'if(zr==lane{lane}.saved_real && zi==lane{lane}.saved_imaginary) periodic|={1<<lane};',
            f'if((round-lane{lane}.born)*4>=lane{lane}.checkpoint) {{ lane{lane}.saved_real=zr;lane{lane}.saved_imaginary=zi;lane{lane}.checkpoint*=2; }}', '}']
    lines += ['}', 'finished|=periodic;']
    for lane in range(count):
        stream, element = divmod(lane, lanes)
        lines += [f'if(finished&{1<<lane}) {{', f'int stop=(round-lane{lane}.born)*4, count=stop; double magnitude=-1.0;',
            f'if(!(periodic&{1<<lane})) {{', f'count=stop-4; double zr=first_real{stream}[{element}],zi=first_imaginary{stream}[{element}];',
            'while(count<=stop && count<view.limit) {', 'double rr=zr*zr,ii=zi*zi;',
            'if(rr+ii>65536.0) {magnitude=rr+ii;break;}',
            f'zi=(zr+zr)*zi+imaginary;zr=rr-ii+lane{lane}.parameter;++count;', '}', '}',
            f'int at=row*256+lane{lane}.column;', 'pixels.counts[at]=magnitude<0?view.limit:count;pixels.magnitudes[at]=magnitude;',
            f'lane{lane}=seed(view,imaginary,next,round);next=lane{lane}.column+1;',
            f'if(!lane{lane}.active) active&=({full}^{1<<lane});',
            f'parameter{stream}[{element}]=lane{lane}.parameter;real{stream}[{element}]=0;imaginary{stream}[{element}]=0;', '}']
    lines += ['if(finished) {next_stop=2000000000;']
    for lane in range(count): lines += [f'next_stop=std::min(next_stop,lane{lane}.stop);']
    lines += ['}', '}', '}', '}']
    return '\n'.join(lines)


FOOTER = r'''
void word(uint32_t n) {for(int shift=24;shift>=0;shift-=8) std::putchar((n>>shift)&255);}
using Kernel=void(*)(const View&,Pixels&);
void measure(Kernel kernel,const char *name,int z,long long x,long long y,bool raw) {
  View view{z,x,y,std::ldexp(4.0,-z-8),256+64*z};
  auto start=std::chrono::steady_clock::now();
  Pixels pixels{std::vector<int>(256*256,view.limit),std::vector<double>(256*256,-1.0)};
  kernel(view,pixels);
  auto ns=std::chrono::duration_cast<std::chrono::nanoseconds>(std::chrono::steady_clock::now()-start).count();
  uint64_t total=0,checksum=0;
  for(int i=0;i<256*256;++i) {
    uint64_t bits=std::bit_cast<uint64_t>(pixels.magnitudes[i]);
    total+=pixels.counts[i];checksum^=bits;
    if(raw) {word(pixels.counts[i]);word(bits>>32);word(bits);}
  }
  if(!raw) std::printf("%s,%lld,%llu,%llu\n",name,(long long)ns,(unsigned long long)total,(unsigned long long)checksum);
}
int main(int argc,char **argv) {
  if(argc<2)return 1;
  std::string mode=argv[1];bool raw=argc>2;Kernel kernel=nullptr;
  SELECT
  if(!kernel)return 2;
  CALLS
}
'''
FERN = '''import "./kernel";
import "std/binary";
import "std/i64";
import "std/i32";
function measure(name: string,z: i32,x: i64,y: i64): void {
  let view: kernel.View = kernel.View { size: 256,x: x*256,y:y*256,step:4.0/((1 as i64 << (z+8) as i64) as f64),limit:256+64*z };
  let start: i64 = now_ns();
  let pixels: kernel.Pixels = kernel.render(view,None);
  let ns: i64 = now_ns()-start;
  let bytes: u8[] = [];
  let total: i64 = 0;
  let at: i32 = 0;
  while(at<256*256) {
    let count: i32 = pixels.counts[at];
    let bits: i64 = pixels.magnitudes[at];
    if(f64_from_bits(bits)<=0.0) {count=view.limit;bits=f64_bits(-1.0);}
    total=total+count as i64;
    if(args().len()>1) {bytes=binary.append_u32_be(bytes,count as u32);bytes=binary.append_u64_be(bytes,bits as u64);}
    at=at+1;
  }
  if(args().len()>1) {match(stdout().write_bytes(bytes)) {Ok(_)=>{},Err(_)=>{exit(1);},}}
  else {print(name+","+ns.to_string()+","+total.to_string()+",0");}
}
function main(): i32 {
CALLS
return 0;
}
'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--fern', type=Path, required=True)
    parser.add_argument('--runs', type=int, default=7)
    parser.add_argument('--output', type=Path, required=True)
    options = parser.parse_args()
    if options.runs < 1: parser.error('--runs must be positive')
    variants = [(128,2),(128,4),(256,2),(256,4)]
    source = HEADER+'\n'+'\n'.join(kernel(*variant) for variant in variants)+'\n'+FOOTER.replace('SELECT','\n'.join(f'if(mode=="{width}x{streams}")kernel=kernel_{width}_{streams};' for width,streams in variants)).replace('CALLS','\n'.join(f'measure(kernel,"{name}",{z},{x}LL,{y}LL,raw);' for name,z,x,y in tile_bench.CASES))
    env = dict(os.environ, FERN_PROFILE='off',FERN_SANITIZE='off',FERN_LEAKCHECK='off')
    with tempfile.TemporaryDirectory(prefix='fractal-simd-') as temporary:
        directory = Path(temporary)
        (directory/'kernel.cpp').write_text(source)
        flags = ['g++','-O3','-std=c++20','-mavx2','-ffp-contract=off','-fno-tree-slp-vectorize','-fno-tree-vectorize',str(directory/'kernel.cpp')]
        subprocess.run(flags+['-o',str(directory/'native')],check=True)
        subprocess.run(flags+['-S','-o',str(directory/'native.s')],check=True)
        for name in ['kernel','linear','series']:
            shutil.copyfile(ROOT/('examples/fractal/'+name+'.fern'),directory/(name+'.fern'))
        (directory/'driver.fern').write_text(FERN.replace('CALLS','\n'.join(f'measure("{name}",{z},{x},{y});' for name,z,x,y in tile_bench.CASES)))
        subprocess.run([str(options.fern.resolve()),'-target','x86-64-linux','-O','-o',str(directory/'fern'),str(directory/'driver.fern'),str(ROOT/'internal/stdlib')],env=env,check=True)
        commands = {f'{width}x{streams}':[str(directory/'native'),f'{width}x{streams}'] for width,streams in variants}
        commands['fern'] = [str(directory/'fern')]
        raw = {variant:subprocess.check_output(command+['--raw'],env=env) for variant,command in commands.items()}
        if len(raw['fern']) != 9*256*256*12 or any(data != raw['fern'] for data in raw.values()):
            raise SystemExit('SIMD width changed an escape count or magnitude')
        runs = {variant:[] for variant in commands}
        names = list(commands)
        for index in range(options.runs):
            for variant in names[index%len(names):]+names[:index%len(names)]:
                output = subprocess.check_output(commands[variant],text=True,env=env)
                result = {}
                for line in output.splitlines():
                    name,ns,total,checksum = line.split(',')
                    result[name] = dict(ms=int(ns)/1e6,normalized_iterations=int(total))
                runs[variant].append(result)
            print(f'Completed SIMD benchmark round {index+1}/{options.runs}',flush=True)
        assembly = (directory/'native.s').read_text()
        codegen = {}
        for width,streams in variants:
            name = f'kernel_{width}_{streams}'
            body = assembly.split(name+':',1)[1].split('.size\t'+name,1)[0]
            instructions = [line for line in body.splitlines() if line.startswith('\t') and not line.startswith('\t.')]
            codegen[f'{width}x{streams}'] = dict(instructions=len(instructions),stack_vector_moves=sum(('mov' in line and ('%xmm' in line or '%ymm' in line) and ('(%rsp)' in line or '(%rbp)' in line)) for line in instructions),ymm_instructions=sum('%ymm' in line for line in instructions))
    summary = {name:{variant:statistics.median(run[name]['ms'] for run in samples) for variant,samples in runs.items()} for name,_,_,_ in tile_bench.CASES}
    report = dict(scope='Direct escape kernel and result storage only. No shared-prefix preparation, color, PNG, transport, or output serialization. Scalar replay and analytic/cycle tests remain active.',source_sha256=hashlib.sha256(source.encode()).hexdigest(),compiler_sha256=hashlib.sha256(options.fern.read_bytes()).hexdigest(),flags=flags[:-1],samples=9*256*256,all_samples_match=True,sample_sha256=hashlib.sha256(raw['fern']).hexdigest(),codegen=codegen,runs=runs,summary=summary)
    options.output.write_text(json.dumps(report,indent=2)+'\n')
    for name,values in summary.items(): print(name,json.dumps(values))


if __name__=='__main__': main()
