// Shared positioning for portal-rendered floating layers (tooltips, explainer
// popovers). Fixed positioning so the rail's overflow can never clip the layer,
// and clamped to the viewport on both axes.
//
// Two placements, because the callers anchor to different things:
//  · 'side'  — beside the anchor, vertically centred on it: left of it when the
//              room is there (the rail hugs the right viewport edge), right of
//              it otherwise. This is HoverTip's proven behaviour and the default.
//  · 'below' — under the anchor, horizontally centred on it, flipping above when
//              the room below is short. Right for a 15px chip sitting inline in
//              body text, where a side-placed 300px panel would cover the very
//              sentence it is explaining.
// Both pick their direction from measured room, never from absolute position.
//
// Takes a DOMRect rather than an element on purpose: React nulls out
// event.currentTarget once the handler returns, so every caller must measure
// synchronously in its own handler.

import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';

/** Viewport padding kept around a positioned layer. */
const EDGE = 8;
/** Distance between the anchor and the layer. */
const GAP = 10;

export type LayerPlacement = 'side' | 'below';

/** Clamp a coordinate so a `size`-long layer stays inside `extent` with EDGE padding. */
function clamp(pos: number, size: number, extent: number): number {
  return Math.max(EDGE, Math.min(pos, extent - size - EDGE));
}

export function useAnchoredLayer(placement: LayerPlacement = 'side'): {
  /** The captured anchor rect, or null when the layer is closed. */
  anchor: DOMRect | null;
  /** Attach to the portal element — positioning reads its measured size. */
  layerRef: React.MutableRefObject<HTMLDivElement | null>;
  openAt: (rect: DOMRect) => void;
  close: () => void;
} {
  const [anchor, setAnchor] = useState<DOMRect | null>(null);
  const layerRef = useRef<HTMLDivElement | null>(null);

  const openAt = useCallback((rect: DOMRect): void => {
    setAnchor(rect);
  }, []);

  const close = useCallback((): void => {
    setAnchor(null);
  }, []);

  // Any scroll or resize invalidates the captured rect — just close.
  useEffect(() => {
    if (anchor === null) return;
    window.addEventListener('scroll', close, true);
    window.addEventListener('resize', close);
    return () => {
      window.removeEventListener('scroll', close, true);
      window.removeEventListener('resize', close);
    };
  }, [anchor, close]);

  // Position after render, once the layer's real size is measurable.
  useLayoutEffect(() => {
    const layer = layerRef.current;
    if (anchor === null || layer === null) return;
    const { offsetWidth: w, offsetHeight: h } = layer;
    const { innerWidth: vw, innerHeight: vh } = window;
    let left: number;
    let top: number;

    if (placement === 'below') {
      // Flip above only when below genuinely has no room and above does.
      const fitsBelow = anchor.bottom + GAP + h <= vh - EDGE;
      const fitsAbove = anchor.top - GAP - h >= EDGE;
      top = fitsBelow || !fitsAbove ? anchor.bottom + GAP : anchor.top - GAP - h;
      top = clamp(top, h, vh);
      left = clamp(anchor.left + anchor.width / 2 - w / 2, w, vw);
    } else {
      const fitsLeft = anchor.left - GAP - w >= EDGE;
      left = clamp(fitsLeft ? anchor.left - w - GAP : anchor.right + GAP, w, vw);
      top = clamp(anchor.top + anchor.height / 2 - h / 2, h, vh);
    }

    layer.style.left = `${String(left)}px`;
    layer.style.top = `${String(top)}px`;
    layer.style.opacity = '1';
  }, [anchor, placement]);

  return { anchor, layerRef, openAt, close };
}
