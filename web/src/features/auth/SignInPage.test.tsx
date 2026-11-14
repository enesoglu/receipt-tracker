import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { describe, expect, it } from 'vitest';
import { SignInPage } from './SignInPage';

describe('SignInPage', () => {
  it('renders the Google sign-in link to /auth/google/login', () => {
    render(
      <MemoryRouter initialEntries={['/login']}>
        <SignInPage />
      </MemoryRouter>,
    );

    const link = screen.getByRole('link', { name: 'Sign in with Google' });
    expect(link).toHaveAttribute('href', '/auth/google/login');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('shows the access is closed state from query parameters or prop', () => {
    const { rerender } = render(
      <MemoryRouter initialEntries={['/login?error=access_closed']}>
        <SignInPage />
      </MemoryRouter>,
    );

    expect(screen.getByRole('alert')).toHaveTextContent('Access is closed');

    rerender(
      <MemoryRouter initialEntries={['/login']}>
        <SignInPage accessClosed />
      </MemoryRouter>,
    );

    expect(screen.getByRole('alert')).toHaveTextContent('Access is closed');
  });
});
