import { Input } from '../../components/ui/Input';
import { type LineItemFormValues } from './line-item-form-values';

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

interface LineItemEditorFieldsProps {
  values: LineItemFormValues;
  fieldErrors: Record<string, string>;
  onChange: (next: LineItemFormValues) => void;
}

export function LineItemEditorFields({
  values,
  fieldErrors,
  onChange,
}: LineItemEditorFieldsProps) {
  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-6">
      <div className="sm:col-span-2 lg:col-span-2">
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
        placeholder="e.g. 2"
        value={values.quantity}
        error={pickFieldError(fieldErrors, 'quantity')}
        onChange={(event) =>
          onChange({ ...values, quantity: event.target.value })
        }
      />
      <Input
        label="Unit"
        placeholder="e.g. kg"
        value={values.quantityUnit}
        error={pickFieldError(fieldErrors, 'quantityUnit', 'quantity_unit')}
        onChange={(event) =>
          onChange({ ...values, quantityUnit: event.target.value })
        }
      />
      <Input
        label="Unit price"
        placeholder="e.g. 12.50"
        value={values.unitPrice}
        error={pickFieldError(fieldErrors, 'unitPrice', 'unit_price')}
        onChange={(event) =>
          onChange({ ...values, unitPrice: event.target.value })
        }
      />
      <Input
        label="Line total"
        required
        placeholder="e.g. 25.00"
        value={values.lineTotal}
        error={pickFieldError(fieldErrors, 'lineTotal', 'line_total')}
        onChange={(event) =>
          onChange({ ...values, lineTotal: event.target.value })
        }
      />
      <div className="sm:col-span-2 lg:col-span-2">
        <Input
          label="Product code"
          placeholder="Optional barcode or PLU"
          value={values.productCode}
          error={pickFieldError(fieldErrors, 'productCode', 'product_code')}
          onChange={(event) =>
            onChange({ ...values, productCode: event.target.value })
          }
        />
      </div>
    </div>
  );
}
