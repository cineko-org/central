import { useCallback, useEffect, useState } from 'react';
import { CentralShellView, type CentralPage } from './ui/CentralShellView';
import { loadJSON } from './api';
import { LoginView } from './LoginView';
import { ObservationsView } from './ObservationsView';
import { DataView } from './DataView';
import { ProbesView } from './ProbesView';
import { ReleasesView } from './ReleasesView';
import { SettingsView } from './SettingsView';
import { StatusView } from './StatusView';
import type { AdminSession } from './types';
import { UsersView } from './UsersView';

export function CentralApplication() {
  const [session, setSession] = useState<AdminSession>();
  const [loaded, setLoaded] = useState(false);
  const [page, setPage] = useState<CentralPage>('overview');
  const [navigationOpen, setNavigationOpen] = useState(false);

  useEffect(() => {
    void loadJSON<AdminSession>('/v1/admin/session')
      .then(setSession)
      .catch(() => undefined)
      .finally(() => setLoaded(true));
  }, []);

  const logout = useCallback(async () => {
    await fetch('/v1/admin/logout', { method: 'POST', credentials: 'same-origin' });
    setSession(undefined);
  }, []);
  const unauthorized = useCallback(() => setSession(undefined), []);
  const navigate = useCallback((nextPage: CentralPage) => {
    setPage(nextPage);
    setNavigationOpen(false);
  }, []);

  if (!loaded) return null;
  if (!session) return <LoginView onLogin={setSession} />;

  return (
    <CentralShellView
      page={page}
      session={session}
      navigationOpen={navigationOpen}
      onNavigate={navigate}
      onToggleNavigation={() => setNavigationOpen((opened) => !opened)}
      onLogout={() => void logout()}
    >
      {page === 'overview' ? <StatusView onUnauthorized={unauthorized} /> : null}
      {page === 'observations' ? <ObservationsView onUnauthorized={unauthorized} /> : null}
      {page === 'probes' ? <ProbesView onUnauthorized={unauthorized} /> : null}
      {page === 'data' ? <DataView onUnauthorized={unauthorized} /> : null}
      {page === 'releases' ? <ReleasesView onUnauthorized={unauthorized} /> : null}
      {page === 'users' ? <UsersView onUnauthorized={unauthorized} /> : null}
      {page === 'settings' ? <SettingsView onUnauthorized={unauthorized} /> : null}
    </CentralShellView>
  );
}
