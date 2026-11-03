<script lang="ts">
  // Adapted from Open WebUI v0.12.5 src/lib/components/common/ToolCallDisplay.svelte.
  // Upstream: f7ac7ee10962ddec0017f5c29b6740d9b9c5faca.
  // Retains JSON unwrapping/formatting, tool state, expand/collapse, slide and
  // long-result preview. Files, iframe embeds and approval branches are removed.
  import { slide } from 'svelte/transition';
  import { quintOut } from 'svelte/easing';
  import Spinner from '$lib/upstream/open-webui/common/Spinner.svelte';
  import ChevronUp from '$lib/upstream/open-webui/icons/ChevronUp.svelte';
  import ChevronDown from '$lib/upstream/open-webui/icons/ChevronDown.svelte';
  import CheckCircle from '$lib/upstream/open-webui/icons/CheckCircle.svelte';
  import XMark from '$lib/upstream/open-webui/icons/XMark.svelte';
  import WrenchSolid from '$lib/upstream/open-webui/icons/WrenchSolid.svelte';

  export let id = '';
  export let name = '工具调用';
  export let input = '';
  export let output: string | undefined = undefined;
  export let done = false;
  export let status: 'running' | 'completed' | 'failed' | undefined = undefined;
  $: isDone = status ? status !== 'running' : done;
  $: isFailed = status === 'failed';
  let open = false;
  let expandedResult = false;
  const RESULT_PREVIEW_LIMIT = 10_000;

  $: if (!open) expandedResult = false;
  function parseJSONString(str: string): unknown {
    let value: unknown = str;
    while (typeof value === 'string') {
      try { value = JSON.parse(value); } catch { break; }
    }
    return value;
  }
  function formatJSONString(str: string): string {
    const parsed = parseJSONString(str);
    return parsed !== null && typeof parsed === 'object' ? JSON.stringify(parsed, null, 2) : String(parsed ?? '');
  }
  $: formattedOutput = formatJSONString(output ?? '');
  $: resultTruncated = formattedOutput.length > RESULT_PREVIEW_LIMIT && !expandedResult;
</script>

<div class="tool-call-display" {id}><button type="button" class="tool-call-trigger" on:click={() => open = !open} aria-expanded={open}><span class:complete={isDone && !isFailed} class:failed={isFailed} class="tool-call-status">{#if !isDone}<Spinner className="size-4" />{:else if isFailed}<XMark className="size-4" />{:else}<CheckCircle className="size-4" />{/if}</span><span class="tool-call-label">{#if isFailed}{name}执行失败{:else if isDone}查看{name}的结果{:else}正在执行{name}…{/if}</span><span class="tool-call-chevron">{#if open}<ChevronUp className="size-3" />{:else}<ChevronDown className="size-3" />{/if}</span></button>
  {#if open}<div transition:slide={{ duration: 180, easing: quintOut, axis: 'y' }}><div class="tool-call-body">{#if input}<div><h4><WrenchSolid className="size-3" />输入</h4><pre>{formatJSONString(input)}</pre></div>{/if}{#if output !== undefined}<div><h4>输出</h4><pre>{resultTruncated ? formattedOutput.slice(0, RESULT_PREVIEW_LIMIT) : formattedOutput}</pre>{#if resultTruncated}<button class="show-all-result" on:click={() => expandedResult = true}>显示全部（{formattedOutput.length.toLocaleString('zh-CN')} 个字符）</button>{/if}</div>{:else}<p class="tool-pending-copy">等待工具结果…</p>{/if}</div></div>{/if}
</div>
