# 开服器 技术设计

> 状态：需求已锁定，待工具链就绪后开工
> 更新：2026-10-04

## 一、定位

一台 Windows 机器上跑 Minecraft 服务端，从主力机 / 手机 / 任何浏览器上管理它。

**不做**：公网穿透、多用户权限体系、Docker、插件市场以外的在线服务。
**要做的差异化**：从零到「朋友能连进来」的路径要短到没有学习成本。

与同类产品的区别：

| | MCSManager | Crafty | 本项目 |
|---|---|---|---|
| 形态 | Web 面板，运维向 | Web 面板，单机 | 托盘桌面端 + Web 同源 |
| 装服务端 | 手动为主 | 有 | 选版本/整合包一键装好 |
| 公网 | 自己搞定 | 自己搞定 | 不做（用户自有方案） |
| 运行时 | Node.js 需预装 | Python 需预装 | 单个 exe，双击就跑 |

## 二、技术选型（已定）

- **后端 / 守护进程**：Go，编译为单个 exe
- **桌面端**：Wails v2（Go + React 前端 + 系统 webview）
- **前端**：React + TypeScript + Tailwind，一份代码三种呈现（桌面壳 / 桌面浏览器 / 手机浏览器自适应）
- **服务端支持**：原版、Paper、Fabric、Forge/NeoForge、Velocity、BungeeCord
- **Java**：自动下载 + 兼容系统已装的，按 MC 版本自动选 8 / 17 / 21
- **插件来源**：Modrinth 在线检索 + CurseForge + 手动上传
- **认证**：单管理员密码，登录后发长期 Token
- **实例**：多实例，进程池

## 三、架构

```
开服机（Windows）
└── server.exe                 单一进程
    ├── 进程池     N 个 MC 子进程，Job Object 托管
    ├── 日志跟随   tail latest.log + stdout pipe，环形缓冲 + WebSocket 广播
    ├── HTTP API   REST + WebSocket，认证中间件
    ├── 静态资源   嵌入 exe（go:embed），自带前端页面
    └── 存储       SQLite，实例/备份/设置

主力机
└── desktop.exe（Wails）        纯客户端，连开服机 API，托盘常驻 + 开机自启

手机 / 任意浏览器
└── 访问 http://<开服机IP>:<端口>  同一套页面自适应
```

## 四、目录结构

```
.
├── cmd/
│   ├── server/          守护进程入口
│   └── desktop/         Wails 桌面端入口
├── internal/
│   ├── daemon/          实例与进程生命周期
│   │   ├── instance.go
│   │   ├── process.go   Windows Job Object 托管
│   │   └── logtail.go   日志跟随
│   ├── java/            运行时检测、下载、版本映射
│   ├── installer/       Paper / Fabric / Forge / Velocity / BungeeCord
│   ├── modrinth/        Modrinth API 客户端
│   ├── curseforge/      CurseForge API 客户端
│   ├── backup/          备份与还原
│   ├── api/             HTTP 路由 + WebSocket Hub
│   └── store/           SQLite 持久化
├── web/                 React + TS + Tailwind 前端
├── docs/
└── go.mod
```

## 五、数据模型

```go
type Instance struct {
    ID            string
    Name          string
    Type          Type      // vanilla paper fabric forge neoforge velocity bungeecord
    MCVersion     string    // 1.20.4
    LoaderVersion string    // Fabric loader / Forge build
    Dir           string
    JavaPath      string    // 为空则自动选择
    JVMArgs       string
    MinMem, MaxMem int      // MB
    Port          int
    AutoStart     bool      // 守护进程启动时拉起
    AutoRestart   bool      // 崩溃自动重启
    MaxBackups    int
    Status        Status    // stopped starting running stopping crashed
    PID           int
    CreatedAt     time.Time
    LastStartedAt time.Time
}

type Backup struct {
    ID         string
    InstanceID string
    Path       string
    Size       int64
    Note       string
    CreatedAt  time.Time
}
```

## 六、关键实现要点（都是坑，先写下来）

### 6.1 Windows 子进程必须整棵树杀干净

直接 `cmd.Process.Kill()` 在 Windows 上只杀 java.exe，**崩遗留的子进程会占着端口**。用 Job Object：

```go
job, _ := windows.CreateJobObject(nil, nil)
info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
    BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
        LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
    },
}
windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, &info)
windows.AssignProcessToJobObject(job, procHandle)
```
兜底方案 `taskkill /PID xxxx /T /F`。

### 6.2 日志不要只信 stdout

Windows 中文环境下 Java 的 stdout 编码随系统 locale（GBK），Go 按 UTF-8 解会乱码。

**解法：双通道，文件为主。**
- 主通道：tail `logs/latest.log`（服务端自己写的，稳定 UTF-8）
- 辅通道：stdout pipe，用于 latest.log 还没生成时的启动早期输出
- 启动参数统一加 `-Dfile.encoding=UTF-8`，Java 18+ 再加 `-Dstdout.encoding=UTF-8`

### 6.3 停止要优雅

`stop` 命令 → 等待进程自己退出（超时 60s）→ 再 kill。硬杀会丢区块数据。

### 6.4 Java 版本映射

| MC 版本 | 需要 |
|---|---|
| ≤ 1.16.5 | Java 8 |
| 1.17 – 1.20.4 | Java 17 |
| ≥ 1.20.5 | Java 21 |
| NeoForge 1.20.5+ | Java 21 |

下载源优先国内镜像（清华 Adoptium / 华为云），失败回退官方。

### 6.5 各服务端安装方式不同，别写成一套

- **Paper**：PaperMC API v2，取 builds → 下载 jar
- **Fabric**：下载 installer jar，执行 `java -jar installer.jar server -mcversion <ver> -loader <ver>`
- **Forge**：下载 installer jar，执行 `java -jar installer.jar --installServer`，产物文件名带版本号需要探测
- **Velocity / BungeeCord**：直接下载 jar

装完自动写 `eula.txt`。

### 6.6 备份

第一版做全量 zip（世界目录 + 配置），排除 `logs`、`cache`、`*.tmp`。
写备份前先发 `save-off` / `save-all flush`，备份完 `save-on`——**热备份不这样做会拿到半个区块**。

## 七、API 轮廓

```
POST   /api/auth/login
GET    /api/instances
POST   /api/instances
DELETE /api/instances/:id
POST   /api/instances/:id/start
POST   /api/instances/:id/stop
POST   /api/instances/:id/restart
POST   /api/instances/:id/kill
POST   /api/instances/:id/command      {"cmd": "op xxx"}
WS     /api/instances/:id/logs
GET    /api/instances/:id/files?path=
POST   /api/instances/:id/files/upload
GET    /api/instances/:id/backups
POST   /api/instances/:id/backups
POST   /api/instances/:id/backups/:bid/restore
GET    /api/java/list
POST   /api/java/install              {"major": 21}
GET    /api/catalog/versions          可安装的服务端版本
POST   /api/catalog/install
GET    /api/modrinth/search?q=&type=&version=
```

## 八、里程碑

| 阶段 | 产出 |
|---|---|
| M1 | Go 骨架、实例 CRUD、进程启停、Job Object 托管 |
| M2 | 日志跟随 + WebSocket 推送 + 前端控制台 |
| M3 | Java 管理 + 服务端安装器（Paper/Fabric/Forge/Velocity） |
| M4 | 文件管理、插件上传、备份还原 |
| M5 | Modrinth / CurseForge 检索与一键安装、整合包导入 |
| M6 | Wails 桌面端打包、托盘、手机端适配打磨 |

## 九、开工前置条件

- [x] 安装 Go 1.27.1（装在 `F:\tools\go`，GOPATH / GOCACHE 也都在 F 盘，不碰 C 盘）
- [x] 确认项目名称：ServerBox
- [x] 建 git 仓库（main 分支）
- [ ] 安装 Wails CLI（桌面端阶段才需要）

## 十、M1 实测记录（2026-10-04）

已编译运行：`go build -o bin/serverbox.exe ./cmd/server`，产物约 10 MB 单文件。

**改了一个决策：日志推送用 SSE，不用 WebSocket。**
M1 阶段不引第三方依赖，标准库 `net/http` 就够；日志是单向流，命令走 HTTP POST 即可。
后续若控制台命令也要走长连接，再换 `coder/websocket`。

**实测跑通的路径**
- 实例创建 / 列表 / 启动 / 停止 / 强制结束 / 发送命令（REST 全部返回正常）
- 进程启动 → stdout+stderr 捕获 → 日志环形缓冲 → SSE 推送 → 崩溃状态自动标记
- 本机 Java 21.0.9 可用

**踩到并已修的坑**
1. `Manager.Start` 在持有 `m.mu` 的情况下调用同样要加 `m.mu` 的 `logsFor`。
   Go 的 `sync.Mutex` 不可重入 → 自死锁，HTTP 请求永久挂起，前端表现为点击启动没反应。
   修法：先取 logs，再加锁。
2. 前端加了 `?nosse=1` 开关，否则 headless 截图会被 SSE 长连接卡住。

**已知限制（M2 / M3 处理）**
- 服务端 jar 需手动放进实例目录，还不会自动下载
- 日志只走 stdout/stderr，尚未接入 `logs/latest.log` 文件通道
- 没有认证，单管理员密码未实现
- 文件管理、插件安装、备份还原均未实现

## 十一、UI 重做与指标采集（2026-10-04 续）

第一版界面（左栏列表 + 右侧大块黑底日志）信息密度过低，推倒重做为监控仪表盘。

### 设计 token（`web/src/index.css` 的 `@theme`）

| 用途 | 值 |
|---|---|
| 底色 | `#0e1013` |
| 面板 | `#14171b` |
| 悬浮 | `#1a1e24` |
| 分割线 | `#242a32` |
| 主 / 次 / 弱文字 | `#e6e8eb` / `#949ca6` / `#6b7280` |
| 主色（运行中、主操作） | `#2fbf87` |
| 警告 | `#e0a33e` |
| 危险 | `#e0574c` |

规则：间距一律 8pt 网格；图标全部手写内联 SVG（禁 emoji）；无渐变铺底、无玻璃拟态、无卡片嵌套。
指标卡只在「数据对比」场景用，图标内联在标题左侧而非居中圆角容器。

对比度实测：主文字 15.52:1、次文字 6.48:1、主色 7.64:1，均达 WCAG AA。

### 新增接口
- `GET /api/instances/{id}/metrics` — CPU / 内存 / 在线玩家 / 运行时长
- `GET /api/instances/{id}/logs/history` — 日志快照，给不建立长连接的场景用

### 指标采集
- CPU / 内存：gopsutil 读子进程（`internal/daemon/metrics.go`）
- 在线玩家：解析服务端日志的 `X joined the game` / `X left the game`，并用 `list` 回执校正
- 曲线：前端本地攒 60 个采样点，后端不存历史

### PaperMC API 变更（硬情报，M3 必须按这个写）
v2（`api.papermc.io/v2`）**已 sunset，返回 410 Gone**。必须用 v3：

```
GET https://fill.papermc.io/v3/projects/paper/versions/{mcVersion}
```

返回值里有 `version.java.version.minimum`（该版本要求的最低 JDK）和 `version.java.flags.recommended`（官方推荐 JVM 参数）——
可以直接拿来选 Java 和生成启动参数，比 v2 时期的信息更全。`builds` 是纯数字数组，取首项即最新 build。

### 开发工具：假服务端
`tools/fakeserver/FakeServer.java` — 编译后即可充当 `server.jar`：

```bash
cd tools/fakeserver && javac FakeServer.java && printf 'Main-Class: FakeServer\n' > manifest.txt && jar cfm server.jar manifest.txt FakeServer.class
```

日志格式、stdin 命令、`stop` 优雅退出、内存占用全部对齐真实服务端，改 UI 时不必每次下载几百 MB 真服务端。
