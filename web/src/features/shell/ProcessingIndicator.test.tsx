import { act, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from '../../api/client';
import { createAppQueryClient } from '../../api/query-client';
import { QueryProvider } from '../../api/QueryProvider';
import { ProcessingIndicator } from './ProcessingIndicator';

const unsettledItem = (index: number) => ({
  receiptId: `rec-${index}`,
  stage: 'queued',
  queuePosition: index + 1,
  failureReason: null,
});

function pipelineResult(count: number) {
  return {
    data: { items: Array.from({ length: count }, (_, i) => unsettledItem(i)) },
    response: new Response(null, { status: 200 }),
  };
}

function renderIndicator() {
  const client = createAppQueryClient({ staleTime: 0 });
  return render(
    <QueryProvider client={client}>
      <MemoryRouter>
        <ProcessingIndicator />
      </MemoryRouter>
    </QueryProvider>,
  );
}

describe('ProcessingIndicator', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('shows the unsettled count and links to the upload screen', async () => {
    vi.spyOn(api, 'GET').mockResolvedValue(pipelineResult(3) as never);

    renderIndicator();

    const link = await screen.findByRole('link', {
      name: 'Processing 3 receipts',
    });
    expect(link).toHaveAttribute('href', '/upload');
    // Static by design: a fixed structure whose only change is the count;
    // the style policy blocks motion utilities in the component source.
    expect(link.querySelector('svg')).toBeNull();
    expect(link.querySelectorAll('span')).toHaveLength(2);
    expect(link.firstElementChild).toHaveAttribute('aria-hidden', 'true');
  });

  it('renders nothing while the pipeline is empty', async () => {
    const getSpy = vi
      .spyOn(api, 'GET')
      .mockResolvedValue(pipelineResult(0) as never);

    renderIndicator();

    await waitFor(() =>
      expect(getSpy).toHaveBeenCalledWith('/pipeline', {
        params: { query: {} },
      }),
    );
    expect(
      screen.queryByRole('link', { name: /Processing/ }),
    ).not.toBeInTheDocument();
  });

  it('polls every 2 seconds while receipts are unsettled and stops at zero', async () => {
    vi.useFakeTimers();
    let pollCount = 0;
    vi.spyOn(api, 'GET').mockImplementation((path) => {
      expect(path).toBe('/pipeline');
      pollCount += 1;
      // Two unsettled polls, then the pipeline empties.
      return Promise.resolve(pipelineResult(pollCount <= 2 ? 1 : 0)) as never;
    });

    renderIndicator();
    // The 100ms async advance lets the first fetch resolve and commit.
    await vi.advanceTimersByTimeAsync(100);

    expect(pollCount).toBe(1);
    expect(
      screen.getByRole('link', { name: 'Processing 1 receipt' }),
    ).toBeInTheDocument();

    await vi.advanceTimersByTimeAsync(2000);
    expect(pollCount).toBe(2);

    await vi.advanceTimersByTimeAsync(2000);
    expect(pollCount).toBe(3);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(
      screen.queryByRole('link', { name: /Processing/ }),
    ).not.toBeInTheDocument();

    // No further polling once the pipeline is empty.
    await vi.advanceTimersByTimeAsync(10000);
    expect(pollCount).toBe(3);
  });
});
