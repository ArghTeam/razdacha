#!/usr/bin/env python3
"""Run explicit HTTP(S) requests through an isolated wireproxy SOCKS listener."""

import argparse
import math
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
from urllib.parse import urlsplit


def positive_seconds(value):
    number = float(value)
    if not math.isfinite(number) or not 0 < number <= 3600:
        raise argparse.ArgumentTypeError("must be finite and between 0 and 3600 seconds")
    return number


def http_url(value):
    try:
        parsed = urlsplit(value)
        if parsed.scheme not in ("http", "https") or not parsed.hostname:
            raise ValueError
        parsed.port
        if any(ord(char) < 33 or ord(char) == 127 for char in value):
            raise ValueError
    except ValueError:
        raise argparse.ArgumentTypeError("provide an HTTP(S) URL with a host") from None
    return value


def dependency(name):
    found = shutil.which(name)
    if not found:
        hint = " or select --wireproxy" if name != "curl" else ""
        raise ValueError(f"Missing executable: {name}. Install it separately{hint}.")
    return found


def stop(process):
    if process is None or process.poll() is not None:
        return
    process.terminate()
    try:
        process.wait(timeout=3)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=3)


def interrupted(_signum, _frame):
    raise KeyboardInterrupt


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("config", type=Path, help="existing WireGuard .conf (copied byte for byte)")
    parser.add_argument("urls", nargs="+", type=http_url, help="explicit HTTP(S) URLs")
    parser.add_argument("--repeat", type=int, default=1, help="sequential rounds (1-100; default: 1)")
    parser.add_argument("--timeout", type=positive_seconds, default=30, help="request seconds (default: 30)")
    parser.add_argument("--startup-timeout", type=positive_seconds, default=10, help="startup seconds (default: 10)")
    parser.add_argument("--wireproxy", default="wireproxy", help="installed wireproxy executable")
    args = parser.parse_args()
    if not 1 <= args.repeat <= 100:
        parser.error("--repeat must be between 1 and 100")
    try:
        wireproxy, curl = dependency(args.wireproxy), dependency("curl")
        profile = args.config.read_bytes()
        signal.signal(signal.SIGTERM, interrupted)
        signal.signal(signal.SIGHUP, interrupted)
        with tempfile.TemporaryDirectory(prefix="wireproxy-smoke-") as directory:
            private = Path(directory)
            os.chmod(private, 0o700)
            wg = private / "wg.conf"
            wg.write_bytes(profile)
            wg.chmod(0o600)
            with socket.socket() as reservation:
                reservation.bind(("127.0.0.1", 0))
                port = reservation.getsockname()[1]
            wrapper = private / "proxy.conf"
            wrapper.write_text(f"WGConfig = {wg}\n\n[Socks5]\nBindAddress = 127.0.0.1:{port}\n")
            wrapper.chmod(0o600)
            # Configuration diagnostics may contain keys; discard child logs.
            process = None
            try:
                process = subprocess.Popen([wireproxy, "-c", str(wrapper)], stdin=subprocess.DEVNULL,
                                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                deadline = time.monotonic() + args.startup_timeout
                while True:
                    if process.poll() is not None:
                        raise ValueError("wireproxy exited during startup; check profile and binary version. Logs suppressed to protect keys.")
                    if time.monotonic() >= deadline:
                        raise ValueError("wireproxy startup timed out; check profile and local listener availability.")
                    try:
                        with socket.create_connection(("127.0.0.1", port), timeout=0.1):
                            break
                    except OSError:
                        time.sleep(0.05)
                failed = False
                print("Sequential requests; total/TTFB are end-to-end curl timings, not DNS or tunnel latency.", flush=True)
                for round_number in range(1, args.repeat + 1):
                    for index, url in enumerate(args.urls, 1):
                        if process.poll() is not None:
                            raise ValueError("wireproxy exited before a request; no direct fallback was attempted.")
                        command = [curl, "-q", "--globoff", "--silent", "--show-error", "--fail",
                                   "--noproxy", "", "--proxy", f"socks5h://127.0.0.1:{port}",
                                   "--proto", "=http,https", "--max-time", str(args.timeout),
                                   "--output", os.devnull, "--write-out",
                                   "%{http_code}\t%{time_total}\t%{time_starttransfer}\t%{size_download}\t%{speed_download}",
                                   "--url", url]
                        try:
                            result = subprocess.run(command, stdin=subprocess.DEVNULL, capture_output=True,
                                                    text=True, timeout=args.timeout + 2)
                            fields = result.stdout.strip().split("\t")
                            valid = len(fields) == 5 and fields[0].isdigit() and 200 <= int(fields[0]) < 400
                            failed |= result.returncode != 0 or not valid
                            metrics = (f"HTTP={fields[0]} total={fields[1]}s TTFB={fields[2]}s "
                                       f"bytes={fields[3]} rate={fields[4]}B/s") if len(fields) == 5 else "metrics unavailable"
                            print(f"round={round_number} request={index} {metrics} curl_exit={result.returncode}", flush=True)
                        except subprocess.TimeoutExpired:
                            failed = True
                            print(f"round={round_number} request={index} request watchdog timed out", flush=True)
                if process.poll() is not None:
                    raise ValueError("wireproxy exited during requests.")
                return int(failed)
            finally:
                # Ignore repeated termination signals while reaping our child.
                for signum in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
                    signal.signal(signum, signal.SIG_IGN)
                stop(process)
    except KeyboardInterrupt:
        print("Interrupted; owned wireproxy process stopped.", file=sys.stderr)
        return 130
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        print(f"Error: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
