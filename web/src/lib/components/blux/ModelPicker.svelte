<script lang="ts">
 import {onMount,onDestroy} from 'svelte';
 import type {AgentAdapter} from '$lib/blux/types';
 import Select from './Select.svelte';
 export let adapter:AgentAdapter='codex';export let project='';export let model='';export let provider='';export let effort='';export let currentModel='';export let currentEffort='';export let disabled=false;
 type Model={id:string;model:string;provider?:string;name:string;reasoning_efforts?:{id:string;description?:string}[];default_effort?:string};
 let catalog:{models:Model[];default_model?:string;default_effort?:string;reason?:string}={models:[]};let loading=false;let mounted=false;let previous='';let epoch=0;let controller:AbortController|undefined;let value='';
 $: scope=JSON.stringify([adapter,project]);
 $: if(mounted&&scope!==previous){previous=scope;model='';provider='';effort='';void load();}
 $: value=catalog.models.find(m=>m.model===model&&(m.provider||'')===provider)?.id||'';
 $: label=currentModel||catalog.default_model||'CLI 默认模型';
 $: options=[{value:'',label:loading?'读取模型…':label,description:currentModel?'保持当前模型':'跟随 CLI 配置'},...catalog.models.map(m=>({value:m.id,label:m.name||m.model}))];
 $: activeModel=catalog.models.find(m=>m.model===(model||currentModel||catalog.default_model)&&(!provider||m.provider===provider));
 $: modelChanged=!!model&&model!==(currentModel||catalog.default_model);
 $: inheritedEffort=modelChanged?activeModel?.default_effort:currentEffort||catalog.default_effort||activeModel?.default_effort;
 $: effortOptions=(activeModel?.reasoning_efforts||[]).map(e=>({value:e.id,label:e.id}));
 $: displayedEffort=effort||inheritedEffort||'';
 onMount(()=>{mounted=true});onDestroy(()=>controller?.abort());
 async function load(){controller?.abort();controller=new AbortController();const token=++epoch;loading=true;catalog={models:[]};try{const r=await fetch(`/api/v1/agents/${adapter}/models?project=${encodeURIComponent(project)}`,{signal:AbortSignal.any([controller.signal,AbortSignal.timeout(20000)])});const result=await r.json();if(!r.ok)throw Error();if(token===epoch)catalog=result;}catch{if(token===epoch)catalog={models:[],reason:'模型列表读取失败，点击重试。'};}finally{if(token===epoch)loading=false;}}
 function chooseEffort(id:string){effort=id;if(id&&activeModel){model=activeModel.model;provider=activeModel.provider||'';}}
 function choose(id:string){const item=catalog.models.find(m=>m.id===id);model=item?.model||'';provider=item?.provider||'';effort=item?.default_effort||'';}
</script>
<div class="composer-model-choice" title={catalog.reason||undefined}>
 <Select ariaLabel="选择模型" {value} {options} compact disabled={disabled||loading||!catalog.models.length} on:change={e=>choose(e.detail.value)}/>
 {#if activeModel?.reasoning_efforts?.length}<div class="composer-effort-choice" title="推理强度"><Select ariaLabel="推理强度" value={displayedEffort} on:change={e=>chooseEffort(e.detail.value)} options={effortOptions} compact {disabled}/></div>{/if}
 {#if catalog.reason&&!loading}<button type="button" class="model-retry ui-icon-button" aria-label="重新读取模型列表" title={catalog.reason} disabled={disabled} on:click={()=>void load()}>↻</button>{/if}
</div>
