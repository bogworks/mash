# mash v0.5.5 walk-through (internal)

Files: `drive.sh` (the driver; `bash drive.sh --narrate`, plain report mode without it), `steps.md` (narration, read by the driver), `v055-walkthrough.tape` (the VHS tape).

## Pinned tools

- vhs: vhs version 0.12.1
- ttyd: ttyd version 1.7.7
- ffmpeg: ffmpeg version 9.0.2 Copyright (c) 2000-2026 the FFmpeg developers

Shown versions: mash = v0.5.5 release binary checked against SHA256SUMS; mashctl = `go install ...@v0.5.5`; nats-server 2.15.1. Release commit e389eaf807279b58e63b05578a779ef672d93a36.

## Run it

First run `sh demos/v055-container/demo-0.5.5.sh up` once (then `down`) so the verified release binary and helper builds exist in `demos/v055-container/`; the driver reuses them (override the directory with `DEMO_SRC`). Then, from the repository root:

    vhs demos/v055-walkthrough/v055-walkthrough.tape

The render goes to `demos/v055-walkthrough/out/` and is NOT committed (`demos/.gitignore` ignores `out/`, `build/` and video files). The 2026-11-18 render is published through the range object store, not in git.

## What it runs

Everything runs inside one podman container on a host with rootless podman (image `mash-video-0.5.5:1`, container `mash-video-055`, label `mash-video=0.5.5`). The servers listen on 24222 and 24223 in the container's own network namespace, never on the host and never on 4222. Temporary files go under `demos/v055-walkthrough/build/`. The driver removes its container and image by exact name when it finishes.
