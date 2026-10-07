package com.tlm.cli.infrastructure.config;

import com.tlm.cli.domain.port.ConfigPort;

import java.util.Map;

/**
 * Precedência de configuração: flags &gt; env &gt; arquivo &gt; padrão.
 * A senha vem apenas do env ( {@code TLM_PASSWORD} ) ou do prompt — nunca é
 * persistida; o prompt acontece fora desta classe (e-mail/senha continuam
 * em memória).
 */
public final class ConfigResolver {

    public static final String DEFAULT_BASE_URL = "https://livemonitortk.com.br";

    private final ConfigPort store;
    private final Map<String, String> env;

    public ConfigResolver(ConfigPort store, Map<String, String> env) {
        this.store = store;
        this.env = env;
    }

    public Resolved resolve(Options options) {
        ConfigPort.StoredConfig persisted = store.load();
        String baseUrl = firstNonBlank(options.url(), env.get("TLM_URL"), persisted.baseUrlOrEmpty().orElse(null), DEFAULT_BASE_URL);
        String email = firstNonBlank(options.email(), env.get("TLM_EMAIL"), persisted.emailOrEmpty().orElse(null));
        String envPassword = firstNonBlank(env.get("TLM_PASSWORD"));
        // "presente mas vazio" == ausente: o consumidor compara com null.
        return new Resolved(normalizeBaseUrl(baseUrl), email, envPassword.isBlank() ? null : envPassword, persisted);
    }

    private static String normalizeBaseUrl(String baseUrl) {
        String normalized = baseUrl == null ? "" : baseUrl.trim();
        while (normalized.endsWith("/")) {
            normalized = normalized.substring(0, normalized.length() - 1);
        }
        return normalized;
    }

    /** Persista após login bem-sucedido: só URL + e-mail. */
    public void persist(Resolved resolved) {
        store.save(new ConfigPort.StoredConfig(resolved.baseUrl(), resolved.email()));
    }

    private static String firstNonBlank(String... candidates) {
        for (String candidate : candidates) {
            if (candidate != null && !candidate.isBlank()) {
                return candidate.trim();
            }
        }
        return "";
    }

    /** Configuração resolvida (sem segredo) + senha de env quando houver. */
    public record Resolved(String baseUrl, String email, String envPassword, ConfigPort.StoredConfig persisted) {

        public boolean emailRequested() {
            return email == null || email.isBlank();
        }

        public boolean passwordRequested() {
            return envPassword == null || envPassword.isBlank();
        }
    }
}
