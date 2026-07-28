// The in-UI explainer affordance: a small "?" (reference) or "!" (needs action)
// circle that opens a popover with the concept's short definition, what to do
// about it, its hard numbers, and a deep link into /docs.
//
// Click-driven, not hover: this is something the user deliberately opens, so it
// is a real <button> with keyboard access and Escape-to-close. Hover-on-dense-
// data-rows stays HoverTip's job.

import { useCallback, useEffect, useId, useRef } from 'react';
import { createPortal } from 'react-dom';
import { Link } from 'react-router-dom';
import { CONCEPTS, type Concept, type ConceptId } from '../lib/glossary';
import { useAnchoredLayer } from './useAnchoredLayer';

const TONE = {
  explain: {
    glyph: '?',
    button: 'border-line text-ink-faint hover:border-ink-dim hover:text-ink-dim',
    eyebrow: 'text-violet-400',
    card: 'border-violet-500/30',
  },
  action: {
    glyph: '!',
    button: 'border-amber-500/50 text-amber-500 hover:border-amber-500 hover:text-amber-400',
    eyebrow: 'text-amber-500',
    card: 'border-amber-500/40',
  },
} as const;

export function Explain({ id }: { id: ConceptId }): JSX.Element {
  // Annotated as Concept, NOT `(typeof CONCEPTS)[ConceptId]`: the latter is a
  // union of the literal entry types, and reading `.actions` off a member that
  // has no `actions` key is a type error. Widening to Concept makes every
  // optional field readable, which is exactly what this component needs.
  const concept: Concept = CONCEPTS[id];
  const tone = TONE[concept.tone];
  const panelId = useId();
  const btnRef = useRef<HTMLButtonElement | null>(null);
  const { anchor, layerRef, openAt, close } = useAnchoredLayer();
  const open = anchor !== null;

  const toggle = useCallback(
    (e: React.MouseEvent<HTMLButtonElement>): void => {
      // These chips live inside clickable cards and <Link> rows — never let the
      // explainer double as a navigation click.
      e.preventDefault();
      e.stopPropagation();
      if (open) close();
      else openAt(e.currentTarget.getBoundingClientRect());
    },
    [open, close, openAt],
  );

  // Escape + outside-pointer close. The guard keeps the button's own pointerdown
  // (which precedes its click) from closing the panel the click is about to open.
  useEffect(() => {
    if (!open) return;
    const onKey = (ev: KeyboardEvent): void => {
      if (ev.key === 'Escape') close();
    };
    const onPointerDown = (ev: PointerEvent): void => {
      const target = ev.target as Node | null;
      if (target !== null && (btnRef.current?.contains(target) === true || layerRef.current?.contains(target) === true)) return;
      close();
    };
    window.addEventListener('keydown', onKey);
    window.addEventListener('pointerdown', onPointerDown, true);
    return () => {
      window.removeEventListener('keydown', onKey);
      window.removeEventListener('pointerdown', onPointerDown, true);
    };
  }, [open, close, layerRef]);

  const panel = !open
    ? null
    : createPortal(
        <div
          ref={layerRef}
          id={panelId}
          role="dialog"
          aria-label={concept.term}
          className={`fixed z-50 w-[300px] rounded-xl border ${tone.card} bg-surface px-3.5 py-3 opacity-0 shadow-lg transition-opacity duration-100`}
        >
          <div className={`font-mono text-[10.5px] tracking-[0.08em] uppercase ${tone.eyebrow}`}>
            {concept.term}
          </div>
          <p className="mt-1.5 text-[12px] leading-relaxed text-ink-2">{concept.short}</p>

          {concept.actions !== undefined && (
            <ul className="mt-2.5 space-y-1 border-t border-line pt-2.5">
              {concept.actions.map((a) => (
                <li key={a} className="flex gap-1.5 text-[11.5px] leading-relaxed text-ink-dim">
                  <span aria-hidden="true" className="text-ink-faint">
                    •
                  </span>
                  <span className="min-w-0">{a}</span>
                </li>
              ))}
            </ul>
          )}

          {concept.steps !== undefined && (
            <ol className="mt-2.5 space-y-1.5 border-t border-line pt-2.5">
              {concept.steps.map((s, i) => (
                <li key={s.title} className="text-[11.5px] leading-relaxed">
                  <span className="font-mono text-[10px] text-ink-faint">{i + 1}. </span>
                  <span className="font-semibold text-ink">{s.title}</span>
                  <span className="text-ink-dim"> — {s.body}</span>
                </li>
              ))}
            </ol>
          )}

          {concept.facts !== undefined && (
            <dl className="mt-2.5 flex flex-wrap gap-x-3 gap-y-1 border-t border-line pt-2.5 font-mono text-[10.5px]">
              {concept.facts.map((f) => (
                <div key={f.label} className="flex gap-1">
                  <dt className="text-ink-faint">{f.label}</dt>
                  <dd className="text-ink-dim">{f.value}</dd>
                </div>
              ))}
            </dl>
          )}

          {concept.doc !== undefined && (
            <Link
              to={`/docs/${concept.doc.slug}#${concept.doc.anchor}`}
              onClick={close}
              className="mt-2.5 inline-block font-mono text-[10.5px] text-brand hover:underline"
            >
              Read more →
            </Link>
          )}
        </div>,
        document.body,
      );

  return (
    <>
      <button
        ref={btnRef}
        type="button"
        onClick={toggle}
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        aria-label={`What is ${concept.term}?`}
        className={`inline-flex h-[15px] w-[15px] shrink-0 items-center justify-center rounded-full border align-middle font-mono text-[9.5px] leading-none transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/60 ${tone.button}`}
      >
        {tone.glyph}
      </button>
      {panel}
    </>
  );
}
