#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Assemble the oneNat server release tarball (dist/oneNat-r<ver>.tar.gz).

版本号的**单一来源**是 src/ngrok/server/dashboard/version.go 的 DefaultRelease
常量 (改那一处即可, 发行包名 / 文档 / systemd 都跟着变)。这里从源码里读出来,
不再在脚本里另抄一份 —— 否则发版时最容易出现「包名是 r2026.09.15、后台显示
r2026.10.16」这种对不上的情况。

可用环境变量 ONENAT_RELEASE 覆盖 (发测试版/灰度版时用, 例: 2026.11.06-rc1)。
"""
import io
import os
import re
import sys
import tarfile
import time

VERSION_GO = os.path.join("src", "ngrok", "server", "dashboard", "version.go")
VERSION_RE = re.compile(r'^\s*DefaultRelease\s*=\s*"([^"]+)"', re.M)
# 文档里的版本占位符: 打包时替换成 r<DefaultRelease>
# 用占位符而不是把版本号写死在文档里, 是为了让「版本号单一来源」这件事
# **结构上**成立 —— 文档里不可能再出现一个与源码不一致的旧版本号。
README_PLACEHOLDER = "{{RELEASE}}"


def read_release():
    """从 version.go 读发行标识; ONENAT_RELEASE 环境变量优先。"""
    env = os.environ.get("ONENAT_RELEASE", "").strip()
    if env:
        return env
    try:
        with open(VERSION_GO, "r", encoding="utf-8") as fh:
            m = VERSION_RE.search(fh.read())
    except OSError as e:
        sys.exit("读取 %s 失败: %s" % (VERSION_GO, e))
    if not m:
        sys.exit("在 %s 里找不到 DefaultRelease 常量" % VERSION_GO)
    return m.group(1)


def render_readme(rel):
    """渲染 packaging/README.md: 把 {{RELEASE}} 占位符换成当前发行标识。"""
    try:
        with open("packaging/README.md", "r", encoding="utf-8") as fh:
            text = fh.read()
    except OSError as e:
        sys.exit("读取 packaging/README.md 失败: %s" % e)
    if README_PLACEHOLDER not in text:
        sys.exit(
            "packaging/README.md 里必须包含 %s 占位符 (发行标识的单一来源是 "
            "version.go 的 DefaultRelease, 文档不应再写死版本号)" % README_PLACEHOLDER
        )
    return text.replace(README_PLACEHOLDER, "r" + rel)


def main():
    rel = read_release()
    REL = "oneNat-r%s" % rel
    OUT = os.path.join("dist", "%s.tar.gz" % REL)

    # (源, 包内路径, 可执行位); README 单独处理 —— 内容要现渲染
    ENTRIES = [
        ("start-onenat.sh",            "%s/start-onenat.sh" % REL,            True),
        ("packaging/start-onenat.bat", "%s/start-onenat.bat" % REL,           True),
        ("stop-onenat.sh",             "%s/stop-onenat.sh" % REL,             True),
        ("packaging/onenat.service",   "%s/onenat.service" % REL,             False),
        ("dist/build/ngrokd-linux-amd64",  "%s/bin/ngrokd-linux-amd64" % REL,  True),
        ("dist/build/ngrokd-linux-arm64",  "%s/bin/ngrokd-linux-arm64" % REL,  True),
        ("dist/build/ngrokd-darwin-arm64", "%s/bin/ngrokd-darwin-arm64" % REL, True),
        ("dist/build/ngrokd-windows-amd64.exe", "%s/bin/ngrokd-windows-amd64.exe" % REL, True),
    ]

    for f in sorted(os.listdir("dl")):
        ENTRIES.append(("dl/%s" % f, "%s/dl/%s" % (REL, f), True))

    os.makedirs("dist", exist_ok=True)
    with tarfile.open(OUT, "w:gz") as tar:
        # README 先渲染版本占位符, 再打进包
        readme_bytes = render_readme(rel).encode("utf-8")
        ti = tarfile.TarInfo("%s/README.md" % REL)
        ti.uid = ti.gid = 0
        ti.uname = ti.gname = "root"
        ti.mode = 0o644
        ti.size = len(readme_bytes)
        ti.mtime = int(os.environ.get("SOURCE_DATE_EPOCH") or 0) or int(time.time())
        tar.addfile(ti, io.BytesIO(readme_bytes))

        for src, arc, executable in ENTRIES:
            ti = tar.gettarinfo(src, arcname=arc)
            ti.uid = ti.gid = 0
            ti.uname = ti.gname = "root"
            ti.mode = 0o755 if executable else 0o644
            with open(src, "rb") as fh:
                if arc.endswith(".bat"):
                    # Windows cmd 对换行敏感, 统一转 CRLF
                    data = fh.read().replace(b"\r\n", b"\n").replace(b"\n", b"\r\n")
                    ti.size = len(data)
                    tar.addfile(ti, io.BytesIO(data))
                else:
                    tar.addfile(ti, fh)
    print("打包完成:", OUT, "%.1f MB" % (os.path.getsize(OUT) / 1024 / 1024))


if __name__ == "__main__":
    sys.exit(main())
