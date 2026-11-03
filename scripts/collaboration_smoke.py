#!/usr/bin/env python3
"""Isolated shared-output scenario; --real opts into Codex + Claude model calls."""
import argparse,pathlib,subprocess,tempfile,json
from smoke import Smoke,require,check
p=argparse.ArgumentParser();p.add_argument('--real',action='store_true');p.add_argument('--real-codex',action='store_true');p.add_argument('--keep',action='store_true');args=p.parse_args()
if not(args.real or args.real_codex):p.error("请选择 --real（Claude + Codex）或 --real-codex（两个 Codex 会话）；本脚本调用真实模型。")
root=pathlib.Path(tempfile.mkdtemp(prefix='blux-collaboration-',dir=pathlib.Path.home()));repo=root/'project';repo.mkdir()
def git(*a):return subprocess.check_output(['git','-c','user.name=Blux Smoke','-c','user.email=blux@localhost','-C',str(repo),*a],stderr=subprocess.DEVNULL).decode().strip()
git('init');(repo/'README.md').write_text('Temporary collaboration fixture\n');git('add','.');git('commit','-m','initial');base=git('rev-parse','HEAD')
s=Smoke(pathlib.Path('bin/blux').resolve(),root,240)
try:
 s.start_api();s.start_worker();s.wait('health',s.health)
 payload={'title':'多 Agent 协作验证','goal':'先写方案，再依据公共产物实现一个加法函数。','repository_path':str(repo)}
 c=s.request('/collaborations',payload,'group',expected=201);cid=c['id'];require(s.request('/collaborations',payload,'group',replayed=True)['id']==cid,'group duplicated')
 def create(adapter,title,prompt,key):
  payload={'title':title,'prompt':prompt,'adapter':adapter,'mode':'explore'}
  d=s.request('/collaborations/'+cid+'/tasks',payload,key,expected=201);tid=d['task']['id']
  require(d['collaboration_id']==cid and d['task']['collaboration_id']==cid,'missing membership')
  require(s.request('/collaborations/'+cid+'/tasks',payload,key,replayed=True)['task']['id']==tid,'duplicate dispatch')
  def finished():
   v=s.task(tid)
   require(v['task']['status']!='failed','Agent failed: '+str([(t.get('error_code'),t.get('error')) for t in v['turns']]))
   return v if v['task']['status']=='completed' else None
  return s.wait('Agent task completion',finished)
 a=create('claude' if args.real else 'codex','编写方案','Save a brief Markdown plan for calculator.py containing def add(a,b): return a+b in the artifacts directory as Plan.md. Do not modify code or commit. Keep response short.','first')
 shared=s.request('/collaborations/'+cid+'/artifacts')['snapshots'];require(len(shared)==1 and shared[0]['artifacts'],'missing first shared outputs')
 check('first Agent output published with task and version provenance')
 b=create('codex','实现函数','Read the exact public inputs under .agent/inputs. Create calculator.py with def add(a,b): return a+b. Save a brief Result.md to the artifacts directory, mentioning the input plan. Do not commit or modify any other project. Keep response short.','second')
 shared=s.request('/collaborations/'+cid+'/artifacts')['snapshots'];require(len(shared)==2,'shared snapshots missing')
 require(len({v['task_id'] for v in shared})==2,'origin overwritten')
 require(s.request('/tasks/'+b['task']['id']+'/context')['input_artifacts'],'second Agent did not pin public artifacts')
 require(git('rev-parse','HEAD')==base and not (repo/'calculator.py').exists(),'source changed before acceptance')
 s.stop(s.api);s.start_api();s.wait('restart',s.health)
 require(len(s.request('/collaborations/'+cid+'/artifacts')['snapshots'])==2,'outputs lost on restart')
 require(s.request('/collaborations/'+cid)['status']=='completed','wrong collaboration status')
 if args.real or args.real_codex:
  require({v['adapter'] for v in shared}==({'claude','codex'} if args.real else {'codex'}),'wrong executors')
  require(pathlib.Path(b['session']['runtime']['worktree_path']).joinpath('calculator.py').exists(),'code missing')
 check('independent Agent tasks, exact version handoff, immutable public outputs and API restart')
 print(json.dumps({'fixture':str(root),'collaboration':cid,'tasks':[a['task']['id'],b['task']['id']]},ensure_ascii=False))
finally:
 s.close()
 if not args.keep:
  import shutil;shutil.rmtree(root)
