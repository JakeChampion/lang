from pathlib import Path
import os,subprocess,json,hashlib,datetime
root=Path(__file__).resolve().parent;env=dict(os.environ,APT_CONFIG='/workspace/scratch/lang-tools-20261109/apt/config');names=['libaio1t64','libbrlapi0.8','libcacard0','libcapstone5','libdaxctl1','libexecs1','libfdt1','libndctl6','libpmem1','liburing2','libusbredirparser1t64','libvdeplug2t64','qemu-system-common','qemu-system-data','qemu-system-arm'];state={'started':datetime.datetime.now(datetime.timezone.utc).isoformat(),'source':'Existing approved Debian snapshot package lists. Private extraction only, no system package/configuration change.','packages':names,'runs':[]}
def save():(root/'result.json').write_text(json.dumps(state,indent=2)+'\n')
save()
try:
 with(root/'download.stdout').open('wb')as so,(root/'download.stderr').open('wb')as se:r=subprocess.run(['/usr/bin/apt-get','download']+names,cwd=root/'debs',env=env,stdout=so,stderr=se)
 state['download_exit']=r.returncode;save();assert r.returncode==0
 for p in sorted((root/'debs').glob('*.deb')):
  with(root/'extract.log').open('ab')as log:r=subprocess.run(['dpkg-deb','-x',str(p),str(root/'root')],stdout=log,stderr=subprocess.STDOUT)
  state['runs'].append({'package':p.name,'sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'exit_code':r.returncode});save();assert r.returncode==0
 wrapper=root/'bin/qemu-system-aarch64';wrapper.write_text('#!/bin/sh\nexport LD_LIBRARY_PATH='+str(root/'root/usr/lib/x86_64-linux-gnu')+'${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}\nexec '+str(root/'root/usr/bin/qemu-system-aarch64')+' -L '+str(root/'root/usr/share/qemu')+' "$@"\n');wrapper.chmod(0o755)
 r=subprocess.run([str(wrapper),'--version'],capture_output=True);(root/'version.stdout').write_bytes(r.stdout);(root/'version.stderr').write_bytes(r.stderr);state['version_exit']=r.returncode;save();assert r.returncode==0;state.update(exit_code=0,finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
except Exception as e:state.update(exit_code=1,error=repr(e),finished=datetime.datetime.now(datetime.timezone.utc).isoformat());raise
finally:save()
