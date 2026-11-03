<script lang="ts">
 import type {Task} from '$lib/blux/types';import type {Collaboration} from '$lib/blux/collaborations';import {folderName} from '$lib/blux/context';import Icon from './Icon.svelte';
 export let tasks:Task[]=[];export let collaborations:Collaboration[]=[];export let onSelect:(path:string)=>void=()=>{};export let onChoose:()=>void=()=>{};
 $: paths=[...new Set([...tasks.map(t=>t.repository_path),...collaborations.map(c=>c.repository_path)].filter(Boolean))];
</script>
<section class="projects-page"><header class="section-heading"><div><h1>个人项目</h1></div><button class="primary-button" on:click={onChoose}><Icon name="plus" size={17}/>选择项目</button></header><div class="project-card-grid">{#each paths as path}<button class="project-card" on:click={()=>onSelect(path)}><Icon name="folder" size={25}/><h2>{folderName(path)}</h2><p>{tasks.filter(t=>t.repository_path===path).length} 个任务 · {collaborations.filter(c=>c.repository_path===path).length} 个协作</p></button>{/each}</div>{#if !paths.length}<div class="collaboration-empty"><Icon name="folder" size={32}/><h2>选择你的第一个项目</h2></div>{/if}</section>
