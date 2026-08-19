import { type FormEvent, useState } from 'react';
import { loadJSON } from './api';
import type { AdminSession } from './types';
import { LoginPageView } from './ui/LoginPageView';

export function LoginView({ onLogin }: { onLogin: (session: AdminSession) => void }) {
  const [userId, setUserId] = useState('');
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const [failed, setFailed] = useState(false);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setLoading(true);
    setFailed(false);
    try {
      onLogin(await loadJSON<AdminSession>('/v1/admin/login', {
        method: 'POST', body: JSON.stringify({ userId, password }),
      }));
    } catch {
      setFailed(true);
    } finally {
      setLoading(false);
    }
  };

  return (
    <LoginPageView
      userId={userId}
      password={password}
      loading={loading}
      failed={failed}
      onUserIdChange={setUserId}
      onPasswordChange={setPassword}
      onSubmit={(event) => void submit(event)}
    />
  );
}
