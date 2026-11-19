import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { EArchivePanel } from './EArchivePanel';
import { bimChain } from './e-archive-fixture';

afterEach(() => {
  vi.useRealTimers();
  Reflect.deleteProperty(navigator, 'clipboard');
});

describe('EArchivePanel', () => {
  it('renders nothing when the chain is unknown', () => {
    const { container } = render(<EArchivePanel chain={null} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('links to the portal and shows one row per field', () => {
    render(<EArchivePanel chain={bimChain} />);

    expect(
      screen.getByRole('heading', { name: 'BİM e-archive' }),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Open the portal, paste each value/),
    ).toBeInTheDocument();

    const link = screen.getByRole('link', { name: 'Open arsiv.bim.com.tr' });
    expect(link).toHaveAttribute('href', 'https://arsiv.bim.com.tr/');
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noopener noreferrer');

    for (const value of ['BIM2026001234', '05-03-2026', '129.40']) {
      expect(screen.getByText(value)).toBeInTheDocument();
    }
    expect(
      screen.getByRole('button', { name: 'Copy Invoice date' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Copy Total paid' }),
    ).toBeInTheDocument();
  });

  it('copies the exact wire value and briefly swaps the label', async () => {
    vi.useFakeTimers();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    });

    render(<EArchivePanel chain={bimChain} />);
    fireEvent.click(screen.getByRole('button', { name: 'Copy Invoice date' }));
    await act(async () => {
      await Promise.resolve();
    });

    expect(writeText).toHaveBeenCalledWith('05-03-2026');
    expect(screen.getByText('Copied')).toBeInTheDocument();

    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(
      screen.getByRole('button', { name: 'Copy Invoice date' }),
    ).toHaveTextContent('Copy');
  });

  it('shows Copy failed when the clipboard rejects but keeps the value visible', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('denied'));
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    });

    render(<EArchivePanel chain={bimChain} />);
    fireEvent.click(screen.getByRole('button', { name: 'Copy Total paid' }));

    expect(await screen.findByText('Copy failed')).toBeInTheDocument();
    expect(screen.getByText('129.40')).toBeInTheDocument();
  });

  it('shows Copy failed when the Clipboard API is unavailable', () => {
    Reflect.deleteProperty(navigator, 'clipboard');

    render(<EArchivePanel chain={bimChain} />);
    fireEvent.click(screen.getByRole('button', { name: 'Copy Invoice date' }));

    expect(screen.getByText('Copy failed')).toBeInTheDocument();
    expect(screen.getByText('05-03-2026')).toBeInTheDocument();
  });

  it('hides the panel and names each missing value', () => {
    render(
      <EArchivePanel
        chain={{
          ...bimChain,
          fields: [
            bimChain.fields[0],
            { ...bimChain.fields[1], value: null },
            { ...bimChain.fields[2], value: null },
          ],
        }}
      />,
    );

    expect(
      screen.getByText(
        'BİM e-archive — this receipt is missing: Invoice date, Total paid.',
      ),
    ).toBeInTheDocument();
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('shows a link-only panel when the portal fields are not recorded', () => {
    render(
      <EArchivePanel chain={{ ...bimChain, fields: [], captcha: 'image' }} />,
    );

    const link = screen.getByRole('link', { name: 'Open arsiv.bim.com.tr' });
    expect(link).toHaveAttribute('href', 'https://arsiv.bim.com.tr/');
    expect(
      screen.getByText('The fields this portal asks for are not recorded yet.'),
    ).toBeInTheDocument();
    expect(
      screen.getByText('The portal shows an image captcha; type it yourself.'),
    ).toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('describes a Turnstile challenge', () => {
    render(<EArchivePanel chain={{ ...bimChain, captcha: 'turnstile' }} />);
    expect(
      screen.getByText('The portal shows a Cloudflare Turnstile challenge.'),
    ).toBeInTheDocument();
  });
});
