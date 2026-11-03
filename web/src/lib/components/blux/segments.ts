import type { FileChange, Segment } from '$lib/blux/types';

export type MessagePart =
  | { type: 'text'; text: string }
  | { type: 'tool'; id: string; name: string; input: string; output?: string; status?: Segment['status'] }
  | { type: 'file_change'; id: string; changes: FileChange[] };

// Stable item identities keep streaming command output and file snapshots in one
// display object instead of adding another card for every projection update.
export function messageParts(segments: Segment[] = []): MessagePart[] {
  const parts: MessagePart[] = [];
  const tools = new Map<string, Extract<MessagePart, { type: 'tool' }>>();
  const fileItems = new Map<string, Extract<MessagePart, { type: 'file_change' }>>();
  for (const [index, segment] of segments.entries()) {
    if (segment.type === 'text') {
      const previous = parts.at(-1);
      if (previous?.type === 'text') previous.text += segment.text || '';
      else parts.push({ type: 'text', text: segment.text || '' });
      continue;
    }
    if (segment.type === 'file_change') {
      const id = segment.item_id || `files-${index}`;
      let item = fileItems.get(id);
      if (!item) {
        item = { type: 'file_change', id, changes: [] };
        fileItems.set(id, item);
        parts.push(item);
      }
      for (const change of segment.changes || []) {
        const previousIndex = item.changes.findIndex((existing) => existing.path === change.path);
        if (previousIndex === -1) item.changes.push(change);
        else item.changes[previousIndex] = change;
      }
      continue;
    }
    const id = segment.tool_call_id || `tool-${index}`;
    let tool = tools.get(id);
    if (!tool) {
      tool = { type: 'tool', id, name: segment.name || '工具调用', input: segment.input || '' };
      tools.set(id, tool);
      parts.push(tool);
    }
    if (segment.name) tool.name = segment.name;
    if (segment.status) tool.status = segment.status;
    if (segment.type === 'tool_call' && segment.input !== undefined) tool.input = segment.input;
    if (segment.type === 'tool_result') tool.output = segment.output || '';
  }
  return parts;
}
