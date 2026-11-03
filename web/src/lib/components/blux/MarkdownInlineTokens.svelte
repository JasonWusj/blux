<script lang="ts">
  // Adapted from Open WebUI v0.12.5 Messages/Markdown/MarkdownInlineTokens.svelte.
  // Keeps recursive inline rendering and safe-link selection; note, citation,
  // skill, file and HTML integrations are removed. Strings stay text nodes.
  import type { Token } from 'marked';
  import { unescapeHtml, safeLinkUrl } from './markdown';
  export let tokens: Token[] = [];
</script>

{#each tokens as token}
  {#if token.type === 'br'}<br />
  {:else if token.type === 'strong'}<strong><svelte:self tokens={token.tokens || []} /></strong>
  {:else if token.type === 'em'}<em><svelte:self tokens={token.tokens || []} /></em>
  {:else if token.type === 'del'}<del><svelte:self tokens={token.tokens || []} /></del>
  {:else if token.type === 'codespan'}<code>{unescapeHtml(token.text || '')}</code>
  {:else if token.type === 'link'}
    {@const href = safeLinkUrl(token.href || '')}
    {#if href}<a {href} target="_blank" rel="noreferrer noopener" title={token.title || undefined}><svelte:self tokens={token.tokens || []} /></a>{:else}<svelte:self tokens={token.tokens || []} />{/if}
  {:else if token.type === 'image'}<span class="markdown-image-label">[图片：{unescapeHtml(token.text || '未命名图片')}]</span>
  {:else if token.type === 'html'}<code class="literal-html">{token.raw || token.text || ''}</code>
  {:else if 'tokens' in token && token.tokens}<svelte:self tokens={token.tokens} />
  {:else}{unescapeHtml(('text' in token && token.text) || token.raw || '')}{/if}
{/each}
