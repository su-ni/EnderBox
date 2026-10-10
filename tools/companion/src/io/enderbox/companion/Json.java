package io.enderbox.companion;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;

/**
 * 一个够用的 JSON 序列化器。
 *
 * 为什么自己写而不用 Gson：Gson 是服务端 shade 进来的第三方库，
 * 它在不同服务端上的包名/可见性并不统一（有的重定位过，有的干脆没带）。
 * 一个「只想把数据写成文件」的插件，不该把自己的命脉系在别人的 jar 上。
 * 这里的类型只有 Map / List / String / Number / Boolean —— 够用，也只支持这些。
 */
final class Json {

    private Json() {
    }

    /** 有序 map，字段顺序就是写出来的顺序 */
    static Map<String, Object> obj() {
        return new LinkedHashMap<>();
    }

    static List<Object> arr() {
        return new ArrayList<>();
    }

    static String write(Object o) {
        StringBuilder sb = new StringBuilder(256);
        w(sb, o);
        return sb.toString();
    }

    private static void w(StringBuilder sb, Object o) {
        if (o == null) {
            sb.append("null");
            return;
        }
        if (o instanceof Map) {
            sb.append('{');
            boolean first = true;
            for (Map.Entry<?, ?> e : ((Map<?, ?>) o).entrySet()) {
                if (!first) {
                    sb.append(',');
                }
                first = false;
                str(sb, String.valueOf(e.getKey()));
                sb.append(':');
                w(sb, e.getValue());
            }
            sb.append('}');
            return;
        }
        if (o instanceof List) {
            sb.append('[');
            boolean first = true;
            for (Object v : (List<?>) o) {
                if (!first) {
                    sb.append(',');
                }
                first = false;
                w(sb, v);
            }
            sb.append(']');
            return;
        }
        if (o instanceof String) {
            str(sb, (String) o);
            return;
        }
        if (o instanceof Boolean) {
            sb.append(((Boolean) o) ? "true" : "false");
            return;
        }
        if (o instanceof Number) {
            sb.append(fmt(((Number) o).doubleValue()));
            return;
        }
        // 未知类型（比如 UUID）一律当字符串写，至少 JSON 还是合法的
        str(sb, String.valueOf(o));
    }

    private static void str(StringBuilder sb, String s) {
        sb.append('"');
        for (int i = 0; i < s.length(); i++) {
            char c = s.charAt(i);
            switch (c) {
                case '"':
                    sb.append("\\\"");
                    break;
                case '\\':
                    sb.append("\\\\");
                    break;
                case '\n':
                    sb.append("\\n");
                    break;
                case '\r':
                    sb.append("\\r");
                    break;
                case '\t':
                    sb.append("\\t");
                    break;
                default:
                    // 控制字符必须转义，否则写出来的 JSON 不合法
                    if (c < 0x20) {
                        sb.append(String.format("\\u%04x", (int) c));
                    } else {
                        sb.append(c);
                    }
            }
        }
        sb.append('"');
    }

    /**
     * 浮点收敛到两位小数再放回 Number。
     *
     * ⚠ 返回的是 Number 不是 String：曾经这里返回字符串，结果坐标被写成
     * "1.5"（带引号），面板那边 float64 直接解析失败。整数就返回 Long，
     * 这样 20 血写出来是 20 而不是 20.0。
     */
    static Number num(double d) {
        if (Double.isNaN(d) || Double.isInfinite(d)) {
            return 0L;
        }
        double r = Math.rint(d * 100.0) / 100.0;
        if (r == Math.rint(r) && Math.abs(r) < 1e15) {
            return (long) r;
        }
        return r;
    }

    private static String fmt(double d) {
        if (Double.isNaN(d) || Double.isInfinite(d)) {
            return "0";
        }
        double r = Math.rint(d * 100.0) / 100.0;
        if (r == Math.rint(r) && Math.abs(r) < 1e15) {
            return String.valueOf((long) r);
        }
        String s = String.format(Locale.ROOT, "%.2f", r);
        while (s.endsWith("0")) {
            s = s.substring(0, s.length() - 1);
        }
        if (s.endsWith(".")) {
            s = s.substring(0, s.length() - 1);
        }
        return s;
    }
}
