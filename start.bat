@echo off
rem Monitor LOCAL standalone: so Java (TUI conecta direto ao TikTok + SQLite).
rem Requer Java 21+ e Maven. Banco: .\data\tlm-local.db (ou --db / TLM_DB).
cd /d "%~dp0"
call mvn -f monitor-cli/pom.xml package -DskipTests
if errorlevel 1 (
  pause
  exit /b 1
)
set "JAVA_OPTS=-Xmx256m"
if not "%TLM_JAVA_OPTS%"=="" set "JAVA_OPTS=%TLM_JAVA_OPTS%"
rem A flag so existe no JDK 23+. Quando aceita, evita o relancamento do JVM.
java %JAVA_OPTS% --sun-misc-unsafe-memory-access=allow -version >nul 2>&1
if errorlevel 1 (
  java %JAVA_OPTS% -jar monitor-cli\target\monitor-cli.jar %*
) else (
  java %JAVA_OPTS% --sun-misc-unsafe-memory-access=allow -jar monitor-cli\target\monitor-cli.jar %*
)
pause
