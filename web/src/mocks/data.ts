import type { components } from '../api/schema.gen';

export type MockAuthMode = 'signed_in' | 'signed_out' | 'access_closed';

const MOCK_AUTH_STORAGE_KEY = 'receipt_tracker_mock_auth';
let memoryAuthMode: MockAuthMode = 'signed_in';

/** Reads initial mock authentication mode from URL query, storage, or memory. */
export function getMockAuthMode(): MockAuthMode {
  if (typeof window !== 'undefined') {
    try {
      const param = new URLSearchParams(window.location.search).get('mockAuth');
      if (
        param === 'signed_in' ||
        param === 'signed_out' ||
        param === 'access_closed'
      ) {
        return param;
      }
      const stored = window.localStorage.getItem(MOCK_AUTH_STORAGE_KEY);
      if (
        stored === 'signed_in' ||
        stored === 'signed_out' ||
        stored === 'access_closed'
      ) {
        return stored;
      }
    } catch {
      // Ignore storage access errors in restricted environments.
    }
  }
  return memoryAuthMode;
}

/** Updates the active mock authentication mode for MSW handlers. */
export function setMockAuthMode(mode: MockAuthMode): void {
  memoryAuthMode = mode;
  if (typeof window !== 'undefined') {
    try {
      window.localStorage.setItem(MOCK_AUTH_STORAGE_KEY, mode);
    } catch {
      // Ignore storage access errors in restricted environments.
    }
  }
}

/** Resets mock authentication and stateful mock upload data. */
export function resetMockAuth(): void {
  memoryAuthMode = 'signed_in';
  resetMockState();
  if (typeof window !== 'undefined') {
    try {
      window.localStorage.removeItem(MOCK_AUTH_STORAGE_KEY);
    } catch {
      // Ignore storage access errors in restricted environments.
    }
  }
}

export const mockUser: components['schemas']['User'] = {
  id: '0193a1b2-0000-7000-8000-000000000001',
  email: 'ada@example.com',
  name: 'Ada Example',
  isAdmin: true,
};

export const mockUploadResponse: components['schemas']['UploadResponse'] = {
  results: [
    {
      index: 0,
      fileName: 'market-2026-11-02.heic',
      outcome: 'created',
      receiptId: '0193a1b2-0000-7000-8000-000000000100',
      error: null,
    },
    {
      index: 1,
      fileName: 'market-2026-11-02-copy.heic',
      outcome: 'duplicate',
      receiptId: '0193a1b2-0000-7000-8000-000000000100',
      error: null,
    },
    {
      index: 2,
      fileName: 'notes.pdf',
      outcome: 'rejected',
      receiptId: null,
      error: {
        code: 'unsupported_type',
        message: 'Only JPEG, PNG and HEIC photos can be uploaded.',
      },
    },
  ],
};

export const mockReceiptPage: components['schemas']['ReceiptPage'] = {
  items: [
    {
      id: '0193a1b2-0000-7000-8000-000000000101',
      title: 'Home shopping',
      store: null,
      purchasedOn: null,
      currency: 'TRY',
      total: null,
      status: 'queued',
      failureReason: null,
      source: 'web',
      openFlagCount: 0,
      createdAt: '2026-11-03T09:14:40Z',
    },
    {
      id: '0193a1b2-0000-7000-8000-000000000100',
      title: 'Home shopping',
      store: {
        id: '0193a1b2-0000-7000-8000-000000000010',
        name: 'Example Market',
      },
      purchasedOn: '2026-11-02',
      currency: 'TRY',
      total: '129.40',
      status: 'needs_review',
      failureReason: null,
      source: 'web',
      openFlagCount: 2,
      createdAt: '2026-11-02T16:05:12Z',
    },
  ],
  nextCursor: null,
};

export const mockReceipt: components['schemas']['Receipt'] = {
  id: '0193a1b2-0000-7000-8000-000000000100',
  title: 'Home shopping',
  store: {
    id: '0193a1b2-0000-7000-8000-000000000010',
    name: 'Example Market',
  },
  purchasedOn: '2026-11-02',
  purchasedTime: '18:42',
  currency: 'TRY',
  subtotal: null,
  taxTotal: '7.65',
  total: '129.40',
  status: 'needs_review',
  failureReason: null,
  source: 'web',
  openFlagCount: 2,
  createdAt: '2026-11-02T16:05:12Z',
  updatedAt: '2026-11-02T16:05:31Z',
  lineItems: [
    {
      id: '0193a1b2-0000-7000-8000-000000000201',
      position: 0,
      rawName: 'AYRAN 1LT',
      name: 'Ayran 1 l',
      quantity: '2',
      quantityUnit: 'ad',
      unitPrice: '42.25',
      lineTotal: '84.50',
      productCode: '004512',
      confidence: 0.97,
      product: {
        id: '0193a1b2-0000-7000-8000-000000000401',
        canonicalName: 'Ayran 1 l',
      },
      matchStatus: 'auto_linked',
      matchScore: 1,
      matchMethod: 'alias',
    },
    {
      id: '0193a1b2-0000-7000-8000-000000000202',
      position: 1,
      rawName: 'EKMEK',
      name: 'Ekmek',
      quantity: '1',
      quantityUnit: 'ad',
      unitPrice: '15.00',
      lineTotal: '15.00',
      productCode: null,
      confidence: 0.94,
      product: null,
      matchStatus: 'needs_review',
      matchScore: null,
      matchMethod: null,
    },
    {
      id: '0193a1b2-0000-7000-8000-000000000203',
      position: 2,
      rawName: 'SUT 1LT TAM YAGLI',
      name: 'Süt 1 l tam yağlı',
      quantity: '1',
      quantityUnit: 'ad',
      unitPrice: '34.90',
      lineTotal: '34.90',
      productCode: null,
      confidence: 0.71,
      product: {
        id: '0193a1b2-0000-7000-8000-000000000402',
        canonicalName: 'Süt 1 l tam yağlı',
      },
      matchStatus: 'new_product',
      matchScore: null,
      matchMethod: null,
    },
    {
      id: '0193a1b2-0000-7000-8000-000000000204',
      position: 3,
      rawName: 'INDIRIM',
      name: 'İndirim',
      quantity: null,
      quantityUnit: null,
      unitPrice: null,
      lineTotal: '-5.00',
      productCode: null,
      confidence: 0.95,
      product: null,
      matchStatus: 'unmatched',
      matchScore: null,
      matchMethod: null,
    },
  ],
  flags: [
    {
      id: '0193a1b2-0000-7000-8000-000000000301',
      receiptId: '0193a1b2-0000-7000-8000-000000000100',
      lineItemId: '0193a1b2-0000-7000-8000-000000000203',
      createdAt: '2026-11-02T16:05:31Z',
      resolvedAt: null,
      resolution: null,
      kind: 'low_confidence',
      detail: {
        field: 'line_item',
        confidence: 0.71,
      },
    },
    {
      id: '0193a1b2-0000-7000-8000-000000000302',
      receiptId: '0193a1b2-0000-7000-8000-000000000100',
      lineItemId: '0193a1b2-0000-7000-8000-000000000202',
      createdAt: '2026-11-02T16:05:31Z',
      resolvedAt: null,
      resolution: null,
      kind: 'uncertain_match',
      detail: {
        reason: 'similar_name',
        candidates: [
          {
            productId: '0193a1b2-0000-7000-8000-000000000403',
            canonicalName: 'Ekmek 350 g',
            sizeValue: '350',
            sizeUnit: 'g',
            score: 0.78,
          },
          {
            productId: '0193a1b2-0000-7000-8000-000000000404',
            canonicalName: 'Kepekli ekmek 400 g',
            sizeValue: '400',
            sizeUnit: 'g',
            score: 0.61,
          },
        ],
      },
    },
  ],
  images: [
    {
      id: '0193a1b2-0000-7000-8000-000000000110',
      position: 0,
      contentType: 'image/jpeg',
      sizeBytes: 1843200,
      original: {
        contentType: 'image/heic',
        sizeBytes: 2516582,
      },
    },
  ],
};

export const mockReviewFlagPage: components['schemas']['ReviewFlagPage'] = {
  items: [
    {
      flag: mockReceipt.flags[1],
      receipt: mockReceiptPage.items[1],
      lineItem: mockReceipt.lineItems[1],
    },
  ],
  nextCursor: 'eyJ2IjoxfQ',
  totalOpen: 2,
};

export const mockProductPage: components['schemas']['ProductPage'] = {
  items: [
    {
      id: '0193a1b2-0000-7000-8000-000000000401',
      canonicalName: 'Ayran 1 l',
      category: {
        id: '0193a1b2-0000-7000-8000-000000000020',
        name: 'Dairy',
      },
      sizeValue: '1',
      sizeUnit: 'l',
      purchaseCount: 6,
      lastPurchase: {
        purchasedOn: '2026-11-02',
        unitPrice: '42.25',
        currency: 'TRY',
        store: {
          id: '0193a1b2-0000-7000-8000-000000000010',
          name: 'Example Market',
        },
      },
    },
    {
      id: '0193a1b2-0000-7000-8000-000000000403',
      canonicalName: 'Ekmek 350 g',
      category: null,
      sizeValue: '350',
      sizeUnit: 'g',
      purchaseCount: 0,
      lastPurchase: null,
    },
  ],
  nextCursor: null,
};

export const mockProduct: components['schemas']['Product'] = {
  id: '0193a1b2-0000-7000-8000-000000000401',
  canonicalName: 'Ayran 1 l',
  category: {
    id: '0193a1b2-0000-7000-8000-000000000020',
    name: 'Dairy',
  },
  sizeValue: '1',
  sizeUnit: 'l',
  purchaseCount: 6,
  lastPurchase: {
    purchasedOn: '2026-11-02',
    unitPrice: '42.25',
    currency: 'TRY',
    store: {
      id: '0193a1b2-0000-7000-8000-000000000010',
      name: 'Example Market',
    },
  },
  aliases: [
    {
      id: '0193a1b2-0000-7000-8000-000000000501',
      rawName: 'AYRAN 1LT',
      normalizedName: 'ayran 1 l',
      store: {
        id: '0193a1b2-0000-7000-8000-000000000010',
        name: 'Example Market',
      },
      seenCount: 5,
      lastSeenAt: '2026-11-02T16:05:31Z',
    },
    {
      id: '0193a1b2-0000-7000-8000-000000000502',
      rawName: 'AYRN 1 L',
      normalizedName: 'ayrn 1 l',
      store: {
        id: '0193a1b2-0000-7000-8000-000000000011',
        name: 'Corner Shop',
      },
      seenCount: 1,
      lastSeenAt: '2026-10-18T10:22:05Z',
    },
  ],
  codes: [
    {
      id: '0193a1b2-0000-7000-8000-000000000601',
      code: '004512',
      kind: 'store',
      store: {
        id: '0193a1b2-0000-7000-8000-000000000010',
        name: 'Example Market',
      },
      seenCount: 5,
    },
  ],
  createdAt: '2026-09-06T11:40:00Z',
  updatedAt: '2026-11-02T16:05:31Z',
};

export const mockPriceHistory: components['schemas']['PriceHistory'] = {
  productId: '0193a1b2-0000-7000-8000-000000000401',
  points: [
    {
      receiptId: '0193a1b2-0000-7000-8000-000000000098',
      lineItemId: '0193a1b2-0000-7000-8000-000000000291',
      purchasedOn: '2026-10-04',
      store: {
        id: '0193a1b2-0000-7000-8000-000000000010',
        name: 'Example Market',
      },
      unitPrice: '39.90',
      quantity: '1',
      quantityUnit: 'ad',
      currency: 'TRY',
    },
    {
      receiptId: '0193a1b2-0000-7000-8000-000000000099',
      lineItemId: '0193a1b2-0000-7000-8000-000000000295',
      purchasedOn: '2026-10-18',
      store: {
        id: '0193a1b2-0000-7000-8000-000000000011',
        name: 'Corner Shop',
      },
      unitPrice: '44.00',
      quantity: '1',
      quantityUnit: 'ad',
      currency: 'TRY',
    },
    {
      receiptId: '0193a1b2-0000-7000-8000-000000000100',
      lineItemId: '0193a1b2-0000-7000-8000-000000000201',
      purchasedOn: '2026-11-02',
      store: {
        id: '0193a1b2-0000-7000-8000-000000000010',
        name: 'Example Market',
      },
      unitPrice: '42.25',
      quantity: '2',
      quantityUnit: 'ad',
      currency: 'TRY',
    },
  ],
  stores: [
    {
      store: {
        id: '0193a1b2-0000-7000-8000-000000000010',
        name: 'Example Market',
      },
      currency: 'TRY',
      min: '39.90',
      max: '42.25',
      latest: '42.25',
      latestOn: '2026-11-02',
      purchaseCount: 2,
    },
    {
      store: {
        id: '0193a1b2-0000-7000-8000-000000000011',
        name: 'Corner Shop',
      },
      currency: 'TRY',
      min: '44.00',
      max: '44.00',
      latest: '44.00',
      latestOn: '2026-10-18',
      purchaseCount: 1,
    },
  ],
};

export const mockStorePage: components['schemas']['StorePage'] = {
  items: [
    {
      id: '0193a1b2-0000-7000-8000-000000000011',
      name: 'Corner Shop',
      receiptCount: 3,
    },
    {
      id: '0193a1b2-0000-7000-8000-000000000010',
      name: 'Example Market',
      receiptCount: 14,
    },
  ],
  nextCursor: null,
};

export const mockStore: components['schemas']['Store'] = {
  id: '0193a1b2-0000-7000-8000-000000000010',
  name: 'Example Market',
  receiptCount: 14,
};

export const mockCategoryPage: components['schemas']['CategoryPage'] = {
  items: [
    {
      id: '0193a1b2-0000-7000-8000-000000000021',
      name: 'Bakery',
      productCount: 4,
    },
    {
      id: '0193a1b2-0000-7000-8000-000000000020',
      name: 'Dairy',
      productCount: 9,
    },
  ],
  nextCursor: null,
};

export const mockCategory: components['schemas']['Category'] = {
  id: '0193a1b2-0000-7000-8000-000000000020',
  name: 'Dairy',
  productCount: 9,
};

export const mockMonthlySpending: components['schemas']['MonthlySpending'] = {
  from: '2026-10',
  to: '2026-11',
  months: [
    { month: '2026-10', totals: [] },
    {
      month: '2026-11',
      totals: [
        { currency: 'TRY', amount: '1294.00', receiptCount: 9 },
        { currency: 'EUR', amount: '23.80', receiptCount: 1 },
      ],
    },
  ],
  excludedReceipts: 1,
};

export const mockMonthBreakdown: components['schemas']['MonthBreakdown'] = {
  month: '2026-11',
  groupBy: 'category',
  currencies: [
    {
      currency: 'TRY',
      total: '1294.00',
      groups: [
        {
          kind: 'category',
          id: '0193a1b2-0000-7000-8000-000000000020',
          name: 'Dairy',
          amount: '845.00',
          share: '0.6530',
          itemCount: 21,
        },
        {
          kind: 'uncategorised',
          id: null,
          name: 'Uncategorised',
          amount: '436.50',
          share: '0.3373',
          itemCount: 17,
        },
        {
          kind: 'unallocated',
          id: null,
          name: 'Unallocated',
          amount: '12.50',
          share: '0.0097',
          itemCount: 0,
        },
      ],
    },
  ],
  excludedReceipts: 1,
};

export const mockSettings: components['schemas']['Settings'] = {
  defaultCurrency: 'TRY',
  defaultReceiptTitle: 'Home shopping',
};

export const mockUsage: components['schemas']['Usage'] = {
  provider: 'azure',
  period: '2026-11',
  pagesUsed: 137,
  pagesLimit: 500,
};

export const mockStorageStatus: components['schemas']['StorageStatus'] = {
  backend: 'drive',
  drive: 'ok',
  lastError: null,
  canManage: true,
  backup: {
    destination: 'drive',
    lastSuccessAt: '2026-11-13T03:15:42Z',
    lastError: null,
  },
};

export const mockProblemUnauthorized: components['schemas']['Problem'] = {
  type: 'about:blank',
  title: 'Unauthorized',
  status: 401,
  detail: 'sign in to continue',
  requestId: '0193a1b2-7c4d-7e2a-9f10-5b6c7d8e9f00',
};

export const mockProblemForbidden: components['schemas']['Problem'] = {
  type: 'about:blank',
  title: 'Forbidden',
  status: 403,
  detail: 'access is closed',
  requestId: '0193a1b2-7c4d-7e2a-9f10-5b6c7d8e9f00',
};

export const mockProblemNotFound: components['schemas']['Problem'] = {
  type: 'about:blank',
  title: 'Not Found',
  status: 404,
  detail: 'receipt not found',
  requestId: '0193a1b2-7c4d-7e2a-9f10-5b6c7d8e9f00',
};

// Valid 1x1 JPEG stream so mocked <img> elements decode cleanly in browsers.
const MOCK_JPEG_BASE64 =
  '/9j/4AAQSkZJRgABAQEAYABgAAD/2wBDAAMCAgMCAgMDAwMEAwMEBQgFBQQEBQoHBwYIDAoMDAsKCwsNDhIQDQ4RDgsLEBYQERMUFRUVDA8XGBYUGBIUFRT/2wBDAQMEBAUEBQkFBQkUDQsNFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBT/wAARCAABAAEDASIAAhEBAxEB/8QAHwAAAQUBAQEBAQEAAAAAAAAAAAECAwQFBgcICQoL/8QAtRAAAgEDAwIEAwUFBAQAAAF9AQIDAAQRBRIhMUEGE1FhByJxFDKBkaEII0KxwRVS0fAkM2JyggkKFhcYGRolJicoKSo0NTY3ODk6Q0RFRkdISUpTVFVWV1hZWmNkZWZnaGlqc3R1dnd4eXqDhIWGh4iJipKTlJWWl5iZmqKjpKWmp6ipqrKztLW2t7i5usLDxMXGx8jJytLT1NXW19jZ2uHi4+Tl5ufo6erx8vP09fb3+Pn6/8QAHwEAAwEBAQEBAQEBAQAAAAAAAAECAwQFBgcICQoL/8QAtREAAgECBAQDBAcFBAQAAQJ3AAECAxEEBSExBhJBUQdhcRMiMoEIFEKRobHBCSMzUvAVYnLRChYkNOEl8RcYGRomJygpKjU2Nzg5OkNERUZHSElKU1RVVldYWVpjZGVmZ2hpanN0dXZ3eHl6goOEhYaHiImKkpOUlZaXmJmaoqOkpaanqKmqsrO0tba3uLm6wsPExcbHyMnK0tPU1dbX2Nna4uPk5ebn6Onq8vP09fb3+Pn6/9oADAMBAAIRAxEAPwD9OKKKKAP/2Q==';

export const mockImageBytes = Uint8Array.from(atob(MOCK_JPEG_BASE64), (char) =>
  char.charCodeAt(0),
);

interface MockUploadedEntry {
  receipt: components['schemas']['Receipt'];
  polls: number;
  signature: string;
}

const MAX_UPLOAD_BYTES = 15 * 1024 * 1024;
const ALLOWED_UPLOAD_TYPES = new Set([
  'image/jpeg',
  'image/png',
  'image/heic',
  'image/heif',
]);
const ALLOWED_UPLOAD_EXTENSIONS = ['.jpg', '.jpeg', '.png', '.heic', '.heif'];

let uploadedMockEntries: MockUploadedEntry[] = [];
let nextMockUploadIndex = 700;
let resolvedMockFlagIds = new Set<string>();

/** Clears runtime mock state created during uploads and reviews so tests stay isolated. */
export function resetMockState(): void {
  uploadedMockEntries = [];
  nextMockUploadIndex = 700;
  resolvedMockFlagIds = new Set<string>();
}

function hasAllowedUploadType(file: { name: string; type: string }): boolean {
  const mime = file.type.toLowerCase();
  if (ALLOWED_UPLOAD_TYPES.has(mime)) return true;
  if (mime === '' || mime === 'application/octet-stream') {
    const lowerName = file.name.toLowerCase();
    return ALLOWED_UPLOAD_EXTENSIONS.some((ext) => lowerName.endsWith(ext));
  }
  return false;
}

function toSummary(
  receipt: components['schemas']['Receipt'],
): components['schemas']['ReceiptSummary'] {
  return {
    id: receipt.id,
    title: receipt.title,
    store: receipt.store,
    purchasedOn: receipt.purchasedOn,
    currency: receipt.currency,
    total: receipt.total,
    status: receipt.status,
    failureReason: receipt.failureReason,
    source: receipt.source,
    openFlagCount: receipt.openFlagCount,
    createdAt: receipt.createdAt,
  };
}

function advancePolledUpload(entry: MockUploadedEntry, fileName: string): void {
  entry.polls += 1;
  if (entry.polls === 1) {
    entry.receipt = { ...entry.receipt, status: 'processing' };
    return;
  }
  if (
    entry.receipt.status !== 'queued' &&
    entry.receipt.status !== 'processing'
  ) {
    return;
  }

  const lower = fileName.toLowerCase();
  const nowIso = '2026-11-14T18:00:00Z';
  if (lower.includes('fail')) {
    entry.receipt = {
      ...entry.receipt,
      status: 'failed',
      failureReason: 'Could not extract fields from the receipt photo.',
      updatedAt: nowIso,
    };
    return;
  }
  if (lower.includes('quota')) {
    entry.receipt = {
      ...entry.receipt,
      status: 'waiting_quota',
      updatedAt: nowIso,
    };
    return;
  }
  if (lower.includes('review')) {
    entry.receipt = {
      ...entry.receipt,
      status: 'needs_review',
      store: { id: mockStore.id, name: mockStore.name },
      purchasedOn: '2026-11-14',
      total: '48.50',
      openFlagCount: 1,
      updatedAt: nowIso,
    };
    return;
  }

  entry.receipt = {
    ...entry.receipt,
    status: 'ready',
    store: { id: mockStore.id, name: mockStore.name },
    purchasedOn: '2026-11-14',
    total: '64.20',
    openFlagCount: 0,
    updatedAt: nowIso,
  };
}

/** Processes uploaded file metadata into stateful mock receipts and UploadResults. */
export function registerMockUploads(
  files: readonly { name: string; type: string; size: number }[],
): components['schemas']['UploadResponse'] {
  const results: components['schemas']['UploadResult'][] = files.map(
    (file, index) => {
      if (!hasAllowedUploadType(file)) {
        return {
          index,
          fileName: file.name,
          outcome: 'rejected',
          receiptId: null,
          error: {
            code: 'unsupported_type',
            message: 'Only JPEG, PNG and HEIC photos can be uploaded.',
          },
        };
      }
      if (file.size > MAX_UPLOAD_BYTES) {
        return {
          index,
          fileName: file.name,
          outcome: 'rejected',
          receiptId: null,
          error: {
            code: 'too_large',
            message: 'Photo exceeds the 15 MB limit.',
          },
        };
      }
      if (file.size === 0) {
        return {
          index,
          fileName: file.name,
          outcome: 'rejected',
          receiptId: null,
          error: {
            code: 'unreadable',
            message: 'File is empty or unreadable.',
          },
        };
      }

      const signature = `${file.name}:${file.size}`;
      const existing = uploadedMockEntries.find(
        (entry) => entry.signature === signature,
      );
      if (existing) {
        return {
          index,
          fileName: file.name,
          outcome: 'duplicate',
          receiptId: existing.receipt.id,
          error: null,
        };
      }

      const seq = nextMockUploadIndex++;
      const receiptId = `0193a1b2-0000-7000-8000-${String(seq).padStart(12, '0')}`;
      const imageId = `0193a1b2-0000-7000-8000-${String(seq + 1000).padStart(12, '0')}`;
      const nowIso = '2026-11-14T18:00:00Z';
      const isHeic =
        file.name.toLowerCase().endsWith('.heic') ||
        file.name.toLowerCase().endsWith('.heif') ||
        file.type.toLowerCase().includes('heic') ||
        file.type.toLowerCase().includes('heif');

      const receipt: components['schemas']['Receipt'] = {
        id: receiptId,
        title: mockSettings.defaultReceiptTitle,
        store: null,
        purchasedOn: null,
        purchasedTime: null,
        currency: mockSettings.defaultCurrency,
        subtotal: null,
        taxTotal: null,
        total: null,
        status: 'queued',
        failureReason: null,
        source: 'web',
        openFlagCount: 0,
        createdAt: nowIso,
        updatedAt: nowIso,
        lineItems: [],
        flags: [],
        images: [
          {
            id: imageId,
            position: 0,
            contentType: 'image/jpeg',
            sizeBytes: file.size,
            original: isHeic
              ? {
                  contentType: 'image/heic',
                  sizeBytes: file.size,
                }
              : null,
          },
        ],
      };

      uploadedMockEntries.unshift({ receipt, polls: 0, signature });
      return {
        index,
        fileName: file.name,
        outcome: 'created',
        receiptId,
        error: null,
      };
    },
  );

  return { results };
}

/** Filters and paginates mock receipts according to listReceipts query parameters. */
export function queryMockReceipts(
  searchParams: URLSearchParams,
): components['schemas']['ReceiptPage'] {
  const ids = searchParams
    .getAll('ids')
    .flatMap((value) => value.split(','))
    .map((value) => value.trim())
    .filter(Boolean);

  if (ids.length > 0) {
    for (const entry of uploadedMockEntries) {
      if (ids.includes(entry.receipt.id)) {
        const fileName = entry.signature.split(':')[0] ?? '';
        advancePolledUpload(entry, fileName);
      }
    }
  }

  const allSummaries: components['schemas']['ReceiptSummary'][] = [
    ...uploadedMockEntries.map((entry) => toSummary(entry.receipt)),
    ...mockReceiptPage.items,
  ];

  const status = searchParams.get('status')?.trim() || null;
  const month = searchParams.get('month')?.trim() || null;
  const storeId = searchParams.get('storeId')?.trim() || null;
  const q = searchParams.get('q')?.trim().toLowerCase() || null;

  const filtered = allSummaries.filter((summary) => {
    if (ids.length > 0 && !ids.includes(summary.id)) return false;
    if (status && summary.status !== status) return false;
    if (month && !(summary.purchasedOn?.startsWith(month) ?? false)) {
      return false;
    }
    if (storeId && summary.store?.id !== storeId) return false;
    if (q) {
      const titleMatch = summary.title.toLowerCase().includes(q);
      const storeMatch = summary.store?.name.toLowerCase().includes(q) ?? false;
      const uploadedDetail = uploadedMockEntries.find(
        (entry) => entry.receipt.id === summary.id,
      )?.receipt;
      const detail =
        uploadedDetail ??
        (summary.id === mockReceipt.id ? mockReceipt : undefined);
      const lineMatch =
        detail?.lineItems.some(
          (line) =>
            line.name.toLowerCase().includes(q) ||
            line.rawName.toLowerCase().includes(q),
        ) ?? false;
      if (!titleMatch && !storeMatch && !lineMatch) return false;
    }
    return true;
  });

  const rawLimit = Number.parseInt(searchParams.get('limit') ?? '50', 10);
  const limit =
    Number.isFinite(rawLimit) && rawLimit >= 1 && rawLimit <= 100
      ? rawLimit
      : 50;
  const rawOffset = Number.parseInt(searchParams.get('cursor') ?? '0', 10);
  const offset = Number.isFinite(rawOffset) && rawOffset >= 0 ? rawOffset : 0;

  const pageItems = filtered.slice(offset, offset + limit);
  const nextOffset = offset + pageItems.length;
  const nextCursor = nextOffset < filtered.length ? String(nextOffset) : null;

  return {
    items: pageItems,
    nextCursor,
  };
}

/** Looks up a stateful uploaded mock receipt before falling back to mockReceipt. */
export function getMockReceiptById(
  receiptId: string,
): components['schemas']['Receipt'] | null {
  if (receiptId === 'missing') return null;
  const uploaded = uploadedMockEntries.find(
    (entry) => entry.receipt.id === receiptId,
  );
  if (uploaded) return uploaded.receipt;
  const nowIso = new Date().toISOString();
  const flags = mockReceipt.flags.map((flag) =>
    resolvedMockFlagIds.has(flag.id)
      ? { ...flag, resolvedAt: flag.resolvedAt ?? nowIso }
      : flag,
  );
  const openFlagCount = flags.filter((flag) => flag.resolvedAt === null).length;
  return {
    ...mockReceipt,
    id: receiptId,
    openFlagCount,
    status: openFlagCount === 0 ? 'ready' : mockReceipt.status,
    flags,
  };
}

/** Returns remaining open mock review flags for the interactive review queue. */
export function queryMockReviewFlags(): components['schemas']['ReviewFlagPage'] {
  const orderedFlags = [mockReceipt.flags[1], mockReceipt.flags[0]];
  const openItems: components['schemas']['ReviewFlagEntry'][] = [];
  for (const flag of orderedFlags) {
    if (!flag || resolvedMockFlagIds.has(flag.id)) continue;
    const lineItem =
      flag.lineItemId !== null
        ? (mockReceipt.lineItems.find((item) => item.id === flag.lineItemId) ??
          null)
        : null;
    openItems.push({
      flag,
      receipt: mockReceiptPage.items[1],
      lineItem,
    });
  }
  return {
    items: openItems,
    nextCursor: null,
    totalOpen: openItems.length,
  };
}

/** Marks a mock review flag as resolved and returns the updated receipt. */
export function resolveMockReviewFlag(
  flagId: string,
): components['schemas']['Receipt'] {
  resolvedMockFlagIds.add(flagId);
  return getMockReceiptById(mockReceipt.id) ?? mockReceipt;
}
