# The demo video: how it is made

[`../mash-demo.mp4`](../mash-demo.mp4) is built from recordings of real terminal sessions by one command. This folder holds
everything the build needs, so anyone can rebuild the video, or change it, from a clean checkout.

## Render it

```sh
docs/demo/video/render.sh
```

Run it from anywhere inside the repository. It needs `podman` (or `docker`) and network access for the image and the
packages. It does this:

1. Starts a throwaway `debian:trixie` container (`--rm`). The `docs/demo/video` folder is mounted read-only; the only
   writable mount is a temporary output directory, removed when the script exits.
2. Installs `ffmpeg`, `python3-pyte`, `python3-pil` and `fonts-dejavu-core`.
3. Runs `render.py`, then `check.py`, then `check.py --self-test`.
4. Only if every step passed, writes `docs/demo/mash-demo.mp4` and `docs/demo/video/render-versions.txt` (ffmpeg, x264,
   Python, pyte, Pillow, the font package and the image digest). It writes nothing else.

The same inputs and the same image give the same video.

## What is in this folder

| File | What it is |
| --- | --- |
| `inputs/setup.cast`, `bob.cast`, `alice.cast`, `alice2.cast` | The four recordings (asciinema v2 casts). Never edited. |
| `inputs/times.txt`, `warning-screen.txt`, `launchers-shown-fixed.txt` | Notes from the recording: wall-clock starts, the development-channel warning screen, the launchers the setup skill wrote. |
| `inputs/recording-notes.md` | The recording note of re-take 2: how the casts were made, in what order, with which versions. |
| `edit.json` | Every edit decision, as data. |
| `render.py` | Reads `edit.json` and the casts, draws every frame, writes the mp4. |
| `check.py` | Checks the result (see below). |
| `render.sh` | The one command. |
| `render-versions.txt` | The tool versions and image digest of the last render. |

## The recordings

The casts were recorded by x-ops1 on 2026-11-22 in a fresh Ubuntu 24.04 container, with Claude Code 2.1.330 and mash
v0.5.6, on a personal Claude account. `setup.cast` is the setup skill installing mash from one sentence. `bob.cast` and
`alice.cast` are two sessions run side by side. `alice2.cast` is Alice's session opened again after she closed it.

How a recording is made, and the exact setup, are in [inputs/recording-notes.md](inputs/recording-notes.md). A new
recording means new casts in `inputs/` and a new `edit.json`. Nothing else needs to change.

## The edit

`edit.json` is the only place edit decisions live. It lists, in order, the cards and the scenes:

- A card has its lines (text, size, bold or regular, colour) and how many seconds it shows.
- A scene has a name, the tag drawn top-left, the casts it plays, a label for each pane, the cut-in and cut-out times
  in cast seconds (`t0`, `t1`), the font size, the layout (`single` or `dual`), the seconds held on the last frame
  (`tail`), and the time compression: `speed` (output text), `type_speed` (typing), `hold` (a wait before typing) and
  `idle` (any other wait).

Time compression is silent. The picture shows the terminal, the scene tag, the pane labels and the cards, and no text
about how the video was made, such as "sped up" or "idle cut". `edit.json` has no field for a caption or a note, and
`render.py` refuses a field it does not know. Edit decisions live in `edit.json` and never as text in the picture.

## The check

`check.py` fails the render, and prints the failing timestamps, if any of these is false:

- The strings the renderer drew (`drawn-text.json`) are exactly the tags, pane labels and card lines in `edit.json`.
- In the video sampled at 2 frames a second, no pixel near the old caption colours lies in the strip where the captions
  were, and no solid box of the caption background colour is anywhere in a frame.
- The video is 1920x1080 and as long as its parts, within 0.1 s.

`check.py --self-test` builds frames with a caption-coloured box and a drawn-text list with a caption in it, and shows the
checks fail on them.

## History

The 0.5.6 video was built by a script (re-take 2 `build.py`) that lived only in a lane's scratch directory, with
the casts beside it. Its renderer drew captions on the picture ("idle N s cut to X s" and "sped up Nx"), and a note in
`inputs/recording-notes.md` still says so. This folder replaces that script. The new video has the same scenes
and the same time compression, with no captions.
