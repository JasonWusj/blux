<script lang="ts">
  // Adapted from Open WebUI v0.12.5 MessageInput; keeps its original Textarea.
  import { tick } from 'svelte';
  import { request, errorText } from '$lib/blux/api';
  import { contextKindLabels, type ContextChoice } from '$lib/blux/context';
  import Textarea from '$lib/upstream/open-webui/common/Textarea.svelte';
  import Tooltip from '$lib/upstream/open-webui/common/Tooltip.svelte';
  import Spinner from '$lib/upstream/open-webui/common/Spinner.svelte';
  import Icon from './Icon.svelte';
  import XMark from '$lib/upstream/open-webui/icons/XMark.svelte';

  export let prompt = '';
  export let running=false;export let stopping=false;export let onStop:()=>void=()=>{};
  export let inputID = 'chat-input';
  export let showSend = true;
  export let showManageContext = true;
  export let menuOpen = false;
 export let spacious=false;
  export let project = '';
  export let contextRevision = 0;
  export let selectedContexts: ContextChoice[] = [];
  export let disabled = false;
  export let submitting = false;
  export let busy = false;
  export let steering = false;
  export let managed = false;
  export let onSubmit: () => void = () => {};
  export let onManageContext: () => void = () => {};

  let container: HTMLDivElement;
  let composing = false;
  let entries: ContextChoice[] = [];
  let loading = false;
  let loadError = '';
  let loadEpoch = 0;
  let dismissedPrompt: string | null = null;
  let highlighted = 0;
  let previousQuery = '';
  let previousProject = '';
  let wasOpen = false;
  let previousRevision=0;
  $: if(contextRevision!==previousRevision){previousRevision=contextRevision;dismissedPrompt=null;wasOpen=false;}
  $: query = prompt.startsWith('/') && !prompt.includes('\n') ? prompt.slice(1).toLocaleLowerCase().trim() : null;
  $: menuOpen = query !== null && prompt !== dismissedPrompt && !composing && !disabled && !submitting && !busy && !steering;
  $: choices = entries.filter(entry => !selectedContexts.some(selected => selected.id === entry.id)
    && `${entry.title} ${contextKindLabels[entry.kind]}`.toLocaleLowerCase().includes(query || ''));
  $: if (query !== previousQuery) { previousQuery = query || ''; highlighted = 0; }
  $: if (highlighted >= choices.length) highlighted = Math.max(0, choices.length - 1);
  $: if (project !== previousProject) {
    previousProject = project; entries = []; selectedContexts = []; loading=false;loadError='';dismissedPrompt=null;wasOpen=false;++loadEpoch;
  }
  $: if (menuOpen && !wasOpen) { wasOpen = true; void loadEntries(project); }
  $: if (!menuOpen) wasOpen = false;

  async function loadEntries(path: string) {
    const epoch = ++loadEpoch;
    loading = !!path; loadError = '';
    if (!path) { entries = []; return; }
    try {
      const data = await request<{ entries: ContextChoice[] }>(`/context?project=${encodeURIComponent(path)}`);
      if (epoch !== loadEpoch) return;
      const rank = { skill: 0, memory: 1, reference: 2 };
      entries = data.entries.map(({ id, title, kind }) => ({ id, title, kind }))
        .sort((a, b) => rank[a.kind] - rank[b.kind] || a.title.localeCompare(b.title, 'zh-CN'));
    } catch (error) { if (epoch === loadEpoch) loadError = errorText(error); }
    finally { if (epoch === loadEpoch) loading = false; }
  }

  function select(entry: ContextChoice) {
    selectedContexts = [...selectedContexts, entry]; prompt = ''; dismissedPrompt = null;
    document.getElementById(inputID)?.focus();
  }
  function closeMenu() { dismissedPrompt = prompt; }
  function outsideMenu(event: PointerEvent) {
    if (menuOpen && event.target instanceof Node && container && !container.contains(event.target)) closeMenu();
  }
  function submitHandler(event: SubmitEvent) {
    event.preventDefault();
    if (!menuOpen && !disabled && !busy && !submitting && prompt.trim()) onSubmit();
  }
  function keydownHandler(event: KeyboardEvent) {
    if (composing || event.isComposing) return;
    if (menuOpen) {
      if (event.key === 'Escape') { event.preventDefault(); closeMenu(); return; }
      if (['ArrowDown', 'ArrowUp'].includes(event.key)) {
        event.preventDefault();
        if (choices.length) { highlighted = (highlighted + (event.key === 'ArrowDown' ? 1 : -1) + choices.length) % choices.length; void tick().then(()=>document.getElementById(`slash-${choices[highlighted]?.id}`)?.scrollIntoView({block:'nearest'})); }
        return;
      }
      if (event.key === 'Enter' || event.key === 'Tab' && choices.length > 0) {
        event.preventDefault(); if (!loading && choices[highlighted]) select(choices[highlighted]); return;
      }
    }
    if (event.key === 'Enter' && !event.shiftKey && !event.altKey) {
      event.preventDefault(); if (!disabled && !busy && !submitting && prompt.trim()) onSubmit();
    }
  }
</script>

<svelte:window on:pointerdown={outsideMenu} />

<div class="composer-container" bind:this={container}>
  {#if menuOpen}
    <div class="slash-menu" aria-label="选择技能与资料">
      <header><span>技能与资料</span><button type="button" class="ui-icon-button" aria-label="关闭技能选择" on:click={closeMenu}><XMark className="size-3.5" /></button></header>
      <div class="slash-options" role="listbox" aria-label="技能与资料" aria-busy={loading}>
        {#if loading}<p class="slash-message">加载中…</p>
        {:else if loadError}<p class="slash-message" role="alert">{loadError}<button type="button" on:click={() => void loadEntries(project)}>重试</button></p>
        {:else if !project}<p class="slash-message">先为任务选择项目。</p>
        {:else if !choices.length}<p class="slash-message">{query ? '没有匹配的技能或资料' : '没有可选的技能或资料'}</p>
        {:else}
          {#each choices as entry, index (entry.id)}
            <button type="button" id={`slash-${entry.id}`} class="slash-option" class:highlighted={highlighted === index} role="option" aria-selected={highlighted === index}
              on:pointermove={() => highlighted = index} on:click={() => select(entry)}>
              <span class="slash-symbol" aria-hidden="true">{entry.kind === 'skill' ? '/' : entry.kind === 'memory' ? '◇' : '≡'}</span>
              <span class="slash-title">{entry.title}</span><span class="slash-kind">{contextKindLabels[entry.kind]}</span>
            </button>
          {/each}
        {/if}
      </div>
      <footer>{#if showManageContext}<button type="button" on:click={() => { closeMenu(); onManageContext(); }}>{project ? '管理项目资料' : '选择项目'}</button>{/if}<span>↑↓ 选择 · Enter 确认</span></footer>
    </div>
  {/if}
  <form class:spacious class="message-composer" on:submit={submitHandler} on:compositionstart={() => composing = true} on:compositionend={() => composing = false}>
    <div class="composer-body">
      {#if selectedContexts.length}
        <div class="composer-selections" aria-label="本轮选中的技能与资料">
          {#each selectedContexts as entry (entry.id)}
            <span class="context-chip"><span>{entry.title}</span><button type="button" disabled={submitting || busy} aria-label={`移除${entry.title}`}
              on:click={() => selectedContexts = selectedContexts.filter(item => item.id !== entry.id)}><XMark className="size-3" /></button></span>
          {/each}
        </div>
      {/if}
      <Textarea bind:value={prompt} id={inputID} ariaLabel="消息" placeholder={managed ? '通过交付阶段返工或回退继续' : busy ? '本轮结束后可继续' : steering ? '插话，调整当前任务…' : '描述任务，/ 选择技能与资料'} rows={1} minSize={spacious?86:42} maxSize={180} disabled={disabled || submitting || busy} onKeydown={keydownHandler} className="composer-textarea" />
    </div>
    <div class="composer-footer">
      <div class="composer-leading">
        {#if showManageContext}<button type="button" class="composer-add ui-icon-button" aria-label="添加资料" title="添加资料" disabled={disabled||submitting||busy||running} on:click={onManageContext}><Icon name="plus" size={23}/></button>{/if}
        <slot name="tools"/>
      </div>
      <div class="composer-trailing">
        <slot name="settings"/>
        {#if running}<button type="button" class="composer-stop" aria-label="停止任务" title="停止任务" disabled={stopping} on:click={onStop}>{#if stopping}<Spinner className="size-4"/>{:else}<span></span>{/if}</button>
        {/if}
        {#if showSend&&(!running||steering&&prompt.trim())}<Tooltip content={submitting ? '正在提交' : '发送 · Shift + Enter 换行'} className="flex">
          <button class="composer-send" type="submit" disabled={disabled || submitting || busy || menuOpen || !prompt.trim()} aria-label={steering ? '插话' : '发送消息'}>
            {#if submitting}<Spinner className="size-5" />{:else}<svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M12 19V5m-6 6 6-6 6 6" stroke-linecap="round" stroke-linejoin="round"/></svg>{/if}
          </button>
        </Tooltip>{/if}
      </div>
    </div>
  </form>
</div>
