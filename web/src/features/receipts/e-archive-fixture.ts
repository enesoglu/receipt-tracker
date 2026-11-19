import type { Receipt, ReceiptChain } from '../../api/receipts';
import { mockReceipt } from '../../mocks/data';

/** Public BİM registry facts with synthetic receipt identity values. */
export const bimChain: ReceiptChain = {
  id: 'bim',
  name: 'BİM',
  portalUrl: 'https://arsiv.bim.com.tr/',
  captcha: 'none',
  fields: [
    {
      name: 'belgeno',
      description: 'Invoice number, ETTN or barcode/reference',
      value: 'BIM2026001234',
    },
    { name: 'tarih', description: 'Invoice date', value: '05-03-2026' },
    { name: 'tutar', description: 'Total paid', value: '129.40' },
  ],
};

export const chainReceipt: Receipt = {
  ...mockReceipt,
  id: '0193a1b2-0000-7000-8000-000000000160',
  purchasedOn: '2026-03-05',
  total: '129.40',
  invoiceNumber: 'BIM2026001234',
  ettn: null,
  sellerTaxNumber: '1750051846',
  chain: bimChain,
};
