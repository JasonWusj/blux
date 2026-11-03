#!/usr/bin/env python3
"""Disposable delivery fixture. --real opts into three real Codex model tasks.
Approvals in this test affect only this script's temporary project and database.
"""
import argparse,json,pathlib,subprocess,tempfile,time,urllib.request
from smoke import Smoke,require,check
p=argparse.ArgumentParser();p.add_argument('--real',action='store_true');args=p.parse_args()
if not args.real:p.error("添加 --real 执行临时仓库中的真实 Codex 交付验证。")
with tempfile.TemporaryDirectory(prefix='blux-flow-',dir=str(pathlib.Path.home())) as tmp:
 root=pathlib.Path(tmp);repo=root/'project';repo.mkdir()
 def git(*a):return subprocess.check_output(['git','-c','user.name=Blux Smoke','-c','user.email=blux@localhost','-C',str(repo),*a],stderr=subprocess.DEVNULL).decode().strip()
 git('init');(repo/'README.md').write_text('Temporary acceptance fixture\n');git('add','.');git('commit','-m','initial');base=git('rev-parse','HEAD')
 binary=pathlib.Path('bin/blux.exe' if __import__('os').name=='nt' else 'bin/blux').resolve();s=Smoke(binary,root,180)
 try:
  s.start_api();s.start_worker();s.wait('health',s.health)
  payload={'goal':'Implement a tiny Python function add(a,b) in calculator.py and a unittest test_calculator.py for positive and negative integers. No dependencies or additional features. Keep specification and reports brief.','repository_path':str(repo),'adapter':'codex'}
  d=s.request('/deliveries',payload,'flow-create',expected=201);did=d['id']
  again=s.request('/deliveries',payload,'flow-create');require(again['id']==did,'duplicate delivery')
  def at_gate(phase):
   d=s.request('/deliveries/'+did)
   require(d['status'] not in ('blocked','awaiting_rework'),str(d['reason']))
   found=next(p for p in d['run']['phases'] if p['id']==phase)
   return (d,found) if found['status']=='awaiting_review' else None
  d,phase=s.wait('design review',lambda:at_gate('design'));require(not d['run']['phases'][1]['attempt']['units'][0]['task_id'],'unreviewed design advanced')
  unit=phase['attempt']['units'][0];spec=s.request('/tasks/'+unit['task_id']+'/snapshots')['snapshots'][-1];require(any(a['path']=='Spec.md' for a in spec['artifacts']),'missing Spec.md')
  require(git('rev-parse','HEAD')==base,'source changed early')
  s.stop(s.api);s.start_api();s.wait('API restart',s.health)
  s.request('/deliveries/'+did+'/actions',{'kind':'approve','phase_id':'design','generation':d['run']['generation'],'snapshot_ids':[u['snapshot_id'] for u in phase['attempt']['units']]},'test-human-design')
  check('design gate binds exact Spec version; API restart preserves worker')
  d,phase=s.wait('verification review',lambda:at_gate('verify'))
  require(git('rev-parse','HEAD')==base,'source changed before final acceptance')
  s.request('/deliveries/'+did+'/actions',{'kind':'approve','phase_id':'verify','generation':d['run']['generation'],'snapshot_ids':[u['snapshot_id'] for u in phase['attempt']['units']]},'test-human-verify')
  d=s.wait('delivery complete',lambda:(lambda d:d if d['status']=='completed' else None)(s.request('/deliveries/'+did)))
  if args.real:
   accepted=s.request('/deliveries/'+did+'/accept',{'generation':d['run']['generation']},'test-human-accept');require(accepted['status']=='accepted',str(accepted['reason']))
   subprocess.run(['python3','-m','unittest','-v'],cwd=repo,check=True)
   check('three independent real Codex sessions and final source acceptance')
  check('full delivery completes only after both human test gates')
 finally:s.close()
