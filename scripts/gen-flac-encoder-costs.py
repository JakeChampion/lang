#!/usr/bin/env python3
"""Generate exhaustive independent FLAC subframe bit-cost expectations."""
from pathlib import Path
import random
ROOT=Path(__file__).resolve().parent.parent
rng=random.Random(14688)
def cost(a,depth,maxorder,maxpart):
    best=8+len(a)*depth
    union=0
    for x in a: union|=x
    wasted=min((union & -union).bit_length()-1,depth-1) if union else depth-1
    for w in sorted({0,wasted}):
        b=[x>>w for x in a]
        best=min(best,8+w+len(a)*(depth-w))
        for order in range(min(maxorder+1,len(a))):
            r=b[:]
            for _ in range(order): r=[r[i]-r[i-1] for i in range(1,len(r))]
            if any(x<=-2147483648 or x>=2147483648 for x in r): continue
            folded=[x*2 if x>=0 else -2*x-1 for x in r]
            for p in range(maxpart+1):
                parts=1<<p
                if len(a)%parts or len(a)//parts<order: break
                costs=[6,6]
                per=len(a)//parts
                for part in range(parts):
                    start=max(part*per,order)-order
                    end=(part+1)*per-order
                    values=folded[start:end]
                    width=max(values,default=0).bit_length()
                    for method,limit in enumerate([14,30]):
                        c=min((4+method)+len(values)*(k+1)+sum(x>>k for x in values) for k in range(limit+1))
                        if width<=31: c=min(c,(4+method)+5+len(values)*width)
                        costs[method]+=c
                best=min(best,8+w+order*(depth-w)+min(costs))
    return best
rows=[]
for i in range(48):
    depth=[8,16,24,32][i%4]
    n=[2,8,16,32,64][i%5]
    shift=i%4
    bound=min((1<<(depth-1))-1,1<<[2,5,9,16,24,29][i%6])>>shift
    a=[rng.randint(-bound,bound)<<shift for _ in range(n)]
    if len(set(a))==1: a[-1]^=1<<shift
    if i==46: a=[-2147483648,2147483647]* (n//2);depth=32
    if i==47: a=[0]*(n//2)+[rng.randint(-32768,32767) for _ in range(n//2)];depth=32
    order=i%5;parts=i%5
    rows.append('    Case { depth: %d, order: %d, partition: %d, bits: %di64, samples: [%s] },'%(depth,order,parts,cost(a,depth,order,parts),', '.join(map(str,a))))
source='''// Costs come from exhaustive Python enumeration of Rice parameters 0..30.
import "std/audio/flac/plan";
import "std/test";

struct Case { depth: i32, order: i32, partition: i32, bits: i64, samples: i32[] }

function cases(): Case[] {
  return [
'''+ '\n'.join(rows)+'''
  ];
}

function costs(): test.TestOutcome {
  for value in cases() {
    let Ok((chosen, _)) = plan.choose(value.samples, 1, 0, 0, value.depth, value.order, value.partition, []) else {
      return test.fail("plan rejected");
    };
    if (chosen.bit_count() != value.bits) {
      return test.fail("exhaustive minimum: depth " + value.depth.to_string() + ", expected " + value.bits.to_string() + ", actual " + chosen.bit_count().to_string());
    }
  }
  return test.pass();
}

function main(): i32 {
  let runner = test.new("FLAC exhaustive Rice cost oracle");
  runner = runner.it("48 independent minima over predictors, partitions, Rice/Rice2, raw escapes, and wasted bits", costs);
  return runner.finish();
}
'''
destination=ROOT/'tests/stdlib/flac_encoder_cost_test.fern'
if '--check' in __import__('sys').argv:
    assert destination.read_text()==source, str(destination)+' is stale'
else:
    destination.write_text(source)
