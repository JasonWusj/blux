import type { AgentAdapter } from './types';
export const agentNames: Record<AgentAdapter, string> = {
 codex: 'Codex', claude: 'Claude Code', deepseek: 'DeepSeek Harness', gemini: 'Gemini CLI'
};
export const agentName = (id: string) => agentNames[id as AgentAdapter] || id;
export interface AgentInfo { id: AgentAdapter; name: string; available: boolean; version: string; reason?: string; capabilities: { streaming: boolean; resume: boolean; steer: boolean; interrupt: boolean } }
export function agentOptions(agents: AgentInfo[] = [], filter = false) {
 return (Object.entries(agentNames) as [AgentAdapter,string][]).map(([value,label]) => {
  const info = agents.find(agent => agent.id === value);
  return { value, label, agent: value, disabled: !filter && info?.available === false, description: !filter && info?.available === false ? (info.reason?.includes('未安装')||info.reason?.includes('找不到')?'未安装':'不可用') : undefined, reason: info?.reason };
 });
}
