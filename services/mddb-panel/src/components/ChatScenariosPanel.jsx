import { useEffect, useState } from 'react';
import { MessageSquare, Plus, Save, Trash2 } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';
import { toForm, fromForm, formProblem } from '../lib/chat-scenario';
import ChatScenarioForm from './ChatScenarioForm';
import ChatScenarioList from './ChatScenarioList';
import ChatScenarioPreview from './ChatScenarioPreview';
import ChatSkillsPanel from './ChatSkillsPanel';
import ChatTabs from './ChatTabs';
import ChatMcpSecretsCard from './ChatMcpSecretsCard';

// #403: chat scenarios — an assistant's prompt, guardrails, allowed
// collections, style, model and limits — stored in MDDB and picked up by the
// chat service without a restart. Everyone who may read a scenario's
// collections sees it; saving and deleting need admin on "*".
//
// #404: the page has two tabs, the scenarios and the skills they attach.
// MCP-005: a scenario's external MCP servers, and a card for their secrets.

export default function ChatScenariosPanel() {
  const [tab, setTab] = useState('scenarios');
  return (
    <div className="h-full overflow-y-auto bg-gray-50 p-6">
      <ChatTabs tab={tab} onChange={setTab} />
      <div role="tabpanel" id={`chat-panel-${tab}`} aria-labelledby={`chat-tab-${tab}`}>
        {tab === 'scenarios' ? <ScenariosView /> : <ChatSkillsPanel />}
      </div>
    </div>
  );
}

function ScenariosView() {
  const { stats } = useStore();
  const [scenarios, setScenarios] = useState([]);
  const [skills, setSkills] = useState([]); // null: the list is admin only and was refused
  const [secrets, setSecrets] = useState([]); // null, likewise (MCP-005)
  const [version, setVersion] = useState(0);
  const [form, setForm] = useState(null);
  const [stored, setStored] = useState(null); // the name being edited; null for a new scenario
  const [error, setError] = useState(null);
  const [message, setMessage] = useState(null);
  const [busy, setBusy] = useState(false);

  const load = () => mddbClient
    .listChatScenarios()
    .then((data) => {
      setScenarios(data.scenarios || []);
      setVersion(data.version || 0);
    })
    .catch((err) => setError(err.message));

  const loadSecrets = () => mddbClient.listChatMcpSecrets().then((data) => setSecrets(data.secrets || []), () => setSecrets(null));

  useEffect(() => {
    load();
    loadSecrets();
    mddbClient.listChatSkills().then((data) => setSkills(data.skills || []), () => setSkills(null));
  }, []);

  const open = (scenario) => {
    setForm(toForm(scenario));
    setStored(scenario ? scenario.name : null);
    setError(null);
    setMessage(null);
  };

  // Runs a save or a delete, then reloads the list.
  const run = async (action) => {
    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      setMessage(await action());
      await load();
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  };

  const save = (e) => {
    e.preventDefault();
    const problem = formProblem(form);
    if (problem) {
      setError(problem);
      return;
    }
    const name = form.name.trim();
    run(async () => {
      await mddbClient.putChatScenario(name, fromForm(form));
      setStored(name);
      return `Saved ${name}. The chat service picks it up on its next poll.`;
    });
  };

  const remove = () => {
    if (!confirm(`Delete the scenario "${stored}"? An assistant using it falls back to its own configuration.`)) return;
    run(async () => {
      await mddbClient.deleteChatScenario(stored);
      setForm(null);
      setStored(null);
      return `Deleted ${stored}.`;
    });
  };

  const collections = (stats?.collections || []).map((c) => c.name);

  return (
    <>
      <div className="mb-4 flex items-center justify-between">
        <div className="flex items-center gap-2">
          <MessageSquare className="h-6 w-6 text-primary-700" aria-hidden="true" />
          <h2 className="text-xl font-bold text-gray-900">Chat scenarios</h2>
          <span className="text-xs text-gray-600">version {version}</span>
        </div>
        <button type="button" onClick={() => open()} className="flex items-center gap-1.5 rounded bg-primary-700 px-3 py-1.5 text-sm text-white hover:bg-primary-800">
          <Plus className="h-4 w-4" aria-hidden="true" /> New scenario
        </button>
      </div>
      <p className="mb-4 max-w-3xl text-sm text-gray-700">
        A scenario is one assistant: who it is, what it must not say, which collections it may search and how it answers.
        A value sent with a chat request wins over the scenario, and the scenario wins over the server default.
        Saving and deleting need admin on &quot;*&quot;.
      </p>

      {error && <p role="alert" className="mb-3 rounded border border-red-200 bg-red-50 p-2 text-sm text-red-800">{error}</p>}
      {message && <output className="mb-3 block rounded border border-green-200 bg-green-50 p-2 text-sm text-green-800">{message}</output>}

      <div className="grid gap-4 lg:grid-cols-[16rem_minmax(0,1fr)]">
        <nav aria-label="Scenarios" className="rounded-lg bg-white p-3 shadow">
          <ChatScenarioList scenarios={scenarios} selected={stored} onSelect={open} />
        </nav>

        {form === null ? (
          <p className="rounded-lg bg-white p-6 text-sm text-gray-700 shadow">Pick a scenario to edit it, or create a new one.</p>
        ) : (
          <div className="grid gap-4 xl:grid-cols-2">
            <form onSubmit={save} className="rounded-lg bg-white p-4 shadow">
              <ChatScenarioForm form={form} set={(field, value) => setForm((f) => ({ ...f, [field]: value }))} collections={collections} skills={skills} secrets={secrets} isNew={stored === null} />
              <div className="mt-4 flex items-center gap-2">
                <button type="submit" disabled={busy} className="flex items-center gap-1.5 rounded bg-primary-700 px-3 py-1.5 text-sm text-white hover:bg-primary-800 disabled:opacity-50">
                  <Save className="h-4 w-4" aria-hidden="true" /> {busy ? 'Working…' : 'Save'}
                </button>
                {stored !== null && (
                  <button type="button" onClick={remove} disabled={busy} className="flex items-center gap-1.5 rounded px-3 py-1.5 text-sm text-red-700 hover:bg-red-50 disabled:opacity-50">
                    <Trash2 className="h-4 w-4" aria-hidden="true" /> Delete
                  </button>
                )}
              </div>
            </form>
            {stored === null ? (
              <p className="rounded-lg bg-white p-6 text-sm text-gray-700 shadow">Save the scenario to see its composed prompt.</p>
            ) : (
              <ChatScenarioPreview name={stored} version={version} />
            )}
          </div>
        )}
      </div>
      <ChatMcpSecretsCard secrets={secrets} onChanged={loadSecrets} />
    </>
  );
}
