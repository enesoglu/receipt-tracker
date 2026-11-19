import { useEffect, useRef, useState } from 'react';
import type { ReceiptChain } from '../../api/receipts';
import { Button } from '../../components/ui/Button';
import { Card } from '../../components/ui/Card';

interface EArchivePanelProps {
  chain: ReceiptChain | null;
}

const captchaNotes: Record<ReceiptChain['captcha'], string | null> = {
  none: null,
  image: 'The portal shows an image captcha; type it yourself.',
  turnstile: 'The portal shows a Cloudflare Turnstile challenge.',
};

function portalHost(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

/** Portal link and paste-ready values for a known chain; nothing otherwise. */
export function EArchivePanel({ chain }: EArchivePanelProps) {
  const [copied, setCopied] = useState<string | null>(null);
  const [failed, setFailed] = useState<string | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(
    () => () => {
      if (timer.current !== null) clearTimeout(timer.current);
    },
    [],
  );

  if (chain === null) return null;

  const missing = chain.fields.filter((field) => field.value === null);
  if (missing.length > 0) {
    return (
      <p className="text-small text-muted">
        {chain.name} e-archive — this receipt is missing:{' '}
        {missing.map((field) => field.description).join(', ')}.
      </p>
    );
  }

  async function copyValue(name: string, value: string) {
    if (timer.current !== null) {
      clearTimeout(timer.current);
      timer.current = null;
    }
    setCopied(null);
    setFailed(null);
    if (typeof navigator === 'undefined' || !navigator.clipboard) {
      setFailed(name);
      return;
    }
    try {
      await navigator.clipboard.writeText(value);
      setCopied(name);
      timer.current = setTimeout(() => {
        setCopied(null);
        timer.current = null;
      }, 2000);
    } catch {
      setFailed(name);
    }
  }

  const captchaNote = captchaNotes[chain.captcha];

  return (
    <Card
      title={`${chain.name} e-archive`}
      description="Open the portal, paste each value and complete the challenge yourself."
    >
      <div className="flex flex-col gap-4">
        <a
          href={chain.portalUrl}
          target="_blank"
          rel="noopener noreferrer"
          className="font-medium text-accent underline underline-offset-2"
        >
          Open {portalHost(chain.portalUrl)}
        </a>
        {chain.fields.map((field) => {
          const label =
            failed === field.name
              ? 'Copy failed'
              : copied === field.name
                ? 'Copied'
                : 'Copy';
          return (
            <div key={field.name} className="flex flex-col gap-1">
              <span className="text-caption font-medium text-muted">
                {field.description}
              </span>
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span className="text-body text-ink">{field.value ?? ''}</span>
                <Button
                  variant="secondary"
                  size="sm"
                  aria-label={`Copy ${field.description}`}
                  onClick={() => void copyValue(field.name, field.value ?? '')}
                >
                  {label}
                </Button>
              </div>
            </div>
          );
        })}
        {chain.fields.length === 0 ? (
          <p className="text-small text-muted">
            The fields this portal asks for are not recorded yet.
          </p>
        ) : null}
        {captchaNote ? (
          <p className="text-small text-muted">{captchaNote}</p>
        ) : null}
      </div>
    </Card>
  );
}
