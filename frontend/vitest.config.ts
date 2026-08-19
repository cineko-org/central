import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    environment: 'jsdom',
    coverage: {
      provider: 'v8',
      include: [
        'src/central/api.ts',
        'src/central/CentralApplication.tsx',
        'src/central/LoginView.tsx',
        'src/central/StatusView.tsx',
        'src/central/UsersView.tsx',
        'src/central/ReleasesView.tsx',
        'src/central/SettingsView.tsx',
        'src/central/ProbesView.tsx',
        'src/central/DataView.tsx',
        'src/central/ObservationsView.tsx',
      ],
      reportsDirectory: 'coverage',
      reporter: ['text', 'json-summary'],
      thresholds: {
        branches: 100,
        functions: 100,
        lines: 100,
        statements: 100,
      },
    },
  },
});
