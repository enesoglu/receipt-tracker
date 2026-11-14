import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { Badge } from './Badge';

describe('Badge', () => {
  it('renders all semantic variants', () => {
    render(
      <div>
        <Badge>Queued</Badge>
        <Badge variant="accent">Processing</Badge>
        <Badge variant="success">Ready</Badge>
        <Badge variant="warning">Needs review</Badge>
        <Badge variant="danger">Failed</Badge>
      </div>,
    );

    expect(screen.getByText('Queued')).toBeInTheDocument();
    expect(screen.getByText('Processing')).toBeInTheDocument();
    expect(screen.getByText('Ready')).toBeInTheDocument();
    expect(screen.getByText('Needs review')).toBeInTheDocument();
    expect(screen.getByText('Failed')).toBeInTheDocument();
  });
});
