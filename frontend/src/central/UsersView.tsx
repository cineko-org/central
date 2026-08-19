import { type FormEvent, useCallback, useEffect, useState } from 'react';
import { CentralAPIError, loadJSON, request } from './api';
import type { ClientPINIssue, ClientPINUser } from './types';
import { UsersPageView } from './ui/UsersPageView';

export function UsersView({ onUnauthorized }: { onUnauthorized: () => void }) {
  const [users, setUsers] = useState<ClientPINUser[]>([]);
  const [displayName, setDisplayName] = useState('');
  const [issued, setIssued] = useState<ClientPINIssue>();
  const [deleting, setDeleting] = useState<ClientPINUser>();
  const [loading, setLoading] = useState(false);
  const [failure, setFailure] = useState(false);
  const handleError = useCallback((error: unknown) => {
    if (error instanceof CentralAPIError && error.status === 401) onUnauthorized();
    else setFailure(true);
  }, [onUnauthorized]);
  const refresh = useCallback(async () => {
    try {
      setUsers((await loadJSON<{ data: ClientPINUser[] }>('/v1/admin/users')).data);
    } catch (error) {
      handleError(error);
    }
  }, [handleError]);

  useEffect(() => { void refresh(); }, [refresh]);

  const create = async (event: FormEvent) => {
    event.preventDefault();
    setLoading(true);
    setFailure(false);
    try {
      setIssued(await loadJSON<ClientPINIssue>('/v1/admin/users', {
        method: 'POST', body: JSON.stringify({ displayName }),
      }));
      setDisplayName('');
      await refresh();
    } catch (error) {
      handleError(error);
    } finally {
      setLoading(false);
    }
  };
  const rotate = async (userId: string) => {
    setLoading(true);
    setFailure(false);
    try {
      setIssued(await loadJSON<ClientPINIssue>(`/v1/admin/users/${encodeURIComponent(userId)}/pin`, { method: 'POST' }));
      await refresh();
    } catch (error) {
      handleError(error);
    } finally {
      setLoading(false);
    }
  };
  const remove = async () => {
    if (!deleting) return;
    setLoading(true);
    setFailure(false);
    try {
      await request(`/v1/admin/users/${encodeURIComponent(deleting.user.id)}`, { method: 'DELETE' });
      setDeleting(undefined);
      setIssued(undefined);
      await refresh();
    } catch (error) {
      handleError(error);
    } finally {
      setLoading(false);
    }
  };

  return (
    <UsersPageView
      users={users}
      displayName={displayName}
      issued={issued}
      deleting={deleting}
      loading={loading}
      failure={failure}
      onDisplayNameChange={setDisplayName}
      onCreate={(event) => void create(event)}
      onRotate={(userId) => void rotate(userId)}
      onDeleteRequest={setDeleting}
      onDeleteCancel={() => setDeleting(undefined)}
      onDelete={() => void remove()}
      onDismissIssue={() => setIssued(undefined)}
    />
  );
}
