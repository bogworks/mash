# Checks the rendered demo video against edit.json. Exit 1 and print what failed if any check fails.
#   (a) audit:    drawn-text.json holds exactly the texts edit.json derives (scene tags, pane labels, card lines), nothing else
#   (b) frames:   at 2 fps, no pixel near the old caption colours in the caption strip, and no solid box (15x15 px or more) of
#                 the caption background colour anywhere (scattered anti-aliased text pixels are not a box)
#   (c) duration: the video is 1920x1080 and as long as the sum of the part durations (within 0.1 s)
# Usage: python3 check.py --video V.mp4 --drawn drawn-text.json --durations durations.json --edit edit.json
#        python3 check.py --self-test      (shows that (a) and (b) fail on a bad input)
import argparse, json, os, shutil, subprocess, sys, tempfile
from PIL import Image, ImageChops, ImageDraw, ImageFilter

CAPTION_BG = (60, 40, 10)
CAPTION_FG = (255, 200, 90)
TOL = 14
REGION = (1100, 1018, 1905, 1067)   # x 1100..1904, y 1018..1066
BOX = 15   # a solid square of this many px per side (225 px, a box larger than 200 px) counts as a box

def derive(edit):
    out = []
    for it in edit['sequence']:
        if 'card' in it: out.extend(l['text'] for l in it['lines'])
        else:
            out.append(it['tag']); out.extend(p['label'] for p in it['panes'] if p['label'])
    return out

def audit(edit, drawn):
    want, got = sorted(derive(edit)), sorted(drawn)
    errs = []
    for t in got:
        if t not in want: errs.append(f'drawn text not in edit.json: {t!r}')
    for t in want:
        if t not in got: errs.append(f'edit.json text not drawn: {t!r}')
    if not errs and want != got: errs.append('drawn texts and edit.json texts differ in count')
    return errs

def near_mask(img, color, box=None):
    im = img.convert('RGB')
    if box: im = im.crop(box)
    mask = None
    for band, c in zip(im.split(), color):
        m = band.point(lambda v, c=c: 255 if abs(v - c) <= TOL else 0)
        mask = m if mask is None else ImageChops.darker(mask, m)
    return mask

def near_count(img, color, box=None):
    return near_mask(img, color, box).histogram()[255]

def frame_errors(img):
    errs = []
    for name, c in (('caption background', CAPTION_BG), ('caption text', CAPTION_FG)):
        n = near_count(img, c, REGION)
        if n: errs.append(f'{n} px near {name} {c} in caption strip')
    # Terminal text anti-aliasing leaves scattered pixels of this colour (dim orange); a caption box is a solid block.
    # Erode the mask: only a solid BOXxBOX square of the colour survives.
    solid = near_mask(img, CAPTION_BG).filter(ImageFilter.MinFilter(BOX))
    n = solid.histogram()[255]
    if n: errs.append(f'solid box of caption background {CAPTION_BG} in frame, bbox {solid.getbbox()}')
    return errs

def probe(video):
    r = subprocess.run(['ffprobe', '-v', 'error', '-select_streams', 'v:0', '-show_entries', 'stream=width,height:format=duration',
                        '-of', 'json', video], check=True, capture_output=True, text=True)
    j = json.loads(r.stdout)
    return j['streams'][0]['width'], j['streams'][0]['height'], float(j['format']['duration'])

def check(a):
    fails = []
    edit = json.load(open(a.edit)); drawn = json.load(open(a.drawn)); durs = json.load(open(a.durations))
    for e in audit(edit, drawn): fails.append('audit: ' + e)
    w, h, dur = probe(a.video)
    want = sum(durs.values())
    if (w, h) != (edit['output']['width'], edit['output']['height']) or (w, h) != (1920, 1080): fails.append(f'size: {w}x{h}, want 1920x1080')
    if abs(dur - want) > 0.1: fails.append(f'duration: {dur:.3f} s, parts sum to {want:.3f} s')
    tmp = tempfile.mkdtemp(prefix='demo-check-')
    try:
        subprocess.run(['ffmpeg', '-y', '-loglevel', 'error', '-i', a.video, '-vf', 'fps=2', os.path.join(tmp, 'f%05d.png')], check=True)
        names = sorted(f for f in os.listdir(tmp) if f.endswith('.png'))
        for i, f in enumerate(names):
            with Image.open(os.path.join(tmp, f)) as img:
                for e in frame_errors(img): fails.append(f'frames: t={i/2:.1f}s: {e}')
        print(f'checked {len(names)} frames at 2 fps, {w}x{h}, {dur:.3f} s (parts {want:.3f} s)')
    finally:
        shutil.rmtree(tmp)
    return fails

def self_test():
    ok = True
    # a clean frame passes (b)
    clean = Image.new('RGB', (1920, 1080), (22, 22, 30))
    if frame_errors(clean): ok = False; print('SELF-TEST BROKEN: clean frame fails')
    # a caption-coloured box in the strip fails (b)
    bad = clean.copy(); d = ImageDraw.Draw(bad)
    d.rounded_rectangle([1150, 1020, 1890, 1064], radius=10, fill=CAPTION_BG)
    d.text((1170, 1030), 'sped up 8x', fill=CAPTION_FG)
    errs = frame_errors(bad)
    print('(b) bad frame, caption strip:'); [print('   FAIL', e) for e in errs]
    if not errs: ok = False; print('SELF-TEST BROKEN: bad frame passed')
    # a big box elsewhere fails (b)
    bad2 = clean.copy(); ImageDraw.Draw(bad2).rounded_rectangle([100, 500, 500, 560], radius=10, fill=CAPTION_BG)
    errs = frame_errors(bad2)
    print('(b) bad frame, box elsewhere:'); [print('   FAIL', e) for e in errs]
    if not errs: ok = False; print('SELF-TEST BROKEN: box elsewhere passed')
    # drawn text with a caption fails (a); matching text passes
    edit = {'sequence': [{'card': 'c', 'lines': [{'text': 'mash'}]},
                         {'scene': 's', 'tag': '1 · install', 'panes': [{'label': ''}, {'label': 'bob'}]}]}
    if audit(edit, ['mash', '1 · install', 'bob']): ok = False; print('SELF-TEST BROKEN: matching drawn text fails')
    errs = audit(edit, ['mash', '1 · install', 'bob', 'idle 12 s cut to 2 s'])
    print('(a) drawn-text.json with a caption:'); [print('   FAIL', e) for e in errs]
    if not errs: ok = False; print('SELF-TEST BROKEN: caption in drawn text passed')
    print('self-test: checks (a) and (b) fail on bad input and pass on clean input' if ok else 'self-test: BROKEN')
    return 0 if ok else 1

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--video'); ap.add_argument('--drawn'); ap.add_argument('--durations'); ap.add_argument('--edit')
    ap.add_argument('--self-test', action='store_true')
    a = ap.parse_args()
    if a.self_test: sys.exit(self_test())
    if not (a.video and a.drawn and a.durations and a.edit): ap.error('need --video --drawn --durations --edit')
    fails = check(a)
    for f in fails: print('FAIL', f)
    print('check FAILED' if fails else 'check passed')
    sys.exit(1 if fails else 0)

main()
