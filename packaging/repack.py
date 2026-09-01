#!/usr/bin/env python3
"""Assemble the ngrokd server release tarball (dist/ngrokd-r<ver>.tar.gz)."""
import io
import os
import sys
import tarfile

REL = "oneNat-r2026.09.01"
OUT = os.path.join("dist", f"{REL}.tar.gz")

ENTRIES = [
    ("start-onenat.sh",            f"{REL}/start-onenat.sh",            True),
    ("packaging/start-onenat.bat", f"{REL}/start-onenat.bat",           True),
    ("stop-onenat.sh",             f"{REL}/stop-onenat.sh",             True),
    ("packaging/onenat.service",   f"{REL}/onenat.service",             False),
    ("packaging/README.md",        f"{REL}/README.md",                  False),
    ("dist/build/ngrokd-linux-amd64",  f"{REL}/bin/ngrokd-linux-amd64",  True),
    ("dist/build/ngrokd-linux-arm64",  f"{REL}/bin/ngrokd-linux-arm64",  True),
    ("dist/build/ngrokd-darwin-arm64", f"{REL}/bin/ngrokd-darwin-arm64", True),
    ("dist/build/ngrokd-windows-amd64.exe", f"{REL}/bin/ngrokd-windows-amd64.exe", True),
]


def main():
    for f in sorted(os.listdir("dl")):
        ENTRIES.append((f"dl/{f}", f"{REL}/dl/{f}", True))

    os.makedirs("dist", exist_ok=True)
    with tarfile.open(OUT, "w:gz") as tar:
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
    print("打包完成:", OUT, f"{os.path.getsize(OUT)/1024/1024:.1f} MB")


if __name__ == "__main__":
    sys.exit(main())
