// Run with: node --test plugin/pi/   (Node 26 strips the types; no packages needed)
import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import extension from "./mash.ts";

// The stub `mash`: answers `hook` per event from STUB_<EVENT> (stdout text) and logs each event to STUB_LOG.
const STUB = `#!/usr/bin/env node
const fs = require("node:fs");
let s = "";
process.stdin.on("data", (d) => (s += d));
process.stdin.on("end", () => {
  const ev = JSON.parse(s).event;
  fs.appendFileSync(process.env.STUB_LOG, ev + " " + process.env.MASH_HOST + "\\n");
  if (process.env.STUB_MODE === "fail") process.exit(1);
  if (process.env.STUB_MODE === "hang") return setTimeout(() => {}, 60000);
  if (process.env.STUB_MODE === "bad") return process.stdout.write("{nope");
  process.stdout.write(process.env["STUB_" + ev.toUpperCase()] ?? "");
});
`;

let tmp: string, wakeDir: string, log: string;
const sessions: any[] = [];

beforeEach(() => {
	tmp = fs.mkdtempSync(path.join(os.tmpdir(), "mash-pi-"));
	wakeDir = path.join(tmp, "wakes");
	log = path.join(tmp, "log");
	fs.writeFileSync(log, "");
	const bin = path.join(tmp, "mash");
	fs.writeFileSync(bin, STUB, { mode: 0o755 });
	process.env.MASH_BIN = bin;
	process.env.STUB_LOG = log;
	process.env.STUB_INIT = JSON.stringify({ wake_dir: wakeDir });
	for (const k of ["STUB_MODE", "STUB_PROMPT", "STUB_TOOL", "STUB_WAKE"]) delete process.env[k];
});

afterEach(async () => {
	for (const s of sessions.splice(0)) await s.shutdown();
	fs.rmSync(tmp, { recursive: true, force: true });
});

const batch = (text: string, seqs = [1], urgent = false) => JSON.stringify({ text, seqs, urgent });
const events = () => fs.readFileSync(log, "utf8").split("\n").filter(Boolean);

// load builds a fake pi, loads the extension into it, and returns what it recorded.
function load() {
	const handlers = new Map<string, (e: any, c: any) => any>();
	const sent: { message: any; options: any }[] = [];
	const mcp: any[] = [];
	const notes: string[] = [];
	const state = { idle: true };
	const ctx = {
		isIdle: () => state.idle,
		ui: { notify: (m: string) => notes.push(m) },
	};
	const pi = {
		on: (e: string, h: any) => void handlers.set(e, h),
		registerMcpServer: (...a: any[]) => void mcp.push(a),
		sendMessage: (message: any, options: any) => void sent.push({ message, options }),
	};
	extension(pi);
	const s = {
		handlers, sent, mcp, notes, state, ctx,
		start: (reason = "startup") => handlers.get("session_start")!({ type: "session_start", reason }, ctx),
		shutdown: async () => handlers.get("session_shutdown")?.({ type: "session_shutdown" }, ctx),
	};
	sessions.push(s);
	return s;
}

async function waitFor(cond: () => boolean, ms = 3000) {
	const end = Date.now() + ms;
	while (!cond()) {
		if (Date.now() > end) assert.fail("timed out waiting");
		await new Promise((r) => setTimeout(r, 20));
	}
}
const settle = () => new Promise((r) => setTimeout(r, 300));

let n = 0;
function writeWake(w: object, name = String(++n).padStart(6, "0")) {
	fs.mkdirSync(wakeDir, { recursive: true });
	fs.writeFileSync(path.join(wakeDir, `.${name}.tmp`), JSON.stringify(w));
	fs.renameSync(path.join(wakeDir, `.${name}.tmp`), path.join(wakeDir, `${name}.json`));
}
const wakeFiles = () => (fs.existsSync(wakeDir) ? fs.readdirSync(wakeDir).filter((f) => f.endsWith(".json")) : []);

test("registers the mash MCP server with MASH_HOST=pi and the resolved binary", () => {
	const s = load();
	assert.deepEqual(s.mcp, [["mash", { command: process.env.MASH_BIN, args: ["serve"], env: { MASH_HOST: "pi" } }]]);
});

test("before_agent_start injects the prompt batch as a mash message", async () => {
	process.env.STUB_PROMPT = batch("hello", [4, 5], true);
	const s = load();
	const r = await s.handlers.get("before_agent_start")!({ messageType: "user", prompt: "x" }, s.ctx);
	assert.deepEqual(r, { message: { customType: "mash", content: "hello", display: true, details: { seqs: [4, 5], urgent: true } } });
	assert.deepEqual(events(), ["prompt pi"]);
});

test("before_agent_start with no batch, or for a custom run, changes nothing", async () => {
	const s = load();
	assert.equal(await s.handlers.get("before_agent_start")!({ messageType: "user" }, s.ctx), undefined);
	process.env.STUB_PROMPT = batch("hello");
	assert.equal(await s.handlers.get("before_agent_start")!({ messageType: "custom" }, s.ctx), undefined);
	assert.deepEqual(events(), ["prompt pi"]);
	assert.deepEqual(s.notes, []);
});

test("tool_result appends the batch as one more text block and keeps structuredContent", async () => {
	process.env.STUB_TOOL = batch("mail");
	const s = load();
	const content = [{ type: "text", text: "out" }];
	const r = await s.handlers.get("tool_result")!({ content, structuredContent: { a: 1 } }, s.ctx);
	assert.deepEqual(r, { content: [...content, { type: "text", text: "mail" }], structuredContent: { a: 1 } });
	assert.deepEqual(content, [{ type: "text", text: "out" }]);
	const r2 = await s.handlers.get("tool_result")!({ content }, s.ctx);
	assert.deepEqual(Object.keys(r2), ["content"]);
});

test("tool_result with no batch returns nothing", async () => {
	const s = load();
	assert.equal(await s.handlers.get("tool_result")!({ content: [] }, s.ctx), undefined);
});

for (const mode of ["fail", "bad"]) {
	test(`a ${mode} mash does not throw and notifies once`, async () => {
		process.env.STUB_MODE = mode;
		const s = load();
		for (let i = 0; i < 3; i++) assert.equal(await s.handlers.get("tool_result")!({ content: [] }, s.ctx), undefined);
		assert.equal(s.notes.length, 1);
	});
}

test("a missing mash binary does not throw and notifies once", async () => {
	process.env.MASH_BIN = path.join(tmp, "absent");
	const s = load();
	for (let i = 0; i < 2; i++) assert.equal(await s.handlers.get("before_agent_start")!({ messageType: "user" }, s.ctx), undefined);
	assert.equal(s.notes.length, 1);
});

test("a hanging mash is cut off at the timeout, with one notice", { timeout: 15000 }, async () => {
	process.env.STUB_MODE = "hang";
	const s = load();
	const t = Date.now();
	assert.equal(await s.handlers.get("tool_result")!({ content: [] }, s.ctx), undefined);
	assert.ok(Date.now() - t < 8000);
	assert.equal(s.notes.length, 1);
	assert.match(s.notes[0], /timed out/);
});

test("a wake while idle runs the wake event and triggers a turn", async () => {
	process.env.STUB_WAKE = batch("mail!", [7], true);
	const s = load();
	await s.start();
	writeWake({ at: "t", text: "summary", meta: null });
	await waitFor(() => s.sent.length === 1);
	assert.deepEqual(s.sent[0], {
		message: { customType: "mash", content: "mail!", display: true, details: { seqs: [7], urgent: true } },
		options: { triggerTurn: true },
	});
	assert.deepEqual(events(), ["init pi", "wake pi"]);
	assert.deepEqual(wakeFiles(), []);
});

test("a wake while busy does nothing, and the file is consumed", async () => {
	process.env.STUB_WAKE = batch("mail!");
	const s = load();
	s.state.idle = false;
	await s.start();
	writeWake({ at: "t", text: "summary", meta: null });
	await waitFor(() => wakeFiles().length === 0);
	await settle();
	assert.deepEqual(s.sent, []);
	assert.deepEqual(events(), ["init pi"]);
});

test("a prompt wake is injected with a turn while idle", async () => {
	const s = load();
	await s.start();
	const meta = { kind: "prompt", request_id: "r1", from: "bob" };
	writeWake({ at: "t", text: "what now?", meta });
	await waitFor(() => s.sent.length === 1);
	assert.deepEqual(s.sent[0], {
		message: { customType: "mash-prompt", content: "what now?", display: true, details: meta },
		options: { triggerTurn: true },
	});
	assert.deepEqual(events(), ["init pi"]); // a prompt is not a hook drain
});

test("a prompt wake is steered while busy", async () => {
	const s = load();
	s.state.idle = false;
	await s.start();
	writeWake({ at: "t", text: "what now?", meta: { kind: "prompt", request_id: "r1" } });
	await waitFor(() => s.sent.length === 1);
	assert.deepEqual(s.sent[0].options, { deliverAs: "steer" });
});

test("an error wake notifies and injects a message without triggering a turn", async () => {
	const s = load();
	await s.start();
	writeWake({ at: "t", text: "serve lost its lease", meta: { error: "true" } });
	await waitFor(() => s.sent.length === 1);
	assert.deepEqual(s.notes, ["serve lost its lease"]);
	assert.equal(s.sent[0].message.customType, "mash-error");
	assert.deepEqual(s.sent[0].options, { triggerTurn: false });
});

test("wakes written before session_start are processed, in name order, each deleted", async () => {
	const s = load();
	writeWake({ text: "third", meta: { kind: "prompt" } }, "000003");
	writeWake({ text: "first", meta: { kind: "prompt" } }, "000001");
	writeWake({ text: "second", meta: { kind: "prompt" } }, "000002");
	fs.writeFileSync(path.join(wakeDir, ".partial.tmp"), "{"); // never read
	await s.start();
	await waitFor(() => s.sent.length === 3);
	assert.deepEqual(s.sent.map((m) => m.message.content), ["first", "second", "third"]);
	assert.deepEqual(wakeFiles(), []);
	assert.ok(fs.existsSync(path.join(wakeDir, ".partial.tmp")));
});

test("a missing wake directory is created, and a garbled wake file is dropped", async () => {
	const s = load();
	await s.start();
	assert.ok(fs.statSync(wakeDir).isDirectory());
	fs.writeFileSync(path.join(wakeDir, "000001.json"), "not json");
	writeWake({ text: "ok", meta: { kind: "prompt" } }, "000002");
	await waitFor(() => s.sent.length === 1);
	assert.deepEqual(wakeFiles(), []);
});

test("a second session_start leaves one watcher: each wake is handled once", async () => {
	const s = load();
	await s.start();
	await s.start("resume");
	await s.start("new");
	for (let i = 0; i < 3; i++) writeWake({ text: `p${i}`, meta: { kind: "prompt" } });
	await waitFor(() => s.sent.length >= 3);
	await settle();
	assert.deepEqual(s.sent.map((m) => m.message.content), ["p0", "p1", "p2"]);
	assert.equal(events().filter((e) => e.startsWith("init")).length, 3);
});

test("session_shutdown stops delivery; the file stays for the next session", async () => {
	const s = load();
	await s.start();
	await s.shutdown();
	writeWake({ text: "later", meta: { kind: "prompt" } });
	await settle();
	assert.deepEqual(s.sent, []);
	assert.equal(wakeFiles().length, 1);
	await s.start();
	await waitFor(() => s.sent.length === 1);
});

test("a failing init binds nothing and notifies once", async () => {
	process.env.STUB_MODE = "fail";
	const s = load();
	await s.start();
	writeWake({ text: "x", meta: { kind: "prompt" } });
	await settle();
	assert.deepEqual(s.sent, []);
	assert.equal(s.notes.length, 1);
});
