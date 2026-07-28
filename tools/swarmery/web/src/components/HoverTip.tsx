// Lightweight hover tooltip: a styled details card rendered through a portal
// with fixed positioning (never clipped by the rail's overflow). Positioning is
// shared with the explainer popover via useAnchoredLayer.
//
// Pointer-only supplement — the same details stay reachable by expanding the
// row / opening the Timeline, so no focus handling is needed. (Deliberate
// contrast with <Explain>, which is click-driven and keyboard-reachable.)

import { useEffect, useRef } from 'react';
import { createPortal } from 'react-dom';
import { useAnchoredLayer } from './useAnchoredLayer';

const SHOW_DELAY_MS = 150;

export function useHoverTip(content: JSX.Element): {
  handlers: {
    onMouseEnter: (e: React.MouseEvent<HTMLElement>) => void;
    onMouseLeave: () => void;
  };
  portal: JSX.Element | null;
} {
  const { anchor, layerRef, openAt, close } = useAnchoredLayer();
  const timer = useRef<number | null>(null);

  const onMouseEnter = (e: React.MouseEvent<HTMLElement>): void => {
    // Measured synchronously — currentTarget is null by the time the timer fires.
    const rect = e.currentTarget.getBoundingClientRect();
    if (timer.current !== null) window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => {
      openAt(rect);
    }, SHOW_DELAY_MS);
  };

  const onMouseLeave = (): void => {
    if (timer.current !== null) window.clearTimeout(timer.current);
    timer.current = null;
    close();
  };

  useEffect(() => {
    return () => {
      if (timer.current !== null) window.clearTimeout(timer.current);
    };
  }, []);

  const portal =
    anchor === null
      ? null
      : createPortal(
          <div
            ref={layerRef}
            role="tooltip"
            className="pointer-events-none fixed z-50 w-[264px] opacity-0 transition-opacity duration-100"
          >
            {content}
          </div>,
          document.body,
        );

  return { handlers: { onMouseEnter, onMouseLeave }, portal };
}
