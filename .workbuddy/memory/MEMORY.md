# ServerBox 开服器：Go 单 exe + React

> ⚠ **注入上限约 8000B**。长参考在 `memory/DETAILS.md`（动手前先读对应节）；已做清单 `docs/NEXT-STEPS.md`。
> 只写「结论+判据/测试名」，先查重再加。网络/GitHub 在**用户级** `~/.workbuddy/MEMORY.md`。

## 工程
- 家里闲置电脑、Windows 优先、开箱即用；**不得 fork/reuse PCL2**（只借鉴）。
- Go `F:\tools\go`(GOPROXY goproxy.cn)、JDK21 `F:\Java\java 21`；Git Bash 路径须 `F:/...`。
- 模块 `github.com/su-ni/ServerBox`；`internal/{daemon,api,fetch,companion,settings,modpack}`+`web/src`+`cmd/{server,desktop}`。
- `build.sh`→`serverbox.exe`(守护进程+界面)；`build-desktop.sh`→`serverbox-desktop.exe`(Wails 壳直连 23456，
  **不嵌前端**)。改前端只跑 build.sh；跳前端=`wails build -s`；v2.16 Windows 无 CGO。

## 产品原则
①不静默失败，没接的明说卡在哪；②错误可操作(preflight 带 fix)；③「可见不可点」别当默认答案，先探接口；
④**集合字段先补空集合**(Go nil→`null`)；⑤调试开关要可见；⑥**别劫持滚轮**；⑦界面承诺都要有入口；
⑧**上屏字符串是纯文本**——`**` 会原样显示(断言查不出、只有截图能发现)。

## 停服判定
`Send()` 认 stop/end 置 `intentional`(**在写 stdin 之前**)→`track()` 认 `Stopping the server`→兜底「退出码0 且 `everReady`」；
纯函数 `stoppedGracefully()`/`shutdown_test.go`。别改模糊匹配(插件有 "Stopping Server Plugin")。
⚠ Mohist 本地化就绪行→`isReadyLine` 须**同时**含 `Mohist`+`启动成功`。

## 进程与路径
- jar 走 `resolveJar()`/`JarPathOf()`，别直读 `inst.JarPath`；判崩溃用标记不用 status。
- Oracle `javapath\java.exe` 是转发器→归一 `<java.home>/bin/java`；厂商读 `java.vendor`。
- 崩溃重启：退避 5s→20s→60s，3 次停；跑满 180s 清零。`store.All()` 顺序随机，用前排序。
  启动**路径驱动**：`buildArgs` 只看 `isArgsFile(target)`。

## 鉴权 / 下载
- 默认不挡人(`requireAuth=false`+回环豁免)；会话用 **Cookie**；令牌只存 sha256；改密码要旧密码+revokeAll；
  前端 401 广播 `sbx-need-auth`。回环判定**不看 `X-Forwarded-For`**。
- **两客户端不合并**：`apiClient`(30s)vs`dlClient`(**不设总超时**)，靠 ctx+分阶段超时。
- 校验三件套：对 API `size`→`is_zipfile`→换源重下。⚠ `curl -o /dev/null` 谎报 `size=0`；**200 ≠ 下全**。
- 元数据小请求重试 3 次(`doWithRetry`)，只重试请求失败。
- ⚠ 下载产物清理：**成功=rename 转正无残留**；**取消/中断=刻意保留 `.part`**（续传用，不自动清）；
  `removePart` 仅 校验败/416/换源；整合包 `serverbox-mrpack-*` 临时目录三路 `release()` 都清（含取消）。

## UI 规范（色号/字阶/级联层/排版细节见 DETAILS；真相源=`index.css` @theme）
- **零圆角零渐变**、像素 SVG 图标(**禁 emoji**)、对比度≥4.5:1、mono 只给机器数据、强描边只给容器与 `.btn`。
- 配色：草绿 accent＝主 CTA＋运行态；青 cyan＝选中/交互/数据高亮。⚠ 别把 accent 改青——运行状态点会跟着变。
- ⚠⚠ **@layer 层优先于特异性**：跨层同名属性被静默压制；**design.css 冻结／overrides 可改**。
- 数值用 StepInput(**禁 `input[type=number]`**)；⚠ **禁嵌套 `.px-card`**(组内用 `.jlist`)。
- 正文渲染用自研 `markdown.tsx`(**禁 `dangerouslySetInnerHTML`**)。
- 装前置走 `DepDialog`(两阶段 `find→vers`)，**不许静默装最新**；每格按钮只锁自己(`busyId`)。
- **一人一行**：白名单/OP/封禁三态别拆三段；操作走方形 `.ibtn`(32×32)；`PlayerBadge`(不联网)。
- ⚠ **交互态一律可寻址**（靠 URL 预置状态）；详情页外壳 `ProjectShell.tsx`；纯函数在 `projectMeta.ts`。
- ⚠⚠ **深链必须带 `page`**：正确 `?page=plugins&mod=market[&pm=]`；⚠ **`pm` 必须配 `mod=market`**（少了会落
  「已安装」列表、详情组件不挂载——症状像「组件坏了」，10-09 白查半小时）。waitFor 不成立先怀疑 URL。
- ⚠⚠ **测试/探针产物为空时，第一个怀疑对象是「自己拼的 URL」**，不是产品代码。
- ⚠ **一页只堆一组**；⚠⚠ **排版体检必须覆盖空态**；吸顶抽搐＝`useStableScrollRange` 补回；
  ⚠⚠ **「界面偏松」是密度问题**——真解法＝收紧 padding/行高/字号，别 `flex:1` 硬撑。
- ⚠ **粘底滚动统一走 `useStickyScroll.ts`**（控制台+概览共用；三道判定＝回波豁免/上翻守卫/回波令牌）。
  ⚠⚠ 原生 scroll 事件**异步**派发——程序写 scrollTop 与回波到达之间有竞态（peel 被自己的回波静默复活＝
  「滚不动」）。回归＝`probe_follow` 11/11（相位锁定＋max/回贴判据，细节见 DETAILS §跟随）。
- ⚠⚠⚠ **「还卡」先测 HiDPI**：headless dSF=1 量不出卡，加 `deviceScaleFactor:2` 才复现；热点常是 `(program)`
  （排版/光栅）。细节见 DETAILS §星图/性能。

## MC 百科星图（**细节全文在 DETAILS §MC 百科关系星图，动手前必读**）
- 数据源＝站方**静态 JSON** `/static/public/json/relation/class/<id>.json`（**GBK**）；⚠ 站方那张图
  **其实不会转**、点击＝直接跳百科 —— 「会转 / 拖拽转 / 点了就地看详情」是**用户要的增量**。
  ⚠⚠⚠ rAF 动画（yaw/pitch/呼吸）**存 ref、绝不进 React state**；节点与边由 effect 全权建，React 只渲染空容器。
- ✅ **形态＝旋涡星系（10-10 实现）**：`spiralLayout()` 极坐标分层 —— 中心固定、半径按 `deep` 几何级数带、
  角度用**层内进度**（每层一小段螺旋、接起来＝一条连续旋臂）、z＝盘厚（中心鼓、缘薄）。
  ⚠⚠ 两条铁律：① 角度**不能**由「半径」或「全局进度」推（同层挤同相位 → 302 点只占画布 26% 宽）；
  ② 归一化用**半径等比 `1/maxR`**，**不是**包围盒（圆对称；包围盒会把中心推偏、把旋臂拉歪）。
  ⚠ 层参数单份出处(`baseROf/halfBandOf/armThetaAt`)，骨架与布局共用，否则「行星在一边、虚线在另一边」。
  ⚠ 核球辉光用 `--color-cyan`（**别用节点分类色**：深蓝在深蓝底上＝隐形）；边 0.12 + `.mcg-edge-hot` 只亮邻居，
  且 **opacity 值必须写在 CSS**（CSS 优先级高于 SVG presentation attribute，两处各写一个数迟早对不上）。
- ⚠⚠⚠ **3D 感三件套（10-10 修）**：① 自转＝绕**竖直轴 z 转盘**（绕 y 轴写＝翻滚；Y=0 时两公式等价、
  低俯角看不出来，抬视角才露馅）；② 初始 pitch **0.62(35.5°)**（16° 俯视≈2D 转圈）；
  ③ 深度雾 12 档对准实测用档（`BUCKETS`：常态 nth-child 4~9 → 0.60→1.0）。
  ⚠ `ZMIN/ZMAX=±1.2`、`D=3.2` 与投影链绑定，换链必须重算；改视角先拍「转过 30°+」对比截图做基线。
- ⚠⚠ **「复位视图」不得碰自转开关**（10-10 移除过一行 `setSpin(true)`：停转→复位→自转重启；
  探针「先停后复位」还会被静默覆盖）；回归断言 probe_spin_axis 尾部 `RESET_KEEPS_SPIN_OFF/ON` 双向。
- ⚠ 拖拽探针先 `scrollIntoView`（CDP 用视口坐标，画布在视口外时事件落 HTML、拖拽静默不启动，
  trace 里 target=HTML 即实锤）；旋转轴定量判据＝位移/半径 RATIO 恒定（probe_spin_axis，实测 0.563~0.565）。
  ⚠ **截图同理**：`Page.captureScreenshot` 只拍视口——画布在视口外时「相隔 N 秒」对比图会双双截到页顶
  （假绿，10-10 踩过）；有效截图口径＝`docs/verify-mcg/star3d_*.png`（下划线；dash/`*_INVALID-pagetop` 为历史）。
- ⚠⚠⚠ **每帧 `appendChild` 节点会掐断点击**（Blink 手势中途撤指针捕获→不发 click；症状＝点击间歇失效、重开首点又能中）：
  分桶搬移**排除中心**（`cur!==bk && !isCenter`）；诊断 `tools/probe_center_click.mjs`（lostpointercapture 早于 pointerup 即实锤）。
- 面板 `.mcg-side` 贴内容高（`bottom:auto; max-height:100%`；**别撑满全高 + `margin-top:auto`**＝148px 空洞）；
  简介「📖简介」前缀 → `mcmodTrimLead()` 双调用点（plainText 出口＋readDisk 读盘，老缓存即清）。
- ⚠ 滚轮＝**缩放**（**指针锚定** f=1.1、上限 3.2）；**右键/中键/Shift+左键拖＝平移**（左键仍是转）；
  尺寸 `k^0.3` 亚线性（旧等比缩放＝整图放大、间距拉不开＝用户反馈；10-10 修）。`.mcg-canvas` 必须
  `touch-action: pan-y`（**绝不能 `none`**）；瘦身只能按 `deep` 分层（按分类砍→剩 41 个无边孤岛）；
  过滤后重归一化用 **min/max 包围盒**；深链 `?page=plugins&mod=market&pm=<id>`。
- ⚠⚠ **快捷安装＝两段搜索**（先带实例过滤、后脱过滤）＋名字候选（剥 `[前缀]`/`(后缀)`/括号拉丁名）＋
  **title/zhName 精确相等**（禁 items[0] 瞎猜）；全空→诚实 toast。旧版死因＝单次+硬过滤（Fabric 名×forge 实例=全灭，
  对照实验 5 样本 4 空）。探针 `tools/probe_quickinstall.mjs`（判据 ≥1 次弹层）。探针坑：hover 卡片挡合成点击
  （点击前先 mouseMoved）、页面表达式在模板串里 `\n` 要写 `\\n`。

## 切页转场（10-10 改为**承接式**；全文见 DETAILS §承接式切页）
- **切页=承接式**（PPT 平滑默认行为）：外壳/页签不动，`.content` 容器 out 130ms（下沉 10px 淡出）→ swap →
  inn 200ms（**从 -11px 接进**）；连点=清动画直接换（不叠）。⚠ `out.cancel()` 与 `inn.animate()` 必须**同一帧**，
  否则闪一下全亮；动效判据统一走 `blockReveal.ts` 导出的 `stillMotion()`。
  幕布式 blockReveal **已从切页与 switchView 全部撤下**（模块留档）；换工作区（向导/市场/详情↔面板）＝
  **opacity 淡入淡出**（out 130 → swap → inn 200），**禁 transform**——根是滚动容器（.main/PageFrame），
  挂 transform 会生成 containing block 搞崩 sticky。⚠ 根节点一律按**结构**认：`shell` 的最后一个子元素
  （向导的根是外层 `.scroll-thin`，`.wz-shell` 在它里面——10-10 探针按类名找把自己坑了）。
- 页签指示条 `.tab-glide`（原生 `.tab.on::after` 已隐）：切页写 left/top 走 CSS transition 滑行（--dur-slow），
  首挂/RO 走瞬移。⚠⚠ **ResizeObserver 只能 mount 装一次**——observe 必有初始回调，跟 `[page]` 重建
  会把刚启动的滑行 `transition:none` 砍成瞬移（10-10 真凶）。
  判据＝`tools/probe_pageflow.mjs` 的 FLOW：indLeft 有连续中间值（71→330→554，≈300ms）。

## 石头拼合过场（10-10 二轮 → **「进入加载」幕**；全文见 DETAILS §石头拼合过场）
- `web/src/stoneBuild.ts`：**铺满视口**的石阵，从**左右两侧**一列列落下拼到正中 →
  拼满闪白 → **logo 段（中央拼草方块）** → 定格内 swap → 按同序散场（先闪白再没）。四材质。
- **定位（用户 10-10 拍板）**：进入加载幕 —— 桌面端一开 / 网页一进铺首屏，登录通过再铺一次
  （App.tsx 的 `splash()`）；**不上切页、不上换视图**。`cover:true` 铺满全屏 + 散场期 smoothstep 淡出
  （角点 alpha 255→0 实测）。**总时长不随屏幕变**（4 档实测 **3307~3331ms**）。
- **logo 段（用户三轮拍板）**：拼满后中央 min(10,行列) 格**从中心向外**翻新成草方块 logo（IconBlock 16×16
  抽稀、四色全 plain、约 0.65s）。⚠⚠ 彩砖取材质**不能走 `texOf()`**——它 hash 随机选色、图案被整个无视＝
  随机马赛克（v1「不清晰」根因）。
- ⚠ **自动化跳过是产品行为**：`navigator.webdriver===true` 不放幕（headless 里恒 true）——探针看页面本身；
  想验幕布要在 CDP 注入里把它盖成 false（`Object.defineProperty`）。no-anim 由 stoneBuild 自己短路。
- 自适应：cell=clamp(floor(短边×0.82/9),36,**144**)+`floor(短边/9)` 兜底；**动画途中 resize 会重排**
  （per-gen 摘钩，别让旧轮反打）。
- ⚠⚠ **时间线常数带 24ms 下限**（`S=ms=>Math.max(24,Math.round(ms/speed))`）：T_FULL=1462、**T_LOGO0=1582**、
  T_SWAP=2592、guard=2842、T_V0=2632、**T_END=3298**。拦截/断言前**按公式重算**，别脑算（10-10 白拦一轮）。
- ⚠⚠ **假 cAF 必须按 id 记账（Map）**：换页时 React commit 跑被删子树组件的 cleanup→`cAF(自己的帧)`，
  「清全队列」会把石头的 pending 帧误杀（症状＝定格不动、永不休止）。
- 探针：`tools/probe_stone.mjs`（手摇 rAF + 语义定格，旧 6 幕）+ **`tools/probe_splash.mjs`（**57** 项全绿：
  4 档屏幕×时长一致 / 幕布 alpha 曲线 / 自动化跳过 / no-anim 跳过 / 登录二幕(Fetch mock) / 换视图不铺幕 / logo 草绿占比×4 档）**；
  截图 `docs/probe-splash/`（含 `logo-hold-*.png` 特写）；特写脚本 `tools/probe_logo_closeup.mjs`。

## 本机运行环境（10-10 补）
- 面板默认 `:23333`（所有网卡）；desktop 壳 23456。⚠⚠ **两个进程可同听 23333**（旧的绑 127.0.0.1、
  新的绑 0.0.0.0）→ 127.0.0.1 命中**绑定更具体的旧的**，症状＝改了前端却加载旧页面（curl JS hash 对不上即实锤）。
  ⚠ 判死活**别拿单次 curl**（会抖）：看 `netstat -ano | grep :23333 | grep -i listen` 的 (地址,PID) + 连测 10 次；
  `tasklist /fo csv | grep` 带 `-a`（UTF-16 被二进制吞）；本机**没有 wmic**（command not found）。exe 短哈希＝**sha256 前缀**。
  ⚠ 本机实例会**周期性被外部终结又自动拉起**（10-10 观测：同一 build 换 PID 续跑，仍绑 :23333）——
  判「用户看到的是不是新版」**只认 curl 首页 bundle 名**，别认 PID/进程存活。
  ⚠⚠ **工作区可能有并行 agent（codex 等）在开发/构建/重启**（10-10 实锤：14:59 有人重建 dist+exe 并重启面板）——
  写记录/验收前**重查 bundle 名**；**别动对方的实例**，自己也别在源码上跟对方抢改。
- ⚠ Git Bash 的 `taskkill` 不可用（`//PID` 被吞、`cmd //c` 也不灵）→ `node -e "process.kill(<pid>)"`（实测可靠）。
- ⚠ 固定 Chrome profile 复用前**删 `DevToolsActivePort`**（残留旧端口 → ECONNREFUSED，像 Chrome 没起来）。
- ⚠⚠ **沙箱三坑（10-10 实测）**：① `vite build` 沙箱内报 rolldown binding 错（真错被吞）→ **放行即过**；
  ② PS 工具 **`Add-Type` 被安全策略拦** → 桌面截图/置顶走 **Python ctypes + Pillow**（venv 已装 pillow）；
  ③ 脚本 `Popen` 起的 GUI 子进程**随 bash 调用被沙箱收走** → 持久窗口用 **Bash run_in_background 直起 exe**
  （且不自动抢前台，需 `SetWindowPos` 置顶；标题=`ServerBox 控制台`）。
- ⚠ `tasklist //FI` 在 Git Bash＝无效参数（曾 `2>/dev/null` 吞成「查不到」）→ 用 `tasklist | grep -a`。

## 工作守则
1. Edit 会假成功→grep 回验；**同文件多改要串行**；反复失败 Read+Write 重写。类型检查 `npx tsc -b --force`(`--noEmit` 假过)。
2. 自定义类写进 `@layer components`；多文件替换**落 `.py` 再跑**。⚠ `curl -d` 中文按 GBK 碾碎→走 python urllib。
3. 修 bug 的测试要**证明抓得住旧代码**(拆修复应变红)。
4. 构建/重启全套命令在 DETAILS §本机构建与运行环境；**先停旧 PID→netstat 验→再起→对哈希**，别凭记忆敲。
5. 行尾别按目录一刀切(`internal/**/*.go` LF；`web/**` 多 CRLF 但 `types.ts`/`ActiveDownloads.tsx` 是 LF)。
6. 无 gcc→`go test -race` 不可用。接三方 API 先打真接口(skill `external-api-probe`)。
7. **`position:fixed` 覆盖层一律 `createPortal`**；入场动画用 `backwards`，别 `fill-mode:both`。
8. 无头复核见 skill `headless-ui-verify`；`uiprobe.mjs` 并行安全；PNG 可 Read（先试读）。
9. 无头里 `cmd &` 起的后端会随 shell 被收，用 `run_in_background`。
10. ⚠⚠ **报异常先怀疑探针**——串台/撞口症状是「量到空态或别人的页面」。
11. ⚠ Windows 子进程沿用父控制台；脱离用 `DETACHED_PROCESS|CREATE_NEW_PROCESS_GROUP`。重建后**必须验哈希**。
12. ⚠⚠ **别在 `setState` updater 里收集要删的 key**；长任务「成没成」**问任务状态**，别拿产物出现当判据。
13. ⚠ 行里加按钮时 `<button>` 容器（如 `.modline`）必须先改 `<div>`。
14. ⚠⚠ **测「滚不动」先打印 `max scrollTop` 并给足余量**；⚠ 滚动问题必须区分**鼠标滚轮 vs 触摸板/触屏**——
    `touch-action` 只作用于后者（`tools/probe_touch.mjs`）。
15. ⚠⚠ **Go 块注释里出现 `*/` 会把注释提前闭合**（如 `libraries/**/*.jar` 里的 `*/`）——后面的中文全变
    「illegal character」报错刷屏。文件头长注释一律用 `//` 行注释，不用 `/** */`。

## 游戏规则编辑器（10-09 完成；全文见 DETAILS §游戏规则）
- 后端 `daemon/gamerules.go`：`/gamerule` 无参枚举没接口——规则全集从 jar 的 `assets/minecraft/lang/en_us.json`
  提取（bundler/Paper/Forge 布局都验过），拿不到退基线表；逐条单查解析（词边界+行尾值+中间有说明文字，防聊天行冒名）。
- API：`GET/POST /api/instances/{id}/gamerules`；409 reason=`no-server`/`not-ready`、404 `unknown-rule`。
- 前端 `RulesPanel.tsx`：tabbar 第 5 项（图标 `i-rule`）；开关立即生效、数字改完按「写入」；值以服务端回报为准。

## 关键接口 / 待办
- 接口清单在 DETAILS §接口清单（更全 grep `mux.HandleFunc`）。
- ✅ 10-09：模组市场；三项 UI 修复；**游戏规则编辑器写完**（后端+API+前端，单测 22 个全绿）。
- ✅ 10-10：**星图＝旋涡星系 + 右侧面板按设计稿重做 + 三补丁**（面板收紧/中心点击/emoji 剥离）。
- ✅ 10-10：**切页改承接式 + 页签指示条滑行**（探针 `probe_pageflow` 五项全绿，exe `8e973fff`）。
- ✅ 10-10：石头拼合接入 switchView（exe `6dc24494`）→ **当日二轮撤下**：用户拍板改为「进入加载幕」
  （首屏/登录通过）+ 换视图改 opacity 承接；`probe_splash` 49/49。⚠ 线上 exe 尚未重建（并行 agent 占用中，
  待其空闲再跑 build.sh；源码已就绪）。
- ✅ 10-10：**展示通道就位**：`bin/serverbox-show.exe`（同源码新构建 + 新前端）+ `.showcase-data`（2 实例副本，
  路径已改写）+ `:23456`；desktop 壳连它＝看新前端的真机通道（双击 `bin/serverbox-desktop.exe` 即达）。
  **石头真机连拍实证 ✅**（0.2s 白 → 0.8s 两侧落砖 → 1.4s 拼满居中 → 2.0s 淡出 → app）。⚠ 正式
  `bin/serverbox.exe` + `web/dist` 仍待 codex 空闲重建。
- ✅ 10-10：**星图操作感三修复**（右键平移/指针锚定缩放/尺寸亚线性）＋**快捷安装重写**（两段搜索+精确匹配）——
  探针全绿 + sweep 42/42 + **用户真机验收通过**。⚠ **线上 bundle 名一天多变**（`8PAud41u`→`Yp7IgLsJ`）：
  跨会话下结论前必 `curl / | grep bundle` 重查，并对「当前版」重跑对应探针（19:46 换代后两条已复验全绿）。
- ✅ 10-10 晚：**跟随两个竞态修复**（重贴守卫＋回波令牌）＋**整合包一键装最新版确认框/进度接回**
  ＋石头三轮＝「铺满视口」——`probe_follow` 11/11（A/B 旧产物 T2/T5 红）、`probe_packconfirm` 8/8、
  桌面窗口连拍实证；show exe 重建 `71c02849`（DETAILS §跟随/§整合包/§石头）。⚠ 正式 exe 仍未动。
- **下一件**：#30 真机验收规则编辑器（vanilla 起服读全量→改 keepInventory→停服 409；无头 UI；sweep 回归）。
- 之后：M6 托盘/自启 → CurseForge → 存档管理/监控历史 → Java 版本匹配。
- 遗留：NeoForge/BungeeCord 仍 false；原版/纯 mod 服无实时数据；曾一次 instances 空 `{}` 未定位。
