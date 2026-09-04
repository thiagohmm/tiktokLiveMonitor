import { Stack } from 'expo-router';

/**
 * Auth route group: only reachable when unauthenticated. The root guard
 * renders this group (and not the app group) while `status !== 'authenticated'`.
 */
export default function AuthLayout() {
  return (
    <Stack screenOptions={{ headerShown: false }}>
      <Stack.Screen name="login" />
    </Stack>
  );
}