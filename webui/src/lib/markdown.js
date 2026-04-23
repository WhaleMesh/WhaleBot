function escapeHtml(s) {
  return s
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

export function renderMarkdown(md) {
  if (!md) return '';

  const fenced = [];
  let working = String(md).replace(/```(?:[a-zA-Z0-9_+-]+)?\n([\s\S]*?)```/g, (_, code) => {
    const token = `__CODE_BLOCK_${fenced.length}__`;
    fenced.push(`<pre><code>${escapeHtml(code.replace(/\n$/, ''))}</code></pre>`);
    return token;
  });

  working = escapeHtml(working);

  // Block-level helpers.
  working = working
    .split('\n')
    .map((line) => {
      if (/^\s*#{1,6}\s+/.test(line)) {
        return `<strong>${line.replace(/^\s*#{1,6}\s+/, '')}</strong>`;
      }
      if (/^\s*[-*]\s+/.test(line)) {
        return `• ${line.replace(/^\s*[-*]\s+/, '')}`;
      }
      return line;
    })
    .join('\n');

  // Inline transforms.
  working = working
    .replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/__([^_]+)__/g, '<strong>$1</strong>')
    .replace(/(^|[\s(])\*([^*]+)\*($|[\s).,!?:;])/g, '$1<em>$2</em>$3')
    .replace(/(^|[\s(])_([^_]+)_($|[\s).,!?:;])/g, '$1<em>$2</em>$3')
    .replace(/`([^`]+)`/g, '<code>$1</code>');

  for (let i = 0; i < fenced.length; i += 1) {
    working = working.replace(`__CODE_BLOCK_${i}__`, fenced[i]);
  }

  return working.replace(/\n/g, '<br/>');
}
