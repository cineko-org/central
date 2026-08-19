import '@mantine/core/styles.css';

import { MantineProvider } from '@mantine/core';
import { createRoot } from 'react-dom/client';
import { cinekoTheme } from '../app/theme';
import { CentralApplication } from './CentralApplication';

const pretendard = new FontFace('Pretendard Variable', "url('/assets/PretendardVariable.woff2') format('woff2-variations')", { display: 'swap', style: 'normal', weight: '45 920' });
document.fonts.add(pretendard);
void pretendard.load();

const root = document.getElementById('root');
if (!root) throw new Error('Cineko Central root element is missing');
createRoot(root).render(<MantineProvider forceColorScheme="dark" theme={cinekoTheme}><CentralApplication /></MantineProvider>);
