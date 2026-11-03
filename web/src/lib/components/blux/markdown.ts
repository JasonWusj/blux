// Local presentation helpers for the adapted Open WebUI Markdown token renderer.
export function unescapeHtml(value: string): string {
  const entities: Record<string, string> = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: '\u00a0' };
  return value.replace(/&(#x[0-9a-f]+|#\d+|amp|lt|gt|quot|apos|nbsp);/gi, (raw, entity: string) => {
    if (entity[0] !== '#') return entities[entity.toLowerCase()] ?? raw;
    const number = entity[1].toLowerCase() === 'x' ? Number.parseInt(entity.slice(2), 16) : Number.parseInt(entity.slice(1), 10);
    return Number.isFinite(number) && number >= 0 && number <= 0x10ffff && !(number >= 0xd800 && number <= 0xdfff) ? String.fromCodePoint(number) : '\ufffd';
  });
}

export function safeLinkUrl(value: string): string | null {
  try {
    const url = new URL(value, window.location.href);
    return ['http:', 'https:', 'mailto:', 'tel:'].includes(url.protocol) ? url.href : null;
  } catch { return null; }
}
