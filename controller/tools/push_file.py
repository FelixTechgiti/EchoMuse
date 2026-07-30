"""Push a local file to a device over the controller shell proxy, resumably.

    docker cp controller/tools/push_file.py echomuse-controller:/tmp/
    docker cp device/build/oww_probe echomuse-controller:/tmp/
    docker exec echomuse-controller python /tmp/push_file.py \
        <device_id> /tmp/oww_probe /data/local/tmp/oww_probe [--chmod 755]

Why this exists rather than `ota.py`: that pushes the firmware binary through
the update endpoint, which writes one specific place. This puts an arbitrary
file anywhere, which is what the on-device wake word work needs (a 12.3MB
libonnxruntime.so, three .onnx models, a test fixture).

Four device-specific traps are baked in, each of which produced a convincing
wrong answer first:

  * The shell plane drops after roughly 50 seconds of sustained load, so the
    transfer reconnects every RECONNECT_S and picks up where it left off. This
    is not optional for anything multi-megabyte.

  * Resuming on file SIZE will happily append to a *different* file that
    happens to be there, and then report success while the device runs stale
    bytes. So the default is to delete the destination first; --resume is
    opt-in, and even then the md5 check at the end is what decides.

  * `busybox truncate` silently no-ops on this build. Trimming a partial tail
    uses `dd` + `mv` instead.

  * The PTY echoes each command back, so a completion marker sent as a literal
    string matches its OWN echo — that is how an empty file once reported
    "transfer OK". Markers here are split in the source (M'K'1) so the echoed
    text differs from what is matched. The data path avoids markers entirely by
    waiting for the shell prompt, which is safe because base64 contains no '#'.

Success is md5 agreement, not "no errors". Exit status is non-zero otherwise.
"""
import asyncio, base64, hashlib, os, secrets, sqlite3, sys, time
import websockets

DB = "/app/data/echomuse.db"

# Base64 characters per shell command. The decoded payload is 3/4 of this, so
# ~24KB a command: large enough that a 12MB push is ~500 commands, small enough
# to stay well inside the device's argument limit.
CHUNK_B64 = 32768
# Reconnect this often. The plane dies somewhere around 50s under load, so
# rotate well before that rather than discovering the edge.
RECONNECT_S = 30


def make_token():
    tok = secrets.token_hex(32)
    con = sqlite3.connect(DB)
    cols = [r[1] for r in con.execute("PRAGMA table_info(sessions)")]
    if "expires_at" in cols:
        con.execute(
            "INSERT INTO sessions (token, user_id, created_at, expires_at) "
            "VALUES (?, 1, datetime('now'), datetime('now', '+30 minutes'))", (tok,))
    else:
        con.execute("INSERT INTO sessions (token, user_id, created_at) "
                    "VALUES (?, 1, datetime('now'))", (tok,))
    con.commit()
    con.close()
    return tok


def drop_token(tok):
    con = sqlite3.connect(DB)
    con.execute("DELETE FROM sessions WHERE token=?", (tok,))
    con.commit()
    con.close()


class Shell:
    """One shell session. Short-lived by design — see RECONNECT_S."""

    def __init__(self, ws):
        self.ws = ws
        self.buf = bytearray()

    async def read_until(self, marker, timeout=30):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            try:
                msg = await asyncio.wait_for(self.ws.recv(), timeout=deadline - time.monotonic())
            except asyncio.TimeoutError:
                break
            if isinstance(msg, str):
                continue
            self.buf.extend(msg)
            if marker in bytes(self.buf[-4096:]):
                return True
        return False

    async def cmd(self, line, timeout=30):
        """Run a command and return its output, prompt-delimited."""
        self.buf.clear()
        await self.ws.send(b"\x00" + (line + "\n").encode())
        ok = await self.read_until(b"# ", timeout=timeout)
        if not ok:
            raise RuntimeError(f"timed out waiting for prompt after: {line[:60]}")
        return bytes(self.buf).decode("utf-8", "replace")


async def connect(device, tok):
    uri = f"ws://127.0.0.1:8768/api/devices/{device}/shell?token={tok}"
    ws = await websockets.connect(uri, max_size=None)
    sh = Shell(ws)
    await sh.read_until(b"# ")
    # Suppress echo where the device supports it; the code never relies on
    # that working, only benefits from the smaller reads.
    await sh.cmd("busybox stty -echo 2>/dev/null", timeout=10)
    return ws, sh


async def remote_size(sh, path):
    out = await sh.cmd(f"busybox wc -c < {path} 2>/dev/null || echo 0")
    for tok in reversed(out.split()):
        if tok.isdigit():
            return int(tok)
    return 0


async def remote_md5(sh, path):
    # Split marker: the PTY echoes this command back, and an unsplit marker
    # would match its own echo.
    out = await sh.cmd(f"echo M'D'5:$(busybox md5sum {path} | busybox cut -d' ' -f1)", timeout=120)
    for line in out.splitlines():
        line = line.strip()
        if line.startswith("MD5:") and len(line) == 4 + 32:
            return line[4:]
    raise RuntimeError(f"could not read md5 of {path}; got: {out[-200:]!r}")


async def push(device, local, remote, mode, resume):
    data = open(local, "rb").read()
    want_md5 = hashlib.md5(data).hexdigest()
    payload = CHUNK_B64 // 4 * 3
    print(f"{local} -> {device}:{remote}", file=sys.stderr)
    print(f"{len(data)} bytes, md5 {want_md5}, {payload}B/command", file=sys.stderr)

    tok = make_token()
    sent = 0
    try:
        ws, sh = await connect(device, tok)
        try:
            await sh.cmd(f"mkdir -p {os.path.dirname(remote) or '/'}")
            if resume:
                have = await remote_size(sh, remote)
                # Only resume on a whole-chunk boundary; trim the partial tail
                # with dd, because truncate silently does nothing here.
                sent = have - (have % payload)
                if have != sent:
                    await sh.cmd(f"busybox dd if={remote} of={remote}.t bs={sent} count=1 "
                                 f"2>/dev/null && busybox mv {remote}.t {remote}")
                if sent:
                    print(f"resuming at {sent} bytes", file=sys.stderr)
            else:
                # The default. Appending to whatever was there is how a stale
                # binary gets run while the push reports success.
                await sh.cmd(f"rm -f {remote}")

            opened = time.monotonic()
            while sent < len(data):
                if time.monotonic() - opened > RECONNECT_S:
                    await ws.close()
                    ws, sh = await connect(device, tok)
                    opened = time.monotonic()

                blk = data[sent:sent + payload]
                b64 = base64.b64encode(blk).decode()
                # No marker needed: base64 contains no '#', so waiting for the
                # prompt cannot match anything in the echoed command.
                await sh.cmd(f"echo {b64} | busybox base64 -d >> {remote}", timeout=60)
                sent += len(blk)
                pct = 100.0 * sent / len(data)
                print(f"\r  {sent}/{len(data)} bytes ({pct:.1f}%)", end="", file=sys.stderr)
            print(file=sys.stderr)

            got = await remote_md5(sh, remote)
            if got != want_md5:
                print(f"MD5 MISMATCH: device has {got}, local is {want_md5}", file=sys.stderr)
                return 1
            print(f"md5 ok: {got}", file=sys.stderr)
            if mode:
                await sh.cmd(f"chmod {mode} {remote}")
                print(f"chmod {mode}", file=sys.stderr)
            return 0
        finally:
            await ws.close()
    finally:
        drop_token(tok)


if __name__ == "__main__":
    import argparse
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("device", help="device id, e.g. G090LF11803611NF")
    ap.add_argument("local", help="local path (inside the controller container)")
    ap.add_argument("remote", help="destination path on the device")
    ap.add_argument("--chmod", metavar="MODE", help="chmod the file after a verified push")
    ap.add_argument("--resume", action="store_true",
                    help="continue an interrupted push instead of deleting the destination; "
                         "the md5 check still decides")
    a = ap.parse_args()
    sys.exit(asyncio.run(push(a.device, a.local, a.remote, a.chmod, a.resume)))
