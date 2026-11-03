"""Run the exact composed scenarios sequentially; stop at the first failure."""
import argparse, datetime, hashlib, json, pathlib, subprocess, sys
p=argparse.ArgumentParser()
p.add_argument('--runtime-root',type=pathlib.Path,required=True)
p.add_argument('--composition-root',type=pathlib.Path,required=True)
p.add_argument('--image',default='radio-v2x-worker-runtime:local')
a=p.parse_args(); root=a.runtime_root.resolve()
scenarios=('start-stop','factory-restart','move-roundtrip','move-coexist','selected','discovery','evolution','unknown','unavailable')
receipt={'started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'status':'RUNNING','runs':[],'scenarios':scenarios,'household':False}
out=root/'FINAL-WORKER-RUNTIME.json'
if out.exists(): raise SystemExit('refusing to replace previous final-suite evidence')
driver=pathlib.Path(__file__).resolve().parent/'run-worker.py'
for scenario in scenarios:
 cmd=[sys.executable,str(driver),'--runtime-root',str(root),'--composition-root',str(a.composition_root.resolve()),'--image',a.image,'--scenario',scenario]
 r=subprocess.run(cmd,text=True,capture_output=True)
 print(r.stdout+r.stderr,flush=True,end='')
 entry={'scenario':scenario,'exit_code':r.returncode,'output':r.stdout+r.stderr}
 last=r.stdout.strip().rsplit(' ',1)
 if len(last)==2 and pathlib.Path(last[0]).is_dir():
  path=pathlib.Path(last[0])/'receipt.json'; entry['receipt']=str(path); entry['receipt_sha256']=hashlib.sha256(path.read_bytes()).hexdigest()
 receipt['runs'].append(entry)
 if r.returncode:
  receipt['status']='FAIL'; out.write_text(json.dumps(receipt,indent=2)+'\n'); raise SystemExit(r.returncode)
 out.write_text(json.dumps(receipt,indent=2)+'\n')
receipt.update(status='PASS',completed_at=datetime.datetime.now(datetime.timezone.utc).isoformat())
out.write_text(json.dumps(receipt,indent=2)+'\n')
print(out,'PASS',flush=True)
