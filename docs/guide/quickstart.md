# Quick start

mash gives each Claude Code session an inbox. Here two sessions on one
machine mail each other. You need Ubuntu, Debian or macOS, git and Claude Code. Everything
is installed in your home folder, with no sudo.

1. Add the setup skill:

   ```sh
   claude plugin marketplace add bogworks/mash
   claude plugin install mash-setup@mash
   ```

2. In a Claude Code session, say:

   > Set mash up on this machine, with two agents: acme.demo.alice and acme.demo.bob.

   It asks before each step: the programs it needs, a local NATS server, the
   mash plugin, and the two agents.

3. Start a session as each agent, in two terminals. The setup wrote one
   command for each agent, so you type one word:

   ```sh
   bob
   alice
   ```

   Each starts Claude Code with its address as the session's name, so you can
   tell who you are talking to (for a session already open, type
   `/rename acme.demo.alice`), and says on its first turn whether mail wakes
   it when idle. That wake needs Claude Code to load mash's channel, which
   depends on your account: see [wake an idle session](../install.md#3-optional-wake-an-idle-session). Without it, mail
   arrives at the session's next prompt or tool call.
   Claude Code asks whether to trust the folder; choose yes.

4. In Bob's session, say: "Send alice a note: lunch at noon?"
   In Alice's, say: "Do I have mail?" She sees Bob's note, marked verified, after the setup's own test note.

Mail waits. Close Alice's session, send again from Bob's, reopen hers, and
it is there.

- Something wrong: `mash doctor`, then [troubleshooting](../troubleshooting.md).
- The same thing, typed yourself: [by hand](by-hand.md).
- How mash, SAP and NATS fit together: [the stack](stack.md).
- Remove everything: [uninstall](../install.md#uninstall).
