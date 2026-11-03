<script lang="ts">
  import { onDestroy } from 'svelte';

  export let label = '';
  export let value = 0;
  export let min = 0;
  export let max = 1000;
  export let direction = 1;
  export let edge: 'left' | 'right' = 'right';
  export let enabled = true;
  export let resetValue = 0;
  export let onResize: (width: number) => void = () => {};
  export let onCommit: () => void = () => {};
  let handle: HTMLDivElement;
  let dragging = false;
  let pointer: number | null = null;
  let originX = 0;
  let originWidth = 0;
  $: if (!enabled && pointer !== null) finish();

  function update(width: number) { onResize(Math.round(Math.max(min, Math.min(max, width)))); }
  function start(event: PointerEvent) {
    if (!enabled || event.button !== 0 || pointer !== null) return;
    event.preventDefault();
    pointer = event.pointerId;
    originX = event.clientX; originWidth = value;
    handle.setPointerCapture(pointer);
    handle.focus({ preventScroll: true });
    dragging = true;
    document.documentElement.classList.add('resizing-panels');
  }
  function move(event: PointerEvent) {
    if (event.pointerId === pointer) update(originWidth + direction * (event.clientX - originX));
  }
  function finish(event?: PointerEvent) {
    if (pointer === null || event && event.pointerId !== pointer) return;
    const id = pointer; pointer = null; dragging = false;
    document.documentElement.classList.remove('resizing-panels');
    if (handle?.hasPointerCapture(id)) handle.releasePointerCapture(id);
    onCommit();
  }
  function keydown(event: KeyboardEvent) {
    if (!enabled) return;
    if (event.key === 'Escape' && pointer !== null) { event.preventDefault(); finish(); return; }
    const step = event.shiftKey ? 40 : 10;
    if (event.key === 'ArrowLeft') update(value - direction * step);
    else if (event.key === 'ArrowRight') update(value + direction * step);
    else if (event.key === 'Home') update(min);
    else if (event.key === 'End') update(max);
    else return;
    event.preventDefault(); onCommit();
  }
  onDestroy(() => finish());
</script>

<svelte:window on:blur={() => finish()}/>

{#if enabled}
  <!-- svelte-ignore a11y_no_noninteractive_tabindex a11y_no_noninteractive_element_interactions (A focusable window separator supports pointer and arrow-key resizing.) -->
  <div bind:this={handle} class="panel-resize-handle" class:resize-left={edge==='left'} class:dragging
    role="separator" aria-label={label} aria-orientation="vertical" aria-valuenow={Math.round(value)} aria-valuemin={min} aria-valuemax={Math.round(max)} tabindex="0"
    on:pointerdown={start} on:pointermove={move} on:pointerup={finish} on:pointercancel={finish} on:lostpointercapture={finish} on:keydown={keydown}
    on:dblclick={() => { update(resetValue); onCommit(); }}></div>
{/if}
