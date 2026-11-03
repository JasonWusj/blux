<script context="module" lang="ts">
 export type Entry={name:string;path:string;kind:string;size?:number;change?:string;previous_path?:string;id?:string};
</script>
<script lang="ts">
 import Icon from './Icon.svelte';
 export let entries:Entry[]=[];export let depth=0;export let selected='';export let revision=0;
 export let loadDirectory:(path:string)=>Promise<Entry[]> = async()=>[];
 export let onSelect:(entry:Entry)=>void=()=>{};
 let expanded=new Set<string>();let children:Record<string,Entry[]>={};let loading=new Set<string>();let errors:Record<string,string>={};let previousRevision=revision;
 $: if(previousRevision!==revision){previousRevision=revision;for(const path of expanded)void load(path);}
 async function load(path:string){loading=new Set(loading).add(path);errors={...errors,[path]:''};try{children={...children,[path]:await loadDirectory(path)};}catch(e){errors={...errors,[path]:e instanceof Error?e.message:'读取失败'};}finally{loading=new Set([...loading].filter(p=>p!==path));}}
 function toggle(entry:Entry){if(expanded.has(entry.path)){expanded=new Set([...expanded].filter(p=>p!==entry.path));}else{expanded=new Set(expanded).add(entry.path);void load(entry.path);}}
 const badge=(name:string)=>{const ext=name.split('.').at(-1)?.toLowerCase();return ext==='md'?'MD':ext==='json'?'{}':ext==='py'?'PY':ext==='ts'||ext==='tsx'?'TS':ext==='js'||ext==='jsx'?'JS':''};
 const changeLabel:Record<string,string>={added:'A',modified:'M',deleted:'D',renamed:'R'};
</script>
<ul class="workspace-tree-list" role={depth===0?'tree':'group'} aria-label={depth===0?'文件列表':undefined}>
 {#each entries as entry(entry.path)}
  <li role="none">
   <button type="button" class="workspace-tree-item" class:selected={selected===entry.path} class:deleted={entry.change==='deleted'} style={`padding-left:${12+depth*14}px`} role="treeitem" aria-expanded={entry.kind==='directory'?expanded.has(entry.path):undefined} aria-selected={selected===entry.path} title={entry.path} disabled={entry.kind==='link'} on:click={()=>entry.kind==='directory'?toggle(entry):onSelect(entry)}>
    {#if entry.kind==='directory'}<span class="tree-chevron" class:expanded={expanded.has(entry.path)}><svg width="10" height="10" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6"><path d="m6 3 5 5-5 5"/></svg></span><Icon name="folder" size={15}/>
    {:else}<span class="tree-chevron"></span>{#if badge(entry.name)}<span class="tree-file-badge" data-kind={badge(entry.name)}>{badge(entry.name)}</span>{:else}<Icon name="file" size={15}/>{/if}{/if}
    <span class="tree-entry-name">{entry.name}</span>{#if entry.change}<span class="tree-change" data-change={entry.change}>{changeLabel[entry.change]||entry.change}</span>{/if}
   </button>
   {#if entry.kind==='directory'&&expanded.has(entry.path)}
    {#if loading.has(entry.path)&&!children[entry.path]}<p class="tree-folder-notice" style={`padding-left:${30+depth*14}px`}>读取中…</p>
    {:else if errors[entry.path]}<button class="tree-folder-notice" on:click={()=>void load(entry.path)}>{errors[entry.path]} · 重试</button>
    {:else if children[entry.path]?.length}<svelte:self entries={children[entry.path]} depth={depth+1} {selected} {revision} {loadDirectory} {onSelect}/>
    {:else}<p class="tree-folder-notice" style={`padding-left:${30+depth*14}px`}>空文件夹</p>{/if}
   {/if}
  </li>
 {/each}
</ul>
