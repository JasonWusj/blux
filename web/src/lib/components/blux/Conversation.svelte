<script lang="ts">
 import {agentName} from '$lib/blux/agents';
  // Adapted from Open WebUI v0.12.5 Messages/Message.svelte, UserMessage.svelte
  // and ResponseMessage.svelte: centered message column, user bubble, assistant
  // content and copy action. Blux's structured segments replace upstream history;
  // ratings, regeneration, sharing, audio and multi-model branches are removed.
  import { afterUpdate, onDestroy } from 'svelte';
  import type { AgentAdapter, Message, TaskDetail } from '$lib/blux/types';
  import Tooltip from '$lib/upstream/open-webui/common/Tooltip.svelte';
  import Spinner from '$lib/upstream/open-webui/common/Spinner.svelte';
  import Name from '$lib/upstream/open-webui/messages/Name.svelte';
  import Clipboard from '$lib/upstream/open-webui/icons/Clipboard.svelte';
  import Check from '$lib/upstream/open-webui/icons/Check.svelte';
  import Markdown from './Markdown.svelte';
  import ToolCallDisplay from './ToolCallDisplay.svelte';
  import { messageParts } from './segments';
  import FileChanges from './FileChanges.svelte';

  export let messages: Message[] = [];
  export let adapter: AgentAdapter = 'codex';
  export let turns: TaskDetail['turns'] = [];
  export let busy = false;
  export let queued = false;
  let container: HTMLDivElement;
  let autoScroll = true;
  let copiedId = '';
  let copyErrorId = '';
  let copyTimer: ReturnType<typeof setTimeout> | undefined;
  let lastMessages = '';
  $: failedWithoutMessage = turns.filter((turn) => turn.status === 'failed' && !messages.some((message) => message.turn_id === turn.id && message.role === 'assistant'));

  afterUpdate(() => {
    const nextMessages = JSON.stringify(messages);
    if (nextMessages === lastMessages) return;
    lastMessages = nextMessages;
    if (autoScroll && container) container.scrollTop = container.scrollHeight;
  });
  function scrollHandler() { autoScroll = container.scrollHeight - container.scrollTop - container.clientHeight < 90; }
  async function copyMessage(message: Message) {
    const text = message.segments.filter((segment) => segment.type === 'text').map((segment) => segment.text || '').join('');
    try { await navigator.clipboard.writeText(text); copiedId = message.id; copyErrorId = ''; }
    catch { copyErrorId = message.id; }
    clearTimeout(copyTimer); copyTimer = setTimeout(() => { copiedId = ''; copyErrorId = ''; }, 2_000);
  }
  onDestroy(() => clearTimeout(copyTimer));
</script>

<div class="conversation-scroll" bind:this={container} on:scroll={scrollHandler}><div class="conversation-messages" role="list" aria-label="对话记录">
  {#each messages as message (message.id)}
    {@const turn = turns.find((item) => item.id === message.turn_id)}
    <article class:user-message={message.role === 'user'} class:assistant-message={message.role !== 'user'} class="conversation-message" role="listitem">
      {#if message.role === 'user'}<div class="user-message-bubble">{message.segments.filter((segment) => segment.type === 'text').map((segment) => segment.text || '').join('')}</div>
      {:else}<div class="assistant-name"><Name>{agentName(adapter)}</Name></div><div class="assistant-content">{#each messageParts(message.segments) as part}{#if part.type === 'text'}<Markdown content={part.text} done={message.status !== 'streaming' && message.status !== 'queued' && message.status !== 'running'} />{:else if part.type === 'file_change'}<FileChanges changes={part.changes} />{:else}<ToolCallDisplay id={part.id} name={part.name} input={part.input} output={part.output} status={part.status} done={part.output !== undefined} />{/if}{/each}{#if message.segments.length === 0 && message.status === 'streaming'}<span class="assistant-thinking"><Spinner className="size-4" />正在准备回复…</span>{/if}{#if turn?.status === 'failed'}<div class="inline-error" role="alert"><strong>本轮未完成</strong><span>{turn.error || '执行意外中断。已有内容已保留，你可以重新发送消息继续。'}</span></div>{/if}</div><div class="message-actions"><Tooltip content={copiedId === message.id ? '已复制回复' : copyErrorId === message.id ? '复制失败，请手动选择文字' : '复制回复'} className="flex"><button class="ui-icon-button" on:click={() => copyMessage(message)} aria-label={copiedId === message.id ? '已复制回复' : '复制回复'}>{#if copiedId === message.id}<Check className="size-4" />{:else}<Clipboard className="size-4" />{/if}</button></Tooltip></div>{/if}
    </article>
  {/each}
  {#each failedWithoutMessage as turn (turn.id)}<div class="inline-error" role="alert"><strong>本轮未完成</strong><span>{turn.error || '执行意外中断。任务已保留，你可以重新发送消息继续。'}</span></div>{/each}
  {#if busy}<div class="conversation-progress" role="status"><Spinner className="size-3.5" /><span>{queued ? '等待执行…' : '正在执行…'}</span></div>{/if}
</div></div>
