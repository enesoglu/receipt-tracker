import { useState, type FormEvent } from 'react';
import { formatApiError } from '../../api/errors';
import { useUpdateProduct, type Product } from '../../api/products';
import { Button } from '../../components/ui/Button';
import { ErrorState } from '../../components/ui/ErrorState';
import { Input } from '../../components/ui/Input';
import { Select } from '../../components/ui/Select';
import { normaliseDecimal, validateDecimal } from '../../components/ui/decimal';

interface ProductEditFormProps {
  product: Product;
  categoryOptions: { value: string; label: string }[];
  onSaveNotice: (message: string) => void;
}

export function ProductEditForm({
  product,
  categoryOptions,
  onSaveNotice,
}: ProductEditFormProps) {
  const [canonicalName, setCanonicalName] = useState(product.canonicalName);
  const [categoryId, setCategoryId] = useState(product.category?.id ?? '');
  const [sizeValue, setSizeValue] = useState(product.sizeValue ?? '');
  const [sizeUnit, setSizeUnit] = useState(product.sizeUnit ?? '');
  const [validationError, setValidationError] = useState<string | null>(null);
  const [serverError, setServerError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const updateMutation = useUpdateProduct(product.id);

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setValidationError(null);
    setServerError(null);
    setFieldErrors({});

    const errors: Record<string, string> = {};
    const trimmedName = canonicalName.trim();
    if (!trimmedName) {
      errors.canonicalName = 'Canonical name is required.';
    }

    const trimmedSizeValue = sizeValue.trim();
    const trimmedSizeUnit = sizeUnit.trim();
    if (
      (trimmedSizeValue !== '' && trimmedSizeUnit === '') ||
      (trimmedSizeValue === '' && trimmedSizeUnit !== '')
    ) {
      setValidationError(
        'Provide both a package size and unit, or leave both blank.',
      );
      return;
    }
    if (trimmedSizeValue !== '') {
      const problem = validateDecimal(trimmedSizeValue, 'quantity');
      if (problem) {
        errors.sizeValue = problem;
      }
    }

    if (Object.keys(errors).length > 0) {
      setFieldErrors(errors);
      setValidationError('Check the highlighted fields.');
      return;
    }

    try {
      await updateMutation.mutateAsync({
        canonicalName: trimmedName,
        categoryId: categoryId || null,
        sizeValue: trimmedSizeValue ? normaliseDecimal(trimmedSizeValue) : null,
        sizeUnit: trimmedSizeUnit || null,
        priceBasis: product.priceBasis,
      });
      onSaveNotice('Product details saved.');
    } catch (error) {
      const formatted = formatApiError(error);
      setServerError(formatted.message);
      setFieldErrors(formatted.fieldErrors);
    }
  }

  function clearErrors() {
    setValidationError(null);
    setServerError(null);
    setFieldErrors({});
  }

  return (
    <form
      onSubmit={(event) => {
        void handleSubmit(event);
      }}
      className="flex flex-col gap-4"
    >
      {validationError ? (
        <ErrorState title="Check product details" message={validationError} />
      ) : null}
      {serverError ? (
        <ErrorState title="Could not save product" message={serverError} />
      ) : null}

      <Input
        label="Canonical name"
        value={canonicalName}
        required
        maxLength={200}
        error={fieldErrors.canonicalName}
        onChange={(event) => {
          setCanonicalName(event.target.value);
          clearErrors();
        }}
      />

      <Select
        label="Category"
        value={categoryId}
        placeholder="Uncategorised"
        options={categoryOptions}
        onChange={(event) => {
          setCategoryId(event.target.value);
          clearErrors();
        }}
      />

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <Input
          label="Package size"
          placeholder="e.g. 1 or 350"
          value={sizeValue}
          error={fieldErrors.sizeValue}
          onChange={(event) => {
            setSizeValue(event.target.value);
            clearErrors();
          }}
        />
        <Input
          label="Size unit"
          placeholder="e.g. l, kg, g, ml, ad"
          maxLength={20}
          value={sizeUnit}
          error={fieldErrors.sizeUnit}
          onChange={(event) => {
            setSizeUnit(event.target.value);
            clearErrors();
          }}
        />
      </div>

      <div className="flex justify-end pt-1">
        <Button type="submit" loading={updateMutation.isPending}>
          Save details
        </Button>
      </div>
    </form>
  );
}
