@echo off
call mvn -f monitor-cli/pom.xml package -DskipTests && java -jar monitor-cli/target/monitor-cli.jar
pause
