package com.tlm.cli.domain.port;

import java.util.Optional;

/** Fonte de senha pela UI (prompt). Nunca trasborda para arquivos ou logs. */
public interface PasswordSource {

    /** Pede a senha; vazio/ESC significa cancelamento. */
    Optional<char[]> promptPassword(String info);
}
