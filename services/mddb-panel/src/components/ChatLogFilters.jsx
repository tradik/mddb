import { inputClass, labelClass } from '../lib/chat-scenario';
import { outcomeLabels } from '../lib/chat-log';

// #406: the Log tab's filters — scenario, date range, the three shortcuts an
// operator reaches for (thumbs-down, no results, refused) and the outcome.

const checkClass = 'h-4 w-4 rounded border-gray-400 text-primary-700 focus:ring-primary-500';

function Check({ label, checked, onChange }) {
  return (
    <label className="flex items-center gap-2 text-sm text-gray-800">
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} className={checkClass} />
      {label}
    </label>
  );
}

export default function ChatLogFilters({ filters, scenarios, onChange }) {
  const set = (field, value) => onChange({ ...filters, [field]: value });
  return (
    <fieldset className="mb-4 rounded-lg bg-white p-3 shadow">
      <legend className="sr-only">Log filters</legend>
      <div className="grid gap-3 md:grid-cols-4">
        <div>
          <label htmlFor="chat-log-scenario" className={labelClass}>Scenario</label>
          <select id="chat-log-scenario" value={filters.scenario} onChange={(e) => set('scenario', e.target.value)} className={inputClass}>
            <option value="">All scenarios</option>
            {scenarios.map((name) => <option key={name} value={name}>{name}</option>)}
          </select>
        </div>
        <div>
          <label htmlFor="chat-log-from" className={labelClass}>From</label>
          <input id="chat-log-from" type="date" value={filters.from} onChange={(e) => set('from', e.target.value)} className={inputClass} />
        </div>
        <div>
          <label htmlFor="chat-log-to" className={labelClass}>To</label>
          <input id="chat-log-to" type="date" value={filters.to} onChange={(e) => set('to', e.target.value)} className={inputClass} />
        </div>
        <div>
          <label htmlFor="chat-log-outcome" className={labelClass}>Outcome</label>
          <select id="chat-log-outcome" value={filters.outcome} onChange={(e) => set('outcome', e.target.value)} className={inputClass}>
            <option value="">Any outcome</option>
            {Object.entries(outcomeLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          </select>
        </div>
      </div>
      <div className="mt-3 flex flex-wrap gap-4">
        <Check label="Thumbs-down only" checked={filters.thumbsDown} onChange={(v) => set('thumbsDown', v)} />
        <Check label="No results" checked={filters.noResults} onChange={(v) => set('noResults', v)} />
        <Check label="Refused / off-topic" checked={filters.refused} onChange={(v) => set('refused', v)} />
      </div>
    </fieldset>
  );
}
