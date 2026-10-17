@echo off
rem Monitor LOCAL standalone: so Java (TUI conecta direto ao TikTok + SQLite).
rem Requer Java 21+ e Maven. Banco: .\data\tlm-local.db (ou --db / TLM_DB).
call mvn -f monitor-cli/pom.xml package -DskipTests && java -jar monitor-cli/target/monitor-cli.jar
pause
