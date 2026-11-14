import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api } from '../api/client';
import { createAppQueryClient } from '../api/query-client';
import { AppRouterContent } from './App';

function makeProblem(status: number, title: string, detail?: string) {
  return new ApiError({
    type: `https://example.com/problems/${status}`,
    title,
    status,
    detail,
  });
}

function mockAuthenticatedApi() {
  return vi.spyOn(api, 'GET').mockImplementation((path) => {
    if (path === '/me') {
      return Promise.resolve({
        data: {
          id: '0193a1b2-0000-7000-8000-000000000001',
          email: 'ada@example.com',
          name: 'Ada Example',
          isAdmin: true,
        },
        response: new Response(null, { status: 200 }),
      }) as never;
    }
    if (path === '/review-flags') {
      return Promise.resolve({
        data: { items: [], nextCursor: null, totalOpen: 2 },
        response: new Response(null, { status: 200 }),
      }) as never;
    }
    if (path === '/usage') {
      return Promise.resolve({
        data: {
          provider: 'azure',
          period: '2026-11',
          pagesUsed: 12,
          pagesLimit: 500,
        },
        response: new Response(null, { status: 200 }),
      }) as never;
    }
    return Promise.reject(makeProblem(404, 'Not found')) as never;
  });
}

describe('App routing and authentication gate', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('redirects signed-out users to SignInPage on 401', async () => {
    vi.spyOn(api, 'GET').mockRejectedValue(
      makeProblem(401, 'Unauthorized', 'Sign in required'),
    );

    const client = createAppQueryClient({ staleTime: 0 });
    render(
      <MemoryRouter initialEntries={['/receipts']}>
        <AppRouterContent queryClient={client} />
      </MemoryRouter>,
    );

    await waitFor(() =>
      expect(
        screen.getByRole('heading', { name: 'Sign in to your account' }),
      ).toBeInTheDocument(),
    );
    expect(
      screen.getByRole('link', { name: 'Sign in with Google' }),
    ).toHaveAttribute('href', '/auth/google/login');
  });

  it('shows access is closed when /me returns 403', async () => {
    vi.spyOn(api, 'GET').mockRejectedValue(
      makeProblem(403, 'Forbidden', 'Access is closed'),
    );

    const client = createAppQueryClient({ staleTime: 0 });
    render(
      <MemoryRouter initialEntries={['/receipts']}>
        <AppRouterContent queryClient={client} />
      </MemoryRouter>,
    );

    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent('Access is closed'),
    );
  });

  it.each([
    ['/receipts', 'Receipts'],
    ['/upload', 'Upload receipts'],
    ['/review', 'Review queue'],
    ['/products', 'Products'],
    ['/spending', 'Spending'],
    ['/categories', 'Categories'],
    ['/settings', 'Settings'],
  ])('renders %s for signed-in users', async (route, expectedHeading) => {
    mockAuthenticatedApi();
    const client = createAppQueryClient({ staleTime: 0 });

    render(
      <MemoryRouter initialEntries={[route]}>
        <AppRouterContent queryClient={client} />
      </MemoryRouter>,
    );

    await waitFor(() =>
      expect(
        screen.getByRole('heading', { level: 1, name: expectedHeading }),
      ).toBeInTheDocument(),
    );
  });

  it('renders /dev/ui in development without requiring authentication', () => {
    const getSpy = vi.spyOn(api, 'GET');
    const client = createAppQueryClient({ staleTime: 0 });

    render(
      <MemoryRouter initialEntries={['/dev/ui']}>
        <AppRouterContent queryClient={client} />
      </MemoryRouter>,
    );

    expect(
      screen.getByRole('heading', { level: 1, name: 'UI Primitives' }),
    ).toBeInTheDocument();
    expect(getSpy).not.toHaveBeenCalled();
  });
});
