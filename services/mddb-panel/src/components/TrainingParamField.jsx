import { inputClass, labelClass, hintClass } from './training-styles';
import { parameterValue } from '../lib/training';

/**
 * One parameter a trainer profile declares, as the field its type calls
 * for: a number with the profile's min and max, a list of the allowed
 * values, a switch, or a line of text. The server checks the value again.
 */
export default function TrainingParamField({ param, value, onChange, idPrefix }) {
  const id = `${idPrefix}-${param.name}`;
  const set = (text) => onChange(param.name, parameterValue(param, text));
  const hint = param.description && <p id={`${id}-hint`} className={`${hintClass} mt-1`}>{param.description}</p>;
  const describedBy = param.description ? `${id}-hint` : undefined;

  if (param.type === 'bool') {
    return (
      <div>
        <label htmlFor={id} className="flex items-center gap-2 text-sm text-fg">
          <input id={id} type="checkbox" role="switch" checked={Boolean(value)} aria-describedby={describedBy}
            onChange={(e) => set(e.target.checked)} />
          {param.name}
        </label>
        {hint}
      </div>
    );
  }
  let control;
  if (param.type === 'enum') {
    control = (
      <select id={id} className={inputClass} value={value ?? ''} aria-describedby={describedBy} onChange={(e) => set(e.target.value)}>
        {!param.required && <option value="">(the trainer&apos;s own default)</option>}
        {param.values.map((v) => <option key={v} value={v}>{v}</option>)}
      </select>
    );
  } else if (param.type === 'int' || param.type === 'float') {
    control = (
      <input id={id} type="number" className={inputClass} value={value ?? ''} min={param.min} max={param.max}
        step={param.type === 'int' ? 1 : 'any'} aria-describedby={describedBy} onChange={(e) => set(e.target.value)} />
    );
  } else {
    control = (
      <input id={id} type="text" className={inputClass} value={value ?? ''} maxLength={param.maxLength}
        aria-describedby={describedBy} onChange={(e) => set(e.target.value)} />
    );
  }
  return (
    <div>
      <label htmlFor={id} className={labelClass}>
        {param.name}{param.required && ' (required)'}
      </label>
      {control}
      {hint}
    </div>
  );
}
