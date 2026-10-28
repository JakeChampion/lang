from pathlib import Path
import subprocess,json,hashlib,struct,bisect,shutil
root=Path('/tmp/image-engine-worktrees/lang-image-retained-large-free-health');out=Path('/tmp/image-retained-large-free-native-capability');assert not out.exists();out.mkdir();g=lambda *a:subprocess.check_output(['git',*a],cwd=root,text=True).strip();assert g('rev-parse','HEAD')=='5acfabebbef2f87844e1389615c7aecd3288baa3'and not g('status','--porcelain');sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
diag=Path('/tmp/image-retained-large-free-current-diagnostic.json');d=json.loads(diag.read_text());assert d['compile_exit']==d['runtime_exit']==0 and d['sources_unchanged'];helper=d['stdout'].split('BEGIN_LARGE_PUSH\n',1)[1].split('\nEND_LARGE_PUSH',1)[0]+'\n';(out/'actual-helper.s').write_text(helper)
limit=1<<34;bound=(1<<35)-1
caps=sorted({q*(1<<(bit-2))for bit in range(19,35)for q in range(4,8)if q*(1<<(bit-2))<=limit});assert len(caps)==61 and caps[-1]==limit
cuts=sorted({0,limit+1,*[x+1 for x in caps if x<limit],*[1<<bit for bit in range(19,35)]});parts=list(zip(cuts[:-1],[x-1 for x in cuts[1:]]));assert len(parts)==77 and sum(hi-lo+1 for lo,hi in parts)==limit+1
samples={0,1,524287,524288,524289,limit,limit+1,(1<<35),(1<<63)-1,1<<63,(1<<64)-1}
for lo,hi in parts:samples.update([lo,(lo+hi)//2,hi])
for bit in range(18,35):samples.update([(1<<bit)-1,1<<bit,(1<<bit)+1])
for x in caps:samples.update([x-1,x,x+1])
samples=sorted(samples);registers=['rax','rcx','rdx','rbx','rsp','rbp','rsi','rdi',*[f'r{i}'for i in range(8,16)]];head=0x7000000000000000;block0=0x1122334455667788;block1=0x8877665544332211
reset='''leaq __fern_large_freelist(%rip), %rdx
movabs $0x7000000000000000, %rax
movq $64, %rcx
.Lprobe_reset:
movq %rax, (%rdx)
addq $16, %rax
addq $8, %rdx
decq %rcx
jnz .Lprobe_reset
movabs $0x1122334455667788, %rax
movq %rax, probe_block(%rip)
movabs $0x8877665544332211, %rax
movq %rax, probe_block+8(%rip)
'''
setup=''.join(f'movabs ${0x1100110011001100+i*0x01010101010101:#x}, %{reg}\n'for i,reg in enumerate(registers)if reg not in ['rsp','rsi','r8','r12','r13'])+'movq (%r12), %rsi\nleaq probe_block(%rip), %r8\n'
snapshot=lambda n:''.join(f'movq %{reg}, probe_{n}+{i*8}(%rip)\n'for i,reg in enumerate(registers))
start='.text\n.globl _start\n_start:\nleaq probe_inputs(%rip), %r12\nleaq probe_inputs_end(%rip), %r13\n.Lprobe_loop:\n'+reset+setup+snapshot('before')+'call __fern_large_push\n'+snapshot('after')+'''movq $1, %rax
movq $1, %rdi
leaq probe_record(%rip), %rsi
movq $784, %rdx
syscall
cmpq $784, %rax
jne .Lprobe_io_failure
addq $8, %r12
cmpq %r13, %r12
jb .Lprobe_loop
xorq %rdi, %rdi
jmp .Lprobe_exit
.Lprobe_io_failure:
movq $2, %rdi
.Lprobe_exit:
movq $60, %rax
syscall
'''
data='.section .data\n.balign 8\nprobe_inputs:\n'+''.join(f'.quad {n:#x}\n'for n in samples)+'probe_inputs_end:\n.section .bss\n.balign 8\nprobe_record:\nprobe_before: .skip 128\nprobe_after: .skip 128\nprobe_block: .skip 16\n__fern_large_freelist: .skip 512\n.section .note.GNU-stack,"",@progbits\n'
results={}
for name,body in [('actual',helper),('wrong-shift32',helper.replace('shrq %cl, %rdx','shrl %cl, %edx'))]:
 if name=='wrong-shift32':assert body!=helper and helper.count('shrq %cl, %rdx')==1
 asm=out/(name+'.s');asm.write_text(start+body+data);elf=out/(name+'.elf');cmd=['gcc','-static','-nostdlib','-no-pie',str(asm),'-o',str(elf)];r=subprocess.run(cmd,capture_output=True,text=True);(out/(name+'-compile.log')).write_text(r.stdout+r.stderr);assert r.returncode==0,r.stdout+r.stderr
 symbols={}
 for line in subprocess.check_output(['nm','-n',str(elf)],text=True).splitlines():
  fields=line.split()
  if len(fields)==3:symbols[fields[2]]=int(fields[0],16)
 r=subprocess.run([str(elf)],cwd=out,capture_output=True);(out/(name+'.stdout.bin')).write_bytes(r.stdout);(out/(name+'.stderr.log')).write_bytes(r.stderr);assert r.returncode==0 and len(r.stdout)==len(samples)*784,(name,r.returncode,len(r.stdout),r.stderr)
 errors=[];seen_classes=set()
 for index,n in enumerate(samples):
  words=struct.unpack_from('<98Q',r.stdout,index*784);before=list(words[:16]);after=list(words[16:32]);block=list(words[32:34]);slots=list(words[34:]);expect=before.copy();expect_slots=[head+i*16 for i in range(64)];expect_block=[block0,block1]
  assert before[6]==n and before[8]==symbols['probe_block']
  if n>limit:expect[0]=limit;cls=None
  else:
   cls=bisect.bisect_left(caps,max(n,524288));expect[0]=cls;expect[1]=head+cls*16;expect[2]=symbols['__fern_large_freelist'];expect_slots[cls]=symbols['probe_block'];expect_block[0]=head+cls*16;seen_classes.add(cls)
  if after!=expect or slots!=expect_slots or block!=expect_block:errors.append(dict(input_decimal=str(n),expected_class=cls,observed_rax_decimal=str(after[0]),registers_match=after==expect,block_match=block==expect_block,slots_match=slots==expect_slots))
 results[name]=dict(compile_exit=0,runtime_exit=0,assembly_sha256=sha(asm),binary_sha256=sha(elf),stdout_sha256=sha(out/(name+'.stdout.bin')),cases=len(samples),mismatch_count=len(errors),mismatches=errors,covered_classes=sorted(seen_classes))
 assert (not errors)if name=='actual'else bool(errors)
record=dict(source=g('rev-parse','HEAD'),diagnostic_only=True,not_Fern_model_qualification=True,actual_emitter_helper_sha256=sha(out/'actual-helper.s'),source_diagnostic_sha256=sha(diag),limit_decimal=str(limit),proposed_affine_bound_decimal=str(bound),capacity_count=len(caps),partition_count=len(parts),partition_covered_integer_count=str(sum(hi-lo+1 for lo,hi in parts)),capacities=caps,partitions=[dict(low=lo,high=hi)for lo,hi in parts],sample_count=len(samples),sample_inputs_decimal=[str(n)for n in samples],gcc_path=shutil.which('gcc'),gcc_version=subprocess.check_output(['gcc','--version'],text=True).splitlines()[0],results=results,worktree_clean=not g('status','--porcelain'))
Path('/tmp/image-retained-large-free-native-capability.json').write_text(json.dumps(record,indent=2)+'\n');print(dict(samples=len(samples),classes=len(caps),partitions=len(parts),actual_mismatches=results['actual']['mismatch_count'],required_mutant_mismatches=results['wrong-shift32']['mismatch_count'],first_mutant_mismatch=results['wrong-shift32']['mismatches'][0]));assert record['worktree_clean']
