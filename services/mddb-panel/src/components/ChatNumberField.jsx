import { inputClass, labelClass } from '../lib/chat-scenario';

// A number input of the scenario editor (#403). An empty value means "not
// set by the scenario": the chat service's own default applies.
export default function ChatNumberField({ id, label, value, onChange, hint, step = '1', max }) {
  return (
    <div>
      <label htmlFor={id} className={labelClass}>{label}</label>
      <input id={id} type="number" min="0" max={max} step={step} value={value} onChange={(e) => onChange(e.target.value)} className={inputClass} />
      {hint && <p className="mt-1 text-xs text-gray-600">{hint}</p>}
    </div>
  );
}
