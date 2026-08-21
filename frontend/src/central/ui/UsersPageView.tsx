import type { FormEventHandler } from 'react';
import { Alert, Box, Button, Group, Indicator, Modal, ScrollArea, Stack, Table, Text, TextInput } from '@mantine/core';
import type { ClientPinIssue, ClientPinUser } from '@cineko/contracts/gen/ts/cineko/admin/admin_pb';
import { PageHeader } from './PageHeader';

export interface UsersPageViewProps {
  users: ClientPinUser[];
  displayName: string;
  issued?: ClientPinIssue;
  deleting?: ClientPinUser;
  loading: boolean;
  failure: boolean;
  onDisplayNameChange: (value: string) => void;
  onCreate: FormEventHandler<HTMLFormElement>;
  onRotate: (userId: string) => void;
  onDeleteRequest: (user: ClientPinUser) => void;
  onDeleteCancel: () => void;
  onDelete: () => void;
  onDismissIssue: () => void;
}

export function UsersPageView(props: UsersPageViewProps) {
  return (
    <Stack gap={40}>
      <PageHeader title="사용자" description={`${props.users.length}명 · PIN으로 Launcher에 로그인합니다.`} />
      {props.failure ? <Alert color="red">요청을 처리하지 못했습니다.</Alert> : null}
      {props.issued ? (
        <Alert color="green" title={`${props.issued.user?.displayName || '사용자'} PIN`} withCloseButton onClose={props.onDismissIssue}>
          <Text fz={28} fw={800} ff="monospace" lts="0.16em">{props.issued.pin}</Text>
          <Text size="xs" c="dimmed" mt={4}>이 화면을 닫으면 PIN을 다시 확인할 수 없습니다.</Text>
        </Alert>
      ) : null}
      <Box component="form" onSubmit={props.onCreate}>
        <Group align="end" gap="sm">
          <TextInput label="새 사용자" placeholder="표시 이름" value={props.displayName} onChange={(event) => props.onDisplayNameChange(event.currentTarget.value)} required maw={360} flex={1} />
          <Button type="submit" loading={props.loading}>사용자 생성</Button>
        </Group>
      </Box>
      <ScrollArea>
        <Table verticalSpacing="md" horizontalSpacing="md" highlightOnHover miw={720}>
          <Table.Thead><Table.Tr><Table.Th>사용자</Table.Th><Table.Th>PIN</Table.Th><Table.Th>등록 기기</Table.Th><Table.Th /></Table.Tr></Table.Thead>
          <Table.Tbody>
            {props.users.map((item) => (
              <Table.Tr key={item.user?.id || 'missing-user'}>
                <Table.Td><Text fw={600}>{item.user?.displayName || '알 수 없는 사용자'}</Text><Text size="xs" c="dimmed">{item.user?.id || '식별자 없음'}</Text></Table.Td>
                <Table.Td><Group gap="sm"><Indicator color={item.pinActive ? 'green' : 'gray'} size={7} /><Text size="sm">{item.pinActive ? '활성' : '없음'}</Text></Group></Table.Td>
                <Table.Td>{item.deviceCount}</Table.Td>
                <Table.Td><Group justify="flex-end" gap="xs"><Button variant="subtle" color="gray" loading={props.loading} disabled={!item.user} onClick={() => item.user && props.onRotate(item.user.id)}>{item.pinActive ? '재발급' : '발급'}</Button><Button variant="subtle" color="red" disabled={!item.user} onClick={() => props.onDeleteRequest(item)}>사용자 제거</Button></Group></Table.Td>
              </Table.Tr>
            ))}
            {props.users.length === 0 ? <Table.Tr><Table.Td colSpan={4}><Text c="dimmed" ta="center" py={32}>등록된 사용자가 없습니다.</Text></Table.Td></Table.Tr> : null}
          </Table.Tbody>
        </Table>
      </ScrollArea>
      <Modal opened={Boolean(props.deleting)} onClose={props.onDeleteCancel} title="사용자 제거" centered>
        <Stack>
          <Text><Text span fw={700}>{props.deleting?.user?.displayName}</Text> 사용자를 제거할까요?</Text>
          <Text size="sm" c="dimmed">PIN, 로그인 세션, 등록 기기, 예매 모니터와 프리셋 등 이 사용자가 소유한 데이터를 함께 제거합니다. 이 작업은 되돌릴 수 없습니다.</Text>
          <Group justify="flex-end"><Button variant="subtle" color="gray" onClick={props.onDeleteCancel}>취소</Button><Button color="red" loading={props.loading} onClick={props.onDelete}>사용자 제거</Button></Group>
        </Stack>
      </Modal>
    </Stack>
  );
}
