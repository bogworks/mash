# mash demo, re-take 2 (2026-11-22): recording note

mash-demo-retake2.mp4: 2:22, 1920x1080, no audio. Real recordings only (asciinema, Claude Code 2.1.330, mash v0.5.6, Ubuntu 24.04 container, no sudo), recorded by x-ops1. Same scenes and editing method as the first cut and the first re-take. Waits are cut or sped up (the first build also drew a caption for each; the current build draws none, see ../README.md).

## What is different from the first cut and re-take 1
- **Personal account.** Logged in as Randy's personal account (approved by Randy, planner #2827), in a fresh container, token never on screen; container, token file and login state destroyed afterwards (x-ops1 reported the teardown to mash-lead).
- **Skill from main with #1232 and #1237.** Setup now asks three on-screen questions: setup mode (Demo), account (Personal with wake, i.e. the development flag), and "No gh: continue". Scene 2 shows them.
- **The launch is the skill's own.** Bob and Alice are started by typing `bob` and `alice`: each is a one-line launcher the skill wrote, `exec claude --name acme.demo.bob "$@" --dangerously-load-development-channels plugin:mash@mash`.
- **The dev-flag warning screen is shown** at the start of both sessions ("WARNING: Loading development channels ... 1. I am using this for local development / 2. Exit"); choice 1 is confirmed. Text in warning-screen.txt on S3.
- **The wake works and is on screen.** The setup's test note wakes the idle Alice ("← mash: 1 message from acme.demo.bob (task)"), and Bob's "lunch at noon?" wakes her again ("(question)"). Bob's second note (#3, fyi) waits for her next prompt, shown in scene 4b, as designed.
- **Folder trust preset** as in re-take 1 (`projects["/home/demo"].hasTrustDialogAccepted = true`); no trust prompt.

## Honest items
- **Weekly-limit banner.** Each session briefly shows Claude Code's own line "You've used 91% of your weekly limit · resets Nov 24" (personal account usage). It is in the raw casts; I did not edit it out. Decide whether to re-cut around it before anyone outside sees this.
- A failed first run happened and is kept: the launchers written by #1232 put the prompt after the variadic flag, so Claude Code rejected them ("entries must be tagged"). Fixed by #1237. Those casts, the pre-fix setup and the "already set up" re-run are in `../raw/failed/`. The video shows only the fixed run.
- Alice sees "lunch at noon?" twice (the setup's test note and Bob's), and Claude says it is the same question sent twice. Not a duplicate delivery.
- Bob's send did not fail this time (no "1 failed").
- Setup on this run asked questions on screen, not a typed sentence; the only typed line is the first request.
- Dim "suggested prompt" text in an input box is Claude Code's own. "Fable 5.2 is here" shows in the header.
- Scenes 3 and 4a text is small (14 px) as a thumbnail. Scene 4b is a separate recording shown after 4a.
- The closing card does not mention the dev flag; this file is the note. The first turn in each session ("a channel was requested") is the skill's own prompt.

## Files
mash-demo-retake2.mp4; raw casts, times.txt, warning-screen.txt and launchers-shown-fixed.txt in ../raw/ (x-ops1's upload).
