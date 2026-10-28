from pathlib import Path
import struct, hashlib, json, subprocess, re, bisect, sys

p = Path(__file__).parent
kind = sys.argv[1]
assert kind in ["stage2", "stage3"]
def loads(binary):
    data = binary.read_bytes()
    phoff = struct.unpack_from('<Q', data, 32)[0]
    entsize, count = struct.unpack_from('<HH', data, 54)
    result = []
    for i in range(count):
        kind, flags, offset, address, _, size, memory, _ = struct.unpack_from('<IIQQQQQQ', data, phoff + i * entsize)
        if kind == 1:
            start = offset + (64 if offset == 0 else 0)
            result.append({'flags': flags, 'offset': offset, 'address': address, 'file_size': size, 'memory_size': memory, 'sha256_without_elf_header': hashlib.sha256(data[start:offset + size]).hexdigest()})
    return result
proof = {name: loads(p / name) for name in [kind, kind + '-g']}
proof['loaded_code_and_data_identical_except_elf_header'] = proof[kind] == proof[kind + '-g']
release_header = (p / kind).read_bytes()[:64]
debug_header = (p / (kind + '-g')).read_bytes()[:64]
header_differences = [i for i in range(64) if release_header[i] != debug_header[i]]
assert set(header_differences) <= set(range(40, 48)) | set(range(58, 64))
proof['elf_header_different_byte_offsets'] = header_differences
proof['elf_header_differences_only_section_fields'] = True
(p / (kind + '-elf-load-equivalence.json')).write_text(json.dumps(proof, indent=2) + '\n')
assert proof['loaded_code_and_data_identical_except_elf_header']
syms = []
for line in (p / (kind + '-g.nm')).read_text().splitlines():
    m = re.match(r'([0-9a-fA-F]+) [Tt] (.+)$', line)
    if m:
        syms.append((int(m[1], 16), m[2]))
syms.sort()
addresses = [s[0] for s in syms]
def resolve(m):
    address = int(m[1], 16)
    i = bisect.bisect_right(addresses, address) - 1
    if i < 0:
        return m[0]
    start, name = syms[i]
    return name + ('+' + hex(address - start) if address != start else '') + '@' + m[1]
annotate = str(p.parent / 'tools/usr/bin/callgrind_annotate')
for report, args in [('self', ['--inclusive=no']), ('callers', ['--inclusive=yes', '--tree=caller'])]:
    result = subprocess.run([annotate, '--threshold=100'] + args + [str(p / (kind + '.callgrind'))], check=True, capture_output=True, text=True)
    (p / (kind + '.' + report + '.annotated')).write_text(result.stdout)
    output = re.sub(r'0x([0-9a-fA-F]+)', resolve, result.stdout)
    (p / (kind + '.' + report + '.resolved')).write_text(output)
    if report == 'self':
        print('\n'.join(output.splitlines()[20:38]))
