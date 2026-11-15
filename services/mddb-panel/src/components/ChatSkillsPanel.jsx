import { useEffect, useState } from 'react';
import { Download, Plus, Sparkles } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { fromSkillForm, skillProblem } from '../lib/chat-skill';
import ChatSkillList from './ChatSkillList';
import ChatSkillEditor from './ChatSkillEditor';
import ChatSkillImport from './ChatSkillImport';
import ChatSkillUpdate from './ChatSkillUpdate';
import ChatSkillHistory from './ChatSkillHistory';

// #404: the Skills tab of the Chat page — reusable SKILL.md instruction
// blocks that scenarios attach. List, write or paste one, import one from a
// URL after reading it whole, update an imported one from its source after a
// diff, see who uses one, delete one no scenario uses. Every call needs
// admin on "*": a skill's text is part of system prompts.

export default function ChatSkillsPanel() {
  const [skills, setSkills] = useState([]);
  const [version, setVersion] = useState(0);
  const [mode, setMode] = useState(null); // null, 'new', 'import' or 'edit'
  const [current, setCurrent] = useState(null); // the skill open in 'edit'
  const [error, setError] = useState(null);
  const [message, setMessage] = useState(null);
  const [busy, setBusy] = useState(false);

  const load = () => mddbClient
    .listChatSkills()
    .then((data) => {
      setSkills(data.skills || []);
      setVersion(data.version || 0);
    })
    .catch((err) => setError(err.message));

  useEffect(() => {
    load();
  }, []);

  const show = (nextMode, skill = null) => {
    setMode(nextMode);
    setCurrent(skill);
    setError(null);
    setMessage(null);
  };

  const open = (name) => mddbClient.getChatSkill(name).then((skill) => show('edit', skill), (err) => setError(err.message));

  // Runs a write, reloads the list and reopens `name`.
  const run = async (name, action) => {
    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      const done = await action();
      await load();
      setCurrent(await mddbClient.getChatSkill(name));
      setMode('edit');
      setMessage(done);
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  };

  const fail = (text) => {
    setMessage(null);
    setError(text);
  };

  // Saves the editor's form or an import's preview, once it can be stored.
  const save = (form) => {
    const problem = skillProblem(form);
    if (problem) {
      fail(problem);
      return;
    }
    const name = form.name.trim();
    void run(name, async () => {
      const stored = await mddbClient.putChatSkill(name, fromSkillForm(form));
      return `Saved ${name}, version ${stored.version}. Scenarios using it get the new text on the chat service's next poll.`;
    });
  };

  const remove = async () => {
    if (!confirm(`Delete the skill "${current.name}" and its earlier versions?`)) return;
    setBusy(true);
    try {
      await mddbClient.deleteChatSkill(current.name);
      show(null);
      setMessage(`Deleted ${current.name}.`);
      await load();
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <Sparkles className="h-6 w-6 text-accent" aria-hidden="true" />
          <h2 className="text-xl font-bold text-fg">Chat skills</h2>
          <span className="text-xs text-fg-subtle">version {version}</span>
        </div>
        <div className="flex gap-2">
          <button type="button" onClick={() => show('import')} className="flex items-center gap-1.5 rounded border border-line-strong bg-surface px-3 py-1.5 text-sm text-fg-soft hover:bg-surface-subtle">
            <Download className="h-4 w-4" aria-hidden="true" /> Import from URL
          </button>
          <button type="button" onClick={() => show('new')} className="flex items-center gap-1.5 rounded bg-primary-700 px-3 py-1.5 text-sm text-white hover:bg-primary-800">
            <Plus className="h-4 w-4" aria-hidden="true" /> New skill
          </button>
        </div>
      </div>
      <p className="mb-4 max-w-3xl text-sm text-fg-muted">
        A skill is a block of instructions in the SKILL.md format that scenarios attach. It is added to the prompt after the
        guardrails, which take precedence. Skills are part of system prompts, so every action here needs admin on &quot;*&quot;.
      </p>

      {error && <p role="alert" className="mb-3 rounded border border-danger-line bg-danger-soft p-2 text-sm text-danger-strong">{error}</p>}
      {message && <output className="mb-3 block rounded border border-success-line bg-success-soft p-2 text-sm text-success-strong">{message}</output>}

      <div className="grid gap-4 lg:grid-cols-[16rem_minmax(0,1fr)]">
        <nav aria-label="Skills" className="rounded-lg bg-surface p-3 shadow">
          <ChatSkillList skills={skills} selected={current?.name} onSelect={open} />
        </nav>
        {mode === null && <p className="rounded-lg bg-surface p-6 text-sm text-fg-muted shadow">Pick a skill to edit it, write a new one, or import one.</p>}
        {mode === 'import' && <ChatSkillImport busy={busy} onSave={save} onError={fail} />}
        {mode === 'new' && <ChatSkillEditor key="new" isNew busy={busy} onSave={save} onError={fail} />}
        {mode === 'edit' && (
          <div className="grid gap-4 xl:grid-cols-2">
            <ChatSkillEditor key={`${current.name}@${current.version}`} skill={current} busy={busy} onSave={save} onDelete={remove} onError={fail} />
            <div className="space-y-4">
              {current.source && <ChatSkillUpdate key={current.version} skill={current} onSaved={(done) => run(current.name, async () => done)} onError={fail} />}
              <ChatSkillHistory key={current.version} skill={current} onError={fail} />
            </div>
          </div>
        )}
      </div>
    </>
  );
}
