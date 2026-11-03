<script lang="ts">
  import ProjectBranch from '$lib/components/blux/ProjectBranch.svelte';
  import { pushState } from '$app/navigation';
  import { request, errorText } from '$lib/blux/api';
  import { onMount } from 'svelte';
  import { createWorkbench } from '$lib/blux/controller';
  import type { AgentAdapter } from '$lib/blux/types';
  import Collaborations from '$lib/components/blux/Collaborations.svelte';
  import Projects from '$lib/components/blux/Projects.svelte';
  import AgentBadge from '$lib/components/blux/AgentBadge.svelte';
  import Icon from '$lib/components/blux/Icon.svelte';
  import {agentOptions,type AgentInfo} from '$lib/blux/agents';
  import type {Collaboration} from '$lib/blux/collaborations';
  import Select from '$lib/components/blux/Select.svelte';
  import Sidebar from '$lib/components/blux/Sidebar.svelte';
  import MessageInput from '$lib/components/blux/MessageInput.svelte';
  import ModelPicker from '$lib/components/blux/ModelPicker.svelte';
  import ContextMeter from '$lib/components/blux/ContextMeter.svelte';
  import Conversation from '$lib/components/blux/Conversation.svelte';
  import FlowPanel from '$lib/components/blux/FlowPanel.svelte';
  import TaskSetup from '$lib/components/blux/TaskSetup.svelte';
  import { folderName, type ContextChoice } from '$lib/blux/context';
  import TaskWorkspace from '$lib/components/blux/TaskWorkspace.svelte';
  import DeleteDialog from '$lib/components/blux/DeleteDialog.svelte';
  import type {DeletionTarget} from '$lib/blux/deletion';
  import PinButton from '$lib/components/blux/PinButton.svelte';import type {PinTarget} from '$lib/blux/pins';
  import RunDetails from '$lib/components/blux/RunDetails.svelte';
  import Tooltip from '$lib/upstream/open-webui/common/Tooltip.svelte';
  import Spinner from '$lib/upstream/open-webui/common/Spinner.svelte';
  import SidebarIcon from '$lib/upstream/open-webui/icons/Sidebar.svelte';
  import CommandLine from '$lib/upstream/open-webui/icons/CommandLine.svelte';
  import ArrowPath from '$lib/upstream/open-webui/icons/ArrowPath.svelte';

  const controller = createWorkbench();
  const state = controller.state;
  let mobile = false;
  let sidebarOpen = true;
  let detailsOpen = false;
  let workspaceOpen=false;let panelTaskID:string|null=null;let wide=false;
  let shell:HTMLDivElement;let shellWidth=1440;let sidebarWidth=248;let workspaceWidth=0;
  $: mainWidth=Math.max(0,shellWidth-(!mobile&&sidebarOpen?sidebarWidth:0));
  $: workspaceMax=Math.max(360,wide?mainWidth-380:shellWidth-36);
  $: defaultWorkspaceWidth=wide?mainWidth/2:Math.min(720,shellWidth-36);
  $: actualWorkspaceWidth=Math.min(workspaceMax,Math.max(360,workspaceWidth||defaultWorkspaceWidth));
  $: sidebarMax=Math.max(208,Math.min(400,shellWidth-(workspaceOpen&&wide?740:380)));
  $: taskWorkspaceVisible=workspaceOpen&&!!$state.selectedId&&!!$state.detail&&!collaborationView&&!projectsView&&!flowView;
  function saveWidths(){try{localStorage.setItem('blux-panel-widths',JSON.stringify({sidebar:sidebarWidth,workspace:workspaceWidth}));}catch{/* Keep session widths. */}}
  let dark = false;
  let deletionTarget:DeletionTarget|null=null;let deletedCollaborations=new Set<string>();let deletedTaskIDs=new Set<string>();
  let pinning=new Set<string>();let pinError='';let collaborationEpoch=0;
  async function togglePin(target:PinTarget){const key=`${target.kind}:${target.id}`;if(pinning.has(key)||$state.submitting)return;pinning=new Set(pinning).add(key);pinError='';try{const value=await request<{pinned_at:string}>(`/${target.kind==='task'?'tasks':'collaborations'}/${target.id}/pin`,{method:'PUT',body:JSON.stringify({pinned:target.pinned})});++collaborationEpoch;if(target.kind==='task'){controller.updateTaskPin(target.id,value.pinned_at);collaborations=collaborations.map(c=>({...c,tasks:c.tasks.map(t=>t.id===target.id?{...t,pinned_at:value.pinned_at}:t)}));}else{collaborations=collaborations.map(c=>c.id===target.id?{...c,pinned_at:value.pinned_at}:c);}void refreshCollaborations();}catch(e){if(!deletedTaskIDs.has(target.id)&&!deletedCollaborations.has(target.id))pinError=errorText(e);}finally{pinning=new Set([...pinning].filter(k=>k!==key));}}
  function deleted(target:DeletionTarget,ids:string[]){const selected=!!$state.selectedId&&ids.includes($state.selectedId);const parent=$state.detail?.collaboration_id;deletedTaskIDs=new Set([...deletedTaskIDs,...ids]);controller.forgetTasks(ids);collaborations=collaborations.map(c=>({...c,tasks:c.tasks.filter(t=>!deletedTaskIDs.has(t.id))}));if(target.kind==='collaboration'){deletedCollaborations=new Set(deletedCollaborations).add(target.id);collaborations=collaborations.filter(c=>c.id!==target.id);if(selected||collaborationView&&selectedCollaboration===target.id)openCollaboration('');}else if(selected&&parent){openCollaboration(parent);}deletionTarget=null;void refreshCollaborations();}
  let prompt = '';
  let selectedModel='';let selectedProvider='';let selectedEffort='';let modelScope='';
  let promptTaskId: string | null = null;
  let localError = '';
  let newAdapter: AgentAdapter = 'codex';
 let flowView=false;let managerView=false;let collaborationView=false;let projectsView=false;
 let collaborations:Collaboration[]=[];let agents:AgentInfo[]=[];let selectedCollaboration='';let createCollaborationRequest=0;
 let collaborationError='';let collaborationsReady=false;let refreshingCollaborations=false;
 async function refreshCollaborations(){if(refreshingCollaborations)return;refreshingCollaborations=true;const ticket=collaborationEpoch;try{const data=await request<{collaborations:Collaboration[]}>('/collaborations');if(ticket!==collaborationEpoch)return;collaborations=data.collaborations.filter(c=>!deletedCollaborations.has(c.id)).map(c=>({...c,tasks:c.tasks.filter(t=>!deletedTaskIDs.has(t.id))}));collaborationError='';}catch(e){collaborationError=errorText(e);}finally{collaborationsReady=true;refreshingCollaborations=false;}}
 function writeView(view:string,id=''){const url=new URL(window.location.href);url.searchParams.delete('task');url.searchParams.delete('view');url.searchParams.delete('collaboration');if(view==='collaborations')url.searchParams.set('collaboration',id||'all');else if(view!=='tasks')url.searchParams.set('view',view);if(url.href!==window.location.href)pushState(url,{});}
 function openCollaboration(id=''){controller.selectTask(null,false);collaborationView=true;projectsView=false;flowView=false;managerView=false;setupOpen=false;selectedCollaboration=id;writeView('collaborations',id);void refreshCollaborations();}
 function openProjects(){controller.selectTask(null,false);collaborationView=false;projectsView=true;flowView=false;managerView=false;setupOpen=false;writeView('projects');}
 function openProject(path:string){repositoryPath=path;controller.selectTask(null,false);collaborationView=false;projectsView=false;flowView=false;managerView=false;setupOpen=false;writeView('tasks');prompt='';selectedContexts=[];}
 function navigationFromURL(){const params=new URLSearchParams(location.search);collaborationView=!params.has('task')&&params.has('collaboration');selectedCollaboration=collaborationView&&params.get('collaboration')!=='all'?params.get('collaboration')||'':'';projectsView=!params.has('task')&&params.get('view')==='projects';flowView=!params.has('task')&&params.get('view')==='flow';}

 const mode='explore';let selectedContexts:ContextChoice[]=[];
  $: contextIDs=selectedContexts.map(entry=>entry.id);
  let repositoryPath = '';
  let configReady = false;
  let setupOpen = false;let contextRevision=0;
  let selectingProject = false;
  let controlling = false;
  let controlRetry: {fingerprint:string;key:string}|null = null;
  async function control(kind: 'stop'|'steer') {
    if (!currentTask || controlling) return;
    const task=currentTask;
    const text=kind==='steer'?prompt.trim():'';
    const fingerprint=JSON.stringify([task.id,task.current_turn_id,kind,text]);
    if (controlRetry?.fingerprint!==fingerprint) controlRetry={fingerprint,key:crypto.randomUUID()};
    const key=controlRetry.key;controlling=true;localError='';
    try {
      let result=await request<{status:string;error?:string}>(`/tasks/${task.id}/controls`,{method:'POST',headers:{'Idempotency-Key':key},body:JSON.stringify({turn_id:task.current_turn_id,kind,prompt:text})});
      for(let i=0;i<40&&['pending','claimed'].includes(result.status);i++){
        await new Promise(resolve=>setTimeout(resolve,200));
        result=await request(`/tasks/${task.id}/controls/${key}`);
      }
      if(result.status==='rejected') throw new Error(result.error||'操作未执行。');
      if(result.status!=='applied') throw new Error('操作仍在处理中，可重试查看结果。');
      if(kind==='steer') prompt='';controlRetry=null;controller.refresh();
    }catch(error){localError=error instanceof Error?error.message:'操作失败。'}finally{controlling=false;}
  }
  async function selectProject() {
    if (selectingProject) return;
    selectingProject = true; localError = '';
    try {
      const response = await fetch('/api/v1/projects/select', { method: 'POST', signal: AbortSignal.timeout(125000) });
      const result = await response.json();
      if (!response.ok) throw new Error(result.error?.message || '无法打开项目选择窗口。');
      if (!result.canceled && typeof result.path === 'string') repositoryPath = result.path;
    } catch (error) { localError = error instanceof Error ? error.message : '项目选择失败。'; }
    finally { selectingProject = false; }
  }
  $: if ($state.selectedId !== promptTaskId) {
    promptTaskId = $state.selectedId;
    prompt = '';
    selectedContexts=[];
    localError = '';
  }
  $: if($state.selectedId!==panelTaskID){panelTaskID=$state.selectedId;workspaceOpen=!!panelTaskID&&wide;detailsOpen=false;}
  $: currentTask = $state.detail?.task || $state.tasks.find((task) => task.id === $state.selectedId) || null;
  $: busy = currentTask?.status === 'running' || currentTask?.status === 'queued';
  $: currentProjectPath=currentTask?.repository_path || (!$state.selectedId?repositoryPath:'');
  $: currentProjectName=folderName(currentProjectPath);
  $: adapter = currentTask?.adapter || (managerView?'codex':newAdapter);
  $: if(modelScope!==`${$state.selectedId||''}:${adapter}`){modelScope=`${$state.selectedId||''}:${adapter}`;selectedModel='';selectedProvider='';selectedEffort='';}
  $: currentModel=$state.detail?.turns.at(-1)?.model||$state.detail?.session.runtime?.model||'';
 $: currentEffort=$state.detail?.turns.at(-1)?.reasoning_effort||$state.detail?.session.runtime?.reasoning_effort||'';
 $: activeSection=collaborationView||flowView?'collaborations':projectsView?'projects':'tasks';
  $: if (configReady) {
    try { localStorage.setItem('blux-task-config', JSON.stringify({ adapter: newAdapter, repository_path: repositoryPath })); } catch { /* Keep the selection for this visit. */ }
  }
  onMount(() => {
    const cleanup = controller.start();navigationFromURL();void refreshCollaborations();void request<{agents:AgentInfo[]}>('/agents').then(v=>agents=v.agents).catch(()=>{});
    const collaborationTimer=setInterval(()=>void refreshCollaborations(),5000);window.addEventListener('popstate',navigationFromURL);
    const wideQuery=window.matchMedia('(min-width: 1180px)');wide=wideQuery.matches;const onWide=(event:MediaQueryListEvent)=>wide=event.matches;wideQuery.addEventListener('change',onWide);
    const query = window.matchMedia('(max-width: 767px)');
    mobile = query.matches;
    sidebarOpen = !mobile;
    const onMediaChange = (event: MediaQueryListEvent) => {
      mobile = event.matches;
      sidebarOpen = !mobile;
    };
    query.addEventListener('change', onMediaChange);
    try { dark = localStorage.getItem('blux-theme') === 'dark'; } catch { dark = false; }
    try {
      const saved = JSON.parse(localStorage.getItem('blux-task-config') || 'null');
      if (agentOptions().some(option=>option.value===saved?.adapter)) newAdapter = saved.adapter;
      if (typeof saved?.repository_path === 'string') repositoryPath = saved.repository_path;
    } catch { /* Invalid preferences do not prevent starting a task. */ }
    configReady = true;
    try{const saved=JSON.parse(localStorage.getItem('blux-panel-widths')||'null');if(Number.isFinite(saved?.sidebar))sidebarWidth=Math.max(208,Math.min(400,saved.sidebar));if(Number.isFinite(saved?.workspace)&&saved.workspace>0)workspaceWidth=saved.workspace;}catch{/* Invalid preferences use defaults. */}
    const observer=new ResizeObserver(([entry])=>shellWidth=entry.contentRect.width);observer.observe(shell);
    document.documentElement.classList.toggle('dark', dark);
    return () => { observer.disconnect();clearInterval(collaborationTimer);window.removeEventListener('popstate',navigationFromURL);cleanup();wideQuery.removeEventListener('change',onWide); query.removeEventListener('change', onMediaChange); };
  });

  function toggleTheme() {
    dark = !dark;
    document.documentElement.classList.toggle('dark', dark);
    try { localStorage.setItem('blux-theme', dark ? 'dark' : 'light'); } catch { /* Session preference still applies. */ }
  }

  function selectTask(id: string | null) {
 flowView=false;managerView=false;collaborationView=false;projectsView=false;
    if ($state.submitting) return;
    if(id===null){controller.selectTask(null,false);writeView('tasks');}else controller.selectTask(id);
    localError = '';
    if (id === null) {
      setupOpen=true;prompt = '';selectedContexts=[];
      setTimeout(() => document.getElementById('chat-input')?.focus(), 0);
    }
  }

  async function submit() {
    if ($state.submitting || controlling) return;
    if(managerView&&!($state.detail?.manager)){
      if(!repositoryPath){localError='请选择项目文件夹。';return;}if(!prompt.trim())return;
      controlling=true;localError='';try{const fp=JSON.stringify(['manager',repositoryPath,prompt.trim(),contextIDs,selectedModel,selectedProvider,selectedEffort]);if(controlRetry?.fingerprint!==fp)controlRetry={fingerprint:fp,key:crypto.randomUUID()};const d=await request<{task:{id:string}}>('/manager/turns',{method:'POST',headers:{'Idempotency-Key':controlRetry.key},body:JSON.stringify({prompt:prompt.trim(),repository_path:repositoryPath,context_ids:contextIDs,model:selectedModel,model_provider:selectedProvider,reasoning_effort:selectedEffort})});controlRetry=null;controller.selectTask(d.task.id);prompt='';managerView=false;}catch(e){localError=e instanceof Error?e.message:'管家请求失败';}finally{controlling=false;}return;
    }
    if(busy){if(currentTask?.adapter==='codex'&&currentTask.status==='running'&&prompt.trim()) await control('steer');return;}
    if (!prompt.trim()) { localError = '请先填写消息。'; return; }
    if (Array.from(prompt.trim()).length > 16_000) {
      localError = '消息不能超过 16,000 个字符。';
      return;
    }
    if (!$state.selectedId && !repositoryPath.trim()) {
      setupOpen = true;
      setTimeout(() => document.getElementById('setup-project')?.focus(), 0);
      localError = '请选择本地项目文件夹。';
      return;
    }
    if (!$state.selectedId && newAdapter === 'codex' && $state.health?.codex?.available === false) {
      setupOpen = true;
      localError = $state.health.codex.error || 'Codex CLI 不可用，请先配置 CLI。';
      return;
    }
    const chosenAgent=agents.find(agent=>agent.id===newAdapter);
    if(!$state.selectedId&&chosenAgent?.available===false){setupOpen=true;localError=chosenAgent.reason||'所选 Agent 当前不可用。';return;}
    localError = '';
    if (await controller.submit(prompt.trim(), $state.selectedId, { adapter: newAdapter, repository_path: repositoryPath.trim(),mode,context_ids:contextIDs,model:selectedModel,model_provider:selectedProvider,reasoning_effort:selectedEffort })) {prompt = '';selectedContexts=[];selectedModel='';selectedProvider='';selectedEffort='';}
  }
</script>

<svelte:head>
  <title>Blux</title>
  <meta name="description" content="Blux 本地个人 Agent 工作台，基于 Open WebUI。" />
</svelte:head>

<div bind:this={shell} class="workbench-shell" class:sidebar-collapsed={!sidebarOpen} style={`--sidebar-width:${sidebarWidth}px;--workspace-width:${actualWorkspaceWidth}px;`}>
  <Sidebar bind:open={sidebarOpen} {mobile} width={sidebarWidth} maxWidth={sidebarMax} onResize={width=>sidebarWidth=width} onResizeEnd={saveWidths} onDelete={target=>deletionTarget=target} onPin={target=>void togglePin(target)} {pinning} tasks={$state.tasks}
    selectedId={$state.selectedId} loading={$state.tasksLoading}
    error={$state.tasksError} submitting={$state.submitting} onSelect={selectTask} {collaborations} selectedCollaboration={selectedCollaboration} section={activeSection} onCollaboration={openCollaboration} onCreateCollaboration={()=>{openCollaboration('');createCollaborationRequest++;}} onProjects={openProjects}
    onManager={()=>{openProject(repositoryPath);prompt='';localError='';managerView=true;}} onDeliveries={()=>openCollaboration('')} onRefresh={() => controller.refresh()} onToggleTheme={toggleTheme} {dark} />
  <main class="workbench-main" class:has-task-workspace={taskWorkspaceVisible}>
    {#if !mobile&&!sidebarOpen&&(collaborationView||projectsView)}<button class="workspace-sidebar-toggle ui-icon-button" class:list-toggle={projectsView||!selectedCollaboration} aria-label="打开侧栏" on:click={()=>sidebarOpen=true}><SidebarIcon className="size-5"/></button>{/if}
    <header class="chat-navbar" class:workspace-header-hidden={collaborationView||projectsView}>
      <div class="navbar-left">
        {#if !sidebarOpen || mobile}
          <Tooltip content="打开侧栏" className="flex">
            <button class="ui-icon-button" on:click={() => sidebarOpen = true} aria-label="打开侧栏">
              <SidebarIcon className="size-5" />
            </button>
          </Tooltip>
        {/if}
        <h1 class="navbar-task-title" title={currentTask?.title}>{collaborationView?'Agent 协作':projectsView?'个人项目':flowView?'流程交付':managerView?'管家':currentTask?.title || '新任务'}</h1>
        {#if !flowView&&!collaborationView&&!projectsView}
          {#if $state.detail?.collaboration_id}<button class="back-to-collaboration" on:click={()=>openCollaboration($state.detail!.collaboration_id!)}>返回协作</button>{/if}
          <button type="button" class="ui-icon-button task-settings-button" aria-label={!$state.selectedId&&!repositoryPath?'选择项目':'任务设置'} on:click={()=>setupOpen=true} disabled={$state.submitting||controlling}>
            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M4 7h16M4 17h16M8 4v6m8 4v6" stroke-linecap="round"/></svg>
          </button>
        {/if}
      </div>
      <div class="navbar-right">
        {#if $state.selectedId&&!flowView&&!collaborationView&&!projectsView}
          <button class="ui-icon-button" class:pressed={workspaceOpen} aria-label={workspaceOpen?'收起文件与产物':'查看文件与产物'} aria-expanded={workspaceOpen} on:click={()=>{workspaceOpen=!workspaceOpen;if(workspaceOpen)detailsOpen=false;}}><Icon name="sidebar" size={18}/></button>
          <Tooltip content={detailsOpen ? '收起运行记录' : '查看运行记录'} className="flex">
            <button class:pressed={detailsOpen} class="ui-icon-button"
              on:click={() => {detailsOpen = !detailsOpen;if(detailsOpen)workspaceOpen=false;}} aria-expanded={detailsOpen}
              aria-label={detailsOpen ? '收起运行记录' : '查看运行记录'}>
              <CommandLine className="size-5" />
            </button>
          </Tooltip>
          <button class="ui-icon-button task-delete-button" aria-label="删除当前任务" disabled={$state.submitting} on:click={()=>{if(currentTask)deletionTarget={kind:'task',id:currentTask.id,title:currentTask.title};}}><Icon name="trash" size={17}/></button>
          {#if currentTask}<PinButton kind="task" id={currentTask.id} title={currentTask.title} pinned={!!currentTask.pinned_at} disabled={$state.submitting||pinning.has(`task:${currentTask.id}`)} onPin={target=>void togglePin(target)}/>{/if}
        {/if}
      </div>
    </header>
    {#if $state.healthError}
      <div class="connection-banner" role="alert">
        <span>{$state.healthError}</span>
        <button on:click={() => controller.refresh()}><ArrowPath className="size-3.5" />重试</button>
      </div>
    {/if}
    {#if pinError&&!$state.healthError}<div class="connection-banner" role="alert"><span>{pinError}</span><button on:click={()=>pinError=''}>关闭</button></div>{/if}
    {#if collaborationView}<Collaborations loadError={collaborationError} ready={collaborationsReady} list={collaborations} selectedID={selectedCollaboration} project={repositoryPath} {agents} onSelect={openCollaboration} onTask={(id)=>selectTask(id)} onDelete={target=>deletionTarget=target} onPin={target=>void togglePin(target)} {pinning} onRefresh={()=>void refreshCollaborations()} createRequested={createCollaborationRequest}/>
    {:else if projectsView}<Projects tasks={$state.tasks} {collaborations} onSelect={openProject} onChoose={()=>{openProject('');setupOpen=true;}}/>
    {:else if flowView}<FlowPanel project={repositoryPath} adapter="codex" {contextIDs} onSelectProject={()=>void selectProject()} onTask={(id)=>selectTask(id)}/>{:else}
    <div class="chat-and-details">
      <section class="chat-workspace" class:home-workspace={!$state.selectedId&&!managerView} aria-label="任务会话">
        {#if $state.detailError}
          <div class="inline-error detail-error" role="alert">
            <span>{$state.detailError}</span>
            <button on:click={() => controller.refresh()}>重新加载</button>
          </div>
        {/if}
        {#if !$state.selectedId}
          <div class="home-welcome"><h2>{managerView?'你的个人管家':'让想法，成为产物。'}</h2></div>
        {:else if $state.detailLoading && !$state.detail}
          <div class="chat-loading"><Spinner className="size-4" /><span>读取中…</span></div>
        {:else if $state.detail}
          <Conversation messages={$state.detail.messages || []} turns={$state.detail.turns || []} adapter={$state.detail.task.adapter}
            {busy} queued={currentTask?.status === 'queued'} />
        {:else}
          <div class="chat-loading"><span>未能读取此任务。</span></div>
        {/if}
        <div class="chat-input-area" class:home-composer={!$state.selectedId}>
          {#if currentProjectName}
            <div class="composer-project" aria-label="当前项目">
              <svg width="14" height="14" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.3" aria-hidden="true"><path d="M2.5 5a1.5 1.5 0 0 1 1.5-1.5h4l2 2h6a1.5 1.5 0 0 1 1.5 1.5v8A1.5 1.5 0 0 1 16 16.5H4A1.5 1.5 0 0 1 2.5 15Z" stroke-linejoin="round"/></svg>
              <span>{currentProjectName}</span>
              <ProjectBranch project={currentProjectPath}/>
            </div>
          {/if}
          {#if localError || $state.submitError}
            <div class="inline-error" role="alert">{localError || $state.submitError}</div>
          {/if}
          <MessageInput running={busy} stopping={controlling} onStop={()=>void control('stop')} spacious={!$state.selectedId} bind:prompt {contextRevision} project={currentProjectPath} bind:selectedContexts onManageContext={()=>setupOpen=true} submitting={$state.submitting || controlling} busy={busy && !(currentTask?.adapter === 'codex' && currentTask.status === 'running')} steering={busy} managed={!!$state.detail?.delivery_id}
            disabled={!!$state.selectedId && (!$state.detail || !!$state.detail.delivery_id)} onSubmit={() => void submit()}>
            <svelte:fragment slot="tools">{#if !$state.selectedId}<div class="composer-agent-choice">{#if managerView}<AgentBadge adapter="codex" compact/><span>Codex 管家</span>{:else}<Select ariaLabel="执行 Agent" bind:value={newAdapter} options={agentOptions(agents)} compact/>{/if}</div>{/if}</svelte:fragment>
            <svelte:fragment slot="settings"><ModelPicker {adapter} project={currentProjectPath} bind:model={selectedModel} bind:provider={selectedProvider} bind:effort={selectedEffort} {currentModel} {currentEffort} disabled={busy||$state.submitting||controlling||!!$state.detail?.delivery_id}/><ContextMeter usage={$state.detail?.session.runtime?.context_usage}/></svelte:fragment>
          </MessageInput>
        </div>
      </section>
      {#if workspaceOpen&&$state.selectedId&&$state.detail}
        {#key $state.selectedId}<TaskWorkspace taskID={$state.selectedId} project={currentProjectPath} workspacePath={$state.detail.session.runtime?.worktree_path||''} revision={$state.detail.task.status==='completed'?$state.detail.task.last_event_seq:0} managed={!!$state.detail.delivery_id} {busy} width={actualWorkspaceWidth} maxWidth={workspaceMax} resetWidth={defaultWorkspaceWidth} resizable={!mobile} onResize={width=>workspaceWidth=width} onResizeEnd={saveWidths} onClose={()=>workspaceOpen=false}/>{/key}
      {/if}
      {#if detailsOpen && $state.selectedId}
        <RunDetails events={$state.events} health={$state.health}
          serviceOffline={!!$state.healthError} task={currentTask} runtime={$state.detail?.session.runtime} turns={$state.detail?.turns || []}
          streamState={$state.streamState} {mobile} onClose={() => detailsOpen = false} />
      {/if}
    </div>
    {/if}
  </main>
</div>

{#if deletionTarget}<DeleteDialog target={deletionTarget} onCancel={()=>deletionTarget=null} onDeleted={deleted}/>{/if}

<TaskSetup {agents} bind:open={setupOpen} project={currentProjectPath} bind:adapter={newAdapter} locked={!!$state.selectedId} manager={managerView||!!$state.detail?.manager}
  selecting={selectingProject} error={localError} onSelectProject={()=>void selectProject()} onDone={()=>{contextRevision++;document.getElementById('chat-input')?.focus();}}/>
