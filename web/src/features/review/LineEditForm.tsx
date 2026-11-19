import { Button } from '../../components/ui/Button';
import { Input } from '../../components/ui/Input';
import { type LineEditValues } from './review-form-values';

interface LineEditFormProps {
  values: LineEditValues;
  fieldErrors: Record<string, string>;
  isSubmitting: boolean;
  submitLabel: string;
  onChange: (next: LineEditValues) => void;
  onSubmit: () => void;
}

function pickFieldError(
  fieldErrors: Record<string, string>,
  ...keys: string[]
): string | undefined {
  for (const key of keys) {
    if (fieldErrors[key]) {
      return fieldErrors[key];
    }
  }
  return undefined;
}

export function LineEditForm({
  values,
  fieldErrors,
  isSubmitting,
  submitLabel,
  onChange,
  onSubmit,
}: LineEditFormProps) {
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        onSubmit();
      }}
      className="flex flex-col gap-3 rounded-control border border-border bg-surface-subtle p-4"
    >
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div className="sm:col-span-2">
          <Input
            label="Item name"
            required
            value={values.name}
            error={pickFieldError(fieldErrors, 'name')}
            onChange={(event) =>
              onChange({ ...values, name: event.target.value })
            }
          />
        </div>
        <Input
          label="Quantity"
          value={values.quantity}
          error={pickFieldError(fieldErrors, 'quantity')}
          onChange={(event) =>
            onChange({ ...values, quantity: event.target.value })
          }
        />
        <Input
          label="Unit"
          value={values.quantityUnit}
          error={pickFieldError(fieldErrors, 'quantityUnit')}
          onChange={(event) =>
            onChange({ ...values, quantityUnit: event.target.value })
          }
        />
        <Input
          label="Unit price"
          value={values.unitPrice}
          error={pickFieldError(fieldErrors, 'unitPrice')}
          onChange={(event) =>
            onChange({ ...values, unitPrice: event.target.value })
          }
        />
        <Input
          label="Line total"
          required
          value={values.lineTotal}
          error={pickFieldError(fieldErrors, 'lineTotal')}
          onChange={(event) =>
            onChange({ ...values, lineTotal: event.target.value })
          }
        />
        <div className="sm:col-span-2">
          <Input
            label="Product code"
            value={values.productCode}
            error={pickFieldError(fieldErrors, 'productCode')}
            onChange={(event) =>
              onChange({ ...values, productCode: event.target.value })
            }
          />
        </div>
      </div>
      <div className="flex justify-end">
        <Button type="submit" variant="secondary" loading={isSubmitting}>
          {submitLabel}
        </Button>
      </div>
    </form>
  );
}
