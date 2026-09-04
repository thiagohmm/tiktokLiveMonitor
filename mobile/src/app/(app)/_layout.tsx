import { Stack } from 'expo-router';

/**
 * Protected app route group. Rendered by the root guard only when the user is
 * authenticated. Concrete screens (monitor, ranking, admin) are added in later
 * phases; for now it shows a placeholder home.
 */
export default function AppLayout() {
  return (
    <Stack screenOptions={{ headerShown: false }}>
      <Stack.Screen name="index" />
    </Stack>
  );
}