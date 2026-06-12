import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import App from './App';

describe('App shell', () => {
  it('renders Cairn navigation and degraded Docker state', () => {
    render(<App />);

    expect(screen.getByRole('img', { name: 'Cairn' })).toBeInTheDocument();
    expect(screen.getByRole('navigation', { name: 'Main navigation' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Overview' })).toBeInTheDocument();
    expect(screen.getByText('Docker is not reachable')).toBeInTheDocument();
  });
});
