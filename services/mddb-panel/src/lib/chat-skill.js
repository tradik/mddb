/**
 * Chat skills (#404): the skill editor's form state, the body it saves, and
 * the ordered skill list a scenario keeps. The server parses SKILL.md front
 * matter (POST /v1/chat/skills/import), so nothing here reads YAML.
 */

/** The form state of a skill, or of a new one; `source` rides along unchanged. */
export function toSkillForm(skill = {}) {
  return {
    name: skill.name || '',
    description: skill.description || '',
    content: skill.content || '',
    source: skill.source || null,
  };
}

/** The body of PUT /v1/chat/skills/{name}. A pasted skill has no source. */
export function fromSkillForm(form) {
  const body = { description: form.description.trim(), content: form.content };
  if (form.source) body.source = form.source;
  return body;
}

/** Why a skill cannot be saved yet, or null when it can. */
export function skillProblem(form) {
  const name = form.name.trim();
  if (!name) return 'Give the skill a name.';
  if (/[|/\s]/.test(name) || name === 'version' || name === 'import') {
    return 'The name may not contain spaces, "/" or "|", or be "version" or "import".';
  }
  if (!form.content.trim()) return 'Write the instructions, or paste a SKILL.md.';
  return null;
}

/** A source in a few words: its host and path, and the commit's first 7 characters. */
export function sourceLabel(source) {
  if (!source) return 'pasted';
  let where = source.url;
  try {
    const url = new URL(source.url);
    where = url.host + url.pathname;
  } catch {
    // An unparsable URL is shown as it is.
  }
  return source.commit ? `${where} @ ${source.commit.slice(0, 7)}` : where;
}

/** A byte count for people: bytes below 1 KiB, else KiB with one decimal. */
export function formatBytes(bytes = 0) {
  return bytes < 1024 ? `${bytes} B` : `${(bytes / 1024).toFixed(1)} KiB`;
}

/** `list` with the item at `index` moved by `step` (-1 up, +1 down); unchanged at an end. */
export function moveItem(list, index, step) {
  const to = index + step;
  if (to < 0 || to >= list.length) return list;
  const out = [...list];
  [out[index], out[to]] = [out[to], out[index]];
  return out;
}
