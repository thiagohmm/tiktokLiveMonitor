import { Pressable, StyleSheet, Text, View } from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';

import { useAuthStore } from '@/store/auth';

/** Placeholder home for the protected app group (Fase 4 adds the real UI). */
export default function AppHome() {
  const user = useAuthStore((s) => s.user);
  const signOut = useAuthStore((s) => s.signOut);

  return (
    <SafeAreaView style={styles.safeArea}>
      <View style={styles.container}>
        <Text style={styles.title}>Sessão iniciada</Text>
        <Text style={styles.email}>{user?.email ?? 'offline'}</Text>
        <Text style={styles.role}>role: {user?.role ?? '-'}</Text>
        <Pressable
          onPress={() => void signOut()}
          style={styles.logoutButton}>
          <Text style={styles.logoutText}>Sair</Text>
        </Pressable>
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safeArea: { flex: 1, backgroundColor: '#0f0f14' },
  container: { flex: 1, justifyContent: 'center', padding: 24, gap: 8 },
  title: { fontSize: 22, fontWeight: '700', color: '#fff' },
  email: { fontSize: 15, color: '#9a9aa0' },
  role: { fontSize: 13, color: '#208aef' },
  logoutButton: {
    backgroundColor: '#2a2a34',
    borderRadius: 10,
    paddingVertical: 14,
    alignItems: 'center',
    marginTop: 24,
  },
  logoutText: { fontSize: 16, fontWeight: '600', color: '#fff' },
});