#!/usr/bin/env python3
"""Exercise owned background startup, stop and offline export in temporary data."""
import pathlib,subprocess,tempfile,socket,json,time,os,zipfile,urllib.request
binary=pathlib.Path('bin/blux.exe' if os.name=='nt' else 'bin/blux').resolve()
with tempfile.TemporaryDirectory(prefix='blux-service-') as tmp:
 data=pathlib.Path(tmp)/'data';backup=pathlib.Path(tmp)/'export.zip'
 with socket.socket() as listener:listener.bind(('127.0.0.1',0));port=listener.getsockname()[1]
 def run(*args):return subprocess.check_output([str(binary),*args,'--data-dir',str(data)],text=True)
 try:
  print(run('start','--addr',f'127.0.0.1:{port}','--web-dir',str(pathlib.Path('web/dist').resolve())).strip())
  status=json.loads(run('status'));assert status['server'] and status['worker']
  for _ in range(50):
   try:
    h=json.load(urllib.request.urlopen(f'http://127.0.0.1:{port}/api/v1/health'));assert h['worker']['online'];break
   except Exception:time.sleep(.1)
  else:raise AssertionError('health unavailable')
  print(run('stop').strip());status=json.loads(run('status'));assert not status['server'] and not status['worker']
  print(run('backup','--output',str(backup)).strip());z=zipfile.ZipFile(backup);assert 'data/blux.db' in z.namelist() and 'manifest.json' in z.namelist()
  print('PASS isolated start/status/stop and offline ZIP backup')
 finally:run('stop')
