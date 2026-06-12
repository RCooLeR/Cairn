import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import App from './App';
import { useAppStore } from './state/appStore';

vi.mock('./api/app', () => ({
  getAppVersion: vi.fn().mockResolvedValue({
    version: '0.1.0',
    goVersion: 'go1.26.4',
  }),
}));

describe('App shell', () => {
  beforeEach(() => {
    useAppStore.setState({
      version: null,
      versionLoading: false,
      versionError: null,
    });
  });

  it('renders Cairn navigation, backend version, and degraded Docker state', async () => {
    render(<App />);

    expect(screen.getByRole('img', { name: 'Cairn' })).toBeInTheDocument();
    expect(screen.getByRole('navigation', { name: 'Main navigation' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Overview' })).toBeInTheDocument();
    expect(await screen.findByText('v0.1.0')).toBeInTheDocument();
    expect(screen.getByText('Docker is not reachable')).toBeInTheDocument();
  });
});
