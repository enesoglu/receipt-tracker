import {
  useEffect,
  useId,
  useRef,
  type KeyboardEvent,
  type MouseEvent,
  type ReactNode,
} from 'react';
import { Button } from './Button';

export interface DialogProps {
  open: boolean;
  onClose: () => void;
  title: string;
  description?: string;
  children: ReactNode;
  footer?: ReactNode;
  className?: string;
}

const focusableSelector =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/** Accessible modal dialog with focus restoration, Tab trapping, and Escape dismissal. */
export function Dialog({
  open,
  onClose,
  title,
  description,
  children,
  footer,
  className = '',
}: DialogProps) {
  const titleId = useId();
  const descriptionId = useId();
  const panelRef = useRef<HTMLDivElement>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!open) return;
    previousFocusRef.current =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;

    const panel = panelRef.current;
    if (panel) {
      const first = panel.querySelector<HTMLElement>(focusableSelector);
      (first ?? panel).focus();
    }

    return () => {
      previousFocusRef.current?.focus();
    };
  }, [open]);

  if (!open) return null;

  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      onClose();
      return;
    }

    if (event.key !== 'Tab' || !panelRef.current) return;
    const focusables = Array.from(
      panelRef.current.querySelectorAll<HTMLElement>(focusableSelector),
    );
    if (focusables.length === 0) {
      event.preventDefault();
      return;
    }
    const first = focusables[0];
    const last = focusables[focusables.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  function handleBackdropClick(event: MouseEvent<HTMLDivElement>) {
    if (event.target === event.currentTarget) onClose();
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-ink/45 p-4"
      onClick={handleBackdropClick}
      onKeyDown={handleKeyDown}
    >
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={description ? descriptionId : undefined}
        tabIndex={-1}
        className={`w-full max-w-dialog rounded-card border border-border bg-surface ${className}`.trim()}
      >
        <div className="flex items-start justify-between gap-4 border-b border-border px-6 py-4">
          <div className="flex flex-col gap-1">
            <h2 id={titleId} className="text-heading font-semibold text-ink">
              {title}
            </h2>
            {description ? (
              <p id={descriptionId} className="text-small text-muted">
                {description}
              </p>
            ) : null}
          </div>
          <Button
            variant="ghost"
            size="sm"
            aria-label="Close dialog"
            onClick={onClose}
          >
            <svg
              viewBox="0 0 20 20"
              fill="none"
              aria-hidden="true"
              className="h-4 w-4"
            >
              <path
                d="M5 5l10 10M15 5L5 15"
                stroke="currentColor"
                strokeWidth="1.75"
                strokeLinecap="round"
              />
            </svg>
          </Button>
        </div>
        <div className="px-6 py-4">{children}</div>
        {footer ? (
          <div className="flex flex-wrap items-center justify-end gap-2 border-t border-border px-6 py-4">
            {footer}
          </div>
        ) : null}
      </div>
    </div>
  );
}
