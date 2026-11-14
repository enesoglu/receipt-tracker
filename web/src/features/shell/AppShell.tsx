import { useState, type ReactNode } from 'react';
import { NavLink, Outlet } from 'react-router';
import { useLogout, type User } from '../../api/auth';
import { useReviewFlags } from '../../api/review';
import { Badge } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { primaryNavItems } from './navigation';

export interface AppShellProps {
  user: User;
  children?: ReactNode;
}

/** Responsive authenticated frame with sidebar navigation, mobile menu, and review badge. */
export function AppShell({ user, children }: AppShellProps) {
  const [menuOpen, setMenuOpen] = useState(false);
  const reviewQuery = useReviewFlags({ limit: 1 });
  const logout = useLogout();

  const openReviewCount = reviewQuery.data?.totalOpen ?? 0;

  function renderNavLinks(onNavigate?: () => void) {
    return (
      <ul className="flex flex-col gap-1">
        {primaryNavItems.map((item) => (
          <li key={item.to}>
            <NavLink
              to={item.to}
              onClick={onNavigate}
              className={({ isActive }) =>
                [
                  'flex items-center justify-between rounded-control px-3 py-2 text-small font-medium focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent',
                  isActive
                    ? 'bg-accent-soft text-accent font-semibold'
                    : 'text-ink hover:bg-surface-subtle',
                ].join(' ')
              }
            >
              <span>{item.label}</span>
              {item.showReviewBadge && openReviewCount > 0 ? (
                <span aria-label="Open review items">
                  <Badge variant="warning">{openReviewCount}</Badge>
                </span>
              ) : null}
            </NavLink>
          </li>
        ))}
      </ul>
    );
  }

  function renderAccountBlock() {
    return (
      <div className="flex flex-col gap-3 border-t border-border pt-4">
        <div className="flex flex-col gap-0.5 px-1">
          <p className="truncate text-small font-medium text-ink">
            {user.name}
          </p>
          <p className="truncate text-caption text-muted">{user.email}</p>
        </div>
        <Button
          variant="secondary"
          size="sm"
          loading={logout.isPending}
          onClick={() => {
            logout.mutate();
          }}
        >
          Sign out
        </Button>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-canvas font-sans text-body text-ink lg:flex">
      <aside className="hidden lg:flex lg:w-64 lg:shrink-0 lg:flex-col lg:justify-between lg:border-r lg:border-border lg:bg-surface lg:p-5">
        <div className="flex flex-col gap-6">
          <div className="px-3">
            <p className="text-caption font-semibold tracking-wider text-accent uppercase">
              Receipt Tracker
            </p>
          </div>
          <nav aria-label="Primary">{renderNavLinks()}</nav>
        </div>
        {renderAccountBlock()}
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="border-b border-border bg-surface px-4 py-3 lg:hidden">
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-2">
              <span className="text-small font-semibold tracking-wider text-accent uppercase">
                Receipt Tracker
              </span>
              {openReviewCount > 0 ? (
                <Badge variant="warning">{openReviewCount}</Badge>
              ) : null}
            </div>
            <Button
              variant="secondary"
              size="sm"
              aria-expanded={menuOpen}
              aria-controls="mobile-navigation"
              onClick={() => setMenuOpen((open) => !open)}
            >
              {menuOpen ? 'Close' : 'Menu'}
            </Button>
          </div>

          {menuOpen ? (
            <div
              id="mobile-navigation"
              className="mt-4 flex flex-col gap-4 border-t border-border pt-4"
            >
              <nav aria-label="Mobile">
                {renderNavLinks(() => setMenuOpen(false))}
              </nav>
              {renderAccountBlock()}
            </div>
          ) : null}
        </header>

        <main className="mx-auto w-full max-w-shell flex-1 px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
          {children ?? <Outlet />}
        </main>
      </div>
    </div>
  );
}
