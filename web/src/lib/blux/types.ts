export type TaskStatus = 'queued' | 'running' | 'completed' | 'failed';
export type AgentAdapter = 'codex' | 'claude' | 'deepseek' | 'gemini';

export interface ContextUsage {used_tokens:number;window_tokens?:number;model?:string;source:string;observed_at:string}

export interface CreateTaskOptions {
  reasoning_effort?:string;
  model?:string;model_provider?:string;
  adapter: AgentAdapter;
  repository_path?: string;
  mode?:string;
  context_ids?:string[];
}

export interface Task {
 pinned_at?:string;
 collaboration_id?:string;
 automation_id?:string;
  id: string;
  title: string;
  prompt: string;
  status: TaskStatus;
  adapter: AgentAdapter;
  session_id: string;
  current_turn_id: string;
  created_at: string;
  updated_at: string;
  last_event_seq: number;
  repository_path: string;
  base_commit: string;
  model: string;
  model_provider: string;
}

export interface RuntimeInfo {
  reasoning_effort?:string;
  context_usage?:ContextUsage;
  workspace_path: string;
  worktree_path: string;
  branch_name: string;
  base_commit: string;
  native_thread_id: string;
  model: string;
  model_provider: string;
  cli_version: string;
}

export interface TokenUsage {
  input_tokens: number;
  output_tokens: number;
  cached_input_tokens: number;
  reasoning_output_tokens: number;
  total_tokens: number;
}

export interface FileChange {
  path: string;
  kind: string;
  diff: string;
}

export interface Segment {
  type: 'text' | 'tool_call' | 'tool_result' | 'file_change';
  status?: 'running' | 'completed' | 'failed';
  text?: string;
  tool_call_id?: string;
  name?: string;
  input?: string;
  output?: string;
  item_id?: string;
  changes?: FileChange[];
}

export interface Message {
  id: string;
  session_id: string;
  turn_id: string;
  role: 'user' | 'assistant';
  status: string;
  segments: Segment[];
}

export interface Turn {
  reasoning_effort?:string;
  model?:string;model_provider?:string;
  id: string;
  task_id: string;
  session_id: string;
  prompt: string;
  status: TaskStatus;
  generation: number;
  created_at: string;
  started_at?: string;
  ended_at?: string;
  error_code?: string;
  error?: string;
  native_turn_id?: string;
  usage: TokenUsage;
}

export interface TaskDetail {
 collaboration_id?:string;
 manager?:boolean;
 delivery_id?:string;
  task: Task;
  session: { id: string; task_id: string; created_at?: string; runtime?: RuntimeInfo };
  turns: Turn[];
  messages: Message[];
  submitted_turn_id?: string;
}

export interface BluxEvent {
  id: string;
  schema_version: number;
  task_id: string;
  session_id: string;
  turn_id: string;
  generation: number;
  seq: number;
  type: string;
  payload: unknown;
  created_at: string;
}

export interface Health {
  status: string;
  phase: number;
  adapter: string;
  data_dir: string;
  worker: { id?: string; last_seen_at?: string; online: boolean };
  codex?: { available: boolean; version?: string; error?: string };
}

export interface WorkbenchState {
  tasks: Task[];
  tasksLoading: boolean;
  tasksError: string;
  selectedId: string | null;
  detail: TaskDetail | null;
  detailLoading: boolean;
  detailError: string;
  health: Health | null;
  healthError: string;
  events: BluxEvent[];
  streamState: 'idle' | 'connecting' | 'live' | 'reconnecting';
  submitting: boolean;
  submitError: string;
}
