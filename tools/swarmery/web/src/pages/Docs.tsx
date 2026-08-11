// Docs screen (Canvas): left DOCUMENTATION rail (mono eyebrow + title/FILE.md
// buttons, active item amber-tinted with a raised fill), right pane rendering
// the doc's markdown under a serif H1 and a "swarmery/docs/<FILE>" mono
// subline. Routes: /docs (first doc) and /docs/{slug}. The markdown body's
// own leading H1 is stripped — the pane title comes from the doc meta.

import { useEffect, useMemo, useState } from 'react';
import { Link, useLocation, useParams } from 'react-router-dom';
import type { DocDetail, DocMeta } from '../api/types';
import { fetchDoc, fetchDocs } from '../api';
import { Markdown } from '../lib/markdown';
import { Empty, ErrorBox, Loading } from '../components/ui';
import { groupDocs } from './docsRail';

/** Drop a leading `# Title` line — the pane renders its own heading. */
function stripLeadingH1(markdown: string): { title: string | null; body: string } {
  const lines = markdown.split('\n');
  let i = 0;
  while (i < lines.length && (lines[i] ?? '').trim() === '') i += 1;
  const m = /^#\s+(.*)$/.exec(lines[i] ?? '');
  if (m === null) return { title: null, body: markdown };
  return { title: m[1] ?? null, body: lines.slice(i + 1).join('\n') };
}

export function Docs(): JSX.Element {
  const { slug } = useParams<{ slug: string }>();
  const [docs, setDocs] = useState<DocMeta[] | null>(null);
  const [listError, setListError] = useState<string | null>(null);
  const [doc, setDoc] = useState<DocDetail | null>(null);
  const [docError, setDocError] = useState<string | null>(null);

  useEffect(() => {
    fetchDocs()
      .then((list) => {
        setDocs(list);
        setListError(null);
      })
      .catch((e: unknown) => setListError(String(e)));
  }, []);

  const activeSlug = slug ?? docs?.[0]?.slug ?? null;

  useEffect(() => {
    if (activeSlug === null) return;
    setDoc(null);
    setDocError(null);
    fetchDoc(activeSlug)
      .then(setDoc)
      .catch((e: unknown) => setDocError(String(e)));
  }, [activeSlug]);

  const { hash, key } = useLocation();

  // Deep links from <Explain>'s "Read more →" carry a heading anchor. The doc
  // body arrives asynchronously, so scroll after `doc` lands, not on mount.
  //
  // `key` is in the deps, not just `hash`: clicking an in-doc link to the
  // section you already came from is a same-URL navigation, so `hash` does not
  // change and a hash-only effect would silently do nothing. The router mints a
  // fresh key for every navigation, which is the signal that a jump was asked
  // for — this is what a native fragment anchor gives you for free and what we
  // give up by routing hash links (so that useLocation stays in sync at all).
  useEffect(() => {
    if (doc === null || hash === '') return;
    const raw = hash.slice(1);
    // A hand-typed or truncated hash can be invalid percent-encoding (`#%`),
    // and decodeURIComponent throws URIError on it. Thrown from inside an
    // effect that is a full-page crash, so a malformed anchor falls back to
    // the literal text — which simply matches no id.
    let id = raw;
    try {
      id = decodeURIComponent(raw);
    } catch {
      /* keep raw */
    }
    const el = document.getElementById(id);
    if (el !== null) el.scrollIntoView({ block: 'start' });
  }, [doc, hash, key]);

  const rendered = useMemo(() => (doc === null ? null : stripLeadingH1(doc.markdown)), [doc]);
  const groups = useMemo(() => groupDocs(docs ?? []), [docs]);

  if (listError !== null) return <ErrorBox message={listError} />;
  if (docs === null) return <Loading label="docs…" />;
  if (docs.length === 0) return <Empty>no docs published by the daemon</Empty>;

  return (
    <div className="min-w-0 px-4 pt-6 pb-10 desk:px-10 desk:pt-[34px] desk:pb-[60px]">
      <div className="desk:grid desk:grid-cols-[220px_minmax(0,1fr)] desk:items-start desk:gap-7">
        {/* Sticky offset is relative to the <main> scroller (frame layout). */}
        <div className="min-w-0 desk:sticky desk:top-[85px]">
          {/* The old single "Documentation" eyebrow is now one eyebrow per
              group (Guides / Reference). With no guides embedded groupDocs
              returns just Reference, so the rail is the same flat list it has
              always been, under a different word. */}
          <nav className="flex flex-col gap-4" aria-label="Documentation pages">
            {groups.map((group) => (
              // role="group" + aria-labelledby, so the Guides/Reference
              // boundary exists for a screen reader too. Without it the rail
              // is one undifferentiated run of links — which was fine when it
              // WAS one list, and stops being fine the moment it is two.
              <div key={group.label} role="group" aria-labelledby={`docgroup-${group.label}`}>
                <div
                  id={`docgroup-${group.label}`}
                  className="mb-2.5 font-mono text-[10.5px] tracking-[0.14em] text-ink-faint uppercase"
                >
                  {group.label}
                </div>
                <div className="flex flex-col gap-0.5">
                  {group.items.map((d) => {
                    const active = d.slug === activeSlug;
                    return (
                      <Link
                        key={d.slug}
                        to={`/docs/${d.slug}`}
                        aria-current={active ? 'page' : undefined}
                        className={`min-h-[44px] rounded-lg px-3 py-[9px] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/60 ${
                          active ? 'bg-surface2' : 'hover:bg-surface2/50'
                        }`}
                      >
                        <span
                          className={`block text-[13px] font-semibold ${active ? 'text-brand' : 'text-ink'}`}
                        >
                          {d.title}
                        </span>
                        <span className="mt-px block font-mono text-[10px] text-ink-faint">
                          {d.file}
                        </span>
                      </Link>
                    );
                  })}
                </div>
              </div>
            ))}
          </nav>
        </div>

        <div className="mt-6 min-w-0 desk:mt-0">
          {docError !== null && <ErrorBox message={docError} />}
          {doc === null && docError === null && <Loading label="doc…" />}
          {doc !== null && rendered !== null && (
            <>
              <h1 className="font-display text-[22px] font-medium tracking-[-0.01em] desk:text-[26px]">
                {rendered.title ?? doc.title}
              </h1>
              <div className="mt-1 font-mono text-[10.5px] text-ink-faint">
                swarmery/docs/{doc.file}
              </div>
              <div className="mt-5 text-[14px] leading-[1.75] text-ink-2">
                {/* The one surface that renders a single body per page, so it
                    owns the heading-id namespace the deep links resolve against. */}
                <Markdown text={rendered.body} anchors />
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
