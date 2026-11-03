<script lang="ts">
 import {onMount} from 'svelte';import {request,errorText} from '$lib/blux/api';import type {DeletionTarget} from '$lib/blux/deletion';import Icon from './Icon.svelte';
 export let target:DeletionTarget;export let onCancel:()=>void=()=>{};export let onDeleted:(target:DeletionTarget,ids:string[])=>void=()=>{};
 let dialog:HTMLDialogElement;let pending=false;let error='';
 onMount(()=>dialog.showModal());
 async function remove(){if(pending)return;pending=true;error='';try{const result=await request<{task_ids:string[]}>(`/${target.kind==='task'?'tasks':'collaborations'}/${encodeURIComponent(target.id)}`,{method:'DELETE'});onDeleted(target,result.task_ids);}catch(e){error=errorText(e);pending=false;}}
</script>
<dialog bind:this={dialog} class="delete-dialog" aria-label={target.kind==='task'?'删除任务':'删除协作'} on:cancel={e=>{if(pending)e.preventDefault();else onCancel()}}>
 <header><h2>{target.kind==='task'?'删除任务':'删除协作'}</h2><button class="ui-icon-button" aria-label="关闭删除窗口" disabled={pending} on:click={onCancel}><Icon name="close" size={17}/></button></header>
 <p class="delete-target-name">{target.title}</p>
 <p>{target.kind==='collaboration'?'协作中的全部指派任务也会删除，执行中的任务会停止。':'任务会从列表中移除，执行中的任务会停止。'}源项目文件保留。</p>
 {#if error}<p class="inline-error" role="alert">{error}</p>{/if}
 <footer><button class="subtle-button" disabled={pending} on:click={onCancel}>取消</button><button class="primary-button danger-button" disabled={pending} on:click={()=>void remove()}>{pending?'删除中…':'删除'}</button></footer>
</dialog>
