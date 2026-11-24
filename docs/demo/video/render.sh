#!/bin/sh
# Renders docs/demo/mash-demo.mp4 from docs/demo/video/inputs and edit.json, in a throwaway container,
# and writes it only if check.py passes. Run from anywhere: docs/demo/video/render.sh
# Writes: docs/demo/mash-demo.mp4 and docs/demo/video/render-versions.txt. Nothing else.
set -eu
ROOT=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
VIDEO="$ROOT/docs/demo/video"
if command -v podman >/dev/null 2>&1; then RT=podman; elif command -v docker >/dev/null 2>&1; then RT=docker; else echo "need podman or docker" >&2; exit 2; fi
IMAGE=docker.io/library/debian:trixie
OUT=$(mktemp -d)
trap 'rm -rf "$OUT"' EXIT
NAME=demo-render-$$

# The repo's video folder is mounted read-only; the only writable mount is the temp output dir.
"$RT" run --rm --name "$NAME" -v "$VIDEO:/video:ro" -v "$OUT:/out:rw" -e PYTHONDONTWRITEBYTECODE=1 "$IMAGE" sh -eu -c '
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq >/dev/null
  apt-get install -y -qq --no-install-recommends ffmpeg python3 python3-pyte python3-pil fonts-dejavu-core >/dev/null
  python3 /video/render.py --inputs /video/inputs --edit /video/edit.json --out /out
  python3 /video/check.py --video /out/mash-demo.mp4 --drawn /out/drawn-text.json --durations /out/durations.json --edit /video/edit.json
  python3 /video/check.py --self-test
  {
    ffmpeg -version | head -1
    ffmpeg -version | grep -q -- --enable-libx264 && echo "x264: built into ffmpeg (--enable-libx264)"
    dpkg-query -W -f="\${Package} \${Version}\n" "libx264-*"
    python3 --version
    dpkg-query -W -f="pyte \${Version} (python3-pyte)\n" python3-pyte
    python3 -c "import PIL; print(\"Pillow\", PIL.__version__)"
    dpkg-query -W -f="fonts-dejavu-core \${Version}\n" fonts-dejavu-core
  } > /out/render-versions.txt
'
DIGEST=$("$RT" image inspect --format '{{index .RepoDigests 0}}' "$IMAGE" 2>/dev/null || "$RT" image inspect --format '{{.Id}}' "$IMAGE")
echo "image: $DIGEST" >> "$OUT/render-versions.txt"
# the check passed (set -e would have stopped us): publish
cp "$OUT/render-versions.txt" "$VIDEO/render-versions.txt"
cp "$OUT/mash-demo.mp4" "$ROOT/docs/demo/mash-demo.mp4"
echo "wrote $ROOT/docs/demo/mash-demo.mp4"
