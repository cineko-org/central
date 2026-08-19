import type { FormEventHandler } from 'react';
import { Alert, Box, Button, Center, PasswordInput, Stack, Text, TextInput, Title } from '@mantine/core';

export interface LoginPageViewProps {
  userId: string;
  password: string;
  loading: boolean;
  failed: boolean;
  onUserIdChange: (value: string) => void;
  onPasswordChange: (value: string) => void;
  onSubmit: FormEventHandler<HTMLFormElement>;
}

export function LoginPageView(props: LoginPageViewProps) {
  return (
    <Center mih="100dvh" bg="dark.9" px="md">
      <Box component="form" onSubmit={props.onSubmit} w="100%" maw={380}>
        <Stack gap={32}>
          <Stack gap={6}>
            <Text size="xs" c="dimmed" tt="uppercase" fw={700} lts="0.12em">Cineko Central</Text>
            <Title order={1} fz={32}>관리자 로그인</Title>
          </Stack>
          <Stack gap="md">
            {props.failed ? <Alert color="red" title="로그인 실패">관리자 ID와 비밀번호를 확인하세요.</Alert> : null}
            <TextInput label="관리자 ID" value={props.userId} onChange={(event) => props.onUserIdChange(event.currentTarget.value)} required autoFocus autoComplete="username" />
            <PasswordInput label="비밀번호" value={props.password} onChange={(event) => props.onPasswordChange(event.currentTarget.value)} required autoComplete="current-password" />
            <Button type="submit" loading={props.loading} fullWidth mt="xs">로그인</Button>
          </Stack>
        </Stack>
      </Box>
    </Center>
  );
}
