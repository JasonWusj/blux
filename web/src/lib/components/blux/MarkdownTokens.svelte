<script lang="ts">
  // Adapted from Open WebUI v0.12.5 Messages/Markdown/MarkdownTokens.svelte.
  // Retains the recursive marked token dispatch for headings, lists, tables,
  // paragraphs, quotes, rules and fenced code. Backend tools, files, embedded
  // HTML and executable previews are omitted from the local first-stage renderer.
  import type { Token } from 'marked';
  import MarkdownInlineTokens from './MarkdownInlineTokens.svelte';
  import CodeBlock from './CodeBlock.svelte';
  import { unescapeHtml } from './markdown';
  export let tokens: Token[] = [];
  function headerComponent(depth: number) { return `h${Math.max(1, Math.min(6, depth))}`; }
</script>

{#each tokens.filter((token) => token.type !== 'space') as token, tokenIndex}
  {#if token.type === 'hr'}<hr />
  {:else if token.type === 'heading'}<svelte:element this={headerComponent(token.depth)}><MarkdownInlineTokens tokens={token.tokens || []} /></svelte:element>
  {:else if token.type === 'paragraph'}<p><MarkdownInlineTokens tokens={token.tokens || []} /></p>
  {:else if token.type === 'blockquote'}<blockquote><svelte:self tokens={token.tokens || []} /></blockquote>
  {:else if token.type === 'list'}
    <svelte:element this={token.ordered ? 'ol' : 'ul'} start={token.ordered ? token.start : undefined}>
      {#each token.items as item}<li class:task-list-item={item.task}>{#if item.task}<input type="checkbox" checked={item.checked} disabled aria-label={item.checked ? '已完成' : '未完成'} />{/if}<svelte:self tokens={item.tokens || []} /></li>{/each}
    </svelte:element>
  {:else if token.type === 'table'}
    <div class="markdown-table"><table><thead><tr>{#each token.header as cell, index}<th style:text-align={token.align[index] || 'left'}><MarkdownInlineTokens tokens={cell.tokens || []} /></th>{/each}</tr></thead><tbody>{#each token.rows as row}<tr>{#each row as cell, index}<td style:text-align={token.align[index] || 'left'}><MarkdownInlineTokens tokens={cell.tokens || []} /></td>{/each}</tr>{/each}</tbody></table></div>
  {:else if token.type === 'code'}<CodeBlock code={token.text || ''} lang={token.lang || ''} id={`code-${tokenIndex}`} />
  {:else if token.type === 'text'}{#if token.tokens}<MarkdownInlineTokens tokens={token.tokens} />{:else}{unescapeHtml(token.text || token.raw || '')}{/if}
  {:else if token.type === 'html'}<code class="literal-html">{token.raw || token.text || ''}</code>
  {:else}<MarkdownInlineTokens tokens={[token]} />{/if}
{/each}
