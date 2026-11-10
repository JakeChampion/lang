from pathlib import Path
import os,time,json,datetime,signal
out=Path('/workspace/lang-interpreter-dispatch-20261109/build/interpreter-dispatch-20261109');specs=[('base',9207,Path('/workspace/lang-interpreter-timeout-20261109/build/interpreter-profile-20261109/full-normal.json')),('candidate',10522,out/'full-candidate.json')];hz=os.sysconf(os.sysconf_names['SC_CLK_TCK']);data={'note':'The tool lost the parent sessions. These monitors read orphan status from procfs and enforce the original wall deadlines. These are completion controls, not paired timing samples. CPU counters have clock-tick precision. Observed VmHWM is a lower bound if the final status omits it.','clock_ticks_per_second':hz,'processes':{}};path=out/'orphan-controls.json';assert not path.exists()
while True:
 active=0
 for label,pid,original in specs:
  entry=data['processes'].setdefault(label,{'pid':pid,'original_record':str(original),'observed_peak_rss_lower_bound_kib':0});d=json.loads(original.read_text());start=datetime.datetime.fromisoformat(d['started']);deadline=start.timestamp()+d['budget_seconds'];p=Path('/proc')/str(pid)
  if entry.get('finished'):continue
  try:
   stat=(p/'stat').read_text();fields=stat[stat.rfind(')')+2:].split();state=fields[0];entry.update(last_observed=datetime.datetime.now(datetime.timezone.utc).isoformat(),state=state,cpu_user_seconds=int(fields[11])/hz,cpu_system_seconds=int(fields[12])/hz)
   for line in (p/'status').read_text().splitlines():
    if line.startswith('VmHWM:'):entry['observed_peak_rss_lower_bound_kib']=max(entry['observed_peak_rss_lower_bound_kib'],int(line.split()[1]))
   if state=='Z':entry.update(finished=True,exit_code=os.waitstatus_to_exitcode(int(fields[49])),completion='zombie exit status from procfs');continue
   if time.time()>deadline and not entry.get('deadline_signal_sent'):
    os.killpg(pid,signal.SIGTERM);entry['deadline_signal_sent']=datetime.datetime.now(datetime.timezone.utc).isoformat();entry['timed_out']=True
   active+=1
  except FileNotFoundError:entry.update(finished=True,completion='process disappeared without retained exit status',exit_code=None)
 path.write_text(json.dumps(data,indent=2)+'\n')
 if not active:break
 time.sleep(.1)
print(json.dumps(data,indent=2))
