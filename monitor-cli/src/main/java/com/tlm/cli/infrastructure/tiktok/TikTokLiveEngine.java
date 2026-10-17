package com.tlm.cli.infrastructure.tiktok;

import com.tlm.cli.domain.model.ChatMessage;
import com.tlm.cli.domain.model.DomainEvent;
import com.tlm.cli.domain.model.FollowEvent;
import com.tlm.cli.domain.model.GiftEvent;
import com.tlm.cli.domain.model.LikeEvent;
import com.tlm.cli.domain.model.LiveMonitorState;
import com.tlm.cli.domain.model.MonitorException;
import com.tlm.cli.domain.model.PinnedCommentEvent;
import com.tlm.cli.domain.port.EventSink;
import com.tlm.cli.domain.port.LocalStorePort;
import com.tlm.cli.domain.port.MonitorEnginePort;
import io.github.jwdeveloper.tiktok.TikTokLive;
import io.github.jwdeveloper.tiktok.data.events.TikTokCommentEvent;
import io.github.jwdeveloper.tiktok.data.events.TikTokConnectedEvent;
import io.github.jwdeveloper.tiktok.data.events.TikTokDisconnectedEvent;
import io.github.jwdeveloper.tiktok.data.events.TikTokErrorEvent;
import io.github.jwdeveloper.tiktok.data.events.TikTokLiveEndedEvent;
import io.github.jwdeveloper.tiktok.data.events.TikTokLivePausedEvent;
import io.github.jwdeveloper.tiktok.data.events.TikTokLiveUnpausedEvent;
import io.github.jwdeveloper.tiktok.data.events.TikTokReconnectingEvent;
import io.github.jwdeveloper.tiktok.data.events.gift.TikTokGiftEvent;
import io.github.jwdeveloper.tiktok.data.events.room.TikTokRoomPinEvent;
import io.github.jwdeveloper.tiktok.data.events.social.TikTokFollowEvent;
import io.github.jwdeveloper.tiktok.data.events.social.TikTokLikeEvent;
import io.github.jwdeveloper.tiktok.data.events.social.TikTokShareEvent;
import io.github.jwdeveloper.tiktok.data.models.gifts.Gift;
import io.github.jwdeveloper.tiktok.data.settings.LiveClientSettings;
import io.github.jwdeveloper.tiktok.live.LiveClient;
import io.github.jwdeveloper.tiktok.live.builder.LiveClientBuilder;

import java.time.Duration;
import java.time.Instant;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.logging.Level;

/**
 * Engine local: conecta direto ao push do TikTok (TikTokLiveJava), mapeia os
 * eventos para o domínio, persiste no store local e publica no sink. A
 * reconexão é da própria biblioteca ({@code retryOnConnectionFailure}).
 */
public final class TikTokLiveEngine implements MonitorEnginePort {

    /** Janela de dedupe de pins, espelhando o bridge do backend (máx. 200). */
    private static final int PIN_KEYS_MAX = 200;

    private final LocalStorePort store;
    private final EventSink sink;
    private final Object connectLock = new Object();
    private final LinkedHashSet<String> processedPins = new LinkedHashSet<>();

    private volatile LiveClient client;
    private volatile String currentLive = "";
    private volatile boolean connected;
    private volatile boolean reconnectNoticeSent;
    private volatile String lastDisconnectReason = "";

    public TikTokLiveEngine(LocalStorePort store, EventSink sink) {
        this.store = store;
        this.sink = sink;
    }

    @Override
    public void connect(String username) throws MonitorException {
        String target = username == null ? "" : username.trim();
        if (target.isEmpty()) {
            throw new MonitorException("informe o username da live");
        }
        LiveClient newClient;
        synchronized (connectLock) {
            closeCurrent();
            currentLive = target;
            connected = false;
            reconnectNoticeSent = false;
            lastDisconnectReason = "";
            processedPins.clear();
            newClient = buildClient(target);
            client = newClient;
        }
        newClient.connectAsync();
    }

    @Override
    public void disconnect() {
        synchronized (connectLock) {
            closeCurrent();
            currentLive = "";
            connected = false;
        }
        publishState();
    }

    @Override
    public String currentLive() {
        return currentLive;
    }

    @Override
    public boolean isConnected() {
        return connected;
    }

    @Override
    public List<String> availableGifts() {
        LiveClient current = client;
        if (current == null) {
            return List.of();
        }
        try {
            return current.getGiftManager().toList().stream()
                    .map(Gift::getName)
                    .filter(name -> name != null && !name.isBlank())
                    .map(String::trim)
                    .distinct()
                    .sorted(String.CASE_INSENSITIVE_ORDER)
                    .toList();
        } catch (RuntimeException e) {
            return List.of();
        }
    }

    @Override
    public void close() {
        disconnect();
    }

    // ---------------------------------------------------------------- cliente

    private LiveClient buildClient(String username) {
        LiveClientBuilder builder = TikTokLive.newClient(username);
        builder.configure(this::configure);
        builder.onEvent(TikTokCommentEvent.class, (c, e) -> onChat(e));
        builder.onEvent(TikTokGiftEvent.class, (c, e) -> onGift(e));
        builder.onEvent(TikTokLikeEvent.class, (c, e) -> onLike(e));
        builder.onEvent(TikTokFollowEvent.class, (c, e) -> onFollow(e));
        builder.onEvent(TikTokShareEvent.class, (c, e) -> onShare(e));
        builder.onEvent(TikTokRoomPinEvent.class, (c, e) -> onRoomPin(e));
        builder.onEvent(TikTokConnectedEvent.class, (c, e) -> onConnected());
        builder.onEvent(TikTokDisconnectedEvent.class, (c, e) -> onDisconnected(e.getReason()));
        builder.onEvent(TikTokReconnectingEvent.class, (c, e) -> onReconnecting());
        builder.onEvent(TikTokErrorEvent.class, (c, e) -> onError(e));
        builder.onEvent(TikTokLiveEndedEvent.class, (c, e) -> onLiveEnded());
        builder.onEvent(TikTokLivePausedEvent.class, (c, e) -> notice("live pausada."));
        builder.onEvent(TikTokLiveUnpausedEvent.class, (c, e) -> notice("live retomada."));
        return builder.build();
    }

    private void configure(LiveClientSettings settings) {
        settings.setFetchGifts(true);
        settings.setRetryOnConnectionFailure(true);
        settings.setRetryConnectionTimeout(Duration.ofSeconds(2));
        settings.setPrintToConsole(false);
        settings.setLogLevel(Level.OFF);
        settings.setClientLanguage("pt-BR");
        String apiKey = System.getenv("TLM_TIKTOK_API_KEY");
        if (apiKey != null && !apiKey.isBlank()) {
            settings.setApiKey(apiKey);
        }
    }

    private void closeCurrent() {
        LiveClient current = client;
        if (current != null) {
            try {
                current.disconnect();
            } catch (RuntimeException ignored) {
                // Desconexão defensiva: a engine nova assume independente.
            }
        }
        client = null;
    }

    // ---------------------------------------------------------------- handlers

    private void onChat(TikTokCommentEvent event) {
        if (event.isVisibleToSender()) {
            return; // avisos de moderação visíveis só para quem enviou
        }
        ChatMessage message = TikTokEventMapper.chat(event);
        persist(() -> store.recordChat(currentLive, message.uniqueId(), message.nickname(),
                message.comment(), message.at()));
        publish(message);
    }

    private void onGift(TikTokGiftEvent event) {
        GiftEvent gift = TikTokEventMapper.gift(event);
        persist(() -> store.recordGift(currentLive, gift.uniqueId(), gift.nickname(),
                gift.giftName(), gift.count(), gift.at()));
        publish(gift);
    }

    private void onLike(TikTokLikeEvent event) {
        LikeEvent like = TikTokEventMapper.like(event);
        Integer total = event.getTotalLikes() > 0 ? event.getTotalLikes() : null;
        persist(() -> store.recordLike(currentLive, like.uniqueId(), like.nickname(),
                like.likeCount() == null ? 1 : like.likeCount(), total, like.at()));
        publish(like);
    }

    private void onFollow(TikTokFollowEvent event) {
        publish(TikTokEventMapper.follow(event));
    }

    private void onShare(TikTokShareEvent event) {
        FollowEvent share = TikTokEventMapper.share(event);
        persist(() -> store.recordShare(currentLive, share.uniqueId(), share.nickname(), share.at()));
        publish(share);
    }

    private void onRoomPin(TikTokRoomPinEvent event) {
        TikTokCommentEvent message = event.getPinnedMessage();
        if (message == null) {
            return;
        }
        String key = pinKey(event, message);
        if (key != null && !rememberPin(key)) {
            return; // pin (ou unpin do mesmo messageId) já processado
        }
        Instant at = event.getTimestamp() > 0
                ? Instant.ofEpochMilli(event.getTimestamp())
                : TikTokEventMapper.instantOf(event);
        PinnedCommentEvent pinned = TikTokEventMapper.pinned(message, at);
        Boolean follower = message.getUser() == null ? null : message.getUser().isFollower();
        boolean fresh = true;
        try {
            fresh = store.recordPinned(currentLive, pinned.uniqueId(), pinned.nickname(),
                    pinned.comment(), key, follower, at);
        } catch (MonitorException | RuntimeException e) {
            alert("banco local: " + e.getMessage());
        }
        if (fresh) {
            publish(pinned);
        }
    }

    private void onConnected() {
        connected = true;
        reconnectNoticeSent = false;
        lastDisconnectReason = "";
        publishState();
        notice("conectado à live de @" + currentLive);
    }

    private void onDisconnected(String reason) {
        connected = false;
        publishState();
        String detail = reason == null || reason.isBlank() || reason.equalsIgnoreCase("None")
                ? "" : " (" + reason + ")";
        if (lastDisconnectReason.equals(detail)) {
            return; // o loop de retry da biblioteca repete o mesmo motivo
        }
        lastDisconnectReason = detail;
        notice("desconectado" + detail);
    }

    private void onReconnecting() {
        if (reconnectNoticeSent) {
            return;
        }
        reconnectNoticeSent = true;
        notice("reconectando...");
    }

    private void onError(TikTokErrorEvent event) {
        Throwable error = event.getException();
        String message = error == null ? "desconhecido"
                : (error.getMessage() == null || error.getMessage().isBlank() ? error.toString() : error.getMessage());
        alert("erro do TikTok: " + message);
    }

    private void onLiveEnded() {
        connected = false;
        publishState();
        notice("live encerrada.");
    }

    // ---------------------------------------------------------------- helpers

    private static String pinKey(TikTokRoomPinEvent event, TikTokCommentEvent message) {
        if (event.getMessageId() > 0) {
            return "m:" + event.getMessageId();
        }
        if (event.getTimestamp() > 0) {
            String text = message.getText() == null ? "" : message.getText();
            return "t:" + event.getTimestamp() + ":" + text.hashCode();
        }
        return null;
    }

    private synchronized boolean rememberPin(String key) {
        if (processedPins.contains(key)) {
            return false;
        }
        processedPins.add(key);
        while (processedPins.size() > PIN_KEYS_MAX) {
            var iterator = processedPins.iterator();
            iterator.next();
            iterator.remove();
        }
        return true;
    }

    /** Grava no banco local; falhas viram alerta na UI, nunca derrubam a engine. */
    private void persist(StoreAction action) {
        try {
            action.run();
        } catch (MonitorException | RuntimeException e) {
            alert("banco local: " + e.getMessage());
        }
    }

    @FunctionalInterface
    private interface StoreAction {
        void run() throws MonitorException;
    }

    private void publish(DomainEvent event) {
        sink.onDomainEvent(event);
    }

    private void publishState() {
        sink.onStateChanged(new LiveMonitorState(connected, currentLive, ""));
    }

    private void notice(String message) {
        sink.onNotice(message);
    }

    private void alert(String message) {
        sink.onAlert(message);
    }
}
