package com.tlm.cli.infrastructure.tiktok;

import io.github.jwdeveloper.tiktok.messages.data.CommonMessageData;
import io.github.jwdeveloper.tiktok.messages.data.User;
import io.github.jwdeveloper.tiktok.messages.webcast.WebcastChatMessage;
import io.github.jwdeveloper.tiktok.messages.webcast.WebcastGiftMessage;
import io.github.jwdeveloper.tiktok.messages.webcast.WebcastLikeMessage;
import io.github.jwdeveloper.tiktok.messages.webcast.WebcastMemberMessage;
import io.github.jwdeveloper.tiktok.messages.webcast.WebcastRoomPinMessage;
import io.github.jwdeveloper.tiktok.messages.webcast.WebcastSocialMessage;
import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

class PinExtractorTest {

    private static User user(String nickname, String handle) {
        return User.newBuilder().setNickname(nickname).setUsername(handle).build();
    }

    @Test
    void extractsChatPin() {
        WebcastRoomPinMessage msg = WebcastRoomPinMessage.newBuilder()
                .setChatMessage(WebcastChatMessage.newBuilder()
                        .setUser(user("Rita", "rita123")).setContent("fixei!"))
                .setPinId(42)
                .setPinTime(1792286000000L)
                .build();
        var pin = PinExtractor.extract(msg).orElseThrow();
        assertEquals("rita123", pin.uniqueId());
        assertEquals("Rita", pin.nickname());
        assertEquals("fixei!", pin.comment());
        assertEquals("p:42", pin.key());
    }

    @Test
    void chatPinWithoutTextGetsPlaceholder() {
        WebcastRoomPinMessage msg = WebcastRoomPinMessage.newBuilder()
                .setChatMessage(WebcastChatMessage.newBuilder()
                        .setUser(user("Rita", "rita123")).setContent(""))
                .setPinTime(1792286000000L)
                .build();
        var pin = PinExtractor.extract(msg).orElseThrow();
        assertEquals("[sem texto identificado]", pin.comment());
    }

    @Test
    void extractsSocialGiftMemberLikePins() {
        var social = PinExtractor.extract(WebcastRoomPinMessage.newBuilder()
                .setSocialMessage(WebcastSocialMessage.newBuilder().setUser(user("Bob", "bob1")))
                .setPinId(1).build()).orElseThrow();
        assertEquals("bob1", social.uniqueId());
        assertEquals("compartilhou/seguiu a live", social.comment());

        var gift = PinExtractor.extract(WebcastRoomPinMessage.newBuilder()
                .setGiftMessage(WebcastGiftMessage.newBuilder()
                        .setUser(user("Rosa", "rosa2"))
                        .setGift(io.github.jwdeveloper.tiktok.messages.data.Gift.newBuilder()
                                .setName("Rosa")))
                .setPinId(2).build()).orElseThrow();
        assertEquals("rosa2", gift.uniqueId());
        assertEquals("Rosa", gift.comment());

        var member = PinExtractor.extract(WebcastRoomPinMessage.newBuilder()
                .setMemberMessage(WebcastMemberMessage.newBuilder()
                        .setUser(user("Cris", "cris3")).setActionDescription("entrou na live"))
                .setPinId(3).build()).orElseThrow();
        assertEquals("cris3", member.uniqueId());
        assertEquals("entrou na live", member.comment());

        var like = PinExtractor.extract(WebcastRoomPinMessage.newBuilder()
                .setLikeMessage(WebcastLikeMessage.newBuilder().setUser(user("Leo", "leo4")))
                .setPinId(4).build()).orElseThrow();
        assertEquals("leo4", like.uniqueId());
        assertEquals("curtiu a live", like.comment());
    }

    @Test
    void unpinIsIgnored() {
        WebcastRoomPinMessage byAction = WebcastRoomPinMessage.newBuilder()
                .setChatMessage(WebcastChatMessage.newBuilder()
                        .setUser(user("Rita", "rita123")).setContent("x"))
                .setAction(2).setPinId(9).build();
        assertTrue(PinExtractor.extract(byAction).isEmpty());

        WebcastRoomPinMessage byMethod = WebcastRoomPinMessage.newBuilder()
                .setChatMessage(WebcastChatMessage.newBuilder()
                        .setUser(user("Rita", "rita123")).setContent("x"))
                .setMethod("unpin").setPinId(10).build();
        assertTrue(PinExtractor.extract(byMethod).isEmpty());
    }

    @Test
    void dedupeKeyPrecedence() {
        // pin_id vence common.msgId
        WebcastRoomPinMessage both = WebcastRoomPinMessage.newBuilder()
                .setChatMessage(WebcastChatMessage.newBuilder()
                        .setUser(user("Rita", "rita123")).setContent("x"))
                .setCommon(CommonMessageData.newBuilder().setMsgId(777))
                .setPinId(55).build();
        assertEquals("p:55", PinExtractor.extract(both).orElseThrow().key());

        // sem pin_id, usa common.msgId (externo)
        WebcastRoomPinMessage msgId = WebcastRoomPinMessage.newBuilder()
                .setChatMessage(WebcastChatMessage.newBuilder()
                        .setUser(user("Rita", "rita123")).setContent("x"))
                .setCommon(CommonMessageData.newBuilder().setMsgId(777))
                .build();
        assertEquals("m:777", PinExtractor.extract(msgId).orElseThrow().key());

        // sem ambos, usa pinTime:texto
        WebcastRoomPinMessage fallback = WebcastRoomPinMessage.newBuilder()
                .setChatMessage(WebcastChatMessage.newBuilder()
                        .setUser(user("Rita", "rita123")).setContent("abc"))
                .setPinTime(1000L).build();
        assertEquals("t:1000:" + "abc".hashCode(), PinExtractor.extract(fallback).orElseThrow().key());
    }
}
