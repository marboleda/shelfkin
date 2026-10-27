import { render, screen } from '@testing-library/react-native';

import HomeScreen from '@/app/index';

describe('<HomeScreen />', () => {
  test('renders the app name', async () => {
    await render(<HomeScreen />);

    expect(screen.getByText('Shelfkin')).toBeTruthy();
  });
});
