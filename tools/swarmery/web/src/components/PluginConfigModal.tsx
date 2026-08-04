// Plugin config modal — the operator-facing form for a pack's declared
// .claude/project.json key (requirements.json → projectConfig[]). The form is
// GENERATED from the pack's own JSON Schema fragment (row.configSchema):
// string leaves, integer leaves (minimum + default), and object nesting into
// fieldsets. Nothing about any specific pack's vocabulary is hardcoded here —
// that is the whole point of the contract (Phase 1's requirements.json).
//
// `default` renders as a placeholder ONLY — a value the operator never typed
// is never silently saved. Prefill comes solely from `configCurrent`. Server
// 422 `problems` (one line per dotted field path, e.g. "child.leaf is
// required") are mapped back onto their field by prefix match; anything that
// doesn't match a rendered leaf (e.g. "unknown field: xyz") falls through to
// the general ErrorBox — same shape as AttachModal/DetachModal.

import { useId, useMemo, useState } from 'react';
import type { ProjectPluginRow } from '../api/types';
import { ConfigValidationError, putProjectConfig } from '../api';
import { ErrorBox } from './ui';

/** The subset of JSON Schema the contract allows (Phase 1): string / integer
 * leaves, object nesting, required + default + minimum. Everything else on
 * the wire is ignored. */
interface SchemaNode {
  type?: string;
  description?: string;
  properties?: Record<string, SchemaNode>;
  required?: string[];
  default?: unknown;
  minimum?: number;
}

type FormValue = Record<string, unknown>;

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

function isObjectSchema(schema: SchemaNode): schema is SchemaNode & { properties: Record<string, SchemaNode> } {
  return schema.type === 'object' && schema.properties !== undefined;
}

/** Immutable set at a dotted path — intermediate objects are shallow-cloned. */
function setAt(obj: FormValue, path: string[], value: unknown): FormValue {
  const [head, ...rest] = path;
  if (head === undefined) return obj;
  const next: FormValue = { ...obj };
  if (rest.length === 0) {
    next[head] = value;
  } else {
    const child = isRecord(obj[head]) ? (obj[head] as FormValue) : {};
    next[head] = setAt(child, rest, value);
  }
  return next;
}

/** True when a value (leaf or nested object) has nothing meaningful in it —
 * an untouched optional object must never be submitted as `{}`. */
function isEmptyValue(value: unknown): boolean {
  if (value === undefined || value === null || value === '') return true;
  if (isRecord(value)) return Object.values(value).every(isEmptyValue);
  return false;
}

/** Builds the JSON value to submit: typed leaves, empty optional leaves and
 * empty optional objects OMITTED entirely (never a silently-saved default). */
function buildSubmitValue(schema: SchemaNode, value: unknown): unknown {
  if (isObjectSchema(schema)) {
    const out: FormValue = {};
    for (const [key, child] of Object.entries(schema.properties)) {
      const childValue = isRecord(value) ? value[key] : undefined;
      if (isEmptyValue(childValue)) continue;
      out[key] = buildSubmitValue(child, childValue);
    }
    return out;
  }
  if (schema.type === 'integer') {
    if (typeof value === 'number') return value;
    if (typeof value === 'string' && value.trim() !== '') {
      const n = Number(value);
      if (Number.isFinite(n)) return n;
    }
    return undefined;
  }
  return value;
}

/** Client-side required check, dotted-path keyed — same shape as the
 * server's `problems`, so client and server errors render identically. */
function validateRequired(schema: SchemaNode, value: unknown, prefix: string[] = []): Record<string, string> {
  const errors: Record<string, string> = {};
  if (!isObjectSchema(schema)) return errors;
  const required = schema.required ?? [];
  for (const [key, child] of Object.entries(schema.properties)) {
    const path = [...prefix, key];
    const childValue = isRecord(value) ? value[key] : undefined;
    if (isObjectSchema(child)) {
      Object.assign(errors, validateRequired(child, childValue, path));
    } else if (required.includes(key) && isEmptyValue(childValue)) {
      errors[path.join('.')] = `${path.join('.')} is required`;
    }
  }
  return errors;
}

/** First leaf field's dotted path, in schema property order — the field that
 * gets `autoFocus` on open, computed once regardless of nesting depth. */
function firstLeafPath(schema: SchemaNode, prefix: string[] = []): string | null {
  if (!isObjectSchema(schema)) return null;
  for (const [key, child] of Object.entries(schema.properties)) {
    const path = [...prefix, key];
    if (isObjectSchema(child)) {
      const nested = firstLeafPath(child, path);
      if (nested !== null) return nested;
    } else {
      return path.join('.');
    }
  }
  return null;
}

/** Every rendered leaf's dotted path, longest first — used to place a server
 * problem line by PREFIX match, since the server sends a human sentence
 * ("child.leaf is required"), not a machine field pointer. Longest-first so
 * "child.leaf" wins over "child" for the same line. */
function leafPaths(schema: SchemaNode, prefix: string[] = []): string[] {
  if (!isObjectSchema(schema)) return [];
  const out: string[] = [];
  for (const [key, child] of Object.entries(schema.properties)) {
    const path = [...prefix, key];
    if (isObjectSchema(child)) out.push(...leafPaths(child, path));
    else out.push(path.join('.'));
  }
  return out.sort((a, b) => b.length - a.length);
}

type Phase = { kind: 'editing' } | { kind: 'saving' };

export function PluginConfigModal({
  projectId,
  row,
  onClose,
  onSaved,
}: {
  projectId: number;
  row: ProjectPluginRow;
  onClose: () => void;
  onSaved: () => void;
}): JSX.Element {
  const titleId = useId();
  const schema = row.configSchema as SchemaNode | undefined;
  const [value, setValue] = useState<FormValue>(
    isRecord(row.configCurrent) ? (row.configCurrent as FormValue) : {},
  );
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [generalError, setGeneralError] = useState<string | null>(null);
  const [phase, setPhase] = useState<Phase>({ kind: 'editing' });
  const busy = phase.kind === 'saving';
  const firstPath = useMemo(() => (schema !== undefined ? firstLeafPath(schema) : null), [schema]);
  const knownLeaves = useMemo(() => (schema !== undefined ? leafPaths(schema) : []), [schema]);

  function onKeyDown(e: React.KeyboardEvent<HTMLDivElement>): void {
    if (e.key === 'Escape' && !busy) onClose();
  }

  async function save(): Promise<void> {
    if (schema === undefined || row.configKey === undefined) return;
    const errors = validateRequired(schema, value);
    if (Object.keys(errors).length > 0) {
      setFieldErrors(errors);
      setGeneralError(null);
      return;
    }
    setPhase({ kind: 'saving' });
    setFieldErrors({});
    setGeneralError(null);
    const submitValue = buildSubmitValue(schema, value);
    try {
      await putProjectConfig(projectId, row.configKey, submitValue);
      onSaved();
    } catch (e) {
      setPhase({ kind: 'editing' });
      if (e instanceof ConfigValidationError) {
        const byField: Record<string, string> = {};
        const unmatched: string[] = [];
        for (const problem of e.problems) {
          const field = knownLeaves.find((p) => problem.startsWith(p));
          if (field === undefined) {
            unmatched.push(problem);
            continue;
          }
          // Accumulate rather than overwrite: the server may report more than
          // one problem for the same leaf, and dropping the earlier one would
          // silently hide a validation failure the operator has to fix.
          const prior = byField[field];
          byField[field] = prior === undefined ? problem : `${prior}; ${problem}`;
        }
        setFieldErrors(byField);
        setGeneralError(unmatched.length > 0 ? unmatched.join('; ') : null);
      } else {
        setGeneralError(e instanceof Error ? e.message : String(e));
      }
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-bg/70 p-4"
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      onClick={busy ? undefined : onClose}
      onKeyDown={onKeyDown}
    >
      <div
        className="w-full max-w-lg rounded-xl border border-line bg-surface px-4 py-4"
        onClick={(e) => e.stopPropagation()}
      >
        <div id={titleId} className="font-display text-[14px] font-bold text-ink">
          {row.configTitle ?? row.name}
        </div>
        {row.configWhy !== undefined && row.configWhy !== '' && (
          <div className="mt-1 text-[12px] leading-relaxed text-ink-dim">{row.configWhy}</div>
        )}
        {row.configDocs !== undefined && row.configDocs !== '' && (
          <div className="mt-1.5 font-mono text-[10.5px] text-ink-faint">docs: {row.configDocs}</div>
        )}

        {schema === undefined || !isObjectSchema(schema) || row.configKey === undefined ? (
          <ErrorBox message="this pack's config schema is missing or malformed" />
        ) : (
          <form
            className="mt-3"
            onSubmit={(e) => {
              e.preventDefault();
              void save();
            }}
          >
            <SchemaFields
              schema={schema}
              path={[]}
              value={value}
              onChange={(path, v) => setValue((cur) => setAt(cur, path, v))}
              fieldErrors={fieldErrors}
              firstPath={firstPath}
            />

            {generalError !== null && <ErrorBox message={generalError} />}

            <div className="mt-4 flex justify-end gap-2">
              <button
                type="button"
                onClick={onClose}
                disabled={busy}
                className="rounded-lg border border-line bg-surface px-3.5 py-1.5 font-mono text-[11.5px] text-ink-2 transition-colors hover:bg-surface2 disabled:opacity-50"
              >
                cancel
              </button>
              <button
                type="submit"
                disabled={busy}
                className="rounded-lg border border-green/40 bg-green/10 px-3.5 py-1.5 font-mono text-[11.5px] font-semibold text-green transition-colors hover:bg-green/20 disabled:opacity-50"
              >
                {busy ? '…' : 'save'}
              </button>
            </div>
          </form>
        )}

        {(schema === undefined || !isObjectSchema(schema) || row.configKey === undefined) && (
          <div className="mt-4 flex justify-end">
            <button
              type="button"
              onClick={onClose}
              className="rounded-lg border border-line bg-surface px-3.5 py-1.5 font-mono text-[11.5px] text-ink-2 transition-colors hover:bg-surface2"
            >
              close
            </button>
          </div>
        )}
      </div>
    </div>
  );
}

function SchemaFields({
  schema,
  path,
  value,
  onChange,
  fieldErrors,
  firstPath,
}: {
  schema: SchemaNode & { properties: Record<string, SchemaNode> };
  path: string[];
  value: unknown;
  onChange: (path: string[], value: unknown) => void;
  fieldErrors: Record<string, string>;
  firstPath: string | null;
}): JSX.Element {
  const required = schema.required ?? [];
  return (
    <div className="space-y-2.5">
      {Object.entries(schema.properties).map(([key, child]) => {
        const childPath = [...path, key];
        const dotted = childPath.join('.');
        const childValue = isRecord(value) ? value[key] : undefined;
        const isReq = required.includes(key);
        if (isObjectSchema(child)) {
          return (
            <fieldset key={dotted} className="rounded-lg border border-line-soft px-3 py-2.5">
              <legend className="px-1 font-mono text-[10px] tracking-[0.12em] text-ink-dim uppercase">
                {key}
                {isReq ? <span className="text-red"> *</span> : null}
              </legend>
              {child.description !== undefined && child.description !== '' && (
                <p className="mb-2 text-[11px] text-ink-faint">{child.description}</p>
              )}
              <SchemaFields
                schema={child}
                path={childPath}
                value={childValue}
                onChange={onChange}
                fieldErrors={fieldErrors}
                firstPath={firstPath}
              />
            </fieldset>
          );
        }
        return (
          <LeafField
            key={dotted}
            fieldKey={key}
            path={childPath}
            schema={child}
            value={childValue}
            required={isReq}
            error={fieldErrors[dotted]}
            autoFocus={dotted === firstPath}
            onChange={onChange}
          />
        );
      })}
    </div>
  );
}

function LeafField({
  fieldKey,
  path,
  schema,
  value,
  required,
  error,
  autoFocus,
  onChange,
}: {
  fieldKey: string;
  path: string[];
  schema: SchemaNode;
  value: unknown;
  required: boolean;
  error: string | undefined;
  autoFocus: boolean;
  onChange: (path: string[], value: unknown) => void;
}): JSX.Element {
  const dotted = path.join('.');
  const fieldId = `cfg-${dotted.replace(/\./g, '-')}`;
  const hintId = `${fieldId}-hint`;
  const errorId = `${fieldId}-error`;
  const isInteger = schema.type === 'integer';
  const hasDescription = schema.description !== undefined && schema.description !== '';
  const describedBy = [hasDescription ? hintId : null, error !== undefined ? errorId : null]
    .filter((v): v is string => v !== null)
    .join(' ');

  return (
    <div>
      <label htmlFor={fieldId} className="block font-mono text-[10.5px] tracking-[0.12em] text-ink-dim uppercase">
        {fieldKey}
        {required ? <span className="text-red"> *</span> : null}
      </label>
      <input
        id={fieldId}
        type={isInteger ? 'number' : 'text'}
        min={isInteger ? schema.minimum : undefined}
        value={typeof value === 'string' || typeof value === 'number' ? value : ''}
        placeholder={schema.default !== undefined ? String(schema.default) : undefined}
        autoFocus={autoFocus}
        aria-describedby={describedBy === '' ? undefined : describedBy}
        aria-invalid={error !== undefined}
        onChange={(e) => onChange(path, e.target.value)}
        className={`mt-1 w-full rounded-lg border bg-bg px-2.5 py-1.5 font-mono text-[12.5px] text-ink outline-none focus:border-line-strong ${
          error !== undefined ? 'border-red/50' : 'border-line'
        }`}
      />
      {hasDescription && (
        <p id={hintId} className="mt-1 text-[10.5px] text-ink-faint">
          {schema.description}
        </p>
      )}
      {error !== undefined && (
        <p id={errorId} className="mt-1 text-[10.5px] text-red">
          {error}
        </p>
      )}
    </div>
  );
}
