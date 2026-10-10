# EnderBox 开服器/全能型盒子：Go 单 exe + React

> ⚠ 注入上限约 8KB——长参考进 `memory/DETAILS.md`（动手前先读对应节）；进度账 `docs/NEXT-STEPS.md`。
> 只写「结论+判据/测试名」，先查重再加。网络/GitHub 在**用户级** `~/.workbuddy/MEMORY.md`。

## 工程（10-10 改名 ServerBox→EnderBox）
- 家用闲置机、Windows 优先、开箱即用；**不 fork/reuse PCL2**（只借鉴）。
- Go `F:\tools\go`(GOPROXY goproxy.cn)、JDK21 `F:\Java\java 21`；Git Bash 路径须 `F:/...`。
- 模块 `github.com/su-ni/EnderBox`；`internal/{daemon,api,fetch,companion,settings,modpack}`+`web/src`+`cmd/{server,desktop}`。
- `build.sh`→`bin/enderbox.exe`(守护进程+界面)；`build-desktop.sh`→`bin/enderbox-desktop.exe`(Wails 壳直连 23456，**不嵌前端**)。改前端只跑 build.sh；跳前端=`wails build -s`；v2.16 Windows 无 CGO。
- 许可 **GPL-3.0**（`LICENSE`）；Companion 插件 `io.enderbox.companion`/`EnderBoxCompanion.jar`（`go:embed`，`tools/companion/build.sh` 重打）。初始实现已提交 **`b5628b4`**（零提交红 flag 已解）。

## 产品原则（八条）
①不静默失败，没接的明说卡哪；②错误可操作(preflight 带 fix)；③「可见不可点」先探接口；
④**集合字段先补空集合**(Go nil→`null`)；⑤调试开关要可见；⑥**别劫持滚轮**；⑦界面承诺都要有入口；
⑧**上屏字符串是纯文本**——`**` 会原样显示(断言查不出、只有截图能发现)。

## 停服判定
`Send()` 认 stop/end 置 `intentional`(**写 stdin 之前**)→`track()` 认 `Stopping the server`→兜底「退出码0 且 `everReady`」；
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
- ⚠ 下载产物清理：**成功=rename 转正无残留**；**取消/中断=刻意保留 `.part`**(续传用，不自动清)；
  `removePart` 仅 校验败/416/换源；整合包 `enderbox-mrpack-*` 临时目录三路 `release()` 都清（含取消）。

## UI 规范（色号/字阶/级联/排版细节见 DETAILS；真相源=`index.css` @theme）
- **零圆角零渐变**、像素 SVG 图标(**禁 emoji**)、对比度≥4.5:1、mono 只给机器数据、强描边只给容器与 `.btn`。
- 配色：草绿 accent＝主 CTA＋运行态；青 cyan＝选中/交互/数据高亮。⚠ 别把 accent 改青——运行状态点会跟着变。
- ⚠⚠ **@layer 层优先于特异性**：跨层同名属性被静默压制；**design.css 冻结／overrides 可改**。
- 数值用 StepInput(**禁 `input[type=number]`**)；⚠ **禁嵌套 `.px-card`**(组内用 `.jlist`)。
- 正文渲染用自研 `markdown.tsx`(**禁 `dangerouslySetInnerHTML`**)。
- 装前置走 `DepDialog`(两阶段 `find→vers`)，**不许静默装最新**；每格按钮只锁自己(`busyId`)。
- **一人一行**：白名单/OP/封禁三态别拆三段；操作走方形 `.ibtn`(32×32)；`PlayerBadge`(不联网)。
- ⚠ **交互态一律可寻址**（靠 URL 预置状态）；详情页外壳 `ProjectShell.tsx`；纯函数在 `projectMeta.ts`。
- ⚠⚠ **深链必须带 `page`**：正确 `?page=plugins&mod=market[&pm=]`；⚠ **`pm` 必须配 `mod=market`**（少了落「已安装」列表、详情组件不挂载）。waitFor 不成立先怀疑 URL。
- ⚠⚠ **测试/探针产物为空时，第一个怀疑对象是「自己拼的 URL」**，不是产品代码。
- ⚠ **一页只堆一组**；⚠⚠ **排版体检必须覆盖空态**；吸顶抽搐＝`useStableScrollRange` 补回；
  ⚠⚠ **「界面偏松」是密度问题**——真解法＝收紧 padding/行高/字号，别 `flex:1` 硬撑。
- ⚠ **粘底滚动统一走 `useStickyScroll.ts`**（控制台+概览共用；三道判定＝回波豁免/上翻守卫/回波令牌）。
  ⚠⚠ 原生 scroll 事件**异步**派发——程序写 scrollTop 与回波到达之间有竞态（peel 被自己的回波静默复活＝「滚不动」）。回归＝`probe_follow` 11/11。
- ⚠⚠⚠ **「还卡」先测 HiDPI**：headless dSF=1 量不出卡，加 `deviceScaleFactor:2` 才复现；热点常是 `(program)`。

## MC 百科星图（**细节全文在 DETAILS §MC 百科关系星图**）
- 数据源＝站方**静态 JSON** `/static/public/json/relation/class/<id>.json`（**GBK**）；站方原图不会转，增量是「会转/拖拽转/点就地看详情」。
- ✅ **形态＝旋涡星系**：`spiralLayout()` 极坐标分层——中心固定、半径按 `deep` 几何级数、角度用**层内进度**、z＝盘厚。
  ⚠⚠ 两条铁律：① 角度**不能**由「半径/全局进度」推；② 归一化用**半径等比 `1/maxR`**，**不是**包围盒。
  ⚠ 层参数单份出处(`baseROf/halfBandOf/armThetaAt`)；核球辉光用 `--color-cyan`（别用分类色）。
- ⚠⚠⚠ **rAF 动画存 ref、绝不进 React state**；节点与边由 effect 全权建，React 只渲空容器。
- ⚠⚠⚠ **3D 感三件套**：① 自转＝绕**竖直轴 z 转盘**（绕 y 写＝翻滚）；② 初始 pitch **0.62(35.5°)**；③ 深度雾 12 档 `BUCKETS`。`ZMIN/ZMAX=±1.2`、`D=3.2` 与投影链绑定。
- ⚠ **「复位视图」不得碰自转开关**（RESET_KEEPS_SPIN_OFF/ON 双向）。
- ⚠ 滚轮＝**缩放**(指针锚定 f=1.1、上限 3.2)；右键/中键/Shift+左键拖＝平移；尺寸 `k^0.3` 亚线性。`.mcg-canvas` 必须 `touch-action: pan-y`（**绝不能 `none`**）。
- ⚠⚠ **每帧 `appendChild` 节点会掐断点击**（分桶搬移排除中心）；诊断 `tools/probe_center_click.mjs`。
- ⚠⚠ **快捷安装＝两段搜索**(先带实例过滤、后脱过滤)＋名字候选(剥前缀/后缀/括号拉丁名)＋**title/zhName 精确相等**；全空→诚实 toast。探针 `tools/probe_quickinstall.mjs`。

## 切页转场（承接式；细节 DETAILS §承接式切页）
- 切页＝承接式：外壳/页签不动，`.content` out 130ms(下沉 10px 淡出)→swap→inn 200ms(从 -11px 接进)；连点清动画直接换。
  ⚠ `out.cancel()` 与 `inn.animate()` 必须**同一帧**，否则闪全亮；判据走 `blockReveal.ts` 的 `stillMotion()`。
- 换工作区(向导/市场/详情↔面板)＝**opacity 淡入淡出**(out 130→swap→inn 200)，**禁 transform**(根是滚动容器，挂 transform 搞崩 sticky)。
  ⚠ 根节点按**结构**认：`shell` 最后一个子元素。
- 页签指示条 `.tab-glide` 走 CSS transition 滑行；⚠⚠ **ResizeObserver 只能 mount 装一次**——跟 `[page]` 重建砍成瞬移。判据 `tools/probe_pageflow.mjs` FLOW。

## 石头拼合过场（进入加载幕；细节 DETAILS §石头拼合过场）
- `web/src/stoneBuild.ts`：铺满视口石阵从两侧落拼→闪白→logo 段(中央草方块)→定格 swap→散场。
- 定位＝进入加载幕(桌面一开/网页一进/登录通过再铺)；**不上切页、不上换视图**。总时长不随屏变(3307~3331ms)。
- ⚠ **自动化跳过**：`navigator.webdriver===true` 不放幕(headless 恒 true)；想验在 CDP 盖 false。no-anim 由 stoneBuild 短路。
- ⚠ **假 cAF 必须按 id 记账(Map)**：换页清全队列会误杀石头 pending 帧(定格不动、永不休止)。
- 探针 `tools/probe_splash.mjs`(57 项全绿)/`tools/probe_logo_closeup.mjs`。

## 本机运行环境
- 面板默认 `:23333`(所有网卡)；desktop 壳 23456。⚠⚠ **两个进程可同听 23333**(旧绑 127.0.0.1、新绑 0.0.0.0)→命中旧的，改前端却加载旧页(curl JS hash 对不上即实锤)。判死活看 `netstat`+连测 10 次；没 wmic；exe 短哈希＝sha256 前缀。
- ⚠ 本机实例周期性被外部终结又自动拉起→判新版**只认 curl 首页 bundle 名**。⚠ **工作区可能有并行 agent(codex 等)在构建/重启**→写记录前重查 bundle 名；别动对方实例。
- ⚠ Git Bash `taskkill` 不可用→`node -e "process.kill(pid)"`；固定 Chrome profile 复用前删 `DevToolsActivePort`。
- ⚠⚠ **沙箱三坑**：① `vite build` 沙箱报 rolldown binding 错(放行即过)；② PS `Add-Type` 被拦→桌面截图标/置顶走 Python ctypes+Pillow(venv 已装)；③ `Popen` 起 GUI 随 bash 被收→持久窗口用 Bash `run_in_background` 直起 exe。
- ⚠ `tasklist //FI` 在 Git Bash＝无效参数→`tasklist | grep -a`。

## 工作守则
1. Edit 会假成功→grep 回验；**同文件多改串行**；反复失败 Read+Write 重写。类型检查 `npx tsc -b --force`(`--noEmit` 假过)。
2. 自定义类写进 `@layer components`；多文件替换**落 `.py` 再跑**。⚠ `curl -d` 中文按 GBK 碾碎→走 python urllib。
3. 修 bug 测试要**证明抓得住旧代码**(拆修复应变红)。
4. 构建/重启：先停旧 PID→netstat 验→再起→对哈希，别凭记忆敲。
5. 行尾别一刀切(`internal/**/*.go` LF；`web/**` 多 CRLF 但 `types.ts`/`ActiveDownloads.tsx` 是 LF)。
6. 无 gcc→`go test -race` 不可用。接三方 API 先打真接口(skill `external-api-probe`)。
7. **`position:fixed` 覆盖层一律 `createPortal`**；入场动画 `backwards`，别 `fill-mode:both`。
8. 无头复核见 skill `headless-ui-verify`；`uiprobe.mjs` 并行安全；PNG 可 Read(先试读)。
9. ⚠⚠ **报异常先怀疑探针**——串台/撞口症状是「量到空态或别人的页面」。
10. ⚠ Windows 子进程沿用父控制台；脱离用 `DETACHED_PROCESS|CREATE_NEW_PROCESS_GROUP`。重建后**必须验哈希**。
11. ⚠⚠ **别在 `setState` updater 里收集要删的 key**；长任务「成没成」**问任务状态**，别拿产物出现当判据。
12. ⚠ 行里加按钮时 `<button>` 容器(如 `.modline`)必须先改 `<div>`。
13. ⚠⚠ **Go 块注释里出现 `*/` 会把注释提前闭合**(如 `libraries/**/*.jar`)→文件头长注释一律 `//` 行注释。

## 游戏规则编辑器（10-09 完成；细节 DETAILS §游戏规则）
- 后端 `daemon/gamerules.go`：规则全集从 jar `assets/minecraft/lang/en_us.json` 提取；API `GET/POST /api/instances/{id}/gamerules`；409 `no-server`/`not-ready`、404 `unknown-rule`。
- 前端 `RulesPanel.tsx`：tabbar 第 5 项；开关立即生效、数字改完按「写入」；值以服务端回报为准。

## 待办 / 进度
- ✅ 10-09：模组市场；三项 UI 修复；**游戏规则编辑器**(后端+API+前端，单测 22 全绿)。
- ✅ 10-10：**星图旋涡星系+面板重做+三补丁**；**切页承接式+页签滑行**(probe_pageflow 5/5)；石头改进入加载幕(probe_splash 57/57)；
  **展示通道就位**(show exe `:23456`+桌面壳)；**星图操作感三修复+快捷安装重写**(用户真机验收通过)；**跟随竞态修复+整合包一键装**(探针全绿)；
  **改名 EnderBox + GPL-3.0 + 首提交 `b5628b4`**。
- ⚠ 正式 `bin/enderbox.exe` 已重建含 logo 版(`9f8737ed`)；线上 bundle 名一天多变→跨会话下结论前 `curl / | grep bundle` 重查。
- 下一件：#30 真机验收规则编辑器 → M6 托盘/自启 → CurseForge → 存档管理/监控历史 → Java 版本匹配。
- **确认项（用户已选，未开工）**：统一作业框架+进度气泡(scope B)——抽 `internal/jobs`，把备份创建/备份恢复/整合包导出 三个分钟级同步操作异步化+进度气泡。
- 遗留：NeoForge/BungeeCord 仍 false；原版/纯 mod 服无实时数据；曾一次 instances 空 `{}` 未定位。
