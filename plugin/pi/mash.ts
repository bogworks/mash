// mash for Pi: delivers mash mail into a Pi session. Everything that decides what is delivered stays in `mash hook`;
// this file only decides when to ask and where Pi puts the answer. A broken mash must never fail or block a Pi run,
// so every failure here is swallowed after at most one notice per kind per session.
import { spawn } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";

type Event = "prompt" | "tool" | "wake" | "init";
type Batch = { text: string; seqs: number[]; urgent: boolean };
type Ctx = { isIdle(): boolean; ui: { notify(message: string, type?: "info" | "warning" | "error"): void } };
type Content = { type: string; [k: string]: unknown };
type Message = { customType: string; content: string; display: boolean; details?: unknown };
// Structural slice of Pi's ExtensionAPI (types.d.ts), so the file needs no Pi package to load or to test.
type Pi = {
	on(event: string, handler: (event: any, ctx: any) => unknown): unknown;
	registerMcpServer(name: string, config: { command: string; args: string[]; env: Record<string, string> }): void;
	sendMessage(message: Message, options?: { triggerTurn?: boolean; deliverAs?: "steer" | "followUp" | "nextTurn" }): void;
};

const HOOK_TIMEOUT_MS = 5000;
const DEBOUNCE_MS = 50;

const mashBin = () => process.env.MASH_BIN || "mash";

export default function (pi: Pi) {
	// Pi merges `env` over its own environment for stdio servers (pi-mcp's StdioTransport spreads process.env first),
	// so MASH_NAME, MASH_OWNER and the rest reach serve. A `mash` entry in mcp.json takes precedence over this one.
	try {
		pi.registerMcpServer("mash", { command: mashBin(), args: ["serve"], env: { MASH_HOST: "pi" } });
	} catch {
		// an invalid config or a name another extension holds: mail still arrives through the hooks
	}

	// One notice per failure kind per session, so a missing binary does not notify on every tool call.
	let reported = new Set<string>();
	// Bumped on every session_start and session_shutdown: work begun under an older generation drops its result.
	let generation = 0;
	let watcher: fs.FSWatcher | undefined;
	let timer: ReturnType<typeof setTimeout> | undefined;
	let chain: Promise<void> = Promise.resolve();

	function report(ctx: Ctx | undefined, kind: string, detail: string) {
		if (reported.has(kind)) return;
		reported.add(kind);
		try {
			ctx?.ui.notify(`mash: ${detail}`, "warning");
		} catch {
			// a stale ctx after session replacement: nothing to tell
		}
	}

	// runHook asks `mash hook` about one event. Null means no batch, or any failure.
	function runHook(event: Event, ctx?: Ctx): Promise<Record<string, unknown> | null> {
		return new Promise((resolve) => {
			let done = false;
			const finish = (v: Record<string, unknown> | null) => {
				if (done) return;
				done = true;
				clearTimeout(kill);
				resolve(v);
			};
			let out = "";
			const fail = (kind: string, detail: string) => {
				report(ctx, kind, detail);
				finish(null);
			};
			let child: ReturnType<typeof spawn>;
			try {
				child = spawn(mashBin(), ["hook"], {
					env: { ...process.env, MASH_HOST: "pi" },
					stdio: ["pipe", "pipe", "ignore"],
				});
			} catch (e) {
				report(ctx, "spawn", `cannot run ${mashBin()}: ${(e as Error).message}`);
				return resolve(null);
			}
			// Settle on the timer, not on close: a killed shell script can leave a grandchild holding stdout open.
			const kill = setTimeout(() => {
				child.kill("SIGKILL");
				fail("timeout", `hook ${event} timed out`);
			}, HOOK_TIMEOUT_MS);
			child.on("error", (e) => fail("spawn", `cannot run ${mashBin()}: ${e.message}`));
			child.stdin!.on("error", () => {}); // a child that exits before reading stdin is judged by its exit
			child.stdout!.on("data", (d) => (out += d));
			child.on("close", (code) => {
				if (code !== 0) return fail("exit", `hook ${event} exited ${code}`);
				if (out.trim() === "") return finish(null);
				try {
					const v = JSON.parse(out);
					if (v === null || typeof v !== "object") throw new Error("not an object");
					finish(v);
				} catch {
					fail("json", `hook ${event} printed bad JSON`);
				}
			});
			child.stdin!.end(JSON.stringify({ event }));
		});
	}

	async function runBatch(event: Event, ctx?: Ctx): Promise<Batch | null> {
		const v = await runHook(event, ctx);
		if (!v || typeof v.text !== "string" || v.text === "") return null;
		return { text: v.text, seqs: Array.isArray(v.seqs) ? (v.seqs as number[]) : [], urgent: v.urgent === true };
	}

	const mailMessage = (b: Batch): Message => ({
		customType: "mash",
		content: b.text,
		display: true,
		details: { seqs: b.seqs, urgent: b.urgent },
	});

	pi.on("before_agent_start", async (event, ctx) => {
		// A run started by one of our own wake messages has just been shown its batch.
		if (event.messageType !== "user") return;
		const b = await runBatch("prompt", ctx);
		return b ? { message: mailMessage(b) } : undefined;
	});

	pi.on("tool_result", async (event, ctx) => {
		const b = await runBatch("tool", ctx);
		if (!b) return;
		const content: Content[] = [...event.content, { type: "text", text: b.text }];
		// Replacing content alone drops structuredContent (ToolResultEventResult), so hand it back when there is one.
		return event.structuredContent === undefined ? { content } : { content, structuredContent: event.structuredContent };
	});

	// act handles one wake file. It runs under the ctx of the session that bound the watcher.
	async function act(w: { text?: unknown; meta?: Record<string, string> | null }, ctx: Ctx, gen: number) {
		const text = typeof w.text === "string" ? w.text : "";
		const meta = w.meta ?? null;
		if (meta?.kind === "prompt") {
			// Prompts are not spooled: this file was the only copy, so it is injected whatever Pi is doing.
			pi.sendMessage(
				{ customType: "mash-prompt", content: text, display: true, details: meta },
				ctx.isIdle() ? { triggerTurn: true } : { deliverAs: "steer" },
			);
		} else if (meta?.error === "true") {
			ctx.ui.notify(text, "error");
			// triggerTurn false: Pi appends it when idle and defers it to the end of a running turn; it never starts one.
			pi.sendMessage({ customType: "mash-error", content: text, display: true, details: meta }, { triggerTurn: false });
		} else if (ctx.isIdle()) {
			// A wake is a hint, not a receipt: the hook decides what is shown. Busy means the next tool_result drains it.
			const b = await runBatch("wake", ctx);
			if (b && gen === generation && ctx.isIdle()) pi.sendMessage(mailMessage(b), { triggerTurn: true });
		}
	}

	// drain handles every wake file in name order (the names sort in write order). Each file is read and deleted
	// before it is acted on, so a crash mid-act cannot replay it.
	async function drain(dir: string, ctx: Ctx, gen: number) {
		let names: string[];
		try {
			names = fs.readdirSync(dir).filter((n) => n.endsWith(".json") && !n.startsWith(".")).sort();
		} catch {
			return;
		}
		for (const name of names) {
			if (gen !== generation) return; // the rest stays on disk for the next session
			const file = path.join(dir, name);
			let w;
			try {
				const raw = fs.readFileSync(file, "utf8");
				fs.unlinkSync(file);
				w = JSON.parse(raw);
			} catch {
				continue; // gone already, or unreadable: nothing to deliver
			}
			try {
				await act(w ?? {}, ctx, gen);
			} catch (e) {
				report(ctx, "wake", `wake failed: ${(e as Error).message}`);
			}
		}
	}

	// schedule coalesces a burst of fs events into one pass, and passes never overlap.
	function schedule(dir: string, ctx: Ctx, gen: number) {
		clearTimeout(timer);
		timer = setTimeout(() => {
			chain = chain.then(() => (gen === generation ? drain(dir, ctx, gen) : undefined)).catch(() => {});
		}, DEBOUNCE_MS);
	}

	function unbind() {
		generation++;
		clearTimeout(timer);
		watcher?.close();
		watcher = undefined;
	}

	pi.on("session_start", async (_event, ctx: Ctx) => {
		unbind(); // a second session_start must not leave the first watcher running
		reported = new Set();
		const gen = generation;
		const init = await runHook("init", ctx);
		if (gen !== generation || !init || typeof init.wake_dir !== "string") return;
		const dir = init.wake_dir;
		try {
			fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
			watcher = fs.watch(dir, () => schedule(dir, ctx, gen));
			watcher.on("error", () => {});
		} catch (e) {
			return report(ctx, "watch", `cannot watch ${dir}: ${(e as Error).message}`);
		}
		schedule(dir, ctx, gen); // wakes written while Pi was down
	});

	pi.on("session_shutdown", unbind);
}
