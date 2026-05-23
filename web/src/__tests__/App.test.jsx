import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import App from '../App';
import React from 'react';

describe('App', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn((url) => {
      if (url.endsWith('/health')) {
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve({ status: 'ok' }),
        });
      }
      if (url.endsWith('/api/specs')) {
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve([]),
        });
      }
      if (url.endsWith('/api/mocks/running')) {
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve([]),
        });
      }
      return Promise.reject(new Error(`Unhandled fetch: ${url}`));
    }));
  });

  it('renders Specguard sidebar and title', async () => {
    await act(async () => {
      render(<App />);
    });
    expect(screen.getByText('SPECGUARD')).toBeInTheDocument();
    expect(screen.getByText('Specifications')).toBeInTheDocument();
    expect(screen.getByText('Mocks Manager')).toBeInTheDocument();
  });
});
