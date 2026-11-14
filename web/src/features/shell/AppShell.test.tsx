import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from '../../api/client';
import { createAppQueryClient } from '../../api/query-client';
import { QueryProvider } from '../../api/QueryProvider';
import { AppShell } from './AppShell';

const sampleUser = {
  id: 'u1',
  email: 'ada@example.com',
  name: 'Ada Example',
  isAdmin: true,
};

function renderShell(initialPath = '/receipts', totalOpen = 2) {
  const client = createAppQueryClient({ staleTime: 0 });
  vi.spyOn(api, 'GET').mockResolvedValue({
    data: { items: [], nextCursor: null, totalOpen },
    response: new Response(null, { status: 200 }),
  } as never);
  vi.spyOn(api, 'POST').mockResolvedValue({
    data: undefined,
    response: new Response(null, { status: 204 }),
  } as never);

  return render(
    <QueryProvider client={client}>
      <MemoryRouter initialEntries={[initialPath]}>
        <Routes>
          <Route element={<AppShell user={sampleUser} />}>
            <Route path="/receipts" element={<div>Receipts view</div>} />
            <Route path="/review" element={<div>Review view</div>} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryProvider>,
  );
}

describe('AppShell', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('renders navigation links, active indicator, and open review badge count', async () => {
    renderShell('/receipts', 4);

    const activeLink = screen.getByRole('link', { name: 'Receipts' });
    expect(activeLink).toHaveAttribute('aria-current', 'page');

    expect(screen.getByRole('link', { name: 'Upload' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Products' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Spending' })).toBeInTheDocument();
    expect(
      screen.getByRole('link', { name: 'Categories' }),
    ).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Settings' })).toBeInTheDocument();

    await waitFor(() =>
      expect(screen.getByLabelText('Open review items')).toHaveTextContent('4'),
    );
  });

  it('toggles the mobile menu and signs out', async () => {
    renderShell('/receipts', 0);

    const menuButton = screen.getByRole('button', { name: 'Menu' });
    expect(menuButton).toHaveAttribute('aria-expanded', 'false');

    fireEvent.click(menuButton);
    expect(screen.getByRole('button', { name: 'Close' })).toHaveAttribute(
      'aria-expanded',
      'true',
    );
    expect(
      screen.getByRole('navigation', { name: 'Mobile' }),
    ).toBeInTheDocument();

    const signOutButtons = screen.getAllByRole('button', { name: 'Sign out' });
    fireEvent.click(signOutButtons[0]);

    await waitFor(() => expect(api.POST).toHaveBeenCalledWith('/auth/logout'));
  });
});
