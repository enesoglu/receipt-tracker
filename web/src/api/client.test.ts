import { describe, expect, it } from 'vitest';
import { ApiError, createApiClient, unwrap } from './client';

const receiptId = '0193a1b2-0000-7000-8000-000000000100';

/** A client whose transport is a function, so no network is involved. */
function clientAnswering(answer: (request: Request) => Response) {
  const requests: Request[] = [];
  const client = createApiClient({
    baseUrl: 'http://localhost/api/v1',
    fetch: async (request) => {
      requests.push(request);
      return answer(request);
    },
  });
  return { client, requests };
}

function json(status: number, body: unknown, contentType = 'application/json') {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': contentType },
  });
}

describe('createApiClient', () => {
  it('calls the versioned API and sends lists as one comma-separated value', async () => {
    const { client, requests } = clientAnswering(() =>
      json(200, { items: [], nextCursor: null }),
    );

    const page = await unwrap(
      client.GET('/receipts', {
        params: {
          query: {
            ids: [receiptId, '0193a1b2-0000-7000-8000-000000000101'],
            status: 'queued',
          },
        },
      }),
    );

    expect(page).toEqual({ items: [], nextCursor: null });
    expect(requests).toHaveLength(1);
    expect(requests[0].method).toBe('GET');
    expect(requests[0].url).toBe(
      `http://localhost/api/v1/receipts?ids=${receiptId},0193a1b2-0000-7000-8000-000000000101&status=queued`,
    );
  });

  it('fills path parameters and sends JSON bodies', async () => {
    const { client, requests } = clientAnswering(() =>
      json(200, { defaultCurrency: 'EUR', defaultReceiptTitle: 'Groceries' }),
    );

    const settings = await unwrap(
      client.PUT('/settings', {
        body: { defaultCurrency: 'EUR', defaultReceiptTitle: 'Groceries' },
      }),
    );
    await unwrap(
      client.GET('/receipts/{receiptId}', { params: { path: { receiptId } } }),
    );

    expect(settings.defaultCurrency).toBe('EUR');
    expect(requests[0].headers.get('Content-Type')).toBe('application/json');
    expect(await requests[0].json()).toEqual({
      defaultCurrency: 'EUR',
      defaultReceiptTitle: 'Groceries',
    });
    expect(requests[1].url).toBe(
      `http://localhost/api/v1/receipts/${receiptId}`,
    );
  });
});

describe('unwrap', () => {
  it('resolves to undefined for a response without a body', async () => {
    const { client } = clientAnswering(
      () => new Response(null, { status: 204 }),
    );

    const result = await unwrap(
      client.DELETE('/receipts/{receiptId}', {
        params: { path: { receiptId } },
      }),
    );

    expect(result).toBeUndefined();
  });

  it('throws the problem document the server sent', async () => {
    const problem = {
      type: 'about:blank',
      title: 'Bad Request',
      status: 400,
      detail: 'the line item is not valid',
      requestId: '0193a1b2-7c4d-7e2a-9f10-5b6c7d8e9f00',
      errors: [
        {
          field: 'lineTotal',
          message: 'must be a decimal amount with two places',
        },
      ],
    };
    const { client } = clientAnswering(() =>
      json(400, problem, 'application/problem+json'),
    );

    const failure = await unwrap(
      client.POST('/receipts/{receiptId}/line-items', {
        params: { path: { receiptId } },
        body: { name: 'Ayran 1 l', lineTotal: 'forty' },
      }),
    ).catch((error: unknown) => error);

    expect(failure).toBeInstanceOf(ApiError);
    const apiError = failure as ApiError;
    expect(apiError.status).toBe(400);
    expect(apiError.message).toBe('the line item is not valid');
    expect(apiError.problem).toEqual(problem);
    expect(apiError.fieldMessage('lineTotal')).toBe(
      'must be a decimal amount with two places',
    );
    expect(apiError.fieldMessage('name')).toBeUndefined();
  });

  it('falls back to the title when a problem has no detail', async () => {
    const { client } = clientAnswering(() =>
      json(
        501,
        { type: 'about:blank', title: 'Not Implemented', status: 501 },
        'application/problem+json',
      ),
    );

    const failure = await unwrap(client.GET('/me')).catch(
      (error: unknown) => error,
    );

    expect((failure as ApiError).message).toBe('Not Implemented');
    expect((failure as ApiError).fieldMessage('anything')).toBeUndefined();
  });

  it('builds a problem when the failure did not come from the application', async () => {
    const { client } = clientAnswering(
      () =>
        new Response('<html>Bad Gateway</html>', {
          status: 502,
          statusText: 'Bad Gateway',
          headers: { 'Content-Type': 'text/html' },
        }),
    );

    const failure = await unwrap(client.GET('/me')).catch(
      (error: unknown) => error,
    );

    expect(failure).toBeInstanceOf(ApiError);
    expect((failure as ApiError).problem).toEqual({
      type: 'about:blank',
      title: 'Bad Gateway',
      status: 502,
    });
  });

  it('does not mistake other JSON errors for a problem document', async () => {
    const { client } = clientAnswering(() =>
      json(500, { message: 'upstream exploded' }),
    );

    const failure = await unwrap(client.GET('/me')).catch(
      (error: unknown) => error,
    );

    expect((failure as ApiError).problem).toEqual({
      type: 'about:blank',
      title: 'Request failed',
      status: 500,
    });
  });

  it('lets a transport failure through unchanged', async () => {
    const offline = new TypeError('network unreachable');
    const client = createApiClient({
      baseUrl: 'http://localhost/api/v1',
      fetch: async () => {
        throw offline;
      },
    });

    await expect(unwrap(client.GET('/me'))).rejects.toBe(offline);
  });
});
