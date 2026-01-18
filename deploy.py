#!/usr/bin/env python3
import subprocess
import sys
import os
import datetime
import argparse

PROJECT_DIR = "/root/telephony-forwarder"
SERVICE_NAME = "telephony-forwarder"
BUILD_CMD = ["go", "build", "-o", "app", "./cmd"]
LOG_FILE = "/var/log/telephony-forwarder/deploy.log"


def run(cmd, cwd=None):
    print(f"$ {' '.join(cmd)}")
    result = subprocess.run(cmd, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    print(result.stdout)
    return result.returncode == 0, result.stdout


def log(msg):
    os.makedirs(os.path.dirname(LOG_FILE), exist_ok=True)
    with open(LOG_FILE, "a") as f:
        f.write(f"[{datetime.datetime.now().isoformat()}] {msg}\n")


def has_new_commits():
    ok, out = run(["git", "fetch"])
    if not ok:
        raise RuntimeError("git fetch failed")

    ok, out = run(["git", "status", "-uno"])
    if not ok:
        raise RuntimeError("git status failed")

    return "behind" in out


def main():
    parser = argparse.ArgumentParser(description="Deploy Go service")
    parser.add_argument("--force", action="store_true", help="Force rebuild even if no git changes")
    parser.add_argument("--no-restart", action="store_true", help="Build only, do not restart service")
    args = parser.parse_args()

    os.chdir(PROJECT_DIR)

    try:
        if has_new_commits():
            print("📥 New commits detected → pulling...")
            ok, _ = run(["git", "pull"])
            if not ok:
                log("❌ git pull failed")
                sys.exit(1)
        elif not args.force:
            print("✅ No changes and --force not set → skip build")
            return
        else:
            print("⚠️ --force enabled → rebuilding anyway")

        ok, _ = run(BUILD_CMD)
        if not ok:
            log("❌ Build failed")
            sys.exit(1)

        if not args.no_restart:
            ok, _ = run(["systemctl", "restart", SERVICE_NAME])
            if not ok:
                log("❌ Restart failed")
                sys.exit(1)

        log("✅ Deploy successful")
        print("🚀 Deploy completed successfully")

    except Exception as e:
        log(f"❌ Deploy exception: {e}")
        raise


if __name__ == "__main__":
    main()
