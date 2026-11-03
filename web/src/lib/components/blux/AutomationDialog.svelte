<script lang="ts">
	import { request, errorText } from '$lib/blux/api';
	import { folderName } from '$lib/blux/context';
	import { agentOptions, type AgentInfo } from '$lib/blux/agents';
	import {
		automationAgents,
		defaultLimits,
		rulesSentence,
		validateDraft,
		type Automation,
		type AutomationDraft
	} from '$lib/blux/automations';
	import type { AgentAdapter } from '$lib/blux/types';
	import Icon from './Icon.svelte';
	import Select from './Select.svelte';

	// Pass an automation to edit it; leave it null to create a new one.
	export let open = false;
	export let automation: Automation | null = null;
	export let project = '';
	export let agents: AgentInfo[] = [];
	export let onSaved: () => void = () => {};

	let dialog: HTMLDialogElement;
	let name = '';
	let prompt = '';
	let repositoryPath = '';
	let adapter: AgentAdapter = 'codex';
	let scheduleTime = '09:00';
	let manualOnly = false;
	let allowNetwork = false;
	let error = '';
	let pending = false;
	let picking = false;
	let wasOpen = false;
	let key = '';
	let fingerprint = '';

	$: editing = automation !== null;
	$: options = agentOptions(agents).filter((option) => automationAgents.includes(option.value));
	$: chosen = agents.find((agent) => agent.id === adapter);
	$: limits = automation
		? {
				timeoutSeconds: automation.timeout_seconds,
				dailyLimit: automation.daily_limit,
				pauseAfterFailures: automation.pause_after_failures
			}
		: defaultLimits;

	// Reset the form each time the dialog opens, so a cancelled edit never leaks.
	$: if (open && !wasOpen) {
		wasOpen = true;
		error = '';
		key = '';
		fingerprint = '';
		if (automation) {
			name = automation.name;
			prompt = automation.prompt;
			repositoryPath = automation.repository_path;
			adapter = automation.adapter;
			manualOnly = automation.schedule_time === '';
			scheduleTime = automation.schedule_time || '09:00';
			allowNetwork = automation.network === 'open';
		} else {
			name = '';
			prompt = '';
			repositoryPath = project;
			adapter = 'codex';
			manualOnly = false;
			scheduleTime = '09:00';
			allowNetwork = false;
		}
	}
	$: if (!open) wasOpen = false;
	$: if (dialog) {
		if (open && !dialog.open) dialog.showModal();
		else if (!open && dialog.open) dialog.close();
	}

	async function chooseFolder() {
		picking = true;
		error = '';
		try {
			const response = await fetch('/api/v1/projects/select', {
				method: 'POST',
				signal: AbortSignal.timeout(125000)
			});
			const result = await response.json();
			if (!response.ok) throw new Error(result.error?.message || '无法选择项目');
			if (!result.canceled && typeof result.path === 'string') repositoryPath = result.path;
		} catch (e) {
			error = errorText(e);
		} finally {
			picking = false;
		}
	}

	async function save() {
		if (pending) return;
		const draft: AutomationDraft = {
			name,
			prompt,
			repository_path: repositoryPath,
			adapter,
			schedule_time: manualOnly ? '' : scheduleTime,
			network: allowNetwork ? 'open' : 'restricted'
		};
		error = validateDraft(draft);
		if (error) return;
		// The same unchanged form keeps one key, so a retry cannot create a duplicate.
		const body = JSON.stringify(draft);
		if (body !== fingerprint) {
			fingerprint = body;
			key = crypto.randomUUID();
		}
		pending = true;
		try {
			if (automation) {
				await request(`/automations/${automation.id}`, { method: 'PUT', body });
			} else {
				await request('/automations', { method: 'POST', headers: { 'Idempotency-Key': key }, body });
			}
			open = false;
			onSaved();
		} catch (e) {
			error = errorText(e);
		} finally {
			pending = false;
		}
	}
</script>

<dialog
	bind:this={dialog}
	class="collaboration-dialog automation-dialog"
	aria-label={editing ? '编辑自动化' : '新建自动化'}
	on:cancel={(event) => {
		if (pending || picking) event.preventDefault();
		else open = false;
	}}
	on:close={() => (open = false)}
>
	<header>
		<h2>{editing ? '编辑自动化' : '新建自动化'}</h2>
		<button
			type="button"
			class="ui-icon-button"
			aria-label="关闭"
			disabled={pending || picking}
			on:click={() => (open = false)}
		>
			<Icon name="close" />
		</button>
	</header>

	<div class="automation-form">
		<label class="automation-field">
			<span>名称（可选）</span>
			<input
				class="collaboration-title-input"
				bind:value={name}
				maxlength="120"
				placeholder="例如：每日测试报告"
				disabled={pending}
			/>
		</label>

		<label class="automation-field">
			<span>要自动执行的提示词</span>
			<div class="collaboration-prompt">
				<textarea
					bind:value={prompt}
					rows="5"
					placeholder="例如：运行项目的测试和 lint，不要修改代码，把失败项汇总成报告。"
					disabled={pending}
				></textarea>
			</div>
		</label>

		<div class="automation-field">
			<span>项目</span>
			<div class="automation-project">
				{#if editing}
					<span class="automation-project-name" title={repositoryPath}>
						<Icon name="folder" size={15} />{folderName(repositoryPath)}
					</span>
					<small>创建后不能更换项目</small>
				{:else}
					<button
						type="button"
						class="choose-collaboration-project"
						disabled={pending || picking}
						on:click={() => void chooseFolder()}
					>
						<Icon name="folder" size={16} />
						{picking ? '正在选择…' : folderName(repositoryPath) || '选择项目文件夹'}
					</button>
					<input
						class="collaboration-title-input automation-path"
						bind:value={repositoryPath}
						placeholder="或直接输入项目路径"
						aria-label="项目路径"
						disabled={pending}
					/>
				{/if}
			</div>
		</div>

		<div class="automation-row">
			<div class="automation-field">
				<span>执行 Agent</span>
				<Select ariaLabel="执行 Agent" bind:value={adapter} {options} disabled={pending} compact />
			</div>
			<div class="automation-field">
				<span>运行时间（每天）</span>
				<div class="automation-time">
					<input
						type="time"
						class="collaboration-title-input"
						bind:value={scheduleTime}
						disabled={pending || manualOnly}
						aria-label="每天运行时间"
					/>
					<label class="automation-check">
						<input type="checkbox" bind:checked={manualOnly} disabled={pending} />
						仅手动运行
					</label>
				</div>
			</div>
		</div>

		<label class="automation-check automation-network">
			<input type="checkbox" bind:checked={allowNetwork} disabled={pending} />
			<span>
				允许命令联网
				<small>默认关闭：自动执行时命令无法访问网络，更安全。需要安装依赖或访问外部服务时再开启。</small>
			</span>
		</label>

		<p class="dialog-agent-notice">{rulesSentence(limits)}</p>

		{#if chosen && chosen.available === false}
			<p class="dialog-agent-notice">{chosen.reason}</p>
		{/if}
		{#if error}<p class="inline-error" role="alert">{error}</p>{/if}
	</div>

	<footer>
		<button type="button" class="subtle-button" disabled={pending || picking} on:click={() => (open = false)}>
			取消
		</button>
		<button type="button" class="primary-button" disabled={pending || picking} on:click={() => void save()}>
			{pending ? '保存中…' : editing ? '保存' : '创建'}
		</button>
	</footer>
</dialog>
