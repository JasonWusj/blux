<script lang="ts">
 import Select from './Select.svelte';
 import {request} from '$lib/blux/api';
 export let manageOnly=false;export let turnID='';export let project='';export let selectedIDs:string[]=[];export let taskID='';
 type Entry={id:string;kind:string;title:string;content:string;previous_id:string};
 let entries:Entry[]=[];let search='';let title='';let content='';let kind='memory';let previous='';let error='';let saving=false;let loadedProject='';let frozen:{mode:{name:string;version:number};entries:Entry[];input_artifacts?:{id:string;path:string;hash:string}[]}|null=null;
 $: if(taskID)void readFrozen(taskID,turnID);
 $: if(!taskID&&project!==loadedProject){loadedProject=project;selectedIDs=[];void load();}
 async function readFrozen(id:string,turn:string){try{frozen=await request(`/tasks/${id}/context${turn?`?turn=${encodeURIComponent(turn)}`:""}`);}catch(e){error=String(e);}}
 async function load(){if(!project){entries=[];return;}try{entries=(await request<{entries:Entry[]}>(`/context?project=${encodeURIComponent(project)}&q=${encodeURIComponent(search)}`)).entries;}catch(e){error=String(e);}}
 function edit(e:Entry){previous=e.id;title=e.title;content=e.content;kind=e.kind;}
 async function save(){saving=true;error='';try{const e=await request<Entry>('/context',{method:'POST',body:JSON.stringify({project_path:project,kind,title,content,previous_id:previous})});if(!manageOnly)selectedIDs=[...selectedIDs.filter(id=>id!==previous),e.id];previous='';title='';content='';await load();}catch(e){error=e instanceof Error?e.message:'保存失败';}finally{saving=false;}}
 async function importFile(){saving=true;error='';try{const r=await fetch('/api/v1/context/import',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({project_path:project,kind}),signal:AbortSignal.timeout(125000)});const e=await r.json();if(!r.ok)throw new Error(e.error?.message||'导入失败');if(!e.canceled){if(!manageOnly)selectedIDs=[...selectedIDs,e.id];await load();}}catch(e){error=String(e);}finally{saving=false;}}
</script>
<details class="context-panel"><summary>{taskID?'本次上下文':manageOnly?'管理项目资料':'资料、记忆与技能'}{#if !taskID&&selectedIDs.length} · {selectedIDs.length}{/if}</summary>
 {#if error}<p role="alert">{error}</p>{/if}
 {#if taskID}
  {#if frozen}{#each frozen.entries as e}<details><summary>{e.title} · {e.kind}</summary><pre>{e.content}</pre><small>版本 {e.id}</small></details>{/each}{#each frozen.input_artifacts||[] as a}<div><a href={`/api/v1/tasks/${taskID}/inputs/${a.id}/content`} download data-sveltekit-reload data-sveltekit-preload-data="off">{a.path}</a><small title={a.hash}> · {a.id}</small></div>{/each}{#if !frozen.entries.length}<p>未附加项目资料。</p>{/if}{/if}
 {:else if project}
  <div class="context-search"><input bind:value={search} aria-label="检索项目资料" placeholder="检索资料"/><button on:click={()=>void load()}>搜索</button></div>
  {#each entries as e}<div class="context-row"><label>{#if !manageOnly}<input type="checkbox" value={e.id} bind:group={selectedIDs}/>{/if}{e.title}</label><button on:click={()=>edit(e)}>编辑</button></div>{/each}
  <details><summary>{previous?'编辑资料版本':'添加资料'}</summary>
   <Select ariaLabel="资料类型" bind:value={kind} options={[{value:'memory',label:'记忆'},{value:'reference',label:'项目资料'},{value:'skill',label:'技能'}]}/><button disabled={saving} on:click={()=>void importFile()}>选择文件导入</button>
   <input aria-label="资料标题" bind:value={title} placeholder="标题"/>
   <textarea aria-label="资料内容" rows="4" bind:value={content} placeholder="内容"></textarea>
   <button disabled={saving||!title.trim()||!content.trim()} on:click={()=>void save()}>{saving?'处理中…':'保存版本'}</button>
  </details>
 {:else}<p>选择项目后可添加资料、记忆和技能。</p>{/if}
</details>
