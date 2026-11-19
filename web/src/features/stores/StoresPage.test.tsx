import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';
import {
  mockStorePage,
  resetMockState,
  setMockAuthMode,
  updateMockStore,
} from '../../mocks/data';
import { server } from '../../mocks/server';
import { StoresPage } from './StoresPage';

function makeTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  });
}

function renderStoresPage() {
  const client = makeTestQueryClient();
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/stores']}>
        <StoresPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('StoresPage', () => {
  beforeAll(() => {
    server.listen({ onUnhandledRequest: 'error' });
  });

  afterEach(() => {
    server.resetHandlers();
    resetMockState();
    setMockAuthMode('signed_in');
  });

  afterAll(() => {
    server.close();
  });

  it('renders loading, error retry, and empty store states', async () => {
    server.use(
      http.get('/api/v1/stores', () => {
        return HttpResponse.json(
          {
            type: 'about:blank',
            title: 'Internal Server Error',
            status: 500,
            detail: 'store catalog unavailable',
            requestId: '0193a1b2-0000-7000-8000-000000000099',
          },
          { status: 500 },
        );
      }),
    );

    renderStoresPage();
    expect(screen.getByText('Loading stores…')).toBeInTheDocument();

    expect(
      await screen.findByRole('heading', { name: 'Could not load stores' }),
    ).toBeInTheDocument();
    expect(screen.getByText('store catalog unavailable')).toBeInTheDocument();

    server.use(
      http.get('/api/v1/stores', () => {
        return HttpResponse.json({ items: [], nextCursor: null });
      }),
    );

    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByText('No stores yet')).toBeInTheDocument();
  });

  it('lists stores with receipt counts and tax numbers', async () => {
    renderStoresPage();

    expect(
      await screen.findByRole('cell', { name: 'Example Market' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('cell', { name: '14 receipts' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('cell', { name: '1234567890' }),
    ).toBeInTheDocument();

    expect(
      screen.getByRole('cell', { name: 'Corner Shop' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('cell', { name: '3 receipts' }),
    ).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'Unknown' })).toBeInTheDocument();

    // Stores that still own receipts cannot be deleted.
    expect(
      screen.getByRole('button', { name: 'Delete Example Market' }),
    ).toBeDisabled();
    expect(
      screen.getByRole('button', { name: 'Delete Corner Shop' }),
    ).toBeDisabled();
  });

  it('creates stores, validates blank names, and surfaces 409 duplicate conflicts', async () => {
    renderStoresPage();

    expect(
      await screen.findByRole('cell', { name: 'Corner Shop' }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Create store' }));
    expect(
      await screen.findByText('Store name is required.'),
    ).toBeInTheDocument();

    const nameInput = screen.getByLabelText('Store name');
    fireEvent.change(nameInput, { target: { value: 'corner shop' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create store' }));

    expect(
      await screen.findByRole('heading', { name: 'Could not create store' }),
    ).toBeInTheDocument();
    expect(
      screen.getByText('a store with this name already exists'),
    ).toBeInTheDocument();

    fireEvent.change(nameInput, { target: { value: 'Migros Jet' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create store' }));

    expect(
      await screen.findByText('Store "Migros Jet" created.'),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('cell', { name: 'Migros Jet' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('cell', { name: '0 receipts' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Delete Migros Jet' }),
    ).toBeEnabled();
  });

  it('renames stores, asserts the request body, and surfaces 409 conflicts', async () => {
    const savedBodies: Array<{ name: string }> = [];
    server.use(
      http.put('/api/v1/stores/:storeId', async ({ request, params }) => {
        const body = (await request.json()) as { name: string };
        savedBodies.push(body);
        const result = updateMockStore(String(params.storeId), body);
        if (!result.ok) {
          return HttpResponse.json(
            {
              type: 'about:blank',
              title: 'Conflict',
              status: 409,
              detail: 'a store with this name already exists',
              requestId: '0193a1b2-0000-7000-8000-000000000099',
            },
            { status: 409 },
          );
        }
        return HttpResponse.json(result.store);
      }),
    );

    renderStoresPage();

    expect(
      await screen.findByRole('cell', { name: 'Corner Shop' }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Rename Corner Shop' }));

    const dialog = await screen.findByRole('dialog');
    const renameInput = within(dialog).getByLabelText('New name');
    fireEvent.change(renameInput, { target: { value: 'example market' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save name' }));

    expect(
      await within(dialog).findByRole('heading', {
        name: 'Could not rename store',
      }),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText('a store with this name already exists'),
    ).toBeInTheDocument();
    expect(savedBodies).toEqual([{ name: 'example market' }]);

    fireEvent.change(renameInput, { target: { value: '  Corner Market  ' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save name' }));

    expect(
      await screen.findByText('Store renamed to "Corner Market".'),
    ).toBeInTheDocument();
    // The correction is trimmed before it is sent.
    expect(savedBodies).toEqual([
      { name: 'example market' },
      { name: 'Corner Market' },
    ]);
    expect(
      screen.getByRole('cell', { name: 'Corner Market' }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('cell', { name: 'Corner Shop' }),
    ).not.toBeInTheDocument();
  });

  it('merges another store in through a confirm dialog', async () => {
    renderStoresPage();

    expect(
      await screen.findByRole('cell', { name: 'Corner Shop' }),
    ).toBeInTheDocument();

    fireEvent.click(
      screen.getByRole('button', { name: 'Merge into Example Market' }),
    );

    const dialog = await screen.findByRole('dialog');
    expect(
      within(dialog).getByText(
        /Select the duplicate store to merge into Example Market/,
      ),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText(/This cannot be undone/),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByRole('button', { name: 'Confirm merge' }),
    ).toBeDisabled();

    const sourceSelect = within(dialog).getByLabelText('Store to merge');
    fireEvent.change(sourceSelect, {
      // The list is ordered by name, so Corner Shop is the first item.
      target: { value: mockStorePage.items[0].id },
    });

    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Confirm merge' }),
    );

    expect(
      await screen.findByText('Merged Corner Shop into Example Market.'),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('cell', { name: 'Corner Shop' }),
    ).not.toBeInTheDocument();
    // The target now owns the source's receipts, so it stays undeletable.
    expect(
      screen.getByRole('cell', { name: '17 receipts' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Delete Example Market' }),
    ).toBeDisabled();
  });

  it('deletes only unused stores through the confirm dialog', async () => {
    renderStoresPage();

    expect(
      await screen.findByRole('cell', { name: 'Example Market' }),
    ).toBeInTheDocument();

    // A store with receipts keeps its delete action disabled.
    expect(
      screen.getByRole('button', { name: 'Delete Example Market' }),
    ).toBeDisabled();

    // A fresh store starts without receipts and can be removed.
    fireEvent.change(screen.getByLabelText('Store name'), {
      target: { value: 'Migros Jet' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create store' }));
    expect(
      await screen.findByRole('cell', { name: 'Migros Jet' }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Delete Migros Jet' }));

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText(/has no receipts/)).toBeInTheDocument();
    expect(
      within(dialog).getByText(/This cannot be undone/),
    ).toBeInTheDocument();

    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Delete store' }),
    );

    expect(
      await screen.findByText('Store "Migros Jet" deleted.'),
    ).toBeInTheDocument();
    await waitFor(() => {
      expect(
        screen.queryByRole('cell', { name: 'Migros Jet' }),
      ).not.toBeInTheDocument();
    });
  });

  it('expands a row into the raw names and branches seen', async () => {
    renderStoresPage();

    expect(
      await screen.findByRole('cell', { name: 'Example Market' }),
    ).toBeInTheDocument();

    fireEvent.click(
      screen.getByRole('button', { name: 'Details for Example Market' }),
    );

    const namesSection = await screen.findByRole('region', {
      name: 'Names seen',
    });
    expect(
      within(namesSection).getByText('Example Market'),
    ).toBeInTheDocument();
    expect(within(namesSection).getByText('Exmple Mrket')).toBeInTheDocument();
    expect(within(namesSection).getByText(/^2 times/)).toBeInTheDocument();

    const branchesSection = screen.getByRole('region', {
      name: 'Branches seen',
    });
    expect(within(branchesSection).getByText('Çankaya')).toBeInTheDocument();
    expect(
      within(branchesSection).getByText('13 receipts'),
    ).toBeInTheDocument();
    expect(within(branchesSection).getByText('Kızılay')).toBeInTheDocument();
    expect(within(branchesSection).getByText('1 receipt')).toBeInTheDocument();

    // A store without history shows both empty states.
    fireEvent.click(
      screen.getByRole('button', { name: 'Details for Corner Shop' }),
    );

    const cornerNames = await screen.findByRole('region', {
      name: 'Names seen',
    });
    expect(
      within(cornerNames).getByText('No raw names recorded yet.'),
    ).toBeInTheDocument();
    const cornerBranches = screen.getByRole('region', {
      name: 'Branches seen',
    });
    expect(
      within(cornerBranches).getByText('No branches recorded yet.'),
    ).toBeInTheDocument();

    // The same control collapses the row again.
    fireEvent.click(
      screen.getByRole('button', { name: 'Details for Corner Shop' }),
    );
    await waitFor(() => {
      expect(
        screen.queryByRole('region', { name: 'Names seen' }),
      ).not.toBeInTheDocument();
    });
  });
});
