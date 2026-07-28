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
import { CONCEPTS, type ConceptId } from '../lib/glossary';
import { useAnchoredLayer } from './useAnchoredLayer';

// Semantic theme roles, not Tailwind's default palette: --color-amber and
// --color-purple are re-tuned per palette in index.css (light mode darkens them
// to clear AA on a white ground), so these flip with the theme. A hard-coded
// default-palette hue would not — it reads 2.15:1 on the light surface.
const TONE = {
  explain: {
    glyph: '?',
    button: 'border-line text-ink-faint hover:border-ink-dim hover:text-ink-dim',
    eyebrow: 'text-purple',
    card: 'border-purple/30',
  },
  action: {
    glyph: '!',
    button: 'border-amber/50 text-amber hover:border-amber hover:text-amber',
    eyebrow: 'text-amber',
    card: 'border-amber/40',
  },
} as const;

// The circle stays 15px so it sits inside a line of body text; before:-inset-1.5
// grows the pointer/touch target to 27px, clearing the WCAG 2.2 AA 24px floor
// without changing what is drawn.
const TRIGGER_CLASS =
  'relative inline-flex h-[15px] w-[15px] shrink-0 items-center justify-center rounded-full ' +
  'border align-middle font-mono text-[9.5px] leading-none transition-colors ' +
  'before:absolute before:-inset-1.5 before:rounded-full ' +
  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/60';

export function Explain({ id }: { id: ConceptId }): JSX.Element {
  const concept = CONCEPTS[id];
  const tone = TONE[concept.tone];
  const panelId = useId();
  const btnRef = useRef<HTMLButtonElement | null>(null);
  const { anchor, layerRef, openAt, close } = useAnchoredLayer('below');
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

  // Move focus into the panel when it opens, so the dialog role is not a lie and
  // a keyboard user lands on the content they just asked for. preventScroll
  // matters: any scroll closes the layer (useAnchoredLayer), so a focus-induced
  // scroll would shut the panel the same frame it opened.
  useEffect(() => {
    if (open) layerRef.current?.focus({ preventScroll: true });
  }, [open, layerRef]);

  // Escape + outside-pointer close, following useDropdownDismiss
  // (pages/system/shared.tsx) — Escape closes AND returns focus to the trigger.
  // An outside pointer-down deliberately does NOT restore focus: the user is
  // already on their way somewhere else, and yanking focus back would fight the
  // element they just clicked.
  //
  // The trigger guard below is what stops the CLOSING click from flickering the
  // panel back open: pointerdown on the trigger would close it, and the click
  // that follows would then see open === false and immediately reopen.
  //
  // Escape is listened for in the CAPTURE phase, and that phase is load-bearing.
  // These chips are placed inside drawers that close themselves on Escape —
  // workspace/TaskDrawer.tsx (document, bubble) and workspace/PlanDocDrawer.tsx
  // (window, bubble). In the bubble phase a window listener runs *after* every
  // document one, so a bubble-phase stopPropagation() here would fire too late
  // and one Escape would close both the popover and its drawer. window is the
  // first node in the capture path, so capture + stopPropagation() makes the
  // topmost layer — this popover — the only thing that consumes the key. No
  // other keydown listener in this tree registers on capture (verified), so
  // nothing else is being pre-empted.
  useEffect(() => {
    if (!open) return;
    const onKey = (ev: KeyboardEvent): void => {
      if (ev.key !== 'Escape') return;
      ev.stopPropagation();
      close();
      btnRef.current?.focus({ preventScroll: true });
    };
    const onPointerDown = (ev: PointerEvent): void => {
      const target = ev.target as Node | null;
      if (target === null) return;
      const inTrigger = btnRef.current?.contains(target) === true;
      const inPanel = layerRef.current?.contains(target) === true;
      if (inTrigger || inPanel) return;
      close();
    };
    window.addEventListener('keydown', onKey, true);
    window.addEventListener('pointerdown', onPointerDown, true);
    return () => {
      window.removeEventListener('keydown', onKey, true);
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
          tabIndex={-1}
          // A portal escapes the DOM tree but NOT the React tree — clicks in here
          // still bubble to whatever clickable card renders the chip.
          onClick={(e) => {
            e.stopPropagation();
          }}
          className={`fixed z-50 w-[300px] rounded-xl border ${tone.card} bg-surface px-3.5 py-3 opacity-0 shadow-lg transition-opacity duration-100 focus:outline-none`}
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
                  {/* The <ol> already numbers this for AT — the visible numeral
                      would otherwise be read twice ("1. 1."). */}
                  <span aria-hidden="true" className="font-mono text-[10px] text-ink-faint">
                    {i + 1}.{' '}
                  </span>
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
        // A wrapping card (workspace/TaskCard.tsx) preventDefault()s Enter/Space
        // to act on itself, which would swallow the click this button
        // synthesizes and leave the popover unopenable from the keyboard.
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') e.stopPropagation();
        }}
        aria-expanded={open}
        aria-haspopup="dialog"
        aria-controls={open ? panelId : undefined}
        aria-label={`What is ${concept.term}?`}
        className={`${TRIGGER_CLASS} ${tone.button}`}
      >
        {tone.glyph}
      </button>
      {panel}
    </>
  );
}
