package com.tlm.cli.domain.model;

import java.util.Map;
import java.util.Optional;

/**
 * Falha de uma chamada à API: carrega status HTTP e o payload de erro do
 * backend ({@code error}, {@code code}, {@code retryAfterSec},
 * {@code remainingAttempts}, {@code needsActivation}, ...).
 */
public class ApiException extends Exception {

    public static final int STATUS_NETWORK = 0;

    private final int status;
    private final Map<String, Object> payload;

    public ApiException(int status, String message, Map<String, Object> payload) {
        super(message);
        this.status = status;
        this.payload = payload == null ? Map.of() : payload;
    }

    public ApiException(int status, String message, Map<String, Object> payload, Throwable cause) {
        super(message, cause);
        this.status = status;
        this.payload = payload == null ? Map.of() : payload;
    }

    public int status() {
        return status;
    }

    public Map<String, Object> payload() {
        return payload;
    }

    public Optional<String> code() {
        Object code = payload.get("code");
        return code == null ? Optional.empty() : Optional.of(String.valueOf(code));
    }

    public Optional<Number> retryAfterSec() {
        return number("retryAfterSec");
    }

    public Optional<Number> remainingAttempts() {
        return number("remainingAttempts");
    }

    public boolean isLockout() {
        return status == 429 || payload.containsKey("retryAfterSec");
    }

    public boolean isCsrfError() {
        return status == 403 && getCode403Text().contains("CSRF");
    }

    public boolean isNeedsActivation() {
        return Boolean.TRUE.equals(payload.get("needsActivation"));
    }

    public Optional<String> redirectTo() {
        Object target = payload.get("redirectTo");
        return target == null ? Optional.empty() : Optional.of(String.valueOf(target));
    }

    private Optional<Number> number(String key) {
        Object v = payload.get(key);
        return v instanceof Number n ? Optional.of(n) : Optional.empty();
    }

    private String getCode403Text() {
        return getMessage() == null ? "" : getMessage();
    }

    @Override
    public String toString() {
        return "ApiException[status=" + status + ", message=" + getMessage() + "]";
    }
}
