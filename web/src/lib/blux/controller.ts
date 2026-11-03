import { pushState } from '$app/navigation';
import { writable, type Readable } from 'svelte/store';
import { ApiError, errorText, request } from './api';
import type { BluxEvent, CreateTaskOptions, Health, Task, TaskDetail, WorkbenchState } from './types';

export type { WorkbenchState } from './types';

interface SubmissionAttempt {
	key: string;
	prompt: string;
	taskId: string | null;
	configuration: string;
}

export function createWorkbench() {
	let current: WorkbenchState = {
		tasks: [],
		tasksLoading: true,
		tasksError: '',
		selectedId: null,
		detail: null,
		detailLoading: false,
		detailError: '',
		health: null,
		healthError: '',
		events: [],
		streamState: 'idle',
		submitting: false,
		submitError: ''
	};
	const writableState = writable<WorkbenchState>(current);
	const state: Readable<WorkbenchState> = { subscribe: writableState.subscribe };
	let running = false;
	let lifecycle = 0;
	let navigationEpoch = 0;
	let detailRequest = 0;
	let tasksRequest = 0;
	let healthRequest = 0;
	let submissionAttempt: SubmissionAttempt | null = null;
	let source: EventSource | null = null;
	let eventTimer: number | undefined;
	let pollTimer: number | undefined;
	const requests = new Set<AbortController>();

	function patch(change: Partial<WorkbenchState>) {
		current = { ...current, ...change };
		writableState.set(current);
	}

	function showDetail(detail: TaskDetail) {
		if (
			current.detail?.task.id === detail.task.id &&
			current.detail.task.last_event_seq > detail.task.last_event_seq
		) {
			return;
		}
		patch({ detail, detailError: '', detailLoading: false });
	}

	async function scopedRequest<T>(path: string, init: RequestInit = {}): Promise<T> {
		const controller = new AbortController();
		requests.add(controller);
		try {
			return await request<T>(path, { ...init, signal: controller.signal });
		} finally {
			requests.delete(controller);
		}
	}

	async function loadTasks() {
		if (!running) return;
		const requestID = ++tasksRequest;
		const epoch = lifecycle;
		try {
			const response = await scopedRequest<{ tasks: Task[] }>('/tasks');
			if (!running || epoch !== lifecycle || requestID !== tasksRequest) return;
			const tasks=response.tasks||[];const selected=tasks.find(t=>t.id===current.detail?.task.id);
			patch({tasks,tasksError:'',tasksLoading:false,...(current.detail&&selected?{detail:{...current.detail,task:{...current.detail.task,pinned_at:selected.pinned_at}}}:{})});
		} catch (error) {
			if (running && epoch === lifecycle && requestID === tasksRequest) {
				patch({ tasksError: errorText(error), tasksLoading: false });
			}
		}
	}

	async function loadHealth() {
		if (!running) return;
		const requestID = ++healthRequest;
		const epoch = lifecycle;
		try {
			const health = await scopedRequest<Health>('/health');
			if (running && epoch === lifecycle && requestID === healthRequest) {
				patch({ health, healthError: '' });
			}
		} catch (error) {
			if (running && epoch === lifecycle && requestID === healthRequest) {
				patch({ healthError: errorText(error) });
			}
		}
	}

	async function loadDetail(id: string, showLoading = false) {
		if (!running || current.selectedId !== id) return;
		const requestID = ++detailRequest;
		const epoch = lifecycle;
		if (showLoading) patch({ detailLoading: true });
		try {
			const detail = await scopedRequest<TaskDetail>(`/tasks/${encodeURIComponent(id)}`);
			if (
				running &&
				epoch === lifecycle &&
				current.selectedId === id &&
				requestID === detailRequest
			) {
				showDetail(detail);
			}
		} catch (error) {
			if (
				running &&
				epoch === lifecycle &&
				current.selectedId === id &&
				requestID === detailRequest
			) {
				if(error instanceof ApiError&&error.status===404){setSelection(null,true);void loadTasks();return;}
				patch({ detailError: errorText(error), detailLoading: false });
			}
		}
	}

	function closeStream() {
		source?.close();
		source = null;
		if (eventTimer !== undefined) {
			window.clearTimeout(eventTimer);
			eventTimer = undefined;
		}
	}

	function subscribeTask(id: string | null) {
		closeStream();
		++detailRequest;
		patch({ detail: null, detailError: '', detailLoading: !!id, events: [] });
		if (!id) {
			patch({ streamState: 'idle' });
			return;
		}
		void loadDetail(id, true);
		patch({ streamState: 'connecting' });
		const epoch = lifecycle;
		const subscription = new EventSource(`/api/v1/tasks/${encodeURIComponent(id)}/events?after=0`);
		source = subscription;
		const isCurrent = () =>
			running && lifecycle === epoch && current.selectedId === id && source === subscription;
		subscription.onopen = () => {
			if (isCurrent()) patch({ streamState: 'live' });
		};
		subscription.onerror = () => {
			if (isCurrent()) patch({ streamState: 'reconnecting' });
		};
		subscription.addEventListener('blux.event', (raw) => {
			if (!isCurrent()) return;
			let event: BluxEvent;
			try {
				event = JSON.parse((raw as MessageEvent<string>).data) as BluxEvent;
			} catch {
				return;
			}
			if (!event || event.task_id !== id || !Number.isSafeInteger(event.seq) || event.seq < 1) return;
			patch({
				streamState: 'live',
				events: current.events.some((item) => item.seq === event.seq)
					? current.events
					: [...current.events, event].sort((a, b) => a.seq - b.seq).slice(-150)
			});
			// The persisted projection owns message text. Replaying historical
			// events after a refresh must never append that text a second time.
			if (eventTimer === undefined) {
				eventTimer = window.setTimeout(() => {
					eventTimer = undefined;
					if (!isCurrent()) return;
					void loadDetail(id);
					void loadTasks();
				}, 100);
			}
		});
	}

	function writeURL(id: string | null) {
		const url = new URL(window.location.href);
		url.searchParams.delete('collaboration');url.searchParams.delete('view');
		if (id) url.searchParams.set('task', id);
		else url.searchParams.delete('task');
		if (url.href !== window.location.href) pushState(url, {});
	}

	function setSelection(id: string | null, updateURL: boolean) {
		if (current.selectedId === id) return;
		++navigationEpoch;
		patch({ selectedId: id, submitError: '' });
		if (updateURL) writeURL(id);
		subscribeTask(id);
	}

	function selectTask(id: string | null, updateURL = true) {
		if (!running || current.submitting) return;
		setSelection(id, updateURL);
	}

	function refresh() {
		if (!running) return;
		void loadTasks();
		void loadHealth();
		if (current.selectedId) void loadDetail(current.selectedId, !current.detail);
	}
	function forgetTasks(ids:string[]) {
		++tasksRequest;
		const removed=new Set(ids);
		patch({tasks:current.tasks.filter(task=>!removed.has(task.id))});
		if(current.selectedId&&removed.has(current.selectedId))setSelection(null,true);
		void loadTasks();
	}
	function updateTaskPin(id:string,pinnedAt:string){
		++tasksRequest;
		patch({tasks:current.tasks.map(t=>t.id===id?{...t,pinned_at:pinnedAt}:t),...(current.detail?.task.id===id?{detail:{...current.detail,task:{...current.detail.task,pinned_at:pinnedAt}}}:{})});
		// Refresh the selected task so an older detail response cannot undo its pin.
		// Pinning another row must not invalidate a pending navigation request.
		if(current.selectedId===id)void loadDetail(id,!current.detail);
		void loadTasks();
	}

	function start(): () => void {
		if (running) return () => {};
		running = true;
		const epoch = ++lifecycle;
		++navigationEpoch;
		patch({ selectedId: new URLSearchParams(window.location.search).get('task'), submitError: '' });
		const onPop = () => {
			setSelection(new URLSearchParams(window.location.search).get('task'), false);
		};
		window.addEventListener('popstate', onPop);
		subscribeTask(current.selectedId);
		void loadTasks();
		void loadHealth();
		pollTimer = window.setInterval(() => {
			void loadTasks();
			void loadHealth();
		}, 5_000);
		return () => {
			if (!running || lifecycle !== epoch) return;
			running = false;
			++lifecycle;
			++navigationEpoch;
			closeStream();
			window.clearInterval(pollTimer);
			pollTimer = undefined;
			window.removeEventListener('popstate', onPop);
			for (const controller of requests) controller.abort();
			requests.clear();
			patch({ streamState: 'idle', submitting: false });
		};
	}

	async function submit(rawPrompt: string, taskId: string | null, options?: CreateTaskOptions): Promise<boolean> {
		if (!running || current.submitting) return false;
		const prompt = rawPrompt.trim();
		if (!prompt) {
			patch({ submitError: '先写下你想完成的事情。' });
			return false;
		}
		if (Array.from(prompt).length > 16_000) {
			patch({ submitError: '内容过长，请将目标缩短到 16,000 个字符以内。' });
			return false;
		}
		const creation = taskId ? null : {
			adapter: options?.adapter || 'codex',model:options?.model||'',model_provider:options?.model_provider||'',reasoning_effort:options?.reasoning_effort||'',
			repository_path: options?.repository_path?.trim() || '',
      mode:options?.mode||'explore',context_ids:options?.context_ids||[]
		};
		if (creation?.adapter === 'codex' && !creation.repository_path) {
			patch({ submitError: '请选择本地项目文件夹。' });
			return false;
		}
		const payload = taskId ? { context_ids: options?.context_ids || [],model:options?.model||'',model_provider:options?.model_provider||'',reasoning_effort:options?.reasoning_effort||'' } : creation;
		const configuration = JSON.stringify(payload);
		const previous = submissionAttempt;
		const attempt =
			previous?.taskId === taskId && previous.prompt === prompt && previous.configuration === configuration
				? previous
				: { key: crypto.randomUUID(), prompt, taskId, configuration };
		submissionAttempt = attempt;
		const epoch = lifecycle;
		const navigation = navigationEpoch;
		patch({ submitting: true, submitError: '' });
		try {
			const response = await scopedRequest<TaskDetail>(
				taskId ? `/tasks/${encodeURIComponent(taskId)}/turns` : '/tasks',
				{
					method: 'POST',
					headers: { 'Idempotency-Key': attempt.key },
					body: JSON.stringify({ prompt, ...payload })
				}
			);
			if (!running || epoch !== lifecycle) return false;
			submissionAttempt = null;
			if (navigationEpoch === navigation) {
				if (taskId && current.selectedId === taskId && response.task.id === taskId) {
					++detailRequest;
					showDetail(response);
				} else if (!taskId) {
					setSelection(response.task.id, true);
					++detailRequest;
					showDetail(response);
				}
			}
			void loadTasks();
			if (current.selectedId === response.task.id) void loadDetail(response.task.id);
			return true;
		} catch (error) {
			if (!running || epoch !== lifecycle) return false;
			if (error instanceof ApiError && error.status === 409) submissionAttempt = null;
			if (navigationEpoch === navigation) patch({ submitError: errorText(error) });
			void loadTasks();
			if (taskId && current.selectedId === taskId) void loadDetail(taskId);
			return false;
		} finally {
			if (running && epoch === lifecycle) patch({ submitting: false });
		}
	}

	return { state, start, selectTask, refresh, submit, forgetTasks, updateTaskPin };
}
