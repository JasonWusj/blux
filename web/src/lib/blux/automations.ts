import type { AgentAdapter } from './types';

export type RunStatus =
	| 'starting'
	| 'active'
	| 'completed'
	| 'needs_decision'
	| 'failed'
	| 'timeout'
	| 'stopped'
	| 'skipped';

export interface AutomationRun {
	id: string;
	automation_id: string;
	automation_name: string;
	adapter: AgentAdapter;
	trigger: 'schedule' | 'manual';
	planned_at?: string;
	status: RunStatus;
	task_id: string;
	turn_id?: string;
	task_status?: string;
	code?: string;
	message?: string;
	summary?: string;
	created_at: string;
	finished_at?: string;
	seen_at?: string;
}

export interface Automation {
	id: string;
	name: string;
	prompt: string;
	repository_path: string;
	adapter: AgentAdapter;
	schedule_time: string;
	network: 'restricted' | 'open';
	timeout_seconds: number;
	daily_limit: number;
	pause_after_failures: number;
	paused: boolean;
	paused_at?: string;
	pause_reason?: string;
	anchor_at: string;
	created_at: string;
	updated_at: string;
	next_run_at?: string;
	runs_today: number;
	last_run?: AutomationRun;
}

export interface AutomationDraft {
	name: string;
	prompt: string;
	repository_path: string;
	adapter: AgentAdapter;
	schedule_time: string;
	network: 'restricted' | 'open';
}

/** Server defaults for new automations. Edits show the values stored on the automation instead. */
export const defaultLimits = { timeoutSeconds: 1800, dailyLimit: 5, pauseAfterFailures: 3 };

/** One sentence telling the user the safety rules that apply to every run. */
export function rulesSentence(limits: {
	timeoutSeconds: number;
	dailyLimit: number;
	pauseAfterFailures: number;
}): string {
	const minutes = Math.round(limits.timeoutSeconds / 60);
	return `每次运行在独立工作区中进行，不会改动你的项目，结果在收件箱里确认后才会接收。单次最长运行 ${minutes} 分钟，每天最多 ${limits.dailyLimit} 次，连续失败 ${limits.pauseAfterFailures} 次会自动暂停。`;
}

/** Agents that can run unattended. Others lack a verified sandbox for it. */
export const automationAgents: AgentAdapter[] = ['codex', 'claude'];

export const runStatusLabels: Record<RunStatus, string> = {
	starting: '准备中',
	active: '进行中',
	completed: '已完成',
	needs_decision: '需要决定',
	failed: '未完成',
	timeout: '已超时',
	stopped: '已停止',
	skipped: '已跳过'
};

/** Status of a run as the user should read it, using the live task state while it runs. */
export function runStatusLabel(run: Pick<AutomationRun, 'status' | 'task_status'>): string {
	if (run.status === 'active' && run.task_status === 'queued') return '排队中';
	return runStatusLabels[run.status] ?? run.status;
}

export function isInProgress(status: RunStatus): boolean {
	return status === 'starting' || status === 'active';
}

/** True when the run ended in a way the user should look at. */
export function needsAttention(status: RunStatus): boolean {
	return status === 'needs_decision' || status === 'failed' || status === 'timeout';
}

export function scheduleText(time: string): string {
	return time ? `每天 ${time}` : '仅手动运行';
}

/** Headline for a failed run, naming the fix when the code points to one. */
const failureTitles: Record<string, string> = {
	codex_auth_required: 'Codex 需要重新登录',
	claude_auth_required: 'Claude Code 需要重新登录',
	codex_usage_limit: 'Codex 额度已用完',
	codex_not_found: '找不到 Codex CLI',
	claude_not_found: '找不到 Claude Code CLI',
	execution_timeout: '运行超过时间上限',
	recovery_required: '运行被中断',
	worker_stopped: 'Worker 已停止',
	repository_not_found: '项目文件夹已不存在',
	git_not_found: '找不到 Git',
	start_failed: '无法开始运行'
};

export function failureTitle(code: string | undefined): string {
	return (code && failureTitles[code]) || '运行未完成';
}

/** Formats an ISO time for the list, or returns an empty string if it is not valid. */
export function shortTime(value: string | undefined): string {
	if (!value) return '';
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) return '';
	return date.toLocaleString('zh-CN', {
		month: 'numeric',
		day: 'numeric',
		hour: '2-digit',
		minute: '2-digit'
	});
}

/** Returns an error message if the draft cannot be saved, otherwise an empty string. */
export function validateDraft(draft: AutomationDraft): string {
	if (!draft.prompt.trim()) return '请填写要自动执行的提示词。';
	if (Array.from(draft.prompt.trim()).length > 16_000) return '提示词不能超过 16,000 个字符。';
	if (Array.from(draft.name.trim()).length > 120) return '名称不能超过 120 个字符。';
	if (!draft.repository_path.trim()) return '请选择项目文件夹。';
	if (draft.schedule_time && !/^([01]\d|2[0-3]):[0-5]\d$/.test(draft.schedule_time)) {
		return '运行时间需要是 HH:MM 格式。';
	}
	return '';
}
