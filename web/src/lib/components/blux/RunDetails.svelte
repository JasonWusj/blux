<script lang="ts">
 import {agentName} from '$lib/blux/agents';
  // Blux-specific execution facts beside the adapted Open WebUI chat.
  import ContextPanel from './ContextPanel.svelte';
  import type { BluxEvent, Health, RuntimeInfo, Task, TokenUsage, Turn } from '$lib/blux/types';
  import XMark from '$lib/upstream/open-webui/icons/XMark.svelte';
  import ChevronDown from '$lib/upstream/open-webui/icons/ChevronDown.svelte';
  import CheckCircle from '$lib/upstream/open-webui/icons/CheckCircle.svelte';
  import Spinner from '$lib/upstream/open-webui/common/Spinner.svelte';
  export let events: BluxEvent[] = [];
  export let health: Health | null = null;
  export let serviceOffline = false;
  export let task: Task | null = null;
  export let runtime: RuntimeInfo | undefined = undefined;
  export let turns: Turn[] = [];
  export let streamState = 'idle';
  export let mobile = false;
  export let onClose: () => void = () => {};

  const eventLabels: Record<string, string> = {
    'task.created': '任务已创建', 'turn.queued': '进入执行队列', 'turn.started': '开始执行',
    'workspace.prepared': '工作区已准备', 'agent.session.ready': '会话已就绪',
    'agent.turn.started': 'Agent 开始执行', 'message.delta': '生成回复',
    'message.snapshot': '回复已同步', 'tool.started': '调用工具', 'tool.output': '工具输出',
    'tool.completed': '工具执行完成', 'file.changed': '文件已变更',
    'usage.updated': '用量已更新', 'agent.warning': 'Agent 提示',
    'turn.completed': '本轮执行完成', 'turn.failed': '本轮执行失败'
  };
  const statusLabels: Record<string, string> = {
    queued: '排队中', running: '执行中', completed: '已完成', failed: '未完成'
  };
  const usageKeys: Array<keyof TokenUsage> = ['input_tokens', 'output_tokens', 'cached_input_tokens', 'reasoning_output_tokens', 'total_tokens'];
  $: usage = turns.reduce<TokenUsage>((total, turn) => {
    for (const key of usageKeys) total[key] += turn.usage?.[key] || 0;
    return total;
  }, { input_tokens: 0, output_tokens: 0, cached_input_tokens: 0, reasoning_output_tokens: 0, total_tokens: 0 });
  $: hasUsage = usageKeys.some((key) => usage[key] > 0);
  $: worktreePath = runtime?.worktree_path || runtime?.workspace_path || '';
  $: model = runtime?.model || task?.model || '';
  $: modelProvider = runtime?.model_provider || task?.model_provider || '';
  $: reasoningEffort=turns.at(-1)?.reasoning_effort||runtime?.reasoning_effort||'';
  $: baseCommit = runtime?.base_commit || task?.base_commit || '';
  $: cliVersion = runtime?.cli_version || (task?.adapter==='codex'?health?.codex?.version:'') || '';
  function eventTime(value: string) {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? '' : date.toLocaleTimeString('zh-CN', { hour12: false });
  }
  function count(value: number) { return value.toLocaleString('zh-CN'); }
</script>

{#if mobile}<button class="details-overlay" on:click={onClose} aria-label="关闭运行记录"></button>{/if}
<aside class:mobile-details={mobile} class="run-details" aria-label="运行记录">
  <header><h2>运行记录</h2><button class="ui-icon-button" on:click={onClose} aria-label="关闭运行记录"><XMark className="size-5" /></button></header>
  <div class="run-details-scroll">
    <section class="runtime-summary">
      <div><span>执行器</span><strong>{agentName(task?.adapter||'codex')}</strong></div>
      <div><span>Worker</span><strong>{serviceOffline ? '服务不可达' : health?.worker.online ? '在线' : '离线'}</strong></div>
      <div><span>状态</span><strong>{task ? statusLabels[task.status] || task.status : '未选择任务'}</strong></div>
    </section>
    {#if task}
      <details class="runtime-metadata">
        <summary>工作区<ChevronDown className="size-3" /></summary>
        <dl>
          {#if task?.repository_path}<dt>项目目录</dt><dd>{task.repository_path}</dd>{/if}
          {#if worktreePath}<dt>工作目录</dt><dd>{worktreePath}</dd>{/if}
          {#if runtime?.branch_name}<dt>分支</dt><dd>{runtime.branch_name}</dd>{/if}
          {#if baseCommit}<dt>基线提交</dt><dd title={baseCommit}>{baseCommit.slice(0, 12)}</dd>{/if}
          {#if model}<dt>模型</dt><dd>{model}</dd>{/if}
          {#if reasoningEffort}<dt>推理强度</dt><dd>{reasoningEffort}</dd>{/if}
          {#if modelProvider}<dt>提供商</dt><dd>{modelProvider}</dd>{/if}
          {#if cliVersion}<dt>CLI</dt><dd>{cliVersion}</dd>{/if}
        </dl>
        {#if !worktreePath}<p class="metadata-pending">工作区尚未准备完成。</p>{/if}
      </details>
      <details class="runtime-metadata">
        <summary>用量<span class="metadata-summary-value">{hasUsage ? `${count(usage.total_tokens)} tokens` : '暂无记录'}</span><ChevronDown className="size-3" /></summary>
        <dl class="usage-metadata">
          <dt>输入</dt><dd>{count(usage.input_tokens)}</dd>
          <dt>输出</dt><dd>{count(usage.output_tokens)}</dd>
          <dt>缓存输入</dt><dd>{count(usage.cached_input_tokens)}</dd>
          <dt>推理输出</dt><dd>{count(usage.reasoning_output_tokens)}</dd>
        </dl>
      </details>
    {/if}
    {#if task}<ContextPanel taskID={task.id} turnID={task.current_turn_id}/>{/if}
    <div class="events-section-heading"><h3>事件流</h3><span class:connected={streamState === 'live'}><i></i>{streamState === 'live' ? '实时连接' : streamState === 'reconnecting' ? '正在重连' : streamState === 'connecting' ? '连接中' : '待连接'}</span></div>
    {#if events.length === 0}
      <div class="events-placeholder"><Spinner className="size-5" /><p>等待执行事件</p></div>
    {:else}
      <div class="event-list">
        {#each events as event (event.id)}
          <details class:failed-event={event.type === 'turn.failed'} class="event-item">
            <summary><span class="event-timeline-node">{#if event.type.endsWith('completed')}<CheckCircle className="size-3.5" />{:else}<i></i>{/if}</span><span class="event-description"><strong>{eventLabels[event.type] || event.type}</strong></span><span class="event-time">{eventTime(event.created_at)}</span></summary>
            <div class="event-data"><span>{event.type} · #{event.seq} · 执行代次 {event.generation}</span><pre>{JSON.stringify(event.payload, null, 2)}</pre></div>
          </details>
        {/each}
      </div>
    {/if}
  </div>
</aside>
