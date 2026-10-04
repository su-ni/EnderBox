import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.time.LocalTime;
import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Set;

/**
 * 假服务端：只用于开发和 UI 验证。
 *
 * 行为刻意对齐真实 Minecraft 服务端：
 *  - 日志行格式 [HH:mm:ss] [Server thread/INFO]: ...
 *  - 从 stdin 读命令，stop 优雅退出
 *  - 占用真实内存，让监控指标有数据
 *  - 模拟玩家加入，验证在线玩家统计
 *
 * 用它就不必每次为了看界面去下一个几百 MB 的真服务端。
 */
public class FakeServer {

    static final Set<String> players = new LinkedHashSet<>();

    public static void main(String[] args) throws Exception {
        // 占住一块堆，让内存曲线有变化
        List<byte[]> heap = new ArrayList<>();
        for (int i = 0; i < 24; i++) {
            heap.add(new byte[1024 * 1024]);
        }

        log("Starting minecraft server version 1.20.4");
        log("Loading properties");
        log("Default game type: SURVIVAL");
        log("Preparing level \"world\"");
        Thread.sleep(600);
        log("Done (3.412s)! For help, type \"help\"");

        BufferedReader in = new BufferedReader(new InputStreamReader(System.in));
        long start = System.currentTimeMillis();
        int tick = 0;

        while (true) {
            while (in.ready()) {
                String cmd = in.readLine();
                if (cmd == null) {
                    break;
                }
                handle(cmd.trim());
            }
            Thread.sleep(1000);
            tick++;

            if (tick == 8) {
                players.add("Steve");
                log("Steve[/127.0.0.1:51423] logged in with entity id 41");
                log("Steve joined the game");
            }
            if (tick == 22) {
                players.add("Alex");
                log("Alex[/127.0.0.1:51987] logged in with entity id 62");
                log("Alex joined the game");
            }
            if (tick % 25 == 0) {
                long up = (System.currentTimeMillis() - start) / 1000;
                long heapMb = (Runtime.getRuntime().totalMemory() - Runtime.getRuntime().freeMemory()) / 1048576;
                log("Tick " + tick + " | uptime " + up + "s | heap " + heapMb + "MB | players " + players.size());
            }
            if (tick % 40 == 0) {
                log("Can't keep up! Is the server overloaded? Running 2149ms or 42 ticks behind");
            }
        }
    }

    static void handle(String cmd) throws Exception {
        if (cmd.equals("stop")) {
            log("Stopping the server");
            log("Saving worlds");
            Thread.sleep(400);
            log("Saving chunks for level 'ServerLevel[world]'/minecraft:overworld");
            log("ThreadedAnvilChunkStorage (world): All chunks are saved");
            log("Server stopped");
            System.exit(0);
            return;
        }
        if (cmd.startsWith("list")) {
            log("There are " + players.size() + " of a max of 20 players online: " + String.join(", ", players));
            return;
        }
        if (cmd.startsWith("say ")) {
            log("[Server] " + cmd.substring(4));
            return;
        }
        if (cmd.equals("tps")) {
            log("TPS from last 1m, 5m, 15m: 19.98, 20.0, 20.0");
            return;
        }
        if (cmd.isEmpty()) {
            return;
        }
        log("Unknown or incomplete command, see below for error");
        log(cmd + "<--[HERE]");
    }

    static void log(String msg) {
        LocalTime t = LocalTime.now();
        System.out.printf("[%02d:%02d:%02d] [Server thread/INFO]: %s%n",
                t.getHour(), t.getMinute(), t.getSecond(), msg);
        System.out.flush();
    }
}
