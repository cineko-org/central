import { type FormEvent, useCallback, useState } from 'react';
import {
  CreateClientUserRequestSchema,
  CreateClientUserResponseSchema,
  ListClientUsersResponseSchema,
  RotateClientPinResponseSchema,
  type ClientPinIssue,
  type ClientPinUser,
} from '@cineko/contracts/gen/ts/cineko/admin/admin_pb';
import { CentralAPIError, loadProto, protoBody, request } from './api';
import { UsersPageView } from './ui/UsersPageView';
import { useInitialRefresh } from './useInitialRefresh';

export function UsersView({ onUnauthorized }: { onUnauthorized: () => void }) {
  const [users, setUsers] = useState<ClientPinUser[]>([]);
  const [displayName, setDisplayName] = useState('');
  const [issued, setIssued] = useState<ClientPinIssue>();
  const [deleting, setDeleting] = useState<ClientPinUser>();
  const [loading, setLoading] = useState(false);
  const [failure, setFailure] = useState(false);
  const handleError = useCallback((error: unknown) => {
    if (error instanceof CentralAPIError && error.status === 401) onUnauthorized();
    else setFailure(true);
  }, [onUnauthorized]);
  const refresh = useCallback(async () => {
    try {
      setUsers((await loadProto(ListClientUsersResponseSchema, '/v1/admin/users')).users);
    } catch (error) {
      handleError(error);
    }
  }, [handleError]);

  useInitialRefresh(refresh);

  const create = async (event: FormEvent) => {
    event.preventDefault();
    setLoading(true);
    setFailure(false);
    try {
      const response = await loadProto(CreateClientUserResponseSchema, '/v1/admin/users', {
        method: 'POST', body: protoBody(CreateClientUserRequestSchema, { displayName }),
      });
      setIssued(response.issue);
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
      const response = await loadProto(RotateClientPinResponseSchema, `/v1/admin/users/${encodeURIComponent(userId)}/pin`, { method: 'POST' });
      setIssued(response.issue);
      await refresh();
    } catch (error) {
      handleError(error);
    } finally {
      setLoading(false);
    }
  };
  const remove = async () => {
    if (!deleting?.user) return;
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
