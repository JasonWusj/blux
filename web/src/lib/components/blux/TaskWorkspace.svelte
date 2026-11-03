<script lang="ts">
 import {onMount,onDestroy} from 'svelte';import {request,errorText} from '$lib/blux/api';import {folderName} from '$lib/blux/context';
 import Icon from './Icon.svelte';import Select from './Select.svelte';import FileTree,{type Entry} from './FileTree.svelte';import FilePreview from './FilePreview.svelte';
 import ResizeHandle from './ResizeHandle.svelte';
 export let taskID='';export let project='';export let workspacePath='';export let busy=false;export let managed=false;export let revision=0;export let onClose:()=>void=()=>{};
 export let width=600;export let maxWidth=1000;export let resetWidth=600;export let resizable=true;export let onResize:(width:number)=>void=()=>{};export let onResizeEnd:()=>void=()=>{};
 let panel:HTMLElement;let panelWidth=600;let treeWidth=196;let treeOpen=true;
 $: treeMin=panelWidth<480?104:140;
 $: treeMax=Math.max(treeMin,Math.min(480,panelWidth-220));
 $: actualTreeWidth=Math.min(treeMax,Math.max(treeMin,treeWidth));
 function saveTreeWidth(){try{localStorage.setItem('blux-file-tree-width',String(treeWidth));}catch{/* Keep session width. */}}
 type Artifact=Entry&{id:string;hash:string;size:number};type Snapshot={id:string;turn_id:string;commit:string;diff:string;artifacts:Artifact[]};
 let tab:'artifacts'|'changes'|'files'='files';let snapshots:Snapshot[]=[];let version='latest';let fileVersion='live';let expanded=false;
 let entries:Entry[]=[];let selected:Entry|null=null;let content='';let binary=false;let truncated=false;let loading=false;let listing=false;let listError='';let previewError='';let notice='';let treeRevision=0;
 let alive=true;let treeEpoch=0;let viewEpoch=0;let snapshotEpoch=0;let scope='';let accepting=false;let acceptResult='';let acceptError='';let acceptance:{id:string;key:string}|null=null;
 $: snapshot=(tab==='files'?fileVersion:version)==='latest'?snapshots.at(-1):snapshots.find(s=>s.id===(tab==='files'?fileVersion:version));
 $: snapshotID=snapshot?.id||'';
 $: live=tab==='files'?fileVersion==='live':tab==='changes'&&version==='live';
 $: rootName=tab==='artifacts'?'产物':tab==='changes'?'变更':folderName(project)||'文件';
 $: versions=[{value:'latest',label:'最新版本'},...snapshots.map((s,i)=>({value:s.id,label:`第 ${i+1} 轮`}))];
 $: currentScope=JSON.stringify([taskID,tab,workspacePath,snapshotID,live,version,fileVersion]);
 $: if(currentScope!==scope){scope=currentScope;entries=[];selected=null;content='';previewError='';acceptError='';acceptResult='';++viewEpoch;void loadTree();}
 $: if(taskID){void loadSnapshots(taskID,revision);}
 $: fileURL=selected?`/tasks/${taskID}/file?path=${encodeURIComponent(selected.path)}${snapshotID&&!live?`&snapshot=${encodeURIComponent(snapshotID)}`:''}`:'';
 $: previewURL=selected?tab==='artifacts'?`/api/v1/previews/${snapshotID}/${selected.path.split('/').map(encodeURIComponent).join('/')}`:`/api/v1/tasks/${taskID}/previews/${live?'live':snapshotID}/${selected.path.split('/').map(encodeURIComponent).join('/')}`:'';
 $: downloadURL=selected&&selected.change!=='deleted'?tab==='artifacts'?`/api/v1/artifacts/${selected.id}/content`:`/api/v1${fileURL}&raw=1&download=1`:'';
 onMount(()=>{try{const saved=Number(localStorage.getItem('blux-file-tree-width'));if(saved>=104&&saved<=480)treeWidth=saved;}catch{/* Use default width. */}const observer=new ResizeObserver(([entry])=>panelWidth=entry.contentRect.width);observer.observe(panel);const timer=setInterval(()=>{if(busy&&live)void loadTree(true)},5000);return()=>{clearInterval(timer);observer.disconnect();}});
 onDestroy(()=>{alive=false;++treeEpoch;++viewEpoch;++snapshotEpoch;});
 async function loadSnapshots(id:string,_revision:number){const token=++snapshotEpoch;try{const result=await request<{snapshots:Snapshot[]}>(`/tasks/${id}/snapshots`);if(alive&&token===snapshotEpoch){snapshots=result.snapshots;}}catch(e){if(alive&&token===snapshotEpoch)listError=errorText(e);}}
 function artifactEntries(directory=''):Entry[]{const prefix=directory?directory+'/':'';const result=new Map<string,Entry>();for(const a of snapshot?.artifacts||[]){if(!a.path.startsWith(prefix))continue;const remaining=a.path.slice(prefix.length);const [name,...rest]=remaining.split('/');if(!name)continue;const path=prefix+name;result.set(path,rest.length?{name,path,kind:'directory'}:{...a,name,path:a.path,kind:'file'});}return [...result.values()].sort((a,b)=>(a.kind==='directory'?0:1)-(b.kind==='directory'?0:1)||a.name.localeCompare(b.name));}
 async function loadDirectory(path:string):Promise<Entry[]>{if(tab==='artifacts')return artifactEntries(path);const result=await request<{entries:Entry[];truncated?:boolean}>(`/tasks/${taskID}/files?path=${encodeURIComponent(path)}${!live&&snapshotID?`&snapshot=${snapshotID}`:''}`);return result.entries;}
 async function loadTree(preserve=false){const token=++treeEpoch;const selectedBefore=selected;listing=true;listError='';notice='';try{
  let values:Entry[]=[];let message='';
  if(tab==='artifacts'){values=artifactEntries();}
  else if(!live&&!snapshotID){values=[];}
  else{const result=await request<{entries:Entry[];pending?:boolean;truncated?:boolean}>(`/tasks/${taskID}/files?path=${tab==='changes'?'&scope=changes':''}${!live&&snapshotID?`&snapshot=${snapshotID}`:''}`);values=result.entries;if(result.pending)message='工作区尚未准备完成。';else if(result.truncated)message='目录较大，仅显示前 500 项。';}
  if(!alive||token!==treeEpoch)return;notice=message;entries=tab==='changes'?values.map(e=>({...e,name:e.path})):values;treeRevision++;
  if(preserve&&selectedBefore){void view(selectedBefore);}else if(!selected){const first=entries.find(e=>e.name.toLowerCase()==='readme.md')||entries.find(e=>e.kind==='file');if(first)void view(first);}
 }catch(e){if(alive&&token===treeEpoch)listError=errorText(e);}finally{if(alive&&token===treeEpoch)listing=false;}}
 async function view(entry:Entry){const token=++viewEpoch;const sourceTab=tab;const sourceSnapshot=snapshotID;const sourceLive=live;selected=entry;content='';binary=false;truncated=false;previewError='';loading=true;
  try{if(sourceTab==='artifacts'){
    if(entry.size&&entry.size>1024*1024){binary=true;return;}
    const response=await fetch(`/api/v1/artifacts/${entry.id}/content`,{signal:AbortSignal.timeout(12000)});if(!response.ok)throw Error('产物读取失败。');const buffer=await response.arrayBuffer();const bytes=new Uint8Array(buffer);let text='';let isBinary=bytes.includes(0);if(!isBinary){try{text=new TextDecoder('utf-8',{fatal:true}).decode(bytes);}catch{isBinary=true;}}if(alive&&token===viewEpoch){binary=isBinary;content=text;}
   }else{const path=`/tasks/${taskID}/file?path=${encodeURIComponent(entry.path)}${!sourceLive&&sourceSnapshot?`&snapshot=${sourceSnapshot}`:''}${sourceTab==='changes'?`&diff=1${entry.previous_path?`&previous_path=${encodeURIComponent(entry.previous_path)}`:''}`:''}`;const result=await request<{content:string;binary:boolean;truncated:boolean}>(path);if(alive&&token===viewEpoch){content=result.content;binary=result.binary;truncated=result.truncated;}}
  }catch(e){if(alive&&token===viewEpoch)previewError=errorText(e);}finally{if(alive&&token===viewEpoch)loading=false;}}
 function refresh(){void loadSnapshots(taskID,revision);void loadTree(true);}
 async function accept(){if(!snapshot||accepting)return;const id=snapshot.id;if(acceptance?.id!==id)acceptance={id,key:crypto.randomUUID()};accepting=true;acceptError='';try{const result=await request<{status:string;reason:string;integration_path:string}>(`/snapshots/${id}/accept`,{method:'POST',headers:{'Idempotency-Key':acceptance.key}});if(!alive)return;acceptResult=result.status==='accepted'?'已接收到源项目':`${result.reason} ${result.integration_path}`;if(result.status==='blocked')acceptance=null;}catch(e){if(alive)acceptError=errorText(e);}finally{if(alive)accepting=false;}}
</script>
<aside bind:this={panel} class="task-workspace-panel" class:workspace-expanded={expanded} aria-label="任务文件与产物">
 <ResizeHandle label="调整右侧工作区宽度" value={width} min={360} max={maxWidth} direction={-1} edge="left" enabled={resizable&&!expanded} resetValue={resetWidth} {onResize} onCommit={onResizeEnd}/>
 <header class="task-workspace-header"><nav aria-label="任务工作区">{#if !treeOpen}<button class="ui-icon-button" aria-label="显示文件列表" on:click={()=>treeOpen=true}><Icon name="sidebar" size={16}/></button>{/if}<button class:active={tab==='artifacts'} on:click={()=>tab='artifacts'}><Icon name="file" size={16}/>产物</button><button class:active={tab==='changes'} on:click={()=>tab='changes'}><Icon name="changes" size={16}/>变更</button><button class:active={tab==='files'} on:click={()=>tab='files'}><Icon name="folder" size={17}/>文件</button></nav><div><button class="ui-icon-button workspace-expand-button" aria-label={expanded?'退出展开':'展开工作区'} on:click={()=>expanded=!expanded}><Icon name={expanded?'collapse':'expand'} size={16}/></button><button class="ui-icon-button" aria-label="关闭文件与产物" on:click={onClose}><Icon name="sidebar" size={17}/></button></div></header>
 <div class="task-workspace-content" class:has-preview={!!selected}>
  {#if treeOpen}<section class="workspace-file-list" style={`width:${actualTreeWidth}px;`} aria-label={tab==='changes'?'变更列表':tab==='artifacts'?'产物列表':'项目文件列表'}>
   <ResizeHandle label="调整文件目录宽度" value={actualTreeWidth} min={treeMin} max={treeMax} resetValue={196} onResize={width=>treeWidth=width} onCommit={saveTreeWidth}/>
   <header><strong><button class="ui-icon-button" aria-label="收起文件列表" on:click={()=>treeOpen=false}><Icon name="sidebar" size={14}/></button><span title={rootName}>{rootName}</span></strong><button class="ui-icon-button" aria-label="刷新文件列表" on:click={refresh} disabled={listing}><Icon name="refresh" size={14}/></button></header>
   <div class="workspace-version">{#if tab==='files'}<Select ariaLabel="文件版本" bind:value={fileVersion} options={[{value:'live',label:'当前工作区'},...versions]} compact/>{:else if snapshots.length||tab==='changes'}<Select ariaLabel="产物版本" bind:value={version} options={tab==='changes'?[...versions,{value:'live',label:'当前工作区'}]:versions} disabled={accepting} compact/>{/if}</div>
   <div class="workspace-tree-scroll">
    {#if listError}<p class="workspace-list-notice" role="alert">{listError}<button on:click={()=>void loadTree()}>重试</button></p>
    {:else if listing&&!entries.length}<p class="workspace-list-notice">读取中…</p>
    {:else if !entries.length}<p class="workspace-list-notice">{notice||(tab==='artifacts'?'暂无产物':tab==='changes'?'暂无代码变更':'暂无文件')}</p>
    {:else}{#key scope}<FileTree {entries} selected={selected?.path||''} revision={treeRevision} {loadDirectory} onSelect={entry=>void view(entry)}/>{/key}{#if notice}<p class="workspace-list-notice">{notice}</p>{/if}{/if}
   </div>
   {#if tab==='changes'&&snapshot?.commit&&snapshot.diff&&!managed&&!live}<footer><button class="workspace-accept" disabled={busy||accepting||snapshot.id!==snapshots.at(-1)?.id} on:click={()=>void accept()}>{accepting?'正在整合…':'接收到源项目'}</button></footer>{/if}
  </section>{/if}
  <FilePreview filename={selected?.path||''} {content} {binary} {truncated} {loading} error={previewError} diff={tab==='changes'} {downloadURL} {previewURL} preferReading={tab==='artifacts'} onRetry={()=>{if(selected)void view(selected)}}/>
 </div>
 {#if acceptResult||acceptError}<p class="workspace-accept-result" role={acceptError?'alert':'status'}>{acceptError||acceptResult}</p>{/if}
</aside>
