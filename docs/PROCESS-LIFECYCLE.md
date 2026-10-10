# 进程生命周期：关掉面板之后，服务器怎么办？

> 调研日期：2026-10-09。起因是用户的一句提问：
> **「假如我开服器不小心关了，再次打开，那之前开启的服务器怎么办？」**
>
> 查完发现**问题真实存在**，而且「加个 flag 让它脱离」这条看着最顺的路**走不通**。
> 这份文档把现状、实测、以及真正可行的方案记下来，免得下次又从头试一遍。

---

## 一、现状（`grep` 结论，不是推测）

```
serverbox-desktop.exe (Wails 壳)
    └─ detach → serverbox.exe (守护进程)      ← cmd/desktop/main.go:193 有 detach ✓
         └─ exec.Command(java)                ← internal/daemon/process.go 没有 ✗
```

**只做了「守护进程脱离桌面壳」这一层，「守护进程 → java」这层没做。**
（`internal/daemon/process.go` 启动 java 时没有设 `SysProcAttr`。）

后果两条：

1. **关面板 = 硬杀 java**。Windows 上子进程沿用父进程的控制台，控制台一关，
   系统给挂在上面的进程发 `CTRL_CLOSE_EVENT` → 进程没装处理器就是**直接结束**。
   注意这是**硬杀**，不是 `stop` —— **存档来不及写盘**，有损坏风险。
2. **重开面板后状态是错的**：没有接管逻辑（grep 不到 `reconcile` / `adopt` / 孤儿进程扫描），
   面板会显示「已停止」，而 java 其实还在后台跑。这时用户点「启动」→
   **开出第二个服务器** → 端口冲突，且**两个进程同时写同一个世界文件**。

---

## 二、试过的路：给它加 `DETACHED_PROCESS`（**失败，已回退**）

想法很直白：桌面壳能脱离，java 也照做一遍就行。按 `cmd/desktop/proc_windows.go` 的现成做法，
给 java 加上 `DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP`。

**结果：`internal/daemon` 的核心护栏测试挂了。**

| 组合 | 结果 |
|---|---|
| 不加 flag | **PASS，2.2 秒** |
| 加 `DETACHED_PROCESS \| CREATE_NEW_PROCESS_GROUP` | **FAIL，60 秒超时** |

（挂的是 `TestE2EConsoleStopIsNotCrash` —— 停服三层判定的端到端测试，用真 Java 跑的。）

### 已经排除的两个想当然的解释

1. **不是「java 起不来」。** 单独写了 flag 对照实验（`F:/tmp/flagprobe`，`java -version`）：

   | CreationFlags | 结果 |
   |---|---|
   | 无 | 71ms，拿到版本输出 |
   | `DETACHED_PROCESS` | 67ms，拿到版本输出 |
   | `CREATE_NEW_PROCESS_GROUP` | 66ms，拿到版本输出 |
   | 两者都有 | 68ms，拿到版本输出 |

   **四个组合全部正常。**

2. **不是「输出没 flush」。** 假服务端 `tools/fakeserver/FakeServer.java:108` 明确调了
   `System.out.flush()`。

**结论（现象层面）**：detached 之后，**这个常驻进程的 stdout 管道拿不到数据**。
**根因未定位** —— 但更重要的是下面这条：

### ⚠⚠ 真正致命的问题：管道断了就接不回来

就算把上面那个原因查清、java 成功脱离了，**「接管」依然做不到**，因为：

> 面板是通过 **stdin/stdout 管道**跟 java 说话的。面板一退，**管道就断了**。
> 重新打开面板时，那个 java 还在跑，但它的 stdout 指向的是**一条已经没有读者的管道** ——
> **没有任何办法重新接上去。**

所以「脱离 + 接管」用管道方案**自相矛盾**。这不是实现细节，是架构层面的死结。

---

## 三、真正可行的方案

### 方案 B：绕开管道（读日志文件 + RCON）

MC 服务端**本来就会写 `logs/latest.log`**。所以：

| 能力 | 脱离后怎么做 |
|---|---|
| 看日志 | **读 `logs/latest.log`**（tail），不依赖管道 ✓ |
| 发命令 | 走 **RCON**（`enable-rcon=true` + `rcon.password`）✓ |
| 判存活 | 按 pid 文件 / 进程特征扫描 |

代价：
- 要**改写实例的 `server.properties`** 开 RCON（面板可以代劳，但必须**告知用户**——
  这等于在服务器上开了一个远程管理端口，是**安全相关的改动**）。
- 日志走文件有个自然延迟（MC 写盘缓冲），不像管道那样即时。
- 工作量明显大于「加个 flag」：pid 文件、孤儿进程扫描、RCON 客户端、
  「这个实例现在是谁在管」的状态机。

### 方案 C：不做"脱离"，改成**优雅停服**（低成本，先消除伤害）

保持现在的架构不变，只做一件事：**面板捕获自己的关闭事件，退出前给所有运行中的实例发 `stop`，
等它们写盘完成再退。**

- ✅ 消除最危险的那条：**不会再有硬杀导致的存档损坏**
- ✅ 改动小、不碰 RCON、不碰 server.properties
- ❌ 服仍然会停（不满足"服继续跑"）

---

## 四、建议

1. **先做 C**：它把「关窗口 = 硬杀 = 可能坏档」这个最要命的风险去掉，成本很低。
2. **再做 B**：那才是用户问的「再次打开，之前的服怎么办」的完整答案。
   但 B 要先跟用户确认一件事 —— **是否接受「为了接管，面板要帮你在实例里开 RCON」**。
3. **不要**再试「加个 flag 就脱离」：已证伪，且就算成功也接不回来。

---

## 附：本次实测的命令与判据

```bash
# 对照实验（核心判据）
go test ./internal/daemon/ -run TestE2EConsoleStopIsNotCrash -v
#   不加 flag → PASS 2.2s ／ 加 flag → FAIL 60s

# flag 对照实验（排除「java 起不来」）
cd F:/tmp/flagprobe && go run .    # 四个组合全部 67ms 正常
```
