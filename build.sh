#!/usr/bin/env bash
# 出一个自带界面的单文件可执行程序。
#
# 为什么要这个脚本：go:embed 不能跨 `..`，所以没法直接嵌 web/dist。
# 脚本负责把前端产物搬到 internal/webui/dist/ 再编译 —— 这样最终用户
# 只需要拿到一个 exe，不需要装 Node。
#
# 开发时不用跑这个：前端 `npx vite`（5173，会把 /api 代理到 23333），
# 后端 `go run ./cmd/server`。改哪边热更哪边。
set -euo pipefail
cd "$(dirname "$0")"

echo "==> 1/3 构建前端"
if [ ! -d web/node_modules ]; then
  echo "    web/node_modules 不存在，先装依赖"
  (cd web && npm install)
fi
(cd web && npx tsc -b --force && npx vite build)

echo "==> 2/3 把产物放进可 embed 的位置（internal/webui/dist）"
# 清掉上一轮产物：用 git clean 而不是 rm。
# 这个目录里除了 .gitkeep（入库的哨兵文件，保证全新 clone 也能编译）之外
# 全都是没入库的构建产物，而 git clean 天生只动未跟踪文件 —— 语义正好，
# 也不会让 .gitkeep 在 git 里显示成「已删除」制造假 diff。
git clean -fdxq internal/webui/dist
mkdir -p internal/webui/dist
cp -r web/dist/. internal/webui/dist/
echo "    文件数：$(find internal/webui/dist -type f | wc -l)"

echo "==> 3/3 编译守护进程"
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
OUT="bin/enderbox.exe"
[ "$(uname -s)" = "Linux" ] && OUT="bin/enderbox"
[ "$(uname -s)" = "Darwin" ] && OUT="bin/enderbox"
go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o "$OUT" ./cmd/server

echo
echo "完成：$OUT（版本 ${VERSION}，前端已内嵌）"
ls -lh "$OUT"
echo
echo "直接双击它、或者 ./$OUT 就会开服面板。"
echo "当系统服务跑的时候记得加 -open=false。"
