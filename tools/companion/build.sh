#!/usr/bin/env bash
# 构建 EnderBoxCompanion 插件 jar。
#
# 用法：bash tools/companion/build.sh
# 产物：internal/companion/EnderBoxCompanion.jar（Go 那边 go:embed 的就是它）
#
# 依赖（paper-api + adventure）会按需下载到 tools/companion/lib/，
# 不进版本库 —— 缺了就重新下，别把 2MB 第三方 jar 塞进 git。
#
# ⚠ 编译用 --release 17：产出的字节码在 Java 17 / 21 上都能跑。
# 别改成 21，服务端如果是 Java 17 会直接 UnsupportedClassVersionError。
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# ⚠ 全程用相对路径，不要拼 $HERE 的绝对路径。
# Git Bash 给 Windows 程序（javac.exe / jar.exe）传参时会做路径转换，
# /f/项目/... 会被转成 \f\项目\...（丢掉盘符），产物就跑到 F:/f/项目/... 去了 —— 踩过。
cd "$HERE"
SRC="src"
LIB="lib"
OUT="build"
JAR="../../internal/companion/EnderBoxCompanion.jar"

JAVAC="${JAVAC:-}"
if [ -z "$JAVAC" ]; then
  if [ -n "${JAVA_HOME:-}" ] && [ -x "$JAVA_HOME/bin/javac" ]; then
    JAVAC="$JAVA_HOME/bin/javac"
  else
    JAVAC="javac"
  fi
fi
JAR_BIN="$(dirname "$JAVAC")/jar"

mkdir -p "$LIB" "$OUT" "../../internal/companion"

fetch() { # url  dest
  if [ -s "$2" ]; then return 0; fi
  echo "下载 $(basename "$2") ..."
  curl -fsSL --retry 2 -o "$2" "$1"
}

MAVEN="https://repo1.maven.org/maven2"
fetch "$MAVEN/io/papermc/paper/paper-api/1.20.1-R0.1-SNAPSHOT/paper-api-1.20.1-R0.1-20230921.165944-178.jar" "$LIB/paper-api.jar"
fetch "$MAVEN/net/kyori/adventure-api/4.14.0/adventure-api-4.14.0.jar" "$LIB/adventure-api.jar"
fetch "$MAVEN/net/kyori/examination-api/1.3.0/examination-api-1.3.0.jar" "$LIB/examination-api.jar"
fetch "$MAVEN/net/kyori/adventure-key/4.14.0/adventure-key-4.14.0.jar" "$LIB/adventure-key.jar"

rm -rf "$OUT"
mkdir -p "$OUT"

CP="$LIB/paper-api.jar;$LIB/adventure-api.jar;$LIB/examination-api.jar;$LIB/adventure-key.jar"
"$JAVAC" --release 17 -nowarn -encoding UTF-8 -cp "$CP" -d build \
  src/io/enderbox/companion/Json.java \
  src/io/enderbox/companion/EnderBoxCompanion.java

"$JAR_BIN" --create --file "$JAR" \
  -C build . \
  -C src plugin.yml \
  -C src config.yml

echo "已生成 $JAR"
"$JAR_BIN" tf "$JAR"
