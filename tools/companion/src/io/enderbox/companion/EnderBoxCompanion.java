package io.enderbox.companion;

import java.io.File;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.text.SimpleDateFormat;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Comparator;
import java.util.Date;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;

import org.bukkit.Bukkit;
import org.bukkit.GameMode;
import org.bukkit.Location;
import org.bukkit.Material;
import org.bukkit.World;
import org.bukkit.enchantments.Enchantment;
import org.bukkit.entity.Player;
import org.bukkit.inventory.ItemStack;
import org.bukkit.inventory.PlayerInventory;
import org.bukkit.inventory.meta.Damageable;
import org.bukkit.inventory.meta.ItemMeta;
import org.bukkit.inventory.meta.PotionMeta;
import org.bukkit.plugin.java.JavaPlugin;
import org.bukkit.potion.PotionEffect;
import org.bukkit.scheduler.BukkitTask;

/**
 * EnderBox 伴侣插件 —— 一个**纯观察者**。
 *
 * 设计上最要紧的一条：**不注册任何事件监听器**。
 * 我们只挂了两个定时器，一个定时读服务端内存状态写文件，一个定时收命令。
 * 这样做的原因不是偷懒，是为了能在**混合端**（Arclight / Mohist / CatServer / Cardboard）
 * 上也活下去：这些端把 Bukkit API 暴露出来了，但上游作者明确拒绝为它们提供支持，
 * 一旦我们的插件和别的插件抢事件、改事件，出问题时没人会管，用户只会骂面板。
 * 「只读 + 定时」把冲突面压到接近零。
 *
 * 通信走文件，不走网络：
 *
 * <pre>
 *   plugins/EnderBoxCompanion/bridge/snapshot.json        插件 → 面板（实时状态）
 *   plugins/EnderBoxCompanion/bridge/inbox/cmd-&lt;id&gt;.json  面板 → 插件（要执行的操作）
 *   plugins/EnderBoxCompanion/bridge/outbox/res-&lt;id&gt;.json 插件 → 面板（执行结果）
 * </pre>
 *
 * 用「一份命令一个文件」而不是一个 cmd.json：面板和插件会同时读写这个目录，
 * 单个文件必然出现「插件还没读完，面板就覆盖了」的竞态。
 * 文件名里带上面板自己生成的 id，谁也不会动谁的文件。
 */
public final class EnderBoxCompanion extends JavaPlugin {

    static final String VERSION = "0.2.0";
    /** 目录名。面板按这个名字找，改了两边就对不上。 */
    static final String BRIDGE = "bridge";

    /** 支持的操作。这既是执行分发的白名单，也是报错时能告诉用户的「你能用哪些」。 */
    private static final java.util.Set<String> OPS = new java.util.LinkedHashSet<>(
            java.util.Arrays.asList("give", "clear", "tp", "heal", "feed", "gamemode", "kill"));

    private File bridgeDir;
    private File inboxDir;
    private File outboxDir;

    /** 离线玩家的最后一次快照。离开服之后界面上还能看到「他最后在哪、剩多少血」。 */
    private final Map<UUID, Map<String, Object>> lastSeen = new ConcurrentHashMap<>();

    private BukkitTask snapshotTask;
    private BukkitTask inboxTask;

    @Override
    public void onEnable() {
        saveDefaultConfig();
        bridgeDir = new File(getDataFolder(), BRIDGE);
        inboxDir = new File(bridgeDir, "inbox");
        outboxDir = new File(bridgeDir, "outbox");
        // 建不出来就明说，别等到写快照时才抛一堆看不懂的异常
        if (!inboxDir.isDirectory() && !inboxDir.mkdirs()) {
            getLogger().severe("无法创建目录：" + inboxDir.getPath() + " —— 面板将读不到任何数据。");
        }
        if (!outboxDir.isDirectory() && !outboxDir.mkdirs()) {
            getLogger().severe("无法创建目录：" + outboxDir.getPath() + " —— 面板将收不到操作结果。");
        }

        int snapTicks = Math.max(20, getConfig().getInt("snapshot-ticks", 40));
        int inboxTicks = Math.max(5, getConfig().getInt("inbox-ticks", 10));

        // 快照必须跑在主线程：读 Bukkit API 的玩家/背包状态在异步线程上是不安全的。
        snapshotTask = Bukkit.getScheduler().runTaskTimer(this, this::writeSnapshot, 20L, snapTicks);
        // 收命令只是扫目录，异步跑；真正执行时再切回主线程。
        inboxTask = Bukkit.getScheduler().runTaskTimerAsynchronously(this, this::pollInbox, 20L, inboxTicks);

        getLogger().info("EnderBoxCompanion " + VERSION + " 已启用（MC " + Bukkit.getBukkitVersion()
                + "，快照间隔 " + snapTicks + " tick，桥接目录 " + bridgeDir.getPath() + "）");
        logCaps();
    }

    @Override
    public void onDisable() {
        if (snapshotTask != null) {
            snapshotTask.cancel();
        }
        if (inboxTask != null) {
            inboxTask.cancel();
        }
        // 停服前补一份：这样面板在「已停止」状态下看到的仍是最后一刻的真实数据，
        // 而不是一份半小时前的旧快照加上一句「服务器没在跑」。
        writeSnapshot();
    }

    /**
     * 启动时探测这台服务端到底提供了哪些「可选 API」，打一行日志。
     *
     * 为什么值得做：插件在好几代服务端上跑，反射拿不到的字段会**静默消失** ——
     * 界面上就是「这一栏空着」，用户没法区分「这台服务端没有」和「插件坏了」。
     * 这里在 Player.class 上探测而不是在某个实例上探测：Player 是服务端自己加载的 API 类，
     * 有就是真有。
     */
    private void logCaps() {
        StringBuilder sb = new StringBuilder("可选能力：");
        sb.append("延迟=").append(has(Player.class, "getPing") ? "有" : "无（走 spigot() 兜底）");
        sb.append("，皮肤=").append(has(Player.class, "getPlayerProfile") ? "有" : "无（Paper 专有）");
        sb.append("，末影箱=").append(has(Player.class, "getEnderChest") ? "有" : "无");
        sb.append("，维度键=").append(has(World.class, "getKey") ? "有" : "无（退回环境名）");
        getLogger().info(sb.toString());
    }

    private static boolean has(Class<?> c, String method) {
        try {
            c.getMethod(method);
            return true;
        } catch (Throwable t) {
            return false;
        }
    }

    // ── 快照 ──────────────────────────────────────────────────────────────

    private void writeSnapshot() {
        try {
            Map<String, Object> root = Json.obj();
            root.put("pluginVersion", VERSION);
            root.put("serverVersion", Bukkit.getVersion());
            root.put("bukkitVersion", Bukkit.getBukkitVersion());
            root.put("generatedAt", nowISO());
            root.put("onlineCount", Bukkit.getOnlinePlayers().size());
            root.put("maxPlayers", Bukkit.getMaxPlayers());

            List<Object> online = Json.arr();
            for (Player p : Bukkit.getOnlinePlayers()) {
                Map<String, Object> e = describe(p, true);
                // 缓存一份，玩家下线后界面还能看到「最后一次见到他」
                e.remove("online");
                lastSeen.put(p.getUniqueId(), new HashMap<>(e));
                e.put("online", true);
                online.add(e);
            }
            root.put("online", online);

            List<Object> offline = Json.arr();
            for (Map.Entry<UUID, Map<String, Object>> en : sortedByLastSeen()) {
                if (Bukkit.getPlayer(en.getKey()) == null) {
                    en.getValue().put("online", false);
                    offline.add(en.getValue());
                }
            }
            root.put("offline", offline);

            writeAtomic(new File(bridgeDir, "snapshot.json"), Json.write(root));
        } catch (Throwable t) {
            // 快照失败绝不能把服务端带崩，也不能刷屏：只报一次原因。
            getLogger().warning("写快照失败：" + t);
        }
    }

    /** 最近见过的排前面。缓存别无限长：一个开了一年的服可能进出过几百人。 */
    private List<Map.Entry<UUID, Map<String, Object>>> sortedByLastSeen() {
        List<Map.Entry<UUID, Map<String, Object>>> all = new ArrayList<>(lastSeen.entrySet());
        all.sort((a, b) -> Long.compare(tsOf(b.getValue().get("lastSeenAt")), tsOf(a.getValue().get("lastSeenAt"))));
        while (lastSeen.size() > 200) {
            Map.Entry<UUID, Map<String, Object>> oldest = all.isEmpty() ? null : all.get(all.size() - 1);
            if (oldest == null) {
                break;
            }
            lastSeen.remove(oldest.getKey());
            all.remove(all.size() - 1);
        }
        return all;
    }

    private static long tsOf(Object o) {
        if (o instanceof Number) {
            return ((Number) o).longValue();
        }
        return 0L;
    }

    /** 一个玩家的实时状态。offline 为 false 时坐标等字段是最后一次见到的值。 */
    private Map<String, Object> describe(Player p, boolean includeInventory) {
        Map<String, Object> e = Json.obj();
        UUID uuid = p.getUniqueId();
        e.put("uuid", uuid.toString());
        e.put("name", p.getName());
        e.put("displayName", safe(p.getDisplayName()));
        e.put("online", true);
        e.put("lastSeenAt", System.currentTimeMillis());

        Location loc = p.getLocation();
        World w = loc.getWorld();
        e.put("world", w == null ? "" : w.getName());
        e.put("dim", dimKey(w));
        e.put("x", Json.num(loc.getX()));
        e.put("y", Json.num(loc.getY()));
        e.put("z", Json.num(loc.getZ()));
        e.put("yaw", Json.num(loc.getYaw()));
        e.put("pitch", Json.num(loc.getPitch()));

        e.put("health", Json.num(p.getHealth()));
        e.put("maxHealth", Json.num(maxHealth(p)));
        e.put("food", p.getFoodLevel());
        e.put("saturation", Json.num(p.getSaturation()));
        e.put("level", p.getLevel());
        e.put("exp", Json.num(p.getExp()));
        e.put("gamemode", p.getGameMode() == null ? "" : p.getGameMode().name());
        e.put("op", p.isOp());
        e.put("dead", p.isDead());

        // 以下三项在老版本服务端上可能压根没有这个方法（1.17 之前没有 PlayerProfile）。
        // 逐个单独兜住，缺哪个就少哪个字段，绝不能因为「想多给一点」让整个插件红掉。
        int ping = pingOf(p);
        if (ping >= 0) {
            e.put("ping", ping);
        }
        long first = longMethod(p, "getFirstPlayed");
        if (first > 0) {
            e.put("firstPlayed", first);
        }
        long last = longMethod(p, "getLastPlayed");
        if (last > 0) {
            e.put("lastPlayed", last);
        }
        String skin = skinUrl(p);
        if (!skin.isEmpty()) {
            e.put("skin", skin);
        }

        List<Object> effects = Json.arr();
        for (PotionEffect pe : p.getActivePotionEffects()) {
            Map<String, Object> m = Json.obj();
            m.put("id", keyOf(pe.getType()));
            m.put("amplifier", pe.getAmplifier());
            m.put("duration", pe.getDuration());
            m.put("ambient", pe.isAmbient());
            effects.add(m);
        }
        e.put("effects", effects);

        if (includeInventory) {
            // ⚠ 必须是 PlayerInventory：头盔/胸甲/主手这些是玩家背包特有的方法，
            // 父类 Inventory 上没有（编译期就会报「找不到符号」）。
            PlayerInventory inv = p.getInventory();
            e.put("inventory", items(Arrays.asList(inv.getContents())));
            Map<String, Object> eq = Json.obj();
            eq.put("helmet", item(inv.getHelmet()));
            eq.put("chestplate", item(inv.getChestplate()));
            eq.put("leggings", item(inv.getLeggings()));
            eq.put("boots", item(inv.getBoots()));
            eq.put("mainhand", item(inv.getItemInMainHand()));
            eq.put("offhand", item(inv.getItemInOffHand()));
            e.put("equipment", eq);
            try {
                e.put("enderChest", items(Arrays.asList(p.getEnderChest().getContents())));
            } catch (Throwable ignored) {
                // 极少数端没有末影箱 API，那就没有
            }
        }
        return e;
    }

    // ── 命令 ──────────────────────────────────────────────────────────────

    /** 扫收件箱。只做文件 IO，真正的执行切回主线程。 */
    private void pollInbox() {
        File[] files;
        try {
            files = inboxDir.listFiles((d, n) -> n.endsWith(".json"));
        } catch (Throwable t) {
            return;
        }
        if (files == null || files.length == 0) {
            return;
        }
        // 按文件名排序：cmd-0001 先于 cmd-0002，面板的下单顺序不该被文件系统打乱
        Arrays.sort(files, Comparator.comparing(File::getName));
        for (File f : files) {
            String body;
            try {
                body = new String(Files.readAllBytes(f.toPath()), StandardCharsets.UTF_8);
            } catch (Throwable t) {
                continue;
            }
            Map<String, Object> cmd = null;
            try {
                cmd = parse(body);
            } catch (Throwable t) {
                cmd = null;
            }
            String id = cmd == null ? "" : String.valueOf(cmd.get("id"));
            if (id.isEmpty() || id.equals("null")) {
                // ⚠ 读到空/半截文件的概率不低：面板创建文件的瞬间我们就扫到了。
                // 早期版本这时会把文件直接删掉然后 continue —— 命令凭空消失，
                // 面板永远等不到回音，用户只看到一直转圈。现在改成留着下一轮再试；
                // 只有超过 30 秒还是废的，才认它是真废并回一个错误。
                long age = System.currentTimeMillis() - f.lastModified();
                if (age < 30_000) {
                    continue;
                }
                id = f.getName().replaceFirst("^cmd-", "").replaceFirst("\\.json$", "");
                cmd = new HashMap<>();
                cmd.put("id", id);
                cmd.put("op", "");
            }
            // 先删再执行：哪怕执行过程中服务端崩了，重启后也不会把这条命令再跑一遍。
            try {
                Files.deleteIfExists(f.toPath());
            } catch (Throwable ignored) {
                // 删不掉就靠 res 文件去重，不至于重复执行
            }
            File res = new File(outboxDir, "res-" + safeFileName(id) + ".json");
            if (res.exists()) {
                continue; // 已经执行过了
            }
            // lambda 只能抓 effectively final 的变量，而 id/cmd 在上面可能被改过 —— 拷一份
            final String fid = id;
            final Map<String, Object> fcmd = cmd;
            Bukkit.getScheduler().runTask(this, () -> {
                Map<String, Object> out = Json.obj();
                out.put("id", fid);
                out.put("op", String.valueOf(fcmd.get("op")));
                out.put("doneAt", System.currentTimeMillis());
                try {
                    String msg = execute(fcmd);
                    out.put("ok", true);
                    if (msg != null && !msg.isEmpty()) {
                        out.put("message", msg);
                    }
                } catch (Throwable t) {
                    out.put("ok", false);
                    out.put("error", String.valueOf(t.getMessage() == null ? t : t.getMessage()));
                }
                try {
                    writeAtomic(res, Json.write(out));
                } catch (Throwable ignored) {
                    // 结果写不出去也要留痕，否则面板会一直转圈
                    getLogger().warning("写结果文件失败：" + res.getName());
                }
            });
        }
        trimDir(outboxDir, 200);
    }

    /**
     * 执行一条面板发来的操作。
     *
     * 这里每一个分支都有可能失败（玩家不在线、物品名不认识、坐标是空的），
     * 失败一律抛异常并把原因带回面板 —— 面板那边会原样显示，不许吞掉。
     */
    private String execute(Map<String, Object> cmd) throws Exception {
        String op = str(cmd.get("op"));
        String target = str(cmd.get("target"));
        Map<String, Object> args = asMap(cmd.get("args"));

        // ⚠ 先认操作，再找人。反过来的话，一个未知操作会报成「玩家不在线」——
        // 用户会以为是自己名字打错了，实际上是面板发了个我们没实现的动作。
        if (!OPS.contains(op)) {
            throw new Exception("不支持的操作：" + op + "（支持的：" + String.join("、", OPS) + "）");
        }

        Player p = findPlayer(target);
        if (p == null) {
            throw new Exception("玩家不在线：" + target + " —— 这些操作需要他正在服里");
        }
        switch (op) {
            case "give": {
                String mat = str(args.get("item"));
                if (mat.isEmpty()) {
                    throw new Exception("没给物品名");
                }
                Material m = Material.matchMaterial(mat);
                if (m == null || m == Material.AIR) {
                    throw new Exception("不认识的物品：" + mat);
                }
                int count = intArg(args.get("count"), 1);
                if (count <= 0) {
                    throw new Exception("数量必须大于 0");
                }
                int left = count;
                // 一次最多一堆：直接 new ItemStack(m, 200) 会在超上限时被静默截断
                int max = Math.max(1, m.getMaxStackSize());
                int given = 0;
                while (left > 0) {
                    int n = Math.min(max, left);
                    Map<Integer, ItemStack> overflow = p.getInventory().addItem(new ItemStack(m, n));
                    int back = 0;
                    for (ItemStack s : overflow.values()) {
                        back += s.getAmount();
                    }
                    given += (n - back);
                    left -= n;
                    if (back > 0) {
                        // 背包满了：剩下的丢在地上，别让物品凭空消失
                        ItemStack drop = new ItemStack(m, back);
                        p.getWorld().dropItem(p.getLocation(), drop);
                    }
                }
                return "已给 " + p.getName() + " " + given + " 个 " + m.getKey().getKey();
            }
            case "clear": {
                boolean armor = boolArg(args.get("armor"), true);
                p.getInventory().clear();
                if (armor) {
                    p.getInventory().setHelmet(null);
                    p.getInventory().setChestplate(null);
                    p.getInventory().setLeggings(null);
                    p.getInventory().setBoots(null);
                }
                return "已清空 " + p.getName() + " 的背包";
            }
            case "tp": {
                String to = str(args.get("to"));
                if (!to.isEmpty()) {
                    Player other = findPlayer(to);
                    if (other == null) {
                        throw new Exception("目标玩家不在线：" + to);
                    }
                    p.teleport(other.getLocation());
                    return "已把 " + p.getName() + " 传送到 " + other.getName();
                }
                double x = dblArg(args.get("x"), Double.NaN);
                double y = dblArg(args.get("y"), Double.NaN);
                double z = dblArg(args.get("z"), Double.NaN);
                if (Double.isNaN(x) || Double.isNaN(y) || Double.isNaN(z)) {
                    throw new Exception("坐标不完整（x/y/z 缺一个）");
                }
                World w = p.getWorld();
                String wn = str(args.get("world"));
                if (!wn.isEmpty()) {
                    World ow = Bukkit.getWorld(wn);
                    if (ow == null) {
                        throw new Exception("没有这个世界：" + wn);
                    }
                    w = ow;
                }
                Location loc = new Location(w, x, y, z);
                if (args.get("yaw") != null) {
                    loc.setYaw((float) dblArg(args.get("yaw"), 0));
                }
                if (args.get("pitch") != null) {
                    loc.setPitch((float) dblArg(args.get("pitch"), 0));
                }
                p.teleport(loc);
                return "已把 " + p.getName() + " 传送到 " + w.getName() + " " + (int) x + "," + (int) y + "," + (int) z;
            }
            case "heal": {
                p.setHealth(maxHealth(p));
                p.setFoodLevel(20);
                p.setSaturation(20);
                p.setFireTicks(0);
                p.setRemainingAir(p.getMaximumAir());
                return "已治疗 " + p.getName();
            }
            case "feed": {
                p.setFoodLevel(20);
                p.setSaturation(20);
                return "已喂饱 " + p.getName();
            }
            case "gamemode": {
                String gm = str(args.get("mode"));
                if (gm.isEmpty()) {
                    throw new Exception("没给模式");
                }
                GameMode g;
                try {
                    g = GameMode.valueOf(gm.toUpperCase());
                } catch (IllegalArgumentException ex) {
                    throw new Exception("不认识的游戏模式：" + gm);
                }
                p.setGameMode(g);
                return "已把 " + p.getName() + " 切成 " + g.name();
            }
            case "kill": {
                p.setHealth(0);
                return "已击杀 " + p.getName();
            }
            default:
                throw new Exception("不支持的操作：" + op);
        }
    }

    private Player findPlayer(String target) {
        if (target == null || target.isEmpty()) {
            return null;
        }
        try {
            return Bukkit.getPlayer(UUID.fromString(target));
        } catch (IllegalArgumentException ignored) {
            // 不是 UUID，那就是名字
        }
        return Bukkit.getPlayerExact(target);
    }

    // ── 反射兜底：老版本服务端缺的 API ──────────────────────────────────────

    /**
     * 所有「新版本才有」的调用都走反射并吞掉异常。
     *
     * 这不是偷懒，是刻意的：一个想服务 1.18 到 1.21 的插件，
     * 如果直接 import org.bukkit.profile.*，在 1.17 上会在**类加载**阶段就炸，
     * 连一句日志都来不及打。反射让它退化成「这一项没有」。
     */
    private static String skinUrl(Player p) {
        try {
            Object prof = p.getClass().getMethod("getPlayerProfile").invoke(p);
            Object tex = prof.getClass().getMethod("getTextures").invoke(prof);
            Object url = tex.getClass().getMethod("getSkin").invoke(tex);
            return url == null ? "" : url.toString();
        } catch (Throwable t) {
            return "";
        }
    }

    /**
     * 延迟。两个入口都试：
     *   · Paper 把 getPing() 直接放在 Player 上；
     *   · Spigot / CraftBukkit 只在 spigot() 里给了。
     * 只试一个，就会有一半的服务端上「延迟」永远空白，而且看不出原因。
     */
    private static int pingOf(Player p) {
        try {
            Object v = p.getClass().getMethod("getPing").invoke(p);
            if (v instanceof Number) {
                return ((Number) v).intValue();
            }
        } catch (Throwable ignored) {
            // 不是 Paper，试下面那条路
        }
        try {
            Object spigot = p.getClass().getMethod("spigot").invoke(p);
            Object v = spigot.getClass().getMethod("getPing").invoke(spigot);
            return v instanceof Number ? ((Number) v).intValue() : -1;
        } catch (Throwable t) {
            return -1;
        }
    }

    private static long longMethod(Player p, String name) {
        try {
            Object v = p.getClass().getMethod(name).invoke(p);
            return v instanceof Number ? ((Number) v).longValue() : 0L;
        } catch (Throwable t) {
            return 0L;
        }
    }

    /** 世界维度键（minecraft:overworld 这种）。拿不到就退回环境名，聊胜于无。 */
    private static String dimKey(World w) {
        if (w == null) {
            return "";
        }
        try {
            Object key = w.getClass().getMethod("getKey").invoke(w);
            return String.valueOf(key);
        } catch (Throwable t) {
            try {
                return String.valueOf(w.getEnvironment());
            } catch (Throwable ignored) {
                return "";
            }
        }
    }

    /**
     * 最大血量。1.20.5+ 这个值来自属性系统，getMaxHealth() 被标记过时但仍可用；
     * 反射 + 兜底 20 是为了在任何一个版本被彻底移除时，界面上少一个数字而不是整个插件红掉。
     */
    private static double maxHealth(Player p) {
        try {
            Object v = p.getClass().getMethod("getMaxHealth").invoke(p);
            if (v instanceof Number) {
                double d = ((Number) v).doubleValue();
                if (d > 0) {
                    return d;
                }
            }
        } catch (Throwable t) {
            // 老版本/新版本都可能没有，退回 20
        }
        return 20.0;
    }

    private static String keyOf(org.bukkit.potion.PotionEffectType t) {
        try {
            Object key = t.getClass().getMethod("getKey").invoke(t);
            return String.valueOf(key);
        } catch (Throwable e) {
            return String.valueOf(t.getName());
        }
    }

    // ── 物品 ──────────────────────────────────────────────────────────────

    private static List<Object> items(List<ItemStack> stacks) {
        List<Object> out = Json.arr();
        for (int i = 0; i < stacks.size(); i++) {
            ItemStack s = stacks.get(i);
            Map<String, Object> m = item(s);
            if (m != null) {
                m.put("slot", i);
                out.add(m);
            }
        }
        return out;
    }

    /** 序列化一个物品栈。null / 空 / 空气一律返回 null —— 空槽位不该占 JSON 体积。 */
    @SuppressWarnings("deprecation")
    private static Map<String, Object> item(ItemStack s) {
        if (s == null || s.getType() == null || s.getType() == Material.AIR || s.getAmount() <= 0) {
            return null;
        }
        Map<String, Object> m = Json.obj();
        Material mt = s.getType();
        String id;
        try {
            id = mt.getKey().toString();
        } catch (Throwable t) {
            id = mt.name().toLowerCase();
        }
        m.put("id", id);
        m.put("count", s.getAmount());

        ItemMeta meta;
        try {
            meta = s.getItemMeta();
        } catch (Throwable t) {
            meta = null;
        }
        if (meta != null) {
            if (meta.hasDisplayName()) {
                m.put("name", meta.getDisplayName());
            }
            if (meta.hasLore()) {
                List<String> lore = meta.getLore();
                if (lore != null && !lore.isEmpty()) {
                    m.put("lore", new ArrayList<Object>(lore));
                }
            }
            if (meta.isUnbreakable()) {
                m.put("unbreakable", true);
            }
            if (meta.hasCustomModelData()) {
                m.put("modelData", meta.getCustomModelData());
            }
            if (meta instanceof Damageable) {
                Damageable d = (Damageable) meta;
                if (d.hasDamage()) {
                    m.put("damage", d.getDamage());
                }
            }
            int maxDur;
            try {
                maxDur = mt.getMaxDurability();
            } catch (Throwable t) {
                maxDur = 0;
            }
            if (maxDur > 0) {
                m.put("maxDamage", maxDur);
            }
            Map<Enchantment, Integer> ench;
            try {
                ench = meta.getEnchants();
            } catch (Throwable t) {
                ench = null;
            }
            if (ench != null && !ench.isEmpty()) {
                Map<String, Object> em = Json.obj();
                for (Map.Entry<Enchantment, Integer> en : ench.entrySet()) {
                    String k;
                    try {
                        k = en.getKey().getKey().toString();
                    } catch (Throwable t) {
                        k = en.getKey().getName();
                    }
                    em.put(k, en.getValue());
                }
                m.put("enchants", em);
            }
            if (meta instanceof PotionMeta) {
                try {
                    Object data = ((PotionMeta) meta).getBasePotionData();
                    if (data != null) {
                        Object type = data.getClass().getMethod("getType").invoke(data);
                        Object typeKey = type.getClass().getMethod("getKey").invoke(type);
                        m.put("potion", String.valueOf(typeKey));
                        m.put("upgraded", Boolean.TRUE.equals(data.getClass().getMethod("isUpgraded").invoke(data)));
                        m.put("extended", Boolean.TRUE.equals(data.getClass().getMethod("isExtended").invoke(data)));
                    }
                } catch (Throwable ignored) {
                    // 药水数据结构版本间改过，读不到就不给这个字段
                }
            }
        }
        return m;
    }

    // ── 小工具 ────────────────────────────────────────────────────────────

    private static void writeAtomic(File f, String text) throws Exception {
        Path target = f.toPath();
        Path tmp = target.resolveSibling(f.getName() + ".tmp");
        Files.write(tmp, text.getBytes(StandardCharsets.UTF_8));
        try {
            Files.move(tmp, target, StandardCopyOption.REPLACE_EXISTING);
        } catch (Throwable t) {
            Files.deleteIfExists(tmp);
            throw t;
        }
    }

    /** 结果文件别堆成山：超过上限就把最旧的删掉 */
    private static void trimDir(File dir, int keep) {
        File[] files = dir.listFiles((d, n) -> n.endsWith(".json"));
        if (files == null || files.length <= keep) {
            return;
        }
        List<File> list = new ArrayList<>(Arrays.asList(files));
        list.sort(Comparator.comparingLong(File::lastModified));
        for (int i = 0; i < list.size() - keep; i++) {
            list.get(i).delete();
        }
    }

    /** 极简 JSON 解析。只认我们自己的命令文件格式，别的一律当没有。 */
    private static Map<String, Object> parse(String s) {
        Map<String, Object> out = new HashMap<>();
        String t = s.trim();
        if (!t.startsWith("{") || !t.endsWith("}")) {
            return out;
        }
        t = t.substring(1, t.length() - 1);
        int depth = 0;
        StringBuilder cur = new StringBuilder();
        List<String> parts = new ArrayList<>();
        for (int i = 0; i < t.length(); i++) {
            char c = t.charAt(i);
            if (c == '"') {
                cur.append(c);
                boolean esc = false;
                for (int j = i + 1; j < t.length(); j++) {
                    char q = t.charAt(j);
                    cur.append(q);
                    if (esc) {
                        esc = false;
                    } else if (q == '\\') {
                        esc = true;
                    } else if (q == '"') {
                        i = j;
                        break;
                    }
                }
                continue;
            }
            if (c == '{' || c == '[') {
                depth++;
            }
            if (c == '}' || c == ']') {
                depth--;
            }
            if (c == ',' && depth == 0) {
                parts.add(cur.toString());
                cur.setLength(0);
                continue;
            }
            cur.append(c);
        }
        if (cur.length() > 0) {
            parts.add(cur.toString());
        }
        for (String part : parts) {
            int colon = part.indexOf(':');
            if (colon < 0) {
                continue;
            }
            String k = unquote(part.substring(0, colon).trim());
            String v = part.substring(colon + 1).trim();
            out.put(k, scalar(v));
        }
        return out;
    }

    private static Object scalar(String v) {
        if (v.startsWith("\"") && v.endsWith("\"") && v.length() >= 2) {
            return unquote(v);
        }
        if (v.startsWith("{") || v.startsWith("[")) {
            // 嵌套结构（args）目前只需要第一层：这里把整段文本交给调用方二次解析
            return parse(v);
        }
        if (v.equals("true")) {
            return Boolean.TRUE;
        }
        if (v.equals("false")) {
            return Boolean.FALSE;
        }
        if (v.equals("null")) {
            return null;
        }
        try {
            if (v.contains(".")) {
                return Double.parseDouble(v);
            }
            return Long.parseLong(v);
        } catch (NumberFormatException e) {
            return v;
        }
    }

    private static String unquote(String s) {
        if (s.length() >= 2 && s.startsWith("\"") && s.endsWith("\"")) {
            s = s.substring(1, s.length() - 1);
        }
        return s.replace("\\\"", "\"").replace("\\\\", "\\").replace("\\n", "\n");
    }

    private static String str(Object o) {
        return o == null ? "" : String.valueOf(o);
    }

    private static String safe(String s) {
        return s == null ? "" : s;
    }

    @SuppressWarnings("unchecked")
    private static Map<String, Object> asMap(Object o) {
        return o instanceof Map ? (Map<String, Object>) o : new HashMap<>();
    }

    private static int intArg(Object o, int def) {
        if (o instanceof Number) {
            return ((Number) o).intValue();
        }
        try {
            return Integer.parseInt(String.valueOf(o));
        } catch (Exception e) {
            return def;
        }
    }

    private static double dblArg(Object o, double def) {
        if (o instanceof Number) {
            return ((Number) o).doubleValue();
        }
        try {
            return Double.parseDouble(String.valueOf(o));
        } catch (Exception e) {
            return def;
        }
    }

    private static boolean boolArg(Object o, boolean def) {
        if (o instanceof Boolean) {
            return (Boolean) o;
        }
        if (o == null) {
            return def;
        }
        return "true".equalsIgnoreCase(String.valueOf(o));
    }

    private static String safeFileName(String s) {
        return s.replaceAll("[^A-Za-z0-9_.-]", "_");
    }

    private static String nowISO() {
        return new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ssXXX").format(new Date());
    }
}
