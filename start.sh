#!/bin/bash
# Monitor LOCAL standalone: só Java (TUI conecta direto ao TikTok + SQLite).
# Requer Java 21+ e Maven. Banco: ./data/tlm-local.db (ou --db / TLM_DB).
mvn -f monitor-cli/pom.xml package -DskipTests && java -jar monitor-cli/target/monitor-cli.jar
