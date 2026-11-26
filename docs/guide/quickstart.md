# Quick start

mash gives each Claude Code session an inbox. Here two sessions on one
machine mail each other. You need Debian or macOS (tested) or Ubuntu (expected to work; see
[compatibility](../compatibility.md)), git and Claude Code 2.1.312 or newer. You need to be signed in to Claude Code. Everything
is installed in your home folder, with no sudo.
No Claude Code session handy? [Install the programs yourself](../../README.md#install-the-programs-yourself),
then [by hand](by-hand.md).

1. Add the setup skill:

   ```sh
   claude plugin marketplace add bogworks/mash
   claude plugin install mash-setup@mash
   ```

2. In a Claude Code session, say:

   > Set mash up on this machine, with two agents: acme.demo.alice and acme.demo.bob.

   It first asks two things. Pick **demo** (it chooses names and folders and
   asks for one yes). Say whether your Claude account is personal or
   Team/Enterprise (type `/status` to see). It then shows its plan and waits
   for your yes.

3. Start a session as each agent, in two terminals. The setup wrote one
   command for each agent, so you type one word:

   ```sh
   bob
   alice
   ```

   They are small files in `~/.local/bin`. If `bob` is not found, run
   `echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc` (zsh: `~/.zshrc`),
   then open a new terminal
   ([by hand, step 2](by-hand.md#2-install-mash-and-mashctl)).
   Start each in its own folder, for example `mkdir -p ~/bob && cd ~/bob && bob`.

   Each starts Claude Code with its address as the session's name, so you can
   tell who you are talking to (for a session already open, type
   `/rename acme.demo.alice`), and says on its first turn whether mail wakes
   it when idle. That wake needs Claude Code to load mash's channel, which
   depends on your account: see [wake an idle session](../install.md#3-optional-wake-an-idle-session). Without it, mail
   arrives at the session's next prompt or tool call.
   Claude Code asks whether to trust the folder; choose yes.

4. In Bob's session, say: "Send alice a note: lunch at noon?"
   In Alice's, say: "Do I have mail?" She sees two notes from Bob, both verified: the setup's test note, then yours.

Mail waits. Close Alice's session, send again from Bob's, reopen hers, and
it is there.

After a reboot, ask Claude: "start mash's server again".

- Something wrong: `mash doctor --endpoint acme.demo.alice`, then [troubleshooting](../troubleshooting.md).
- The same thing, typed yourself: [by hand](by-hand.md).
- How mash, SAP and NATS fit together: [the stack](stack.md).
- Remove everything: [uninstall](../install.md#uninstall).
