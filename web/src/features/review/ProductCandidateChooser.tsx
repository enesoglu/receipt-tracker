import { useEffect, useState, type FormEvent } from 'react';
import { type ReviewFlagEntry, type ReviewResolution } from '../../api/review';
import type { components } from '../../api/schema.gen';
import { Button } from '../../components/ui/Button';
import { Input } from '../../components/ui/Input';

type MatchCandidate = components['schemas']['MatchCandidate'];

type ReviewFlag = components['schemas']['ReviewFlag'];

function formatCandidateSize(candidate: MatchCandidate): string | null {
  if (!candidate.sizeValue && !candidate.sizeUnit) return null;
  if (candidate.sizeValue && candidate.sizeUnit) {
    return `${candidate.sizeValue} ${candidate.sizeUnit}`;
  }
  return candidate.sizeValue ?? candidate.sizeUnit;
}

function buildProductChoicePreview(
  lineName: string,
  chosenCandidate: MatchCandidate | null,
  newProductName: string,
  flagKind: 'uncertain_match' | 'code_conflict',
): string {
  const itemLabel = lineName || 'this item';
  if (chosenCandidate) {
    return `Links "${itemLabel}" to existing product "${chosenCandidate.canonicalName}" and remembers this store item name${flagKind === 'code_conflict' ? ' and code' : ''} for future receipts.`;
  }
  const targetName = newProductName.trim() || itemLabel;
  return `Creates a new product named "${targetName}" for "${itemLabel}" and excludes the suggested matches for this line name.`;
}

interface ProductCandidateChooserProps {
  flag: Extract<ReviewFlag, { kind: 'uncertain_match' | 'code_conflict' }>;
  entry: ReviewFlagEntry;
  isSubmitting: boolean;
  onSubmit: (resolution: ReviewResolution) => Promise<void>;
  onRegisterShortcutAction: (fn: (() => void) | null) => void;
}

export function ProductCandidateChooser({
  flag,
  entry,
  isSubmitting,
  onSubmit,
  onRegisterShortcutAction,
}: ProductCandidateChooserProps) {
  const rawLineName = entry.lineItem?.name ?? 'this line item';
  const candidates: Array<{
    key: string;
    roleLabel?: string;
    candidate: MatchCandidate;
  }> =
    flag.kind === 'uncertain_match'
      ? flag.detail.candidates.slice(0, 3).map((candidate) => ({
          key: candidate.productId,
          candidate,
        }))
      : [
          {
            key: `code:${flag.detail.codeProduct.productId}`,
            roleLabel: `Owns code ${flag.detail.code}`,
            candidate: flag.detail.codeProduct,
          },
          {
            key: `name:${flag.detail.nameProduct.productId}`,
            roleLabel: 'Best name match',
            candidate: flag.detail.nameProduct,
          },
        ];

  const [selectedKey, setSelectedKey] = useState<string>(
    candidates[0]?.key ?? '__new__',
  );
  const [canonicalName, setCanonicalName] = useState<string>(
    entry.lineItem?.name ?? '',
  );

  const selectedCandidate =
    candidates.find((option) => option.key === selectedKey)?.candidate ?? null;

  const previewText = buildProductChoicePreview(
    rawLineName,
    selectedCandidate,
    canonicalName,
    flag.kind,
  );

  function submitCurrentChoice() {
    if (selectedCandidate) {
      void onSubmit({
        action: 'same_product',
        productId: selectedCandidate.productId,
      });
      return;
    }
    const trimmed = canonicalName.trim();
    void onSubmit(
      trimmed.length > 0
        ? { action: 'different_product', canonicalName: trimmed }
        : { action: 'different_product' },
    );
  }

  useEffect(() => {
    onRegisterShortcutAction(submitCurrentChoice);
    return () => onRegisterShortcutAction(null);
  });

  function handleFormSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    submitCurrentChoice();
  }

  return (
    <form onSubmit={handleFormSubmit} className="flex flex-col gap-4">
      {flag.kind === 'code_conflict' ? (
        <p className="text-small text-muted">
          Code <strong>{flag.detail.code}</strong> belongs to one product while
          the printed item name matches another. Choose which product this line
          represents.
        </p>
      ) : null}

      <fieldset className="flex flex-col gap-2">
        <legend className="mb-1 text-small font-medium text-ink">
          Product options
        </legend>

        {candidates.map(({ key, roleLabel, candidate }) => {
          const checked = selectedKey === key;
          const sizeLabel = formatCandidateSize(candidate);
          const scorePercent = Math.round(candidate.score * 100);
          return (
            <label
              key={key}
              className={
                checked
                  ? 'flex cursor-pointer items-start gap-3 rounded-control border border-accent bg-accent-soft p-3'
                  : 'flex cursor-pointer items-start gap-3 rounded-control border border-border bg-surface p-3 hover:bg-surface-subtle'
              }
            >
              <input
                type="radio"
                name={`candidate-${flag.id}`}
                value={key}
                checked={checked}
                onChange={() => setSelectedKey(key)}
                className="mt-1 h-4 w-4 accent-accent"
              />
              <div className="flex min-w-0 flex-1 flex-wrap items-center justify-between gap-2">
                <div className="flex flex-col gap-0.5">
                  <span className="text-body font-medium text-ink">
                    {candidate.canonicalName}
                  </span>
                  <span className="text-small text-muted">
                    {roleLabel ? `${roleLabel} · ` : ''}
                    {sizeLabel ? `Size ${sizeLabel} · ` : ''}
                    {scorePercent}% name similarity
                  </span>
                </div>
              </div>
            </label>
          );
        })}

        <label
          className={
            selectedKey === '__new__'
              ? 'flex cursor-pointer items-start gap-3 rounded-control border border-accent bg-accent-soft p-3'
              : 'flex cursor-pointer items-start gap-3 rounded-control border border-border bg-surface p-3 hover:bg-surface-subtle'
          }
        >
          <input
            type="radio"
            name={`candidate-${flag.id}`}
            value="__new__"
            checked={selectedKey === '__new__'}
            onChange={() => setSelectedKey('__new__')}
            className="mt-1 h-4 w-4 accent-accent"
          />
          <div className="flex min-w-0 flex-1 flex-col gap-1">
            <span className="text-body font-medium text-ink">
              None of these — create a new product
            </span>
            <span className="text-small text-muted">
              Create a separate product record for this item.
            </span>
          </div>
        </label>
      </fieldset>

      {selectedKey === '__new__' ? (
        <Input
          label="New product name"
          placeholder={rawLineName}
          value={canonicalName}
          onChange={(event) => setCanonicalName(event.target.value)}
        />
      ) : null}

      <div
        role="status"
        aria-live="polite"
        className="rounded-control border border-border bg-surface-subtle px-3 py-2 text-small text-ink"
      >
        <strong>Effect:</strong> {previewText}
      </div>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <span className="text-caption text-muted">
          Shortcut: press <kbd className="font-mono font-semibold">C</kbd> to
          apply
        </span>
        <Button type="submit" loading={isSubmitting}>
          Apply choice
        </Button>
      </div>
    </form>
  );
}
