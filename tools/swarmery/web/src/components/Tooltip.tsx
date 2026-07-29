// The themed replacement for the browser's native `title=` tooltip.
//
// Native tooltips are unstyleable, appear after ~1s at an OS-decided position,
// and render in the OS chrome — a grey Windows/macOS box floating over a warm
// near-black dashboard. Every `title=` in this app is now `data-tip=`, and this
// single document-level layer draws them in the marketplace language: hairline
// card, surface fill, the same portal positioning as <Explain> and <HoverTip>.
//
// Why one global listener rather than a <Tooltip> wrapper component:
//  · converting a call site is an attribute rename, not a re-nesting — no
//    wrapper element is inserted into any flex/grid row, so no layout moves;
//  · `data-tip={cond ? 'why' : undefined}` keeps working exactly like `title`
//    did (React omits an undefined attribute, so the tip simply does not exist);
//  · one portal node exists for the whole app instead of one per tipped element.
//
// Deliberately NOT a replacement for <Explain> (click-driven, keyboard-first,
// multi-part concept panels) or <HoverTip> (rich JSX cards on dense data rows).
// This is the plain-string tier: one short line of supporting text.

import { useCallback, useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useAnchoredLayer, type LayerPlacement } from './useAnchoredLayer';

/** Attribute carrying the tip text. */
const TIP_ATTR = 'data-tip';
/** Optional per-site placement override: `data-tip-place="side"`. */
const PLACE_ATTR = 'data-tip-place';
const TIP_SELECTOR = `[${TIP_ATTR}]`;

/** Long enough not to flash while the pointer crosses a toolbar, short enough
 * to feel like an answer rather than the OS's ~1s delay. */
const SHOW_DELAY_MS = 200;

/** id of the single live tooltip node, pointed at by aria-describedby. */
const TIP_ID = 'app-tooltip';

interface Tip {
  text: string;
  place: LayerPlacement;
  /** Monospace body — paths, ids, branch names, cron expressions. */
  mono: boolean;
}

/** Read the tip payload off an element, or null when it carries no usable text. */
function readTip(el: HTMLElement): Tip | null {
  const text = el.getAttribute(TIP_ATTR);
  if (text === null || text.trim() === '') return null;
  return {
    text,
    place: el.getAttribute(PLACE_ATTR) === 'side' ? 'side' : 'below',
    mono: el.hasAttribute('data-tip-mono'),
  };
}

/**
 * Resolve the tipped element under a pointer event.
 *
 * `closest()` alone misses disabled controls: a disabled <button> dispatches no
 * pointer events, so the event surfaces on an ancestor and the button — a
 * DESCENDANT of that target — is invisible to closest(). Those are exactly the
 * sites whose tip matters most ("nothing to kill", "read-only — daemon started
 * without SWARMERY_ONBOARD_ROOTS"): the tip is the reason the control is dead.
 * So when the ancestor walk comes up empty, hit-test the point instead —
 * elementFromPoint still returns disabled controls. One extra hit test, only on
 * pointer events over untipped regions, and only when crossing an element
 * boundary (pointerover, not pointermove).
 */
function tipTargetAt(e: PointerEvent): HTMLElement | null {
  const target = e.target as Element | null;
  const viaTree = target?.closest?.(TIP_SELECTOR) ?? null;
  if (viaTree !== null) return viaTree as HTMLElement;
  const hit = document.elementFromPoint(e.clientX, e.clientY);
  return (hit?.closest(TIP_SELECTOR) ?? null) as HTMLElement | null;
}

export function TooltipLayer(): JSX.Element | null {
  const [tip, setTip] = useState<Tip | null>(null);
  const { anchor, layerRef, openAt, close } = useAnchoredLayer(tip?.place ?? 'below');
  const timer = useRef<number | null>(null);
  /** The element the tip currently belongs to (shown or still pending). */
  const owner = useRef<HTMLElement | null>(null);

  const hide = useCallback((): void => {
    if (timer.current !== null) window.clearTimeout(timer.current);
    timer.current = null;
    // aria-describedby is added by us and unmanaged by React, so it is ours to
    // clean up — a stale one would describe the element with a removed node.
    owner.current?.removeAttribute('aria-describedby');
    owner.current = null;
    setTip(null);
    close();
  }, [close]);

  const schedule = useCallback(
    (el: HTMLElement): void => {
      const next = readTip(el);
      if (next === null) return;
      owner.current = el;
      timer.current = window.setTimeout(() => {
        timer.current = null;
        // Re-read at fire time: a row can re-render during the delay (live WS
        // updates are constant here) and the text may have moved on.
        const fresh = readTip(el);
        if (fresh === null || !el.isConnected) return;
        el.setAttribute('aria-describedby', TIP_ID);
        setTip(fresh);
        openAt(el.getBoundingClientRect());
      }, SHOW_DELAY_MS);
    },
    [openAt],
  );

  useEffect(() => {
    const onPointerOver = (e: PointerEvent): void => {
      // Touch has no hover state; a tip that latches on tap and stays there is
      // worse than none. Coarse pointers get the visible label only.
      if (e.pointerType === 'touch') return;
      const el = tipTargetAt(e);
      if (el === owner.current) return;
      hide();
      if (el !== null) schedule(el);
    };
    // Pointer left the document entirely (window edge, another app) — pointerover
    // will not fire again until it comes back, so nothing else would clear this.
    //
    // Bound to <html> WITHOUT capture, and that is the whole point: pointerleave
    // does not bubble, but it does traverse the capture path, so a capturing
    // document-level listener would fire every time the pointer left any child
    // of a tipped element — restarting the show delay on every micro-move inside
    // the control. Non-capture on documentElement fires only for <html> itself.
    const onPointerLeave = (): void => {
      hide();
    };
    // A tip explaining a button must not outlive the click that acts on it —
    // half these controls open a modal or navigate out from under it.
    const onPointerDown = (): void => {
      hide();
    };
    const onKeyDown = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') hide();
    };
    // Keyboard parity: tab to a tipped control and the tip appears. Gated on
    // :focus-visible so a mouse click does not leave a tip stuck behind the
    // pointer that already dismissed it.
    const onFocusIn = (e: FocusEvent): void => {
      const el = (e.target as Element | null)?.closest?.(TIP_SELECTOR) ?? null;
      if (el === null || !(el as HTMLElement).matches(':focus-visible')) return;
      hide();
      schedule(el as HTMLElement);
    };
    const onFocusOut = (): void => {
      hide();
    };

    document.addEventListener('pointerover', onPointerOver, true);
    document.documentElement.addEventListener('pointerleave', onPointerLeave);
    document.addEventListener('pointerdown', onPointerDown, true);
    document.addEventListener('keydown', onKeyDown, true);
    document.addEventListener('focusin', onFocusIn, true);
    document.addEventListener('focusout', onFocusOut, true);
    return () => {
      document.removeEventListener('pointerover', onPointerOver, true);
      document.documentElement.removeEventListener('pointerleave', onPointerLeave);
      document.removeEventListener('pointerdown', onPointerDown, true);
      document.removeEventListener('keydown', onKeyDown, true);
      document.removeEventListener('focusin', onFocusIn, true);
      document.removeEventListener('focusout', onFocusOut, true);
      if (timer.current !== null) window.clearTimeout(timer.current);
    };
  }, [hide, schedule]);

  if (tip === null || anchor === null) return null;

  return createPortal(
    <div
      ref={layerRef}
      id={TIP_ID}
      role="tooltip"
      // pointer-events-none is load-bearing, not decoration: a hoverable tip
      // would sit under the cursor on a `below` placement and fire pointerover
      // on itself, which reads as "left the control" and would flicker.
      className={
        'pointer-events-none fixed z-[60] max-w-[300px] rounded-lg border border-line-strong ' +
        'bg-surface px-2.5 py-1.5 text-ink-2 opacity-0 shadow-lg transition-opacity duration-100 ' +
        (tip.mono
          ? 'font-mono text-[10.5px] leading-[1.5] break-all'
          : 'text-[11.5px] leading-[1.45] break-words whitespace-pre-line')
      }
    >
      {tip.text}
    </div>,
    document.body,
  );
}
