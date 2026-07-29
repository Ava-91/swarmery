// The numbered "how it works" block that makes a feature page double as its own
// documentation. Steps come from the glossary so the copy has one home; the
// markup is unchanged from the two page-local copies this replaces.

import { STEP_CONCEPTS, type StepConceptId } from '../lib/glossary';

export function HowItWorks({
  id,
  className = '',
}: {
  id: StepConceptId;
  className?: string;
}): JSX.Element {
  const { steps } = STEP_CONCEPTS[id];
  return (
    <ol className={`grid gap-3 border-t border-line pt-5 sm:grid-cols-2 ${className}`}>
      {steps.map((step, i) => (
        <li key={step.title} className="flex gap-2.5">
          <span
            aria-hidden="true"
            className="mt-[1px] inline-flex h-[18px] w-[18px] shrink-0 items-center justify-center rounded-full border border-line font-mono text-[10px] text-ink-dim"
          >
            {i + 1}
          </span>
          <div className="min-w-0">
            <div className="text-[12.5px] font-semibold text-ink">{step.title}</div>
            <p className="mt-0.5 text-[12px] leading-relaxed text-ink-dim">{step.body}</p>
          </div>
        </li>
      ))}
    </ol>
  );
}
