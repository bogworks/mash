# v0.5.0 walk-through demo

A scripted tour of mash v0.5.0's policy model, recorded as a terminal video. It is the first example of the demo-video
draft standard. `steps.md` is the text of each step (what you see, why it matters, the nearest `iptables` idea);
`drive.sh` builds mash from this repository into a throwaway directory, starts a throwaway NATS server on a local port,
runs every example with real output, and, with `--narrate`, prints the numbered steps a recording shows. `docs/` holds the
four list-set templates the examples sign. Nothing live is touched: every `mash`, `mashctl` and `nats` call runs with
all `MASH_*` variables unset and its own `HOME` and key directories inside the temp directory.

## Pins

| Tool | Version |
|---|---|
| vhs | 0.12.1 |
| ttyd | 1.7.7 |
| ffmpeg | 9.0.2 (any recent ffmpeg that VHS accepts) |

Other tools the driver needs: `git`, `go`, `jq`, `python3`, `nats`, `tar`.

## Run it

1. Reserve a free local port with portwarden (an MCP tool: `reserve_port` with an `owner` and `purpose`). Never use
   4222, which is the live server's port. Renew it while you work and release it afterwards (`release_port`).
2. The text report only, no video, from the repository root:

   ```sh
   bash demos/v050-walkthrough/drive.sh --port 20001
   ```

   The report goes to `demos/v050-walkthrough/out/v050-examples-report.md`.
3. The narration on the terminal, still without a recording:

   ```sh
   bash demos/v050-walkthrough/drive.sh --narrate --port 20001
   ```

4. The video:

   ```sh
   PORT=20001 vhs demos/v050-walkthrough/walkthrough.tape
   ```

   It writes an mp4 and a gif to `demos/v050-walkthrough/out/`. A full run takes up to about 30 minutes, most of it the
   build and the throwaway server.
5. `drive.sh --shell --port N` opens an interactive shell on the same throwaway server, for a person to try the commands.

`--repo DIR` and `--ref REF` choose what to build; the default is the checked-out `HEAD` of this repository. An example
that needs a command the build does not have prints `SKIPPED: needs <X>` and the run carries on.

## Renders are not committed

`out/` is ignored by git. Renders (mp4, gif, the report) go to the object store, never to git. Link the stored file from
the place that needs it.
