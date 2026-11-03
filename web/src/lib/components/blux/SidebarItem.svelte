<script lang="ts">
 import Icon from './Icon.svelte';import PinButton from './PinButton.svelte';import type {DeletionTarget} from '$lib/blux/deletion';import type {PinTarget} from '$lib/blux/pins';
 export let target:DeletionTarget;export let selected=false;export let running=false;export let disabled=false;export let pinnedAt='';export let pinPending=false;
 export let onSelect:()=>void=()=>{};export let onDelete:(target:DeletionTarget)=>void=()=>{};export let onPin:(target:PinTarget)=>void=()=>{};
</script>
<div class="sidebar-task-row">
 <button class="sidebar-task" class:collaboration-sidebar-task={target.kind==='collaboration'} class:active={selected} aria-current={selected?'page':undefined} title={target.title} {disabled} on:click={onSelect}>
  {#if target.kind==='collaboration'}<Icon name="team" size={15}/>{/if}<span class="sidebar-task-title">{target.title}</span>{#if running}<i class="sidebar-state-dot running"></i>{/if}
 </button>
 <div class="sidebar-item-actions"><PinButton kind={target.kind} id={target.id} title={target.title} pinned={!!pinnedAt} disabled={disabled||pinPending} {onPin}/><button class="ui-icon-button item-delete-button" aria-label={`删除${target.kind==='task'?'任务':'协作'}：${target.title}`} {disabled} on:click={()=>onDelete(target)}><Icon name="trash" size={14}/></button></div>
</div>
