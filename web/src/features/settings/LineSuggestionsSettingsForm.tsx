import { useState } from 'react';
import { getErrorMessage } from '../../api/errors';
import { useSuggestionsStatus } from '../../api/suggestions';
import { useUpdateSettings, type Settings } from '../../api/settings';
import { Checkbox } from '../../components/ui/Checkbox';
import { ErrorState } from '../../components/ui/ErrorState';

interface LineSuggestionsSettingsFormProps {
  settings: Settings;
  statusQuery: ReturnType<typeof useSuggestionsStatus>;
  onSaved: (message: string) => void;
}

/** The per-user switch for model suggestions, with the endpoint's state. */
export function LineSuggestionsSettingsForm({
  settings,
  statusQuery,
  onSaved,
}: LineSuggestionsSettingsFormProps) {
  const updateSettings = useUpdateSettings();
  const [submitError, setSubmitError] = useState<string | null>(null);

  async function handleToggle() {
    setSubmitError(null);
    try {
      const updated = await updateSettings.mutateAsync({
        ...settings,
        llmSuggestionsEnabled: !settings.llmSuggestionsEnabled,
      });
      onSaved(
        updated.llmSuggestionsEnabled
          ? 'Line suggestions turned on.'
          : 'Line suggestions turned off.',
      );
    } catch (error) {
      setSubmitError(getErrorMessage(error));
    }
  }

  const status = statusQuery.data;
  const statusNote = !settings.llmSuggestionsEnabled ? (
    <p className="text-small text-muted">
      The model is not asked about your receipts.
    </p>
  ) : statusQuery.isError ? (
    <p className="text-small text-danger">
      Could not load the suggestion model status.
    </p>
  ) : !status ? null : !status.available ? (
    <p className="text-small text-muted">
      Your switch is on, but this instance has no suggestion endpoint
      configured. Nothing is sent to any model until an administrator configures
      one.
    </p>
  ) : status.offline ? (
    <p className="text-small text-warning">
      The most recent suggestion job could not reach the model. Suggestions
      resume automatically; no action is needed.
    </p>
  ) : null;

  return (
    <div className="flex flex-col gap-4">
      {submitError ? (
        <ErrorState title="Could not save settings" message={submitError} />
      ) : null}

      <Checkbox
        label="Suggest line names and categories"
        description="A configured language model proposes clean names and categories for lines the matcher could not resolve. Nothing is applied until you accept a suggestion."
        checked={settings.llmSuggestionsEnabled}
        disabled={updateSettings.isPending}
        onChange={() => {
          void handleToggle();
        }}
      />

      {statusNote}
    </div>
  );
}
