<script lang="ts">
  import { createEventDispatcher, onMount, tick } from 'svelte';
  import AgentBadge from './AgentBadge.svelte';
  export let value = '';
  export let options: { value: string; label: string; disabled?: boolean; description?: string; reason?: string; agent?: string }[] = [];
  export let ariaLabel = '';
  export let id = '';
  export let disabled = false;
  export let compact = false;
  const dispatch = createEventDispatcher<{ change: { value: string } }>();
  let trigger: HTMLButtonElement;
  let menu: HTMLDivElement;
  let open = false;
  let active = 0;
  let uid = '';
  let position = '';
  let search = '';
  let searchedAt = 0;
  $: selected = options.find(option => option.value === value);
  $: if (disabled && open) close(false);
  onMount(() => {
    uid = `select-${crypto.randomUUID()}`;
    const scroll = (event: Event) => { if (open && event.target instanceof Node && !menu?.contains(event.target)) close(false); };
    window.addEventListener('scroll', scroll, true);
    return () => { window.removeEventListener('scroll', scroll, true); menu?.hidePopover(); };
  });
  function close(focus = true) { menu?.hidePopover(); open = false; if (focus) trigger?.focus(); }
  async function show() {
    if (disabled) return;
    const rect = trigger.getBoundingClientRect();
    const width = Math.min(Math.max(rect.width, compact ? 210 : 220), innerWidth - 24);
    const below = innerHeight - rect.bottom - 12;
    const placeBelow = below >= Math.min(240, options.length * 44 + 12);
    const height = Math.max(80,Math.min(320, placeBelow ? below : rect.top - 12));
    position = `left:${Math.max(12, Math.min(rect.left, innerWidth - width - 12))}px;width:${width}px;max-height:${height}px;${placeBelow ? `top:${rect.bottom + 7}px` : `bottom:${innerHeight - rect.top + 7}px`}`;
    active = options.findIndex(option => option.value === value && !option.disabled);
    if (active < 0) active = options.findIndex(option => !option.disabled);
    open = true; await tick(); menu.showPopover(); menu.focus(); reveal();
  }
  function reveal() { document.getElementById(`${uid}-${active}`)?.scrollIntoView({ block: 'nearest' }); }
  function choose(index: number) {
    const option = options[index]; if (!option || option.disabled) return;
    value = option.value; dispatch('change', { value }); close();
  }
  function keydown(event: KeyboardEvent) {
    if (event.isComposing) return;
    if (event.key === 'Escape' && open) { event.preventDefault(); event.stopPropagation(); close(); return; }
    if (event.key === 'Tab') { close(false); return; }
    if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
      event.preventDefault(); if (!open) { void show(); return; }
      const enabled = options.map((option, index) => option.disabled ? -1 : index).filter(index => index >= 0);
      if (!enabled.length) return;
      const current = enabled.indexOf(active);
      active = event.key === 'Home' ? enabled[0] : event.key === 'End' ? enabled.at(-1)! : enabled[(current + (event.key === 'ArrowDown' ? 1 : -1) + enabled.length) % enabled.length];
      void tick().then(reveal); return;
    }
    if (['Enter', ' '].includes(event.key)) { event.preventDefault(); if (open) choose(active); else void show(); return; }
    if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
      event.preventDefault(); search = Date.now() - searchedAt > 700 ? event.key : search + event.key; searchedAt = Date.now();
      const index = options.findIndex(option => !option.disabled && option.label.toLocaleLowerCase().startsWith(search.toLocaleLowerCase()));
      if (index >= 0) { if (!open) { value = options[index].value; dispatch('change', { value }); } else { active = index; void tick().then(reveal); } }
    }
  }
  function outside(event: PointerEvent) { if (open && event.target instanceof Node && !menu.contains(event.target) && !trigger.contains(event.target)) close(false); }
</script>
<svelte:window on:pointerdown={outside} on:resize={()=>close(false)} on:keydown={(e)=>{if(e.key==='Escape'&&open){e.preventDefault();e.stopPropagation();close();}}}/>
<div class="blux-select" class:compact>
  <button bind:this={trigger} {id} type="button" class="blux-select-trigger" class:expanded={open} {disabled} aria-label={ariaLabel} aria-haspopup="listbox" aria-expanded={open} aria-controls={uid} on:click={()=>open?close():void show()} on:keydown={keydown}>
    {#if selected?.agent}<AgentBadge adapter={selected.agent} compact/>{/if}<span>{selected?.label || '请选择'}</span><svg class="select-chevron" width="14" height="14" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><path d="m5 8 5 5 5-5" stroke-linecap="round" stroke-linejoin="round"/></svg>
  </button>
  <div bind:this={menu} id={uid} popover="manual" class="blux-select-menu" style={position} role="listbox" aria-label={ariaLabel} aria-activedescendant={active>=0?`${uid}-${active}`:undefined} tabindex="-1" on:keydown={keydown}>
    {#each options as option,index}<button type="button" id={`${uid}-${index}`} role="option" aria-selected={option.value===value} aria-disabled={!!option.disabled} tabindex="-1" class="blux-select-option" class:highlighted={active===index} class:chosen={option.value===value} disabled={option.disabled} title={option.reason} on:pointermove={()=>{if(!option.disabled)active=index;}} on:click={()=>choose(index)}>
      {#if option.agent}<AgentBadge adapter={option.agent} compact/>{/if}<span class="select-option-label">{option.label}{#if option.description}<small>{option.description}</small>{/if}</span>{#if option.value===value}<svg width="15" height="15" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><path d="m4 10 4 4 8-8" stroke-linecap="round" stroke-linejoin="round"/></svg>{/if}
    </button>{/each}
  </div>
</div>
