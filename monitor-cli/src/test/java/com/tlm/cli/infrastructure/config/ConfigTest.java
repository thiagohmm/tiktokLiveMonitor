package com.tlm.cli.infrastructure.config;

import com.tlm.cli.domain.port.ConfigPort;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;

import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.attribute.PosixFilePermission;
import java.util.HashMap;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;
import static org.junit.jupiter.api.Assumptions.assumeTrue;

class ConfigStoreTest {

    @TempDir
    Path tempDir;

    private Path file() {
        return tempDir.resolve("config").resolve("tlm-cli").resolve("config.properties");
    }

    @Test
    void loadOfMissingFileIsEmpty() {
        ConfigStore store = new ConfigStore(file());
        ConfigPort.StoredConfig loaded = store.load();
        assertTrue(loaded.baseUrlOrEmpty().isEmpty());
        assertTrue(loaded.emailOrEmpty().isEmpty());
    }

    @Test
    void saveThenLoadRoundTrip() throws IOException {
        ConfigStore store = new ConfigStore(file());
        store.save(new ConfigPort.StoredConfig("https://example.org", "eu@example.org"));
        assertTrue(Files.exists(file()));
        ConfigPort.StoredConfig loaded = store.load();
        assertEquals("https://example.org", loaded.baseUrl());
        assertEquals("eu@example.org", loaded.email());
    }

    @Test
    void savesPasswordNeverEver() throws IOException {
        ConfigStore store = new ConfigStore(file());
        store.save(new ConfigPort.StoredConfig("https://example.org", "eu@example.org"));
        String content = Files.readString(file());
        assertFalse(content.contains("password"));
        assertFalse(content.contains("senha"));
        assertFalse(content.toLowerCase().contains("token"));
    }

    @Test
    void posixPermissionsAreOwnerOnly() throws IOException {
        assumeTrue(java.nio.file.FileSystems.getDefault().supportedFileAttributeViews().contains("posix"),
                "não-POSIX: sem verificação de permissão");
        ConfigStore store = new ConfigStore(file());
        store.save(new ConfigPort.StoredConfig("https://example.org", "eu@example.org"));
        var permissions = java.nio.file.Files.getPosixFilePermissions(file());
        assertTrue(permissions.contains(PosixFilePermission.OWNER_READ), permissions::toString);
        assertTrue(permissions.contains(PosixFilePermission.OWNER_WRITE), permissions::toString);
        assertFalse(permissions.contains(PosixFilePermission.GROUP_READ), permissions::toString);
        assertFalse(permissions.contains(PosixFilePermission.OTHERS_READ), permissions::toString);
    }

    @Test
    void clearRemovesFileAndForgetsConfig() throws IOException {
        ConfigStore store = new ConfigStore(file());
        store.save(new ConfigPort.StoredConfig("https://example.org", "eu@example.org"));
        assertTrue(store.clear());
        assertFalse(Files.exists(file()));
        assertTrue(store.load().emailOrEmpty().isEmpty());
        assertTrue(store.load().baseUrlOrEmpty().isEmpty());
    }

    @Test
    void clearIsIdempotent() {
        ConfigStore store = new ConfigStore(file());
        assertFalse(store.clear());
        assertFalse(store.clear());
    }
}

class ConfigResolverTest {

    @TempDir
    Path tempDir;

    private ConfigStore store(String baseUrl, String email) throws IOException {
        Path file = tempDir.resolve("config.properties");
        if (baseUrl != null) {
            Files.writeString(file, "baseUrl=" + baseUrl + "\nemail=" + email + "\n");
        }
        return new ConfigStore(file);
    }

    @Test
    void defaultsWhenNothingConfigured() throws IOException {
        ConfigResolver resolver = new ConfigResolver(store(null, null), Map.of());
        ConfigResolver.Resolved resolved = resolver.resolve(Options.parse());
        assertEquals(ConfigResolver.DEFAULT_BASE_URL, resolved.baseUrl());
        assertTrue(resolved.emailRequested());
        assertTrue(resolved.passwordRequested());
        assertNull(resolved.envPassword());
    }

    @Test
    void emptyEnvPasswordMeansAbsent() throws IOException {
        ConfigResolver resolver = new ConfigResolver(store("https://f.example", "f@x"), Map.of("TLM_PASSWORD", ""));
        ConfigResolver.Resolved resolved = resolver.resolve(Options.parse());
        assertNull(resolved.envPassword(), "env vazio deve ser tratado como ausente (evita enviá-la em branco)");
        assertTrue(resolved.passwordRequested());
    }

    @Test
    void flagBeatsEnvAndFile() throws IOException {
        ConfigResolver resolver = new ConfigResolver(store("https://file.example", "file@x"), Map.of(
                "TLM_URL", "https://env.example",
                "TLM_EMAIL", "env@x",
                "TLM_PASSWORD", "env-pass"));
        ConfigResolver.Resolved resolved = resolver.resolve(Options.parse("--url", "https://flag.example", "--email", "flag@x"));
        assertEquals("https://flag.example", resolved.baseUrl());
        assertEquals("flag@x", resolved.email());
        assertEquals("env-pass", resolved.envPassword());
    }

    @Test
    void envBeatsFile() throws IOException {
        ConfigResolver resolver = new ConfigResolver(store("https://file.example", "file@x"), Map.of(
                "TLM_URL", "https://env.example/",
                "TLM_EMAIL", "env@x"));
        ConfigResolver.Resolved resolved = resolver.resolve(Options.parse());
        assertEquals("https://env.example", resolved.baseUrl());
        assertEquals("env@x", resolved.email());
    }

    @Test
    void fileIsUsedWhenNoFlagsOrEnv() throws IOException {
        ConfigResolver resolver = new ConfigResolver(store("https://file.example/", "file@x"), Map.of());
        ConfigResolver.Resolved resolved = resolver.resolve(Options.parse());
        assertEquals("https://file.example", resolved.baseUrl());
        assertEquals("file@x", resolved.email());
    }

    @Test
    void trailingSlashTrimmedOnEverySource() {
        ConfigResolver resolver = new ConfigResolver(new ConfigStore(tempDir.resolve("nao-existe")), Map.of());
        ConfigResolver.Resolved resolved = resolver.resolve(Options.parse("--url", "https://x.example///"));
        assertEquals("https://x.example", resolved.baseUrl());
    }

    @Test
    void persistStoresOnlyBaseUrlAndEmail() throws IOException {
        Path file = tempDir.resolve("persist.properties");
        ConfigStore store = new ConfigStore(file);
        ConfigResolver resolver = new ConfigResolver(store, Map.of("TLM_PASSWORD", "well-kept-password"));
        ConfigResolver.Resolved resolved = resolver.resolve(Options.parse("--url", "https://x.example", "--email", "a@b.c"));
        resolver.persist(resolved);

        assertEquals("https://x.example", store.load().baseUrl());
        assertEquals("a@b.c", store.load().email());
        String content = Files.readString(file);
        assertFalse(content.contains("password"));
        assertFalse(content.contains("well-kept"));
        assertFalse(content.toLowerCase().contains("senha"));
    }

    @Test
    void unknownFlagFailsFast() {
        assertThrows(IllegalArgumentException.class, () -> Options.parse("--sudo"));
    }
}

class OptionsTest {

    @Test
    void valueCannotBeTheNextFlag() {
        assertThrows(IllegalArgumentException.class, () -> Options.parse("--url", "--email", "x@y"));
    }

    @Test
    void equalsFormIsStillAccepted() {
        Options options = Options.parse("--url=https://a.example");
        assertEquals("https://a.example", options.url());
        assertNull(options.email());
    }

    @Test
    void forgetFlagsParseAsBooleans() {
        assertTrue(Options.parse("--esquecer").forget());
        assertTrue(Options.parse("--forget").forget());
        Options options = Options.parse("--esquecer", "--email", "novo@x");
        assertTrue(options.forget());
        assertEquals("novo@x", options.email());
        assertFalse(Options.parse().forget());
        assertFalse(Options.parse("--url", "https://a.example").forget());
    }
}
