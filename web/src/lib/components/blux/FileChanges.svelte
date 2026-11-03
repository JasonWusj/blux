<script lang="ts">
  // Blux's read-only view of persisted Codex file-change facts.
  import type { FileChange } from '$lib/blux/types';
  import ChevronDown from '$lib/upstream/open-webui/icons/ChevronDown.svelte';
  export let changes: FileChange[] = [];
  const kindLabels: Record<string, string> = {
    add: '新增', added: '新增', create: '新增',
    delete: '删除', deleted: '删除', remove: '删除',
    update: '修改', modify: '修改', modified: '修改',
    rename: '重命名', move: '移动'
  };
</script>

<div class="file-change-list">
  {#each changes as change (change.path)}
    <details class="file-change">
      <summary>
        <span class="file-change-kind">{kindLabels[change.kind] || change.kind || '变更'}</span>
        <span class="file-change-path" title={change.path}>{change.path}</span>
        <ChevronDown className="size-3" />
      </summary>
      {#if change.diff}
        <pre class="file-diff" aria-label={`${change.path} 的文件差异`}><code>{#each change.diff.split('\n') as line}<span class:diff-add={line.startsWith('+') && !line.startsWith('+++')} class:diff-delete={line.startsWith('-') && !line.startsWith('---')} class:diff-hunk={line.startsWith('@@')}>{line || ' '}</span>{/each}</code></pre>
      {:else}
        <p class="file-diff-empty">未提供此文件的差异内容。</p>
      {/if}
    </details>
  {/each}
</div>
