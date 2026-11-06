#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""oneNat 客户端 GitHub Release 资产产出脚本 (纯标准库, 跨 macOS / Linux)。

为什么需要这个脚本
------------------
1) 客户端资产有**两套命名约定**, 且各自都有硬约束, 不能互相替代:
   - ``onenat-client_<os>_<arch>`` (下划线): 服务端后台 ``/dl/`` 端点与一键安装
     脚本的硬要求。``web.go`` 的 ``dlClientBinaries()`` 只收 ``onenat-client_``
     前缀的文件, 安装脚本按 ``BIN_NAME="onenat-client_${os}_${arch}"`` 下载。
     名字对不上 ⇒ 烘进安装脚本的 MD5 表为空 ⇒ 每次重装都重新下载。
   - ``onenat-client-<os>-<arch>.tar.gz`` / ``.zip`` (连字符): GitHub Release 上
     历史 release (r2026.09.01 / r2026.09.07) 的资产命名, 人工下载与外部消费方
     (例如 dsh-onenat 插件) 都按这个约定取文件。
   ⇒ 本脚本一次产出**两种形态**, 下游不用再猜该用下划线还是连字符。

2) 校验统一为 sha256: 产出 ``checksums.txt``, 格式 ``<hex>  <file>`` (两个空格),
   与已发布 release 完全一致, 同时兼容 ``sha256sum -c`` / ``shasum -a 256 -c``。

3) 幂等 / 可复现: 归档成员的 mtime / uid / gid / 权限全部固定, gzip 头的 mtime
   置 0 ⇒ 同样的输入重复执行, 产出**逐字节相同**的资产, 便于重跑与事后比对。

用法
----
    # 正常产出 (写入 dist/release/<tag>/)
    python3 packaging/release_client.py --tag r2026.11.06

    # 只看会产出什么, 不落盘 (归档在临时目录里真算一遍, 所以输出是真实的)
    python3 packaging/release_client.py --tag r2026.11.06 --dry-run

    # 自检: 造临时假二进制, 跑完整条流水线并校验结果 (不需要 Go, 不需要 dl/)
    python3 packaging/release_client.py --selftest
"""

import argparse
import gzip
import hashlib
import json
import os
import re
import shutil
import sys
import tarfile
import tempfile
import time
import zipfile

# ---------------------------------------------------------------------------
# 常量
# ---------------------------------------------------------------------------

# 7 个交叉编译平台, 与 Makefile 的 release-clients 目标一一对应。
# 顺序即 assets 清单里的展示顺序 (按平台分组, 便于人读)。
PLATFORMS = (
    ("linux", "amd64"),
    ("linux", "arm64"),
    ("linux", "arm"),
    ("darwin", "arm64"),
    ("darwin", "amd64"),
    ("windows", "amd64"),
    ("windows", "arm64"),
)

# 归档内的成员名。历史 release 的 tarball 里装的是改名前的 `ngrok`,
# 现在与 dl/ 的下发名对齐 —— 解压出来即 `onenat-client`, Windows 带 .exe。
ARCHIVE_MEMBER = "onenat-client"

CHECKSUM_FILE = "checksums.txt"

# 默认时间戳: 1980-01-01T00:00:00Z (zip 格式能表示的最早时间)。
# 固定时间戳 ⇒ 归档可复现 (同样的输入 → 同样的字节)。
DEFAULT_MTIME = 315532800

TAG_RE = re.compile(r"^r[0-9]{4}\.[0-9]{2}\.[0-9]{2}(-[0-9A-Za-z.]+)?$")
CHECKSUM_LINE_RE = re.compile(r"^([0-9a-f]{64})  (\S+)$")

# 自检用的假二进制文件头。用来识别"这些产物只是测试夹具, 不是真客户端",
# 防止有人拿 --make-fixture-dir 造的假文件去发真 release。
FIXTURE_MAGIC = b"FAKE-ONENAT-CLIENT"


# ---------------------------------------------------------------------------
# 小工具
# ---------------------------------------------------------------------------

def eprint(*args):
    """错误信息统一走 stderr, 方便把 stdout 当机器可读输出。"""
    print(*args, file=sys.stderr)


def binary_name(os_name, arch):
    """dl/ 里的产物名 (也是裸二进制 Release 资产名)。"""
    return "onenat-client_%s_%s%s" % (os_name, arch, ".exe" if os_name == "windows" else "")


def archive_name(os_name, arch):
    """归档资产名 (沿用历史 release 的连字符命名; Windows 用 zip)。"""
    if os_name == "windows":
        return "onenat-client-%s-%s.zip" % (os_name, arch)
    return "onenat-client-%s-%s.tar.gz" % (os_name, arch)


def sha256_file(path):
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def resolve_mtime(explicit=None):
    """确定归档内统一使用的 mtime。

    优先级: --mtime 参数 > SOURCE_DATE_EPOCH 环境变量 > 固定常量。
    固定 mtime 是归档可复现的前提。
    """
    if explicit is not None:
        return int(explicit)
    raw = os.environ.get("SOURCE_DATE_EPOCH", "").strip()
    if raw.isdigit():
        return int(raw)
    return DEFAULT_MTIME


def zip_datetime(ts):
    """epoch 秒 → zip 头里的 (year, month, day, hour, min, sec)。"""
    t = time.gmtime(max(int(ts), DEFAULT_MTIME))
    return (t.tm_year, t.tm_mon, t.tm_mday, t.tm_hour, t.tm_min, t.tm_sec)


# ---------------------------------------------------------------------------
# 归档构建 (确定性输出)
# ---------------------------------------------------------------------------

def build_targz(src_path, member, out_path, mtime):
    """构建 .tar.gz: 单文件成员, 权限 0755, mtime/uid/gid 固定。"""
    with open(out_path, "wb") as raw:
        # filename="" + mtime=0 ⇒ gzip 头里不写文件名、不写时间 ⇒ 字节可复现
        gz = gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0)
        try:
            with tarfile.open(fileobj=gz, mode="w", format=tarfile.GNU_FORMAT) as tar:
                ti = tar.gettarinfo(src_path, arcname=member)
                ti.uid = 0
                ti.gid = 0
                ti.uname = ""
                ti.gname = ""
                ti.mtime = int(mtime)
                ti.mode = 0o755
                ti.type = tarfile.REGTYPE
                with open(src_path, "rb") as fh:
                    tar.addfile(ti, fh)
        finally:
            gz.close()


def build_zip(src_path, member, out_path, mtime):
    """构建 .zip: 单文件成员, 权限 0755, 时间戳固定。"""
    with zipfile.ZipFile(out_path, "w", compression=zipfile.ZIP_DEFLATED) as zf:
        zi = zipfile.ZipInfo(member, date_time=zip_datetime(mtime))
        zi.compress_type = zipfile.ZIP_DEFLATED
        zi.create_system = 3  # unix
        zi.external_attr = (0o755 << 16)  # unix 权限 0755
        with open(src_path, "rb") as fh:
            zf.writestr(zi, fh.read())


def archive_member_name(os_name):
    return ARCHIVE_MEMBER + (".exe" if os_name == "windows" else "")


# ---------------------------------------------------------------------------
# 资产清单
# ---------------------------------------------------------------------------

def collect_plan(dl_dir, allow_missing=False):
    """扫描 dl/ 目录, 得出「每个平台该产出哪些资产」的清单。

    返回 (plan, missing, fixtures):
      plan     —— [(os, arch, 源文件路径, 裸二进制名, 归档名)]
      missing  —— 缺失的裸二进制名 (用于报错提示)
      fixtures —— 内容是自检假二进制的源文件 (防止误发布)
    """
    plan = []
    missing = []
    fixtures = []
    for os_name, arch in PLATFORMS:
        bn = binary_name(os_name, arch)
        src = os.path.join(dl_dir, bn)
        if not os.path.isfile(src):
            missing.append(bn)
            continue
        if is_fixture(src):
            fixtures.append(bn)
        plan.append((os_name, arch, src, bn, archive_name(os_name, arch)))
    if missing and not allow_missing:
        eprint("[错误] dl/ 目录缺少以下平台产物: %s" % ", ".join(missing))
        eprint("       请先执行 `make release-clients` (交叉编译 7 个平台, -tags release)。")
        eprint("       确实只想发部分平台时加 --allow-missing。")
        sys.exit(2)
    return plan, missing, fixtures


def is_fixture(path):
    """判断文件是不是自检用的假二进制 (看文件头 magic)。"""
    try:
        with open(path, "rb") as fh:
            return fh.read(len(FIXTURE_MAGIC)) == FIXTURE_MAGIC
    except OSError:
        return False


def make_fixture_dir(target):
    """造一份假的 dl/ 目录 (只用于在没有 Go 的机器上验证脚本逻辑)。"""
    os.makedirs(target, exist_ok=True)
    for os_name, arch in PLATFORMS:
        bn = binary_name(os_name, arch)
        with open(os.path.join(target, bn), "wb") as fh:
            fh.write(FIXTURE_MAGIC + b"-%s-%s\n" % (os_name.encode(), arch.encode()))
    return target


def build_assets(plan, staging, mtime, want_bare=True, want_archive=True):
    """把 plan 里的产物真正写进 staging 目录, 返回资产记录列表。"""
    records = []
    for os_name, arch, src, bn, an in plan:
        if want_bare:
            dst = os.path.join(staging, bn)
            shutil.copyfile(src, dst)
            os.chmod(dst, 0o755)
            records.append({
                "name": bn,
                "path": dst,
                "kind": "binary",
                "platform": "%s/%s" % (os_name, arch),
            })
        if want_archive:
            dst = os.path.join(staging, an)
            member = archive_member_name(os_name)
            if an.endswith(".zip"):
                build_zip(src, member, dst, mtime)
            else:
                build_targz(src, member, dst, mtime)
            records.append({
                "name": an,
                "path": dst,
                "kind": "archive",
                "platform": "%s/%s" % (os_name, arch),
            })
    return records


def write_checksums(records, staging):
    """写 checksums.txt: sha256, 格式 `<hex>  <file>` (两个空格, 与历史 release 一致)。

    排序用文件名的字节序, 保证同样输入产出同样的文件。
    """
    lines = []
    for rec in sorted(records, key=lambda r: r["name"]):
        lines.append("%s  %s" % (sha256_file(rec["path"]), rec["name"]))
    path = os.path.join(staging, CHECKSUM_FILE)
    with open(path, "w", encoding="utf-8", newline="\n") as fh:
        fh.write("\n".join(lines) + "\n")
    return path


def manifest(records, staging, tag):
    """给机器读的资产清单 (--json 输出用)。"""
    items = []
    for rec in sorted(records, key=lambda r: r["name"]):
        items.append({
            "name": rec["name"],
            "kind": rec["kind"],
            "platform": rec["platform"],
            "size": os.path.getsize(rec["path"]),
            "sha256": sha256_file(rec["path"]),
        })
    return {"tag": tag, "assets": items}


def print_report(records, staging, tag, out_dir, dry_run):
    lines = []
    lines.append("发行标签 : %s" % tag)
    lines.append("输出目录 : %s%s" % (out_dir, "  (dry-run, 不落盘)" if dry_run else ""))
    lines.append("")
    lines.append("%-38s %-9s %10s  %s" % ("资产", "形态", "大小", "sha256"))
    lines.append("-" * 108)
    for rec in sorted(records, key=lambda r: (r["platform"], r["kind"])):
        lines.append("%-38s %-9s %10s  %s" % (
            rec["name"],
            rec["kind"],
            "%d B" % os.path.getsize(rec["path"]),
            sha256_file(rec["path"]),
        ))
    lines.append("-" * 108)
    lines.append("共 %d 个资产 + %s (sha256 校验清单)" % (len(records), CHECKSUM_FILE))
    print("\n".join(lines))


# ---------------------------------------------------------------------------
# 自检 (不需要 Go / 不需要 dl/, 可在任何装了 python3 的机器上跑)
# ---------------------------------------------------------------------------

def selftest():
    """造临时假二进制 → 跑完整条流水线 → 校验结果。"""
    tmp = tempfile.mkdtemp(prefix="onenat-release-selftest-")
    ok = True
    try:
        dl_dir = os.path.join(tmp, "dl")
        os.makedirs(dl_dir)
        for os_name, arch in PLATFORMS:
            bn = binary_name(os_name, arch)
            with open(os.path.join(dl_dir, bn), "wb") as fh:
                fh.write(b"FAKE-ONENAT-CLIENT-%s-%s\n" % (os_name.encode(), arch.encode()))

        mtime = resolve_mtime()
        plan, missing, _fixtures = collect_plan(dl_dir)

        staging = os.path.join(tmp, "staging")
        os.makedirs(staging)
        records = build_assets(plan, staging, mtime)
        write_checksums(records, staging)

        # 1) 资产数量: 7 裸二进制 + 7 归档
        names = sorted(r["name"] for r in records)
        if len(records) != 14:
            eprint("[FAIL] 资产数应为 14 (7 裸二进制 + 7 归档), 实际 %d" % len(records))
            ok = False

        # 2) 归档内只有一个成员, 且名字是 onenat-client[.exe]
        for os_name, arch, _src, _bn, an in plan:
            ap = os.path.join(staging, an)
            want = archive_member_name(os_name)
            if an.endswith(".zip"):
                with zipfile.ZipFile(ap) as zf:
                    got = zf.namelist()
            else:
                with tarfile.open(ap, "r:gz") as tf:
                    got = tf.getnames()
            if got != [want]:
                eprint("[FAIL] %s 内成员应为 [%s], 实际 %s" % (an, want, got))
                ok = False

        # 3) checksums.txt 格式与数值
        cpath = os.path.join(staging, CHECKSUM_FILE)
        with open(cpath, "r", encoding="utf-8") as fh:
            clines = [ln.rstrip("\n") for ln in fh if ln.strip()]
        if len(clines) != len(records):
            eprint("[FAIL] checksums.txt 行数应为 %d, 实际 %d" % (len(records), len(clines)))
            ok = False
        for ln in clines:
            m = CHECKSUM_LINE_RE.match(ln)
            if not m:
                eprint("[FAIL] checksums.txt 行格式不符 (应为 '<64位hex>  <file>'): %r" % ln)
                ok = False
                continue
            digest, fname = m.group(1), m.group(2)
            fp = os.path.join(staging, fname)
            if not os.path.isfile(fp):
                eprint("[FAIL] checksums.txt 指向不存在的文件: %s" % fname)
                ok = False
                continue
            if sha256_file(fp) != digest:
                eprint("[FAIL] %s 的 sha256 与 checksums.txt 不符" % fname)
                ok = False

        # 4) 幂等: 再构建一次, 逐字节相同
        staging2 = os.path.join(tmp, "staging2")
        os.makedirs(staging2)
        records2 = build_assets(plan, staging2, mtime)
        write_checksums(records2, staging2)
        for rec in records:
            p2 = os.path.join(staging2, rec["name"])
            with open(rec["path"], "rb") as a, open(p2, "rb") as b:
                if a.read() != b.read():
                    eprint("[FAIL] %s 两次构建结果不一致 (不可复现)" % rec["name"])
                    ok = False
    finally:
        shutil.rmtree(tmp, ignore_errors=True)

    if ok:
        print("[OK] 自检通过: 7 平台资产、归档单成员、sha256 清单格式、可复现性 全部正确")
        return 0
    eprint("[FAIL] 自检未通过")
    return 1


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def parse_args(argv):
    ap = argparse.ArgumentParser(
        description="产出 oneNat 客户端 GitHub Release 资产 (裸二进制 + 归档 + sha256 清单)",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "示例:\n"
            "  python3 packaging/release_client.py --tag r2026.11.06\n"
            "  python3 packaging/release_client.py --tag r2026.11.06 --dry-run\n"
            "  python3 packaging/release_client.py --selftest\n"
        ),
    )
    ap.add_argument("--tag", help="发行标签, 形如 r2026.11.06 (发到 GitHub Release 用)")
    ap.add_argument("--dl-dir", default="dl", help="交叉编译产物目录 (默认: dl)")
    ap.add_argument("--out-root", default=os.path.join("dist", "release"),
                    help="输出根目录, 资产落在 <out-root>/<tag>/ (默认: dist/release)")
    ap.add_argument("--dry-run", action="store_true",
                    help="只在临时目录里真算一遍并打印清单, 不写 dist/ (不产生任何副作用)")
    ap.add_argument("--allow-missing", action="store_true",
                    help="允许缺少部分平台产物 (默认缺一个就报错, 避免发残缺 release)")
    ap.add_argument("--no-archive", action="store_true", help="不产出 .tar.gz / .zip 归档")
    ap.add_argument("--no-bare", action="store_true", help="不产出裸二进制")
    ap.add_argument("--mtime", type=int, default=None,
                    help="归档内固定时间戳 (默认取 SOURCE_DATE_EPOCH, 否则 1980-01-01)")
    ap.add_argument("--json", action="store_true", help="额外输出机器可读的资产清单 (JSON)")
    ap.add_argument("--selftest", action="store_true",
                    help="自检: 造临时假二进制跑完整流水线并校验 (不需要 dl/ 与 Go)")
    ap.add_argument("--make-fixture-dir", metavar="DIR",
                    help="只造一份假的 dl/ 目录到 DIR 就退出 (用于在没有 Go 的机器上验证脚本)")
    ap.add_argument("--allow-fixture", action="store_true",
                    help="允许用自检假二进制产出资产 (默认拒绝, 防止误发测试夹具)")
    return ap.parse_args(argv)


def main(argv=None):
    args = parse_args(argv)

    if args.make_fixture_dir:
        target = make_fixture_dir(os.path.abspath(args.make_fixture_dir))
        print("已生成假产物目录 (仅供脚本自测, 不可用于发版): %s" % target)
        return 0

    if args.selftest:
        return selftest()

    if not args.tag:
        eprint("[错误] 缺少 --tag (例: --tag r2026.11.06); 或用 --selftest 跑自检。")
        return 2
    if not TAG_RE.match(args.tag):
        eprint("[错误] 发行标签格式应为 rYYYY.MM.DD (可选 -后缀), 实际: %s" % args.tag)
        return 2

    dl_dir = os.path.abspath(args.dl_dir)
    if not os.path.isdir(dl_dir):
        msg = "找不到客户端产物目录: %s" % dl_dir
        if args.dry_run:
            # dry-run 是「只报告」模式: 输入缺失只警告, 便于在没有编译产物的机器上
            # 也能验证脚本本身是通的 (真正的发版请用非 dry-run, 那里缺输入会直接失败)。
            eprint("[警告] %s" % msg)
            eprint("       本次 --dry-run 没有可产出的资产; 请先执行 `make release-clients`。")
            eprint("       想在没有 Go 的机器上验证脚本本身, 用 --selftest, 或:")
            eprint("         python3 packaging/release_client.py --make-fixture-dir /tmp/fake_dl")
            eprint("         python3 packaging/release_client.py --tag %s --dl-dir /tmp/fake_dl --dry-run" % args.tag)
            return 0
        eprint("[错误] %s" % msg)
        eprint("       请先执行 `make release-clients` (交叉编译 7 个平台, -tags release)。")
        return 1

    plan, missing, fixtures = collect_plan(dl_dir, allow_missing=args.allow_missing)
    if not plan:
        eprint("[错误] %s 下没有任何可用的客户端产物。" % dl_dir)
        return 1

    if fixtures:
        # 自检假二进制绝不能发到 GitHub Release 上 —— 那是给用户下载执行的程序。
        if args.dry_run:
            # dry-run 不落盘, 没有危害, 只提示清楚即可 (否则没法用假文件做演示)
            eprint("[警告] 输入是自检用的假二进制 (%d 个: %s); dry-run 只是演示, 产物不可发布。"
                   % (len(fixtures), ", ".join(fixtures)))
        elif not args.allow_fixture:
            eprint("[错误] 以下产物看起来是**自检用的假二进制** (文件头 %s): %s"
                   % (FIXTURE_MAGIC.decode(), ", ".join(fixtures)))
            eprint("       拒绝用测试夹具产出 release 资产 (真实发版请先跑 `make release-clients`)。")
            eprint("       确实要用, 请显式加 --allow-fixture。")
            return 3
        else:
            eprint("[警告] 使用的是自检假二进制, 产出物**不可用于真实发布**!")

    mtime = resolve_mtime(args.mtime)
    out_dir = os.path.join(os.path.abspath(args.out_root), args.tag)

    # 先在临时目录里构建, 再整体搬到目标目录:
    #   - dry-run 天然不落盘;
    #   - 正式产出时目标目录是"整体替换", 不会残留上一次跑剩下的旧资产。
    staging = tempfile.mkdtemp(prefix="onenat-release-")
    try:
        records = build_assets(plan, staging, mtime,
                               want_bare=not args.no_bare,
                               want_archive=not args.no_archive)
        write_checksums(records, staging)

        if args.dry_run:
            print_report(records, staging, args.tag, out_dir, dry_run=True)
        else:
            os.makedirs(os.path.dirname(out_dir), exist_ok=True)
            if os.path.isdir(out_dir):
                shutil.rmtree(out_dir)
            # tempfile.mkdtemp 建的是 0700, 产出目录给其他人看/拷贝时会很别扭, 改回 0755
            os.chmod(staging, 0o755)
            shutil.move(staging, out_dir)
            staging = None
            # 资产搬过家了, 记录里的路径要跟着更新 (否则后面算 sha256 会找不到文件)
            for rec in records:
                rec["path"] = os.path.join(out_dir, rec["name"])
            print_report(records, out_dir, args.tag, out_dir, dry_run=False)
            print("")
            print("下一步: bash packaging/publish_release.sh --tag %s" % args.tag)

        if args.json:
            print(json.dumps(manifest(records, staging or out_dir, args.tag),
                             ensure_ascii=False, indent=2))
    finally:
        if staging and os.path.isdir(staging):
            shutil.rmtree(staging, ignore_errors=True)

    if missing:
        eprint("[警告] 以下平台缺失, 未产出对应资产: %s" % ", ".join(missing))
    return 0


if __name__ == "__main__":
    sys.exit(main())
