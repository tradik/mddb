// Rendering request bodies as literals in the languages the command modal
// offers (SEC / CodeQL js/incomplete-sanitization).
//
// In their own module, not inside the component, for the reason
// advisor-format.js exists: Node's test runner cannot import .jsx, so a helper
// that lives in a component is a helper nothing can test. These two had a real
// escaping bug for exactly that long.
//
// What they produce is meant to be copied and run, so a value that escapes its
// string literal is code the user pastes into a shell.

export function jsonToPHP(obj, indent = 0) {
  const pad = '    '.repeat(indent);
  const innerPad = '    '.repeat(indent + 1);

  if (obj === null || obj === undefined) return 'null';
  if (typeof obj === 'boolean') return obj ? 'true' : 'false';
  if (typeof obj === 'number') return String(obj);
  // Backslash first, then the quote. Escaping only the quote leaves `a\` as
  // `'a\'`, where the backslash escapes the closing quote instead of itself —
  // the generated snippet is broken at best, and at worst the rest of the
  // value is read as code. Order matters: escaping the quote first would then
  // double the backslash this step adds.
  if (typeof obj === 'string') return `'${obj.replace(/\\/g, '\\\\').replace(/'/g, "\\'")}'`;

  if (Array.isArray(obj)) {
    if (obj.length === 0) return '[]';
    const items = obj.map(v => `${innerPad}${jsonToPHP(v, indent + 1)}`);
    return `[\n${items.join(",\n")}\n${pad}]`;
  }

  const entries = Object.entries(obj);
  if (entries.length === 0) return '[]';
  const items = entries.map(([k, v]) => `${innerPad}'${k}' => ${jsonToPHP(v, indent + 1)}`);
  return `[\n${items.join(",\n")}\n${pad}]`;
}

export function jsonToPython(obj, indent) {
  const pad = '    '.repeat(indent);
  const innerPad = '    '.repeat(indent + 1);

  if (obj === null || obj === undefined) return 'None';
  if (typeof obj === 'boolean') return obj ? 'True' : 'False';
  if (typeof obj === 'number') return String(obj);
  // Backslash first, then the quote — same reasoning as jsonToPHP above.
  if (typeof obj === 'string') return `"${obj.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`;

  if (Array.isArray(obj)) {
    if (obj.length === 0) return '[]';
    const items = obj.map(v => `${innerPad}${jsonToPython(v, indent + 1)}`);
    return `[\n${items.join(",\n")}\n${pad}]`;
  }

  const entries = Object.entries(obj);
  if (entries.length === 0) return '{}';
  const items = entries.map(([k, v]) => `${innerPad}"${k}": ${jsonToPython(v, indent + 1)}`);
  return `{\n${items.join(",\n")}\n${pad}}`;
}
