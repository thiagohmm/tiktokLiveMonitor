import { Stack } from 'expo-router';
import * as SplashScreen from 'expo-splash-screen';
import { useEffect } from 'react';
import { ActivityIndicator, View } from 'react-native';

import { useAuthStore } from '@/store/auth';

SplashScreen.preventAutoHideAsync();

export default function RootLayout() {
  const status = useAuthStore((s) => s.status);

  useEffect(() => {
    // Kick off auth init, then allow the UI to render before hiding the splash.
    void useAuthStore.getState().init();
    void SplashScreen.hideAsync();
  }, []);

  // While auth is resolving, keep the splash screen visible.
  if (status === 'idle' || status === 'loading') {
    return (
      <View style={{ flex: 1, justifyContent: 'center', backgroundColor: '#0f0f14' }}>
        <ActivityIndicator color="#208aef" />
      </View>
    );
  }

  // Authenticated users see the protected app group; everyone else the login.
  return (
    <Stack screenOptions={{ headerShown: false }}>
      {status === 'authenticated' ? (
        <Stack.Screen name="(app)" />
      ) : (
        <Stack.Screen name="(auth)" />
      )}
    </Stack>
  );
}