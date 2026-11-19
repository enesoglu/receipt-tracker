import { describe, expect, it } from 'vitest';
import {
  decimalMessage,
  normaliseDecimal,
  positiveQuantityMessage,
  validateDecimal,
} from './decimal';

describe('normaliseDecimal', () => {
  it('trims, turns the decimal comma into a dot and blanks empty input', () => {
    expect(normaliseDecimal(' 351,9 ')).toBe('351.9');
    expect(normaliseDecimal('.5')).toBe('.5');
    expect(normaliseDecimal('5,')).toBe('5.');
    expect(normaliseDecimal('   ')).toBe('');
    expect(normaliseDecimal('')).toBe('');
  });
});

describe('validateDecimal', () => {
  it('accepts every flexible money form without a message', () => {
    for (const value of [
      '351.9',
      '351,9',
      ' 351.9 ',
      '351',
      '351.90',
      '.5',
      ',5',
      '5.',
      '5,',
      '351,',
      '-12,5',
      '0',
      '9999999999.99',
    ]) {
      expect(validateDecimal(value, 'money')).toBeNull();
    }
  });

  it('accepts every flexible quantity form, but only positive ones', () => {
    for (const value of [
      '1,25',
      '1.25',
      '0,114',
      '351.999',
      '.5',
      '351,',
      '999999999.999',
    ]) {
      expect(validateDecimal(value, 'quantity')).toBeNull();
    }
    for (const value of ['0', '0,0', '-2', '-0.5', ',0']) {
      expect(validateDecimal(value, 'quantity')).toBe(positiveQuantityMessage);
    }
  });

  it('rejects wrong bounds, grouped thousands and malformed text', () => {
    for (const [value, kind] of [
      ['0,114', 'money'],
      ['351.999', 'money'],
      ['1.2504', 'money'],
      ['1.234,56', 'money'],
      ['10000000000.00', 'money'],
      ['abc', 'money'],
      ['3 51.9', 'money'],
      ['+12.5', 'money'],
      ['.', 'money'],
      [',', 'money'],
      ['1.2504', 'quantity'],
      ['1000000000', 'quantity'],
      ['1.234,56', 'quantity'],
      ['two', 'quantity'],
    ] as const) {
      expect(validateDecimal(value, kind)).toBe(decimalMessage(kind));
    }
  });

  it('treats blank input as valid; required fields check for it separately', () => {
    expect(validateDecimal('', 'money')).toBeNull();
    expect(validateDecimal('   ', 'quantity')).toBeNull();
  });
});
