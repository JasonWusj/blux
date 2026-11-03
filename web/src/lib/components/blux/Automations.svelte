<script lang="ts">
	import { onMount } from 'svelte';
	import { request, errorText } from '$lib/blux/api';
	import { agentName, type AgentInfo } from '$lib/blux/agents';
	import { folderName } from '$lib/blux/context';
	import {
		failureTitle,
		isInProgress,
		needsAttention,
		runStatusLabel,
		scheduleText,
		shortTime,
		type Automation,
		type AutomationRun
	} from '$lib/blux/automations';
	import AgentBadge from './AgentBadge.svelte';
	import AutomationDialog from './AutomationDialog.svelte';
	import Icon from './Icon.svelte';

	export let agents: AgentInfo[] = [];
	export let project = '';
	export let onTask: (id: string) => void = () => {};
	// Called after anything that changes the unread count, so the sidebar badge follows.
	export let onChanged: () => void = () => {};
	export let initialTab: 'inbox' | 'automations' = 'inbox';

	let tab: 'inbox' | 'automations' = initialTab;
	let automations: Automation[] = [];
	let runs: AutomationRun[] = [];
	let unread = 0;
	let loaded = false;
	let error = '';
	let notice = '';
	let dialogOpen = false;
	let editing: Automation | null = null;
	let busy = new Set<string>();
	let confirmingDelete = '';
	let confirmTimer: ReturnType<typeof setTimeout> | undefined;
	let alive = true;
	let epoch = 0;

	async function refresh() {
		const ticket = ++epoch;
		try {
			const [list, inbox] = await Promise.all([
				request<{ automations: Automation[] }>('/automations'),
				request<{ runs: AutomationRun[]; unread: number }>('/inbox')
			]);
			if (!alive || ticket !== epoch) return;
			automations = list.automations;
			runs = inbox.runs;
			unread = inbox.unread;
			error = '';
		} catch (e) {
			if (alive && ticket === epoch) error = errorText(e);
		} finally {
			if (alive) loaded = true;
		}
	}

	onMount(() => {
		alive = true;
		void refresh();
		const timer = setInterval(() => void refresh(), 3000);
		return () => {
			alive = false;
			epoch++;
			clearInterval(timer);
			if (confirmTimer) clearTimeout(confirmTimer);
		};
	});

	// Runs one action at a time per item and refreshes afterwards.
	async function act(id: string, work: () => Promise<void>) {
		if (busy.has(id)) return;
		busy = new Set(busy).add(id);
		error = '';
		notice = '';
		try {
			await work();
			await refresh();
			onChanged();
		} catch (e) {
			error = errorText(e);
		} finally {
			busy = new Set([...busy].filter((item) => item !== id));
		}
	}

	function runNow(automation: Automation) {
		void act(automation.id, async () => {
			await request(`/automations/${automation.id}/runs`, {
				method: 'POST',
				headers: { 'Idempotency-Key': crypto.randomUUID() }
			});
			notice = `已开始运行「${automation.name}」，结果会出现在收件箱。`;
			tab = 'inbox';
		});
	}

	function setPaused(automation: Automation, paused: boolean) {
		void act(automation.id, async () => {
			await request(`/automations/${automation.id}/paused`, {
				method: 'PUT',
				body: JSON.stringify({ paused })
			});
		});
	}

	// Deleting asks twice: the first click arms the button, the second confirms.
	function remove(automation: Automation) {
		if (confirmingDelete !== automation.id) {
			confirmingDelete = automation.id;
			if (confirmTimer) clearTimeout(confirmTimer);
			confirmTimer = setTimeout(() => (confirmingDelete = ''), 4000);
			return;
		}
		confirmingDelete = '';
		void act(automation.id, async () => {
			await request(`/automations/${automation.id}`, { method: 'DELETE' });
		});
	}

	function openRun(run: AutomationRun) {
		if (!run.task_id) return;
		// Mark it read first; opening must work even if this call fails.
		if (!run.seen_at) void request(`/inbox/${run.id}/seen`, { method: 'PUT' }).then(onChanged, () => {});
		onTask(run.task_id);
	}

	function dismiss(run: AutomationRun) {
		void act(run.id, async () => {
			await request(`/inbox/${run.id}/dismiss`, { method: 'POST' });
		});
	}

	function edit(automation: Automation) {
		editing = automation;
		dialogOpen = true;
	}

	function create() {
		editing = null;
		dialogOpen = true;
	}

	function saved() {
		void refresh();
		onChanged();
	}
</script>

<div class="collaboration-list-page automation-page">
	<header class="section-heading">
		<div>
			<h1>自动化</h1>
			<p>让 Agent 按时间自动执行任务，结果在收件箱里确认后才会接收到项目。</p>
		</div>
		<div class="section-heading-actions">
			<button class="primary-button" on:click={create}><Icon name="plus" size={17} />新建自动化</button>
		</div>
	</header>

	<nav class="collaboration-tabs automation-tabs" aria-label="自动化">
		<button class:active={tab === 'inbox'} aria-current={tab === 'inbox' ? 'page' : undefined} on:click={() => (tab = 'inbox')}>
			收件箱{#if unread > 0}<span class="automation-badge" aria-label={`${unread} 条未读`}>{unread}</span>{/if}
		</button>
		<button class:active={tab === 'automations'} aria-current={tab === 'automations' ? 'page' : undefined} on:click={() => (tab = 'automations')}>
			自动化 · {automations.length}
		</button>
	</nav>

	{#if error}<p class="inline-error" role="alert">{error}<button on:click={() => void refresh()}>重试</button></p>{/if}
	{#if notice}<p class="automation-notice" role="status">{notice}</p>{/if}

	{#if !loaded}
		<div class="public-loading">正在读取…</div>
	{:else if tab === 'inbox'}
		{#if runs.length === 0}
			<div class="collaboration-empty">
				<Icon name="clock" size={32} />
				<h2>收件箱是空的</h2>
				<p class="muted">自动化运行后，结果会出现在这里。</p>
				{#if automations.length === 0}
					<button class="subtle-button" on:click={create}>新建自动化 <Icon name="plus" size={15} /></button>
				{/if}
			</div>
		{:else}
			<ul class="automation-list" aria-label="运行结果">
				{#each runs as run (run.id)}
					<li class="automation-card" class:is-unread={!run.seen_at && !isInProgress(run.status)}>
						<div class="automation-card-main">
							<div class="automation-card-title">
								<strong>{run.automation_name}</strong>
								<span
									class="status-pill"
									class:running={isInProgress(run.status)}
									class:complete={run.status === 'completed'}
									class:attention={needsAttention(run.status)}
								>
									{runStatusLabel(run)}
								</span>
							</div>
							<div class="automation-meta">
								<AgentBadge adapter={run.adapter} compact />
								<span>{agentName(run.adapter)}</span>
								<span>{run.trigger === 'schedule' ? '定时' : '手动'}</span>
								<time>{shortTime(run.finished_at || run.created_at)}</time>
							</div>
							{#if run.status === 'failed' || run.status === 'timeout'}
								<p class="automation-failure"><b>{failureTitle(run.code)}</b>{#if run.message}：{run.message}{/if}</p>
							{:else if run.summary}
								<p class="automation-summary">{run.summary}</p>
							{/if}
						</div>
						<div class="automation-card-actions">
							<button class="subtle-button" disabled={!run.task_id} on:click={() => openRun(run)}>
								{run.status === 'needs_decision' ? '查看并回复' : '查看结果'}
							</button>
							{#if !isInProgress(run.status)}
								<button
									class="ui-icon-button"
									aria-label={`丢弃：${run.automation_name}`}
									title="丢弃"
									disabled={busy.has(run.id)}
									on:click={() => dismiss(run)}
								>
									<Icon name="close" size={16} />
								</button>
							{/if}
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	{:else if automations.length === 0}
		<div class="collaboration-empty">
			<Icon name="clock" size={32} />
			<h2>还没有自动化</h2>
			<p class="muted">例如：每天早上 9 点跑一遍测试和 lint，把失败项汇总成报告。</p>
			<button class="subtle-button" on:click={create}>新建自动化 <Icon name="plus" size={15} /></button>
		</div>
	{:else}
		<ul class="automation-list" aria-label="自动化列表">
			{#each automations as automation (automation.id)}
				<li class="automation-card" class:paused={automation.paused}>
					<div class="automation-card-main">
						<div class="automation-card-title">
							<strong>{automation.name}</strong>
							{#if automation.paused}
								<span class="status-pill attention">已暂停</span>
							{:else if automation.last_run && isInProgress(automation.last_run.status)}
								<span class="status-pill running">运行中</span>
							{/if}
						</div>
						<div class="automation-meta">
							<AgentBadge adapter={automation.adapter} compact />
							<span>{agentName(automation.adapter)}</span>
							<span title={automation.repository_path}><Icon name="folder" size={13} />{folderName(automation.repository_path)}</span>
							<span><Icon name="clock" size={13} />{scheduleText(automation.schedule_time)}</span>
							{#if automation.network === 'open'}<span class="automation-warn">允许命令联网</span>{/if}
						</div>
						{#if automation.paused && automation.pause_reason}
							<p class="automation-failure">{automation.pause_reason}</p>
						{:else}
							<p class="automation-summary">
								{#if automation.next_run_at}下次运行：{shortTime(automation.next_run_at)}{:else}仅在手动运行时执行{/if}
								{#if automation.last_run}· 上次：{runStatusLabel(automation.last_run)}{/if}
							</p>
						{/if}
					</div>
					<div class="automation-card-actions">
						<button class="subtle-button" disabled={busy.has(automation.id)} on:click={() => runNow(automation)}>立即运行</button>
						<button class="subtle-button" disabled={busy.has(automation.id)} on:click={() => setPaused(automation, !automation.paused)}>
							{automation.paused ? '恢复' : '暂停'}
						</button>
						<button class="subtle-button" disabled={busy.has(automation.id)} on:click={() => edit(automation)}>编辑</button>
						<button
							class="subtle-button"
							class:danger={confirmingDelete === automation.id}
							disabled={busy.has(automation.id)}
							on:click={() => remove(automation)}
						>
							{confirmingDelete === automation.id ? '确认删除' : '删除'}
						</button>
					</div>
				</li>
			{/each}
		</ul>
	{/if}
</div>

{#if dialogOpen}
	<AutomationDialog bind:open={dialogOpen} automation={editing} {project} {agents} onSaved={saved} />
{/if}
