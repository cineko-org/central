import { Button, Chip, CloseButton, Drawer, Modal, Notification, Paper, createTheme } from '@mantine/core';

const fontFamily = "'Pretendard Variable', Pretendard, sans-serif";

export const cinekoTheme = createTheme({
  primaryColor: 'cineko',
  defaultRadius: 'xs',
  radius: { xs: '0px', sm: '0px', md: '0px', lg: '0px', xl: '0px' },
  fontFamily,
  headings: { fontFamily, fontWeight: '700', textWrap: 'balance' },
  colors: {
    cineko: ['#fff0f0', '#ffdddd', '#ffc0c1', '#ff9a9d', '#ff7478', '#ff5a5f', '#f13f45', '#d72e34', '#bd2228', '#a5141a'],
  },
  components: {
    Button: Button.extend({ styles: { root: { borderRadius: 0 } } }),
    Chip: Chip.extend({ defaultProps: { radius: 0 } }),
    CloseButton: CloseButton.extend({ defaultProps: { radius: 0 } }),
    Drawer: Drawer.extend({ defaultProps: { radius: 0 } }),
    Modal: Modal.extend({ defaultProps: { radius: 0, centered: true } }),
    Notification: Notification.extend({ defaultProps: { radius: 0 } }),
    Paper: Paper.extend({ styles: { root: { borderRadius: 0 } } }),
  },
});
