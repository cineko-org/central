import { act, type ReactNode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { create, toJson } from '@bufbuild/protobuf';
import { GetSessionResponseSchema, PrincipalSchema, type Principal } from '@cineko/contracts/gen/ts/cineko/admin/admin_pb';

vi.mock('../src/central/ui/CentralShellView', () => ({
  CentralShellView: (props: {
    page: string;
    navigationOpen: boolean;
    onNavigate: (page: string) => void;
    onToggleNavigation: () => void;
    onLogout: () => void;
    children: ReactNode;
  }) => (
    <main data-page={props.page} data-navigation-open={String(props.navigationOpen)}>
      {['overview', 'observations', 'probes', 'data', 'releases', 'users', 'settings'].map((page) => (
        <button key={page} data-testid={`nav-${page}`} onClick={() => props.onNavigate(page)}>go</button>
      ))}
      <button data-testid="toggle-navigation" onClick={props.onToggleNavigation}>toggle</button>
      <button data-testid="logout" onClick={props.onLogout}>logout</button>
      {props.children}
    </main>
  ),
}));

vi.mock('../src/central/LoginView', () => ({
  LoginView: ({ onLogin }: { onLogin: (session: Principal) => void }) => (
    <button data-testid="login" onClick={() => onLogin(create(PrincipalSchema, { userId: 'admin', displayName: 'Admin' }))}>login</button>
  ),
}));

function mockPage(label: string, onUnauthorized: () => void) {
  return (
    <section data-testid={`page-${label}`}>
      {label}
      <button data-testid={`unauthorized-${label}`} onClick={onUnauthorized}>unauthorized</button>
    </section>
  );
}

vi.mock('../src/central/StatusView', () => ({ StatusView: ({ onUnauthorized }: { onUnauthorized: () => void }) => mockPage('overview', onUnauthorized) }));
vi.mock('../src/central/ObservationsView', () => ({ ObservationsView: ({ onUnauthorized }: { onUnauthorized: () => void }) => mockPage('observations', onUnauthorized) }));
vi.mock('../src/central/ProbesView', () => ({ ProbesView: ({ onUnauthorized }: { onUnauthorized: () => void }) => mockPage('probes', onUnauthorized) }));
vi.mock('../src/central/DataView', () => ({ DataView: ({ onUnauthorized }: { onUnauthorized: () => void }) => mockPage('data', onUnauthorized) }));
vi.mock('../src/central/ReleasesView', () => ({ ReleasesView: ({ onUnauthorized }: { onUnauthorized: () => void }) => mockPage('releases', onUnauthorized) }));
vi.mock('../src/central/UsersView', () => ({ UsersView: ({ onUnauthorized }: { onUnauthorized: () => void }) => mockPage('users', onUnauthorized) }));
vi.mock('../src/central/SettingsView', () => ({ SettingsView: ({ onUnauthorized }: { onUnauthorized: () => void }) => mockPage('settings', onUnauthorized) }));

import { CentralApplication } from '../src/central/CentralApplication';

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
}

async function settle(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe('Central application controller', () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement('div');
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.unstubAllGlobals();
  });

  it('loads an admin session, closes navigation during routing, and logs out', async () => {
    const session = create(PrincipalSchema, { userId: 'admin', displayName: 'Admin' });
    let resolveSession!: (response: Response) => void;
    const pendingSession = new Promise<Response>((resolve) => { resolveSession = resolve; });
    const fetchMock = vi.fn<typeof fetch>()
      .mockReturnValueOnce(pendingSession)
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);

    await act(async () => root.render(<CentralApplication />));
    expect(container.textContent).toBe('');
    resolveSession(json(toJson(GetSessionResponseSchema, create(GetSessionResponseSchema, { principal: session }))));
    await settle();
    expect(container.querySelector('[data-testid="page-overview"]')).not.toBeNull();

    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="toggle-navigation"]')?.click());
    expect(container.querySelector('main')?.dataset.navigationOpen).toBe('true');

    for (const page of ['observations', 'probes', 'data', 'releases', 'users', 'settings', 'overview']) {
      // Navigation is intentionally sequential because each assertion observes the resulting route.
      // oxlint-disable-next-line no-await-in-loop
      await act(async () => container.querySelector<HTMLButtonElement>(`[data-testid="nav-${page}"]`)?.click());
      expect(container.querySelector('main')?.dataset.page).toBe(page);
      expect(container.querySelector(`[data-testid="page-${page}"]`)).not.toBeNull();
      expect(container.querySelector('main')?.dataset.navigationOpen).toBe('false');
    }

    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="logout"]')?.click());
    await settle();
    expect(fetchMock).toHaveBeenLastCalledWith('/v1/admin/logout', { method: 'POST', credentials: 'same-origin' });
    expect(container.querySelector('[data-testid="login"]')).not.toBeNull();
  });

  it('shows login after a missing session, accepts login, and handles authorization loss', async () => {
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(json({}, 401)));

    await act(async () => root.render(<CentralApplication />));
    await settle();
    expect(container.querySelector('[data-testid="login"]')).not.toBeNull();

    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="login"]')?.click());
    expect(container.querySelector('[data-testid="page-overview"]')).not.toBeNull();

    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="unauthorized-overview"]')?.click());
    expect(container.querySelector('[data-testid="login"]')).not.toBeNull();
  });
});
