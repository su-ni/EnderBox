#!/usr/bin/env python3
"""生成物品中文名表与图标包。

产物（两个文件，被 Go 用 go:embed 嵌进 exe）：
    internal/materials/materials.json   物品/状态效果/附魔的中文名（附英文兜底）
    internal/materials/textures.zip     物品与方块的贴图（item/ 与 block/ 两个目录）

为什么离线自带而不是联网查：
    面板是给家里那台机器用的，它读的是插件写在本地文件里的物品 id（minecraft:diamond_sword）。
    这种 id 拿到任何翻译服务上都查不出「钻石剑」—— 只有 Mojang 自己的语言文件里有。
    装的时候下好、打包进 exe，运行时零网络。

用法：
    python tools/matgen/build.py
    可选：--versions 1.20.1,1.21.8  指定要合并哪些版本的语言文件
"""
import argparse
import io
import json
import os
import re
import sys
import time
import urllib.request
import zipfile

UA = {"User-Agent": "ServerBox matgen/1.0"}
MANIFEST = "https://piston-meta.mojang.com/mc/game/version_manifest_v2.json"
RESOURCE_BASE = "https://resources.download.minecraft.net"

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))
OUT_DIR = os.path.join(ROOT, "internal", "materials")
CACHE = os.path.join(HERE, ".cache")


def fetch(url, dest=None, binary=True):
    """带缓存地取一个 URL。抓 Mojang 别太急，也别并发 —— 见 memory 里被封禁那次。"""
    if dest and os.path.exists(dest) and os.path.getsize(dest) > 0:
        with open(dest, "rb") as f:
            return f.read()
    req = urllib.request.Request(url, headers=UA)
    with urllib.request.urlopen(req, timeout=60) as r:
        data = r.read()
    if dest:
        os.makedirs(os.path.dirname(dest), exist_ok=True)
        with open(dest, "wb") as f:
            f.write(data)
    return data


def get_json(url, dest=None):
    return json.loads(fetch(url, dest).decode("utf-8"))


def lang_for(version_id, versions_index):
    """取某个版本的 zh_cn.json / en_us.json。

    ⚠ 1.20 之后的客户端 jar 里只有 en_us.json，其他语言必须单独从资源服务器下。
    只看客户端 jar 的人会拿到一份「只有英文」的表，然后以为 Mojang 没翻译。
    """
    entry = versions_index.get(version_id)
    if not entry:
        print(f"  ! 版本 {version_id} 不在清单里，跳过", file=sys.stderr)
        return {}, {}
    vj = get_json(entry["url"], os.path.join(CACHE, f"{version_id}.json"))
    idx = get_json(vj["assetIndex"]["url"], os.path.join(CACHE, f"{version_id}-assets.json"))
    out = {}
    for lang in ("zh_cn", "en_us"):
        key = f"minecraft/lang/{lang}.json"
        obj = idx["objects"].get(key)
        if not obj:
            print(f"  ! {version_id} 的资源索引里没有 {key}", file=sys.stderr)
            continue
        h = obj["hash"]
        url = f"{RESOURCE_BASE}/{h[:2]}/{h}"
        out[lang] = get_json(url, os.path.join(CACHE, f"{version_id}-{lang}.json"))
    return out.get("zh_cn", {}), out.get("en_us", {})


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--versions", default="1.20.1,latest",
                    help="要合并的 MC 版本，用逗号分隔；latest = 当前最新正式版")
    args = ap.parse_args()

    os.makedirs(CACHE, exist_ok=True)
    os.makedirs(OUT_DIR, exist_ok=True)

    print("拉版本清单 ...")
    manifest = get_json(MANIFEST, os.path.join(CACHE, "manifest.json"))
    index = {v["id"]: v for v in manifest["versions"]}

    wanted = []
    for v in args.versions.split(","):
        v = v.strip()
        if v == "latest":
            v = manifest["latest"]["release"]
        wanted.append(v)
    print(f"要合并的版本：{wanted}")

    # ── 语言表：旧版本在前，新版本补漏 ──────────────────────────────
    # 先铺 1.20.1（跟插件的 API 基线一致、跟贴图同一版），
    # 再用新版本把「后加的物品」补上。同一条目以先到的为准，避免新版改名把旧名冲掉。
    zh, en = {}, {}
    for v in wanted:
        print(f"  取 {v} 的语言文件 ...")
        z, e = lang_for(v, index)
        for k, val in z.items():
            zh.setdefault(k, val)
        for k, val in e.items():
            en.setdefault(k, val)
        time.sleep(0.4)
    print(f"语言条目：zh={len(zh)} en={len(en)}")

    # ── 贴图：每个版本都抽，先到的为准，后面的只补缺 ────────────────
    # ⚠ 只抽第一个版本是不够的：语言表合并了新版本，新物品（犰狳鳞片、陶罐碎片…）
    # 在 1.20.1 的客户端里根本没有贴图，覆盖率会掉到四成。
    # 只抽一层目录的 png（item/ 与 block/ 下的），item/xxx/ 子目录里是动画帧，别混进来。
    tex = {}
    pat = re.compile(r"^assets/minecraft/textures/(item|block)/([^/]+\.png)$")
    tex_versions = []
    for v in wanted:
        vj = get_json(index[v]["url"], os.path.join(CACHE, f"{v}.json"))
        print(f"  取 {v} 的客户端 jar（贴图在里面）...")
        jar_data = fetch(vj["downloads"]["client"]["url"],
                         os.path.join(CACHE, f"{v}-client.jar"))
        z = zipfile.ZipFile(io.BytesIO(jar_data))
        tex_versions.append(v)
        added = 0
        for n in z.namelist():
            m = pat.match(n)
            if not m:
                continue
            key = f"{m.group(1)}/{m.group(2)}"
            if key not in tex:
                tex[key] = z.read(n)
                added += 1
        print(f"    新增 {added} 张（累计 {len(tex)}）")

        # ⚠ en_us.json 不在资源索引里，只在客户端 jar 里 —— lang_for 一定会报「没有」，
        # 英文兜底得从这里补。缺了它，中文表没覆盖到的物品就只能显示 minecraft:xxx。
        if not en:
            try:
                en = json.loads(z.read("assets/minecraft/lang/en_us.json").decode("utf-8"))
                print(f"    顺带补到英文表 {len(en)} 条")
            except KeyError:
                print("    ! 这个客户端 jar 里也没有 en_us.json", file=sys.stderr)
        time.sleep(0.4)

    print(f"贴图：{len(tex)} 张，共 {round(sum(len(b) for b in tex.values())/1048576, 2)} MB")

    # ── 组装物品表 ──────────────────────────────────────────────
    def pick(prefix):
        """把 item.minecraft.X / block.minecraft.X 合成 minecraft:X 一张表。"""
        out = {}
        for pre in ("item", "block") if prefix == "item" else (prefix,):
            head = f"{pre}.minecraft."
            for k in list(zh) + list(en):
                if not k.startswith(head):
                    continue
                mid = "minecraft:" + k[len(head):]
                if mid in out:
                    continue
                icon = f"item/{k[len(head):]}.png"
                if icon not in tex:
                    icon = f"block/{k[len(head):]}.png"
                out[mid] = {
                    "zh": zh.get(k, ""),
                    "en": en.get(k, ""),
                    "icon": icon if icon in tex else "",
                }
        return out

    items = pick("item")
    effects = pick("effect")
    enchants = pick("enchantment")
    print(f"物品 {len(items)} / 状态效果 {len(effects)} / 附魔 {len(enchants)}")

    doc = {
        "generatedAt": time.strftime("%Y-%m-%dT%H:%M:%S%z"),
        "langVersions": wanted,
        "textureVersions": tex_versions,
        "items": items,
        "effects": effects,
        "enchants": enchants,
    }
    mp = os.path.join(OUT_DIR, "materials.json")
    with open(mp, "w", encoding="utf-8") as f:
        json.dump(doc, f, ensure_ascii=False, separators=(",", ":"))
    print(f"写出 {mp}（{round(os.path.getsize(mp)/1024)} KB）")

    zp = os.path.join(OUT_DIR, "textures.zip")
    with zipfile.ZipFile(zp, "w", zipfile.ZIP_DEFLATED) as out:
        for name, data in sorted(tex.items()):
            out.writestr(name, data)
    print(f"写出 {zp}（{round(os.path.getsize(zp)/1024)} KB）")


if __name__ == "__main__":
    main()
