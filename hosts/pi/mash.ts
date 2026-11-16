// mash for Pi (ADR 0140). Pi has no hooks and takes no MCP channel, so this extension is both:
//   - after each tool result and before each agent start it runs `mash hook` (MASH_HOST=pi) and puts the plain text
//     it prints into the model's next request;
//   - it watches the spool directory `mash spool-dir` prints, and wakes an idle session with sendMessage
//     ({triggerTurn:true}) when mail arrives. While the model is mid-answer the wake is held until agent_settled.
// Copy this file to ~/.pi/agent/extensions/. Pi must be started with MASH_NAME (and MASH_OWNER, MASH_URL, MASH_CREDS
// as for any mash endpoint) in its environment, and MASH_HOST=pi, which the mcp.json entry sets for serve; see
// docs/hosts/pi.md.
import { spawn } from "node:child_process";
import { existsSync, watch } from "node:fs";
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

  // wake starts a turn that carries the waiting mail, unless the model is busy or there is none.
  const wake = async () => {
    if (busy) {
      held = true;
      return;
    }
    const text = await hook("before_agent_start", session);
    if (!text) return;
    pi.sendMessage({ customType: "mash-mail", content: text, display: true }, { triggerTurn: true });
  };

  pi.on("session_start", async (_e: any, ctx: any) => {
    session = ctx.sessionManager.getSessionId();
    if (armed) return;
    const dir = await run(["spool-dir"], "");
    if (!dir) return; // no mash identity: nothing to watch
    // serve creates the spool once its MCP server is up, which can be after session_start: poll for it, then watch.
    const arm = () => {
      if (armed || !existsSync(join(dir, ".owner"))) return;
      armed = true;
      watch(dir, (_kind, name) => {
        // a new message file, not serve's or the hook's bookkeeping (dotfiles)
        if (name && name.endsWith(".json") && !name.startsWith(".")) void wake();
      });
      void wake(); // mail that arrived before the watch
    };
    arm();
    if (!armed) {
      const t = setInterval(() => {
        arm();
        if (armed) clearInterval(t);
      }, 250);
      t.unref();
    }
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
    const text = await hook("tool_result", session);
    if (!text) return;
    return { content: [...event.content, { type: "text", text: "\n\n" + text }] };
  });

  // Before an agent start: put the mail in the request. A wake's own turn already carries its mail.
  pi.on("before_agent_start", async (event: any) => {
    if (event.messageType === "custom") return;
    const text = await hook("before_agent_start", session);
    if (text) return { message: { customType: "mash-mail", content: text, display: true } };
  });
}
