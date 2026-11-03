<script lang="ts">
  import Select from './Select.svelte';
  import ProjectBranch from './ProjectBranch.svelte';
  import { agentOptions, type AgentInfo } from '$lib/blux/agents';
  import type { AgentAdapter } from '$lib/blux/types';
  import { folderName } from '$lib/blux/context';
  import ContextPanel from './ContextPanel.svelte';
  import XMark from '$lib/upstream/open-webui/icons/XMark.svelte';

  export let open = false;
  export let agents: AgentInfo[] = [];
  export let project = '';
  export let adapter: AgentAdapter = 'codex';
  export let locked = false;
  export let selecting = false;
  export let manager = false;
  export let error = '';
  export let onSelectProject: () => void = () => {};
  export let onDone: () => void = () => {};
  let dialog: HTMLDialogElement;

  $: if (dialog) {
    if (open && !dialog.open) dialog.showModal();
    else if (!open && dialog.open) dialog.close();
  }
</script>

<dialog bind:this={dialog} class="task-setup-dialog" aria-labelledby="task-setup-title"
  on:cancel={() => open = false} on:close={() => open = false}>
  <header><h2 id="task-setup-title">{locked ? '项目设置' : manager ? '管家设置' : '新任务'}</h2>
    <button type="button" class="ui-icon-button" aria-label="关闭任务设置" on:click={() => open = false}><XMark className="size-4" /></button>
  </header>
  <div class="task-setup-body">
    <div class="setup-project-row">
      <span>项目</span>
      {#if locked}<span class="setup-project-name">{folderName(project) || '未选择项目'}</span>
      {:else}<button type="button" class="setup-project-button" id="setup-project" aria-label={project ? '更换项目' : '选择项目文件夹'} on:click={onSelectProject} disabled={selecting}>
        {selecting ? '正在选择…' : folderName(project) || '选择项目文件夹'}
        <span aria-hidden="true">↗</span>
      </button>{/if}
    </div>
    {#if open&&project}<ProjectBranch {project}/>{/if}
    {#if !manager && !locked}
      <div class="setup-field"><label for="setup-adapter">执行器</label>
        <Select id="setup-adapter" ariaLabel="执行器" bind:value={adapter} options={agentOptions(agents)} disabled={locked}/>
      </div>
    {/if}
    {#if project}<ContextPanel {project} manageOnly />{/if}
    {#if error}<p class="inline-error" role="alert">{error}</p>{/if}
  </div>
  <footer><button type="button" class="setup-done" disabled={selecting || (!locked && !project)}
    on:click={() => { open = false; onDone(); }}>{locked ? '完成' : '继续'}</button></footer>
</dialog>
