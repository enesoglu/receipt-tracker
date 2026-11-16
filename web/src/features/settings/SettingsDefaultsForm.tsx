import { useState, type FormEvent } from 'react';
import { formatApiError } from '../../api/errors';
import { useUpdateSettings, type Settings } from '../../api/settings';
import { Button } from '../../components/ui/Button';
import { ErrorState } from '../../components/ui/ErrorState';
import { Input } from '../../components/ui/Input';

const MAX_DEFAULT_TITLE_LENGTH = 200;

interface SettingsDefaultsFormProps {
  settings: Settings;
  onSaved: (message: string) => void;
}

export function SettingsDefaultsForm({
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
