// mash for Pi (ADR 0140). Pi has no hooks and takes no MCP channel, so this extension is both:
//   - after each tool result and before each agent start it runs `mash hook` (MASH_HOST=pi), which offers the waiting
//     mail as JSON {"text","ack"} without draining it; the extension puts the text into the model's next request and
//     runs `mash hook --ack <token>` only when Pi's message_end shows the message that carries the text was added to
//     the conversation, so mail is never lost to a shutdown or abort in between;
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

// REOFFER_MS must exceed mash's re-offer hold (reofferAfter = 60 * time.Second in `mash hook`), or a retry finds the
// dropped batch still withheld and offers nothing.
const REOFFER_MS = 61_000;

type Offer = { text: string; ack: string };

// hook asks `mash hook` for the mail waiting for this session. event is the name the Go adapter (internal/host/pi.go)
// reads: "tool_result" or "before_agent_start". It prints nothing when no mail waits, else {"text","ack"} (a notice
// has no "ack"). The hook only offers the batch: it stays spooled until ack() runs.
async function hook(event: "tool_result" | "before_agent_start", session: string): Promise<Offer | undefined> {
  const out = await run(["hook"], JSON.stringify({ event, session }));
  if (!out) return undefined;
  try {
    const o = JSON.parse(out);
    if (typeof o?.text === "string" && o.text) return { text: o.text, ack: typeof o.ack === "string" ? o.ack : "" };
  } catch {}
  return undefined;
}

// ack tells `mash hook` Pi has accepted the batch, so it marks exactly those messages delivered. Call it only after
// Pi took the message and only while the session is the one the batch was offered to; otherwise do not call it, and the
// mail stays spooled for the next session. A failed ack is not an error: the batch is offered again.
const ack = (event: "tool_result" | "before_agent_start", session: string, offer: Offer) =>
  offer.ack ? run(["hook", "--ack", offer.ack], JSON.stringify({ event, session })) : Promise.resolve("");

export default function (pi: any) {
  let session = "";
  let busy = false; // the model is mid-answer: hold a wake
  let held = false; // a wake arrived while busy
  let armed = false;
  let warned = false; // the no-identity notice was given for this session lifetime (session_start can fire twice)
  // pending are offers handed to Pi (a tool result, an injected message, a wake) and not yet confirmed by message_end.
  // Each is acked at its confirming message_end, and dropped unacked at agent_settled or session_shutdown: the mail
  // stays spooled and is offered again (to this session after 60 s; a new session gets it once its serve takes the mail over, which can take up to about 30 s).
  type Pending = { g: number; s: string; event: "tool_result" | "before_agent_start"; offer: Offer };
  let pending: Pending[] = [];
  // gen counts session lifetimes. session_shutdown bumps it, so a wake or hook that began for an old
  // session (a stale watcher event, a pending spool-dir or hook run) sees gen has moved and does nothing.
  let gen = 0;
  let watcher: FSWatcher | undefined;
  let timer: ReturnType<typeof setInterval> | undefined;
  // retry looks for mail again once the hook's hold on a dropped offer has expired (see agent_settled).
  let retry: ReturnType<typeof setTimeout> | undefined;

  // wake starts a turn that carries the waiting mail, unless the model is busy or there is none.
  const wake = async () => {
    if (!armed) return; // no session to wake yet, or it has shut down
    if (busy) {
      held = true;
      return;
    }
    const g = gen;
    const s = session;
    const offer = await hook("before_agent_start", s);
    if (!offer || g !== gen) return; // the session changed: not acked, the mail stays spooled for the next one
    // Pi has no event for "sendMessage took it" other than the custom message's message_end, so the ack waits for that.
    const p: Pending = { g, s, event: "before_agent_start", offer };
    pending.push(p);
    try {
      pi.sendMessage({ customType: "mash-mail", content: offer.text, display: true }, { triggerTurn: true });
    } catch {
      pending = pending.filter((x) => x !== p); // not sent: not acked, the mail stays spooled
    }
  };

  // textOf is the text of a message's content, a string or a list of parts.
  const textOf = (c: any): string =>
    typeof c === "string" ? c : Array.isArray(c) ? c.map((x: any) => (typeof x?.text === "string" ? x.text : "")).join("") : "";

  // A message was finalized into the conversation (message_end fires for user, assistant, tool-result and custom
  // messages). If it carries a pending offer's text (the offer's own custom message, or the tool result it was added
  // to), the mail has reached the conversation: ack it.
  pi.on("message_end", (event: any) => {
    const m = event?.message;
    if (!m || (m.role !== "custom" && m.role !== "toolResult") || pending.length === 0) return;
    if (m.role === "custom" && m.customType !== "mash-mail") return;
    const body = textOf(m.content);
    const done = pending.filter((p) => p.g === gen && body.includes(p.offer.text));
    if (done.length === 0) return;
    pending = pending.filter((p) => !done.includes(p));
    for (const p of done) void ack(p.event, p.s, p.offer);
  });

  pi.on("session_start", async (_e: any, ctx: any) => {
    const g = gen;
    session = ctx.sessionManager.getSessionId();
    if (armed) return;
    const dir = await run(["spool-dir"], "");
    if (g !== gen) return;
    if (!dir) {
      // No mash identity in Pi's own environment (serve may still work, from mcp.json's env): nothing to watch, and
      // mail would sit unseen. Say so once per session, in the UI and on stderr, never silently.
      if (!warned) {
        warned = true;
        const msg =
          "mash: Pi's environment has no mash identity (MASH_NAME/MASH_OWNER/MASH_URL or config); mail will not be delivered to this session";
        console.error(msg);
        try {
          ctx.ui?.notify?.(msg, "warning");
        } catch {}
      }
      return;
    }
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
    if (retry) clearTimeout(retry);
    retry = undefined;
    armed = false;
    warned = false;
    busy = false;
    held = false;
    pending = []; // unconfirmed offers are dropped unacked: the mail stays spooled
  });

  pi.on("agent_start", () => {
    busy = true;
  });
  pi.on("agent_settled", () => {
    busy = false;
    held = false;
    const dropped = pending.length > 0;
    pending = []; // offers no message_end confirmed (blocked, aborted) are dropped unacked
    // Mail beyond one batch, or held while busy, waits with no new file to wake on: look again. wake() sends nothing
    // when the hook offers nothing, and the hook holds an offered batch back from this session for 60 s, so this
    // cannot loop hot: a turn starts only when the hook offers new text, and then settles once.
    void wake();
    // A dropped offer stays held back from this session for the hook's 60 s, and nothing else may check again when
    // the hold ends (an idle session gets no new file): look once more then. One timer, replaced not stacked; a retry
    // whose own offer is dropped schedules the next, so delivery is at least once, at most one look per REOFFER_MS.
    if (dropped) {
      const g = gen;
      if (retry) clearTimeout(retry);
      retry = setTimeout(() => {
        retry = undefined;
        if (g === gen) void wake();
      }, REOFFER_MS);
      retry.unref();
    }
  });

  // After a tool result: add the mail to that result, so the model sees it on its next step.
  pi.on("tool_result", async (event: any) => {
    const g = gen;
    const s = session;
    const offer = await hook("tool_result", s);
    if (!offer || g !== gen) return;
    pending.push({ g, s, event: "tool_result", offer }); // acked at the tool result's message_end
    return { content: [...event.content, { type: "text", text: "\n\n" + offer.text }] };
  });

  // Before an agent start: put the mail in the request. A wake's own turn already carries its mail.
  pi.on("before_agent_start", async (event: any) => {
    if (event.messageType === "custom") return;
    const g = gen;
    const s = session;
    const offer = await hook("before_agent_start", s);
    if (!offer || g !== gen) return;
    pending.push({ g, s, event: "before_agent_start", offer }); // acked at the message's message_end
    return { message: { customType: "mash-mail", content: offer.text, display: true } };
  });
}
