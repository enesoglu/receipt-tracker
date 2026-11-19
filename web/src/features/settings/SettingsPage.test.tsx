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
import type { components } from '../../api/schema.gen';
import { mockUser, resetMockState, setMockAuthMode } from '../../mocks/data';
import { server } from '../../mocks/server';
import { SettingsPage } from './SettingsPage';

function makeTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  });
}

function renderSettingsPage() {
  const client = makeTestQueryClient();
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/settings']}>
        <SettingsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('SettingsPage', () => {
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

  it('renders loading and error retry recovery for settings, usage, and storage', async () => {
    server.use(
      http.get('/api/v1/settings', () => {
        return HttpResponse.json(
          {
            type: 'about:blank',
            title: 'Internal Server Error',
            status: 500,
            detail: 'settings unavailable',
            requestId: '0193a1b2-0000-7000-8000-000000000091',
          },
          { status: 500 },
        );
      }),
      http.get('/api/v1/usage', () => {
        return HttpResponse.json(
          {
            type: 'about:blank',
            title: 'Internal Server Error',
            status: 500,
            detail: 'usage unavailable',
            requestId: '0193a1b2-0000-7000-8000-000000000092',
          },
          { status: 500 },
        );
      }),
      http.get('/api/v1/storage', () => {
        return HttpResponse.json(
          {
            type: 'about:blank',
            title: 'Internal Server Error',
            status: 500,
            detail: 'storage unavailable',
            requestId: '0193a1b2-0000-7000-8000-000000000093',
          },
          { status: 500 },
        );
      }),
    );

    renderSettingsPage();
    expect(screen.getByText('Loading settings…')).toBeInTheDocument();
    expect(screen.getByText('Loading line suggestions…')).toBeInTheDocument();
    expect(screen.getByText('Loading usage…')).toBeInTheDocument();
    expect(screen.getByText('Loading storage status…')).toBeInTheDocument();

    expect(
      await screen.findByRole('heading', { name: 'Could not load settings' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('heading', {
        name: 'Could not load line suggestions',
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('heading', { name: 'Could not load usage' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('heading', { name: 'Could not load storage status' }),
    ).toBeInTheDocument();

    server.resetHandlers();

    for (const button of screen.getAllByRole('button', { name: 'Try again' })) {
      fireEvent.click(button);
    }

    expect(await screen.findByLabelText('Default currency')).toHaveValue('TRY');
    expect(screen.getByText('137 / 500 pages used')).toBeInTheDocument();
    expect(screen.getAllByText('Connected').length).toBeGreaterThanOrEqual(1);
    expect(await screen.findByText('Online')).toBeInTheDocument();
  });

  it('shows the Online badge and turns suggestions off without another status call', async () => {
    let statusCalls = 0;
    const putBodies: components['schemas']['Settings'][] = [];

    server.use(
      http.get('/api/v1/suggestions/status', () => {
        statusCalls += 1;
        return HttpResponse.json({ available: true, offline: false });
      }),
      http.put('/api/v1/settings', async ({ request }) => {
        const body =
          (await request.json()) as components['schemas']['Settings'];
        putBodies.push(body);
        return HttpResponse.json(body);
      }),
    );

    renderSettingsPage();

    const toggle = await screen.findByLabelText(
      'Suggest line names and categories',
    );
    expect(toggle).toBeChecked();
    expect(await screen.findByText('Online')).toBeInTheDocument();

    await waitFor(() => expect(statusCalls).toBe(1));

    fireEvent.click(toggle);

    expect(
      await screen.findByText('Line suggestions turned off.'),
    ).toBeInTheDocument();
    expect(await screen.findByText('Off')).toBeInTheDocument();
    expect(
      screen.getByText('The model is not asked about your receipts.'),
    ).toBeInTheDocument();

    expect(putBodies).toEqual([
      {
        defaultCurrency: 'TRY',
        defaultReceiptTitle: 'Home shopping',
        llmSuggestionsEnabled: false,
      },
    ]);

    // The status endpoint is never called again while the switch is off.
    await waitFor(() => expect(statusCalls).toBe(1));
  });

  it('maps an unconfigured endpoint and an unreachable model to honest badges and notes', async () => {
    server.use(
      http.get('/api/v1/suggestions/status', () =>
        HttpResponse.json({ available: false, offline: false }),
      ),
    );

    const { unmount } = renderSettingsPage();

    expect(await screen.findByText('Off')).toBeInTheDocument();
    expect(
      screen.getByText(/this instance has no suggestion endpoint configured/i),
    ).toBeInTheDocument();
    expect(
      screen.getByLabelText('Suggest line names and categories'),
    ).toBeChecked();

    unmount();
    server.resetHandlers();
    server.use(
      http.get('/api/v1/suggestions/status', () =>
        HttpResponse.json({ available: true, offline: true }),
      ),
    );

    renderSettingsPage();

    expect(await screen.findByText('LLM offline')).toBeInTheDocument();
    expect(
      screen.getByText(
        /The most recent suggestion job could not reach the model\./i,
      ),
    ).toBeInTheDocument();
  });

  it('never calls the status endpoint while suggestions are off and surfaces toggle failures', async () => {
    let statusCalls = 0;

    server.use(
      http.get('/api/v1/settings', () =>
        HttpResponse.json({
          defaultCurrency: 'TRY',
          defaultReceiptTitle: 'Home shopping',
          llmSuggestionsEnabled: false,
        }),
      ),
      http.get('/api/v1/suggestions/status', () => {
        statusCalls += 1;
        return HttpResponse.json({ available: true, offline: false });
      }),
      http.put('/api/v1/settings', () =>
        HttpResponse.json(
          {
            type: 'about:blank',
            title: 'Internal Server Error',
            status: 500,
            detail: 'settings unavailable',
            requestId: '0193a1b2-0000-7000-8000-000000000094',
          },
          { status: 500 },
        ),
      ),
    );

    renderSettingsPage();

    const toggle = await screen.findByLabelText(
      'Suggest line names and categories',
    );
    expect(toggle).not.toBeChecked();
    expect(await screen.findByText('Off')).toBeInTheDocument();
    expect(
      screen.getByText('The model is not asked about your receipts.'),
    ).toBeInTheDocument();

    fireEvent.click(toggle);

    expect(
      await screen.findByRole('heading', { name: 'Could not save settings' }),
    ).toBeInTheDocument();
    expect(
      screen.getByLabelText('Suggest line names and categories'),
    ).not.toBeChecked();

    await waitFor(() => expect(statusCalls).toBe(0));
  });

  it('validates and saves default currency and default receipt title', async () => {
    renderSettingsPage();

    const currencyInput = await screen.findByLabelText('Default currency');
    const titleInput = screen.getByLabelText('Default receipt title');
    expect(currencyInput).toHaveValue('TRY');
    expect(titleInput).toHaveValue('Home shopping');

    fireEvent.change(currencyInput, { target: { value: '12' } });
    fireEvent.change(titleInput, { target: { value: '   ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save defaults' }));

    expect(
      await screen.findByText(
        'Enter a 3-letter ISO currency code (e.g. TRY, USD).',
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText('Default receipt title must be 1 to 200 characters.'),
    ).toBeInTheDocument();

    fireEvent.change(currencyInput, { target: { value: 'eur' } });
    fireEvent.change(titleInput, { target: { value: 'Weekly groceries' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save defaults' }));

    expect(
      await screen.findByText('Receipt defaults saved.'),
    ).toBeInTheDocument();
    expect(screen.getByLabelText('Default currency')).toHaveValue('EUR');
    expect(screen.getByLabelText('Default receipt title')).toHaveValue(
      'Weekly groceries',
    );
  });

  it('shows monthly extraction quota and exhausted-quota alert when usage reaches limit', async () => {
    server.use(
      http.get('/api/v1/usage', () => {
        const exhausted: components['schemas']['Usage'] = {
          provider: 'azure',
          period: '2026-11',
          pagesUsed: 500,
          pagesLimit: 500,
        };
        return HttpResponse.json(exhausted);
      }),
    );

    renderSettingsPage();

    expect(await screen.findByText('500 / 500 pages used')).toBeInTheDocument();
    expect(screen.getByText('0 pages remaining')).toBeInTheDocument();
    expect(
      screen.getByText(/Monthly page quota is exhausted\./i),
    ).toBeInTheDocument();

    const progress = screen.getByRole('progressbar', {
      name: 'Monthly extraction usage',
    });
    expect(progress).toHaveAttribute('aria-valuenow', '500');
    expect(progress).toHaveAttribute('aria-valuemax', '500');
  });

  it('supports connected Drive status and disconnecting through confirmation dialog', async () => {
    renderSettingsPage();

    expect(
      await screen.findByRole('button', { name: 'Disconnect' }),
    ).toBeInTheDocument();
    const connectLink = screen.getByRole('link', {
      name: 'Connect Google Drive',
    });
    expect(connectLink).toHaveAttribute(
      'href',
      '/api/v1/storage/drive/connect',
    );

    fireEvent.click(screen.getByRole('button', { name: 'Disconnect' }));

    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveTextContent('Disconnect Google Drive?');
    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Confirm disconnect' }),
    );

    expect(
      await screen.findByText('Google Drive disconnected.'),
    ).toBeInTheDocument();
    await waitFor(() => {
      expect(
        screen.queryByRole('button', { name: 'Disconnect' }),
      ).not.toBeInTheDocument();
    });
    expect(
      screen.getByText('Google Drive is not connected'),
    ).toBeInTheDocument();
  });

  it('shows not_connected state with Connect Google Drive link for admins', async () => {
    server.use(
      http.get('/api/v1/storage', () => {
        const status: components['schemas']['StorageStatus'] = {
          backend: 'drive',
          drive: 'not_connected',
          lastError: null,
          canManage: true,
          backup: {
            destination: 'drive',
            lastSuccessAt: null,
            lastError: null,
          },
        };
        return HttpResponse.json(status);
      }),
    );

    renderSettingsPage();

    expect(
      await screen.findByText('Google Drive is not connected'),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('link', { name: 'Connect Google Drive' }),
    ).toHaveAttribute('href', '/api/v1/storage/drive/connect');
    expect(
      screen.queryByRole('button', { name: 'Disconnect' }),
    ).not.toBeInTheDocument();
  });

  it('renders clear reconnect_needed alert with reason and Reconnect Google Drive action for admins', async () => {
    server.use(
      http.get('/api/v1/storage', () => {
        const status: components['schemas']['StorageStatus'] = {
          backend: 'drive',
          drive: 'reconnect_needed',
          lastError: 'storage_reconnect_needed',
          canManage: true,
          backup: {
            destination: 'drive',
            lastSuccessAt: '2026-11-11T03:15:39Z',
            lastError: 'storage_reconnect_needed',
          },
        };
        return HttpResponse.json(status);
      }),
    );

    renderSettingsPage();

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Google Drive reconnect needed');
    expect(alert).toHaveTextContent('storage reconnect needed');
    expect(
      screen.getByRole('link', { name: 'Reconnect Google Drive' }),
    ).toHaveAttribute('href', '/api/v1/storage/drive/connect');
    expect(
      screen.getByRole('button', { name: 'Disconnect' }),
    ).toBeInTheDocument();
    expect(
      screen.getByText('Last backup error: storage reconnect needed'),
    ).toBeInTheDocument();
  });

  it('hides Drive connect/disconnect controls for non-admin users while keeping status visible', async () => {
    server.use(
      http.get('/api/v1/me', () => {
        return HttpResponse.json({
          ...mockUser,
          name: 'Sam Member',
          email: 'sam@example.com',
          isAdmin: false,
        });
      }),
      http.get('/api/v1/storage', () => {
        const status: components['schemas']['StorageStatus'] = {
          backend: 'drive',
          drive: 'reconnect_needed',
          lastError: 'storage_reconnect_needed',
          canManage: false,
          backup: {
            destination: 'drive',
            lastSuccessAt: '2026-11-11T03:15:39Z',
            lastError: null,
          },
        };
        return HttpResponse.json(status);
      }),
    );

    renderSettingsPage();

    expect(
      await screen.findByText('Google Drive reconnect needed'),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Managed by an administrator\./i),
    ).toBeInTheDocument();
    expect(screen.getByText('Sam Member')).toBeInTheDocument();
    expect(screen.getByText('Member')).toBeInTheDocument();
    expect(
      screen.queryByRole('link', { name: /Connect Google Drive/i }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Disconnect' }),
    ).not.toBeInTheDocument();
  });

  it('signs out the current user when clicking Sign out', async () => {
    let signedOut = false;
    server.use(
      http.post('/api/v1/auth/logout', () => {
        signedOut = true;
        setMockAuthMode('signed_out');
        return new HttpResponse(null, { status: 204 });
      }),
    );

    renderSettingsPage();

    const signOutButton = await screen.findByRole('button', {
      name: 'Sign out',
    });
    expect(screen.getByText('Ada Example')).toBeInTheDocument();
    fireEvent.click(signOutButton);

    await waitFor(() => {
      expect(signedOut).toBe(true);
    });
  });
});
