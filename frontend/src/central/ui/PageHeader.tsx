import type { ReactNode } from 'react';
import { Group, Stack, Text, Title } from '@mantine/core';

export function PageHeader({ title, description, actions }: { title: string; description?: string; actions?: ReactNode }) {
  return (
    <Group justify="space-between" align="flex-end" gap="xl">
      <Stack gap={4}>
        <Title order={1} fz={{ base: 28, sm: 32 }} lh={1.15}>{title}</Title>
        {description ? <Text c="dimmed" size="sm">{description}</Text> : null}
      </Stack>
      {actions}
    </Group>
  );
}
