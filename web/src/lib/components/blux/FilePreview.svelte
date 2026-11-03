<script lang="ts">
 import Icon from './Icon.svelte';import Markdown from './Markdown.svelte';
 export let filename='';export let content='';export let loading=false;export let error='';export let binary=false;export let truncated=false;export let diff=false;export let downloadURL='';export let previewURL='';export let preferReading=false;
 export let onRetry:()=>void=()=>{};
 let mode:'code'|'preview'='code';let previous='';let copied=false;let copyFailed=false;
 $: ext=filename.split('.').at(-1)?.toLowerCase()||'';
 $: isMarkdown=['md','markdown','mdx'].includes(ext);$: isImage=['png','jpg','jpeg','webp','gif','svg','avif'].includes(ext);$: isHTML=['html','htm'].includes(ext);
 $: identity=JSON.stringify([filename,preferReading,diff]);
 $: lines=content.split('\n');
 $: if(previous!==identity){previous=identity;mode=preferReading&&(isMarkdown||isHTML)?'preview':'code';copied=false;copyFailed=false;}
 $: basename=filename.split('/').at(-1)||'';
 $: directory=filename.includes('/')?filename.slice(0,filename.lastIndexOf('/')):'';
 async function copy(){try{await navigator.clipboard.writeText(content);copied=true;copyFailed=false;}catch{copyFailed=true;}}
</script>
<section class="workspace-file-preview" aria-label="文件预览">
 {#if filename}
  <header class="workspace-file-header"><div class="workspace-file-name" title={filename}><strong>{basename}</strong>{#if directory}<span>{directory}</span>{/if}</div><div class="workspace-file-actions">
   {#if !diff&&(isMarkdown||isHTML)}<button type="button" class="preview-mode" on:click={()=>mode=mode==='code'?'preview':'code'}>{mode==='code'?'预览':'源码'}</button>{/if}
   {#if !binary&&!isImage}<button class="ui-icon-button" aria-label={copied?'已复制文件内容':'复制文件内容'} title={copyFailed?'复制失败，请手动选择内容':copied?'已复制':'复制内容'} disabled={loading||!!error} on:click={()=>void copy()}><Icon name={copied?'check':'copy'} size={15}/></button>{/if}
   {#if downloadURL}<a class="ui-icon-button" href={downloadURL} download={basename} data-sveltekit-reload data-sveltekit-preload-data="off" aria-label="下载文件" title="下载文件"><Icon name="download" size={15}/></a>{/if}
  </div></header>
 {/if}
 <div class="workspace-preview-body" class:document-preview={!diff&&mode==='preview'&&isMarkdown}>
  {#if !filename}<div class="workspace-preview-empty"><Icon name="file" size={30}/><p>选择文件查看内容</p></div>
  {:else if loading}<div class="workspace-preview-empty"><p>正在读取…</p></div>
  {:else if error}<div class="workspace-preview-empty" role="alert"><p>{error}</p><button class="subtle-button" on:click={onRetry}>重试</button></div>
  {:else if isImage&&!diff&&previewURL}<div class="workspace-image-preview"><img src={previewURL} alt={basename}/></div>
  {:else if mode==='preview'&&isHTML&&!diff&&previewURL}<iframe title={filename} src={previewURL} sandbox="allow-scripts" class="workspace-html-preview"></iframe>
  {:else if binary}<div class="workspace-preview-empty"><Icon name="file" size={30}/><p>此文件暂不支持预览</p>{#if downloadURL}<a class="subtle-button" href={downloadURL} download={basename} data-sveltekit-reload data-sveltekit-preload-data="off">下载文件</a>{/if}</div>
  {:else if mode==='preview'&&isMarkdown&&!diff}<Markdown {content}/>
  {:else}<div class="workspace-code" class:workspace-diff={diff} aria-label={diff?'文件变更内容':'文件内容'}>{#each lines.slice(0,10000) as line,index}<div class="workspace-code-line" class:diff-add={diff&&line.startsWith('+')&&!line.startsWith('+++')} class:diff-delete={diff&&line.startsWith('-')&&!line.startsWith('---')} class:diff-hunk={diff&&line.startsWith('@@')}><span class="workspace-line-number" aria-hidden="true">{index+1}</span><code>{line||' '}</code></div>{/each}</div>{/if}
 </div>
 {#if truncated||lines.length>10000}<p class="workspace-preview-notice">{truncated?'仅显示前 1 MiB':'仅显示前 10,000 行'}，请下载查看完整文件。</p>{/if}
</section>
