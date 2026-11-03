<script lang="ts">
  // Adapted from Open WebUI v0.12.5 src/lib/components/chat/Messages/CodeBlock.svelte.
  // Preserves the language header, code body and copy interaction. Execution,
  // editor, downloads, Mermaid/Vega and Pyodide are removed in this stage.
  import { onDestroy } from 'svelte';
  import Tooltip from '$lib/upstream/open-webui/common/Tooltip.svelte';
  import Clipboard from '$lib/upstream/open-webui/icons/Clipboard.svelte';
  import Check from '$lib/upstream/open-webui/icons/Check.svelte';
  export let code = '';
  export let lang = '';
  export let id = '';
  let copied = false;
  let copyError = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  async function copyCode() {
    try { await navigator.clipboard.writeText(code); copied = true; copyError = false; }
    catch { copyError = true; }
    clearTimeout(timer); timer = setTimeout(() => { copied = false; copyError = false; }, 2_000);
  }
  onDestroy(() => clearTimeout(timer));
</script>

<div class="code-block" {id}><div class="code-block-header"><span>{lang.split(/\s/)[0] || '代码'}</span><Tooltip content={copied ? '已复制' : copyError ? '复制失败，请手动选择代码' : '复制代码'} className="flex"><button type="button" on:click={copyCode} aria-label={copied ? '代码已复制' : '复制代码'}>{#if copied}<Check className="size-3.5" />{:else}<Clipboard className="size-3.5" />{/if}<span>{copied ? '已复制' : copyError ? '复制失败' : '复制'}</span></button></Tooltip></div><pre><code>{code}</code></pre></div>
