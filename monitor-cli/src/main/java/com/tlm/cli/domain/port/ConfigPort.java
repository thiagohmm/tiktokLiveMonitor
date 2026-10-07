package com.tlm.cli.domain.port;

import java.util.Optional;

/**
 * Porta de configuração persistida (base URL + e-mail). A senha NUNCA faz
 * parte da configuração.
 */
public interface ConfigPort {

    /** Valor resolvido (arquivo vazio/inexistente → campos vazios). */
    StoredConfig load();

    /** Persiste e define permissões 600 em POSIX. */
    void save(StoredConfig config);

    record StoredConfig(String baseUrl, String email) {

        public StoredConfig {
            baseUrl = baseUrl == null ? "" : baseUrl.trim();
            email = email == null ? "" : email.trim();
        }

        public Optional<String> baseUrlOrEmpty() {
            return baseUrl.isBlank() ? Optional.empty() : Optional.of(baseUrl);
        }

        public Optional<String> emailOrEmpty() {
            return email.isBlank() ? Optional.empty() : Optional.of(email);
        }
    }
}
