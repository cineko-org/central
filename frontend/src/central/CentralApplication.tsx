import { useCallback, useEffect, useState } from 'react';
import { GetSessionResponseSchema, type Principal } from '@cineko/contracts/gen/ts/cineko/admin/admin_pb';
import { CentralShellView, type CentralPage } from './ui/CentralShellView';
import { loadProto } from './api';
import { LoginView } from './LoginView';
import { ObservationsView } from './ObservationsView';
import { DataView } from './DataView';
import { ProbesView } from './ProbesView';
import { ReleasesView } from './ReleasesView';
import { SettingsView } from './SettingsView';
import { StatusView } from './StatusView';
import { UsersView } from './UsersView';

export function CentralApplication() {
  const [session, setSession] = useState<Principal>();
  const [loaded, setLoaded] = useState(false);
  const [page, setPage] = useState<CentralPage>('overview');
  const [navigationOpen, setNavigationOpen] = useState(false);

  useEffect(() => {
    void loadProto(GetSessionResponseSchema, '/v1/admin/session')
      .then((response) => setSession(response.principal))
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
