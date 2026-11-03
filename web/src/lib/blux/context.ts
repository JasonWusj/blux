export interface ContextChoice {
  id: string;
  title: string;
  kind: 'skill' | 'memory' | 'reference';
}

export function folderName(path: string): string {
  return path.trim().replace(/[\\/]+$/, '').split(/[\\/]/).at(-1) || '';
}

export const contextKindLabels: Record<ContextChoice['kind'], string> = {
  skill: '技能', memory: '记忆', reference: '资料'
};
