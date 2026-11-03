<script lang="ts">
 import {agentName} from '$lib/blux/agents';
 export let adapter='codex';
 export let compact=false;
 const icons:Record<string,string>={codex:'/agents/codex.svg',claude:'/agents/claude.svg',deepseek:'/agents/deepseek.svg',gemini:'/agents/gemini.png'};
 $: icon=icons[adapter];
 $: monochrome=adapter==='codex'||adapter==='deepseek';
</script>

<span class="agent-badge" class:compact>
 <span class="agent-glyph" data-agent={adapter} aria-hidden="true">
  {#if icon}
   {#if monochrome}<span class="agent-brand-mask" style={`--agent-icon:url('${icon}')`}></span>
   {:else}<img class="agent-brand-image" src={icon} alt="" width="20" height="20" draggable="false"/>{/if}
  {:else}<span>·</span>{/if}
 </span>
 {#if !compact}<span>{agentName(adapter)}</span>{/if}
</span>
