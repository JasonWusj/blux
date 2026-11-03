<script lang="ts">
  // Blux navigation built around Open WebUI's responsive sidebar primitive.
  import type { Task } from '$lib/blux/types';import type {Collaboration} from '$lib/blux/collaborations';
  import MobileSwipePanel from '$lib/upstream/open-webui/common/MobileSwipePanel.svelte';
  import Icon from './Icon.svelte';
  import ResizeHandle from './ResizeHandle.svelte';
  import SidebarItem from './SidebarItem.svelte';import {pinnedFirst,type PinTarget} from '$lib/blux/pins';
  export let onPin:(target:PinTarget)=>void=()=>{};export let pinning=new Set<string>();
  import type {DeletionTarget} from '$lib/blux/deletion';
  export let onDelete:(target:DeletionTarget)=>void=()=>{};
  export let width=248;export let maxWidth=400;export let onResize:(width:number)=>void=()=>{};export let onResizeEnd:()=>void=()=>{};
  export let open=true;export let mobile=false;export let tasks:Task[]=[];export let collaborations:Collaboration[]=[];export let selectedId:string|null=null;export let selectedCollaboration='';export let section='tasks';export let loading=false;export let error='';export let submitting=false;export let dark=false;
  export let onSelect:(id:string|null)=>void=()=>{};export let onRefresh:()=>void=()=>{};export let onToggleTheme:()=>void=()=>{};export let onManager:()=>void=()=>{};export let onDeliveries:()=>void=()=>{};export let onCollaboration:(id:string)=>void=()=>{};export let onCreateCollaboration:()=>void=()=>{};export let onProjects:()=>void=()=>{};export let onAutomations:()=>void=()=>{};export let automationUnread=0;
  let search='';let searchOpen=false;let more=false;
  $: personal=pinnedFirst(tasks.filter(t=>!t.collaboration_id&&!t.automation_id&&`${t.title} ${t.prompt}`.toLocaleLowerCase().includes(search.toLocaleLowerCase().trim())),t=>t.updated_at);
  $: pinnedTasks=personal.filter(t=>!!t.pinned_at);$: recentTasks=personal.filter(t=>!t.pinned_at);
  $: groups=pinnedFirst(collaborations.filter(c=>`${c.title} ${c.goal}`.toLocaleLowerCase().includes(search.toLocaleLowerCase().trim())),c=>c.created_at);
  $: pinnedGroups=groups.filter(c=>!!c.pinned_at);$: recentCollaborations=groups.filter(c=>!c.pinned_at).slice(0,4);
  function select(id:string|null){if(submitting)return;onSelect(id);if(mobile)open=false}
  function navigate(action:()=>void){action();if(mobile)open=false}
  function toggleSearch(){searchOpen=!searchOpen;if(searchOpen)setTimeout(()=>document.getElementById('task-search')?.focus(),0);else search=''}
</script>
<MobileSwipePanel {open} enabled={mobile} blocked={submitting} side="left" width={264} onOpenChange={(v)=>open=v} let:visible let:progress let:panelStyle let:backdropStyle>
 <div class:sidebar-desktop-slot={!mobile} class:collapsed={!mobile&&!open} class:sidebar-mobile-host={mobile}>
 {#if visible||!mobile}
  {#if mobile}<button class="sidebar-overlay" style={`${backdropStyle};pointer-events:${progress>0.01?'auto':'none'}`} tabindex={progress>0.01?0:-1} aria-hidden={progress<=0.01} on:click={()=>open=false} aria-label="关闭任务列表"></button>{/if}
  <aside id="blux-sidebar" class="workbench-sidebar reference-sidebar" class:mobile-panel={mobile} style={mobile?panelStyle:''} inert={mobile?progress<0.01:!open} aria-label="任务导航" data-swipe-panel="left">
   <div class="sidebar-brand-row"><button class="sidebar-brand" disabled={submitting} on:click={()=>select(null)}><img class="blux-brand-mark" src="/favicon.svg?v=4" alt="" width="28" height="28" draggable="false"/>Blux</button><div class="sidebar-top-actions"><button class="ui-icon-button" aria-label="搜索任务" class:pressed={searchOpen} on:click={toggleSearch}><Icon name="search" size={17}/></button><button class="ui-icon-button" aria-label="收起侧栏" on:click={()=>open=false}><Icon name="sidebar" size={17}/></button></div></div>
   {#if searchOpen}<div class="sidebar-search"><Icon name="search" size={15}/><input id="task-search" aria-label="搜索任务" type="search" bind:value={search} placeholder="搜索任务与协作"/></div>{/if}
   <div class="sidebar-sections-scroll">
    <section class="sidebar-section"><h2>Agent 协作</h2><div class="sidebar-section-entry" class:active={section==='collaborations'&&!selectedCollaboration}><button class="sidebar-section-link" aria-current={section==='collaborations'&&!selectedCollaboration?'page':undefined} on:click={()=>navigate(onDeliveries)}><Icon name="team"/>协作任务</button><button class="ui-icon-button" aria-label="新建协作" on:click={()=>navigate(onCreateCollaboration)}><Icon name="plus" size={18}/></button></div>
    {#if pinnedGroups.length}<h3>已置顶</h3>{#each pinnedGroups as c}<SidebarItem target={{kind:'collaboration',id:c.id,title:c.title}} pinnedAt={c.pinned_at||''} selected={section==='collaborations'&&selectedCollaboration===c.id} running={c.status==='running'} disabled={submitting} pinPending={pinning.has(`collaboration:${c.id}`)} onSelect={()=>navigate(()=>onCollaboration(c.id))} {onDelete} {onPin}/>{/each}{/if}
    {#if recentCollaborations.length}<h3>最近协作</h3>{#each recentCollaborations as c}<SidebarItem target={{kind:'collaboration',id:c.id,title:c.title}} selected={section==='collaborations'&&selectedCollaboration===c.id} running={c.status==='running'} disabled={submitting} pinPending={pinning.has(`collaboration:${c.id}`)} onSelect={()=>navigate(()=>onCollaboration(c.id))} {onDelete} {onPin}/>{/each}{/if}{#if groups.length-pinnedGroups.length>4}<button class="sidebar-show-more" on:click={()=>navigate(onDeliveries)}>查看全部协作</button>{/if}
    </section>
    <section class="sidebar-section personal-section"><h2>个人</h2><button class="sidebar-new" class:active={section==='tasks'&&!selectedId} disabled={submitting} on:click={()=>select(null)}><Icon name="chat"/>新建任务</button><button class="sidebar-new" class:active={section==='projects'} on:click={()=>navigate(onProjects)}><Icon name="folder"/>个人项目</button><button class="sidebar-new" on:click={()=>navigate(onManager)}><Icon name="spark"/>个人管家</button><button class="sidebar-new" class:active={section==='automations'} aria-current={section==='automations'?'page':undefined} on:click={()=>navigate(onAutomations)}><Icon name="clock"/>自动化{#if automationUnread>0}<span class="automation-badge" aria-label={`${automationUnread} 条未读`}>{automationUnread}</span>{/if}</button>
     {#if pinnedTasks.length}<h3>已置顶</h3><nav aria-label="置顶个人任务">{#each pinnedTasks as task}<SidebarItem target={{kind:'task',id:task.id,title:task.title}} pinnedAt={task.pinned_at||''} selected={section==='tasks'&&selectedId===task.id} disabled={submitting} pinPending={pinning.has(`task:${task.id}`)} onSelect={()=>select(task.id)} {onDelete} {onPin}/>{/each}</nav>{/if}
     <h3>最近任务</h3><nav aria-label="个人任务历史">{#if loading}<p class="sidebar-section-hint">读取中…</p>{/if}{#if error}<div class="sidebar-feedback" role="alert"><p>任务列表读取失败</p><button on:click={onRefresh}>重试</button></div>{/if}{#each (more?recentTasks:recentTasks.slice(0,10)) as task}<SidebarItem target={{kind:'task',id:task.id,title:task.title}} selected={section==='tasks'&&selectedId===task.id} disabled={submitting} pinPending={pinning.has(`task:${task.id}`)} onSelect={()=>select(task.id)} {onDelete} {onPin}/>{/each}{#if !loading&&!personal.length&&search}<p class="sidebar-section-hint">没有匹配的任务</p>{/if}{#if recentTasks.length>10}<button class="sidebar-show-more" on:click={()=>more=!more}>{more?'收起':'展开更多'}</button>{/if}</nav>
    </section>
   </div>
   <div class="sidebar-footer"><button class="ui-icon-button theme-toggle" aria-label={dark?'切换浅色':'切换深色'} on:click={onToggleTheme}><span aria-hidden="true">{dark?'☀':'◐'}</span></button></div>
   <ResizeHandle label="调整左侧导航宽度" value={width} min={208} max={maxWidth} enabled={!mobile&&open} resetValue={248} {onResize} onCommit={onResizeEnd}/>
  </aside>
 {/if}
 </div>
</MobileSwipePanel>
