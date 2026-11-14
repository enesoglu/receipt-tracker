import {
  useId,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
} from 'react';

export interface TabItem {
  id: string;
  label: string;
  badge?: string | number;
  disabled?: boolean;
  content: ReactNode;
}

export interface TabsProps {
  items: readonly TabItem[];
  value?: string;
  defaultValue?: string;
  onChange?: (id: string) => void;
  label: string;
  className?: string;
}

/** WAI-ARIA tabs with keyboard arrow, Home, and End navigation. */
export function Tabs({
  items,
  value,
  defaultValue,
  onChange,
  label,
  className = '',
}: TabsProps) {
  const baseId = useId();
  const tabRefs = useRef<Array<HTMLButtonElement | null>>([]);
  const firstEnabled = items.find((item) => !item.disabled)?.id ?? items[0]?.id;
  const [internalValue, setInternalValue] = useState(
    defaultValue ?? firstEnabled ?? '',
  );
  const activeId = value ?? internalValue;

  function selectTab(id: string) {
    if (value === undefined) setInternalValue(id);
    onChange?.(id);
  }

  function handleKeyDown(
    event: KeyboardEvent<HTMLButtonElement>,
    currentIndex: number,
  ) {
    const enabledIndices = items
      .map((item, idx) => (item.disabled ? -1 : idx))
      .filter((idx) => idx !== -1);
    if (enabledIndices.length === 0) return;

    const currentPos = enabledIndices.indexOf(currentIndex);
    let nextIndex: number | undefined;

    if (event.key === 'ArrowRight') {
      nextIndex = enabledIndices[(currentPos + 1) % enabledIndices.length];
    } else if (event.key === 'ArrowLeft') {
      nextIndex =
        enabledIndices[
          (currentPos - 1 + enabledIndices.length) % enabledIndices.length
        ];
    } else if (event.key === 'Home') {
      nextIndex = enabledIndices[0];
    } else if (event.key === 'End') {
      nextIndex = enabledIndices[enabledIndices.length - 1];
    }

    if (nextIndex === undefined) return;
    event.preventDefault();
    const target = items[nextIndex];
    selectTab(target.id);
    tabRefs.current[nextIndex]?.focus();
  }

  return (
    <div className={`flex flex-col gap-4 ${className}`.trim()}>
      <div
        role="tablist"
        aria-label={label}
        className="flex flex-wrap gap-1 border-b border-border"
      >
        {items.map((item, index) => {
          const isActive = item.id === activeId;
          const tabId = `${baseId}-tab-${item.id}`;
          const panelId = `${baseId}-panel-${item.id}`;

          return (
            <button
              key={item.id}
              ref={(element) => {
                tabRefs.current[index] = element;
              }}
              type="button"
              role="tab"
              id={tabId}
              aria-selected={isActive}
              aria-controls={panelId}
              tabIndex={isActive ? 0 : -1}
              disabled={item.disabled}
              onClick={() => selectTab(item.id)}
              onKeyDown={(event) => handleKeyDown(event, index)}
              className={`inline-flex items-center gap-2 border-b-2 px-4 py-2.5 text-small cursor-pointer disabled:cursor-not-allowed disabled:opacity-50 ${
                isActive
                  ? 'border-accent font-semibold text-accent'
                  : 'border-transparent font-medium text-muted hover:text-ink'
              }`}
            >
              <span>{item.label}</span>
              {item.badge !== undefined ? (
                <span className="rounded-pill bg-surface-subtle px-2 py-0.5 text-caption text-muted">
                  {item.badge}
                </span>
              ) : null}
            </button>
          );
        })}
      </div>
      {items.map((item) => {
        const isActive = item.id === activeId;
        const tabId = `${baseId}-tab-${item.id}`;
        const panelId = `${baseId}-panel-${item.id}`;

        return (
          <div
            key={item.id}
            role="tabpanel"
            id={panelId}
            aria-labelledby={tabId}
            tabIndex={0}
            hidden={!isActive}
          >
            {isActive ? item.content : null}
          </div>
        );
      })}
    </div>
  );
}
