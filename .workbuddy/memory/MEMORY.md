# 项目：开服器（MC 服务器管理器）

## 定位与约束
- 面向「家里闲置电脑」的个人用户：Windows 优先、开箱即用、30 秒开服。区别于 PCL/HMCL（客户端）与 MCSManager（运维面板）。
- **不得 fork/复用 PCL2 代码**（LICENCE 强制蓝色主题+署名+开源），只借鉴产品思路。
- 工具链全在 F 盘。Go 在 `F:\tools\go`（GOPROXY goproxy.cn）；JDK21 在 `F:\Java\java 21`。
- Git Bash 给 Go 传路径必须 `F:/...` 形式，`/f/...` 会被判相对路径。

## 技术栈
Go 单 exe 守护进程 + React/TS/Tailwind v4 + Wails（**未开始**）。多实例、Job Object 托管子进程、SSE 推日志。
**后端不托管前端（embed 未做），单 exe 还不能开箱即用** —— 这是 P0 唯一硬门槛。

## 目录要点
`internal/daemon`（实例/进程/preflight 体检/日志落盘/properties）、`internal/api`、`internal/fetch`（jar 下载）、
`internal/settings`（鉴权）、`web/src/components`、`docs/BACKEND-PLAN.md`（**剩余工作权威清单**）、`tools/fakeserver`。

## 关键决策（别改回）
- **崩溃重启护栏**：退避 5s→20s→60s，3 次后停下给人话提示；跑满 180s 才清零；崩溃存最后 200 行到 `data/logs/<id>-crash-*.log`。
- **鉴权**（internal/settings + api/auth.go）：默认不挡人（requireAuth=false+回环豁免），只在「非回环+无保护」红警告；会话用 **Cookie**（EventSource 设不了请求头）；不看 X-Forwarded-For；令牌只存 sha256；改密码必须旧密码且 revokeAll。前端 401 广播 `sbx-need-auth`，登录闸门在 App 渲染之前。
- **下载源**（实测 2026-10-04）：PaperMC 真地址 `fill.papermc.io/v3`（api.papermc.io 全废）；下载域名 `fill-data.papermc.io`；下载项键名 `server:default`；builds 顶层是数组；**默认选最新 STABLE 构建**（26.3 零 STABLE）；Java 要求以原版元数据 `javaVersion.majorVersion` 为准；BMCLAPI 只镜像原版；续传必须先把 .part 喂进 hash。
- **产品原则**：①不静默失败，没接的明说卡在哪；②错误必须可操作（preflight 每条带 fix）。
- store.Store 的 All() 遍历 map 顺序随机，用前必须自己排序。

## UI 规范（用户拍板）
- Token：底 `#111311`/面板 `#1c201d`/线 `#3d453d`/文字 `#eef1ec·#b7c2b4·#8b978a`/主绿 `#7cc24f`/警 `#f2c14e`/危 `#e0574c`/青 `#4aedd9`。零圆角零渐变，像素 SVG 图标（禁 emoji）。
- 对比度正文 ≥4.5:1（ink-3 已修到 6.1）；分隔线 1.5-1.9 是刻意的。字阶封闭 7 档（22/16/14/13.5/12/11.5/11）；mono 只给机器数据。强描边只给容器和 .btn，**chip 不描边**。
- 版式靠 `ui.tsx` 的 `ListRow`/`Field` 钉死；SectionHead.idx 只放序号；可搜索内容别写死 JSX。
- 动效 140/220/300ms，`cubic-bezier(.16,.84,.44,1)` 不许弹跳；留 `body.no-anim` 开关；滚动容器只做 opacity 动画（transform 会毁 sticky）。
- 配置页：左分组导航+右侧一组，滚到底切组，搜索切全部命中；`.crow` 标签列 330px 控件紧随；滚轮劫持前先判断面板能否滚。
- 数值用 StepInput（禁 `<input type=number>`）；滚动条全局统一。
- 可寻址状态：`?page= ?view=create&step=N ?nosse=1 ?g= ?q= ?probe=1 ?drawer=1 ?restore=1`。

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

## 后端接口已实现（均实测）
preflight 六项体检 / eula / fix(accept_eula·sync_port·rescan_java) / PUT 实例（整体覆盖）/
properties GET|PUT（23 字段带 kind/label/hint/options，未知键只读附尾）/ fetch sources|versions|builds|tasks /
server-jar 下载 / start 体检不过回 409+整份报告 / 全局设置+鉴权 / Paper+原版 jar 一键下载（含断点续传+SHA+轮询进度）。
