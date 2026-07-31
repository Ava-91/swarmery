// "Connect account" — the browser half of the daemon's own OAuth session.
//
// Rung 1 of multi-account usage reads each account's <configDir>/.credentials.json,
// which is where the `claude` CLI keeps its credential on Linux and Windows. On
// macOS the CLI uses the login Keychain instead, so a NON-DEFAULT account there
// has no readable credential at all and its card says "not connected" forever,
// no matter how many times the operator runs the suggested login command.
//
// This closes that: the operator authorizes swarmery itself, once. The daemon
// mints the PKCE challenge and holds the verifier; this component only opens the
// authorize URL and posts back the code the callback page displays. It never
// sees a token, and the code it does handle is single-use and useless without
// the verifier that never left the daemon.
//
// Rendered ONLY on a card that is in the login-hint state, so a connected
// account — the single-account default on every OS — is pixel-identical to what
// it was before this existed.

import { useState } from 'react';
import { completeUsageLogin, startUsageLogin } from '../../api';
import { useUsage } from '../../lib/usageData';

type Phase = 'idle' | 'starting' | 'awaiting-code' | 'submitting';

const btn =
  'rounded-[6px] border border-line-strong px-2 py-0.5 font-mono text-[10px] whitespace-nowrap text-ink-dim transition-colors hover:bg-surface2 hover:text-ink focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand disabled:opacity-50';

export function UsageConnect({ account }: { account: string }): JSX.Element {
  const { refresh } = useUsage();
  const [phase, setPhase] = useState<Phase>('idle');
  const [code, setCode] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [authorizeUrl, setAuthorizeUrl] = useState<string | null>(null);

  const busy = phase === 'starting' || phase === 'submitting';

  const begin = async (): Promise<void> => {
    setPhase('starting');
    setError(null);
    try {
      const { authorizeUrl: url } = await startUsageLogin(account);
      setAuthorizeUrl(url);
      // noopener/noreferrer: the authorization page must not get a handle on
      // this window. A blocked popup is not an error — the link below is the
      // fallback, and it is the same URL.
      window.open(url, '_blank', 'noopener,noreferrer');
      setPhase('awaiting-code');
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
      setPhase('idle');
    }
  };

  const submit = async (): Promise<void> => {
    setPhase('submitting');
    setError(null);
    try {
      await completeUsageLogin(account, code.trim());
      // Clear the code from component state the moment it is spent: it is
      // single-use, and there is no reason to keep it around.
      setCode('');
      setAuthorizeUrl(null);
      setPhase('idle');
      await refresh(true);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
      // Stay in the paste step: the usual cause is a partially copied value,
      // and the operator's authorize tab is still open.
      setPhase('awaiting-code');
    }
  };

  return (
    <div className="mt-2 border-t border-line pt-2" data-connect={account}>
      {phase === 'idle' || phase === 'starting' ? (
        <div className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            onClick={() => void begin()}
            disabled={busy}
            className={btn}
          >
            {phase === 'starting' ? 'starting…' : 'Connect account'}
          </button>
          <span className="font-mono text-[9.5px] leading-relaxed text-ink-faint">
            Authorize swarmery to read this account&apos;s quota — no CLI login needed.
          </span>
        </div>
      ) : (
        <div className="flex flex-col gap-1.5">
          <p className="font-mono text-[10px] leading-relaxed text-ink-dim">
            <span className="text-ink-faint">1 — </span>
            Sign in <span className="text-ink-2">as {account}</span>. The browser tab that just
            opened must be logged into THAT account&apos;s claude.ai subscription — if it is signed
            in as someone else, sign out there first or use a private window.
          </p>
          {authorizeUrl !== null && (
            <a
              href={authorizeUrl}
              target="_blank"
              rel="noopener noreferrer"
              className="font-mono text-[9.5px] break-all text-brand underline-offset-2 hover:underline"
            >
              tab didn&apos;t open? authorize here
            </a>
          )}
          <p className="font-mono text-[10px] leading-relaxed text-ink-dim">
            <span className="text-ink-faint">2 — </span>
            Paste the whole code the page shows, including the part after the <code>#</code>.
          </p>
          <div className="flex items-center gap-1.5">
            <input
              type="text"
              value={code}
              autoComplete="off"
              spellCheck={false}
              aria-label={`authorization code for ${account}`}
              placeholder="code#state"
              onChange={(e) => setCode(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && code.trim() !== '' && !busy) void submit();
              }}
              className="min-w-0 flex-1 rounded-lg border border-line bg-bg/40 px-2 py-1 font-mono text-[10.5px] text-ink-2 outline-none focus-visible:border-brand"
            />
            <button
              type="button"
              onClick={() => void submit()}
              disabled={busy || code.trim() === ''}
              className={btn}
            >
              {phase === 'submitting' ? '…' : 'connect'}
            </button>
            <button
              type="button"
              onClick={() => {
                setCode('');
                setError(null);
                setAuthorizeUrl(null);
                setPhase('idle');
              }}
              disabled={busy}
              className={btn}
            >
              cancel
            </button>
          </div>
        </div>
      )}

      {error !== null && (
        <p
          role="alert"
          className="mt-1.5 font-mono text-[10px] leading-snug break-words text-red"
        >
          {error}
        </p>
      )}
    </div>
  );
}
