<script lang="ts">
 import Icon from './Icon.svelte';import type {PinTarget} from '$lib/blux/pins';
 export let kind:'task'|'collaboration'='task';export let id='';export let title='';export let pinned=false;export let disabled=false;export let onPin:(target:PinTarget)=>void=()=>{};
 $: label=`${pinned?'取消置顶':'置顶'}${kind==='task'?'任务':'协作'}：${title}`;
</script>
<button type="button" class="ui-icon-button item-pin-button" class:pinned {disabled} aria-label={label} title={pinned?'取消置顶':'置顶'} aria-pressed={pinned} on:click={()=>onPin({kind,id,pinned:!pinned})}><Icon name="pin" size={15}/></button>
