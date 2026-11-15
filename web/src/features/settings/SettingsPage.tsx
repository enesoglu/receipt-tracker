import { useState, type FormEvent } from 'react';
import { useLogout, useMe } from '../../api/auth';
import { formatApiError, getErrorMessage } from '../../api/errors';
import {
  useSettings,
  useUpdateSettings,
  useUsage,
  type Settings,
  type Usage,
} from '../../api/settings';
import {
  useDisconnectStorage,
  useStorageStatus,
  type StorageStatus,
} from '../../api/storage';
import { Badge, type BadgeVariant } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Card } from '../../components/ui/Card';
import { DateText } from '../../components/ui/DateText';
import { Dialog } from '../../components/ui/Dialog';
import { ErrorState } from '../../components/ui/ErrorState';
import { Input } from '../../components/ui/Input';
import { PageHeader } from '../../components/ui/PageHeader';
import { Spinner } from '../../components/ui/Spinner';

const MAX_DEFAULT_TITLE_LENGTH = 200;

function formatReasonCode(code: string): string {
  return code.replaceAll('_', ' ');
}

function getDriveBadge(drive: StorageStatus['drive']): {
  label: string;
  variant: BadgeVariant;
} {
  switch (drive) {
    case 'ok':
      return { label: 'Connected', variant: 'success' };
    case 'not_connected':
      return { label: 'Not connected', variant: 'warning' };
    case 'reconnect_needed':
      return { label: 'Reconnect needed', variant: 'danger' };
    case 'not_required':
      return { label: 'Not required', variant: 'neutral' };
  }
}

function getBackupDestinationLabel(
  destination: StorageStatus['backup']['destination'],
): string {
  switch (destination) {
    case 'drive':
      return 'Google Drive';
    case 'local':
      return 'Local disk';
    case 'none':
      return 'Disabled';
  }
}

function getProviderLabel(provider: Usage['provider']): string {
  return provider === 'azure'
    ? 'Azure Document Intelligence'
    : 'Fake extractor';
}

interface SettingsDefaultsFormProps {
  settings: Settings;
  onSaved: (message: string) => void;
}

function SettingsDefaultsForm({
  settings,
  onSaved,
}: SettingsDefaultsFormProps) {
  const updateSettings = useUpdateSettings();
  const [defaultCurrency, setDefaultCurrency] = useState(
    settings.defaultCurrency,
  );
  const [defaultReceiptTitle, setDefaultReceiptTitle] = useState(
    settings.defaultReceiptTitle,
  );
  const [currencyError, setCurrencyError] = useState<string | null>(null);
  const [titleError, setTitleError] = useState<string | null>(null);
  const [submitError, setSubmitError] = useState<string | null>(null);

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSubmitError(null);

    const normalizedCurrency = defaultCurrency.trim().toUpperCase();
    const trimmedTitle = defaultReceiptTitle.trim();

    let hasClientError = false;
    if (!/^[A-Z]{3}$/.test(normalizedCurrency)) {
      setCurrencyError('Enter a 3-letter ISO currency code (e.g. TRY, USD).');
      hasClientError = true;
    } else {
      setCurrencyError(null);
    }

    if (
      trimmedTitle.length === 0 ||
      trimmedTitle.length > MAX_DEFAULT_TITLE_LENGTH
    ) {
      setTitleError('Default receipt title must be 1 to 200 characters.');
      hasClientError = true;
    } else {
      setTitleError(null);
    }

    if (hasClientError) {
      return;
    }

    try {
      await updateSettings.mutateAsync({
        defaultCurrency: normalizedCurrency,
        defaultReceiptTitle: trimmedTitle,
      });
      setDefaultCurrency(normalizedCurrency);
      setDefaultReceiptTitle(trimmedTitle);
      onSaved('Receipt defaults saved.');
    } catch (error) {
      const formatted = formatApiError(error);
      if (formatted.fieldErrors.defaultCurrency) {
        setCurrencyError(formatted.fieldErrors.defaultCurrency);
      }
      if (formatted.fieldErrors.defaultReceiptTitle) {
        setTitleError(formatted.fieldErrors.defaultReceiptTitle);
      }
      setSubmitError(formatted.message);
    }
  }

  return (
    <form
      onSubmit={(event) => {
        void handleSubmit(event);
      }}
      noValidate
      className="flex flex-col gap-4"
    >
      {submitError ? (
        <ErrorState title="Could not save settings" message={submitError} />
      ) : null}

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <Input
          label="Default currency"
          hint="3-letter ISO 4217 code (for example TRY, USD, EUR)."
          maxLength={3}
          value={defaultCurrency}
          error={currencyError ?? undefined}
          onChange={(event) => {
            setDefaultCurrency(event.target.value);
            setCurrencyError(null);
            setSubmitError(null);
          }}
        />
        <Input
          label="Default receipt title"
          hint="Applied to newly uploaded receipts when no title is given."
          maxLength={MAX_DEFAULT_TITLE_LENGTH}
          value={defaultReceiptTitle}
          error={titleError ?? undefined}
          onChange={(event) => {
            setDefaultReceiptTitle(event.target.value);
            setTitleError(null);
            setSubmitError(null);
          }}
        />
      </div>

      <div className="flex justify-end">
        <Button type="submit" loading={updateSettings.isPending}>
          Save defaults
        </Button>
      </div>
    </form>
  );
}

/** Account preferences, OCR extraction quota, Google Drive storage status, and sign out. */
export function SettingsPage() {
  const meQuery = useMe();
  const settingsQuery = useSettings();
  const usageQuery = useUsage();
  const storageQuery = useStorageStatus();
  const disconnectStorage = useDisconnectStorage();
  const logout = useLogout();

  const [statusMessage, setStatusMessage] = useState<string | null>(null);
  const [disconnectDialogOpen, setDisconnectDialogOpen] = useState(false);
  const [disconnectError, setDisconnectError] = useState<string | null>(null);

  async function handleConfirmDisconnect() {
    setDisconnectError(null);
    try {
      await disconnectStorage.mutateAsync();
      setDisconnectDialogOpen(false);
      setStatusMessage('Google Drive disconnected.');
    } catch (error) {
      setDisconnectError(getErrorMessage(error));
    }
  }

  const usagePercent = usageQuery.data
    ? Math.min(
        100,
        Math.round(
          (usageQuery.data.pagesUsed /
            Math.max(1, usageQuery.data.pagesLimit)) *
            100,
        ),
      )
    : 0;
  const usageBarClass =
    usagePercent >= 100
      ? 'bg-danger'
      : usagePercent >= 80
        ? 'bg-warning'
        : 'bg-accent';

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow="Preferences"
        title="Settings"
        description="Configure receipt defaults, check monthly OCR quota, and manage Google Drive storage."
      />

      {statusMessage ? (
        <div
          role="status"
          className="rounded-card border border-success/30 bg-success-soft px-4 py-3 text-small font-medium text-success"
        >
          {statusMessage}
        </div>
      ) : null}

      <Card
        title="Receipt defaults"
        description="Defaults apply to receipts uploaded after saving."
      >
        {settingsQuery.isPending ? (
          <div className="flex items-center justify-center py-6">
            <Spinner showLabel label="Loading settings…" />
          </div>
        ) : settingsQuery.isError ? (
          <ErrorState
            title="Could not load settings"
            message={getErrorMessage(settingsQuery.error)}
            onRetry={() => {
              void settingsQuery.refetch();
            }}
          />
        ) : (
          <SettingsDefaultsForm
            key={`${settingsQuery.data.defaultCurrency}-${settingsQuery.data.defaultReceiptTitle}`}
            settings={settingsQuery.data}
            onSaved={(message) => {
              setStatusMessage(message);
            }}
          />
        )}
      </Card>

      <Card
        title="Monthly extraction usage"
        description="Instance-wide OCR page allowance resets at the start of each UTC calendar month."
      >
        {usageQuery.isPending ? (
          <div className="flex items-center justify-center py-6">
            <Spinner showLabel label="Loading usage…" />
          </div>
        ) : usageQuery.isError ? (
          <ErrorState
            title="Could not load usage"
            message={getErrorMessage(usageQuery.error)}
            onRetry={() => {
              void usageQuery.refetch();
            }}
          />
        ) : (
          <div className="flex flex-col gap-4">
            {usageQuery.data.pagesUsed >= usageQuery.data.pagesLimit ? (
              <div
                role="alert"
                className="rounded-card border border-warning/40 bg-warning-soft px-4 py-3 text-small text-ink"
              >
                Monthly page quota is exhausted. Newly uploaded receipts stay
                queued until next month.
              </div>
            ) : null}

            <div className="flex flex-col gap-2">
              <div className="flex flex-wrap items-baseline justify-between gap-2 text-small">
                <span className="font-medium text-ink">
                  {usageQuery.data.pagesUsed} / {usageQuery.data.pagesLimit}{' '}
                  pages used
                </span>
                <span className="text-muted">
                  {Math.max(
                    0,
                    usageQuery.data.pagesLimit - usageQuery.data.pagesUsed,
                  )}{' '}
                  pages remaining
                </span>
              </div>
              <div
                role="progressbar"
                aria-label="Monthly extraction usage"
                aria-valuenow={usageQuery.data.pagesUsed}
                aria-valuemin={0}
                aria-valuemax={usageQuery.data.pagesLimit}
                className="h-3 w-full overflow-hidden rounded-pill bg-surface-subtle"
              >
                <div
                  className={`h-full rounded-pill ${usageBarClass}`}
                  style={{ width: `${usagePercent}%` }}
                />
              </div>
            </div>

            <dl className="grid grid-cols-1 gap-4 text-small sm:grid-cols-2">
              <div>
                <dt className="text-caption text-muted">Current period</dt>
                <dd className="mt-0.5 font-medium text-ink">
                  <DateText value={usageQuery.data.period} format="month" />
                </dd>
              </div>
              <div>
                <dt className="text-caption text-muted">Extraction provider</dt>
                <dd className="mt-0.5 font-medium text-ink">
                  {getProviderLabel(usageQuery.data.provider)}
                </dd>
              </div>
            </dl>
          </div>
        )}
      </Card>

      <Card
        title="Storage & backups"
        description="Receipt image storage backend and scheduled database backups."
        actions={
          storageQuery.data ? (
            <Badge variant={getDriveBadge(storageQuery.data.drive).variant}>
              {getDriveBadge(storageQuery.data.drive).label}
            </Badge>
          ) : undefined
        }
      >
        {storageQuery.isPending ? (
          <div className="flex items-center justify-center py-6">
            <Spinner showLabel label="Loading storage status…" />
          </div>
        ) : storageQuery.isError ? (
          <ErrorState
            title="Could not load storage status"
            message={getErrorMessage(storageQuery.error)}
            onRetry={() => {
              void storageQuery.refetch();
            }}
          />
        ) : (
          <div className="flex flex-col gap-5">
            {storageQuery.data.drive === 'reconnect_needed' ? (
              <div
                role="alert"
                className="flex flex-col gap-1.5 rounded-card border border-danger/30 bg-danger-soft p-4 text-small"
              >
                <p className="font-semibold text-danger">
                  Google Drive reconnect needed
                </p>
                <p className="text-ink">
                  The stored Google Drive authorization no longer works. Images
                  and backups cannot sync until an administrator connects Google
                  Drive again.
                </p>
                {storageQuery.data.lastError ? (
                  <p className="text-caption text-danger">
                    Reason: {formatReasonCode(storageQuery.data.lastError)}
                  </p>
                ) : null}
              </div>
            ) : null}

            {storageQuery.data.drive === 'not_connected' ? (
              <div className="flex flex-col gap-1 rounded-card border border-warning/40 bg-warning-soft p-4 text-small">
                <p className="font-semibold text-warning">
                  Google Drive is not connected
                </p>
                <p className="text-ink">
                  Connect Google Drive to enable cloud image storage and remote
                  backups.
                </p>
              </div>
            ) : null}

            {storageQuery.data.drive === 'not_required' ? (
              <p className="text-small text-muted">
                Neither receipt images nor database backups currently require
                Google Drive authorization.
              </p>
            ) : null}

            <dl className="grid grid-cols-1 gap-4 text-small sm:grid-cols-2 lg:grid-cols-4">
              <div>
                <dt className="text-caption text-muted">Image storage</dt>
                <dd className="mt-0.5 font-medium text-ink">
                  {storageQuery.data.backend === 'drive'
                    ? 'Google Drive'
                    : 'Local storage'}
                </dd>
              </div>
              <div>
                <dt className="text-caption text-muted">Google Drive status</dt>
                <dd className="mt-0.5 font-medium text-ink">
                  {getDriveBadge(storageQuery.data.drive).label}
                </dd>
              </div>
              <div>
                <dt className="text-caption text-muted">Backup destination</dt>
                <dd className="mt-0.5 font-medium text-ink">
                  {getBackupDestinationLabel(
                    storageQuery.data.backup.destination,
                  )}
                </dd>
              </div>
              <div>
                <dt className="text-caption text-muted">Last backup</dt>
                <dd className="mt-0.5 font-medium text-ink">
                  <DateText
                    value={storageQuery.data.backup.lastSuccessAt}
                    format="datetime"
                    fallback="No backup yet"
                  />
                </dd>
              </div>
            </dl>

            {storageQuery.data.backup.lastError ? (
              <p className="text-small text-danger">
                Last backup error:{' '}
                {formatReasonCode(storageQuery.data.backup.lastError)}
              </p>
            ) : null}

            {storageQuery.data.canManage ? (
              storageQuery.data.drive !== 'not_required' ? (
                <div className="flex flex-wrap items-center gap-3 border-t border-border pt-4">
                  <a
                    href="/api/v1/storage/drive/connect"
                    className={`inline-flex items-center justify-center rounded-control border px-4 py-2 text-small font-medium focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent ${
                      storageQuery.data.drive === 'ok'
                        ? 'border-border bg-surface text-ink hover:border-border-strong hover:bg-surface-subtle'
                        : 'border-transparent bg-accent text-on-accent hover:bg-accent-hover'
                    }`}
                  >
                    {storageQuery.data.drive === 'reconnect_needed'
                      ? 'Reconnect Google Drive'
                      : 'Connect Google Drive'}
                  </a>

                  {storageQuery.data.drive === 'ok' ||
                  storageQuery.data.drive === 'reconnect_needed' ? (
                    <Button
                      variant="danger"
                      onClick={() => {
                        setDisconnectError(null);
                        setDisconnectDialogOpen(true);
                      }}
                    >
                      Disconnect
                    </Button>
                  ) : null}
                </div>
              ) : null
            ) : (
              <p className="border-t border-border pt-4 text-small text-muted">
                Managed by an administrator. Only an administrator can connect
                or disconnect Google Drive.
              </p>
            )}
          </div>
        )}
      </Card>

      <Card title="Account">
        {meQuery.isPending ? (
          <div className="flex items-center justify-center py-4">
            <Spinner showLabel label="Loading account…" />
          </div>
        ) : meQuery.isError ? (
          <ErrorState
            title="Could not load account"
            message={getErrorMessage(meQuery.error)}
          />
        ) : meQuery.data ? (
          <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex flex-col gap-1">
              <div className="flex flex-wrap items-center gap-2">
                <p className="text-body font-semibold text-ink">
                  {meQuery.data.name}
                </p>
                <Badge variant={meQuery.data.isAdmin ? 'accent' : 'neutral'}>
                  {meQuery.data.isAdmin ? 'Administrator' : 'Member'}
                </Badge>
              </div>
              <p className="text-small text-muted">{meQuery.data.email}</p>
            </div>
            <Button
              variant="secondary"
              loading={logout.isPending}
              onClick={() => {
                logout.mutate();
              }}
            >
              Sign out
            </Button>
          </div>
        ) : null}

        {logout.isError ? (
          <div className="mt-4">
            <ErrorState
              title="Could not sign out"
              message={getErrorMessage(logout.error)}
            />
          </div>
        ) : null}
      </Card>

      <Dialog
        open={disconnectDialogOpen}
        onClose={() => {
          if (!disconnectStorage.isPending) {
            setDisconnectDialogOpen(false);
          }
        }}
        title="Disconnect Google Drive?"
        footer={
          <>
            <Button
              variant="secondary"
              disabled={disconnectStorage.isPending}
              onClick={() => {
                setDisconnectDialogOpen(false);
              }}
            >
              Cancel
            </Button>
            <Button
              variant="danger"
              loading={disconnectStorage.isPending}
              onClick={() => {
                void handleConfirmDisconnect();
              }}
            >
              Confirm disconnect
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-4 text-small">
          {disconnectError ? (
            <ErrorState
              title="Could not disconnect Google Drive"
              message={disconnectError}
            />
          ) : null}

          <p className="text-ink">
            Disconnecting removes the stored Google Drive token from this
            application. Files and backups already stored in Google Drive remain
            in place.
          </p>
        </div>
      </Dialog>
    </div>
  );
}
