import { useState } from 'react';
import {
  KeyboardAvoidingView,
  Pressable,
  Platform,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';

import { useAuthStore } from '@/store/auth';

/**
 * Login screen. Reads lockout state from the auth store and renders the
 * backend-provided `locked` / `retryAfterSec` / `remainingAttempts` messages.
 */
export default function LoginScreen() {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const { status, isLoading, lockout, error, config, signIn, clearError } = useAuthStore();

  const locked = Boolean(lockout?.locked);
  const attemptsLeft = lockout?.remainingAttempts;
  // Backend default is 5 (AUTH_MAX_LOGIN_ATTEMPTS); the config endpoint overrides it.
  const maxAttempts = config?.maxLoginAttempts ?? 5;
  const isBusy = isLoading || submitting || locked;

  async function onSubmit() {
    if (isBusy) return;
    setSubmitting(true);
    clearError();
    try {
      await signIn(email.trim(), password);
    } finally {
      setSubmitting(false);
    }
  }

  // Already authenticated: the root guard navigates to the app group.
  if (status === 'authenticated') {
    return null;
  }

  return (
    <SafeAreaView style={styles.safeArea}>
      <KeyboardAvoidingView
        style={styles.container}
        behavior={Platform.OS === 'ios' ? 'padding' : undefined}
        keyboardVerticalOffset={Platform.OS === 'ios' ? 90 : 0}>
        <View style={styles.card}>
          <Text style={styles.title}>TikTok Live Monitor</Text>
          <Text style={styles.subtitle}>Entre com sua conta</Text>

          <TextInput
            style={styles.input}
            placeholder="E-mail"
            placeholderTextColor="#8a8a8e"
            value={email}
            onChangeText={setEmail}
            autoCapitalize="none"
            autoCorrect={false}
            keyboardType="email-address"
            textContentType="username"
            editable={!isBusy}
          />
          <TextInput
            style={styles.input}
            placeholder="Senha"
            placeholderTextColor="#8a8a8e"
            value={password}
            onChangeText={setPassword}
            autoCapitalize="none"
            autoCorrect={false}
            secureTextEntry
            textContentType="password"
            editable={!isBusy}
          />

          {locked && (
            <Text style={styles.lockedText}>
              Conta temporariamente bloqueada.
              {lockout?.retryAfterSec != null
                ? ` Tente novamente em ${Math.ceil(lockout.retryAfterSec)}s.`
                : ''}
            </Text>
          )}

          {!locked && attemptsLeft != null && attemptsLeft < maxAttempts && (
            <Text style={styles.warningText}>
              {attemptsLeft} tentativa(s) restante(s) antes do bloqueio.
            </Text>
          )}

          {error && !locked && <Text style={styles.errorText}>{error}</Text>}

          <Pressable
            onPress={onSubmit}
            disabled={isBusy}
            style={({ pressed }) => [styles.button, { opacity: isBusy || pressed ? 0.6 : 1 }]}>
            <Text style={styles.buttonText}>{locked ? 'Bloqueado' : 'Entrar'}</Text>
          </Pressable>
        </View>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safeArea: { flex: 1, backgroundColor: '#0f0f14' },
  container: { flex: 1, justifyContent: 'center', padding: 20 },
  card: { gap: 12 },
  title: { fontSize: 24, fontWeight: '700', color: '#fff' },
  subtitle: { fontSize: 15, color: '#9a9aa0', marginBottom: 8 },
  input: {
    backgroundColor: '#1c1c24',
    borderColor: '#2a2a34',
    borderWidth: 1,
    borderRadius: 10,
    paddingHorizontal: 14,
    paddingVertical: 12,
    fontSize: 16,
    color: '#fff',
  },
  button: {
    backgroundColor: '#208aef',
    borderRadius: 10,
    paddingVertical: 14,
    alignItems: 'center',
    marginTop: 8,
  },
  buttonText: { fontSize: 16, fontWeight: '600', color: '#fff' },
  lockedText: { fontSize: 14, color: '#ff6b6b', lineHeight: 20 },
  warningText: { fontSize: 13, color: '#ffd166', lineHeight: 18 },
  errorText: { fontSize: 13, color: '#ff6b6b', lineHeight: 18 },
});