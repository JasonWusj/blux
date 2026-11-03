<script lang="ts">
 import Select from './Select.svelte';
 import {onMount} from 'svelte';import {request} from '$lib/blux/api';
 export let project='';export let adapter='codex';export let contextIDs:string[]=[];export let onSelectProject:()=>void=()=>{};export let onTask:(id:string)=>void=()=>{};
 type Unit={id:string;name:string;task_id:string;snapshot_id:string;status:string};type Phase={id:string;name:string;status:string;gate:boolean;attempt:{number:number;units:Unit[];feedback:string}};
 type Delivery={id:string;goal:string;status:string;reason:string;adapter:string;run:{generation:number;phases:Phase[]};reworks:number;history:{generation:number;phases:Phase[]}[]};
 let list:Delivery[]=[];let selected='';let goal='';let feedback='';let error='';let pending=false;let key='';let fingerprint='';let alive=true;let initialized=false;let acceptKey='';let acceptFor='';
 const labels:Record<string,string>={pending:'待开始',queued:'排队中',running:'执行中',completed:'已完成',awaiting_review:'等待验收',accepted:'已通过',blocked:'已阻塞',awaiting_rework:'等待处理',failed:'未完成'};
 $: delivery=list.find(d=>d.id===selected);
 async function refresh(){try{const data=await request<{deliveries:Delivery[]}>('/deliveries');if(!alive)return;list=data.deliveries;if(!initialized){selected=list[0]?.id||'';initialized=true;}}catch(e){if(alive)error=String(e);}}
 onMount(()=>{alive=true;void refresh();const timer=setInterval(()=>void refresh(),1500);return()=>{alive=false;clearInterval(timer);};});
 async function create(){if(!goal.trim()||pending)return;if(adapter==='codex'&&!project){error='请选择项目文件夹。';return;}const body={goal:goal.trim(),repository_path:project,adapter,context_ids:contextIDs};const fp=JSON.stringify(body);if(fp!==fingerprint){fingerprint=fp;key=crypto.randomUUID();}pending=true;error='';try{const d=await request<Delivery>('/deliveries',{method:'POST',headers:{'Idempotency-Key':key},body:JSON.stringify(body)});selected=d.id;goal='';key='';fingerprint='';await refresh();}catch(e){error=e instanceof Error?e.message:'创建失败';}finally{pending=false;}}
 let actionRetry:{fp:string;key:string}|null=null;
 async function action(kind:string,phase?:Phase){if(!delivery||pending)return;const body={kind,phase_id:phase?.id||'',generation:delivery.run.generation,snapshot_ids:phase?.attempt.units.map(u=>u.snapshot_id)||[],feedback};const fp=JSON.stringify([delivery.id,body]);if(actionRetry?.fp!==fp)actionRetry={fp,key:crypto.randomUUID()};pending=true;error='';try{await request(`/deliveries/${delivery.id}/actions`,{method:'POST',headers:{'Idempotency-Key':actionRetry.key},body:JSON.stringify(body)});actionRetry=null;feedback='';await refresh();}catch(e){error=e instanceof Error?e.message:'操作失败';}finally{pending=false;}}
 async function accept(){if(!delivery||pending)return;pending=true;error='';const fingerprint=`${delivery.id}:${delivery.run.generation}`;if(acceptFor!==fingerprint||!acceptKey){acceptFor=fingerprint;acceptKey=crypto.randomUUID();}try{await request(`/deliveries/${delivery.id}/accept`,{method:'POST',headers:{'Idempotency-Key':acceptKey},body:JSON.stringify({generation:delivery.run.generation})});acceptKey='';await refresh();}catch(e){error=String(e);}finally{pending=false;}}
</script>
<div class="flow-panel">
 <div class="flow-toolbar"><Select ariaLabel="交付列表" bind:value={selected} options={[{value:'',label:'新交付'},...list.map(d=>({value:d.id,label:`${d.goal.slice(0,36)} · ${labels[d.status]||d.status}`}))]}/><button on:click={()=>selected=''}>新建</button></div>
 {#if error}<p role="alert" class="inline-error">{error}</p>{/if}
 {#if delivery}
  <p class="flow-goal">{delivery.goal}</p><p class="flow-meta">第 {delivery.run.generation} 代 · {labels[delivery.status]||delivery.status}</p>
  {#if delivery.reason}<p class="flow-reason">{delivery.reason}</p>{/if}
  {#each delivery.run.phases as p}<section class="flow-phase"><div class="flow-phase-title"><strong>{p.name}</strong><span>{labels[p.status]||p.status}</span></div>
   {#each p.attempt.units as u}<div class="flow-unit"><button disabled={!u.task_id} on:click={()=>onTask(u.task_id)}>{u.name}</button><span>{labels[u.status]||u.status}</span></div>{/each}
   <div class="flow-actions">{#if p.status==='awaiting_review'}<button disabled={pending} on:click={()=>void action('approve',p)}>验收通过</button><button disabled={pending} on:click={()=>void action('retry',p)}>返工</button>{:else if ['blocked','awaiting_rework'].includes(delivery.status)&&p.status==='running'}<button disabled={pending} on:click={()=>void action('retry',p)}>重新执行</button>{/if}{#if p.status==='accepted'&&delivery.status!=='accepted'}<button disabled={pending} on:click={()=>void action('rollback',p)}>回退到此阶段</button>{/if}</div>
  </section>{/each}
  {#if delivery.status==='blocked'}<button class="project-picker" disabled={pending} on:click={()=>void action('resume')}>已处理阻塞，继续整合</button>{/if}
  {#if delivery.status==='completed'&&delivery.adapter==='codex'}<button class="project-picker" disabled={pending} on:click={()=>void accept()}>接收到源项目</button>{/if}
  <details><summary>验收反馈</summary><textarea aria-label="验收反馈" bind:value={feedback} rows="3" placeholder="返工或回退时带给下一轮的反馈"></textarea></details>
  {#if delivery.history.length}<details><summary>历史执行 · {delivery.history.length} 代</summary>{#each delivery.history as run}<details><summary>第 {run.generation} 代 · 已失效</summary>{#each run.phases as p}{#each p.attempt.units as u}{#if u.task_id}<button on:click={()=>onTask(u.task_id)}>{p.name} / {u.name}</button>{/if}{/each}{/each}</details>{/each}</details>{/if}
 {:else}
  <textarea aria-label="交付目标" rows="4" bind:value={goal} placeholder="描述要完成的目标…"></textarea>
  <div class="flow-create"><button class="project-picker" on:click={onSelectProject}>{project?project.replace(/\\/g,'/').split('/').at(-1):'选择项目文件夹'}</button><span>Codex</span><button disabled={pending||!goal.trim()} on:click={()=>void create()}>开始交付</button></div>
  <p class="flow-meta">方案设计需验收 → 开发 → 验证需验收</p>
 {/if}
</div>
