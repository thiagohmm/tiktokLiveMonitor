package com.tlm.cli.infrastructure.config;

import com.tlm.cli.domain.port.ConfigPort;

import java.io.IOException;
import java.io.InputStream;
import java.io.Writer;
import java.nio.charset.StandardCharsets;
import java.nio.file.FileAlreadyExistsException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.attribute.FileAttribute;
import java.nio.file.attribute.PosixFilePermission;
import java.nio.file.attribute.PosixFilePermissions;
import java.util.Optional;
import java.util.Properties;
import java.util.Set;

/**
 * Configuração persistida em {@code ~/.config/tlm-cli/config.properties}
 * (base URL + e-mail; senha NUNCA). Permissões POSIX 600 na criação/atualização.
 */
public final class ConfigStore implements ConfigPort {

    private final Path file;

    public ConfigStore(Path file) {
        this.file = file;
    }

    public static ConfigStore defaultFile() {
        return new ConfigStore(Path.of(System.getProperty("user.home"), ".config", "tlm-cli", "config.properties"));
    }

    @Override
    public StoredConfig load() {
        if (!Files.isRegularFile(file)) {
            return new StoredConfig("", "");
        }
        Properties properties = new Properties();
        try (InputStream in = Files.newInputStream(file)) {
            properties.load(in);
        } catch (IOException ignored) {
            return new StoredConfig("", "");
        }
        return new StoredConfig(
                properties.getProperty("baseUrl", ""),
                properties.getProperty("email", ""));
    }

    @Override
    public void save(StoredConfig config) {
        Properties properties = new Properties();
        properties.setProperty("baseUrl", config.baseUrl());
        properties.setProperty("email", config.email());
        try {
            Files.createDirectories(file.getParent());
            // Nascimento já restrito: sem janela em que o arquivo fica
            // legível para grupo/outros (o chmod depois só cobre o caso de
            // arquivo pré-existente).
            if (!Files.exists(file)) {
                try {
                    Set<PosixFilePermission> ownerOnly = PosixFilePermissions.fromString("rw-------");
                    FileAttribute<?> attr = PosixFilePermissions.asFileAttribute(ownerOnly);
                    Path created = Files.createFile(file, attr);
                    try (Writer out = Files.newBufferedWriter(created, StandardCharsets.UTF_8)) {
                        properties.store(out, "config tlm-cli: somente base URL + e-mail (nunca secret)");
                    }
                    return;
                } catch (UnsupportedOperationException unsupportedView) {
                    // Sem view POSIX: cai para a escrita comum abaixo.
                } catch (FileAlreadyExistsException lostRace) {
                    // Perdeu a corrida de criação: segue na escrita comum
                    // (a permissão final ainda é garantida por restrictToOwner).
                }
            }
            try (Writer out = Files.newBufferedWriter(file, StandardCharsets.UTF_8)) {
                properties.store(out, "config tlm-cli: somente base URL + e-mail (nunca secret)");
            }
            restrictToOwner();
        } catch (IOException ignored) {
            // Armazenamento restrito é best-effort; não bloqueia a aplicação.
        }
    }

    /**
     * Remove o arquivo de configuração (esquecer e-mail/base URL); idempotente.
     *
     * @return {@code true} quando um arquivo existente foi apagado.
     */
    public boolean clear() {
        try {
            return Files.deleteIfExists(file);
        } catch (IOException ignored) {
            return false;
        }
    }

    private void restrictToOwner() throws IOException {
        try {
            Set<PosixFilePermission> ownerOnly = PosixFilePermissions.fromString("rw-------");
            Files.setPosixFilePermissions(file, ownerOnly);
        } catch (UnsupportedOperationException alreadyUnsupported) {
            // Sistemas sem permissão POSIX: ignora silenciosamente.
        }
    }

    public Optional<Path> file() {
        return Optional.of(file);
    }
}
