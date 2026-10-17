package com.tlm.cli.ui;

import com.googlecode.lanterna.TerminalPosition;
import com.googlecode.lanterna.TerminalSize;
import com.googlecode.lanterna.TextColor;
import com.googlecode.lanterna.graphics.TextGraphics;
import com.googlecode.lanterna.input.KeyStroke;
import com.googlecode.lanterna.input.KeyType;
import com.googlecode.lanterna.screen.Screen;
import com.googlecode.lanterna.terminal.DefaultTerminalFactory;
import com.tlm.cli.application.ConnectLiveUseCase;
import com.tlm.cli.application.DisconnectLiveUseCase;
import com.tlm.cli.application.MonitorHub;
import com.tlm.cli.domain.model.ChatMessage;
import com.tlm.cli.domain.model.DomainEvent;
import com.tlm.cli.domain.model.FollowEvent;
import com.tlm.cli.domain.model.GiftEvent;
import com.tlm.cli.domain.model.LikeEvent;
import com.tlm.cli.domain.model.LiveMonitorState;
import com.tlm.cli.domain.model.MonitorException;
import com.tlm.cli.domain.model.PinnedCommentEntry;
import com.tlm.cli.domain.model.PinnedCommentEvent;
import com.tlm.cli.domain.model.SystemNotice;
import com.tlm.cli.domain.port.EventSink;

import java.io.IOException;
import java.nio.file.Path;
import java.time.Duration;
import java.time.Instant;
import java.time.OffsetDateTime;
import java.time.ZoneId;
import java.time.format.DateTimeFormatter;
import java.util.EnumMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Queue;
import java.util.Set;
import java.util.concurrent.ConcurrentLinkedQueue;
import java.util.concurrent.ExecutorService;

/**
 * TUI principal (Lanterna): header com status, feed rolável com cores por
 * tipo, contadores no rodapé e linha de comando ':' (estilo vi). Modo local:
 * consome eventos da engine direta e histórico do SQLite via hub.
 */
public final class TerminalUi implements EventSink, AutoCloseable {

    /** Tamanho do histórico buscado em {@code :pinned list} e no backfill. */
    private static final int PINNED_HISTORY = 20;

    private final Screen screen;
    private final ExecutorService executor;
    private final String dbLabel;
    private final Instant startedAt = Instant.now();

    private final FeedBuffer feed = new FeedBuffer();
    private final Presenter presenter = new Presenter();
    private final Map<Category, Long> counters = new EnumMap<>(Category.class);
    private final Queue<Runnable> pending = new ConcurrentLinkedQueue<>();

    private ConnectLiveUseCase connectUseCase;
    private DisconnectLiveUseCase disconnectUseCase;
    private MonitorHub hub;

    private volatile LiveMonitorState state = LiveMonitorState.offline();
    private volatile boolean closed;

    private volatile List<String> giftCatalog = List.of();
    private volatile Set<String> selectedGifts = Set.of();

    private final StringBuilder input = new StringBuilder();
    private final Object screenLock = new Object();
    private boolean screenStopped;

    public TerminalUi(Path dbFile, ExecutorService executor) throws IOException {
        this.dbLabel = dbFile == null ? "data/tlm-local.db" : dbFile.toString();
        this.executor = executor;
        DefaultTerminalFactory factory = new DefaultTerminalFactory();
        Screen created = factory.createScreen();
        created.startScreen();
        this.screen = created;
    }

    /** Liga os casos de uso e o hub de dados aos comandos da linha ':' da TUI. */
    public void bind(ConnectLiveUseCase connectUseCase, DisconnectLiveUseCase disconnectUseCase,
                     MonitorHub hub) {
        this.connectUseCase = connectUseCase;
        this.disconnectUseCase = disconnectUseCase;
        this.hub = hub;
    }

    // ---------------------------------------------------------------- EventSink

    @Override
    public void onDomainEvent(DomainEvent event) {
        post(() -> {
            feed.add(presenter.line(event));
            counters.merge(categoryOf(event), 1L, Long::sum);
        });
    }

    @Override
    public void onStateChanged(LiveMonitorState newState) {
        post(() -> this.state = newState);
    }

    @Override
    public void onNotice(String message) {
        post(() -> feed.add(new FeedLine(message, Category.INFO)));
    }

    @Override
    public void onAlert(String message) {
        post(() -> feed.add(new FeedLine(message, Category.SYSTEM)));
    }

    // ---------------------------------------------------------------- loop principal

    /** Loop da UI: roda na thread chamadora até {@code :quit}/Ctrl+C/EOF. */
    public void run() {
        while (!closed) {
            drainPending();
            try {
                KeyStroke key = screen.pollInput();
                if (key != null) {
                    handleKey(key);
                }
                screen.doResizeIfNecessary();
                draw();
                screen.refresh();
                Thread.sleep(30);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                break;
            } catch (IOException e) {
                break;
            }
        }
        stopScreenOnce();
    }

    public void requestQuit() {
        closed = true;
        drainPending();
    }

    @Override
    public void close() {
        closed = true;
        // Garante a restauração do terminal mesmo que o encerramento venha do
        // shutdown hook (SIGTERM/SIGINT externo), quando run() pode não
        // alcançar a saída normal.
        stopScreenOnce();
    }

    private void stopScreenOnce() {
        synchronized (screenLock) {
            if (screenStopped) {
                return;
            }
            screenStopped = true;
        }
        try {
            screen.stopScreen();
        } catch (IOException | IllegalStateException e) {
            // terminal já fechado ou nunca inicializado
        }
    }

    private void drainPending() {
        Runnable task;
        while ((task = pending.poll()) != null) {
            task.run();
        }
    }

    private void post(Runnable task) {
        pending.offer(task);
    }

    // ---------------------------------------------------------------- teclado

    private void handleKey(KeyStroke key) {
        if (key.isCtrlDown() && key.getCharacter() != null && key.getCharacter() == 'c') {
            requestQuit();
            return;
        }
        switch (key.getKeyType()) {
            case Enter -> submitInput();
            case Escape -> input.setLength(0);
            case Backspace -> {
                if (input.length() > 0) {
                    input.deleteCharAt(input.length() - 1);
                }
            }
            case PageUp -> feed.pageUp(feedRows());
            case PageDown -> feed.pageDown(feedRows());
            case ArrowUp -> feed.pageUp(1);
            case ArrowDown -> feed.pageDown(1);
            case Character -> {
                Character character = key.getCharacter();
                if (character != null && character.charValue() >= 32) {
                    input.append(character.charValue());
                }
            }
            default -> {
            }
        }
    }

    private int feedRows() {
        TerminalSize size = screen.getTerminalSize();
        return Math.max(1, size.getRows() - 4);
    }

    private void submitInput() {
        String typed = input.toString();
        input.setLength(0);
        CommandParser.parse(typed).ifPresent(this::dispatch);
    }

    private void dispatch(CommandParser.Command command) {
        switch (command) {
            case CommandParser.Connect connect -> executor.submit(() ->
                    runSafely(() -> connectUseCase.execute(connect.username())));
            case CommandParser.Disconnect disconnect -> executor.submit(() ->
                    runSafely(() -> disconnectUseCase.execute(disconnect.username())));
            case CommandParser.Gift gift -> handleGift(gift.argument());
            case CommandParser.Pinned pinned -> handlePinned(pinned.argument());
            case CommandParser.Filter filter -> handleFilter(filter.category());
            case CommandParser.Clear ignored -> feed.clear();
            case CommandParser.Help ignored -> showHelp();
            case CommandParser.Quit ignored -> requestQuit();
            case CommandParser.Unknown unknown -> feed.add(new FeedLine(unknown.message(), Category.SYSTEM));
        }
    }

    private void handleGift(String argument) {
        String arg = argument == null ? "" : argument.trim();
        if (arg.isEmpty()) {
            fetchGiftCatalog();
            return;
        }
        if (arg.equalsIgnoreCase("all")) {
            selectedGifts = Set.of();
            applyFilter(null);
            return;
        }
        GiftSelection.Result result = GiftSelection.resolve(arg, giftCatalog);
        if (!result.ok()) {
            feed.add(new FeedLine(result.error(), Category.SYSTEM));
            return;
        }
        Set<String> chosen = new LinkedHashSet<>();
        for (String name : result.names()) {
            chosen.add(name.toLowerCase());
        }
        selectedGifts = Set.copyOf(chosen);
        feed.setFilter(line -> line.category() == Category.GIFT
                && selectedGifts.contains(line.giftName().toLowerCase()));
        feed.add(new FeedLine("presentes: " + String.join(", ", result.names())
                + " — o feed mostra só os envios deles", Category.INFO));
    }

    private void fetchGiftCatalog() {
        executor.submit(() -> runSafely(() -> {
            List<String> catalog = hub.availableGifts();
            giftCatalog = catalog;
            post(() -> {
                if (catalog.isEmpty()) {
                    feed.add(new FeedLine(
                            "catálogo indisponível — conecte a uma live e tente de novo em alguns segundos",
                            Category.INFO));
                    return;
                }
                for (int i = 0; i < catalog.size(); i++) {
                    feed.add(new FeedLine((i + 1) + ". " + catalog.get(i), Category.INFO));
                }
                feed.add(new FeedLine(catalog.size()
                        + " presentes; escolha com :gift <números ou nomes> (ex.: :gift 1,4)", Category.INFO));
            });
        }));
    }

    private void handlePinned(String argument) {
        String arg = argument == null ? "" : argument.trim().toLowerCase();
        if (arg.equals("list")) {
            fetchPinnedHistory();
            return;
        }
        applyPinnedFilter();
    }

    private void handleFilter(Category category) {
        if (category == Category.PINNED) {
            applyPinnedFilter();
            return;
        }
        applyFilter(category);
    }

    /**
     * Filtro de fixados com backfill: sem fixados ao vivo no feed, busca os
     * últimos do banco local (marcados como histórico) e então aplica o filtro.
     */
    private void applyPinnedFilter() {
        if (feed.hasCategory(Category.PINNED)) {
            applyFilter(Category.PINNED);
            return;
        }
        executor.submit(() -> runSafely(() -> {
            String live = state.username();
            List<PinnedCommentEntry> entries = hub.pinnedComments(live, PINNED_HISTORY);
            boolean allLives = live.isBlank();
            post(() -> {
                if (entries.isEmpty()) {
                    feed.add(new FeedLine("nenhum comentário fixado no histórico local", Category.INFO));
                } else {
                    feed.add(new FeedLine(entries.size() + " fixados do histórico:", Category.INFO));
                    for (PinnedCommentEntry entry : entries) {
                        feed.add(new FeedLine(describePinned(entry, allLives) + " (histórico)", Category.PINNED));
                    }
                }
                applyFilter(Category.PINNED);
            });
        }));
    }

    private void fetchPinnedHistory() {
        executor.submit(() -> runSafely(() -> {
            String live = state.username();
            List<PinnedCommentEntry> entries = hub.pinnedComments(live, PINNED_HISTORY);
            boolean allLives = live.isBlank();
            post(() -> {
                if (entries.isEmpty()) {
                    feed.add(new FeedLine("nenhum comentário fixado", Category.INFO));
                    return;
                }
                feed.add(new FeedLine("últimos " + entries.size() + " fixados:", Category.INFO));
                for (PinnedCommentEntry entry : entries) {
                    feed.add(new FeedLine(describePinned(entry, allLives), Category.INFO));
                }
            });
        }));
    }

    private static String describePinned(PinnedCommentEntry entry, boolean allLives) {
        String who = entry.nickname().isBlank() ? entry.uniqueId() : entry.nickname();
        String liveAt = allLives && !entry.liveName().isBlank() ? "@" + entry.liveName() + " " : "";
        String when = pinnedStamp(entry.timestamp());
        return "[fixado" + when + "] " + liveAt + "@" + who + ": " + entry.comment();
    }

    /** Melhor esforço de exibição " HH:mm" do timestamp ISO; inválido → "". */
    private static String pinnedStamp(String timestamp) {
        if (timestamp == null || timestamp.isBlank()) {
            return "";
        }
        try {
            Instant at = OffsetDateTime.parse(timestamp).toInstant();
            return " " + DateTimeFormatter.ofPattern("HH:mm").withZone(ZoneId.systemDefault()).format(at);
        } catch (RuntimeException e) {
            return "";
        }
    }

    private void applyFilter(Category category) {
        if (category == null) {
            feed.setFilter(line -> true);
            feed.add(new FeedLine("filtro: all", Category.INFO));
        } else {
            feed.setFilter(line -> line.category() == category);
            feed.add(new FeedLine("filtro: " + category.name().toLowerCase(), Category.INFO));
        }
    }

    private void showHelp() {
        for (String line : CommandParser.helpText().split("\\n")) {
            feed.add(new FeedLine(line, Category.INFO));
        }
    }

    private void runSafely(LocalCallable callable) {
        try {
            callable.run();
        } catch (MonitorException error) {
            post(() -> feed.add(new FeedLine(error.getMessage(), Category.SYSTEM)));
        } catch (RuntimeException runtime) {
            post(() -> feed.add(new FeedLine("Erro inesperado: " + runtime.getMessage(), Category.SYSTEM)));
        }
    }

    @FunctionalInterface
    private interface LocalCallable {
        void run() throws MonitorException;
    }

    // ---------------------------------------------------------------- desenho

    private void draw() {
        TerminalSize size = screen.getTerminalSize();
        TextGraphics g = screen.newTextGraphics();
        g.setBackgroundColor(TextColor.ANSI.DEFAULT);
        g.fillRectangle(new TerminalPosition(0, 0), size, ' ');

        int rows = size.getRows();
        int cols = size.getColumns();
        if (rows < 4 || cols < 20) {
            g.setForegroundColor(TextColor.ANSI.WHITE_BRIGHT);
            g.putString(0, Math.max(0, rows / 2), "terminal pequeno demais");
            return;
        }

        drawHeader(g, rows, cols);
        drawFeed(g, rows, cols);
        drawStats(g, rows, cols);
        drawInput(g, rows, cols);
    }

    private void drawHeader(TextGraphics g, int rows, int cols) {
        LiveMonitorState st = state;
        String status;
        TextColor statusColor;
        if (st.connected()) {
            status = "Monitorando " + st.displayName();
            statusColor = TextColor.ANSI.GREEN_BRIGHT;
        } else {
            status = "Desconectado";
            statusColor = TextColor.ANSI.RED_BRIGHT;
        }
        g.setForegroundColor(TextColor.ANSI.WHITE_BRIGHT);
        g.putString(0, 0, "TikTok Live Monitor (local)");
        g.setForegroundColor(statusColor);
        g.putString(28, 0, truncate(status, cols - 29));
        g.setForegroundColor(TextColor.ANSI.WHITE_BRIGHT);
        String uptime = "no ar " + formatUptime(Duration.between(startedAt, Instant.now()));
        g.putString(Math.max(0, cols - uptime.length()), 0, uptime);

        g.setForegroundColor(TextColor.ANSI.BLACK_BRIGHT);
        String meta = "SQLite: " + dbLabel
                + "  live: " + (st.connected() ? st.displayName() : "-");
        g.putString(0, 1, truncate(meta, cols));
    }

    private void drawFeed(TextGraphics g, int rows, int cols) {
        int available = Math.max(0, rows - 4);
        List<FeedLine> window = feed.window(available);
        int y = 2 + Math.max(0, available - window.size());
        for (FeedLine line : window) {
            g.setForegroundColor(colorOf(line.category()));
            g.putString(0, y, truncate(line.text(), cols));
            y++;
        }
        if (!feed.atBottom()) {
            g.setForegroundColor(TextColor.ANSI.CYAN_BRIGHT);
            g.putString(cols - 2, 2, "▲");
        }
    }

    private void drawStats(TextGraphics g, int rows, int cols) {
        long comments = counters.getOrDefault(Category.CHAT, 0L);
        long gifts = counters.getOrDefault(Category.GIFT, 0L);
        long likes = counters.getOrDefault(Category.LIKE, 0L);
        long followers = counters.getOrDefault(Category.FOLLOW, 0L);
        long pinned = counters.getOrDefault(Category.PINNED, 0L);
        String stats = String.format(
                "comentários %d   presentes %d   curtidas %d   seguidores %d   fixados %d",
                comments, gifts, likes, followers, pinned);
        g.setForegroundColor(TextColor.ANSI.BLACK_BRIGHT);
        g.putString(0, rows - 2, truncate(stats, cols));
    }

    private void drawInput(TextGraphics g, int rows, int cols) {
        String line = ": " + input;
        g.setForegroundColor(TextColor.ANSI.WHITE_BRIGHT);
        g.putString(0, rows - 1, truncate(line, Math.max(0, cols - 2)));
        g.setBackgroundColor(TextColor.ANSI.WHITE_BRIGHT);
        g.putString(Math.min(cols - 2, line.length()), rows - 1, " ");
        g.setBackgroundColor(TextColor.ANSI.DEFAULT);
    }

    private TextColor colorOf(Category category) {
        return switch (category) {
            case CHAT -> TextColor.ANSI.CYAN_BRIGHT;
            case GIFT -> TextColor.ANSI.YELLOW_BRIGHT;
            case LIKE -> TextColor.ANSI.MAGENTA_BRIGHT;
            case FOLLOW -> TextColor.ANSI.GREEN_BRIGHT;
            case PINNED -> TextColor.ANSI.BLUE_BRIGHT;
            case SYSTEM -> TextColor.ANSI.RED_BRIGHT;
            case INFO -> TextColor.ANSI.WHITE_BRIGHT;
        };
    }

    private Category categoryOf(DomainEvent event) {
        return switch (event) {
            case ChatMessage m -> Category.CHAT;
            case GiftEvent g -> Category.GIFT;
            case LikeEvent l -> Category.LIKE;
            case FollowEvent f -> Category.FOLLOW;
            case PinnedCommentEvent p -> Category.PINNED;
            case SystemNotice n -> Category.SYSTEM;
        };
    }

    private static String truncate(String text, int cols) {
        if (cols <= 0) {
            return "";
        }
        if (text.codePointCount(0, text.length()) <= cols) {
            return text;
        }
        int limit = text.offsetByCodePoints(0, Math.max(0, cols - 1));
        // Não divide um par surrogate ao meio.
        if (limit > 0 && text.codePointBefore(limit) > 0xFFFF
                && Character.isLowSurrogate(text.charAt(limit))) {
            limit--;
        }
        return text.substring(0, limit) + "…";
    }

    private static String formatUptime(Duration duration) {
        long total = Math.max(0, duration.getSeconds());
        long h = total / 3600;
        long m = (total % 3600) / 60;
        long s = total % 60;
        return String.format("%02d:%02d:%02d", h, m, s);
    }
}
