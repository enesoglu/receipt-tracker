export type DecimalKind = 'money' | 'quantity';

// The forms one decimal input may take, after the comma has become a dot:
// digits with an optional fraction ("351", "351.9", "5."), or a fraction
// alone (".5"). A value holding both separators is rejected before the
// shape is tried: a thousands group such as "1.234,56" cannot be read
// without a locale. The shapes are sign-agnostic; whether a quantity may
// be negative or zero is a semantic rule the server enforces.
const SHAPES: Record<DecimalKind, RegExp> = {
  money: /^-?(\d{1,10}(\.\d{0,2})?|\.\d{1,2})$/,
  quantity: /^-?(\d{1,9}(\.\d{0,3})?|\.\d{1,3})$/,
};

/**
 * The message shown when a decimal field cannot be sent, in the same
 * wording the API uses in its field errors.
 */
export function decimalMessage(kind: DecimalKind): string {
  return kind === 'money'
    ? 'enter a number with up to two decimal places, using . or , — such as 351.90'
    : 'enter a number with up to three decimal places, using . or , — such as 1.250';
}

/** The message shown when a quantity is zero or negative. */
export const positiveQuantityMessage =
  'must be positive with at most three decimal places';

/** Trims and turns the decimal comma into a dot; '' when blank. */
export function normaliseDecimal(value: string): string {
  const trimmed = value.trim();
  return trimmed ? trimmed.replace(',', '.') : '';
}

/**
 * Returns why the value cannot be sent, or null when it can. Blank is
 * valid: required fields check for it separately. The quantity variant
 * also rejects zero and negative values, like the server does.
 */
export function validateDecimal(
  value: string,
  kind: DecimalKind,
): string | null {
  const trimmed = value.trim();
  if (!trimmed) return null;
  if (trimmed.includes('.') && trimmed.includes(',')) {
    return decimalMessage(kind);
  }
  const normalised = normaliseDecimal(trimmed);
  if (!SHAPES[kind].test(normalised)) {
    return decimalMessage(kind);
  }
  if (
    kind === 'quantity' &&
    (normalised.startsWith('-') || !/[1-9]/.test(normalised))
  ) {
    return positiveQuantityMessage;
  }
  return null;
}
