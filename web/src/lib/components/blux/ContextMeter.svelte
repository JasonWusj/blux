<script lang="ts">
 import type {ContextUsage} from '$lib/blux/types';
 export let usage:ContextUsage|undefined=undefined;
 let panel:HTMLDivElement;let trigger:HTMLButtonElement;let position='';let open=false;
 const format=(n:number)=>new Intl.NumberFormat('zh-CN').format(n);
 $: known=!!usage; $: percent=usage&&usage.window_tokens?Math.round(usage.used_tokens/usage.window_tokens*100):null;
 $: label=usage?`已用上下文 ${format(usage.used_tokens)} tokens${percent===null?'':`，${percent}%`}`:'上下文未上报';
 function close(){panel?.hidePopover();open=false;}
 function toggle(){if(open){close();return;}const r=trigger.getBoundingClientRect();position=`right:${Math.max(12,innerWidth-r.right)}px;bottom:${innerHeight-r.top+8}px;`;panel.showPopover();open=true;}
 function outside(e:PointerEvent){if(open&&e.target instanceof Node&&!panel.contains(e.target)&&!trigger.contains(e.target))close();}
</script>
<svelte:window on:pointerdown={outside} on:resize={close} on:keydown={e=>{if(e.key==='Escape')close()}}/>
<button bind:this={trigger} type="button" class="context-meter" class:reported={known} class:context-full={percent!==null&&percent>=90} aria-label={label} title={label} aria-expanded={open} on:click={toggle}>
 <svg width="23" height="23" viewBox="0 0 28 28" aria-hidden="true"><circle class="context-ring-track" cx="14" cy="14" r="10"/><circle class="context-ring-value" cx="14" cy="14" r="10" pathLength="100" stroke-dasharray={`${percent===null?known?12:0:Math.min(100,percent)} 100`}/></svg>
 {#if percent!==null}<span>{percent}%</span>{/if}
</button>
<div bind:this={panel} popover="manual" class="context-meter-panel" style={position} role="status">
 <strong>上下文用量</strong>
 {#if usage}<p>{format(usage.used_tokens)}{#if usage.window_tokens} / {format(usage.window_tokens)}{/if} tokens</p>{#if usage.model}<p class="context-meter-note">{usage.model}</p>{/if}<p class="context-meter-note">CLI 最近一次模型请求的上报值{usage.window_tokens?'':' · 未上报容量'}</p>
 {:else}<p>尚未收到 CLI 的上下文用量。</p>{/if}
</div>
