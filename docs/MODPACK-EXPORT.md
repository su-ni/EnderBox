# 一键制作整合包：可行性研究 + 方案

> 调研日期：2026-10-09。方法：**先打真接口**（`external-api-probe` 的规矩），不采信文档记忆。
> 相关：`docs/MODPACK.md`（导入 / 升级的既有设计）、`internal/modpack/`（2233 行，已能导入 + 升级）。

---

## 一、结论

**可行，而且地基已经现成。**

「一键制作整合包」＝ **导入的反向操作**：把当前实例里的模组与配置，打成一个能分发给玩家的
`.mrpack`。核心依赖只有一条 —— **把本地 jar 反查回它的下载地址**（这样包体只要几十 KB，
而不是把几百 MB 的 jar 全塞进去）。这条已实测通过。

现有的 `internal/modpack/` 里 `mrpack.go`（解析）、`packstate.go`（身份）、`executor.go`（落地）
**全是可复用的**：导出要写的 `modrinth.index.json` 和导入要读的是**同一个格式**。

---

## 二、实测事实（2026-10-09，本机直连）

样本：`Cardboard-1.20.jar`（5,867,092 字节，sha1 `0e5b1596a6258dd9d551c19236bff42dd974b400`）

### 2.1 单个反查

```
GET https://api.modrinth.com/v2/version_file/{sha1}?algorithm=sha1
→ 200，816 bytes
```

返回**完整的 version 对象**（不是精简版）：

| 字段 | 值 | 用途 |
|---|---|---|
| `project_id` | `MLYQ9VGP` | 回查项目名/图标 |
| `version_number` | `1.20.1-13` | 写进清单 |
| `game_versions` | `["1.20.1"]` | **推导包要声明的游戏版本** |
| `loaders` | `["fabric"]` | **推导加载器** |
| `environment` | `dedicated_server_only` | 服务端专用标记 |
| `files[0].url` | `https://cdn.modrinth.com/data/MLYQ9VGP/versions/LuMXUGLh/Cardboard-1.20.jar` | **直接写进 downloads** |
| `files[0].hashes.sha1 / sha512` | 齐全 | 写进 hashes，分发端可校验 |
| `files[0].size` | 5867092 | 写进 fileSize |
| `dependencies` | `[]` | 依赖关系 |

### 2.2 批量反查（打包要靠它，别一个一个打）

```
POST https://api.modrinth.com/v2/version_files
body: {"hashes":[...], "algorithm":"sha1"}
→ 200
```

- 顶层是 **dict**，`key = 请求时给的 hash`，`value = version 对象`。
- ⚠⚠ **查不到的 hash 直接不出现在返回里**（不是在值里给 `null`）。
  实测：给 2 个 hash（1 真 1 假），返回 **只有 1 条**。
  → **这就是判定「本地自制 mod」的依据：请求集 − 返回集 = 查不到的那些。**

### 2.3 没测但要注意的

- **批量接口的条数上限**：没实测到上限，实现时要分批（按 100 一批）+ 失败重试，
  不能假设「一次能塞几百个」。
- **`environment` 的取值**：这次样本是 `dedicated_server_only`。做**服务端包**时应当
  只收 `dedicated_server_only` 与 `*`，把 `client_only` 的挑出来**明确告知用户**
  （它们装到服务端上没用，但直接丢掉会让用户以为我们漏了文件）。
- **速率限制**：Modrinth 有速率限制但本次未触顶。实现时要处理 429。

---

## 三、方案

### 3.1 导出流程（五步）

```
① 扫描实例        mods/*.jar + config/ + 可选的 scripts/、defaultconfigs/
② 算 sha1         每个 jar 流式算（不能整个读进内存，整合包的 jar 可能上百 MB）
③ 批量反查        POST /version_files，分批 + 重试
④ 生成清单        modrinth.index.json（格式见下）
⑤ 打包 zip        index.json + overrides/ → .mrpack
```

### 3.2 `modrinth.index.json`（导出要产出的东西）

```json
{
  "formatVersion": 1,
  "game": "minecraft",
  "versionId": "1.0.0",
  "name": "生存服 · 长期档",
  "files": [
    {
      "path": "mods/Cardboard-1.20.jar",
      "hashes": { "sha1": "0e5b1596...", "sha512": "e9ae527d..." },
      "downloads": ["https://cdn.modrinth.com/data/MLYQ9VGP/versions/LuMXUGLh/Cardboard-1.20.jar"],
      "fileSize": 5867092
    }
  ],
  "dependencies": { "minecraft": "1.20.1", "fabric-loader": "0.15.0" }
}
```

- `dependencies` 的 `minecraft` / 加载器版本：**从实例的 server.properties + 反查结果推导**，
  推导不出就**不写这一项**（别编一个）。
- **查不到的 jar**：两种处理，要选一种并说清 ——
  - **A（默认）塞进 `overrides/mods/`**：包能用，但体积变大；界面上标出「这 N 个是本地模组」。
  - **B 只列清单不打包**：包很小，但装的人**装不齐**（少 mod 会起不来）。
  → 默认走 A（**能跑起来**优先于包小），并在导出结果里如实列出哪些文件走了 overrides。

### 3.3 安全底线（照搬导入侧的三条）

1. **zip-slip**：写进 zip 的路径一律用固定前缀拼，拒绝 `..` 与绝对路径（导入侧已有
   `TestExecuteBlocksZipSlip`，导出侧要对称地防）。
2. **路径必须在实例目录内**：扫描时用导入侧的 `resolveWithin` 思路，别让符号链接带出去。
3. **不碰用户没让碰的文件**：只收明确列出的目录（`mods/` `config/` 等），
   不做「整个实例打包」—— 那会把 `world/`、`logs/`、`server.jar` 一起塞进去。

### 3.4 明确不做

- **不做客户端整合包**（`MODPACK.md` §3.4 已经写了理由，继续沿用）：
  我们管的是服务端，客户端包该由启动器管。
- **不自动上传到 Modrinth**：那涉及账号授权，且是**外部发布动作**，必须用户手动确认。

---

## 四、界面入口建议

放在**整合包页**（`page=modpack`）里，与「导入整合包」并列 ——
它们是一对反向操作，放一起用户才找得到：

```
整合包
├─ 导入整合包（已有）
└─ 制作整合包（新增）→ 选要收的目录 → 预览清单 → 导出
```

⚠ 预览那一步不能省：用户得先看到「会打包哪些文件、哪些查不到会走 overrides」，
再决定导不导。**发出去的包改不了**。

---

## 六、需求访谈结论（2026-10-09，grill-me）

按 grill-me 的规矩逐条走完五个决策分支，结论如下 —— **实现以这张表为准**：

| 分支 | 结论 | 备注 |
|---|---|---|
| **受众** | **客户端包 + 服务端包都做** | ⚠ **推翻** `MODPACK.md` §3.4「不做客户端整合包」那条。旧理由是「客户端包该由启动器管」，但用户的真实场景是「发个包给朋友，他一装就能进我服」——这件事目前完全空白 |
| **格式** | 两边**统一 `.mrpack`** | PCL2/HMCL/Prism 都认（客户端必须能被启动器装）；我们自己的导入也读它（一份解析器两用）；包体几十 KB |
| **导出方式** | **一次可选**：客户端 / 服务端 / 两个都要 | 扫描与反查只做一遍，避免两个包里的 mod 版本对不上 |
| **反查不到的 mod** | **默认塞进 `overrides/mods/`** | 「发出去的包改不了」——收包人装不起来，他自己没法解决；包大只是下载慢。导出结果里**如实列出哪些文件走了 overrides** |
| **收录目录** | 默认勾 `mods/` `config/` `defaultconfigs/` `scripts/` `kubejs/`；**仅客户端包**额外勾 `resourcepacks/` `shaderpacks/` | **一律不收**：`world/`（那是用户的存档，不是模组内容）、`logs/`、`crash-reports/`、`server.jar`、`libraries/`、`server.properties`（含本机端口） |
| **包身份** | **自动推导 + 可改** | 名字取实例名；游戏版本 / 加载器从 `server.properties` + 反查结果反推（推不出**就不写**，不编）。界面里可改——发出去的包，名字和版本是门面 |
| **去向** | **存本地 + 打开文件夹**，不自动上传 | 生成到实例旁的 `packs/`。上传 Modrinth 要账号授权，且属于**对外发布动作**，必须用户手动做 |

### 由此带来的两条实现要求

1. **两个包要按 `environment` 分流**（反查结果里就有这个字段，实测样本是 `dedicated_server_only`）：
   - 客户端包：收 `client_only` 与 `*`
   - 服务端包：收 `dedicated_server_only` 与 `*`
   - ⚠ 两边都不收的（被剔除的）**要在预览里列出来**并说明原因，
     直接丢掉会让用户以为我们漏了文件。
2. **`config/` 在两边都收**，但界面上要能看出来「这个包收了哪些目录」——
   同一个 config 目录同时进两个包是正常的（多数模组配置两边通用）。

---

## 七、工作分解（估）

| # | 事项 | 说明 |
|---|---|---|
| 1 | `internal/modpack/export.go` | 扫描 + 算 sha1 + 反查 + **按 environment 分流** + 生成两份清单 + 打包。**纯逻辑，可单测** |
| 2 | Modrinth 客户端加 `VersionFilesByHash` | 批量反查 + 分批（100/批）+ 429/5xx 重试 |
| 3 | API：`POST /api/instances/{id}/modpack/export` | **preview（dry-run）与真导出两个动作** |
| 4 | 前端：整合包页加「制作整合包」入口 | 与「导入整合包」并列（一对反向操作）；预览对话框复用 `DepDialog` 的两段式做法 |
| 5 | 测试 | 算哈希、差集判定自制 mod、**environment 分流**、zip-slip、不越界、清单格式 |
