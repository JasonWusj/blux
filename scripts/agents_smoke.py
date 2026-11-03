#!/usr/bin/env python3
"""Exercise new adapters through real worker processes with explicit JSONL CLI fixtures.
No models or installed agent configuration are accessed.
"""
import json,os,pathlib,subprocess,tempfile
from smoke import Smoke,require,check
FIXTURE='''#!/usr/bin/env python3
import sys,json,pathlib
kind=pathlib.Path(sys.argv[0]).name
if '--version' in sys.argv:print('0.99.0-protocol-fixture');sys.exit()
if '--help' in sys.argv:print('--json --session-id --permission-mode --approval-policy --add-dir --output-format stream-json --resume --sandbox --approval-mode --include-directories');sys.exit()
def arg(key):return sys.argv[sys.argv.index(key)+1] if key in sys.argv else ''
def emit(**v):print(json.dumps(v),flush=True)
text=sys.stdin.read();sid=arg('--session-id') or arg('--resume') or kind+'-native-session'
if kind=='dsh':
 assert arg('--permission-mode')=='workspace-write' and arg('--approval-policy')=='never'
 emit(type='session',sessionId=sid,cwd=str(pathlib.Path.cwd()))
else:
 assert arg('--approval-mode')=='yolo' and os.environ['GEMINI_SANDBOX']=='sandbox-exec' and not os.environ.get('SANDBOX')
 emit(type='init',session_id=sid,model='protocol-fixture-model')
out=pathlib.Path(arg('--add-dir') or arg('--include-directories'))/'Result.md'
out.write_text('# CLI protocol fixture\\nExplicit simulated output for '+kind+'\\n')
if kind=='dsh':
 emit(type='text',text='fixture complete');emit(type='final',sessionId=sid,reason={'kind':'completed'},text='fixture complete')
else:emit(type='message',role='assistant',content='fixture complete',delta=True);emit(type='result',status='success')
'''.replace('import sys,json,pathlib','import sys,json,pathlib,os')
with tempfile.TemporaryDirectory(prefix='blux-agents-',dir=pathlib.Path.home()) as tmp:
 root=pathlib.Path(tmp);repo=root/'repo';repo.mkdir();(repo/'README.md').write_text('Adapter fixture\n')
 def git(*a):return subprocess.check_output(['git','-c','user.name=Blux Fixture','-c','user.email=blux@localhost','-C',str(repo),*a],stderr=subprocess.DEVNULL).decode().strip()
 git('init');git('add','.');git('commit','-m','initial');base=git('rev-parse','HEAD')
 for name,var in [('dsh','BLUX_DEEPSEEK_BIN'),('gemini','BLUX_GEMINI_BIN')]:
  path=root/name;path.write_text(FIXTURE);path.chmod(0o700);os.environ[var]=str(path)
 s=Smoke(pathlib.Path('bin/blux').resolve(),root,25)
 try:
  s.start_api();s.start_worker();s.wait('health',s.health)
  agents=s.request('/agents')['agents'];require(all(next(a for a in agents if a['id']==id)['available'] for id in ('deepseek','gemini')),'adapters unavailable')
  c=s.request('/collaborations',{'title':'Protocol fixtures','goal':'Validate Agent dispatch and artifact sync','repository_path':str(repo)},'group',expected=201)
  ids=[]
  for adapter in ('deepseek','gemini'):
   payload={'adapter':adapter,'prompt':'Protocol fixture task','mode':'explore'}
   d=s.request('/collaborations/'+c['id']+'/tasks',payload,adapter,expected=201);tid=d['task']['id'];ids.append(tid)
   def finished():
    v=s.task(tid);require(v['task']['status']!='failed',str(v['turns']));return v if v['task']['status']=='completed' else None
   d=s.wait('Agent completion',finished);native=d['session']['runtime']['native_thread_id'];require(native,'missing native session')
   if adapter=='gemini':require(s.request('/tasks/'+tid+'/context')['input_artifacts'],'public inputs not pinned')
   again=s.request('/collaborations/'+c['id']+'/tasks',payload,adapter,replayed=True);require(again['task']['id']==tid,'duplicate dispatch')
   s.request('/tasks/'+tid+'/turns',{'prompt':'Continue protocol fixture task'},adapter+'-continue',expected=201)
   d=s.wait('continuation',finished);require(d['session']['runtime']['native_thread_id']==native,'native session changed')
  shared=s.request('/collaborations/'+c['id']+'/artifacts')['snapshots'];require(len(shared)==4 and {v['adapter'] for v in shared}=={'deepseek','gemini'},'public versions missing')
  require(git('rev-parse','HEAD')==base,'source changed')
  check('DeepSeek/Gemini protocol fixtures: availability, task dispatch, native session resume, fixed public inputs, same-name artifact versions and source isolation')
 finally:s.close()
