# EnderBox

Minecraft 开服器 / 全能型盒子。一个双击即用的单机·局域网面板：一键下载并管理各类服务端
（Paper / Spigot / Purpur / Leaves / Forge / Fabric / 混合端 Arclight·Cardboard·Mohist、及转换），
装模组与整合包、实时看玩家数据、自动备份、文件管理，再配一个桌面端壳。

> 愿景：不止开服，也要兼顾客户端需求，做成一体化的 Minecraft 盒子。

## 许可证

**GPL-3.0**（强 copyleft，衍生必须开源）。详见 [LICENSE](./LICENSE)。

## 构建

- 后端（Go，单 exe）：`bash build.sh` → `bin/enderbox.exe`
- 桌面端壳（Wails）：`bash build-desktop.sh` → `bin/enderbox-desktop.exe`
- 前端在 `web/`，由构建脚本打包并内嵌进 exe。

## 目录

- `cmd/server` 守护进程（面板后端 + 界面）
- `cmd/desktop` 桌面端壳（窗口 + 自动拉起守护进程）
- `internal/` 后端实现（daemon / api / fetch / modpack / companion / jobs …）
- `web/` React + TypeScript 前端
- `tools/companion` 自带 Bukkit 桥接插件（纯观察者，把服务端内存里的玩家数据写成文件给面板读）

## 进度

详见仓库内的规划文档（`docs/NEXT-STEPS.md`、`docs/UNFINISHED-WORK.md`、`docs/LONG-TASKS-INVENTORY.md`）。
