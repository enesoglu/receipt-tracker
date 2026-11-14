export interface NavItem {
  to: string;
  label: string;
  showReviewBadge?: boolean;
}

/** Primary navigation links displayed in desktop and mobile app shells. */
export const primaryNavItems: readonly NavItem[] = [
  { to: '/receipts', label: 'Receipts' },
  { to: '/upload', label: 'Upload' },
  { to: '/review', label: 'Review', showReviewBadge: true },
  { to: '/products', label: 'Products' },
  { to: '/spending', label: 'Spending' },
  { to: '/categories', label: 'Categories' },
  { to: '/settings', label: 'Settings' },
];
