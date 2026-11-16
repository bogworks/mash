// mash for Pi (ADR 0140). Pi has no hooks and takes no MCP channel, so this extension is both:
//   - after each tool result and before each agent start it runs `mash hook` (MASH_HOST=pi) and puts the plain text
//     it prints into the model's next request;
//   - it watches the spool directory `mash spool-dir` prints, and wakes an idle session with sendMessage
//     ({triggerTurn:true}) when mail arrives. While the model is mid-answer the wake is held until agent_settled.
// Copy this file to the extensions/ directory of Pi's agent directory (~/.pi/agent, or $PI_CODING_AGENT_DIR).
// Pi must be started with MASH_NAME (and MASH_OWNER, MASH_URL, MASH_CREDS
// as for any mash endpoint) in its environment, and MASH_HOST=pi, which the mcp.json entry sets for serve; see
// docs/hosts/pi.md.
import { spawn } from "node:child_process";
import { existsSync, watch, type FSWatcher } from "node:fs";
import { join } from "node:path";

// run runs `mash args...` with input on stdin, and resolves with its stdout (trimmed), or "" on any failure: a
// failing hook must never stop the session.
function run(args: string[], input: string): Promise<string> {
  return new Promise((resolve) => {
    const p = spawn("mash", args, {
      stdio: ["pipe", "pipe", "ignore"],
      env: { ...process.env, MASH_HOST: "pi" },
    });
    let out = "";
    p.stdout.on("data", (d) => (out += d));
    p.on("error", () => resolve(""));
    p.on("close", (code) => resolve(code === 0 ? out.trim() : ""));
    p.stdin.on("error", () => {});
    p.stdin.end(input);
  });
}

// hook asks `mash hook` for the mail waiting for this session. event is the name the Go adapter (internal/host/pi.go)
// reads: "tool_result" or "before_agent_start".
const hook = (event: "tool_result" | "before_agent_start", session: string) =>
  run(["hook"], JSON.stringify({ event, session }));

export default function (pi: any) {
  let session = "";
  let busy = false; // the model is mid-answer: hold a wake
  let held = false; // a wake arrived while busy
  let armed = false;
  // gen counts session lifetimes. session_shutdown bumps it, so a wake or hook that began for an old
  // session (a stale watcher event, a pending spool-dir or hook run) sees gen has moved and does nothing.
  let gen = 0;
  let watcher: FSWatcher | undefined;
  let timer: ReturnType<typeof setInterval> | undefined;

  // wake starts a turn that carries the waiting mail, unless the model is busy or there is none.
  const wake = async () => {
    if (busy) {
      held = true;
      return;
    }
    const g = gen;
    const text = await hook("before_agent_start", session);
    if (!text || g !== gen) return;
    pi.sendMessage({ customType: "mash-mail", content: text, display: true }, { triggerTurn: true });
  };

  pi.on("session_start", async (_e: any, ctx: any) => {
    const g = gen;
    session = ctx.sessionManager.getSessionId();
    if (armed) return;
    const dir = await run(["spool-dir"], "");
    if (!dir || g !== gen) return; // no mash identity: nothing to watch
    // serve creates the spool once its MCP server is up, which can be after session_start: poll for it, then watch.
    const arm = () => {
      if (g !== gen || armed || !existsSync(join(dir, ".owner"))) return;
      armed = true;
      watcher = watch(dir, (_kind, name) => {
        if (g !== gen) return; // a watcher event from a session that has ended
        // a new message file, not serve's or the hook's bookkeeping (dotfiles). Node does not always give the file
        // name: then wake anyway, since wake() shows nothing when no mail waits.
        if (!name || (name.endsWith(".json") && !name.startsWith("."))) void wake();
      });
      void wake(); // mail that arrived before the watch
    };
    arm();
    if (!armed) {
      const t = (timer = setInterval(() => {
        arm();
        if (armed || g !== gen) clearInterval(t);
      }, 250));
      t.unref();
    }
  });

  // Pi emits session_shutdown for /new, /resume, /fork and reload (before session_start for the replacement) and on
  // quit: close the watcher and timer, so a stale instance cannot take the spool lock and drain mail.
  pi.on("session_shutdown", () => {
    gen++;
    watcher?.close();
    watcher = undefined;
    if (timer) clearInterval(timer);
    timer = undefined;
    armed = false;
    busy = false;
    held = false;
  });

  pi.on("agent_start", () => {
    busy = true;
  });
  pi.on("agent_settled", () => {
    busy = false;
    if (held) {
      held = false;
      void wake();
    }
  });

  // After a tool result: add the mail to that result, so the model sees it on its next step.
  pi.on("tool_result", async (event: any) => {
    const g = gen;
    const text = await hook("tool_result", session);
    if (!text || g !== gen) return;
    return { content: [...event.content, { type: "text", text: "\n\n" + text }] };
  });

  // Before an agent start: put the mail in the request. A wake's own turn already carries its mail.
  pi.on("before_agent_start", async (event: any) => {
    if (event.messageType === "custom") return;
    const g = gen;
    const text = await hook("before_agent_start", session);
    if (text && g === gen) return { message: { customType: "mash-mail", content: text, display: true } };
  });
}
