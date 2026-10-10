#!/usr/bin/env bash
# 出桌面端窗口：bin/enderbox-desktop.exe
#
# 与 build.sh 的区别：那个出的是**守护进程**（自带界面，浏览器访问）；
# 这个出的是**窗口**，它本身不含界面资源 —— 窗口整个重定向到守护进程地址，
# 所以两边必须配套：改了前端只需要重新跑 build.sh，窗口不用动。
#
# 依赖 Wails CLI（`go install github.com/wailsapp/wails/v2/cmd/wails@latest`）。
# ⚠ 本机没有 gcc、CGO_ENABLED=0，而 Wails v2.16 在 Windows 上用的是纯 Go 的
# WebView2Loader，**不需要 CGO** —— 这条路实测能出 exe（见 docs/NEXT-STEPS.md 补记八）。
set -euo pipefail
cd "$(dirname "$0")"

WAILS="$(go env GOPATH)/bin/wails"
if [ ! -x "$WAILS" ] && ! command -v wails >/dev/null 2>&1; then
  echo "没找到 wails CLI。先装：go install github.com/wailsapp/wails/v2/cmd/wails@latest"
  exit 1
fi
WAILS="${WAILS}"
[ -x "$WAILS" ] || WAILS="wails"

echo "==> 编译桌面窗口"
cd cmd/desktop
# -s                跳过前端构建：窗口不嵌前端资源（界面在守护进程那边）
# -skipbindings     没有 JS 绑定要用
# -nosyncgomod      别去动主模块的 go.mod
# -skipembedcreate  不用生成 embed 资源
"$WAILS" build -s -skipbindings -nosyncgomod -skipembedcreate

cd ../..
cp cmd/desktop/build/bin/enderbox-desktop.exe bin/
echo
echo "完成：bin/enderbox-desktop.exe"
echo
echo "用法：双击它就行（它会自己拉起同目录的 enderbox.exe）。"
echo "  -data <目录>   指定守护进程的数据目录（默认用它自己的 ./data）"
echo "  -daemon <地址> 连另一台机器上的面板，例如 http://192.168.1.5:23456"
echo "  -auto-start=false  不自动拉起守护进程（自己先起好）"
echo
echo "⚠ 守护进程才是「管着服务器」的那个。实测确认：关掉窗口不会带走它。"
