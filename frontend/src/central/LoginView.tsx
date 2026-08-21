import { type FormEvent, useState } from 'react';
import {
  LoginRequestSchema,
  LoginResponseSchema,
  type Principal,
} from '@cineko/contracts/gen/ts/cineko/admin/admin_pb';
import { loadProto, protoBody } from './api';
import { LoginPageView } from './ui/LoginPageView';

export function LoginView({ onLogin }: { onLogin: (session: Principal) => void }) {
  const [userId, setUserId] = useState('');
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const [failed, setFailed] = useState(false);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setLoading(true);
    setFailed(false);
    try {
      const response = await loadProto(LoginResponseSchema, '/v1/admin/login', {
        method: 'POST', body: protoBody(LoginRequestSchema, { userId, password }),
      });
      if (!response.principal) throw new Error('missing admin principal');
      onLogin(response.principal);
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
