import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useLogout, useMe } from './auth';
import {
  useCategories,
  useCreateCategory,
  useDeleteCategory,
  useUpdateCategory,
} from './categories';
import { api } from './client';
import {
  useMergeProducts,
  useProduct,
  useProductPriceHistory,
  useProducts,
  useSplitProduct,
  useUpdateProduct,
} from './products';
import { queryKeys } from './query-keys';
import { createAppQueryClient } from './query-client';
import { QueryProvider } from './QueryProvider';
import {
  receiptImageUrl,
  useCreateLineItem,
  useDeleteLineItem,
  useDeleteReceipt,
  useReceipt,
  useReceipts,
  useReprocessReceipt,
  useUpdateLineItem,
  useUpdateReceipt,
} from './receipts';
import { useMonthBreakdown, useMonthlySpending } from './reports';
import { useResolveReviewFlag, useReviewFlags } from './review';
import { useSettings, useUpdateSettings, useUsage } from './settings';
import { useDisconnectStorage, useStorageStatus } from './storage';
import { useStores, useUpdateStore } from './stores';
import { useUploadReceipts } from './uploads';

function okResult<Data>(data: Data) {
  return Promise.resolve({
    data,
    response: new Response(null, { status: 200 }),
  }) as ReturnType<typeof api.GET>;
}

function createWrapper() {
  const client = createAppQueryClient({ staleTime: 0 });
  function Wrapper({ children }: { children: ReactNode }) {
    return <QueryProvider client={client}>{children}</QueryProvider>;
  }
  return { client, Wrapper };
}

describe('API query and mutation hooks', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('calls auth hooks and clears cached user + queries on logout', async () => {
    const getSpy = vi.spyOn(api, 'GET').mockResolvedValue(
      okResult({
        id: 'u1',
        email: 'ada@example.com',
        name: 'Ada Example',
        isAdmin: true,
      }) as never,
    );
    const postSpy = vi
      .spyOn(api, 'POST')
      .mockResolvedValue(okResult(undefined) as never);

    const { client, Wrapper } = createWrapper();
    client.setQueryData(queryKeys.receipts.list(), {
      items: [],
      nextCursor: null,
    });

    const meHook = renderHook(() => useMe(), { wrapper: Wrapper });
    await waitFor(() => expect(meHook.result.current.data?.id).toBe('u1'));
    expect(getSpy).toHaveBeenCalledWith('/me');

    const logoutHook = renderHook(() => useLogout(), { wrapper: Wrapper });
    await act(async () => {
      await logoutHook.result.current.mutateAsync();
    });

    expect(postSpy).toHaveBeenCalledWith('/auth/logout');
    expect(client.getQueryData(queryKeys.me)).toBeNull();
    expect(client.getQueryData(queryKeys.receipts.list())).toBeUndefined();
  });

  it('uploads receipt files as FormData and invalidates receipts and usage', async () => {
    const postSpy = vi.spyOn(api, 'POST').mockImplementation((path, init) => {
      expect(path).toBe('/receipts');
      const serializer = (init as { bodySerializer?: (b: unknown) => FormData })
        .bodySerializer;
      expect(serializer).toBeDefined();
      const form = serializer?.((init as { body: unknown }).body);
      expect(form).toBeInstanceOf(FormData);
      expect(form?.getAll('files')).toHaveLength(2);
      return okResult({ results: [] }) as never;
    });

    const { client, Wrapper } = createWrapper();
    const invalidateSpy = vi.spyOn(client, 'invalidateQueries');
    const hook = renderHook(() => useUploadReceipts(), { wrapper: Wrapper });

    const files = [
      new File(['a'], 'a.jpg', { type: 'image/jpeg' }),
      new File(['b'], 'b.png', { type: 'image/png' }),
    ];

    await act(async () => {
      await hook.result.current.mutateAsync(files);
    });

    expect(postSpy).toHaveBeenCalledTimes(1);
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: queryKeys.receipts.all,
    });
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: queryKeys.settings.usage,
    });
  });

  it('covers receipt, line item, and image URL hooks', async () => {
    expect(receiptImageUrl('rec-1', 'img-1')).toBe(
      '/api/v1/receipts/rec-1/images/img-1',
    );
    expect(receiptImageUrl('rec-1', 'img-1', 'original')).toBe(
      '/api/v1/receipts/rec-1/images/img-1?variant=original',
    );

    const sampleReceipt = { id: 'rec-1' };
    const getSpy = vi.spyOn(api, 'GET').mockImplementation((path) => {
      if (path === '/receipts') {
        return okResult({ items: [], nextCursor: null }) as never;
      }
      return okResult(sampleReceipt) as never;
    });
    const putSpy = vi
      .spyOn(api, 'PUT')
      .mockResolvedValue(okResult(sampleReceipt) as never);
    const postSpy = vi
      .spyOn(api, 'POST')
      .mockResolvedValue(okResult(sampleReceipt) as never);
    const deleteSpy = vi
      .spyOn(api, 'DELETE')
      .mockResolvedValue(okResult(sampleReceipt) as never);

    const { client, Wrapper } = createWrapper();

    const listHook = renderHook(() => useReceipts({ status: 'needs_review' }), {
      wrapper: Wrapper,
    });
    await waitFor(() => expect(listHook.result.current.isSuccess).toBe(true));

    const detailHook = renderHook(() => useReceipt('rec-1'), {
      wrapper: Wrapper,
    });
    await waitFor(() => expect(detailHook.result.current.isSuccess).toBe(true));
    expect(getSpy).toHaveBeenCalledWith('/receipts/{receiptId}', {
      params: { path: { receiptId: 'rec-1' } },
    });

    const updateHook = renderHook(() => useUpdateReceipt('rec-1'), {
      wrapper: Wrapper,
    });
    const reprocessHook = renderHook(() => useReprocessReceipt('rec-1'), {
      wrapper: Wrapper,
    });
    const createLineHook = renderHook(() => useCreateLineItem('rec-1'), {
      wrapper: Wrapper,
    });
    const updateLineHook = renderHook(() => useUpdateLineItem('rec-1'), {
      wrapper: Wrapper,
    });
    const deleteLineHook = renderHook(() => useDeleteLineItem('rec-1'), {
      wrapper: Wrapper,
    });
    const deleteReceiptHook = renderHook(() => useDeleteReceipt(), {
      wrapper: Wrapper,
    });

    await act(async () => {
      await updateHook.result.current.mutateAsync({
        title: 'Market',
        currency: 'TRY',
      });
      await reprocessHook.result.current.mutateAsync();
      await createLineHook.result.current.mutateAsync({
        name: 'MILK',
        quantity: '1',
        quantityUnit: 'each',
        lineTotal: '42.00',
      });
      await updateLineHook.result.current.mutateAsync({
        lineItemId: 'li-1',
        body: {
          name: 'MILK 1L',
          quantity: '1',
          quantityUnit: 'each',
          lineTotal: '42.00',
        },
      });
      await deleteLineHook.result.current.mutateAsync('li-1');
      await deleteReceiptHook.result.current.mutateAsync('rec-1');
    });

    expect(putSpy).toHaveBeenCalledTimes(2);
    expect(postSpy).toHaveBeenCalledTimes(2);
    expect(deleteSpy).toHaveBeenCalledTimes(2);
    expect(
      client.getQueryData(queryKeys.receipts.detail('rec-1')),
    ).toBeUndefined();
  });

  it('covers review, product, store, category, report, settings, and storage hooks', async () => {
    vi.spyOn(api, 'GET').mockResolvedValue(okResult({}) as never);
    vi.spyOn(api, 'POST').mockResolvedValue(okResult({ id: 'res-1' }) as never);
    vi.spyOn(api, 'PUT').mockResolvedValue(okResult({}) as never);
    vi.spyOn(api, 'DELETE').mockResolvedValue(okResult(undefined) as never);

    const { Wrapper } = createWrapper();

    const hooks = {
      reviewList: renderHook(() => useReviewFlags({ limit: 5 }), {
        wrapper: Wrapper,
      }),
      resolveReview: renderHook(() => useResolveReviewFlag(), {
        wrapper: Wrapper,
      }),
      productsList: renderHook(() => useProducts({ q: 'ayran' }), {
        wrapper: Wrapper,
      }),
      productDetail: renderHook(() => useProduct('prod-1'), {
        wrapper: Wrapper,
      }),
      productUpdate: renderHook(() => useUpdateProduct('prod-1'), {
        wrapper: Wrapper,
      }),
      productMerge: renderHook(() => useMergeProducts('prod-1'), {
        wrapper: Wrapper,
      }),
      productSplit: renderHook(() => useSplitProduct('prod-1'), {
        wrapper: Wrapper,
      }),
      priceHistory: renderHook(
        () => useProductPriceHistory('prod-1', { from: '2026-01-01' }),
        { wrapper: Wrapper },
      ),
      storesList: renderHook(() => useStores(), { wrapper: Wrapper }),
      storeUpdate: renderHook(() => useUpdateStore(), { wrapper: Wrapper }),
      categoriesList: renderHook(() => useCategories(), { wrapper: Wrapper }),
      categoryCreate: renderHook(() => useCreateCategory(), {
        wrapper: Wrapper,
      }),
      categoryUpdate: renderHook(() => useUpdateCategory(), {
        wrapper: Wrapper,
      }),
      categoryDelete: renderHook(() => useDeleteCategory(), {
        wrapper: Wrapper,
      }),
      monthlySpending: renderHook(
        () => useMonthlySpending({ from: '2026-01', to: '2026-11' }),
        { wrapper: Wrapper },
      ),
      monthBreakdown: renderHook(
        () => useMonthBreakdown({ month: '2026-11', groupBy: 'category' }),
        { wrapper: Wrapper },
      ),
      settings: renderHook(() => useSettings(), { wrapper: Wrapper }),
      settingsUpdate: renderHook(() => useUpdateSettings(), {
        wrapper: Wrapper,
      }),
      usage: renderHook(() => useUsage(), { wrapper: Wrapper }),
      storageStatus: renderHook(() => useStorageStatus(), { wrapper: Wrapper }),
      storageDisconnect: renderHook(() => useDisconnectStorage(), {
        wrapper: Wrapper,
      }),
    };

    await waitFor(() => {
      expect(hooks.reviewList.result.current.isSuccess).toBe(true);
      expect(hooks.productsList.result.current.isSuccess).toBe(true);
      expect(hooks.productDetail.result.current.isSuccess).toBe(true);
      expect(hooks.priceHistory.result.current.isSuccess).toBe(true);
      expect(hooks.storesList.result.current.isSuccess).toBe(true);
      expect(hooks.categoriesList.result.current.isSuccess).toBe(true);
      expect(hooks.monthlySpending.result.current.isSuccess).toBe(true);
      expect(hooks.monthBreakdown.result.current.isSuccess).toBe(true);
      expect(hooks.settings.result.current.isSuccess).toBe(true);
      expect(hooks.usage.result.current.isSuccess).toBe(true);
      expect(hooks.storageStatus.result.current.isSuccess).toBe(true);
    });

    await act(async () => {
      await hooks.resolveReview.result.current.mutateAsync({
        flagId: 'flag-1',
        body: { action: 'accept' },
      });
      await hooks.productUpdate.result.current.mutateAsync({
        canonicalName: 'Ayran 1 L',
      });
      await hooks.productMerge.result.current.mutateAsync({
        sourceProductId: 'prod-2',
      });
      await hooks.productSplit.result.current.mutateAsync({
        aliasIds: ['alias-1'],
        canonicalName: 'Ayran 500 ml',
      });
      await hooks.storeUpdate.result.current.mutateAsync({
        storeId: 'store-1',
        body: { name: 'Corner Market' },
      });
      await hooks.categoryCreate.result.current.mutateAsync({ name: 'Dairy' });
      await hooks.categoryUpdate.result.current.mutateAsync({
        categoryId: 'cat-1',
        body: { name: 'Dairy & Eggs' },
      });
      await hooks.categoryDelete.result.current.mutateAsync('cat-1');
      await hooks.settingsUpdate.result.current.mutateAsync({
        defaultCurrency: 'TRY',
        defaultReceiptTitle: 'Home shopping',
      });
      await hooks.storageDisconnect.result.current.mutateAsync();
    });
  });
});
