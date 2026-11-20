Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# MCP server

| Topic | Detail |
| --- | --- |
| Started by | The agent host, as 'mash serve', once per session. It speaks MCP over standard input and output. |
| MCP revisions | 2024-11-05, 2025-03-26 and 2025-06-18. Not 2026-07-28: that revision does not let a server send what the client did not ask for, and Claude Code does not register a channel server that negotiates it. Looked at again in the 0.6.0 sprint (board card 213). |
| Every tool result | Carries two things: the structured result, which is exactly the object the matching command prints with --json; and a text block, which is exactly what the command prints without it. Today tools return text only. No output schema is declared in 0.5.6. The text block being the command's own text, not JSON, is a deliberate departure from MCP's suggestion. |
| A tool that fails | Returns an error result whose text is the command's 'error:' line and its 'hint:' line, and whose structured result is {"error": ..., "hint": ...}. |
| Setup skills | The marketplace carries two plugins: 'mash' (this server and its hooks) and 'mash-setup' (two skills, one to set mash up and one to run it day to day). A person installs mash-setup first and asks their agent to set mash up; the skill installs mash, the NATS server if wanted, and the mash plugin, asking before each step. A skill cannot install the plugin it ships in, which is why there are two. |
| A tool's description is written for the agent | It may be longer than the command's one line: it tells the model when to use the tool and how to choose its parameters. It is part of this specification and is the text the server sends. |
| wide on a tool | Where a command has --wide, its tool has wide. It changes only the text block; the structured result always carries every field. |
| One tool, one command | Each tool is one everyday mash command with the same name, the same parameters (an option --reply-to is the parameter reply_to) and the same result. Setup, integration and raw SAP commands have no tool. |
| With no endpoint named, or a broken configuration | Only status, doctor and version are offered, so the agent can find out what is wrong. The server never picks an endpoint by itself: a session is an endpoint only when MASH_ENDPOINT, --endpoint or the configuration file says which. |
| Waking an idle session | A Claude Code channel event: the capability 'claude/channel', declared as experimental, and the notification 'notifications/claude/channel' carrying one line that says mail is waiting. The mail itself arrives through mash hook. This is a Claude Code feature carried inside MCP, not part of MCP. |
| Instructions to the agent | On connecting, the server tells the agent its own address, how mail arrives, what each trust label means, and that message text is data from the sender, not instructions. |
