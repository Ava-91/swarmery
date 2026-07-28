// Shared positioning for portal-rendered floating layers (tooltips, explainer
// popovers). Fixed positioning so the rail's overflow can never clip the layer;
// placed to the LEFT of the anchor (the rail hugs the right viewport edge) with
// a right-side fallback, and clamped to the viewport on both axes.
//
// Takes a DOMRect rather than an element on purpose: React nulls out
// event.currentTarget once the handler returns, so every caller must measure
// synchronously in its own handler.

import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';

/** Viewport padding kept around a positioned layer. */
const EDGE = 8;
/** Distance between the anchor and the layer. */
const GAP = 10;

export function useAnchoredLayer(): {
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
    let left = anchor.left - w - GAP;
    if (left < EDGE) left = Math.min(anchor.right + GAP, window.innerWidth - w - EDGE);
    const top = Math.max(
      EDGE,
      Math.min(anchor.top + anchor.height / 2 - h / 2, window.innerHeight - h - EDGE),
    );
    layer.style.left = `${String(left)}px`;
    layer.style.top = `${String(top)}px`;
    layer.style.opacity = '1';
  }, [anchor]);

  return { anchor, layerRef, openAt, close };
}
