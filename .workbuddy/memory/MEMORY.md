# 项目：开服器（MC 服务器管理器）

## 定位与约束
- 面向「家里闲置电脑」的个人用户：Windows 优先、开箱即用、30 秒开服。区别于 PCL/HMCL（客户端）与 MCSManager（运维面板）。
- **不得 fork/复用 PCL2 代码**（LICENCE 强制蓝色主题+署名+开源），只借鉴产品思路。
- 工具链全在 F 盘。Go 在 `F:\tools\go`（GOPROXY goproxy.cn）；JDK21 在 `F:\Java\java 21`。
- Git Bash 给 Go 传路径必须 `F:/...` 形式，`/f/...` 会被判相对路径。

## 技术栈
Go 单 exe 守护进程 + React/TS/Tailwind v4 + Wails（**未开始**）。多实例、Job Object 托管子进程、SSE 推日志。
**单 exe（前端已 go:embed 内嵌）已落地并实测**（`internal/webui`，`build.sh` 一键出包）。

## 目录要点
`internal/daemon`（实例/进程/preflight 体检/日志落盘/properties/**forge.go 跑安装器+认启动 jar**）、
`internal/api`、`internal/fetch`（jar 下载；**forge.go 版本表/构建列表/安装器下载**）、
`internal/settings`（鉴权）、`web/src/components`、`docs/BACKEND-PLAN.md`（**剩余工作权威清单**）、`tools/fakeserver`。
`internal/daemon/testdata/forge-installer-sample.txt` = **1.20.4 安装器真实输出**的抽样（1204 行），
给日志过滤规则做回归 —— 别手写样本，见工作守则 12。

## 关键决策（别改回）
- **崩溃重启护栏**：退避 5s→20s→60s，3 次后停下给人话提示；跑满 180s 才清零；崩溃存最后 200 行到 `data/logs/<id>-crash-*.log`。
- **鉴权**（internal/settings + api/auth.go）：默认不挡人（requireAuth=false+回环豁免），只在「非回环+无保护」红警告；会话用 **Cookie**（EventSource 设不了请求头）；不看 X-Forwarded-For；令牌只存 sha256；改密码必须旧密码且 revokeAll。前端 401 广播 `sbx-need-auth`，登录闸门在 App 渲染之前。
- **下载源**（实测 2026-10-04）：PaperMC 真地址 `fill.papermc.io/v3`（api.papermc.io 全废）；下载域名 `fill-data.papermc.io`；下载项键名 `server:default`；builds 顶层是数组；**默认选最新 STABLE 构建**（26.3 零 STABLE）；Java 要求以原版元数据 `javaVersion.majorVersion` 为准；BMCLAPI 只镜像原版；续传必须先把 .part 喂进 hash。
- **产品原则**：①不静默失败，没接的明说卡在哪；②错误必须可操作（preflight 每条带 fix）。
- **进程路径三原则**（真机联调撞出来的，别改回）：
  ①服务端 jar 一律走 `resolveJar()`/`JarPathOf()`，**任何地方都别直接读 `inst.JarPath`**（可能是空的，
  java 会收到空 `-jar` 秒崩，而体检那边用 resolveJar 会说「没问题」——自相矛盾，最伤信任）。
  ②`Process.Kill/Stop` 必须置 `intentional=true`；收尾 goroutine 只认这个标记判「崩溃 vs 正常停止」，
  **别拿 status 当依据**（Kill 想让状态立刻可读写 Stopped，会被 goroutine 覆盖成 Crashed，
  还会触发自动重启 + 写假崩溃档）。
  ③**Oracle `javapath\java.exe` 是转发器**，会再 spawn 子进程跑真 JVM。必须把 java 路径归一到
  `<java.home>/bin/java`（`probeJava` 用 `-XshowSettings:properties` 读），否则 PID/内存全读错
  （壳 10MB vs 真身 1.3GB）。厂商判定也读权威属性 `java.vendor`，**别对整份输出做子串扫描**
  （`java.library.path` 里混着 Microsoft.DesktopAppInstaller，会把 Oracle 认成 Microsoft）。
- store.Store 的 All() 遍历 map 顺序随机，用前必须自己排序。
- **别把「可见但不可点」当成默认答案**（2026-10-04 返工教训）：库里没实现一个类型时，
  我第一反应是把它做成「列出来但置灰、写明原因」。用户反问「你堵死干什么？」——
  他要的是**能用**。正确的第一动作是**先探接口看能不能真接上**，接不上再说为什么；
  置灰只该用于「探过了、确认接不了」的类型。
- **Fabric**（实测 2026-10-04，2026-10-04 接入）：`meta.fabricmc.net/v2`。
  三段式 `versions/loader/{游戏版本}/{Loader}/{安装器}/server/jar` → **200，直接就是可运行的服务端启动器**
  （181,840 字节，PK 头）。**少一段就 404**（第一遍只写两段，三个组合全 404，差点误判成「不支持直连」）。
  它是启动器，真正的 jar 首次启动才拉 → 首次开服必须联网且慢；
  **既不给 Content-Length 也不给校验和**，所以大小/校验只能是「未知」——别编数。
  版本筛选用上游自带的 `stable` 字段，别用「名字里有没有横杠」去猜。
  BMCLAPI 只镜像它的**元数据**、jar 路径 404，所以不给它挂镜像。
- **Velocity**：和 Paper 同一个源站、同一套结构 → `paper.go` 做成项目参数化复用，别复制一份。
  ⚠ `channel` 是 **RECOMMENDED** 而非 STABLE（3.5.1=RECOMMENDED，3.4.0/4.2.0=STABLE）。
  前端 `isStable()` 必须两个都放行，否则会把推荐版误报成测试版 + 误触发「零稳定构建」警告。
  Java 要求上游**不给**，按版本线推断（读 jar 字节码实测：3.4.0→17、3.5.1→21、4.2.0→25；
  class major − 44 = Java 版本）。三档跨度说明**取固定值一定是错的**。见 `velocityJava()`。
- **Forge（已接入，2026-10-04）**：`maven.minecraftforge.net` 官方源**直连可用**，和 NeoForge 不是一回事（别混为一谈）。
  它是**两段式**：下的只是 installer，必须在本机再跑 `java -jar installer.jar --installServer <dir>` 才产出服务端
  （这一步再下 100 MB 左右库文件；实测 1.20.4 全流程 42s、1.12.2 38s）。
  产物分两代，**必须按目录实况决定启动哪个 jar，不许按版本号推断**：
    `spec=1`（1.17+）→ 根目录 `forge-<tag>-shim.jar`（真身在 libraries/）
    `spec=0`（≤1.16.5）→ 根目录 `forge-<tag>.jar` + `minecraft_server.<MC>.jar`（**认错成后者 = 模组全没了**）
  两代根目录那个 jar 都能直接 `-jar` 启动，**不需要 win_args.txt**。
  版本表取 `promotions_slim.json`（官方推荐/最新），构建表取 `maven-metadata.xml`（含 35 条垃圾版本，要按
  `validForgeTag` 滤掉）；排序 = 官方推荐/最新在前，其余按**分段数值**从新到旧（字符串排序会把 49.9 排到 49.2.10 后面）。
  版本号不是整数（`49.2.0`），塞不进 `Build.ID`，所以加了 `Build.Tag` 字符串做稳定标识。
- **未接的两家（探明结论，别再重复探测）**：
  NeoForge：`maven.neoforged.net` 在本机**只解析出 IPv6、连接被重置**，打不通。
  BungeeCord：`ci.md-5.net` 301 到 `hub.spigotmc.org`；jar 有 26 MB 而实测 ~80 KB/s
  （两分钟只跑到 9.9 MB）→ 和「30 秒开服」的承诺直接冲突，且是滚动构建、没有游戏版本概念。

- **⚠ 两个 http 客户端不能合并**：`apiClient`（Timeout 30s，只给元数据）与
  `dlClient`（**不设总超时**）。`http.Client.Timeout` 覆盖整个 body 读取 —— 曾经共用一个
  Timeout=30s 的客户端，于是「下载几十 MB 的 jar」被限死在 30 秒内：
  机房网络 49 MB/3 秒所以一直没暴露，换家宽（1 MB/s 要 50 秒）就稳定报「下载失败」，
  而且看起来像**源站**的问题。

## UI 规范（用户拍板）
- Token：底 `#111311`/面板 `#1c201d`/线 `#3d453d`/文字 `#eef1ec·#b7c2b4·#8b978a`/主绿 `#7cc24f`/警 `#f2c14e`/危 `#e0574c`/青 `#4aedd9`。零圆角零渐变，像素 SVG 图标（禁 emoji）。
- 对比度正文 ≥4.5:1（ink-3 已修到 6.1）；分隔线 1.5-1.9 是刻意的。字阶封闭 7 档（22/16/14/13.5/12/11.5/11）；mono 只给机器数据。强描边只给容器和 .btn，**chip 不描边**。
- 版式靠 `ui.tsx` 的 `ListRow`/`Field` 钉死；SectionHead.idx 只放序号；可搜索内容别写死 JSX。
- 动效 140/220/300ms，`cubic-bezier(.16,.84,.44,1)` 不许弹跳；留 `body.no-anim` 开关；滚动容器只做 opacity 动画（transform 会毁 sticky）。
- 配置页：左分组导航+右侧一组，滚到底切组，搜索切全部命中；`.crow` 标签列 330px 控件紧随；滚轮劫持前先判断面板能否滚。
- 数值用 StepInput（禁 `<input type=number>`）；滚动条全局统一。
- 可寻址状态：`?page= ?view=create&step=N ?view=create&type=forge&mc=1.20.4 ?nosse=1 ?g= ?q= ?probe=1 ?drawer=1 ?restore=1`。
  向导的 `type`/`mc` 是这轮加的 —— 深链可分享，也是无头浏览器**唯一**能进到「第 2 步的某个类型」的入口（点不了按钮）。
  `preferVersion` 只在 URL 显式点名时才传，别改成总是传：新建向导默认落到最新版本是**刻意**的。

## 工作守则（血泪）
1. Edit 会假成功 → 小改动用 python replace + grep 回验；反复失败就 Read+Write 重写。
2. 类型检查必须 `npx tsc -b --force`（--noEmit 会假通过）。
3. 自定义组件类必须写进 `@layer components`（层外样式会压掉工具类）。
4. 截图用 `?nosse=1`；先用 fakeserver 造真进程。
5. 后端改完要「重建+重启」，重启前查 23333 端口占用（run_in_background 常驻）。
6. headless Chrome：手机模拟最小 `--window-size=520`，1440 实际视口 1422；溢出用 `?probe=1` 读 SW/CW 定论；交互态用 URL 参数预置。
7. 缩略图只发现疑点，定论走 getComputedStyle/grep 构建产物。
8. 批改正则别只匹配行首，改完用测试复验（见 properties_test.go 的教训：漏 Group 零报错）。
9. 接第三方 API 前先打真接口（skill `external-api-probe`）。
10. **补丁脚本必须写成幂等**：沙箱会重跑，且「在锚点前后插入」的编辑其锚点在插入后依然存在 →
   会被插两遍（`redeclared in this block` / 组件里出现两份）。凡是插入式编辑，先判 `count(new)==1` 就 return。
11. **行尾别一刀切**：本仓库行尾是混的 —— `internal/**/*.go` 存 **LF**、`web/**` 存 **CRLF**。
    python 读入默认把 `\r\n` 归一成 `\n`，写出若用 `newline='\n'` 就把整个文件压成 LF，
    diff 里表现为「整个文件都改了」（几百行膨胀到上千行），真实改动全被淹没。
    **改完必须 `git diff --stat` 复核**；判行尾用字节统计或 `od -c`，
    **别用 `grep -c $'\r'`**——本轮它把纯 LF 文件报成「每行都有 CR」，据此做了错误的一刀切。
12. **要过滤第三方输出时，样本必须逐字抄自真输出，并固化成 fixture**。
    这轮 `interestingLine` 第三版单测全绿、真机灌进 11736 行噪音，原因只有一个：
    **样本是我按想象手写的裸路径**，而真输出前面还挂着动词（`Copying xxx.class`）。
    正确姿势：把原始输出整个抓下来 → 按形状抽样存 `testdata/` → 断言「过滤后只剩 N 条」，
    任何一族噪音漏过去都会让测试变红。配套三条：
    ①**先按形状排噪音，再按关键字找信号，顺序不能反**；
    ②任何关键字规则都会在「合法名字里含 fail/exception」上翻车（`DefaultUncaughtExceptionHandler.class`、
      guava 的 `failureaccess` 都是活例子）；
    ③动词规则里必须要求路径含 `/` 或 `.`，否则 `Patching failed` 这种真报错会被一起吃掉。
    真输出里数出来的噪音族（29733 行）：Copying 11736 / Reading patch 1421 / Patching 1420 / Checksum: 1420 / Can't Find Class 557。

## 后端接口已实现（均实测）
preflight 六项体检 / eula / fix(accept_eula·sync_port·rescan_java) / PUT 实例（整体覆盖）/
properties GET|PUT（23 字段带 kind/label/hint/options，未知键只读附尾）/ fetch sources|versions|builds|tasks /
server-jar 下载 / start 体检不过回 409+整份报告 / 全局设置+鉴权 / Paper+原版 jar 一键下载（含断点续传+SHA+轮询进度）。

**服务端类型真相源**：`fetch.Sources()` 是**唯一**真相源 —— 列出全部 7 种类型并带 `available`，
现为 **paper / vanilla / fabric / velocity / forge 五种可自动下载**，NeoForge 与 BungeeCord 标
`available:false` + 写明为什么。前端「能不能选 / 能不能下」
全读它（`SourceOf()` 让 API 复用同一份文案）。**别在前端另写一份类型名单** ——
曾经只返回 2 种，导致选 Forge 时 ServerPicker 的「不支持」面板判据 `src && !src.available` 永不触发（死代码），
于是径直去拉 versions → 后端 400 → 前端画成一个点了永远一样的「重试」，用户走进死循环。

**真 Paper 1.21.4 全流程已跑通**（2026-10-04）：下载→体检→EULA→启动→命令→停服，就绪 18.2s(首)/11.6s(二)。

**待人拍板的待办**：
- `PaperVersions()` 给每个版本都标 `stable:true`（只看名字有无 `-`，没看构建 channel），与前端判定口径不一致。
- v0.1 剩余：自动备份（P0，用户已点名下一个做）、崩溃日志智能分析（P1）。

**已做**：选版本/构建时就比对本机 Java（`web/src/useLocalJava.ts`）。
**已做**：未支持的服务端类型如实说清原因**已做**：服务端直连下载覆盖 **paper / vanilla / fabric / velocity / forge** 五种。
Forge 是唯一的两段式（下安装器 → 本机跑安装 → 自动把 `jarPath` 指向生成的启动 jar，并回填 `loaderVersion`），
其余四种下完即用。
Fabric 只有一个构建（最新稳定 Loader）；Velocity 构建带真实编号（如 #615）。
两种都走 `Build.Label` / `Build.JavaFrom`，前端据此显示「Loader 0.19.5」和如实标注 Java 数值来源。
 —— `fetch.Sources()` 补全 7 种 + `SourceOf()`；
向导第 1 步置灰不可点的类型（仍列出并写明原因，免得选完版本走到第 2 步才发现是死路）；
ServerPicker 的「不支持」面板加了 `srcReady`（问过源站没有）才生效，**避免把网络失败误报成「不支持」**。
`null`=还没问到（闭嘴）、`0`=真没有（报警）必须分开；`maxRef` 给「加载时挑默认项」用，避免加载函数重建。
配置页分组可用 `?g=__server__` 直达「服务端文件」组；建一个目标版本的实例就能借 `preferVersion` 自动选中它，免点击。
