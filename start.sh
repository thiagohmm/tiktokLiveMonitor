#!/bin/bash
# Monitor LOCAL standalone: só Java (TUI conecta direto ao TikTok + SQLite).
# Requer Java 21+ e Maven. Banco: ./data/tlm-local.db (ou --db / TLM_DB).
set -euo pipefail

cd "$(dirname "$0")"

JAR="monitor-cli/target/monitor-cli.jar"

# Só recompila quando o jar não existe ou está mais velho que o código/pom.
if [ ! -f "$JAR" ] || [ -n "$(find monitor-cli/src monitor-cli/pom.xml -newer "$JAR" -print -quit)" ]; then
    mvn -f monitor-cli/pom.xml package -DskipTests
fi

JAVA_OPTS="${TLM_JAVA_OPTS:--Xmx256m}"

# A flag só existe no JDK 23+. Quando aceita, o Java sobe direto com ela e o
# JvmRelaunch não precisa criar um segundo processo.
if java --sun-misc-unsafe-memory-access=allow -version >/dev/null 2>&1; then
    exec java $JAVA_OPTS --sun-misc-unsafe-memory-access=allow -jar "$JAR" "$@"
else
    exec java $JAVA_OPTS -jar "$JAR" "$@"
fi