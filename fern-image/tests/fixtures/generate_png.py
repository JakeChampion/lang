from pathlib import Path
import zlib,struct
sig=b'\x89PNG\r\n\x1a\n'
def chunk(t,b):return struct.pack('>I',len(b))+t+b+struct.pack('>I',zlib.crc32(t+b))
def image(raw,channels,filter=0,mode=None,suffix=b''):
 w,h=2,2
 rows=[raw[i*2*channels:(i+1)*2*channels] for i in range(2)]
 out=b''
 for y,row in enumerate(rows):
  line=[]
  for i,x in enumerate(row):
   a=row[i-channels] if i>=channels else 0
   b=rows[y-1][i] if y else 0
   c=rows[y-1][i-channels] if y and i>=channels else 0
   p=a+b-c;da,db,dc=abs(p-a),abs(p-b),abs(p-c)
   predictor=[0,a,b,(a+b)//2,a if da<=db and da<=dc else b if db<=dc else c][min(filter,4)]
   line.append((x-predictor)&255)
  out+=bytes([filter])+bytes(line)
 hdr=struct.pack('>IIBBBBB',w,h,8,mode if mode is not None else 2 if channels==3 else 6,0,0,0)
 return sig+chunk(b'IHDR',hdr)+chunk(b'IDAT',zlib.compress(out)+suffix)+chunk(b'IEND',b'')
rgb=bytes([255,0,17,11,200,0,2,3,4,250,128,99]);rgba=bytes([255,0,17,0,11,200,0,128,2,3,4,255,250,128,99,17])
fixtures={**{'rgb'+str(f):image(rgb,3,f) for f in range(5)},**{'rgba'+str(f):image(rgba,4,f) for f in range(5)},'unsupported':image(rgb,3,mode=3),'bad_filter':image(rgb,3,filter=5),'compressed_trailing':image(rgb,3,suffix=b'X')}
base=fixtures['rgb0']
for name,kind in [('transparency',b'tRNS'),('profile',b'iCCP'),('animation',b'acTL'),('critical',b'ABCD')]:
 fixtures[name]=base[:33]+chunk(kind,b'')+base[33:]
fixtures['short_scanline']=sig+base[8:33]+chunk(b'IDAT',zlib.compress(b'\0'))+chunk(b'IEND',b'')
text='// Fixtures generated offline with Python zlib and PNG filter equations.\n'
for name,data in fixtures.items():text+='pub function '+name+'(): u8[] { return ['+', '.join(str(x)+' as u8' for x in data)+']; }\n'
for name,data in [('rgb_bytes',rgb),('rgba_bytes',rgba)]:text+='pub function '+name+'(): u8[] { return ['+', '.join(str(x)+' as u8' for x in data)+']; }\n'
Path(__file__).with_name('png.fern').write_text(text)
