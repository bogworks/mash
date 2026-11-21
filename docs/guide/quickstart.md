# Quick start

mash gives each Claude Code session an inbox. Here two sessions on one
machine mail each other. You need Linux or macOS, git and Claude Code. Everything
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

3. Start a session as each agent, in two terminals:

   ```sh
   MASH_ENDPOINT=acme.demo.bob claude
   MASH_ENDPOINT=acme.demo.alice claude
   ```

   Claude Code asks whether to trust the folder; choose yes.

4. In Bob's session, say: "Send alice a note: lunch at noon?"
   In Alice's, say: "Do I have mail?" She sees Bob's note, marked verified, after the setup's own test note.

Mail waits. Close Alice's session, send again from Bob's, reopen hers, and
it is there.

- Something wrong: `mash doctor`, then [troubleshooting](../troubleshooting.md).
- The same thing, typed yourself: [by hand](by-hand.md).
- How mash, SAP and NATS fit together: [the stack](stack.md).
- Remove everything: [uninstall](../install.md#uninstall).
