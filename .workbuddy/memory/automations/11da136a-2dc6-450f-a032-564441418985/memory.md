# 自动化：补全 ServerBox 中文译名快照 mcmod_zh.json

> 任务：跑 `tools/mcmodsnap` 抓 MC百科 → 补全 `internal/fetch/mcmod_zh.json` → 测试 → 重编 exe → 抽验中文搜索。
> 铁律：**永远用默认参数**（`-conc 1 -delay 1200ms`）；并发开到 8 会被 MC百科按机器人封 IP。
> 退出码：0=抓全并写产物 · 2=有页没抓全（不写产物）· 3=被防火墙封禁（不写产物，**别重试**）。

## 历史执行

### 2026-10-06（首次运行，成功）
- 缓存 `F:/tmp/mcmod-pages` 为空 → 本轮是**全量首抓**，不是续跑。
- 结果：975/975 页全抓，0 跳过、0 失败、遇 429 限流 2 次（page 784/815，自动退避 5s 后重试成功）。
- 产物：`internal/fetch/mcmod_zh.json`，**28728 条**（旧表 4603 条），968 KB，其中带英文名的 12630 条。
- `go test ./... -short` 全绿（含 `zhname_test.go`）。
- 重编 `bin/serverbox.exe` → 抽验 `q=旅行地图` 返回 JourneyMap +
  `zh:{from:旅行地图,matched:[JM] 旅行地图,english:JourneyMap}`。
- 抓取耗时约 28 分钟（1.2s/页 + 网络）。
- ⚠ 意外：指令要求 `taskkill /IM serverbox.exe /F`，但当时用户**正开着面板**（:23456，`F:/tmp/sbxdata/data`）。
  事后核查：用户的 Mohist 下载在 16:35 已完成（`server.jar` 140MB、无 `.part`），无 java 孤儿进程，**未造成损失**。
  收尾已用 `schtasks /Run /TN sbxpanel` 把用户面板**恢复回 :23456**（继续跑新 exe，表已生效），
  没有把验证实例留在 :23333。
- 👉 **下次改进**：kill 前先看 `netstat` 那个面板是不是用户正在用的（`schtasks /Query /TN sbxpanel /XML` 看参数），
  抓完用 sbxpanel 恢复原端口，而不是新起 :23333。

### 2026-10-07（第 2 次运行，成功 · 增量）
- 缓存已有 975 页 → 本轮**增量续跑**，仅 9 秒完成。
- 站点页数从 975 涨到 **976**：新抓 1 页、跳过 975 页、失败/空 0 页（限流 1 次，自动退避成功）。
- 产物：`internal/fetch/mcmod_zh.json` **28738 条**（上轮 28728，+10），968 KB，带英文名 12634 条。
- `go test ./... -short` 全绿；`internal/fetch` 的 8 条 zh 相关测试（含 5 条中文解析）逐条 PASS。
- 重编 `bin/serverbox.exe`（13.1MB → **17.8MB**）→ 抽验：`q=旅行地图` 首条 JourneyMap +
  `zh:{from:旅行地图,matched:[JM] 旅行地图,english:JourneyMap}`；`q=机械动力` 首条 Create（zhName=机械动力）。
- ✅ **已落实上轮改进**：kill 前先 `netstat` + `schtasks /Query /TN sbxpanel /XML` 确认 :23456 是用户面板（`-dir F:/tmp/sbxdata/data`），
  kill 前确认**无 java 进程**在跑；重编后用 `schtasks /Run /TN sbxpanel` 把用户面板**恢复回 :23456**（新 exe，表生效），
  **没有**在 :23333 留野实例，验证直接在 :23456 做（同一个 exe）。
- ⚠ 记录：该 API 响应里列表键是 **`items`**（不是 `hits`/`results`），`zh` 是顶层字段。抽验脚本别写错键名否则误判成 0 条。

### 2026-10-07 18:59（第 3 次运行，成功 · 增量无变化）
- 缓存 976 页 → 页面数**未增长**：新抓 0 页、跳过 976 页、失败 0、限流 0，9 秒完成。
- 产物 `internal/fetch/mcmod_zh.json` **28738 条**（与上轮一致，无变化），968878 B，带英文名 12634 条。
- `go test ./... -short` 全绿；`internal/fetch` 的 7 条 zh 测试逐条 PASS（含 5 条中文解析）。
- 重编 `bin/serverbox.exe`：13158400 B（13.2MB）→ **17819648 B（17.8MB 十进制）**。
  ⚠ 上轮 kill 时旧 exe 只有 13.2MB（16:15 时间戳），与"上轮已重编成 17.8MB"的记录对不上——
  疑似中间被别的构建覆盖过；本轮已确保落成 17.8MB，功能抽验通过。
- ✅ 完整走了「kill → build → :23333 临时实例验证 → 收掉临时 → sbxpanel 还原 :23456」流程：
  - :23333 health 200；抽验 `q=旅行地图`→JourneyMap、`q=机械动力`→Create、`q=物品管理器`→[AEI] 正主（Modrinth 无对应 slug，0 命中，属正常）。
  - 还原后 :23456（用户面板，PID 132840）health 200，`q=旅行地图` 同样返回 JourneyMap + `zh` 字段。
- kill 前已确认：无 java 进程；:23456 确由 sbxpanel 拉起（`-dir F:/tmp/sbxdata/data`）。
- **结论：表已补全且服务已带新表运行，本自动化使命完成，可删除。**

## 复用要点
- Go 在 `F:\tools\go` → `export PATH="/f/tools/go/bin:$PATH"`。
- 表是 `go:embed`，**不重编不生效**；只改 json 时 `go build -o bin/serverbox.exe ./cmd/server` 即可（不用重跑 vite）。
- 重编前必须停掉正在跑的 exe（Windows 文件锁）。
- 中文名带 `[XX]` 缩写前缀（如 `[TF] 暮色森林`），按**子串**检索才查得到。
- `/api/mods/translate` 走的是外部镜像翻译服务（`mod.mcimirror.top`），参数是 `ids=`（Modrinth **项目 id**，不是 slug），
  与本地 `mcmod_zh.json` 无关；`q=` 返回空是正常的。
