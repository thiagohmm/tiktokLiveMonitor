package com.tlm.cli.ui;

import org.junit.jupiter.api.Test;

import java.util.Optional;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

class CommandParserTest {

    @Test
    void connectTakesUsernameAndStripsAt() {
        CommandParser.Command command = CommandParser.parse(":connect @user3").orElseThrow();
        assertEquals(new CommandParser.Connect("user3"), command);
    }

    @Test
    void connectWithoutArgIsUnknownWithUsage() {
        CommandParser.Command command = CommandParser.parse(":connect").orElseThrow();
        assertTrue(command instanceof CommandParser.Unknown unknown && unknown.message().contains(":connect"));
    }

    @Test
    void disconnectWithoutArgTargetsCurrent() {
        CommandParser.Command command = CommandParser.parse(":disconnect").orElseThrow();
        assertEquals(new CommandParser.Disconnect(""), command);
    }

    @Test
    void disconnectWithUserKeepUsername() {
        CommandParser.Command command = CommandParser.parse(":disconnect user7").orElseThrow();
        assertEquals(new CommandParser.Disconnect("user7"), command);
    }

    @Test
    void filterParsesCategories() {
        assertEquals(new CommandParser.Filter(Category.CHAT), CommandParser.parse(":filter chat").orElseThrow());
        assertEquals(new CommandParser.Filter(Category.GIFT), CommandParser.parse(":filter gifts").orElseThrow());
        assertEquals(new CommandParser.Filter(null), CommandParser.parse(":filter all").orElseThrow());
    }

    @Test
    void filterInvalidBecomesUnknown() {
        CommandParser.Command command = CommandParser.parse(":filter nada").orElseThrow();
        assertTrue(command instanceof CommandParser.Unknown unknown && unknown.message().contains(":filter"));
    }

    @Test
    void filtersAcceptPinnedAliases() {
        assertEquals(new CommandParser.Filter(Category.PINNED), CommandParser.parse(":filter pinned").orElseThrow());
        assertEquals(new CommandParser.Filter(Category.PINNED), CommandParser.parse(":filter pin").orElseThrow());
        assertEquals(new CommandParser.Filter(Category.PINNED), CommandParser.parse(":filter fixados").orElseThrow());
        assertEquals(new CommandParser.Filter(Category.GIFT), CommandParser.parse(":filter gift").orElseThrow());
    }

    @Test
    void giftParsesAliasesAndArguments() {
        assertEquals(new CommandParser.Gift(""), CommandParser.parse(":gift").orElseThrow());
        assertEquals(new CommandParser.Gift(""), CommandParser.parse(":presentes").orElseThrow());
        assertEquals(new CommandParser.Gift("1,rosa"), CommandParser.parse(":gift 1,rosa").orElseThrow());
        assertEquals(new CommandParser.Gift("all"), CommandParser.parse(":gift all").orElseThrow());
        assertEquals(new CommandParser.Gift("Rosa, Dino"), CommandParser.parse(":gift Rosa, Dino").orElseThrow());
    }

    @Test
    void pinnedParsesAliasesAndListArgument() {
        assertEquals(new CommandParser.Pinned(""), CommandParser.parse(":pinned").orElseThrow());
        assertEquals(new CommandParser.Pinned(""), CommandParser.parse(":fixado").orElseThrow());
        assertEquals(new CommandParser.Pinned("list"), CommandParser.parse(":pinned list").orElseThrow());
        assertEquals(new CommandParser.Pinned("list"), CommandParser.parse(":fixados lista").orElseThrow());
    }

    @Test
    void pinnedWithInvalidArgumentIsUnknownWithUsage() {
        CommandParser.Command command = CommandParser.parse(":pinned ontem").orElseThrow();
        assertTrue(command instanceof CommandParser.Unknown unknown && unknown.message().contains(":pinned"));
    }

    @Test
    void simpleCommandsParse() {
        assertEquals(new CommandParser.Clear(), CommandParser.parse(":clear").orElseThrow());
        assertEquals(new CommandParser.Help(), CommandParser.parse(":help").orElseThrow());
        assertEquals(new CommandParser.Quit(), CommandParser.parse(":quit").orElseThrow());
        assertEquals(new CommandParser.Quit(), CommandParser.parse(":q").orElseThrow());
    }

    @Test
    void colonAloneShowsHelp() {
        assertEquals(new CommandParser.Help(), CommandParser.parse(":").orElseThrow());
    }

    @Test
    void blankLineParsesEmpty() {
        assertEquals(Optional.empty(), CommandParser.parse("   "));
    }

    @Test
    void freeTextIsUnknown() {
        CommandParser.Command command = CommandParser.parse("olá mundo").orElseThrow();
        assertTrue(command instanceof CommandParser.Unknown);
    }

    @Test
    void unknownCommandExplains() {
        CommandParser.Command command = CommandParser.parse(":foo bar").orElseThrow();
        AtomicReference<String> message = new AtomicReference<>();
        if (command instanceof CommandParser.Unknown unknown) {
            message.set(unknown.message());
        }
        assertTrue(message.get().contains("foo"));
        assertTrue(message.get().contains(":help"));
    }

    @Test
    void helpMentionsAllCommands() {
        String help = CommandParser.helpText();
        assertTrue(help.contains(":connect"));
        assertTrue(help.contains(":disconnect"));
        assertTrue(help.contains(":gift"));
        assertTrue(help.contains(":pinned"));
        assertTrue(help.contains(":filter"));
        assertTrue(help.contains(":clear"));
        assertTrue(help.contains(":help"));
        assertTrue(help.contains(":quit"));
    }
}
