<script lang="ts">
  // Adapted from Open WebUI v0.12.5 src/lib/components/chat/Messages/Markdown.svelte.
  // Upstream: f7ac7ee10962ddec0017f5c29b6740d9b9c5faca.
  // Retains marked.lexer and animation-frame coalescing during streaming. Blux
  // renders a recursive, inert token tree; user/model stores and embeds are removed.
  import { marked, type Token } from 'marked';
  import { onDestroy } from 'svelte';
  import MarkdownTokens from './MarkdownTokens.svelte';
  export let content = '';
  export let done = true;
  let tokens: Token[] = [];
  let pendingUpdate: number | null = null;
  let lastContent: string | null = null;

  function parseTokens() {
    pendingUpdate = null;
    if (content === lastContent) return;
    lastContent = content;
    tokens = marked.lexer(content || '', { gfm: true, breaks: true });
  }

  function updateHandler(nextContent: string, complete: boolean) {
    if (complete) {
      if (pendingUpdate !== null) cancelAnimationFrame(pendingUpdate);
      parseTokens();
    } else if (nextContent !== lastContent && pendingUpdate === null) {
      pendingUpdate = requestAnimationFrame(parseTokens);
    }
  }

  $: updateHandler(content, done);
  onDestroy(() => { if (pendingUpdate !== null) cancelAnimationFrame(pendingUpdate); });
</script>

<div class="markdown-body prose prose-neutral max-w-none dark:prose-invert"><MarkdownTokens {tokens} /></div>
