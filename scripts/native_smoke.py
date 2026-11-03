#!/usr/bin/env python3
"""Verify installed Codex handshake and native sandbox without a model call.
Windows must be run on a Windows desktop; cross compilation is not this test.
"""
import json,pathlib,subprocess,tempfile,threading,queue,time,sys,os,shutil
with tempfile.TemporaryDirectory(prefix='blux-native-',dir=str(pathlib.Path.home())) as tmp:
 root=pathlib.Path(tmp).resolve();code=root/'code';artifacts=root/'artifacts';agent=root/'.agent'
 for d in (code,artifacts,agent):d.mkdir()
 binary=shutil.which(os.environ.get('BLUX_CODEX_BIN','codex'));assert binary,'Codex CLI not found'
 argv=[binary]
 if os.name=='nt' and pathlib.Path(binary).suffix.lower() in ('.cmd','.bat'):
  script=pathlib.Path(binary).parent/'node_modules/@openai/codex/bin/codex.js';node=shutil.which('node');assert node and script.exists(),'Unsupported Codex npm launcher';argv=[node,str(script)]
 p=subprocess.Popen(argv+['app-server','--listen','stdio://'],cwd=code,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,text=True)
 frames=queue.Queue()
 def read():
  for line in p.stdout:
   try:frames.put(json.loads(line))
   except json.JSONDecodeError:pass
  frames.put(None)
 threading.Thread(target=read,daemon=True).start()
 def rpc(i,method,params):
  p.stdin.write(json.dumps({'id':i,'method':method,'params':params})+'\n');p.stdin.flush();end=time.monotonic()+30
  while time.monotonic()<end:
   v=frames.get(timeout=max(.1,end-time.monotonic()))
   if v is None:raise AssertionError('app-server exited')
   if v.get('id')==i:
    assert 'error' not in v,v.get('error');return v['result']
  raise TimeoutError(method)
 try:
  rpc(1,'initialize',{'clientInfo':{'name':'blux-native-test','version':'0.7.0'}})
  p.stdin.write('{"method":"initialized"}\n');p.stdin.flush()
  v=rpc(2,'thread/start',{'cwd':str(code),'approvalPolicy':'never','sandbox':'workspace-write','ephemeral':True,'config':{'sandbox_workspace_write.network_access':True,'sandbox_workspace_write.writable_roots':[str(artifacts),str(agent)],'projects':{str(code):{'trust_level':'trusted'}}}})
  assert v['approvalPolicy']=='never' and v['sandbox']['type']=='workspaceWrite' and v['sandbox']['networkAccess']
  policy={'type':'workspaceWrite','writableRoots':[str(code),str(artifacts),str(agent)],'networkAccess':True}
  def write(i,path):return rpc(i,'command/exec',{'command':[sys.executable,'-c',"import pathlib,sys;pathlib.Path(sys.argv[1]).write_text('test')",str(path)],'cwd':str(code),'sandboxPolicy':policy,'timeoutMs':10000})
  allowed=write(3,code/'allowed.txt');assert allowed['exitCode']==0 and (code/'allowed.txt').exists(),allowed
  outside=write(4,root/'blocked.txt');assert outside['exitCode']!=0 and not(root/'blocked.txt').exists(),outside
  if os.name!='nt':
   (code/'link').symlink_to(root/'symlink-blocked.txt');escaped=write(5,code/'link');assert escaped['exitCode']!=0 and not(root/'symlink-blocked.txt').exists()
  print('PASS real app-server handshake, never approval, native workspace writes and outside-write denial')
 finally:
  p.stdin.close()
  try:p.wait(timeout=5)
  except subprocess.TimeoutExpired:p.kill();p.wait()
