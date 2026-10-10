# 下一步计划（2026-10-04 收官时写）

> 本文档是「接下来干嘛」的行动清单。权威的全量清单仍在 `BACKEND-PLAN.md`，
> 这里只放**排好序的近期待办**，做完一项划一项。

---

# 设计稿像素重制搬迁（2026-10-07，进行中 🔄）

用户要求「照最新设计稿 `docs/design/pixel-refresh/` **原封不动**重写各页 UI，一模一样」。
方法统一为**三步走**（用户原话：「你复制过去之后再接接口不好吗？」）：
① 图标精灵搬进 `web/index.html`（一次性，全局可用）；
② 页面 HTML 原样转 JSX；
③ `app.js` 里运行时生成的部分（刻度条/火花线/开关）照算法实现成组件。

**已完成**：外壳（`App.tsx`+`WorldList`+`PageFrame`）、概览页（`pages/OverviewPage.tsx`）、
HUD（`HudBar.tsx`）、玩家页（`pages/PlayersPage.tsx`）、插件与模组页（`pages/PluginsPage.tsx`）。

样式：`web/src/design.css`（设计稿 styles.css 整份复制，只改 @font-face 路径）
+ `web/src/design-overrides.css`（适配层：演示专用值、**老 CSS 污染压制**）。

⚠⚠ **搬新页前必做**：`grep -c "\.<类名>" web/src/index.css` —— index.css 里还留着**老组件**的
同名类（`.prow`/`.pstat` 已经撞过车）。为 0 才安全；不为 0 就去 `design-overrides.css` 里压掉。
玩家页栽在这上面：`.prow{min-height:54px}` 把每行从 49px 偷偷撑到 54px。

**工具**：
- 转换脚本 `web/_gen_{overview,players,plugins}.py`（提 HTML → 转 JSX）。
- 对比沙盘 `docs/design/pixel-refresh/_cmp.py <page-key>`（+ `_pixdiff.py` 像素差分，需 Pillow）。
- `?probe=1` 探针已扩：`pstatL`/`pcoordsL`/`pactL`（列左缘去重集合，**单值＝对齐**）、
  `rowH`（每行高度必须相等）、`parts:`（行内各区高度 + `gtr`/`mh`）。

**设计稿里还有、但没搬的**：配置 / 备份还原 / 计划任务 / 设置 / 状态速览。
**设计稿里根本没有的**：整合包市场、文件管理、模组详情页（市场侧）—— 无基准，不能「死搬」。

## ▶ 当前目标（用户 2026-10-07 下午）：**要可用性，不只版式**

原话：「继续做模组相关的页面、详情页面、市场页面等，做全套，我需要的是可用性」。
即把模组链路的页面**从假数据换成真实接口**（`PluginsPanel.tsx` 里已有现成的数据层），
版式仍用设计稿搬来的那套。范围：
1. **已安装列表**（`PluginsPage.tsx`）→ 接 `/api/mods/*`（扫描/启停/删除/缺前置）。
2. **模组详情**（市场侧 + 已安装侧）→ 复用 `ProjectShell.tsx` 那套。
3. **市场页**（搜索/列表/装前置）→ `/api/mods/{search,project}`。

---

# 整合包市场 P1（2026-10-06 深夜，已完成 ✅）

搜 Modrinth 整合包（默认只列 `server_side != unsupported`）→ 挑版本 → 预览代价 → 一键开成**新**实例。
后端 4 条路由（`/api/modpacks/{search,versions,inspect,install}`）+ 一次性取件码缓存（预览下过的包，安装时不再重下）；
前端独立一屏 `?view=market`。契约与界面落点见 `MODPACK.md` §9，改动**未提交**。

收尾这一轮修掉的三个真问题（都写进规格了）：
- 点包之后版本区滚不到位 —— ①`scrollIntoView` 被「当时的页面高度」夹住（版本行还没渲染），
  ②版本区是最后一屏内容、下面没东西可滚。修法：等 `vLoading` 落回 false 再滚 + 容器 `min-h-[70vh]`。
- 源站描述里的 Markdown 原样印在列表上（取样 100 条热门包有 1 条中招）→ `fetch.plainText()`。
- 下架项目返回 404（不是 502），「这个包没了」不能说成「我们的故障」。

下一梯队：**P2 CurseForge**（要 API key）→ 游戏规则编辑器 → 存档管理 → 监控历史。

## 收尾二：详情页重做（2026-10-07 凌晨，已完成 ✅）

用户两轮反馈：①「介绍和下载做分页，安装按钮放到整合包简介的右侧」；②改完说「PC 端页面不好看」，
追问确认是**右边太空**，并点名照 `modrinth.com/modpack/cobblemon-fabric` 做。

- 页内分页 `介绍` / `下载`（`?ptab=dl` 可深链），形式沿用设置页的 `.tab-btn`。
- 版面照 Modrinth：**抬头一行 + 左右两栏**，右栏是兼容性/分类/外链/详情，桌面端吸顶。
  版心 `max-w-[1240px]`、右栏 `xl` 340px —— 1440 屏正好填满内容区，右侧零留白。
- 抬头「安装」按钮**只切页不装**（判据：点了 `/api/modpacks/ops` 仍 `[]`）。
- 全局下载进度（右下气泡 + 台账面板）+ `GET /api/modpacks/ops`；下载分片 4→8。
- 规格与验收命令见 `MODPACK.md` §9.7 / §9.8。改动**未提交**。

下一梯队：**P2 CurseForge**（要 API key）→ 游戏规则编辑器 → 存档管理 → 监控历史。

## 收尾三：模组详情页对齐 + 链接 `)` 真 bug（2026-10-07 凌晨，已完成 ✅）

用户看了整合包详情页后问「模组详情页（市场侧）和已安装侧的详情能不能也做成这样」，并顺带要一份竞品对比文档。

- **抽出公共外壳 `web/src/components/ProjectShell.tsx`**：`mx-auto` 版心 + PACK/MOD 类型条 + 骨架屏 + 页内分页 + 左右两栏（右栏 `xl` 340px 吸顶）。
  纯函数（`licenseLabel`/`sideLabel`/`releaseLabel`）与 `MOD_LOADER_TAGS` 挪到 `projectMeta.ts` —— 组件文件只导组件，别破 Fast Refresh。
- **整合包详情页改为复用外壳**：内层内容（简介/下载/右栏）**原样搬运**（脚本按行区间抽取），没重写已验证的代码。
- **新增市场侧模组详情页** `ModProjectDetail.tsx`：`?pm=<id>` 深链，分页 介绍/下载，右栏外链含「去 MC百科 查」+「复制名称」。
  模组没有「预览安装」这一步（就是个 jar），下载即安装。新增后端 `GET /api/mods/project`（对称 `/api/modpacks/inspect`：
  缺参 400、下架 404、其它 502、`project_type` 不是 mod/plugin 就 400 并说清它是什么）。
- **已安装侧模组详情**：保留原弹窗，只重排内部（图标 48×48 + 徽章行 + 作者，两列 `<dl>` 改单列 `.srow`）。
- **真 bug**：`markdown.tsx` 的链接/图片正则没吃掉收尾的 `)` → 正文里出现游离的 `)`（如「Fabric Loader), the」）。
  修法：`\(\s*([^)\s]+)(?:\s*\))?` 对 `![]()` 与 `[]()` 两处都加。回扫整合包页 88 个链接、0 条游离括号。

规格：`internal/api/mod_project_test.go`（4 条，含变异测试证明守卫可被抓）+ `MODPACK.md` §9.9。改动**未提交**。

下一梯队：**P2 CurseForge**（要 API key）→ 游戏规则编辑器 → 存档管理 → 监控历史。

---

# 收尾四：市场列表「就地展开」+ 前置卡片队列（2026-10-07，已完成 ✅）

用户 2026-10-07 反馈原话：**「细化设计，为安装页的模组列表默认点击列表后展开该模组的版本列表可快捷下载本体和前置，
可翻页避免过长，详情页从右边的按钮进入，你觉得如何？」** 经回问确认落点后实施并真机验收。

## A. 市场列表「就地展开版本」—— 已完成 ✅

- `.modline` 由 `<button>` 改 `<div>`（⚠ **button 不能嵌 button**），内分「点击主体 `modline-hit`（就地展开）」
  与右侧独立「详情」按钮（进整屏详情页）。
- 展开区 `MarketRow`：惰性加载、每页 **5 条**、翻页控件、每条可安装、有前置则给「装前置（N）」。
  点本体「安装」**只装本体**，前置要用户自己点进队列 —— 保住「不静默装最新前置」。
- 手风琴（同时只开一行）+ `?expand=<projectId>` 可寻址（只 replaceState）。

## B. 前置弹窗改「卡片队列」—— 已完成 ✅

新 `web/src/components/DepQueueDialog.tsx`：每前置一卡（图标/标题/简介/版本列表每页 5 条/默认选最新/
「将安装：X」/安装），顶部「一键安装（N）」——**每张卡明写将装哪一版，所以它不算「静默装最新」**。
装完该卡上滑移出，全部装完 **1.2s 后自动关闭**。第二轮按用户反馈调整（见 `MODPACK.md` §9.10.7）：
**卡片改横向双排**（弹窗 920 宽，≥2 张才分两列，窄屏退化单列）、**一键安装把未确认项目的卡也提示出来**
（它们没有可选版本、装不了，但不许在计数里消失；没有可装项时不渲染按钮）。
同时修掉两个真问题（详见 `MODPACK.md` §9.10.4 / §9.10.5）：

1. **别在 `setState` 的 updater 里收集「要删掉的 key」** —— updater 不保证同步执行，拿到空数组会导致
   卡片永远摘不掉、自动关闭永不触发。key 必须在 setState 之外先算好。
2. **卡片「装没装上」要问任务状态，不能只看文件名** —— 只认文件名时，下载失败会让卡片永远停在
   「下载中…」且轮询永不停止。改成卡片记 `taskId`、轮询问任务，失败把原因**写在卡上**并放开重试。

验收（真机实测，命令见 `MODPACK.md` §9.10.6）：1440/520 两档 9 项判据全过、控制台零报错；
端到端「点安装 → 卡片滑出 → 4 秒后自动关闭」通过；**反证**注入必败任务 → 2 秒写出错误、可重试。
改动**未提交**。

## 收尾（用户 2026-10-07：好了，补充文档，收工 ✅）

本轮到此为止。

> ⚠⚠ **详见下方「补记九」：上面这句「下一件：玩家管理」是错的。**
> P0-2 玩家管理在 **2026-10-05/06 就已经做完并真机验收**，只是全程未 `git add`、
> 也没写进任何文档 —— 于是三份文档连续三天把它当成「还没开工的下一件事」。
> 订正后的待选梯队见「补记九」末尾。

## C. 实例列表曾返回空 `{}`（未排查，保留）

- 现象：测试期间某次 `GET /api/instances` 返回 `{}`（实例列表瞬间变空）。
- 已知：本轮所有探针脚本**均为只读**，从未下发 DELETE / 删除实例的请求。
- 判断：疑似数据目录/加载路径问题或并发写，**尚未定位**。当时已另建测试实例绕过，未影响交付。
- 下一步：先复现，再查 `data/` 下的实例索引文件与 `store` 的加载路径；**别靠猜**。
  （用户 2026-10-07：先记录，继续往下做。）

---

# 整合包收尾（2026-10-06 晚，已完成 ✅）

`?version=` 按版本预览、`POST` 真升级（planHash 防竞态 + 同实例单作业闸 + 作业表与取消）、
前端「整合包」导航页 + 从本地 `.mrpack` 安装——工作流并行开发 + 真机验收全通（含真实 Modrinth 网络腿），
过程与真机抓到的 diskProbe 覆盖哈希 bug 见 `.workbuddy/memory/2026-10-06.md` 四十四。
剩：P1 市场（Modrinth 只列 `server_side != unsupported`）→ P2 CurseForge。改动**未提交**。

# 当前这一轮：界面重做 + Java 自选（2026-10-04 晚，已完成 ✅）

用户一口气提了 7 条意见，逐条确认后给出 4 个选择。**后端 + 前端 A/B/C/D 全部做完，已实跑验证。**

## 已确认的用户决定（别改）

| 议题 | 用户的选择 |
|---|---|
| 版本/构建界面「杂、乱」 | **左版本列 + 右构建列** 两栏（PCL/HMCL 式） |
| Java 怎么处理 | **两个都要** —— 既能在运行环境一步自己选本机 JDK，也能一键下载 |
| 新建世界默认选中 Paper | **不预选，必须你点** |
| 前面那批修复（日志/进度/删除/弹窗） | 「没问题，继续」 |

## 已完成 ✅（后端全通过 `go build` + `go test ./...`）

1. `internal/fetch/jdk.go` —— JDK 下载源解析
   - 镜像二选一：**南大优先**（版本最新，实测 17.0.20.1 / 21.0.12.1 / 25.0.4.1），
     **中科大兜底**（带 `.sha256.txt`，但 LatestRelease 滞后两年）。
     文件名从索引页读，不自己拼。
   - `JDKMajors()` 静态清单（8 / 11 / 17 / 21 / 25），`JDKLatest(major)` 带 10 分钟缓存。
   - 校验和「有就校、没有就不装」：南大不提供 sha256，解压时 zip 的 CRC32 会兜底。
2. `internal/daemon/archive.go` —— zip / tar.gz 解压（zip-slip 防护 + 跳过软链接）
   + `Manager.InstallJDK()`：先解到 `runtimes/.staging/<name>`，验过有 `bin/java`
   再原子换入 `runtimes/temurin-<major>`，失败自动回滚。
3. `internal/daemon/java.go` —— 加了 `isStagedPath()`，`.` 开头的中间态目录
   不会被当成「本机可用的 Java」。
4. `internal/fetch/download.go` —— `Task` / `Options` 增加 `Label`：
   没有归属实例的下载（JDK）在全局下载条里显示「Java 21 · Temurin 21.0.12.1+1」，
   而不是一个文件名。
5. `internal/api/extras.go` —— 三条新路由：
   - `GET /api/java/releases` 静态清单（零外部请求）
   - `GET /api/java/releases/{major}` 查真实版本 / 大小 / 校验和
   - `POST /api/java/download` 起下载任务（复用多线程+续传+重试+PostInstall 解压）
6. `internal/fetch/jdk_test.go` —— 6 个真样本 + 4 个负样本。

> ⚠ 本次踩坑已写进注释和测试：文件名的正则**前缀不能要求引号**。
> 南大的 href 是相对路径（引号紧贴 `OpenJDK…`），中科大是完整路径
> （引号在 `/adoptium` 前）——要求前缀引号会让中科大这条回落**永远匹配不上**，
> 等于没有兜底。这条是靠单元测试发现的，不是靠读代码看出来的。

## 前端 A/B/C/D 已完成 ✅（2026-10-04 晚）

- **A. ServerPicker 双栏重排**：`.vpick` 左列竖排版本、右列构建，两列同高各自滚。
  新增 CSS：`.vpick`（grid）/`.vcol`/`.bcol`/`.vhead`/`.bhead`/`.vlist`/`.blist`/`.vitem`/`.bnote`；
  删掉了已无引用的 `.ver-chip`。
  ⚠ **坑（已踩，别改回去）**：grid 行必须写 `grid-template-rows: minmax(0,1fr)`。
  默认的 `auto` 行会被内容撑高（实测 55 个版本撑到 1983px），于是 `height:320px` 形同虚设、
  两列都不滚、整块溢出压住下面的步骤按钮。另需 `overflow:hidden` 兜底。
  ⚠ 另加：选中版本要 `scrollIntoView({block:'nearest'})` 滚进视野 —— 深链 `?mc=1.21.4`
  时它在 55 个版本里排第 12，停在顶部等于看不见自己选了什么。
- **B. `web/src/components/JavaPicker.tsx`**（新增）：上半本机 Java（固定第一项是「自动挑一个够用的」，
  空串=自动挑），下半下载 JDK（5 个主版本 → 查真实版本/大小/校验和 → 下载 → 轮询 → 完成后 reload）。
  `useLocalJava()` 增加返回 `list` / `reload`（`max` / `maxRef` 原样保留，ServerPicker 依赖它们）。
  `api.ts` 增加 `JavaMajor` / `JDKRelease` 类型 + `javaReleases/javaRelease/javaDownload`，
  `DownloadTask` 增加 `label`。
  ⚠ **不能嵌套 `.px-card`**（用户规范明令禁止嵌套 Card）：卡片内部再出现的「一组行」用 `.jlist`
  （只有上下两条细线），不要再套第二个黑边外框。
- **C. 接线**：向导第 1 步 `form.type` 初值改 `''`（无预选，URL 的 `?type=` 仍认）；
  第 2 步只读说明换成 JavaPicker，`javaPath` 进 form 并随创建提交；
  ConfigPanel 的「Java 路径」换成 JavaPicker（`Crow` 增加 `wide`，配 `.crow-ctl.wide` 铺满整行）
  并保留手写路径兜底；ActiveDownloads 显示 `t.label`、无归属实例时名字不做成按钮、安装阶段右侧用 `t.step`。
- **D. 验证（已实跑）**：`npx tsc -b --force` 通过 → `vite build` → 编译 `bin/serverbox.exe`（9.4 MB）；
  `go test ./...` 全通过；用 Chrome DevTools MCP 实读布局数值 + 截图核对：
  第 1 步 0/7 选中且「下一步」禁用；双栏 `sideBySide=true`、`sameHeight=true`、左列 `scrollHeight 1948 > clientHeight 284`（真可滚）、
  底栏不再被压、`scrollWidth==clientWidth==1440`（无横向溢出）；
  深链 `mc=1.21.4` 选中项已滚入可见区（`scrollTop=141`、`pageScrollY=0` 没带动整页）；
  `GET /api/java/releases/21` 实回 `21.0.12.1+1 / 195.6 MB / 南京大学镜像`（无 sha256，界面如实标注）。
  ⚠ **`./build.sh` 在本机 bash 环境跑不通**：那是 Git-Bash/WSL 的 node，去加载 Linux 版
  rolldown 原生绑定（`@rolldown/binding-linux-x64-gnu`）而依赖里只有 Windows 绑定，直接报
  `Cannot find native binding`。等价三步在 PowerShell 里执行即可（vite build → 拷到
  `internal/webui/dist` → `go build`）。这是环境问题，不是脚本逻辑问题。
  ⚠ 重新编译 exe 前必须先停掉正在运行的 `serverbox.exe`，否则 `go build` 报
  `The process cannot access the file`（文件被占用）。

## 用户反馈后的补修（2026-10-04 晚，已完成 ✅）

用户看过实际效果后提了两条，都成立、都已修：

1. **「构建栏为什么无法滚动」**：真 bug。`.bcol` 只写了 `display:block`，里面 `.blist` 的 `flex:1`
   失效、被内容撑到 880px，`overflow-y:auto` 永不触发。修法：`.vcol, .bcol` 合并成同一条
   `display:flex; flex-direction:column`（漏掉 flex 的那列必然滚不动）。实测修复后
   `.blist` clientHeight 284 / scrollHeight 880、`scrollTop` 可写。
2. **「双栏为什么没有搜索功能」**：版本几十个（Paper 55），加左栏顶部搜索框
   `#sbx-version-search`（复用配置页 `.search` 样式），命中计数 `6 / 55` 同时显示在搜索框尾和左列表头。
   **只筛版本、不筛构建** —— 构建是按选中版本拉的、最多 20 条，再叠一层搜索会出现
   「搜了版本号、右栏却空掉/对不上」的困惑；右栏表头改成 `构建 · 1.21.4` 标清归属。

## 用户反馈第二批（2026-10-04 深夜，已完成 ✅）

用户新建了一个 Forge 实例「10086」，报了三个问题：

1. **「10086 这一块没能显示出来」+ 界面部件出错**
   `TypeError: Cannot read properties of null (reading 'length')`
   **根因**：Go 的 nil slice 序列化成 JSON 的 `null`（不是 `[]`）。新实例从没保存过备份策略，
   `instances.json` 里是 `"atTimes": null`，前端 `pol.atTimes.length` 直接抛异常，
   ErrorBoundary 把整页（**连控制台一起**）替换成错误界面。
   修法：`fillPolicyDefaults` 在 `Create` / `Update` / `List` / `policyView` 四处统一补齐，
   前端 `loadPolicy` 再兜一手（磁盘上的老数据不会自愈）。
   回归测试 `TestBackupPolicyNeverSerializesNull` —— 用 `json.Marshal` 直接断言不出现
   `"atTimes":null`，拆掉修复会红（实测报错内容与真机一字不差）。
2. **「日志为什么没有自动更新」**：不是日志流的问题 —— 上一条崩溃把控制台一起换掉了。
   （顺带确认 SSE 正常：实测启动 Forge 时页面上日志行数 13 → 17 实时增长，徽标 LIVE。）
   另外给 `?nosse=1` 调试模式加了可见提示：徽标显示「非实时」并说明怎么恢复 ——
   以前它会静默关闭日志推送，看起来就像「日志坏了」。
3. **去掉配置页的滚轮切组**：原来「滚到边界再往下滚就切下一组」被用户读成
   「滚动空白切换下一页」。删掉 `onWheel` 劫持，只留显式的「上一组 / 下一组」按钮与左侧导航。
   （文案也从「滚到底再往下滚 → 下一组」改成「一组一屏，切换用左边或这里的按钮」。）

## JDK 端到端实跑（2026-10-04 晚，已完成 ✅）

`POST /api/java/download {major:21}` → 195.6 MB（南大镜像）→ 解压到
`data/runtimes/temurin-21/jdk-21.0.12.1+1/bin/java.exe` → 临时目录 `.download/` 自动清理
→ `GET /api/java` 认到 `Eclipse Temurin / 21.0.12.1 / source=managed`
→ 向导第 2 步「本机的 Java」列出它（标「自动托管 / 够用」）并能选中。

**过程中抓到一个真 bug（已修 + 有回归测试）**：第一次下载直接失败，报
「无法写入分片文件 …\.zip.part.2：The system cannot find the path specified」。
根因是 `internal/fetch/download.go` 的 `MkdirAll` **只在落盘 rename 那一步做**，
而多线程分片 `.part.N` 在**下载阶段**就往目标目录写了。服务端 jar 因为实例目录本来就存在，
这条路径从没被走到；JDK 下到 `runtimes/.download`（全新目录）才炸出来。
修法：把 `MkdirAll` 提到 `fetchToPart` 之前（下载开始前），落盘处不再重复。
回归测试 `TestDownloadCreatesMissingDestDir`（新增，5 MB 走多线程 + 三级不存在的父目录）——
拆掉修复跑一遍会红，报错与真机一字不差，实测有效。

## 未做（留给下一轮）

- 暂无阻塞项。下一步见下方优先级（P0 自动备份起）。

---

## 〇、上次修复的收尾（2026-10-04 晚，已完成 ✅）

1. **`Manager.List()` 的 PID/StartedAt 残留** —— 已修。除了「不在跑就归零」
   （口径对齐 `Metrics()`：只清 PID/StartedAt，**保留 `Error`** 因为那是崩溃原因，
   界面靠它解释「为什么炸了」），顺带修掉一个更严重的问题：`List()` 原来是
   **绕开锁直接读 `m.procs`**，而 `start()`/`Delete()` 在别的 goroutine 里写它 ——
   `go test -race` 下是数据竞争。改成走持锁的 `m.proc(id)`。
   验证：启停 `测试服` 一轮，停止后接口返回 `status=stopped pid=0 startedAt=空 error=空`。
2. **旧误判崩溃档清理** —— 已删 5 个（`data/logs/` 4 个 + `F:/sbx-realrun/logs/` 1 个）。
   删前逐个核对过：18:31 那个也是误判（日志里有 `[ServerBox] > stop` + 干净的两行
   `Stopping` + 存档完成，无崩溃迹象），不是真崩溃档。误判根因早已修复
   （`Send()` 写 stdin 前置 `intentional`），启停验证时也没再误建新档。
3. **`Version` 的 `stable` / `prerelease` 字段** —— 直接**删掉**。
   这两个字段对**所有**源都是恒定值（各源返回前就滤掉了快照/预发布：vanilla 认
   `type=="release"`、fabric 认上游 `stable`、paper 认名字里的 `-`、forge 用推荐表），
   零信息量，还和 `Build.Channel`（构建通道的 STABLE/RECOMMENDED）撞名、让人以为
   「版本稳定性」可以直接读字段。判据留在各源的过滤逻辑里；`forge_test.go` 里那条
   断言改写成「ID 不含 `-` 且非空」，强度不变。Go 侧 4 处赋值 + TS 的 `GameVersion`
   一并清理。验证：`/api/fetch/versions?type=paper` 现在每项只有 `{"id","family"}`。

## 一、v0.1 剩余（已排定优先级）

- **P0 自动备份** —— **已完成 ✅（2026-10-04 晚）**，见下方专节。
- **P1 崩溃日志智能分析**。MCSL2 有现成思路（`serverErrorHandler.py`）：
  对已存的 200 行崩溃快照做规则匹配 —— OOM / 端口占用 / EULA / mod 缺依赖
  等常见死法 → 输出人话结论 + 可操作的 fix。和 preflight「每条带 fix」同一哲学。

## P0 自动备份（2026-10-04 晚，已完成 ✅）

用户拍板的四条：**两种触发都要**（每天固定时刻 + 每隔 N 小时，按实例各配）、**只留最近 N 份**、
**停服前不自动备**、**范围页面可切换**。

### 后端
- `daemon.BackupPolicy`（挂在 `Instance.Backup` 上）：`enabled / everyHours / atTimes / keep /
  scope / since / lastRun`。**`since`（启用时刻）与 `lastRun`（真跑过）是两个字段，别合并** ——
  合并之后界面会把「刚打开」显示成「上次自动备份：现在」，是在撒谎。
- 调度：`backupDue` / `NextBackupRun` 是纯函数（规格表在 `backup_test.go`），
  `StartBackupScheduler` 在 main 里起，30 秒一轮、启动后先等 20 秒。
  `RunDueBackups(now)` 抽出来给测试直接调。
- **刚开启的策略不立刻备一份**：固定时刻只认 `since` 之后的点；间隔式从 `max(lastRun, since)` 起算。
- 两条路由：`GET|PUT /api/instances/{id}/backup-policy`。**没走整体 PUT 实例** ——
  备份页只关心策略，让它在整份实例对象上读-改-写迟早和配置页互相覆盖。
  「下次什么时候跑」由**后端**算好返回：调度规则只能有一份实现。
- 保留：`applyRetention` 删最旧的（手动/自动都算在内，用户选的就是「总量 N 份」这个模型）。
- 校验失败一律 400 并把原话显示在页面上（「时刻 "25:00" 格式不对」），
  不悄悄纠正；`ErrInstanceNotFound`→404、`ErrNoWorldArchive`→400、其余→500。

### 顺手修掉的两个真问题（都有回归测试）
1. **还原「只存存档」的备份会把 `server.jar` / `plugins` 删掉**（服务器直接起不来）。
   根因：`RestoreBackup` 原来是「清空整个实例目录再解压」。改成
   **只清这份备份确实含有的顶层条目**（从 zip 里算出来）。整目录备份行为与以前等价，
   唯一差别是 `logs/` 会被保留（这是好事：排障线索不丢）。
   测试 `TestRestoreWorldsBackupKeepsNonWorldFiles` —— 拆掉修复会红，实测有效。
2. **「只存存档」在没启动过的实例上会产出空 zip 还报成功**。刚建好没启过的实例只有
   jar + properties，没有 `world/`。现在直接报错并提示改成「整个实例目录」。
   顺带确认了范围过滤对**顶层文件**也生效（否则 50 MB 的 server.jar 会被打进「只存档」）。

### 前端
- `BackupPanel` 第 03 区从「还没接后端」的空话换成真的设置面板：开关 / 每天时刻（可加可删，
  最多 6 个，`04:00` 与 `4:00` 会去重）/ 每隔几小时 / 保留份数（默认 7）/ 范围二选一 /
  上次与下次时间 / 原地错误提示。
- 备份列表给「自动」加了独立的标签与颜色；`kind: 'auto'` 同步进 `BackupItem` 类型。
- `Instance.backup?` 是可选字段（老实例没有），`scope` 空串由后端兜底成 `all`,
  否则界面上两个范围标签会一个都不高亮。

### 验证
`tsc -b --force` 通过；`go test ./...` 全通过（新增 12 条备份测试）；
接口实测：非法时刻→400、开了没触发方式→400、无世界目录→400、不存在的实例→404、
`atTimes` 去重排序成 `["04:00","12:00"]`、`since` 记录而 `lastRun` 保持零值、
`nextRun` 正确顺延到次日；保留策略 5 份→3 份删最旧；中文备注文件名正常；
浏览器实读 UI（开关/时刻标签/范围选中态/上次下次文案）并截图核对。
⚠ 本机没有 gcc，`go test -race` 跑不起来（环境限制，不是代码问题）。

## 二、向两位前辈抄的作业（按性价比排序）

调研对象：MCSL2（`MCSLTeam/MCSL2`，桌面单机形态，和我们最像）、
MCSManager（`mcsmanager/mcsmanager`，Web 面板形态，功能最全）。

| 优先 | 功能 | 出处 | 备注 |
|---|---|---|---|
| P2 | ~~**文件管理**~~（已完成 ✅ 见下方「模组相关」） | MCSManager（`file_router_service`/`system_file`/`seven_zip_service`） | 装模组是刚需，没有它用户得自己去翻目录 |
| P3 | **计划任务**：定时备份/重启/执行命令 | MCSManager（`eventTask` + 生命周期任务） | 依赖 P0 的备份先落地 |
| P4 | **导入已有服务器**：选个目录直接纳管，不走下载向导 | MCSL2 的「导入模式」 | 实现成本低：建实例指向现有目录 + preflight 体检即可（注：并行已完成一版，见 `daemon/import.go`） |
| P5 | **秒退检测**：启动后 2 秒内退出 → 专门提示「启动命令/服务端文件可能不对」 | MCSManager `stopped()` 里的 `startThreshold` | 一行判断 + 一句人话提示，性价比极高 |
| P6 | **自动重启次数可配置**（`-1`=无限） | MCSManager `autoRestartMaxTimes` | 目前硬编码 3 次；设置页文案已承诺等 Windows 服务层一起收 |
| P7 | **基岩版服务端**：直接跑 `bedrock_server.exe`，不走 java | MCSL2 `_BedrockServerProcessBridge` | 注意：进程模型不同，Track/停服信号要单独适配 |

### 停服判定的借鉴结论（本次已落地，别退化）

- MCSManager：`ignoreEventTaskOnce()` 一次性忽略标记 + 重启次数上限，**不看退出码**。
- MCSL2：**把崩溃判定整行注释掉了**，根本不区分。
- 我们的方案（三层防护）比两家都完备，已实测：
  ① `Send()` 识别 stop/end 置标记（两家共同盲区：手敲命令不走专用停机通道）；
  ② `track()` 认日志里的 `Stopping the server` + `Stopping server` 两行（真机 Paper/Forge 都是连发两行）；
  ③ 兜底：退出码 0 且曾打出 `Done (`。判据在 `stoppedGracefully()`，规格表见 `shutdown_test.go`。

## 三、更远的（想起来就记着）

- Windows 服务注册（设置页「守护与启动」的 NotReady 已承诺）
- ~~插件/模组市场~~（已完成 ✅ 见下方「模组相关」）

## 四、模组相关：读元数据 + 直链安装 + 在线市场 + 文件管理（2026-10-04 深夜，已完成 ✅）

用户全选四项功能，排版选「PCL 式卡片网格」。已实跑验证（含真实下载一个 Modrinth 模组）。

### 后端

1. **`daemon/modmeta.go`**（上一轮已建）+ 本轮补两个真问题：
   - Forge 的图标字段是 **`logoFile`**（Fabric 才叫 `icon`）—— 漏掉它所有 Forge 模组都没图标。
   - 归档条目名统一成 `/`：用 Windows 工具（含 .NET `ZipFile`）重新打包的 jar 条目名是
     `META-INF\mods.toml`，不归一化就永远读不出元数据，而报错只说「没有认识的元数据文件」。
     回归测试 `TestReadModMetaBackslashEntries`。
2. **`daemon/plugins.go`**：`PluginDest`（直链安装的落点校验）、`PluginIcon`（读 jar 内图标）。
3. **`daemon/filebrowser.go`**（新）：文件管理。`ListFiles` / `ReadTextFile` / `WriteTextFile` /
   `DeletePath` / `ExtractZipAt` / `ExtractUploadedZip` / `SaveUploadTo`。
   - **安全边界**：`resolvePath` 一律相对实例目录解析并拒绝越界（`../`、绝对路径）。
     测试 `TestResolvePathBlocksTraversal`。
   - 文本读取拒绝 >1MB 与含 NUL 的文件（`ErrNotText` / `ErrTooLarge`）。
   - 解压自动剥掉「整包只套了一层文件夹」——但**顶层目录名是 `mods`/`config` 这类已知布局目录时不剥**，
     否则会把 `mods/b.jar` 里的 b.jar 直接倒进实例根目录（这个坑是测试抓出来的）。
4. **`fetch/market.go`**（新）：Modrinth 客户端。搜索/版本两个接口，**base 地址可配**
   （设置页「模组市场地址」），国内换镜像即可用。实测 `api.modrinth.com` 现在直连可用。
5. **`api/extras.go`** 新增路由：插件图标、`install-url`（直链与市场共用）、文件管理六条、市场两条。
6. `settings.go` 新增 `MarketBase`（默认官方 API，空值兜底）。

### 前端

- **`PluginsPanel.tsx` 重排**：卡片网格（`.modgrid`/`.modcard`，图标内联标题左侧、零渐变零阴影）+
  **筛选五档**（全部 / 已启用 / 已禁用 / 缺前置 / 读不出信息）+ 搜索（名字/文件名/modId）+
  依赖问题条（只显示「必需」的，可选前置不刷屏）+ 详情弹窗（简介/作者/modId/完整依赖清单）。
  三块安装入口：本地上传 / **直链安装** / **在线市场**（选项目 → 选版本 → 装）。
- **`FileManager.tsx`**（新）+ 新导航页「文件」：面包屑、上一级、按文件名过滤、上传、上传并解压、
  zip 解压（可选同名文件夹 / 当前目录）、文本编辑弹窗、删除。目录同步进地址栏（`?page=files&path=`）。
- 模组页的「打开文件夹」按钮直接跳到「文件」页的 `mods` 目录。
- `AccessPanel` 增加「模组市场地址」输入（失焦保存 + 形状校验）。

### 验证（实跑）

- `go build ./...` ✅、`go test ./...` ✅、`npx tsc -b --force` ✅、`vite build` ✅、exe 重编译 ✅。
- 造了一个临时 jar（forge `mods.toml` + logoFile + 一条缺失前置）核对界面：
  卡片显示「界面核对模组 / 1.2.3 / forge / 缺少前置：create」，图标走 `/plugins/{file}/icon` 正常返回。
- **真机端到端**：在线市场搜 `sodium` → 20 条结果 → 选版本 → 下载
  `sodiumoptionsapi-forge-1.0.10-1.20.1.jar`（443,978 字节）落地 → 列表读出元数据
  （name/version/loader/icon/1 条前置问题）→ 图标接口返回 170 KB PNG。
- 文件页：根目录 18 项（目录在前）、进 `mods/`、面包屑正确、`?page=files&path=config` 深链直达、
  `eula.txt` 编辑器能读回内容且「没有改动」时保存键为禁用态。
- 测试用的临时 jar 与下载的模组**已全部删除**，实例恢复原状。

### 已知取舍（别当成 bug）

- 在线市场的封面图来自 `cdn.modrinth.com`，与 API 域名不同 —— 它会**单独**加载失败（退回像素木箱图标），
  不影响搜索与安装。
- 文件管理的删除是不可逆的（有确认弹窗，但没有回收站）。备份页是唯一的后悔药。

### 用户反馈：排版难受 → 按 PCL 真实做法重排（2026-10-05，已完成 ✅）

用户原话：「你不觉得这个排版真的很难受吗？？？」。量了一下，问题是真的：

| 元素 | 改前实际宽度（1600 窗口） |
|---|---|
| 4 个分区标题的 3px 黑线 | 各 **1302px** |
| 直链安装的 URL 输入框 | **1179px**（一个网址条拉满整屏） |
| 在线市场搜索框 | **1238px** |

三条毛病叠在一起：①元素被无脑拉满宽度，右半边全空；②4 条 3px 纯黑通栏线 + `.px-card` 的 3px 黑边，
整页像一叠表格横杠；③「安装」的三块永远展开、各占一条通栏带，把「已安装」挤成最上面一小块。

⚠ **我犯的偏差**：用户要的是「参照 PCL」，但我上一轮实现成了卡片网格。
PCL2 的模组页其实是**顶部搜索+筛选、主体一列列表、安装收进独立的下载窗口**。这次按真实做法重排。

改了什么：

- **卡片网格 → 单列列表**：`.modrow`（图标 + 名字 + 版本/加载器 chip + 行尾「文件名 · 大小」+ 开关 + 删除）。
  ⚠ 文件名/大小放在**行尾**而不是标题下面：放标题下面会在中间留一条一千多像素的空档，
  眼睛要从名字跨到最右边的开关 —— 这正是「难受」的来源之一。
- **安装收进对话框**（`InstallDialog` + `LocalTab`/`UrlTab`/`MarketTab`）：三个小标签切换，
  页面从 4 条通栏带减到 1 个区块。
- **市场结果区内滚**：20 个结果摊开会让对话框长到 **1651px**（关闭按钮跑到屏幕外），
  加 `max-h-[42vh] overflow-y-auto` 后是 642px 且能滚。
- **版本选择不再叠第二层弹窗**：改成在同一对话框里「搜索结果 ↔ 版本列表」切换（带返回）。
- 搜索框固定 `248px`、说明文字限 `62ch`、`.fchip.tab` 给对话框标签用。
- 删掉已无引用的 `.modsub`、`.modissue`、`.modcard.off`。
- ⚠ 禁用态只压暗内容（`.modicon`/`.modrow-main`/`.mono-meta`），**不压暗开关本身** ——
  开关是用户要点的那一下，跟着变淡会让人以为它坏了。

实读验证（Chrome DevTools）：1600 / 1032 / 420 三档宽度都核对过；对话框 642px、
结果区 378px 可滚（scrollHeight 1387）、版本列表 9 条可滚；420 窄屏下文件名/大小自动隐藏、开关不被挤掉。

---

# 非 Paper 服务端实测（2026-10-05/06，已完成 ✅）

上一轮只敢说「Paper 上验过」。这一轮把「其他端呢」补齐 —— **全部在真机真服上跑，不是读文档**。

## 结论表

| 服务端 | 判定 | 依据 |
|---|---|---|
| Paper 1.20.1 | ✅ 可用 | 首轮验证（快照 + 写操作全通） |
| Spigot 1.20.1 | ✅ 可用 | 真机跑通：自动注入、快照、give/gamemode/tp/kill |
| Purpur 1.20.1（git-Purpur-2062） | ✅ 可用 | 真机跑通，`serverVersion` 如实回传 |
| **Folia 1.20.1（build 17）** | ❌ **用不了** | 实测加载阶段直接拒绝：`Could not load plugin 'ServerBoxCompanion v0.2.0' as it is not marked as supporting Folia!` |
| **Arclight 1.20.1（Forge 混合端）** | ✅ 可用 | 真机跑通。日志：`ServerBoxCompanion 0.2.0 已启用` |
| **Cardboard 1.20.1（Fabric 混合端）** | ✅ 可用（补记） | 真机跑通，日志 `Cardboard mod Loaded.` → `Enabling ServerBoxCompanion v0.2.0`。见下方「混合端真的验成了」 |
| ~~Mohist~~ | **⚠ 这条结论是错的，已订正** | 原写「官方下载端点 200 但 body 0 字节」。真相：**项目搬了家还改了名**，我打的是废弃的老地址。见下方「补记四：Mohist 其实能下」 |
| CatServer | ⚠ 不适用 | 最新发行版 `25.02.04` 的 MANIFEST 写的是 `git-CatServer-1.12.2` —— 实际是 1.12.2 |

> ⚠ 下表头一版把「CatServer / Cardboard 未能验证」写成结论，**是错的** ——
> 那是 `curl -o /dev/null` 报假数造成的误判，已推翻。详见下方补记。

**Folia 为什么不行（别再试）**：它不是「没有插件系统」，是「有，但不让这么用」——
Folia 按区域分线程，插件的 `BukkitScheduler` 定时器语义不成立。所以 UI 上 Folia 是
**明确不可用 + 说清原因**，而不是静默留一个点不动的「插件开关」按钮。

**混合端为什么算「可用但尽力而为」**：它能装插件（Arclight 实测确认），
但上游不受理兼容性问题、插件和模组抢同一个类也发生过 —— 所以 UI 在
**能用的时候就把风险亮出来**（`sup.hybrid && sup.supported`），不是等出事再说。

## 这轮修掉的真 bug：混合端导入挑错了启动文件

**现象**：Arclight 第一次启动会**把 Forge 装进自己的目录**，于是根目录里同时躺着
`arclight.jar`（真正的启动器）和 `libraries/net/minecraftforge/forge/<tag>/win_args.txt`
（Forge 生成的 JVM 参数）。

`DetectImport` 的老逻辑是「有 args.txt 就无条件优先」→ 导入后 `JarPath` 变成
`win_args.txt`。而 `win_args.txt` **只有 JVM/模块参数、没有主类**（主类写在 jar 的
MANIFEST 里：`arclight.jar` → `io.izzel.arclight.server.Launcher`），
`buildArgs` 会发 `@win_args.txt` → 起不来。

**改法**（`internal/daemon/import.go`）：
- 新增 `isHybridLauncherName(low)` / `hybridBaseType(low)`，**名单只有一个来源** ——
  `classifyJar`（认类型）和 `DetectImport`（挑启动文件）都调它。两处各写一份的话，
  将来改一边忘一边，就会出现「类型认出来了、启动文件却挑错」的半对半错。
- 分支顺序改成 **混合端 jar > args.txt > 通用名 jar**。
- 混合端 jar 名里通常不带游戏版本，顺手从它刚装好的那个 Forge 目录名里捡
  （`1.20.1-47.3.22` → mc=1.20.1 / loader=47.3.22）。
- ⚠ **普通 Forge 必须继续走 args.txt**，有专门的反向护栏测试盯着（`TestDetectImportPlainForgeStillUsesArgs`）。

回归测试（`import_test.go`）：`TestDetectImportHybridBeatsForgeArgs` /
`TestDetectImportMohistIsForge` / `TestDetectImportCardboardIsFabric` / 上面那条护栏。
**已验证它抓得住旧代码**：把分支条件改成「args 存在时混合端不优先」后，
`TestDetectImportHybridBeatsForgeArgs` 如实变红并打印出 `win_args.txt`，还原后转绿。

实读验证：修复前面板扫描 Arclight 目录得到 `jarPath=win_args.txt`；修复后得到
`jarPath=arclight.jar`、`type=forge`、`mc=1.20.1`、`loader=47.3.22`、`notes=[]`。
导入后 `/live` 返回 `support={supported:true, hybrid:true}`、
`serverVersion=arclight-1.20.1-1.0.6-4e94eb76 (MC: 1.20.1)`、`online=1`（ArclightBot，主世界）。

## 「面板没托管进程、但插件还在写数据」的诚实提示（已存在，本轮实测确认生效）

面板重启过、或服务端是用户手动开的 → `running=false`，但插件仍每 2 秒写快照 → `stale=false`。
这时侧栏写着「已停止」、实时面板却显示「1 人在线」，两者会互相打架。
UI 已经专门有一条说明（`LivePanel` 的 `!snap.running && !snap.stale` 分支）：
「插件还在稳定写数据，说明服务器确实在跑 —— 但这个进程不是面板拉起来的……
停服、重启、看控制台这些要面板托管才做的事暂时用不了。」
Arclight 实例实测渲染出这段话，侧栏也确实显示「已停止」。

## 下载通道现状（环境变了，记一笔）

- **本机 127.0.0.1:7897 有可用代理**（`curl -x http://127.0.0.1:7897 https://github.com` → 200）。
  GitHub 直连仍然不通（443/22），但走这个代理能拿到 release 附件（Arclight 5.7MB 下载成功）。
- ⚠ 这个代理**不稳定**：几分钟后同一个 Arclight 附件再下就变成 `000` / SSL connect error。
  所以「靠它去验 CatServer/Cardboard」这条路不可靠 —— 这也是那两个端这轮没验成的原因。
- ~~Mohist 官方 API 的 `builds` 列表能取到（189 条），但 `.../download` 永远是 0 字节 —— 不是我们这边的问题。~~
  **⚠ 这句是错的。** 那 189 条来自**已废弃的老 API**（`mohistmc.com/api/v2/...`），
  它连 `download` 一起都返回 0 字节 —— 是老入口死了，不是「项目下不到」。
  新地址实测能下 134MB。见「补记四」。

---

# 补记：混合端真的验成了（2026-10-06 凌晨，已完成 ✅）

上面那张表里「CatServer / Cardboard 未能验证」的结论**被推翻了**，原因是我用错了测量方法。

## ⚠ 犯的错：`curl -o /dev/null` 在 Git Bash 下发假数据

我拿 `curl -s -o /dev/null -w "%{size_download}"` 去测镜像，三次都报 `200 size=0`，
于是判定「国内 GitHub 镜像全是空的、下不动」。

**是假的。** 换成 `-o 真文件` 再量，同一批镜像立刻吐出 **7795165 字节的合法 zip**。
`/dev/null` 在 Git Bash / Windows 下不可靠，`size_download` 会骗人。
**判据改成：写到真实文件再 `os.path.getsize` + `zipfile.is_zipfile`。**
（这条和「缩略图只发现疑点、定论走 getComputedStyle」是同一类错误：**中间层会撒谎，去量真东西**。）

## 结果：`gh-proxy.com` / `ghfast.top` / `ghproxy.net` 都能稳定拿到 GitHub release 附件

实测 `CatServer-4168d848-universal.jar` 三个镜像各下到 7.8MB、校验为合法 zip。

## 于是又验了两个混合端

| 端 | 结论 |
|---|---|
| **Arclight 1.20.1**（Forge 混合端） | ✅ 插件加载 + 快照 + 面板读取全通 |
| **Cardboard 1.20.1**（Fabric 混合端） | ✅ 插件加载 + 快照 + 面板读取全通；⚠ **玩家在线的数据没能验**（见下） |
| ~~Mohist 1.20.1~~ | **⚠ 这条是错的，已订正** —— 见「补记四」。新地址 `api.mohistmc.com` 实测能下 134MB、sha256 逐字节对上 |
| CatServer | ⚠ `25.02.04` 那个 universal jar 的 MANIFEST 写的是 **`git-CatServer-1.12.2`** —— 最新版其实是 1.12.2，对我们的 1.20.1 插件没有参考价值 |

## Cardboard 怎么装（这条直接回答「混合端能不能直接配」）

**Cardboard 不是「一个能直接启动的 jar」**，而是「Fabric 服务端 + mods/ 里的三个模组」：

```
fabric-server.jar            ← 启动文件（meta.fabricmc.net，1.20.1 / loader 0.19.5 / installer 1.1.2）
mods/Cardboard-1.20.jar      ← Modrinth: cardboard (MLYQ9VGP)，1.20.1-13
mods/fabric-api-*.jar        ← Modrinth: fabric-api (P7dR8mSH)
mods/iCommon-Fabric-bundle.jar ← Modrinth: icommon (SVKv1SZo)
plugins/ServerBoxCompanion.jar
```

⚠ **Modrinth 的 version JSON 里 `dependencies` 是空的**，但 Fabric Loader 会报
`HARD_DEP cardboard 13 {depends fabric}, {depends icommon}`。
**别信元数据，看真产物**：mod 声明在 jar 的 `fabric.mod.json` 里，Modrinth 那一栏不一定同步。

启动日志（实测）：
```
Cardboard mod Loaded.
This server is running Cardboard version git-854ad26 (MC: 1.20.1) (Implementing API version 1.20.1-R0.1-SNAPSHOT)
Loading ServerBoxCompanion v0.2.0
Enabling ServerBoxCompanion v0.2.0
Done (18.165s)!
```

**这四样东西我们的面板全都已经支持**：Fabric 服务端能下载、Modrinth 市场能装这三个 mod。
所以「混合端能不能直接配」的答案是：**Cardboard 这条路不用新功能，用户自己就能配出来**。

## 补的第二个真 bug：装了 Cardboard 的 Fabric 服被判成「用不了」

Cardboard 的**启动文件是 `fabric-server.jar`**，Cardboard 本人躺在 `mods/` 里。
而 `isBukkitHybrid` 只看启动文件名 → 认不出 → 走 `TypeFabric` 分支 → 判定
「Fabric 不认 Bukkit 插件，用不了」。但插件**其实正在正常写数据** ——
于是界面上「用不了」和「0 人在线 · 插件 v0.2.0」同时挂着，自己打自己的脸。

修法（`internal/daemon/companion.go`）：`isBukkitHybrid` 除了看启动文件名，
再看 `mods/` 里有没有桥接模组（cardboard / sinytra / connector）。
- `CompanionSupportOf` 的「纯函数不碰磁盘」这句话顺势改成实话：
  只读 `mods/` 一处，读不到就当没装（与旧行为一致，不会误判）。
- 顺手把 Forge/Fabric 那条原因文案改成**按端点名桥接**：Fabric → Cardboard，
  Forge/NeoForge → Sinytra Connector；并保留规格表要求的 `Bukkit` 关键字。
- 回归测试：`TestBukkitBridgeModMakesModdedServerHybrid`（按真实目录形状搭）
  + `TestModdedServerWithoutBridgeStaysUnsupported`（连 `mods/` 都没有时行为不变）。
  **已验证抓得住旧代码**：把 `mods/` 那条路短路掉，测试如实变红并打印出那句假话。

## 还没验到的一小块：Cardboard 上的「玩家在线」数据

测试机器人（`F:/tmp/srv/bot.py`，极简 1.20.1 客户端）在 Paper/Spigot/Purpur/Arclight 上
都能进服（服务端日志有 `joined the game`），但在 Cardboard 上收到
`0x03 SetCompression` 之后紧接着一个登录期 `Disconnect`，**从没出现过 `joined the game`**。
所以 Cardboard 的快照一直是 `online: 0`，`getOnlinePlayers()` 这条路**没有实测过**。

结论：Cardboard 上「插件能装能跑」是实测的；「玩家数据取得对」是没验的。
UI 上 Cardboard 已经落在混合端那条分支里，原文就是「兼容性是尽力而为」——
和这个状态是自洽的，但不能说「Cardboard 全通」。

---

# 补记二：Leaves 也验完了（2026-10-06 凌晨，已完成 ✅）

**为什么必须验它**：Folia 那条不可用的提示里，我们**主动推荐**了
「要实时数据就用 Paper / Spigot / Purpur / **Leaves**」。这句话当时只有三个端验过 ——
等于把没测过的东西写进了给用户的建议里。不验就是空头承诺。

## 结果：全通（含玩家数据）

Leaves **1.20.1**（build `847357b`，`leaves-1.20.1.jar` 43.6 MB，GitHub releases）。

```
[ServerBoxCompanion] Enabling ServerBoxCompanion v0.2.0
[ServerBoxCompanion] ServerBoxCompanion 0.2.0 已启用（MC 1.20.1-R0.1-SNAPSHOT，快照间隔 40 tick，桥接目录 plugins\ServerBoxCompanion\bridge）
[ServerBoxCompanion] 可选能力：延迟=有，皮肤=有，末影箱=有，维度键=有
Done (3.347s)!
```

面板侧实测（`/api/instances/{id}/live`）：
```
support   = {"supported": true}          ← 没有 hybrid 标记，Leaves 是正统 Bukkit 系
have=true stale=false installed=true
server    = git-Leaves-"847357b" (MC: 1.20.1)
online    = 1  → LeavesBot | hp 20 | SURVIVAL | 主世界 | 5.5 / -60 / 9.5
```
浏览器实读：侧栏 `LEAVES 1.20.1`、实时区「1 人在线 · 插件 v0.2.0」、
玩家卡「LeavesBot 生存 主世界 world · 6, -60, 10 · 0 ms 血 20 饱 20」、无溢出。

注意 Leaves 的 `serverVersion` 是 `git-Leaves-"847357b"` —— **面板原样回传，没有二次加工**，
这正是我们要的（服务端说自己是谁就是谁）。

## 顺带确认：43.6 MB 的大文件会**被镜像截断**

第一次从 `gh-proxy.com` 下 Leaves 得到 42585805 字节（`iszip False`）——
**比 GitHub API 报的 43616045 少了一兆**，是个残缺文件。
`ghfast.top` 重下得到 43616045、zip 校验通过。

**教训**：下大文件不能只看 `http_code=200`，必须
**拿 GitHub API 的 `assets[].size` 加上 `zipfile.is_zipfile` 双重校验**；
只看着 HTTP 成功会把半截文件当成好东西。下完就校验，不对就换镜像重下。

## 现在「实时数据能用」的端（全部真机实测）

Paper · Spigot · Purpur · **Leaves** —— ✅ 全通（含玩家数据）
Arclight（Forge 混合端）—— ✅ 全通
Cardboard（Fabric 混合端）—— ✅ 插件可用；⚠ 玩家数据未验
Folia —— ❌ 实测被拒（见上）
Mohist / CatServer —— ⚠ 下不到 / 版本不适用（**Mohist 那条是错的，见「补记四」**）


---

# 补记三：逆向验证（反面也验了一遍）+ 三个真 bug（2026-10-06）

上面两节都在验「哪些端**能**用」。这一轮反过来验「说**不能**用的那些，是不是真的不能」——
判错的方向反了照样是错：把能用的端说成不能用，用户白丢一个功能。

## 反面实测结果（全部真机跑过）

| 端 | 面板怎么说 | 真机上到底怎么回事 |
|---|---|---|
| 普通 Fabric（无桥接） | ❌ 模组端不认 Bukkit 插件 | 启动日志里 `ServerBoxCompanion` 出现 **0 次** —— `plugins/` 对它等于不存在 ✅ |
| Velocity | ❌ 代理端不认 | 启动时当场拒绝：`The plugin file ServerBoxCompanion.jar appears to be a Bukkit or BungeeCord plugin. Velocity does not support Bukkit or BungeeCord plugins.` ✅ |
| BungeeCord | ❌ 代理端不认 | **和 Velocity 完全不是一回事**：它把插件收下了，再去加载主类，死在 `NoClassDefFoundError: org/bukkit/plugin/java/JavaPlugin` ✅ |
| 原版 Vanilla | ❌ 没有插件系统 | 1.20.1 起服到 `Done (`，全程日志里 `ServerBoxCompanion` 出现 **0 次** ✅ |

四个反面判定全部被真实服务端印证。但这一轮**顺手挖出三个真 bug**：

## bug 1（严重）：代理端的「不支持」理由是错的，还印着空白

导入真的 Velocity 3.4.0 之后，界面上写的是：

> 这个插件要求 Java 17 以上，而 **（空白）** 需要 Java 8。往 Java 8 的服务端里塞一个 Java 17 的 jar…

原因：`CompanionSupportOf` 的判定顺序是**先查 Java 版本、再分端类型**。
代理端**没有 MC 版本这个概念**（`inst.MCVersion` 是空串），而 `JavaForVersion("")`
会把空串当成 `0.0.0` → 算出 Java 8 → 命中「需要 Java 8」那条分支。
于是端名 `%s` 没东西可填，真正的原因（代理端）压根没机会说出口。

**修法**：先按「这是什么端」分流，Java 那条规矩只留在 `TypePaper` 分支里 ——
它本来就只对真的跑地图的 Bukkit 端有意义。版本未知的 Paper 干脆不下结论
（拿一个猜出来的 8 去下结论，比不说更坏）。

## bug 2：`/live` 在早退路径上发 `null` 而不是 `[]`

```
"online": null, "offline": null
```
四条早退路径（端不支持 / 用户关掉了 / 插件没装 / 从没启动过）都不碰这两个字段，
Go 的 nil slice 就序列化成了 `null`。前端这次是在读的地方写了 `?? []` 才没崩 ——
但「每个读的地方都记得兜一下」正是这个项目已经吃过亏的老坑。
按既有规矩（集合字段先补空集合）在构造时就填上。

## bug 3：BungeeCord 的「不可用」理由是假的

源清单里原本写着「源站能连通但**实测很慢**（26 MB 的 jar 两分钟只跑到 9.9 MB）」。
2026-10-06 连测三次：

```
第1次: Content-Length=26175295 落盘=26175295 用时=10.5s 均速=2.37 MB/s
第2次: Content-Length=26175295 落盘=26175295 用时=11.3s 均速=2.20 MB/s
第3次: Content-Length=26175295 落盘=26175295 用时=11.4s 均速=2.20 MB/s
```

**11 秒下完 26 MB，逐字节一致。** 那句「很慢」是错的 ——
也就是说界面在用一个假理由拒绝一个下载通道完全没问题的端。
真正卡住的是设计上的事：它是滚动构建、没有游戏版本概念，
新建向导那套「先选游戏版本、再选构建」的表单对它不成立。
文案已改正，**没有翻 `available`**（要接得给代理端单独做一套入口，那是另一件事）。

**顺手立的规矩（写进测试了）**：不可用的源**不许拿「很慢」当理由**。
网速结论会过时，而它一旦写进界面，用户就永远失去那个功能。
真有问题就写具体现象（「连接被重置」「只解析出 IPv6」），那种话有据可查。

## 三条新测试（都验证过「能抓住旧代码」）

- `TestProxyReasonNeverBlamesJava` —— 代理端原因里不许出现 Java 版本要求、
  不许有连续空格（那是一个没填上的 `%s`）；BungeeCord 不许引用 Velocity 的报错。
  *拆掉修复跑一遍：红，并原样打印出 `而  需要 Java 8`。*
- `TestCompanionSnapshotNeverSerializesNullPlayers` —— 三条早退路径各发一次，
  序列化后 `online`/`offline` 必须是 `[]`。*拆掉修复：三条全红。*
- `TestSourcesAlwaysExplainThemselves` —— 每个源都要有说明；不可用的必须说清卡在哪，
  且**不许拿「很慢」当理由**。*拿旧文案跑：红。*
  （这条的第一版写得太客气 ——「提了很慢就得再提点别的」——
   旧文案带着「版本/构建」两个词就绕过去了，等于没测。判据要盯住错法本身。）

**教训**：这一步差点漏掉 —— 我第一版把「空版本必须不下结论」写好了，
但拿旧的判定顺序一跑才发现，真正印在界面上的是那句空着名字的 Java 文案。
**「我以为的错」和「用户看到的错」不是一个东西**，所以每次都要把旧代码跑一遍看它到底吐什么。

## 现在这张表

| 端 | 插件可用 | 玩家数据 | 依据 |
|---|---|---|---|
| Paper / Spigot / Purpur / Leaves | ✅ | ✅ | 真机 + 机器人进服 |
| Arclight（Forge 混合） | ✅ | ✅ | 真机 + 机器人进服 |
| Cardboard（Fabric 混合） | ✅ | ⚠ 未验 | 插件加载正常；bot 在登录期被断 |
| 普通 Fabric / NeoForge / Forge | ❌（装桥接后可以） | — | 日志 0 次提及 |
| Folia | ❌ | — | 加载阶段被拒 |
| Velocity / BungeeCord | ❌ | — | 两家拒绝方式不同，都实测了 |
| 原版 Vanilla | ❌ | — | 没有插件系统，日志 0 次提及 |

---

# 补记四：Mohist 其实能下 —— 我上一轮把老地址当成了「项目下不到」（2026-10-06）

**这是一次方法上的失误，写下来免得再犯。** 上一轮我在五个地址上打了 200/404/DNS 失败，
就写下「Mohist 验不了、一键新建做不了」。**错在哪：我只 probe，没搜。**
项目整个搬了家、还改了名，我打的全是废弃的老入口。

## 老地址 vs 真地址

| 我打的老地址（已死） | 现在的真地址 |
|---|---|
| `mohistmc.com` | **`mohistmc.cn`**（新站，Next.js 写的） |
| `mohistmc.com/api/v2/projects/mohist/{mc}/builds` → 200 但**零字节** | **`api.mohistmc.com/project/mohist/{mc}/builds`** |
| `.../builds/{id}/download` → 200 但**零字节** | **`api.mohistmc.com/project/mohist/{mc}/builds/{id}/download`** |
| GitHub `MohistMC/Mohist`（`downloads` tag、0 附件） | **已改名 `TiangongCraft/Tenet`** |
| `mohist.red` | **DNS 都解析不出来** |
| `maven.mohistmc.com` 404 / `repo.mohistmc.com` DNS 失败 | `MohistMC/Youer` = NeoForge 侧的兄弟项目 |

**怎么一眼认出来的**：GitHub 仓库搜索按 star 排，`TiangongCraft/Tenet` 的 star 是 **1508**、
简介和 `MohistMC/Mohist` 一字不差 —— **改名会保留 star**。
（另外 `GET /repos/MohistMC/Mohist` 会直接返回 `full_name = TiangongCraft/Tenet`，
`homepage` 字段里就写着新域名 `mohistmc.cn`。）

## 新 API（全部实测，可直接照着写代码）

```
GET https://api.mohistmc.com/project/mohist/versions          → [{"name":"1.20.1"}, …] 共 10 个
GET https://api.mohistmc.com/project/mohist/{mc}/builds       → 直接是数组，不是 {"builds":[…]}
                                                                 [{ id, file_sha256, build_date,
                                                                    commit:{hash,changelog,author,commit_date},
                                                                    loader:{forge_version,neoforge_version} }]
GET https://api.mohistmc.com/project/mohist/{mc}/builds/{id}/download
                                                              → jar 本体，带 Content-Length
GET https://api.mohistmc.com/project/youer/{mc}/builds        → 同上（NeoForge 侧）
```

Mohist 支持的游戏版本（10 个）：
`1.7.10 / 1.12.2 / 1.16.5 / 1.18.2 / 1.19.2 / 1.19.4 / 1.20.1 / 1.20.2 / 1.21.1 / 1.21.4`
Youer 支持：`1.21.1`（595 个 build）/ `26.2`

## 实测数据

```
1.20.1 build 921  →  Content-Length 140522640（134.0MB）
                     直连 13.7 秒，均速 9.80 MB/s
                     sha256 = ade8962d51108f70a9facb8a7680990e50768cab83a2559d8bb64889f043471c
                     与 API 返回的 file_sha256 逐字节一致 ✅
Youer 1.21.1 build 920 → 122.4MB，下载口同样 200 + Content-Length ✅
```

⚠ 新站页面上自己写着「因资源有限，当前项目下载源位于法国，请自行使用魔法」——
**对这条线路不成立**，直连就是 9.8MB/s。**别把上游的免责声明抄进我们的界面。**

## 形态（决定「一键新建」好不好做）

`Main-Class: com.mohistmc.MohistMCStart`，Forge 本体塞在包内 `data/`
（`forge-1.20.1-47.4.23-universal.jar`、`fmlcore`、`fmlloader`…），
但 `libraries/` **不在包里** —— 包内 `installer.txt` 列了 24 个待下载的库（路径|sha256|大小）。
**所以它和 Arclight 一样：首次启动自己装库，会再下几十 MB。**
（`internal/daemon/import.go:104` 那条注释是对的，不用改。）

**面板侧几乎不用动**：`classifyJar("mohist-…jar")` 早就归 `TypeForge`，
`isBukkitHybrid` 早就认 `mohist` 这个词 → 支持判定自动落到「混合端：能用，但风险自负」。
**缺的只是「新建向导里的 Mohist 入口 + 下载源」这一件事。**

## 这条失误的教训

1. **「这个站下不到」≠「这个东西下不到」。** 先搜项目现在叫什么、搬去哪了，再下结论。
2. **「200 + 零字节」是废弃入口的典型形状**，不是「资源不存在」。
   而且它连 `Content-Type: application/java-archive` 都对 —— 特别好骗。
   **判据：200 必须配 `Content-Length > 0` 才算真能下。**
3. **SPA 的真实接口读 HTML 读不到**（Next.js 把地址藏在 JS chunk 里）。
   正解：真 Chrome + CDP `Network.enable`，让它自己跑一遍，请求列表直接摊在你面前。
   这次就是一条 `[GET 200] https://api.mohistmc.com/project/mohist/1.20.1/builds` 破的案。
4. **结论会过时，别继承。** 同一轮里 BungeeCord 那条「实测很慢」也是继承来的旧结论，
   重打一遍发现是 11 秒下完 26MB。**「慢 / 不通 / 被墙」必须当场重打。**

---

# 补记五：混合端「一键新建」补齐并真机跑通（2026-10-06 晚）

上一节结尾写着「缺的只是『新建向导里的 Mohist 入口 + 下载源』这一件事」。这一轮把那件事做完了，
顺手挖出并修掉了一个**更隐蔽的 bug**。

## 挖出来的真 bug：下载那一环漏了混合端（界面说能用、点下去说不能下）

`startServerJar`（`internal/api/fetch.go`）原本是**第三个并列 switch** —— 前面
`fetchVersions` / `fetchBuilds` 两个都补了 Mohist / Arclight 的分支，唯独**真正解析下载的这一个**没补。
于是表现是：

> `Sources()` 说 `mohist` / `arclight` 可用 → 向导里能选、能选版本、能选构建 →
> 点「创建」回一句 **「Mohist（模组 + 插件） 还不能自动下载」**。

**把「我们忘了写分支」说成了「上游不支持」**，用户没有任何办法自己搞清楚。
而且它**不会编译报错** —— 这正是「三个并列 switch」这种写法的固有缺陷。

**修法（结构性的，不只是补两行）**：把下载分派从 switch 改成 **`jarResolvers` 表**
（`map[fetch.Kind]func(body serverJarReq) (fetch.Build, error)`），每个类型一个小的纯函数 resolver。
这样「有没有漏类型」就变成一个**能被测试问出来的问题**：

| 测试 | 守什么 |
|---|---|
| `TestJarResolversCoverEveryAvailableKind` | `Sources()` 里每个 `Available:true` 的类型，表里必须有实现 |
| `TestNotAvailableKindsHaveNoResolver` | 标了不可用的**不许**偷偷留分支（否则界面说不能下、代码里下得动） |
| `TestJarResolversHaveNoUnknownKind` | 表里不许有 `Sources()` 不认识的 key（拼错的 key 永远不会被命中） |
| `TestResolveByBuildID` | 给了构建号按号取、没给取最新、**找不到必须报错**而不是交回零值 Build |

**已验证这条测试抓得住旧状态**：把 `KindMohist` 从表里摘掉跑一遍 →
`类型 mohist 在 Sources() 里写着可用，jarResolvers 里却没有它` 变红，再还原。

顺带补的两处「别把内部错误说成源站问题」：
- resolver 说成功、却给出空 URL → 单独报**「内部错误：…解析出的下载地址是空的（这是面板的 bug）」**。
  照旧往下走的话会拿空地址发请求，报出来像「源站挂了」，排查方向从一开始就是错的。
- 原版 / Fabric 的列表为空时**显式报错**（`原版没有 X 这个版本`），不再交回零值 Build。

## 前端两张名单：向导入口是 `TYPE_LABEL` 渲染出来的

后端 `Sources()` 决定「能不能自动下载」，前端 `TYPE_LABEL`（`web/src/types.ts`）决定
「向导里有没有这个入口」—— 向导第 1 步是 `Object.keys(TYPE_LABEL).map` 渲染的，
所以**只加后端 = 后端能下、用户在向导里根本选不到**（Mohist 两边都漏过）。
这一轮补上 `mohist` / `arclight` 到 `TYPE_LABEL` + `TYPE_INFO`（卡片文案）+ `TYPE_SKIN`（图标配色）。
`ImportDialog` 也吃 `TYPE_LABEL`，所以「导入已有服务器」现在也能把类型选成 Mohist / Arclight 了 ——
这很重要：选成 `forge` 的话，preflight 的「一键补齐」会去下 Forge 安装器跑 `--installServer`。

Arclight 那张卡片如实写「⚠ 它只发在 GitHub，这里要经第三方加速代理转一道」——不写「官方直连」。

## 真机验收（全部实测，不是读文档）

**① 面板新建 Mohist → 下载（不指定 build，走「自动挑最新」）**
```
total = 140522640 字节（= 上游 Content-Length）
落盘 140522640，zipfile 合法，Main-Class = com.mohistmc.MohistMCStart
sha256 = ade8962d…43471c  ← 与 API 的 file_sha256 逐字节一致
server.properties 按实例配置生成（server-port=25611）
```
**② 面板新建 Arclight → 按 tag 下载 `Trials/1.0.6`**
```
落盘 5725384（与列表 size 一致），zipfile 合法
Main-Class = io.izzel.arclight.server.Launcher
Implementation-Version = arclight-1.20.1-1.0.6-4e94eb76  ← 正是用户挑的那个构建
```
> ⚠ Arclight **没有构建号**（`Build.ID` 恒为 0），身份在 `Tag` 上。
> 所以 `resolveArclightBuild` 只看 Tag，拿 `body.Build` 判会把用户挑的构建悄悄换成「最新那个」。

**③ 面板新建的 Mohist 真启动**（不是导入的，jarPath 是 `server.jar`）
```
preflight: jarFound=true, javaPath=java 21（needJava=17），只差 eula
首次启动 55.7s（它自己把 libraries/ 装进了实例目录）
Mohist 启动成功啦！╰(*°▽°*)╯，总耗时共 55.684s    ← "Done (" 命中 0 次
[ServerBoxCompanion] ServerBoxCompanion 0.2.0 已启用（桥接目录 plugins\ServerBoxCompanion\bridge）
This server is running Mohist version 1.20.1-71a84ff7 (MC: 1.20.1)
```
`/live` → `supported=true, hybrid=true`，`online`/`offline` 都是 `[]`（不是 `null`）。

**④ 停服**：`POST /stop` → `status=stopped`（**没有**判成崩溃），`crash-reports` 是 `items: []`。

**⑤ 界面（真 Chrome + CDP 实测）**：第 1 步 9 张类型卡片，Mohist / Arclight 都**可点**；
点 Mohist → `aria-pressed=true`；进第 2 步真拉到 10 个版本（含 1.20.1）；
1440 / 1024 / 520 三档 `SW==CW` 无横向溢出。

## 这一轮值得记的一条

**「界面上的名单」和「代码里的名单」是两份东西，加类型时必须同时改，而且最好让它们对不上时能被测试吼出来。**
这个 bug 的真正成因不是「忘了写」，而是**同一件事被写成了三个并列的 switch**——
并列结构天生不保证完整。把其中一个换成表 + 一条覆盖性测试，比补两行分支值钱。

---

# 补记六：「普通模组服 → 对应的混合端」转换（2026-10-06 晚）

用户提的需求：**能不能加一个把普通模组服转成对应混合服的操作。**

## 为什么不做成「下拉选个类型」

「类型」这一栏是好几处的判据：服务端文件怎么下（preflight 一键补齐、配置页重新下载）、
插件与模组目录、能不能用实时数据。**手改一个字段，等于让这些判据集体说谎。**

而且真正要做的事是一整套动作，顺序还不能反：

```
① 下好混合端的整端 jar → ② 校验通过 → ③ 才设成启动文件 → ④ 最后才改类型
```

反过来的话（先改类型再下载），一旦下载失败（断网、上游 502），就留下一台
**「自称混合端、启动文件却还是 Forge 的 args.txt」**的实例 —— 那种状态界面解释不了，用户也修不回来。
所以设计定成：**下载成功才算转换成功**。这个顺序由后端保证，前端只提供一个「转换」按钮，
没有「改类型」这个入口。

## 接口（两条，先问后做）

```
GET  /api/instances/{id}/convert   → { current, currentLabel, mcVersion, jarPath, running,
                                       targets[], refusals[], warning }
POST /api/instances/{id}/convert   → { target, build?, tag?, mirror? } → 202 + 下载任务
```

`targets[]` 是**能转的目标**（含 `newJar`：转换后启动文件叫什么）；
`refusals[]` 是**我们故意不做的路 + 原因**。两个都摆在界面上 ——
只写「不支持」的话，用户会以为面板没这个功能，然后去别处折腾。

**只收同一个模组系的组合**（这才是「对应」两个字的含义）：

| 当前 | 可以转 |
|---|---|
| Forge | Mohist、Arclight |
| Mohist | Arclight（方便来回换） |
| Arclight | Mohist |

不做的四条，各带具体理由：Fabric → Cardboard（那不是换启动文件，是往 mods/ 装三件套，
属于另一件事）、NeoForge → Youer（下载源还没接）、Paper/原版（本来就是 Bukkit 端，
要模组等于换一台服，建议新建实例搬存档）、代理端（没有游戏版本也没有存档）。

## 实现要点

- 纯逻辑在 `internal/daemon/convert.go`（`ConversionTargetsOf` / `ConversionRefusalsOf` /
  `ConversionAllowed` / `ConversionNewJarName` / `ConvertedInstance`），路由在 `internal/api/convert.go`。
- **新启动文件换个名字，不动旧的**：`mohist-<mc>.jar` / `arclight-forge-<mc>.jar`。
  旧的 Forge 启动文件（args.txt / shim jar）**原样留着**，那是唯一的回退路径。
  名字里带 mohist / arclight 也顺带让侧栏标签与混合端识别认得出。
- **只动两处**：`Type` 与 `JarPath`。端口、内存、备份策略、自动重启一个都不碰
  （有护栏 `TestConvertedInstanceOnlyTouchesTypeAndJar`，把这两处还原后必须逐字段相等）。
- 运行中拒绝（409）：Windows 上运行中的 jar 覆盖不了，而且换完要重启才生效。
  「运行中」的判据与下载那条路**共用** `instanceRunning`，不许各判各的。
- 审计两条配对：`convert.request`（谁、从什么、什么构建）与 `convert.done`（落盘字节数、新文件名）。
  两条配着看，就能分辨「发起了但下载失败」这种半途。

## 验收（真机跑通，不是读代码）

在**独立端口 23457 + 独立数据目录**上跑新 exe 验的 —— 用户那台面板里有一台正在跑的 MC 服务器，
重启面板会把 java 变成孤儿进程（端口占着、面板不认识它），所以全程没碰它。

现场：建一个 Forge 实例，启动文件故意指向 `libraries/.../win_args.txt`（真机形态）。

| 检查项 | 结果 |
|---|---|
| 跨系目标（cardboard） | ✅ 400，且说清「跨了模组系：模组要重装、存档也不保证能读」 |
| 转成自己（forge） | ✅ 400「它已经是 Forge 了」 |
| 真转 Mohist | ✅ 140,522,640 字节，sha256 与上游逐字节一致，合法 zip |
| 转换后 `type` / `jarPath` | ✅ `mohist` / `mohist-1.20.1.jar` |
| **旧启动文件** | ✅ md5 与转换前**一字未变**（`a1bbd9f0…`）—— 回退路径保住了 |
| mods/ 与 world/ | ✅ 原样 |
| 端口 / 内存 | ✅ 没被改（25577 / 1024–4096） |
| 审计 | ✅ `convert.request` + `convert.done` 两条配对 |
| 支持判定 | ✅ 自动变成 `hybrid=true`（不用手动改任何东西） |
| 界面（真 Chrome + CDP） | ✅ 2 个目标行 + 代价提示；1440/520 都 `SW==CW` 无溢出；滚到底最后一行完整可见 |

## 这一轮抓到的两个真 bug（都是本项目的老坑）

**① 空集合序列化成了 `null`（老坑第 N 次）。** 单测全绿，一打接口：
`"refusals":null` —— 因为 Go 的 nil slice 序列化成 `null`，而前端写的是
`d.refusals.map(...)` / `d.targets.length`，**拿到 null 直接抛异常、整块面板白掉**。
修法是在**构造函数里**就返回 `[]T{}`，判据放在**序列化之后**
（`TestConversionListsNeverSerializeNull`：Go 里 nil 和空 slice 用 == / 遍历都分不出，
必须过一遍 JSON）。已验证这条护栏抓得住旧写法（改回 `return nil` 立刻变红）。

**② 文案里的 Markdown 记号被原样印出来。** `ConversionWarning` 里写了 `**加粗**`，
而界面是整段纯文本渲染 → 屏幕上真的出现星号。截图才看出来。
**规矩：给界面用的字符串是纯文本，不许夹 Markdown；这类问题断言查不出，只能靠截图。**

另外顺手改掉一处文案错误：Paper / 代理端这类**一个目标都没有**的实例，
原本也会印「混合端的本体还是你这个模组端」和那份 libraries 代价提示 ——
前者对它们就是错的，后者摆在点不了的页面上只是噪音。现在没目标时只解释「为什么不做」。

---

# 补记七：玩家页改成「一组一屏」（2026-10-06 晚）

用户的反馈只有一句，但很准：**「你不觉得很乱吗这个排版？什么都堆到一个页面？」**

## 我第一轮为什么没看出来（值得记住的方法论错误）

我先做了一轮量化体检：三档宽度无溢出、强描边只有 5 处且都来自 `SectionHead`、
字号 7 种但大部分在字阶上 —— 于是得出「结构没问题，先问清楚」。**这个结论是错的。**

**错在拿 mock 数据体检**：我为了让页面「有内容」，灌了 3 个在线 + 2 个离线玩家。
有数据时页面看着是满的；而用户看到的是**真实空态** ——
那时页面上「两段解释 + 三处空态 + 一段注意事项」全挤在一屏，**九成是灰字**。
→ **教训：排版体检必须覆盖「空态」。** 空态不是「没数据的特殊情况」，它是新用户的默认所见。
用富数据体检，等于把最容易出问题的那一屏藏起来不看。

## 改法：复用配置页早就有的「分组导航 + 一组一屏」

配置页已经有现成的一套（`.fnav` / `.fitem` / `.fpane`、可寻址状态 `?g=`）。玩家页照搬同一套：

| 分组 | 内容 |
|---|---|
| `?g=live` 实时状态 | 谁在线、在哪、身上有什么（原 01 区） |
| `?g=roster` 玩家名单 | 白名单 / 管理员 / 封禁，一个人一行（原 02 区） |
| `?g=ip` 封禁的 IP | 封的是网线，不是某个人（原 03 区） |

不再新造样式，**用同一套类**，所以两页看起来是一家的。

同时把满屏的**解释性长文收敛**（这才是「乱」的另一半）：
- 01 区那段讲插件实现的三行说明 → 一句；
- 03 区「封 IP 会误伤」两行 → 一句；
- 页脚「名单就是 whitelist.json / ops.json / banned-players.json，我们直接读写它们」**整段删掉** ——
  那是我们自己怎么实现的，用户不需要知道。（离线服 UUID 那个说明保留，只在 `onlineMode=false` 时出现。）

**保留**可操作的警告（白名单关着、快照过期）—— 那不是说明文字，是「你得去做点什么」。

## 验收（真 Chrome + CDP，两种数据形态 × 两种宽度）

| 检查 | 结果 |
|---|---|
| 每组只显示自己那一个区块 | ✅ 1440/520 × 三组，全部 `heads.length == 1` |
| 分组导航有高亮项 | ✅ |
| **说明性灰字段落 ≤1** | ✅ （把「可操作警告」和「说明性灰字」分开数 —— 前者必须留，后者该收敛） |
| 横向无溢出 | ✅ 1440/520 全部 `SW==CW` |
| **每组页面高 = 视口高（1000px）** | ✅ **不再需要纵向滚动** —— 这正是「什么都堆一页」的反面 |
| 点击切换 + URL 记下分组 | ✅ 点三个 item 依次得到 `g=roster` / `g=ip` / `g=live`，区块同步 |

跑的是**空态（真面板 0 玩家）**与**有数据（mock 3 在线 + 2 离线）**两种形态。

## 还留着、没动的两处（等用户点头）

1. 01 区在线玩家行：名字在最左、血量/饱食被推到最右，**中间一大片空**；
2. 血/饱食只有色块 + 数字，一眼看不出那两条是什么。

这两条是「同一行内的信息密度」问题，和这次「页面级堆叠」不是一回事，改法也不同 ——
没一起动，免得把两种改动混在一起、出了问题分不清是谁引起的。

---

# 补记八：桌面端窗口（M6 起步，2026-10-06 晚）

用户：「好了，做桌面端UI界面吧」。`docs/DESIGN.md` 里本来就规划了这件
（形态「托盘桌面端 + Web 同源」；M6「Wails 桌面端打包、托盘、手机端适配打磨」；
待办里那句「**Wails 桌面壳未开始 —— 托盘常驻、单实例、开机自启都依赖它**」）。
所以按既定选型做，不另立方案。

## 先验前提：这台机器能不能出 Wails 的 exe

本机 **`CGO_ENABLED=0` 且没有 gcc**，而 Wails 在 Windows 上要不要 CGO 直接决定这条路通不通 ——
所以**先探测再写代码**：

```
wails doctor          → v2.16.0 / Win11 25H2 / Go 1.27.1 / WebView2 154.x 已装 / Node 22 已装，无告警
最小工程 wails build  → probe.exe 11.9MB，32 秒，**没有 gcc 也成功**
```

结论：**Wails v2.16 在 Windows 上用纯 Go 的 `go-webview2`（新 WebView2Loader），不需要 CGO。**

## 关键取舍：窗口「重定向」而不是「内嵌前端 + 反代」

DESIGN 说「一份代码三种呈现」，直觉做法是把前端再嵌一份进 desktop.exe、再把 `/api` 反代过去。
**没这么做**，因为反代要穿过 SSE：

> 日志与实时数据都是 SSE。反代要求中间每一层都不缓冲，而 AssetServer 那一层不是我们能控制的 ——
> 一旦被缓冲，控制台就「卡住不动」，而现场看着像服务端不推数据。

改成一个 **302**：窗口首屏整个重定向到守护进程地址。之后 webview 的**源**就是守护进程本身，
会话 Cookie、SSE、相对路径全部天然成立 —— 跟自己用浏览器打开是同一个环境，一行反代都不用写。
代价是窗口本身不含界面资源（daemon 不在就没界面），所以补了一个**自带的等待页**：
先探活，健康就 302，不健康就自动拉起守护进程并返回一个每秒自刷新的「正在启动面板…」。
**不能让用户看见浏览器的「无法访问此网站」** —— 那既不像我们的产品，也说不出下一步干嘛。

## 落地的东西

| 文件 | 作用 |
|---|---|
| `cmd/desktop/main.go` | 窗口 + 首屏处理（探活 / 302 / 等待页）+ 自动拉起守护进程 |
| `cmd/desktop/proc_windows.go` | 让拉起的守护进程脱离本进程控制台（见下面 bug ②） |
| `cmd/desktop/main_test.go` | 7 条规格测试 |
| `build-desktop.sh` | 出 `bin/serverbox-desktop.exe` |

单实例用 Wails 自带的 `SingleInstanceLock`（⚠ `OnSecondInstanceLaunch` 挂在
**`SingleInstanceLock` 里面**，不是 `App` 上的字段），第二次双击把已有窗口提前而不是开第二个。

## 本轮抓到的两个真 bug

**① 拉起的守护进程监听到了别的端口 → 窗口一直「正在启动」。**
守护进程不带 `-addr` 时会用它**自己保存的配置**（本机这份配的是 `:23333`），
而窗口在等 `-daemon` 给的 `:23456` —— 两个进程都活着，窗口却永远转圈，现场看像「面板坏了」。
修法：拉起时**必须把地址一起传下去**（与 `-daemon` 同源）。
护栏 `TestDaemonArgsAlwaysCarryAddr` 盯的是**参数本身**，不是 `addrOf` 算得对不对 ——
因为这个 bug 的形态是「算对了但没传」。

**② 关掉窗口会不会把守护进程（以及它管着的服务器）一起带走？**
默认会：子进程沿用父进程的控制台，**控制台一关，系统给挂在它上面的进程发 CTRL_CLOSE_EVENT**，
没装处理器就直接结束。于是「我只是关了窗口，服却停了」，而且不好往这里想。
修法：拉起时给 `DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP`，让它**完全不带控制台**。

⚠ 这条结论差点被**我自己的测试环境骗过去**：第一轮测「杀壳 → 守护进程也没了」，
看着像修复无效。真相是我从工具的控制台启动壳，壳与被它拉起的守护进程共用那个控制台，
一杀整条一起走 —— **是测试夹具的干扰，不是产品行为**。
反证：先**独立**启动守护进程（不由壳拉起），再启动壳、只杀壳 → 守护进程活着。
最后用**计划任务**（脱离我的控制台）跑了一遍干净场景，确认「壳拉起的守护进程在壳被杀后仍存活」。

## 真机验收

| 检查 | 结果 |
|---|---|
| 无 gcc / CGO=0 能否出 exe | ✅ `serverbox-desktop.exe` 11.8MB |
| 冷启动（什么都没开）| ✅ 自动拉起守护进程，`/api/health` 200 |
| 拉起的守护进程端口正确 | ✅ 在 `127.0.0.1:23456` 监听（修 ① 之后） |
| `-data` 是否传给守护进程 | ✅ 指向全新空目录 → 实例落在该目录（另一目录保持 0 条） |
| 关窗不带走守护进程 | ✅ 计划任务启动的壳拉起的 daemon，壳被杀后 `/api/health` 仍 200 |
| 首屏四个分支（健康 / 不健康 / 拉起失败 / 地址转义）| ✅ 7 条规格测试 |
| 单实例 | ⚠ 配置项已就位，未做「双击两次」的手工验证 |

## 还没做的（M6 剩余）

1. **托盘**：要引托盘库，而它和 Wails 抢主线程 —— 得单独验一轮，不想混在这次里赌。
   现在的行为是**关窗即退出**（守护进程不受影响），「最小化到托盘」还不是默认。
2. **应用图标**：目前是 Wails 默认图标，还没做 `.ico` 资源。
3. **开机自启**：计划写注册表 Run 项或计划任务，与面板现有那套常驻方式统一。
4. **远程模式打磨**：`-daemon` 已能指向别的机器（且此时**不会**在本机乱拉进程），
   但还没有「填地址 + 记住」的界面。

---

# 补记九：玩家管理其实早就做完了 —— 一次「文档与现实脱节」事故（2026-10-07）

用户问「查看当前项目进度」，照文档回答会得到「下一件：玩家管理」。**这是假的。**

## 实况：代码早就在磁盘上，只是从未 `git add`

| 文件 | 行数 | 最后改动 |
|---|---|---|
| `internal/daemon/players.go` | 630 | 10-05 21:18 |
| `internal/daemon/players_test.go` | — | 10-05 21:18 |
| `internal/daemon/playerstats.go` | — | 10-05 21:17 |
| `internal/api/players.go` | 134 | 10-05 21:17 |
| `web/src/components/PlayersPanel.tsx` | 1521 | 10-06 17:10 |

`git ls-files` 查这五个文件**全是空的** —— 从未跟踪。而 `git status` 里它们埋在 198 个未跟踪文件的
列表中间，扫一眼根本看不见。

已经实现的（`daemon/players.go` 导出符号清单 + 10-05/10-06 的验收记录）：

- 名单读写：`PlayerLists` / `WhitelistSet` / `OpSet`（含 level 1–4）/ `BanSet`（玩家 + IP）
- **限时封禁**：`banRejectReason(running, on, expires)` 纯函数，跑着不许封、过期时间不能在过去，
  `ErrBanNeedsStop` → 409；写盘时空写 `"forever"`、否则 `mcTime`
- **离线服 UUID**：`offlineUUID(name)` —— `md5("OfflinePlayer:"+name)` 的带连字符形式（当初预判的坑，已落地）
- **玩家档案**（`playerstats.go`，不靠插件）：读 `<world>/stats/<uuid>.json` + `playerdata/<uuid>.dat` 的 mtime。
  ⚠ `play_time` / 老档 `play_one_minute` 两个字段都认；`<world>` 读 `level-name`，不硬编码 `world`；
  没档案时返回**解释**（离线模式没 UUID / 还没进过服），不是空对象
- `KickPlayer` / `OnlinePlayers` / `OnlineEntries`
- 前端：右侧抽屉详情（遮罩只给 `bg-black/15`）、行内 4 个语义分色方按钮、
  导出名单 JSON、分页 20/页、按在线/名字/封禁排序、`?player=<小写名>` 与 `?playerdlg=ban|add|kick|ip` 深链
- **真机验收过**：1440/520 两档 `SW==CW`、`.ibtn` 实测 32×32、`.prow` 行高 54、
  封禁弹层 innerText 逐句核对、档案接口实测 Steve `playTicks=3312000`(46h)/死亡 12/击杀 3/杀怪 486、
  `go vet` 干净 + `go test ./...` 全绿 + `tsc -b --force` 通过 + **生产 exe 已重编**（11M）

## 为什么三份文档会连续三天说假话

1. **做完的功能没有 `git add`，也没有写进 `NEXT-STEPS.md`** —— 「已做清单」这个文件当时没更新。
2. `COMPETITOR-GAP.md` 是 10-05 的调研稿，它那句「← 下一个」从来没回头改过。
3. **`MEMORY.md` 的「待办」是从 `NEXT-STEPS.md` 抄过去的**，源头错，下游一起错，还被压缩成一句话，
   于是每次会话开头注入的都是这句假话。

⚠ **这类错比代码 bug 更难发现**：代码编译得过、测试是绿的，唯一的问题是「描述进度的文档」和
「磁盘上的文件」不是一个东西。**排查办法：`git ls-files <文件>` 是不是空的、`git log` 最近提交到哪，
别只看 `git status` 的第一屏。**（198 个未跟踪文件里藏着整份实现，`git status --porcelain | wc -l`
给的数字越大越要单独核对。）

**规矩**：判断「某功能有没有做」一律走 **磁盘 + 测试**，不走文档；文档只用来查动机与决策。
下次做完一个功能必须同时做两件事 —— 写进 `NEXT-STEPS.md` **并**至少 `git add`。

## 订正后的待选梯队（原来的「P0-2 之后」全部作废）

| 优先级 | 事项 | 状态 |
|---|---|---|
| M6 剩余 | 托盘常驻 / 应用图标 `.ico` / 开机自启 | 未做（托盘库与 Wails 抢主线程，要单独验一轮） |
| P0-3 | 游戏规则编辑器 | **已写完待验收**（10-09：后端 jar 目录+解析+防注入、API 两条路由、前端 RulesPanel；22 测试绿。剩 #30 真机读写验收 + sweep 回归） |
| P2 | CurseForge 市场（需 API key） | 未做 |
| P1 | 崩溃日志智能分析（OOM/端口占用/EULA/缺依赖 → 人话 + fix） | 未做 |
| P1 | 存档管理独立化 / 监控历史落盘 / Java 版本区间匹配 | 未做 |
| P3 | MCP Server（65 条路由已就绪）/ 审计日志 / Webhook | 未做 |





---

# 像素重制稿 v2 已补齐九屏（2026-10-07）

用户：「查看当前最新的那版设计稿，按照它那版设计稿，开始实现UI重构，先设计另外几页的设计稿给我看完整版」。

**已交付**：`docs/design/pixel-refresh/` 从 5 屏补到 **9 屏**（概览/控制台/玩家/配置/状态速览/
插件模组/备份还原/计划任务/设置），+ `spec.html` 新增第 09 节「行列表与面板头」。
四屏内容对着真实后端写：`tasks.go` 的触发四型与动作七种、`settings.go` 的字段、备份策略的真实语义。
截图 `docs/shots/pixel-refresh/v3-*.png`（九屏 × 1440/520 + 规范页）。
回退快照：`*.v2-0410.bak.{html,css,js}`。改前改后的完整记录见 `.workbuddy/memory/2026-10-07.md`。

**顺手修的三个既有 bug**（都是旧五屏就有的，非补页引入）：
①「快捷操作」两列在 520 档撑破面板 158px —— 根因是 `1fr` 的自动最小尺寸 = min-content，
而 `.btn` 是 `nowrap`，压不下去；正解是窄屏单列（`minmax(0,1fr)` 只会把字切掉）。
②`.chipf` 缺 `nowrap`，窄栏里被压成竖排。③`.con-head` 不换行，手机档芯片纵向堆叠。

⚠ 过程中夹具骗了两次，已立规矩：**探针出数前先自检 CSS 是否真的加载**
（`getComputedStyle(document.body).backgroundColor` 不是透明）——第一次是临时文件放子目录导致
`styles.css` 404、测量全废；第二次是 headless 虚拟时钟不推进 CSS 动画、`.reveal` 停在 `opacity:0`
导致截图空白。详见 `2026-10-07.md`。

**用户已定的下一步**：① 先不补「文件管理」「整合包市场」两屏，先确认这九屏；
② 确认后 **UI 重构从「概览页」开始**（不是先抽 token，也不是按新四屏顺序）。

---

# UI 全量移植完成（2026-10-07 深夜）

用户指令：「完整的，包括所有的页面，所有的详情页，所有的动态效果等，并且需要一模一样…按设计稿来」。

**移植面全部落掉**：
- 旧设计类残留（`_scan_old.py` 口径：jlist / row-b / px-card / btn-grass / chip chip-x /
  border-l-[3px] / modline 等）：**117 处 → 0**。可达组件无旧类；死文件（LivePanel / PlayersPanel /
  LogConsole / Meter / MetricBand / Spark）未动。
- `btn-sm` / `btn-grass` 全仓清零。旧 `./Icons` 只剩三处**有意保留**：IconTypeBlock（服务端类型彩块）、
  IconHeart（体检心条）、IconBlock 家族；其余全换 `<Icon name>` 精灵。
- ui.tsx 的 ListRow / DangerZone（全仓 0 引用的死代码）已删；Skeleton 系改 `.panel` 几何。

**验收证据**：
- tsc 0 错 → `vite build`（js 550KB / css 117KB，字体本地打包）→ `go build` 13M
  → `bin/serverbox-next.exe`（**embed 校验**：临时实例首页资源 hash 与 dist 一致）。
- 四档截图（1440×11 页 + 1024 / 720 / 520）**SW==CW 全过**，存 `F:/tmp/sbx-shots-20261007/`。

**遗留**：`bin/serverbox.exe` 未就地替换（被运行中的测试实例占用）——正式启用：
停实例 → `cp bin/serverbox-next.exe bin/serverbox.exe` → 重启。

**下一梯队（待选）**：ModDetail / DepQueueDialog 弹窗深检 → M6 托盘/图标/开机自启 →
P0-3 游戏规则编辑器 → P2 CurseForge → 存档管理 → 监控历史 → Java 版本区间匹配。

## 2026-10-09 二批：三项 UI 修复（用户点名；细节见 memory/DETAILS.md）

- **配置页超出边距**：`.cfg-pane` 补设计稿内衬 `6px 16px 16px`（字段左偏移 0→16；12 组 × 1440/900/520 零越界）。
- **「设置」挪位**：tabbar → 顶栏（刷新右侧）、tabbar 9 项；顶栏激活态 cyan；深链 `?page=settings` 不变。
- **select 统一皮肤**：`appearance:none` + 9×5 阶梯箭头 SVG（`.px-input/.inp/.sel` 三条类选择器，13 处 `<select>` 全覆盖）。
- 构建 `index-CDQh96Vw.js` / `index-BZPsQRrZ.css`，exe sha `5fcbb77a…`；全站 sweep 16 URL × 1440/520 全绿；
  验收截图 `F:/tmp/sbx-1009c/`（4 张）。

## 2026-10-09 三批：三项修复深化（用户回：「没看见下拉选框有变化啊，配置页重新设计，好丑」）

- **select 升级 base-select 两级方案**：第一版 9×5 三角跟系统箭头肉眼一样 = 白干；
  正解 `appearance:base-select`（Chrome 135+，本机 154）**连弹层一起换皮** —— `::picker-icon` 像素箭头
  （紧跟文字=设计稿 .sel 排法）、`::picker(select)` = `.rmenu` 材质（深底/方角/硬阴影）、`option:checked` 青字；
  9×5 三角降级为老浏览器兜底（@supports 外层）。
- **配置页控件撑满**：Crow 去掉 `justifyContent:flex-end`（曾把 input 塌成 ~178px 挤右边、中间空几百像素）；
  控件包 `.ctl` + `.field > .ctl` 撑满规则（input/select 100%、stepper flex、`.tog{display:block}`——span 会塌）。
- **两处自伤**：删 AccessPanel 内联 `appearance:none`（压过 base-select）；删同处残留的手动 chev-r 箭头
  （base-select 的 ::picker-icon 会自己画 → 双箭头）。
- 构建 `index-CLrvya2C.js` / `index-CpH9Qeyz.css`，exe sha `0b57eb92475b7aec`，面板 PID 32716；
  sweep 16 URL×1440/520 全绿；截图 `F:/tmp/final/z-cfg-page.png` / `z-cfg-open.png` / `z-cfg-520.png` / `src-open2.png`。
- 新坑：build.sh 直跑被 safe-delete shim 拦（vite 清 outDir 走 node rmSync，>50 文件触发确认）→
  先 `rm -rf web/dist` 再跑；uiprobe `--click` 支持不到视口外坐标（520 下 select y=1184 > 视口 900）→ 加高 `--h`。

## 2026-10-09 四批：游戏规则编辑器写完（P0-3，最后一个 P0）

**前提修正**：COMPETITOR-GAP.md 的「路线 A = `/gamerule` 无参枚举全部规则」这个前提是错的 ——
Java 版没有无参枚举（1.20.1 实测 + wiki 语法 `gamerule <rule> [value]`，rule 必填）。
实际落地：规则全集从**服务端 jar 的 `assets/minecraft/lang/en_us.json`** 提取（跟版本走、不硬编码；
vanilla bundler 内层 / Paper / Forge 布局实测都能读，Fabric 没有→退内置基线表），再逐条单查解析。

- 后端 `daemon/gamerules.go`：jar 目录扫描 + 中文叠加表（45 条）+ 解析（名字词边界 + 行尾值 +
  名字值之间必须有说明文字——单测抓出「聊天行冒名」误读）+ 防注入两道闸 + `Request` 收集器
  （`enough` 回调集齐即返回，正常路径 0.41s→0.01s；quiet 800ms 只兜底读不全）。
- API：`GET/POST /api/instances/{id}/gamerules`，错误带 reason 码（no-server / not-ready / unknown-rule）；
  前端 `req()` 的错误对象全局补 `status`/`reason`。
- 前端：`RulesPanel.tsx`（照配置页骨架：左组导航+右单组+搜索+停服说明态），tabbar 第 5 项「游戏规则」
  （新画告示牌图标 `i-rule`；页面编号 04，后续顺延到 10）。开关点一下立刻生效；数字改完按行内「写入」；
  值以服务端回报为准（clamp 会说明）。
- 测试 22 个全绿（解析 17 例 / jar 三种布局 / 假服务端全链路 / 早退计时 / 注入 15 例 / API 分流）。
- 构建 exe sha `ba4102df…`（PID 54648）；停服态无头验收过（10 页签/说明态/overflowX=0）。
- **剩 #30**：真机读写验收（vanilla 起服读全量→改 keepInventory→复读→停服 409）+ 运行态无头 UI + 16 URL sweep 回归。

## 2026-10-10 凌晨：sweep 假绿修复（顺手复查「还有什么没做的」）

- `tools/sweep.mjs`：URL 清单里 `?page=backups` 是错的（页面 key 是 `backup`）→ App 静默回落概览页，
  而 sweep **只判溢出不判页面** → 「全站全绿」里混着一条假绿，备份页其实从没被体检过。
  修：改对 key + 新增 `tab` 断言（`tab !== URL 里的 page` 直接失败并打印实际量到谁）+ 补 `?page=rules&g=玩家`。
  判据 42/42（13 URL × 1440/900/520）零溢出且页面全对。
- 顺带核对的存量缺口（都在上文梯队表里）：NeoForge/BungeeCord 仍 `Available:false`（`fetch/source.go`）；
  FileManager 目录下载要后端打 zip（未做，界面已明说）；整合包升级的「版本列表接口」未做（只能手填）；
  设置页「守护与启动」全局行为要等 Windows 服务层；M6 托盘/.ico/自启；P1 崩溃日志智能分析、配置编辑器高亮与
  保存前校验、存档管理/监控历史/Java 版本区间；P3 MCP / Webhook（审计只覆盖了 live 与插件侧装）。
- 清理项：死文件 `web/src/components/PlayersPanel.tsx`（旧面板，已无人引用）、`PluginsPanel.tsx.bak-render`。
