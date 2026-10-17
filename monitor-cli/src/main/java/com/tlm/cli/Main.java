package com.tlm.cli;

import com.tlm.cli.application.ConnectLiveUseCase;
import com.tlm.cli.application.DisconnectLiveUseCase;
import com.tlm.cli.application.MonitorHub;
import com.tlm.cli.domain.model.MonitorException;
import com.tlm.cli.infrastructure.config.Options;
import com.tlm.cli.infrastructure.sqlite.SqliteStore;
import com.tlm.cli.infrastructure.tiktok.TikTokLiveEngine;
import com.tlm.cli.ui.TerminalUi;

import java.io.IOException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicBoolean;

/**
 * Composition root do modo local (standalone): abre o SQLite, monta a engine
 * que conecta direto ao TikTok e sobe a TUI. Não há login, API remota ou SSE.
 */
public final class Main {

    private Main() {
    }

    public static void main(String[] args) {
        // Antes da TUI: o protobuf avisa no stderr nativo ao conectar e apaga
        // as janelas. O filho já sobe com o acesso permitido.
        if (JvmRelaunch.handOffIfNeeded(args)) {
            return;
        }
        // A biblioteca loga em stderr (slf4j-simple + java.util.logging), o que
        // corromperia a tela da Lanterna: silencia antes de qualquer conexão.
        System.setProperty("org.slf4j.simpleLogger.defaultLogLevel", "off");

        Options options;
        try {
            options = Options.parse(args);
        } catch (IllegalArgumentException e) {
            System.err.println("Erro: " + e.getMessage());
            System.err.print(USAGE);
            System.exit(2);
            return;
        }
        if (options.help()) {
            System.out.print(USAGE);
            return;
        }

        try {
            run(options);
        } catch (MonitorException e) {
            System.err.println("Erro: " + e.getMessage());
            System.exit(1);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        } catch (IOException e) {
            System.err.println("Erro de terminal: " + e.getMessage());
            System.exit(1);
        }
    }

    private static void run(Options options) throws MonitorException, InterruptedException, IOException {
        SqliteStore store = new SqliteStore(options.db());
        MonitorHub hub = new MonitorHub();
        TikTokLiveEngine engine = new TikTokLiveEngine(store, hub);
        hub.bind(engine, store);

        ExecutorService executor = Executors.newVirtualThreadPerTaskExecutor();
        TerminalUi ui = new TerminalUi(options.db(), executor);
        hub.addListener(ui);

        ConnectLiveUseCase connect = new ConnectLiveUseCase(engine, hub);
        DisconnectLiveUseCase disconnect = new DisconnectLiveUseCase(engine, hub);
        ui.bind(connect, disconnect, hub);

        // Encerramento idempotente (:quit, Ctrl+C ou sinal externo): derruba a
        // conexão do TikTok, restaura o terminal e fecha o banco.
        AtomicBoolean shutdownDone = new AtomicBoolean(false);
        Runnable shutdownSequence = () -> {
            if (!shutdownDone.compareAndSet(false, true)) {
                return;
            }
            disconnect.executeBestEffort();
            engine.close();
            ui.close();
            store.close();
            executor.shutdown();
        };
        // Thread de plataforma de propósito: virtual thread é daemon e não
        // serve como shutdown hook (a JVM pode sair antes de terminá-la).
        Runtime.getRuntime().addShutdownHook(new Thread(shutdownSequence, "tlm-cli-shutdown"));

        hub.refreshState();
        try {
            ui.run();
        } finally {
            shutdownSequence.run();
        }
    }

    private static final String USAGE = """
            tlm-monitor — monitor de terminal standalone para TikTok Live

            Uso: java -jar monitor-cli/target/monitor-cli.jar [opções]

            Opções:
              --db ARQUIVO  banco SQLite local (padrão: ./data/tlm-local.db ou TLM_DB)
              --help        esta ajuda

            Tudo roda na sua máquina: a TUI conecta direto ao push do TikTok
            (sem backend, sem login) e grava os eventos no SQLite local.

            Comandos da TUI:
              header   status da conexão + live atual + tempo no ar
              feed     eventos em tempo real com cores por tipo (PageUp/PageDown rolam)
              rodapé   contadores de comentários/presentes/curtidas/seguidores/fixados
              :connect <user>     conecta à live (só uma por vez, local)
              :disconnect         desconecta a live atual
              :gift [seleção]     (lista o catálogo; :gift 1,4 ou :gift Rosa,Dino filtra)
              :pinned [list]      fixados ao vivo (busca o histórico se o feed não tem
                                  nenhum); :pinned list imprime os últimos 20 do banco
              :filter <all|chat|gift|like|follow|pinned|system> | :clear | :help
              :quit (Ctrl+C também encerra; sai desconectando a live)

            Ambiente: TLM_DB (caminho do banco); TLM_TIKTOK_API_KEY opcional para a
            assinatura do WebSocket quando o limite gratuito do Euler Stream faltar.
            """;
}
