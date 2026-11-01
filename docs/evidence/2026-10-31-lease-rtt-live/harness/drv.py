#!/usr/bin/env python3
# drv.py TAG NAME URL [K=V ...]: run `mash serve` as endpoint NAME as an MCP client, with its own HOME/XDG_STATE_HOME.
# Writes $R/TAG.pid (mash pid), $R/TAG.log (timestamped events), $R/TAG.err. Stays until mash exits.
import subprocess, sys, os, time, json, threading
tag, name, url = sys.argv[1:4]
R = os.environ["LRL_DIR"] + "/run"
creds = [l.split()[2] for l in open(R + "/env.txt") if l.startswith("EP ") and l.split()[1] == name][0]
H = f"{R}/home-{name}"; os.makedirs(H + "/state", exist_ok=True)
env = {"PATH": "/usr/bin:/bin", "HOME": H, "XDG_STATE_HOME": H + "/state", "MASH_NAME": name, "MASH_URL": url,
       "MASH_OWNER": "o", "MASH_CREDS": creds}
for kv in sys.argv[4:]:
    k, v = kv.split("=", 1); env[k] = v
log = open(f"{R}/{tag}.log", "a", buffering=1)
def L(s): log.write(f"{time.time():.3f} {s}\n")
p = subprocess.Popen([os.environ["BIN"], "serve"], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                     stderr=open(f"{R}/{tag}.err", "w"), env=env)
open(f"{R}/{tag}.pid", "w").write(str(p.pid))
L(f"spawn pid={p.pid}")
def rd():
    for line in p.stdout:
        try:
            m = json.loads(line)
        except Exception:
            L("raw " + line[:200].decode(errors="replace")); continue
        if m.get("method") == "notifications/claude/channel":
            L("CHANNEL " + json.dumps(m["params"])[:300])
        else:
            L("recv id=%s method=%s" % (m.get("id"), m.get("method")))
threading.Thread(target=rd, daemon=True).start()
def send(o): p.stdin.write((json.dumps(o) + "\n").encode()); p.stdin.flush()
send({"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"experimental":{"claude/channel":{}}},"clientInfo":{"name":"live-proof","version":"1"}}})
time.sleep(0.2)
send({"jsonrpc":"2.0","method":"notifications/initialized"})
send({"jsonrpc":"2.0","id":2,"method":"tools/list"})
L("handshake sent")
rc = p.wait(); L(f"EXIT rc={rc}")
