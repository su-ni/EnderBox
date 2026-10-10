# 整合包（modpack）：竞品对比 + 实测事实 + 实施方案

> 2026-10-06。用户要求：「先对比一下竞品，然后把整合包导入做一下 —— **只能识别服务端整合包，
> 不能当客户端模组包导入**；另外要有一个整合包市场，所以可以下。」
>
> 本文事实全部来自**实测**（打真接口 / 拆真文件 / 读真源码），不采信宣传文案。

---

## 一、竞品对比：这块是空白，而且现有做法都很落后

| | 整合包导入 | 整合包市场 | 做法 |
|---|:--:|:--:|---|
| **MCSL2** | ○ | ○ | 全仓库里「整合包」只出现在 `serverErrorHandler.py`（报错文案）与 `configurePage.py` —— **没有导入实现** |
| **MCSManager** | ○ | ○ | `modpack` 只出现在 `mod_manager_service.ts` 与 7 个语言文件里 —— 同样**没有导入实现** |
| 专用 CLI 工具 | ● | ○ | `nothub/mrpack-install`(Go)、`Patbox/mrpack4server`、`HarvelsX/Sugar`、`OceanOC/mrpack-cli` … |
| **ServerBox（现状）** | ○ | ○ | 只有**单个模组**市场（Modrinth），没有整合包 |

两点结论：

1. **我们常对着比的那两家都没有整合包导入**（这才是他们「插件/模组市场」那格只有 ◐ 的真实原因）。
   做出来就是实打实的差异，不是补作业。
2. **现有的实现全是命令行工具** —— 用户得自己下 jar、敲 `mrpack-install <url> --server-dir`。
   **没有一个是「在面板里点一下」**。我们的机会在「把 CLI 那套流程搬进界面」，不是重写算法。

（方法：GitHub 全库 code search。`mrpack` 命中 22848 条，但按仓库聚合后
MCSManager / MCSL2 **一次都没出现**，而专门做这件事的仓库一抓一把。）

---

## 二、实测事实（先打接口，再写代码）

### 2.1 Modrinth 的整合包目录是可直连的

```
GET https://api.modrinth.com/v2/search?facets=[["project_type:modpack"]]
  → total_hits: 18670

命中项自带这两个字段（这就是「服务端能不能装」的现成判据）：
  server_side: unsupported | optional | required
  client_side: unsupported | optional | required
```

实测样例：

| 包 | server_side | client_side | 说明 |
|---|---|---|---|
| Fabulously Optimized（1799 万下载）| **unsupported** | required | **纯客户端包**，必须拒 |
| Zombie Invade 100 Days | required | required | 可装服务端 |
| Cobblemon Official Modpack | required | required | 可装服务端 |

→ 市场上**默认只列 `server_side != unsupported`**，这就是「服务端整合包市场」。
客户端包可以看，但要标清「装不到服务端」并禁用安装按钮。

### 2.2 `.mrpack` 的真实结构（拆的真包：Zombie Invade 100 Days 2.3，28.6 MB）

```
modrinth.index.json
  game           "minecraft"
  formatVersion  1
  versionId      "2.3"
  dependencies   { "forge": "47.4.10", "minecraft": "1.20.1" }   ← 建实例需要的一切
  files          list[129]
     每一项：{ path: "mods/xxx.jar",
               hashes: {sha1, sha512},
               env: { client: required, server: required },
               downloads: [ CDN 地址 ] }
overrides/          1243 个文件   ← 共享覆盖（config/…）
server-overrides/      0 个文件   ← **服务端专属**覆盖
```

**两个关键点**（凭直觉一定会写错）：

1. **模组本体不在包里**。包里只有 `overrides/`。129 个模组全靠 `files[].downloads` 去 CDN 取 ——
   所以这一步必须复用我们**现有的下载管理器**（多线程 + 续传 + 校验 + 可取消），
   而不是自己再写一条下载路径。
2. `dependencies` 直接给出 `forge: 47.4.10` + `minecraft: 1.20.1` → **建实例所需的加载器与版本不用猜**。

### 2.3 参考实现的判据（`nothub/mrpack-install`，Go）

```go
// modrinth/mrpack/overrides.go —— 覆盖文件算不算「服务端的」
func (o *override) server() bool {
    return strings.HasPrefix(o.Name, pathPrefixBoth) ||    // overrides/
        strings.HasPrefix(o.Name, pathPrefixServer)        // server-overrides/
}

// update/command.go —— 每个待装文件按 env.server 分流
switch f.Env.Server {
case modrinth.RequiredEnvSupport: ...
default: log.Fatalf("Unexpected environment configuration %q for mod %q", f.Env.Server, f.Path)
}
```

和我们从真文件里推出来的判据**完全一致**：
`env.server` 分 `required / optional / unsupported` 三档；覆盖文件只取 `overrides/` 与 `server-overrides/`。

---

## 三、方案：只认服务端整合包

### 3.1 判定规则（这是这件事的核心）

对 `.mrpack` 的每个 `files[]`：

| `env.server` | 处理 |
|---|---|
| `required` | **装**到 `mods/`（按 `path`） |
| `optional` | **列出来让用户勾**（默认装），因为「可选」在服务端这边通常是想装的 |
| `unsupported` | **不装**（客户端专属，装了服务端会起不来） |

**拒绝条件**：整包里**没有一个文件是 server 需要的**（全部 `unsupported`）→
判定为**客户端整合包**，拒绝，并把证据摊出来：

> 「这个包里的 129 个模组**全部**标记为 `server: unsupported` —— 它是给玩家客户端用的整合包，
> 装到服务端不仅没用，还会因为缺客户端专属依赖起不来。
> 如果作者另外发布过服务端包，请用它；Modrinth 上这类包通常标着 `server_side: required`。」

**不静默失败**：不能只说「不支持」，要给出上面这些具体证据 + 下一步动作。

### 3.2 导入流程

```
① 识别格式（按内容，不按后缀）
     .mrpack  → zip 里有 modrinth.index.json
     CF 包    → zip 里有 manifest.json
     其它 zip → 看有没有 mods/ + server.properties 之类，判「服务端目录压缩包」
② 解析 + 判定（见 3.1），把「将要装什么」先摊给用户看
     加载器 / MC 版本 / 要下的模组数 / 覆盖文件数 / 跳过多少客户端专属
③ 建实例（类型与版本直接取 dependencies）
④ 下载（复用现有下载管理器，下到 <实例>/mods/）
⑤ 解压覆盖（overrides/ + server-overrides/ → 实例根）
⑥ 收尾：写 eula/端口、把「这个实例来自哪个整合包、什么版本」记进实例配置（为将来的升级留路）
```

### 3.3 必须防的一件事：zip-slip

解压 `overrides/` 时，条目名可能是 `../../windows/system32/x`。
**每个条目都要校验「解压后仍在实例目录内」**（`filepath.Rel` 后不许以 `..` 开头），
和配置编辑器那三层路径防护是同一条规矩。整合包的 zip 来自**用户下载的第三方文件**，
这条不是洁癖。

### 3.4 明确**不做**客户端整合包（写清理由，因为一定会被问）

- 客户端整合包的主体是光影、优化模组、按键与界面配置 —— 装到服务端要么是死重量、要么直接崩；
- 我们的定位是**开服器**，不是启动器。客户端那条线（装到玩家机器上）是 PCL2 的地盘，
  而本项目从第一天就定了「**不得 fork/复用 PCL2 代码**、也不做客户端启动器」。

---

## 四、接口草案

| 路由 | 作用 |
|---|---|
| `POST /api/modpacks/inspect` | 给一个本地 zip（或上传），**只解析不落盘**：返回格式、加载器、版本、将装什么、以及「能不能装到服务端」+ 理由 |
| `POST /api/modpacks/install` | 真正导入：建实例 + 起下载任务，返回下载任务 id（复用现有任务条） |
| `GET /api/modpacks/search?q=&onlyServer=1` | Modrinth 整合包市场（默认只列 `server_side != unsupported`） |
| `GET /api/modpacks/{project}/versions` | 某个包的版本列表（拿 `.mrpack` 地址） |

`inspect` 与 `install` **分开**是刻意的：选包和装包之间必须有一次「先看清楚要装什么」，
而判定结果（能不能装、跳过多少）是用户决定要不要继续的依据。

---

## 五、分期

| 期 | 内容 | 依赖 |
|---|---|---|
| **P0** | `.mrpack` **本地导入** + 服务端/客户端判定 + 拒绝文案 + zip-slip 防护 | 现有下载管理器、现有建实例流程 |
| **P1** | **整合包市场**（Modrinth，默认只列服务端可行的）+ 一键装成实例 | P0 的解析与安装 |
| **P2** | CurseForge 包（⚠ 需要 API key；且它把**服务端包单独发** —— 客户端 zip 里没有服务端文件，要按 `serverPackFileId` 去取）| 需要用户提供 key |
| **P3** | 整合包**升级**（比对 pack 新版本、只动变了的文件；参考 `mrpack-install` 的 `update` + `packstate`）| P1 |

建议先只做 **P0 + P1**：这两步就能把「本地导入」和「从市场装」两条路走通，
而且**P0 的解析器就是 P1 要用的那个**，不返工。

---

## 六、待确认

1. ~~用户提到的「刚给的链接」在这条消息里没有送达~~ —— **已确认：对比对象就是 MCSL2 / MCSManager**
   （2026-10-06 用户明确「对就是这两」）。第一节的结论不变。
2. CurseForge 要不要做（需要 API key）、以及整合包**升级**的范围 —— 升级已确认要做
   （见第七节），CurseForge 仍未定。

---

# 七、整合包**升级**：详细设计（用户指定先做这一块）

## 7.0 先说一个硬依赖：升级必须先有「导入时记下的身份」

**升级这东西没有地基就无处下手**：要升级，得先回答「这个实例来自哪个整合包、装的哪个版本」。
对着一台已经跑着的服务器目录，我们是**认不出**它属于哪个包的 —— 模组会被人手动增删、
config 会被改、作者还会换模组，靠比对文件反推包身份不可靠，做出来就是个会误判的东西。

所以升级的地基是**导入时写下的 packstate**：

> 结论：**P0（导入）不是升级的前置「功能」，而是升级的前置「数据」**。
> 本次实现顺序：packstate 与解析器 → 升级的比对与文件操作（纯逻辑 + 测试）→ 接到界面上。
> 这样即使导入的界面还没做，升级的核心也是完整可测的。

## 7.1 packstate：一个实例「属于哪个包」

存 `<实例目录>/.serverbox/packstate.json`（跟实例走，不跟面板的 data 目录走 ——
实例目录被搬走/备份还原之后，身份必须还在）：

```json
{
  "format": "mrpack",
  "projectId": "l9m9tuPN",
  "slug": "zombie-invade-100-days",
  "versionId": "6OMYdNAO",
  "versionNumber": "2.3",
  "minecraft": "1.20.1",
  "loader": { "forge": "47.4.10" },
  "installedAt": "2026-10-06T18:20:00+08:00",
  "files": [
    { "path": "mods/gravestone-forge-1.20.1-1.0.35.jar",
      "sha512": "0f1b3f80…", "side": "required" }
  ],
  "overrides": [
    { "path": "config/alltheleaks.json", "sha1": "…" }
  ]
}
```

`files` 与 `overrides` 是**我们自己装进去的那份清单** —— 升级要靠它区分
「这个文件是整合包的」和「这个文件是用户自己丢进来的」。

## 7.2 升级算法（差异比对）

对每个 path 比较「旧 packstate」与「新包 index」：

| 情形 | 动作 |
|---|---|
| 新增（新有旧无） | 下载 |
| 变更（同 path，sha512 不同） | 重新下载并覆盖 |
| 删除（旧有新无） | **删掉**（见下面三条底线） |
| 不变（sha 相同） | 不动（这是省流量的主要来源） |
| 新版标记 `env.server=unsupported` | 从「装」变成「删」（例如作者把某个模组改成纯客户端） |

`overrides/` 与 `server-overrides/` 同样按清单比对，只解压变化的部分。

## 7.3 三条安全底线（这块最容易被写坏）

**① 只删「我们自己装进去的」文件。**
删除动作的判据是「它出现在**旧 packstate** 里」，不是「它不在新包里」。
用户在 `mods/` 里自己加的模组（不在任何 packstate 里）**一个都不能碰** ——
整合包升级最不能接受的后果就是「我加的东西被升级搞没了」。

**② 磁盘上那份和 packstate 记的不一致时，不覆盖、先报出来。**
说明这个文件被用户改过（改 config、换模组版本）。默认动作是**保留用户那份并列入「跳过」清单**，
让用户自己决定。**默认覆盖是错的** —— 那等于悄悄吃掉用户的改动。

**③ 加载器或游戏版本发生跳跃时，明确警告 + 建议先备份。**
`minecraft: 1.20.1 → 1.21.1` 这种不是「更新一下」，是**换了一台服**：
存档、世界生成、模组全都要跟着变。这一条必须在按钮之前说，而不是升级完再说。

## 7.4 前置条件与复用

- **实例必须停止**：Windows 上运行中的 jar/文件被锁，覆盖会失败（与「换服务端文件」同一道闸，
  复用 `instanceRunning`）。
- **下载复用现有下载管理器**：几十上百个模组、带 sha512 校验、要能中途取消 ——
  和「下一个服务端 jar」是同一件事，没有理由另写一条路。
- **升级前建议走一次备份**：现有的自动备份/手动备份都能用，界面上直接给入口。

## 7.5 接口

| 路由 | 作用 |
|---|---|
| `GET /api/instances/{id}/modpack` | 这个实例装的是哪个包、什么版本（没装过则说明「不是整合包装的」） |
| `GET /api/instances/{id}/modpack/upgrade?version=<id>` | **只算不装**：返回将新增/变更/删除/保留的数量与清单、跳过的（被用户改过的）清单、版本跳跃警告 |
| `POST /api/instances/{id}/modpack/upgrade` | 真升级：起下载任务 + 落文件 + 更新 packstate |

`GET ?version=` 这条「只算不装」是刻意的：**升级是有破坏性的操作**，
必须先把「要删哪些文件」摊给用户看。

## 7.6 分期（这一块内部）

| 步 | 内容 | 依赖 |
|---|---|---|
| **U1** | `.mrpack` 解析器（`modrinth.index.json` → 结构体 + 服务端/客户端分档）| 无 |
| **U2** | packstate 读写 | U1 |
| **U3** | 差异比对（纯函数：新增/变更/删除/保留/跳过）| U1+U2 |
| **U4** ✅（2026-10-06 晚完成并真机验收） | 真升级（下载 + 落盘 + 删旧 + 写回 packstate）| U1–U3 |
| U5 ✅（同晚完成） | 接界面 | U4 |

本次先做 **U1–U3**：全部是纯逻辑、可测试，而且 **U1/U3 也正是「导入」要用的那两块** ——
先做它们不返工。

---

# 八、接口契约（冻结点：2026-10-06 18:30）

> **这一节是并行开发的分界。** 后端执行器（`internal/modpack/executor.go`）与前端面板
> （`web/src/components/`）各自照这里写，互不改对方的文件。**改契约要先改本节，再改两边。**
>
> ⚠ 反复强调过的那条：**所有发给前端的集合字段一律 `[]`，不得 `null`**（`null.length` 会当场炸）。

## 8.1 后端执行器（Go 内部接口，不对外）

> **勘误（2026-10-06，冻结契约 v2）**：`Fetcher.Fetch` 增加哈希种类参数 ——
> `Fetch(ctx context.Context, url, dst, wantSHA, shaKind string, size int64) error`。
> Modrinth 索引里的哈希有 sha512 / sha1 两种（包只给哪种就是哪种），只传值不传种类，
> 下载方不知道按什么算法校验。以下代码块保留原文，签名以这行为准。

```go
// Fetcher 把「下一个文件」这件事交出去。
// ⚠ **复用面板现有的下载管理器**，不要在这里另写一条下载路径 ——
// 整合包动辄几十上百个模组，要求：建父目录、校验哈希、断点续传、可取消。
type Fetcher interface {
    Fetch(ctx context.Context, url, dst, wantSHA string, size int64) error
}

type Progress struct {
    FilesDone, FilesTotal int   `json:"filesDone"`
    BytesDone, BytesTotal int64 `json:"bytesDone"`
    Current               string `json:"current"` // 当前文件（相对实例目录）
}

type ExecuteOptions struct {
    InstanceDir string // 落地根目录（所有写入都必须校验「仍在这个目录内」）
    PackPath    string // .mrpack 本地路径（解 overrides/ 与 server-overrides/ 用）
    Plan        *Plan  // 由 BuildPlan 产出；执行器**只做计划里的事**
    Index       *Index // 目标版本的索引
    Old         *State // 升级前的状态；首次安装为 nil
    Slug, ProjectID, VersionNumber string
    Fetcher     Fetcher
    OnProgress  func(Progress)
}

type Result struct {
    Added, Replaced, Removed, SkippedCount int
    // Skipped 明细（给「哪些没动、为什么」用）
    Skipped []string `json:"skipped"`
    // Failed 失败的路径 + 原因。**部分失败不算成功**，要如实报。
    Failed  []string `json:"failed"`
    State   *State   `json:"state"` // 新的 packstate（调用方写回）
}

func Execute(ctx context.Context, o ExecuteOptions) (*Result, error)
```

**执行器必须遵守**（与 7.3 那三条底线一一对应）：

1. **只动计划里的路径**。`plan.ToAdd/ToReplace` 才下载，`plan.ToRemove` 才删除，
   其余一律不碰（`plan.Kept` / `plan.Skipped` 连 open 都不要）。
2. **每个写入/删除前校验路径仍在 `InstanceDir` 内**（`filepath.Rel` 后不许以 `..` 开头）——
   包是第三方下载来的，zip-slip 在这一层必须再挡一次，不能只信解析层。
3. **删除失败要区分**：文件本来就不在（`os.IsNotExist`）→ 不算失败；
   其他错误 → 进 `Failed` 并让整个操作以「部分完成」收场。
4. `ctx` 取消 → 立刻停，**如实返回已做到哪一步**（不要假装全成功）。
5. 覆盖文件（`overrides/` + `server-overrides/`）从 `PackPath` 解出来写进 `InstanceDir`，
   同样逐个校验路径。

## 8.2 前端要用的四个接口

### `GET /api/instances/{id}/modpack`

```json
{ "have": true, "format": "mrpack", "slug": "zombie-invade-100-days",
  "projectId": "l9m9tuPN", "versionId": "6OMYdNAO", "versionNumber": "2.3",
  "name": "Zombie Invade 100 Days", "minecraft": "1.20.1",
  "loader": { "forge": "47.4.10" },
  "installedAt": "2026-10-06T18:20:00+08:00", "updatedAt": "",
  "fileCount": 129, "overrideCount": 1243 }
```

不是整合包装的：`{ "have": false, "note": "这个实例不是由整合包安装的" }`（`have` 恒存在）。

### `GET /api/instances/{id}/modpack/upgrade?version=<modrinthVersionId>`

**只算不装**。查参数二选一：`version`（市场里挑的）或 `pack`（本地 .mrpack 绝对路径）。

```json
{ "ok": true, "refusal": "",
  "planHash": "9f2c…",
  "summary": { "add": 3, "replace": 1, "remove": 2, "keep": 125, "skip": 1 },
  "downloadBytes": 12345678,
  "levelJump": true,
  "levelJumpNote": "游戏版本要从 1.20.1 换到 1.21.1……先做一次备份再升。",
  "running": false,
  "toAdd":     [ { "path": "mods/new.jar", "action": "add" } ],
  "toReplace": [ { "path": "mods/up.jar",  "action": "replace" } ],
  "toRemove":  [ { "path": "mods/old.jar", "action": "remove", "reason": "新版本里没有这个文件了" } ],
  "skipped":   [ { "path": "config/x.toml", "action": "skip", "reason": "这个文件不是你装的整合包那一份（内容对不上），保留你的，没有覆盖" } ],
  "target": { "versionId": "…", "versionNumber": "2.4", "minecraft": "1.20.1", "loader": { "forge": "47.4.10" } } }
```

- `refusal` 非空 = **不能升**（例如目标版本不再是服务端包），界面此时只显示原因、禁用按钮。
- `running` 为 true = 实例在跑，界面要提示先停服。
- 四个数组**一律 `[]`**。

### `POST /api/instances/{id}/modpack/upgrade`

```json
请求：{ "version": "6OMYdNAO", "pack": "", "planHash": "<预览时拿到的>", "confirmStale": false }
响应 202：{ "opId": "mp_1759750000" }
```

**`planHash` 与 `confirmStale` 的含义**（⚠ 这两个在初稿里只写了名字没写含义，是契约的漏洞，
2026-10-06 补上；前端当时把它错映射成了「版本跳跃」，已纠正）：

- `planHash`：预览响应里给的一个指纹。POST 时**原样带回**。
- 服务端会把 `planHash` 和「现在重新算出来的计划」对一遍：**不一致 → HTTP 409**，
  意思是「你看到的那份计划已经不是现在这份了」（竞态：预览之后包被更新了、或文件被人动过）。
- `confirmStale` **默认 false**。只有在用户**看到 409、重新看过新计划之后**再点一次确认，
  才允许传 true。
- 这条存在的理由：**不能让用户批准计划 A、系统却执行计划 B**。没有它，「只算不装」那一步
  就只是建议，真跑的可能完全是另一份计划。
- ⚠ **`confirmStale` 与 `levelJump` 是两件事**：`levelJump` 是「这是个大变动」，
  用户**看得见**，属于界面警告，不需要服务端字段；`confirmStale` 是「计划本身变了」，
  用户**看不见**，只能靠服务端挡。

### `GET /api/modpacks/ops/{opId}` / `POST /api/modpacks/ops/{opId}/cancel`

```json
{ "state": "running",           // running | done | failed | canceled
  "progress": { "filesDone": 12, "filesTotal": 129, "bytesDone": 1048576,
                "bytesTotal": 528482304, "current": "mods/xxx.jar" },
  "result": { "added": 3, "replaced": 1, "removed": 2, "skipped": 1,
              "skippedPaths": [], "failed": [] },
  "err": "" }
```

`state` 为 `done` 时 `err` 空；**`result.failed` 非空 = 部分失败**，界面必须显示出来，
不能因为 `state=done` 就报「全部成功」。

---

# 九、P1 整合包市场：接口契约（冻结点：2026-10-06 23:55）

> 与第八节同一条规矩：这一节是后端与前端的分界，**改契约先改这里，再改两边**。
> 所以发给前端的集合字段一律 `[]`，不得 `null`。

## 9.0 先摆事实（2026-10-06 实测，不是读文档）

- Modrinth `/search` 的 `server_side` facet **成立**：
  `[["project_type:modpack"],["server_side:required","server_side:optional"]]`
  → 整合包总数 **18669**、服务端可用 **13019**、`unsupported` **5755**（余 105 是 unknown）。
  默认要滤掉的正是那 30% 装不上的，所以 `onlyServer=1` 不是洁癖。
- 官方与镜像 `mod.mcimirror.top` 的 `total_hits` 与前三名**逐条一致**（都是 13019）。
- 热门 40 个服务端整合包的加载器：**fabric 24 / forge 17 / neoforge 7 / quilt 2**。
  ⚠ 约 **1/4 的热门包要 NeoForge 或 Quilt**，而这两者我们**不能自动准备服务端文件**
  （NeoForge 官方 maven 本机只解析出 IPv6；Quilt 面板干脆没有这个类型）。
  **这条必须在用户点下去之前说清楚**，不能装到一半才说。
- 整合包的 `.mrpack` **总是 primary 文件**（抽查 19/19），`hashes` 同给 sha1 + sha512。
- 版本级的 `loaders` / `game_versions` 只能当预检代理；**权威值在包内 `modrinth.index.json` 的
  `dependencies`**（`modpack.Index.Loader()` / `Minecraft()`）—— 不下包就不知道。

## 9.1 `GET /api/modpacks/search`

参数：`q`、`loader`(forge/fabric/neoforge/quilt)、`mc`、`sort`、`src`(auto/official/mirror)、
`offset`、`limit`、**`all=1`**（关掉服务端过滤，默认 0）。

```json
{ "items": [ { "id": "l9m9tuPN", "slug": "zombie-invade-100-days",
               "title": "Zombie Invade 100 Days", "description": "…", "iconUrl": "…",
               "downloads": 14909057, "categories": ["forge","adventure"],
               "projectType": "modpack", "zhName": "" } ],
  "base": "https://api.modrinth.com/v2", "mirrored": false, "onlyServer": true }
```

- `items` 一律 `[]`；`categories` 也一律 `[]`（它的来源可能缺，别让它变 null）。
- `all=1` → `onlyServer: false`，界面必须**明说**「这次包含客户端包，装不上的会在下一步被挡」。
- **`loader` / `mc` / `sort` / `src` 四个参数界面全都有入口**（2026-10-06 补的）。
  后端从一开始就支持，但前端一个都没传过 —— 用户只能拿关键词硬凑（「1.20.1 forge 机械动力」），
  搜不到也说不清是市场里没有还是自己拼错了。界面落点：`ModpackMarket.tsx` 的四组下拉
  （加载器 / 游戏版本 / 排序 / 数据源），**一改就重查**，不攒到按「搜索」。
  几处定死的规则：
  - 排序**故意不列「相关度」**：后端在 `sort` 为空时本来就是「有词→相关度，无词→热门榜」。
    单列出来，用户空着关键词点它会拿到一份没有排序依据的名单。
  - 数据源选「仅官方」/「仅镜像」时后端**有意不兜底**，那正是用户用来判断哪条线不通的手段。
    界面上的 `结果来自镜像源 <url>` 必须保留 —— 它是「这次的答案不是官方给的」的唯一提示。
  - 游戏版本清单来自 `/api/mods/game-versions`（Mojang 版本清单，40 条），**不许写死几条**：
    写死的话新版本出来这个筛选就成了摆设。⚠ 深链带了清单里没有的版本时要补一个
    `（当前）` 选项，否则下拉会显示成「全部版本」—— 界面写着没筛、实际筛了。
  - 深链：`?pkld=?pkmc=?pksort=?pksrc=`（默认值的 `pksrc` 不写进 URL，免得像带着一层筛选）。
- `title` / `description` 出接口前一律过 `fetch.plainText()` **洗成纯文本**（`**粗体**`、`` `代码` ``、
  `[文字](链接)`、行首 `#`）。源站的描述是 Markdown 原文，界面只印纯文本，不洗的话
  列表里就会挂着两个星号。实测取样热门榜 100 条整合包有 1 条中招（`BattleArmory TACZ`，
  句尾一对不成对的 `**`）—— 不常见，但撞上了用户只会觉得「这软件做的什么玩意」。
  ⚠ 只洗**成对的双字符**标记，**单个 `*` 和 `_` 一律不动**：模组 id 全是单下划线
  （`mezz_config`、`fabric_api`），把单下划线当斜体吃掉会改坏名字，而「改坏」不报错，
  只表现为用户搜不到东西。护栏：`TestMarketDescriptionIsPlainText`。

## 9.2 `GET /api/modpacks/versions?project=<id>&loader=&mc=`

```json
{ "items": [ { "id": "6OMYdNAO", "versionNumber": "2.3-Fix", "releaseType": "release",
               "gameVersions": ["1.20.1"], "loaders": ["forge"], "size": 29975184,
               "downloads": 12345, "datePublished": "2026-…", "hasMrpack": true } ] }
```

- **只回 `hasMrpack: true` 的版本**：能装的是 `.mrpack`，没有它的版本在界面上是个死按钮。
- 一个都没有 → `items: []`，界面说「这个包没发过可安装的版本」，**不是**「加载失败」。
- 两个数组字段同样一律 `[]`。

## 9.3 `POST /api/modpacks/inspect` —— 只算不装，但这次要下包

请求：`{ "version": "<Modrinth 版本 id>", "pack": "" }`（二选一；`pack` 是本地 .mrpack 绝对路径）。

```json
{ "token": "pk_1759760000123456789", "ok": true, "refusal": "",
  "name": "Zombie Invade 100 Days",
  "slug": "zombie-invade-100-days", "projectId": "l9m9tuPN", "versionId": "6OMYdNAO",
  "versionNumber": "2.3-Fix", "minecraft": "1.20.1",
  "loaderKind": "forge", "loaderVersion": "47.4.10", "serverType": "forge",
  "install": 129, "skipped": 12, "overrideCount": 1243,
  "packBytes": 29975184, "downloadBytes": 528482304,
  "canAutoJar": true, "jarNote": "" }
```

- `token` = 这次下到临时目录那份包的**取件码**：**20 分钟内有效、只能用一次**。
  没有它 install 就得把几十 MB 再下一次 —— fastly 实测只有 ~60KB/s，这一步不能白做。
- `refusal` 非空 ⇒ `ok:false`，界面只显示原因、禁用按钮。**两种**情况都会走到这里：
  ① 纯客户端包（判据同 3.1）；② **加载器认不出**（`serverType` 为空串，例如 Quilt 或上游新加的加载器）——
  面板没有对应的服务端类型，模组装进原版服**一个都不会加载**，那是静默失败；宁可当场把三条路说清。
- **`canAutoJar: false`** ⇒ 服务端文件我们不能自动准备（**仅限 `serverType` 认得出的**，目前是 NeoForge）。
  `jarNote` 给**具体现象 + 下一步**。此时 `ok` **仍可为 true**：模组装好是有价值的，且实例类型是对的，
  用户自己下一个 NeoForge 安装器跑一遍就能起。但界面必须把 `jarNote` 显示在按钮**上方** ——
  让用户在点之前就知道「装完还得自己放服务端文件」。
- 所有数组字段一律 `[]`。

## 9.4 `POST /api/modpacks/install`

请求：`{ "token": "pk_…", "name": "", "port": 0, "javaPath": "" }`

```json
202 { "instanceId": "i1759760000123", "opId": "mp_1759760000123456789" }
```

- `token` **必填** —— 必须先 inspect。过期 / 已用过 → 400，文案指向「重新预览」。
- `name` 空 → 用包名；重名自动加「 2」「 3」。
- `port` 0 → **自动挑**（从 25565 往上找第一个没被别的实例占用的）。
- 建实例**同步**做（失败立刻 400/500，不留垃圾实例）；作业里做「服务端文件 → 装模组 → 写 packstate」。
- ⚠ **作业里若发现装不了**（判定反转、包下不下来），必须**把刚建的实例删掉**并带出原因 ——
  否则会留下一个「什么都没有、名字还占着」的空壳实例。
- 实例 `Type` 取 `serverType`，`LoaderVersion` 取包内 dependencies 记的加载器版本（Forge 靠它回源站定位）。
- **不接受 EULA**：同意书是用户勾的（与新建向导同一套做法），前端在拿到 `instanceId` 之后
  自己调一次 `POST /api/instances/{id}/eula`。后端替用户点「同意」是一次法律行为，不做。

### 作业快照新增字段（`GET /api/modpacks/ops/{opId}`）

```json
{ "state": "running",
  "kind": "install",          // install | upgrade；**缺省 = upgrade**（老作业不带这字段）
  "instanceId": "i1759…",     // 装新包时才有：界面要有「去看这个实例」的入口
  "stage": "jar",             // jar(服务端文件) | files(装模组) | ""(终态)
  "progress": { … },
  "result": { "added": 129, "replaced": 0, "removed": 0, "skipped": 12,
              "skippedPaths": [], "failed": [], "warnings": [] },
  "err": "" }
```

- `stage` 让界面说清「现在在干嘛」，别只有一个百分比。
- `warnings` **一律 `[]`**。服务端文件没能自动准备时这里放一句能照着做的话。
  ⚠ 它与 `failed` **不是一回事**：`failed` 是「该做的没做成」（红），
  `warnings` 是「我们没接、你自己来」（黄）。两者的界面待遇必须分开。
- `kind` 只影响文案与「去看实例」入口，**不影响进度字段**。

## 9.5 P1 明确不做（写清理由，免得被反复问）

- **不做**「装进已有实例」的市场入口 —— 那是升级页（`?version=`）的活。市场页永远是「新建一个实例」。
- **不做** CurseForge（P2，要 API key）。
- **不做**可选模组逐个勾选：`env.server == optional` 一律按「装」（与服务端语义一致）。
  勾选要同时重做计划与执行两条链路，等真有人提再说。

## 9.6 界面落点（P1 实现时定的，改这块前先读）

**它是独立的一屏，不在任何实例的详情页里。**

原因：市场装出来的东西**是一个新实例**。塞进实例详情页的话，用户在实例 A 的页面上点
「从市场安装」，会理所当然地以为装的是 A，结果列表里多出一个 B —— 那是两个动作共用一个入口
造成的误会，不是用户没看清。

| 界面 | 文件 | 职责 |
|---|---|---|
| 入口按钮 | `web/src/components/WorldList.tsx` | 「从整合包市场开服」，与「新建世界」并列（都是开新服） |
| 视图分发 | `web/src/App.tsx` | `view: 'board' \| 'create' \| 'market'`；`?view=market` 可深链 |
| 市场页 | `web/src/components/ModpackMarket.tsx` | 搜 → 挑版本 → 预览 → 安装；含 `InspectView` |
| 作业进度 | `web/src/components/ModpackOpView.tsx` | **安装与升级共用**（原来只在 ModpackPanel 里，装新实例要同一种进度） |

几条实现约定，别退化：

- **作业进度只有一份实现**。抄第二份的话，「done 但有 failed 不算全部成功」「取消只在进行中可点」
  这些判断各有两份，总有一边会漏。
- **动词由调用方给**（`verb` 参数），**不从 `op.kind` 推**：往已有实例里装本地 .mrpack 时，
  后端记的 `kind` 是 `upgrade`（走的是同一个接口），但用户看到的是「安装」。
- `op.kind === 'install'` 才显示「打开这个实例」—— 只有这种作业的 `instanceId` 是新实例。
- **进度只在市场页里**：切走再回来就看不到了。后端作业照跑，实例在受理那一刻已经建好，
  所以左边列表里找得到；但界面必须提前说明，不能让用户以为进度丢了就是失败了。
  （要全局追进度得先给后端加一个「列出运行中作业」的接口，P1 的契约里没有，不做。）
- 深链参数：`?pkq=` 关键词 / `?pkall=1` 连客户端包一起搜 / `?pksel=<projectId>` 直接进版本层。
  无头验证点不了按钮，交互态只能靠 URL 进去（和 `?view=create&step=N` 同一个套路）。
- 空态要分得清「这个条件下没有」和**「被服务端过滤滤掉了」**—— 后者要直接引导去打开
  「连纯客户端包一起搜」，那是完全不同的下一步。
- **点包之后必须把版本区滚进视口，而且要等版本行渲染完再滚。** 列表 20 条、版本区排在它们下面，
  不滚的话用户点完眼前毫无变化（变化在屏幕外一两千像素处），结论只会是「点了没反应」。
  两个坑，都是实测踩出来的：
  ① **不能 `picked` 一变就滚** —— `scrollIntoView` 会被「当时」的可滚动高度夹住，版本还在拉时页面
     只有骨架屏那么矮，滚到矮页面的底（实测 `scrollTop=979`、版本区顶距视口 664px），等十几行版本
     把页面撑高，滚动位置不会自己补上。要等 `vLoading` 落回 false 再滚（改后 `1381 / 247`）。
  ② **版本区是这一屏的最后一屏内容，下面没有东西了**，所以它天生滚不到顶。给容器一个
     `min-h-[70vh]` 下限，滚动才有余量。（没加时换到版本少的包只能到 `top=427`，看着还是没滚上来。）
  用瞬间跳转不用 `behavior:'smooth'`：跨十几行的动画要一秒多，期间用户看到的还是那份列表，
  反而更像没反应，而且截图会落在滚动中途。

### 验收命令（真机，必跑）

```bash
# ① 分支文案（本地微型包，几毫秒）：Quilt 拒 / 纯客户端拒 / NeoForge 带 jarNote / Forge 正常
python mkpacks.py
curl -s -X POST localhost:23461/api/modpacks/inspect \
  -H 'Content-Type: application/json' --data-binary '{"pack":"F:/tmp/p1/neoforge-pack.mrpack"}'
# ② 下架项目必须回 404（不是 502）—— 502 会把「这个包没了」说成「我们的故障」
curl -s -o /dev/null -w '%{http_code}\n' 'localhost:23461/api/modpacks/versions?project=no-such-project'
# ③ 无头截图 + 读数：真 Chrome + CDP（--dump-dom 对 React 只会给一个空壳）。
#    脚本是一次性的，放在仓库外（F:/tmp/p1/verify2.mjs 这类），要点：
#    · URL 带 ?view=market&nosse=1 直进市场页；点不了按钮就把交互态预置进 URL（?pksel=）
#    · 断言「版本区进了视口」= 版本区容器 getBoundingClientRect().top 落在 [-2, 400)
#    · ⚠ 等条件必须包含「新内容已渲染」，只等标题：标题在 picked 一变就有了，
#      那时 .mp-ver 还是 null，getBoundingClientRect 抛异常 → 读到 undefined，看着像功能坏了
```

## 9.7 详情页 / 全局下载进度（P1 增补，2026-10-06 夜）

### 9.7.1 为什么要拆出详情页

9.6 那版把「列表 → 版本 → 预览 → 安装」全塞在一屏里，实际用下来两个问题：
①**预览内容太多**（长描述正文 + 二十行版本），底下那截页面根本承载不了；
②用户想「先看看这个包是啥」时，得到的是**一次滚到底的长页面**，而不是一个能返回的页面。

改成：列表只负责搜与点，**点进去是独立一页**（`view: 'pack'`，URL `?pk=<projectId>`）。

| 界面 | 文件 | 职责 |
|---|---|---|
| 市场列表 | `web/src/components/ModpackMarket.tsx` | **只列表**：搜索 / 四件套筛选 / 空态；`onOpenPack(id)` 跳详情 |
| 详情页 | `web/src/components/ModpackDetail.tsx` | 图标+作者+许可、数据行、外链、**Markdown 正文**、版本列表、预览、安装、作业轮询 |
| Markdown 渲染 | `web/src/markdown.tsx` | 见 9.7.3 |

- 详情页用 `key={packId}` 挂载，**换包就重挂**（否则上一个包的正文/版本会残留一帧）。
- 外链按钮 `ExtLink`：主链走 `webUrl`（人看的页面），issues/source/wiki/discord/donation 有才显示。
- **作者/许可缺失时不写「未公开」** —— 那一行整体不渲染，别替项目方编结论。
- 版本列表复用原 `.mp-ver` 样式；`InspectView`/`releaseLabel` 从 ModpackMarket **搬到** ModpackDetail（一份实现）。

### 9.7.2 作业进度要能全局追（`GET /api/modpacks/ops`）

9.6 遗留的问题：**进度只在市场页里，切走就没了**。但装着装的作业在后端照跑，用户切走再回来
（或从侧栏点进来）理应还能看到。所以补一条**列表接口**：

```
GET /api/modpacks/ops   →  { "items": [ { ...op snapshot, "id": "...", "startedAt": "..." }, ... ] }
```

- 按 `startedAt` 倒序；**空的时候是 `[]` 不是 `null`**（前端读 `.length`，`null` 会炸）。
  `TestModpackOpListAlwaysArrayAndCarriesIdentity` 同时断言「空返回 `[]`」与「作业带 id/startedAt/kind/instanceId」。
- **终态作业不立刻删**：`opLifetime` 提到 24h，这样「最近结束」那一栏才有记录可看（否则完成即蒸发，
  用户退出重进就查无此事，反而像失败）。
- 取消仍走 `POST /api/modpacks/ops/{opId}/cancel`。

### 9.7.3 正文是 Markdown + 有限 HTML，**不用 `dangerouslySetInnerHTML`**

Modrinth 的 `body` 是**原始 Markdown，里面混着大量 HTML**（实测 60 个包里 40 个含裸标签；
有的整篇就是 HTML：54 个 `<p>` + 186 个 `<li>` + 66 个 `<span>` + 16 个 `<font>`）。
所以渲染要过一层**自己写的**解析器（`web/src/markdown.tsx`），只产出 React 元素：

- **先降级 HTML 块，再解析**：`htmlBlocksToMarkdown()` 把 `<p>`→空行、`<li>`→`- `、`<hN>`→`#`×N、`<br>`→换行、
  列表/引用/hr 全转成 Markdown，**然后**才进 `parseBlocks()`。
  ⚠ 这一步不能省：整篇 HTML 的帖子不做降级，会把全部内容塌成**一个段落**（当时 `.md-body` 只有 1 个子块，
  页面上一整块糊字，还漏 36 个裸标签）。
- **实体要解**：`&eacute;`→`é`、`&rsquo;`、`&nbsp;`… 以及 `&#233;` / `&#xE9;` 数字实体（`decodeEntities()`）。
  不解的话 `Capture Pok&eacute;mon` 原样印在屏幕上。
- **标签一律丢弃、不认属性**，属性只从白名单里取（`attr()`）；链接协议过 `safeUrl()` 白名单（http/https/mailto）。
  自闭合候选按**最长匹配优先**排序，这样 `<a href>` 不会被泛型「丢标签」规则先吃掉。
- 支持：标题 / 列表（方点）/ 引用 / 表格（认 `| --- |` 分隔行）/ 代码块 / `<details>` / 单图 / 粗斜体 / 链接 / 图片 / hr。
- 样式在 `web/src/index.css` 的 `.md-body`/`.md-*`：`max-width:72ch`、13px、`line-height:1.75`，
  正文**左对齐**、1px 表格线、图片描边 + `max-width:100%`。
- ⚠ **正文图片不要加背景、也不要加边框**（`.md-img` 只有 `display:block / max-width:100% / height:auto`）。
  这里记一次**被用户当场纠正的错误结论**，免得以后又加回来：
  - 现象：Cobblemon 的分隔图在我们的深色底上看着像「空框 + 裂图」。
  - 当时的（错误）判断：作者是照白底页面作图的，透明 PNG 落深色底上隐形了 →
    给 `.md-img` 加 `background:#fff`，还配了 `border:1px solid var(--color-line)`。
  - 用户的反应：「为什么图片有白边？原网站没有？」—— 白底板把分隔图变成了一条横贯正文的**刺眼白条**。
  - **真正的原因**：把那张 PNG 采下来看，线的颜色是 **`rgb(32,27,38)`**，它本来就是**给深色底画的**
    （Cobblemon 的暗色，几乎等于 Modrinth 深色模式的表面色）。我们的页面底色 `#111311` 比它更暗，
    所以线**本来就能显示** —— 根本不需要什么衬板。边框同理：参考页的正文图片没有描边。
  - **怎么避免再犯**：看到「图在深色底上看不见」时，第一件事是**采这张图本身的像素**
    （`PIL` 读 `convert('RGBA')`，看非透明像素的颜色），而不是猜作者的作图背景。
    另外 Modrinth 自己就有深色模式 —— 拿 `Emulation.setEmulatedMedia` 打开参考页看一眼，
    对照它深色下怎么显示，比推理快得多（实测它就是「隐约细线 + 精灵球」，和白底板毫无关系）。

### 9.7.4 下载中心：气泡管进行中 + 面板留记录

原来只有一个「活动下载」块。现在拆成两件：

- **气泡**（`Bubbles`，右下角 fix，`createPortal` 到 body）：**只显示进行中**的条目，每条一个 36px **环形进度**
  （`ProgressRing`，SVG `stroke-dasharray`；**总大小未知时环空转** `.ring-indet`，百分比显示 `—` 而不是编一个数）。
  面板打开时气泡清空（`items={dlOpen ? [] : feed.items}`），避免同一条进展两处打架。
- **面板**（`DownloadPanel`，`Sheet`）：**进行中 + 最近结束**两栏，是完整台账（点侧栏「下载」开，或 `?dl=1` 深链）。
- 两个数据源**合并成一份 feed**（`web/src/useDownloadFeed.ts`）：`fetch.Tasks`（下载）+ `modpackOps`（装/升级作业），
  key 前缀区分 `dl:` / `op:`，取消时按前缀路由回各自的 cancel。轮询忙时 1s、闲时 1.5s
  （⚠ 原来闲时 3s，侧栏计数在下载窗口里明显滞后才改到 1.5s）。
- **侧栏入口**：`WorldList` 的「共 N 个世界 · 运行中 M 个」旁边加「后台 N 个任务在跑」+「下载」按钮（`onDownloads` 打开面板）。
  没有任务时也要给「下载」按钮（否则界面承诺的入口只在有任务时才存在）。
- `feed.settledTick` 在「有任务刚跑完」时触发一次刷新 + preflight ——下载完 jar 变了，得重新算一遍前置。

### 9.7.5 下载提速：`segMaxParts` 4 → 8

实测单连接 vs 8 连接差距明显（8 线程 ≈ 7.2× 单线程，见当天日志），分片上限从 4 提到 8。
⚠ **MCIM 镜像只镜像 API、不镜像文件**，所以「换镜像下文件」这条路对 `.mrpack`/模组 CDN 走不通；
提速只能靠**多线程分片**（面板上的「多线程下载（8 线程）」就是它）。
选克隆站优先的原则（`marketBases()` 兜底链）适用于**接口**，对**文件本体**无效，别混为一谈。

## 9.8 详情页：页内分两页 + 照 Modrinth 的左右两栏（2026-10-07 凌晨）

这一节的缘起是用户两句话：
①「你把那个介绍和下载做分页，安装按钮添加到整合包简介的右侧」；
②改完之后「PC 端页面不好看啊....这个排版」，追问后确认指的是**右边太空**，
并点名「做的和那个 `modrinth.com/modpack/cobblemon-fabric` 一样」。

### 9.8.1 页内分两页：介绍 / 下载

9.7.1 只把详情从「列表下面的一坨」变成了独立一页，但**页内还是上下叠着**：
作者写的正文实测能到 53KB（几十屏），底下再接二十行版本列表。于是两种人的路都被堵住：
- 想**装包**的人，得先划过整个帖子才够得着版本列表；
- 想**读帖子**的人，滚到一半撞上版本列表，读也不是、不读也不是。

这两件事的下一步完全不同（「再读点」 / 「挑一版装」），挤在一屏里就是互相挡路。

| 页 | 内容 |
|---|---|
| `01 介绍` | 作者正文（`renderMarkdown`）。抬头那一段一句话简介不重复放，它属于抬头 |
| `02 下载` | VERSION 段 + 版本列表 + 预览安装 + 预览结果 + 开始安装 + 安装作业进度 |

### 9.8.2 形态与地址

- 页签用设置页那套 `.tab-btn`（同一套「标签分页」形状），**不另造一种页签组件**。
- ⚠ 外层 `nav` 加 **`w-fit`**：设置页有 8 个页签，铺满是必需的；这里只有 2 个，
  铺满会变成两个 600px 宽、文字居中的大格子 —— 和 72ch 左对齐的正文脱节，
  而且白白把点击区拉成两条长条。实测改 `w-fit` 后页签贴着内容宽、和正文同一条左边缘。
- **可寻址**：`?ptab=dl` 直接落在下载页，缺省 / `desc` 是介绍页。切页用 `history.replaceState` 同步。
  ⚠ 无头浏览器点不了页签，**深链是唯一的验证入口**，所以这个参数不是锦上添花。
- ⚠ 切页后只在 **`nav.rect.top < 0`** 时把分页条 `scrollIntoView` 拉回视口。
  无脑滚会在元素本来就完全可见时也把它对齐到顶，用户明明看着它却平白跳一下。
- ⚠ `App.tsx` 的 `onOpenPack` 与 `onBack` 都要 `delete('ptab')`：
  否则从列表点一个新包会直接落在下载页（上一个包的页签状态被带过来），返回列表后参数也留脏。

### 9.8.3 版面：抬头一行 + 左右两栏（照 Modrinth 官方项目页）

先把参考页的真实结构量了一遍（真 Chrome + CDP 截 `modrinth.com/modpack/cobblemon-fabric`）：

```
抬头（占满整行）
  [图标] 标题 / 一句话简介 / N 次下载 · M 人关注        ......      [Download] [♡] [🔖] [⋮]
────────────────────────────────────────────────────────────────
左栏（约 70%）：页签(Description/Gallery/Changelog/Versions) + 正文
右栏（约 30%）：Compatibility / (广告) / Links / Tags / Creators / Details   ← 桌面端吸顶
```

**为什么非要做成两栏**：这一页原先只有一栏 72ch 的正文，在 1440 的屏上右边整整空掉六百多像素
（用户原话「右边太空」）。信息栏不是为了好看，是把「能不能装在我这版上 / 缺不缺东西 / 去哪提问」
这些**看正文时一定会回头找**的东西，放到不用滚回去就能看见的位置。

几处刻意的偏差，都是本项目自己的规矩压过参考页：

| 参考页 | 这里 | 为什么 |
|---|---|---|
| 右栏是 4~5 个各自带圆角边框的小卡片 | **一整块面板 + 内部 1px 分隔线** | 四张卡片就是本项目明令禁止的「无差别套卡片」，四个边框叠在 300px 窄栏里很吵 |
| 栏目纵向居中（`max-w` + `mx-auto`） | **也居中**：`mx-auto max-w-[1240px]`，但正文文字仍左对齐 | 初版只做了 `max-w` 没做 `mx-auto`，1920 屏上整块贴左、右边空一大条 —— 用户原话「你就不能居中吗」。⚠ 本项目禁的是**把正文居中排**，不是把版心居中，两者别混为一谈 |
| 正文满栏约 733px（16px 字） | `md-wide`：**14px + 不限宽**（左栏 788px@1440） | 全局 `.md-body` 卡在 72ch（13px→548px），在这个版心里右边又会空一条。⚠ 放宽度必须同时提字号，只放宽度只会让一行读不完 |
| Download 是圆形图标按钮 | **带文字的「安装」按钮**，在抬头右侧 | 参考页那个位置就是「简介右侧」的出处；带字比圆图标更明确这是干什么的 |

**居中的是版心这一整块**，连最上面那条 `PACK / 返回列表` 也包在版心里：
只包正文的话，宽屏上版心居中了、返回按钮还贴在屏幕最右，两块对不齐。
（1920 实测：版心 1240 居中，内容区左右各余 222px。）

**尺寸是反推出来的，不是拍出来的**：先定左栏约 800px（14px 下 ≈ 88 字符/行，和 Modrinth 正文相当），
剩下的都给右栏。所以 `xl` 上右栏是 **340px** 而不是 300px —— 1440 屏上版心正好填满内容区（262→1414），
右侧零留白；屏幕再宽只是外边的留白多一点，**正文不会被拉宽**。

### 9.8.4 抬头「安装」按钮：**只切页，不直接装**

刻意**不**做成「一键装了」：装之前有几件用户必须看见的事 ——
这一版要下多少、会装进去什么、跳过哪些、缺不缺服务端启动文件。
一个按钮替用户把这些跳过，等于把一个需要确认的动作变成盲盒。
所以它只做一件事：切到「下载」页，让用户先挑版本、再预览、再装。

判据（改这块时别退化）：点它之后 **`GET /api/modpacks/ops` 必须仍是 `items: []`** ——
只要多出一个作业，就说明它偷偷开始装了。

### 9.8.5 响应式行为（逐区块写清楚，不是"自适应"三个字）

| 区块 | ≥1024px（`lg`） | <1024px |
|---|---|---|
| 抬头 | 一行：图标 + 身份/简介/统计 …… 「安装」靠右，右边缘与右栏对齐 | 同一个 flex 行，标题换行、简介跟着换行；「安装」仍贴右上（`shrink-0`） |
| 页签 | `w-fit`，两个页签贴内容宽 | 同 |
| 正文 / 版本行 | 左栏 788px | 整宽一栏 |
| 信息栏 | 右栏，`position: sticky; top: 16px` | **退到正文后面**（单栏） |
| 兼容性/分类/外链/详情 | 右栏内四节，1px 线分隔 | 同样四节，排在正文之后 |

⚠ **窄屏是"正文在前、信息栏在后"，不是把信息栏顶到前面。**
试过 `order-first` 把信息栏提到正文前，结果手机上要先划过整整一屏（约 700px）才看得到正文，
比"兼容性晚一点看到"更糟。参考页手机端实测也是正文在前（它的 Compatibility/Links/Details 同样排在正文后）。
而且「我这版能不能装」在「下载」页的版本行上本来就写着。

### 9.8.6 验收（真机，必跑）

```bash
# ① 页内分页：默认落在「介绍」，有正文没有版本行
#    node F:/tmp/p1/cdp.mjs "http://127.0.0.1:23456/?pk=<id>&nosse=1" 1440 1100 out.png "document.querySelector('.md-body')"
#    → mdChildren>0、verRows==0
# ② 点头部「安装」→ 切到下载页（URL 多出 ptab=dl）、verRows==12，且**作业表仍是 []**
#    curl -s localhost:23456/api/modpacks/ops      → {"items":[]}
# ③ 深链 ?ptab=dl 直达下载页
# ④ 几何：版心右边缘 == 信息栏右边缘 ==「安装」按钮右边缘（delta 必须为 0）
#    1440 下应为 x 262 → 1414，左栏 788 + 右栏 340
# ⑤ 吸顶：滚动到 400/1200/2600/4000，信息栏的 getBoundingClientRect().top 必须**始终是同一个数**
#    （本机实测恒为 36；变化 = 抽搐）
# ⑥ 1440 / 520：documentElement.scrollWidth == clientWidth
# ⑦ 正文图片不带背景也不带边框（曾经加过白底板，被用户一眼看穿）：
#    img 的 getComputedStyle → backgroundColor 必须是 rgba(0,0,0,0)、borderTopWidth 必须是 0px
# ⑧ 宽屏居中：1920 下版心（含顶部 PACK 条）左右余量相等（实测各 222px），
#    判据是「版心 x − 内容区 x == 内容区 right − 版心 right」，不是「距视口左边多少」
```

⚠ 跑多个无头实例时**别用写死的调试端口**（`--remote-debugging-port`）：后起的 Chrome 抢不到端口，
两个脚本会连到同一个 Chrome，表现为「卡两分钟然后超时」，很容易被误判成"页面加载慢"。


## 9.9 版式抽成共用外壳 + 模组详情页（2026-10-07）

### 9.9.1 为什么要抽外壳

§9.8 那套版式（版心居中 + 抬头一行 + 页内页签 + 左正文右信息栏 + 吸顶）本来是写在
`ModpackDetail.tsx` 里的。用户接着问了同一个问题：「模组详情页是不是也可以做成这样？」

两页长得一模一样，是**结果**而不是**目标** —— 目标是不让它们各自漂移。把版式抄第二遍，
下次改其中一处（比如信息栏宽度、吸顶偏移）必然漏掉另一处，表现就是「同一个产品里两个详情页长得不一样」。
所以版式下沉成 `web/src/components/ProjectShell.tsx`：

| 导出 | 干什么 |
|---|---|
| `ProjectShell` | 整页骨架：版心居中、顶部 `PACK`/`MOD` 条 + 返回、错误块、骨架屏、页签、左右两栏、吸顶 `<aside>` |
| `ProjectHead` | 抬头一行：图标 + 名称 + 英文原名 + 作者/许可 + 一句话简介 + 统计 + 右侧主行动 |
| `RailSection` / `RailRow` / `ExtLink` | 右栏的一节 / 键值行 / 外链 |
| `ProjectShell` 自动做的两件事 | ① 切页后把分页条拉回视口（判据是「已被滚到视口上方」，不是无脑滚）；② 窄屏把信息栏排到正文**之后** |

纯函数（`licenseLabel` / `sideLabel` / `releaseLabel`）和常量（`MOD_LOADER_TAGS`）另放
`web/src/components/projectMeta.ts` —— 它们没有 JSX，留在组件文件里会让那个文件不再
「只导出组件」，`react(only-export-components)` 会报，fast refresh 跟着失效。
`MOD_LOADER_TAGS` 从此**只有一份**（插件面板、搜索结果、模组详情三处共用）。

⚠ 转换 `ModpackDetail.tsx` 时**没有重写内层正文**：用脚本按行区间把原文件的正文原样搬进
`desc=` / `dl=` / `rail=` 三个 prop，只换外壳。理由是那块内容刚验过，凭手抄一遍等于自找回归。
代价是内层缩进不齐（不影响渲染）。

⚠ 内层一旦成为 prop，**TypeScript 的窄化就传不进来**了：原来整块包在 `{d && !err && (…)}` 里，
`d` 是非空；拆成 prop 之后 `d` 又变回 `MarketProjectDetail | null`，编译直接报 20 多处 TS18047。
修法是给 `rail` 自己补一次判空（`d ? (…) : null`），不是在每个字段后面加 `!`。

### 9.9.2 模组详情页（市场侧）

- 形态：**独立详情页**，复用 `ProjectShell`。入口是插件面板的市场视图，选中一个项目后
  **整屏交给详情页**（连上面那条吸顶头都不留）—— 留着的话屏幕上有两条「返回」和两组页签，
  用户得先想清楚哪条返回到哪儿。
- 地址：`?page=plugins&mod=market&pm=<项目id|slug>[&ptab=dl]`。
  ⚠ 选中项存的是 **project id 字符串**，不是搜索结果对象 —— 后者是「刚才那次搜索」的产物，
  刷新就没了；存 id 才能让详情页自己把需要的东西重新拉一遍。
- 页签：`介绍`（作者正文，`md-wide`）/ `下载`（版本列表）。
- 与整合包详情**唯一的结构性差别：没有「预览再安装」**。
  整合包是一棵要解包、要挑服务端启动文件的树；模组就是一个 jar，落进 `mods/` 就完事。
  多一道预览只会让每次装模组都多两次点击。
- 右栏「外链」比整合包多两条：**去 MC百科 查**（我们的译名表不全，这是中文用户的出口）
  和**复制名称**。原来的版本列表上有这两个出口，换页面时不能丢。

### 9.9.3 新增接口 `GET /api/mods/project?project=<id|slug>`

批量接口 `/api/mods/projects?ids=` **不带正文**（body 中位 4KB、最大 53KB，只为给列表补个标题不值得），
详情页要的恰恰是正文，所以单独一条，与 `/api/modpacks/project` 对称。

- 守门条件与整合包入口**反过来**：这里只放 `mod` / `plugin` 进来，整合包/材质包/光影/数据包一律 400，
  并在文案里点名它到底是什么、该去哪个入口。单页 URL 用户能手改，静默渲染会让
  「模组市场里怎么出来一个整合包」变成没人解释得了的现象。
- `project_type` 为空时**不下结论**（源站没给这一栏，不该因此把用户挡在门外）。
- 返回 `{item, base, mirrored}`；`mirrored` 为真时右栏写出镜像地址。
- 源站 404 → 404（下一跳是「换一个项目」）；连不上 → 502（下一跳是「重试」）。两者不能混。

### 9.9.4 已安装模组的详情弹窗（保留弹窗形态）

用户明确选择「保留弹窗，只改内部」，所以形态不动（`Dialog` width 620），只重排内部：

- 抬头一行：**图标 48×48** + 状态/版本/加载器 chips + 模组标识（mono）+ 作者。
  名称在弹窗标题条上已经有了，这里**不再重复** —— 同一个名字印两遍像是两个东西。
- 键值区由原来的**两列 `<dl>`** 改成**一列 `.srow`**（左标签 / 右值 + 1px 分隔）。
  两列在 620px 里会把长文件路径和 URL 挤成三四行，而且左右两列的值互相对不对齐。
- 依赖区、缺前置警示不动。

### 9.9.5 顺带修掉的一个真 bug：Markdown 链接后面多一个 `)`

排查模组正文时发现的：界面上每一处 Markdown 链接后面都跟着一个孤零零的右括号，
读作「Also check out Fabric Loader**)** , the …」。根因在 `web/src/markdown.tsx` 的行内正则：

```
/\[([^\]]*)\]\(\s*([^)\s]+)/     ← 匹配到 `[文字](地址` 就收工，闭合的 `)` 留在正文里
```

修法是补一个**可选**的闭合括号：`…([^)\s]+)(?:\s*\))?`。
写成「有就吃、没有也认」而不是硬要求括号：真实正文里偶尔有地址本身带不成对括号的野写法
（`[x](http://a(b)`），硬要求会让整条链接退化成原文印出来，比现在更糟。

验收（探针，红→绿）：
```
# 取正文里某个链接，看它后面那个文本节点
var a=[...document.querySelectorAll('.md-body a')].find(x=>x.href.includes('fabric-loader'))
String(a.nextSibling.textContent).slice(0,20)
# 修前："), the (mostly) version…"   修后：", the (mostly) version…"
```
整合包详情页同样受益（实测该页 88 个链接，修后「链接紧跟右括号」计数为 0）。

### 9.9.6 验收（真机，必跑）

```
# ① 深链直达模组详情
node F:/tmp/p1/probe-shell.mjs "http://127.0.0.1:23456/?page=plugins&mod=market&pm=fabric-api&nosse=1" 1440 1200
#    → tabs=["01介绍","02下载28 个版本"]；asides 里信息栏 pos=sticky top=16px
# ② 三档几何：1920 左右余量相等（实测各 196）；1440 余量 0/0；520 下信息栏 order=1 且 y 落在正文之后
# ③ overX 必须为 false（1920/1440/520 三档）
# ④ 端到端安装：下载页点第一行的「安装」→ curl /api/fetch/tasks 出现 state=done && verified=true
# ⑤ 已安装弹窗：图标 .modicon 实测 48×48；「主页」链接 border 0px / background rgba(0,0,0,0)
```

---

## 9.10 市场列表「就地展开」+ 前置安装卡片队列（2026-10-07）

用户原话：**「为安装页的模组列表默认点击列表后展开该模组的版本列表可快捷下载本体和前置，
可翻页避免过长，详情页从右边的按钮进入」**，并进一步定了前置弹窗的形态：
**卡片式、一次展示多个、装完自动上滑离开、全装完自动关闭、自动先选最新版、提供一键安装。**

### 9.10.1 落点（用户 2026-10-07 拍板）

| 议题 | 决定 |
|---|---|
| 「安装页的列表」指哪个 | **市场搜索结果列表**（不是详情页的下载页签） |
| 版本列表翻页 | **每页 5 条** |
| 快捷装前置怎么和「不静默装最新前置」共存 | **可选**：点本体「安装」只装本体；前置要用户点「装前置（N）」进队列 |
| 前置弹窗形态 | **卡片队列**（详见下） |

### 9.10.2 市场列表：点主体就地展开，右侧独立「详情」

- `.modline` 从 `<button>` 改成 **`<div>`**，内部两块：`.modline-hit`（可点击主体，就地展开）
  + 独立的「详情」按钮（`onPick` → 整屏详情页）。
  ⚠⚠ **button 里不能嵌 button** —— 这是改这行时最容易踩的坑；探针顺手收了 console error，有嵌套会立刻报出来。
- 展开区（`MarketRow`）：**惰性加载**（首次展开才 `modVersions`，之后收起再展开不重复请求）、
  每页 5 条、上一页/下一页 + 「第 n / m 页」、每条可「安装」；有前置的版本另给「装前置（N）」。
- **手风琴**：同一时刻只展开一行。
- **可寻址**：`?expand=<projectId>` 打开即展开；展开/收起只 `replaceState`，不在历史里留跳。

### 9.10.3 前置弹窗 = 卡片队列（`DepQueueDialog.tsx`）

每张卡 = 图标 + 标题 + 简介 + 版本列表（每页 5 条，**默认选最新**，接口本身就是新的在前，
不自己写版本号比较）+ 「将安装：X」 + 安装按钮；顶部「一键安装（N）」。

**「一键安装」凭什么不算「静默装最新」**：每张卡**明确写出它将要装哪一版**，
一键装的也是用户**当前选中**的那一版。没有这行字，这个按钮就是违规的。

### 9.10.4 停下来记住：别在 `setState` 的 updater 里收集「要删掉的 key」

初版写法是在 `setCards(prev => prev.map(...))` 的 updater 里 `done.push(key)`，
调用完再 `done.forEach(markDone)`。**这是错的**：updater 不保证同步执行（React 只在
队列为空时做 eager 求值），拿到的很可能是**空数组** → 卡片被标成隐藏却**永远不从数组摘掉**
→ `cards.length` 到不了 0 → **「全部装完自动关闭」永不触发**。
修法：把要摘掉的 key **在 setState 之外**先算好。

### 9.10.5 停下来记住：卡片「装没装上」要问任务，不能只看文件名

初版同步检查只认「下载文件名出现在已安装列表里」。后果（已实测复现）：
**下载失败时那个文件永远不会出现** → 卡片永远停在「下载中…」，而且 `anyInstalling` 一直为真
→ 1 秒一次的 `/api/plugins` 轮询**永远停不下来**，用户只能关掉弹窗。

修法：卡片记下自己的 `taskId`，轮询**问任务状态**（`running` 继续等 / `done` 摘卡 /
其余把 `err` **写在卡上**并放开重试）。任务状态是权威信号，已安装列表只用来复核。

### 9.10.6 验收（真机，必跑）

```
# ① 展开 / 翻页 / 深链 / 详情按钮 / 卡片队列 / 溢出 / 控制台零报错（1440 与 520 各跑一次）
node F:/tmp/p1/probe-market.mjs http://127.0.0.1:23456 1440 1200
node F:/tmp/p1/probe-market.mjs http://127.0.0.1:23456 520 1000
#   判据：expand.open=true 且 verRows===5；翻页后 pager 变成「第 2 / 40 页」；
#        deeplink==='expanded-on-load'；detailBtn.hasPm 非空；dialog.cards>=1 且 hasSelectedLabel/hasInstallAll 均为 true；
#        geom.overX===false；consoleErrors 为空
# ② 端到端：卡片里点安装 → 卡片滑出 → 弹窗自动关闭（脚本轮询到 dialog 消失为止）
node F:/tmp/p1/probe-depinstall.mjs http://127.0.0.1:23456
#   实测：sawDownloading=true、cardGone=2、dialogGone=true、elapsedSec=4、文件出现在已安装列表
# ③ 反证（证明能抓住旧代码）：注入一个必败的下载任务，卡片必须在几秒内写出错误
node F:/tmp/p1/probe-depfail.mjs http://127.0.0.1:23456
#   实测：secsToError=2，downloading=false、hasErrText=true、canRetry=true
#   ⚠ 旧代码（只认文件名）在这里会一直 downloading=true —— 这就是这条反证的意义
```

出图：`docs/shots/2026-10-07-mk-expand-1440.png`、`...-mk-depqueue-1440.png`、`...-mk-expand-520.png`。

### 9.10.7 用户第二轮反馈（2026-10-07，同一天内改完）

| 反馈 | 落法 |
|---|---|
| **「卡片改成横向双排放置」** | 弹窗 `width` 620 → **920**（`Dialog` 是 `w-full` + `maxWidth`，窄屏自动收，不会比屏还宽）；卡片容器在有 **2 张及以上**时用 `grid items-start gap-3 md:grid-cols-2`，只有 1 张时铺满（半宽悬着更难看）。⚠ `.dep-card` 补了 `min-width:0` —— 网格项默认 `min-width:auto`，长文件名会把卡片撑破 |
| 「打开弹窗就预先判断某个前置已装」 | **不做**（用户拍板）。Modrinth 给的是 `projectId`，已装模组给的是**模组内部名**（`jei` 这种），两者没有可靠映射，硬做只会误报。保持「装完才同步」 |
| 「一键安装要**纳入但提示**」 | 未确认项目的卡没有可选版本，装了就是「静默装最新」，所以**仍然装不了**，但不能让它们在计数里消失：提示文案写出来，且**措辞按场景分两种** —— 旁边真有「一键安装」按钮时才说「另有 N 个…」，只有 find 卡时改成「N 个前置还没确认是哪个项目 —— 先在上面挑出来，才谈得上选版本安装。」；没有可装项时**不渲染**「一键安装」按钮（不给假按钮） |

验收（真机，1440 与 520 各跑）：

```
node F:/tmp/p1/probe-2col.mjs http://127.0.0.1:23456 1440 1200
#   用 Sodium Extra 那个「一个版本带 2 个前置」的版本（PtjYWJkn … mc1.20.1-0.5.9）打开队列
#   判据：dlgW===920、cards===2、sideBySide===true、rects 两张 y 相同且 x 相差 ≈450、overX===false
#   520 下同一支脚本应给出 sideBySide===false（同 x 不同 y，退化成单列）且 overX===false
node F:/tmp/p1/probe-findhint.mjs http://127.0.0.1:23456
#   从已安装的 Cardboard 详情点「选版本装」→ 打开只有内部名（icommon）的队列
#   判据：findStageText===true、unresolvedHint===true、hasInstallAll===false（没有可装项就不给按钮）
```

出图：`docs/shots/2026-10-07-mk-depqueue-2col-1440.png`、`...-2col-520.png`、`...-find-1440.png`。
