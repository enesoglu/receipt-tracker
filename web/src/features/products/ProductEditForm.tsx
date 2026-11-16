import { useState, type FormEvent } from 'react';
import { getErrorMessage } from '../../api/errors';
import { useUpdateProduct, type Product } from '../../api/products';
import { Button } from '../../components/ui/Button';
import { ErrorState } from '../../components/ui/ErrorState';
import { Input } from '../../components/ui/Input';
import { Select } from '../../components/ui/Select';

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

  const updateMutation = useUpdateProduct(product.id);

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setValidationError(null);
    setServerError(null);

    const trimmedName = canonicalName.trim();
    if (!trimmedName) {
      setValidationError('Canonical name is required.');
      return;
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
    if (trimmedSizeValue !== '' && !/^\d+(?:\.\d+)?$/.test(trimmedSizeValue)) {
      setValidationError('Package size must be a positive decimal number.');
      return;
    }

    try {
      await updateMutation.mutateAsync({
        canonicalName: trimmedName,
        categoryId: categoryId || null,
        sizeValue: trimmedSizeValue || null,
        sizeUnit: trimmedSizeUnit || null,
      });
      onSaveNotice('Product details saved.');
    } catch (error) {
      setServerError(getErrorMessage(error));
    }
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
        onChange={(event) => {
          setCanonicalName(event.target.value);
          setValidationError(null);
          setServerError(null);
        }}
      />

      <Select
        label="Category"
        value={categoryId}
        placeholder="Uncategorised"
        options={categoryOptions}
        onChange={(event) => {
          setCategoryId(event.target.value);
          setServerError(null);
        }}
      />

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <Input
          label="Package size"
          placeholder="e.g. 1 or 350"
          value={sizeValue}
          onChange={(event) => {
            setSizeValue(event.target.value);
            setValidationError(null);
            setServerError(null);
          }}
        />
        <Input
          label="Size unit"
          placeholder="e.g. l, kg, g, ml, ad"
          maxLength={20}
          value={sizeUnit}
          onChange={(event) => {
            setSizeUnit(event.target.value);
            setValidationError(null);
            setServerError(null);
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
