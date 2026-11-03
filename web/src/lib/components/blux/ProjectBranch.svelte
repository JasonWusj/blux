<script lang="ts">
 import {onMount} from 'svelte';
 import {request} from '$lib/blux/api';
 export let project='';
 let mounted=false;let previous='';let branch='';let loading=true;let failed=false;let epoch=0;
 async function refresh(path:string){
  const ticket=++epoch;if(!path)return;
  try{const info=await request<{branch:string}>(`/projects/info?path=${encodeURIComponent(path)}`);if(ticket===epoch){branch=info.branch;failed=false;}}
  catch{if(ticket===epoch)failed=true;}
  finally{if(ticket===epoch)loading=false;}
 }
 $: if(mounted&&project!==previous){previous=project;branch='';failed=false;loading=true;void refresh(project);}
 onMount(()=>{mounted=true;const focus=()=>void refresh(project);const timer=window.setInterval(focus,15000);window.addEventListener('focus',focus);return()=>{++epoch;window.clearInterval(timer);window.removeEventListener('focus',focus);};});
 $: label=loading?'…':failed?'读取失败':branch||'暂无';
</script>
{#if project}<span class="project-branch" aria-label={`Git 分支：${label}`} title={failed?'无法读取项目的 Git 分支':`Git 分支：${label}`}><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><circle cx="6" cy="5" r="2.5"/><circle cx="6" cy="19" r="2.5"/><circle cx="18" cy="5" r="2.5"/><path d="M6 7.5v9M18 7.5v1a6 6 0 0 1-6 6H6"/></svg><span>{label}</span></span>{/if}
