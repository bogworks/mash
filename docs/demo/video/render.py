# RULE: no text about how the video was made may be drawn onto a frame.
# Time compression (cutting idle gaps, speeding up output) is silent: the viewer
# sees terminal content, scene tags, pane labels and cards, never "sped up" or "idle" notes.
# Every string drawn that is not terminal content is written to drawn-text.json; check.py
# compares that list with edit.json.
#
# Usage: python3 render.py --inputs DIR --edit edit.json --out DIR
# Writes OUT/mash-demo.mp4, OUT/drawn-text.json and OUT/durations.json. Scratch parts go to a
# temp dir that is removed on exit. Needs ffmpeg on PATH, python3-pyte, python3-pil, fonts-dejavu-core.
import re, subprocess, sys, os, json, argparse, tempfile, shutil
import pyte
from PIL import Image, ImageDraw, ImageFont

FF = 'ffmpeg'
FONTS = '/usr/share/fonts/truetype/dejavu/'
DRAWN = []   # every non-terminal string drawn, in drawing order

# ---- casts ----
SAN = re.compile(r'\x1b\[[<>=][0-9;]*[A-Za-z]|\x1b\[\?u|\x1b\[[0-9;]*q|\x1b\[c')
def load(p):
    L = open(p).read().splitlines()
    h = json.loads(L[0]); ev = [json.loads(x) for x in L[1:]]
    return h, [[e[0], 'o', SAN.sub('', e[2])] for e in ev if e[1] == 'o']

# ---- time compression ----
KEY_CLAUDE = re.compile(r'\x1b\[\d+A[^\x1b\r\n]\x1b\[7m ')
def is_key(text):
    return (len(text)==1 and text.isprintable()) or bool(KEY_CLAUDE.search(text)) or bool(re.search(r'\x1b\[\d+A\x1b\[38;5;153m/', text))

def merge(srcs, t0, t1):
    """srcs: {name: events}. returns (pre, ev): pre = events before t0 per src (fed instantly); ev = merged events in [t0,t1]"""
    pre={n:[] for n in srcs}; ev=[]
    for n,evs in srcs.items():
        for e in evs:
            if e[0]<t0: pre[n].append(e)
            elif e[0]<=t1: ev.append((e[0],n,e[2]))
    ev.sort(key=lambda x:x[0])
    return pre, ev

def retime(ev, t0, speed=8.0, type_speed=1.0, hold=2.0, idle=0.8, thresh=1.2,
           overrides=None, profile=None):
    """pure time compression. returns out=[(t_out, src, text)], total
    profile: list of (ta,tb,speed) overriding speed for events in orig time range.
    overrides: {orig_gap_start_rounded: hold} for gap before a non-typing event."""
    overrides=overrides or {}
    out=[]; cur=0.0; prev=t0; prevkey=False
    for (t,n,x) in ev:
        dt=t-prev; k=is_key(x)
        f=speed
        if profile:
            for a,b,s in profile:
                if a<=t<b: f=s
        if dt>thresh:
            if k and not prevkey: new=hold
            else: new=idle
            for ka,kv in overrides.items():
                if abs(prev-ka)<0.6 or (ka<=prev<ka+0.6): new=kv
        else:
            sp=type_speed if k else f
            new=dt/sp
        cur+=new
        out.append((cur,n,x)); prev=t; prevkey=k
    return out, cur

# ---- drawing ----
class Font:
    def __init__(self,size):
        base=FONTS
        self.r=ImageFont.truetype(base+'DejaVuSansMono.ttf',size)
        self.b=ImageFont.truetype(base+'DejaVuSansMono-Bold.ttf',size)
        self.cw=self.r.getlength('M'); a,d=self.r.getmetrics(); self.lh=int(round((a+d)*1.0))+1; self.asc=a

PAL={'black':(0,0,0),'red':(205,49,49),'green':(13,188,121),'brown':(229,229,16),'blue':(36,114,200),
 'magenta':(188,63,188),'cyan':(17,168,205),'white':(229,229,229),
 'brightblack':(102,102,102),'brightred':(241,76,76),'brightgreen':(35,209,139),'brightbrown':(245,245,67),
 'brightblue':(59,142,234),'brightmagenta':(214,112,214),'brightcyan':(41,184,219),'brightwhite':(255,255,255)}
BG=(22,22,30); FG=(212,212,212)
def col(c,default):
    if c=='default': return default
    if c in PAL: return PAL[c]
    try: return tuple(int(c[i:i+2],16) for i in (0,2,4))
    except Exception: return default

def draw_screen(img, scr, font, x0, y0, show_cursor=True):
    d=ImageDraw.Draw(img)
    cw,lh=font.cw,font.lh
    lines=set()
    for y in range(scr.lines):
        row=scr.buffer[y]; txt=''
        # backgrounds
        x=0
        while x<scr.columns:
            ch=row[x]; bg=col(ch.bg,None); fg=col(ch.fg,FG)
            if ch.reverse: bg,fg=(fg if ch.fg!='default' else FG),(bg if bg else BG)
            if bg is not None:
                x2=x
                while x2<scr.columns:
                    c2=row[x2]; b2=col(c2.bg,None); f2=col(c2.fg,FG)
                    if c2.reverse: b2=(f2 if c2.fg!='default' else FG)
                    if b2!=bg: break
                    x2+=1
                d.rectangle([x0+x*cw,y0+y*lh,x0+x2*cw-1,y0+(y+1)*lh-1],fill=bg); x=x2
            else: x+=1
        for x in range(scr.columns):
            ch=row[x]; txt+=ch.data
            if ch.data.strip()=='' : continue
            fg=col(ch.fg,FG); 
            if ch.reverse:
                fg=col(ch.bg,BG) if ch.bg!='default' else BG
            f=font.b if ch.bold else font.r
            gx,gy=x0+x*cw,y0+y*lh
            if ch.data=='⎿':
                lw=max(1,int(lh/16)); mx=gx+cw*0.35; my=gy+lh*0.55
                d.line([(mx,gy+lh*0.12),(mx,my),(gx+cw*0.95,my)],fill=fg,width=lw); continue
            if ch.data=='⏵':
                d.polygon([(gx+cw*0.2,gy+lh*0.25),(gx+cw*0.2,gy+lh*0.8),(gx+cw*0.85,gy+lh*0.525)],fill=fg); continue
            d.text((x0+x*cw,y0+y*lh),ch.data,font=f,fill=fg)
        lines.add(txt.rstrip())
    if show_cursor and not scr.cursor.hidden:
        cx,cy=scr.cursor.x,scr.cursor.y
        d.rectangle([x0+cx*cw,y0+cy*lh,x0+(cx+1)*cw-1,y0+(cy+1)*lh-1],fill=(200,200,200))
    return lines

def new_screen(): return pyte.Screen(110,30)

def render_scene(outpath, srcs, panes, t0, t1, rt, total, tail, tag, size, layout, W, H, fps):
    """panes: list of (src, label). layout: function giving (x0,y0) for pane i, plus font."""
    DRAWN.append(tag)
    DRAWN.extend(l for _n,l in panes if l)
    pre,_=merge(srcs,t0,t1)
    scr={}; st={}
    for n,_l in panes:
        scr[n]=new_screen(); st[n]=pyte.Stream(scr[n])
        for e in pre[n]: st[n].feed(e[2])
    font=Font(size)
    ff=subprocess.Popen([FF,'-y','-loglevel','error','-f','rawvideo','-pix_fmt','rgb24','-s',f'{W}x{H}','-r',str(fps),'-i','-',
        '-c:v','libx264','-pix_fmt','yuv420p','-crf','16','-preset','veryfast','-an',outpath],stdin=subprocess.PIPE)
    dur=total+tail; nframes=int(round(dur*fps))
    i=0; cache=None
    tagfont=ImageFont.truetype(FONTS+'DejaVuSans.ttf',26)
    labfont=ImageFont.truetype(FONTS+'DejaVuSans-Bold.ttf',40)
    for k in range(nframes):
        tf=k/fps; dirty=False
        while i<len(rt) and rt[i][0]<=tf:
            _,n,x=rt[i]; st[n].feed(x); i+=1; dirty=True
        if dirty or cache is None:
            img=Image.new('RGB',(W,H),BG); d=ImageDraw.Draw(img)
            d.text((24,14),tag,font=tagfont,fill=(140,140,160))
            for idx,(n,l) in enumerate(panes):
                x0,y0=layout(idx,font)
                if l:
                    d.text((x0+font.cw*55,y0-52),l,font=labfont,fill=(230,230,240),anchor='mt')
                draw_screen(img,scr[n],font,x0,y0,show_cursor=True)
            cache=img.tobytes()
        ff.stdin.write(cache)
    ff.stdin.close(); ff.wait()
    return dur

# ---- edit.json ----
CARD_KEYS = {'card', 'seconds', 'lines'}
LINE_KEYS = {'text', 'size', 'style', 'color'}
SCENE_KEYS = {'scene', 'tag', 'layout', 'font_size', 'tail', 'sources', 'panes', 't0', 't1',
              'speed', 'type_speed', 'hold', 'idle'}
PANE_KEYS = {'source', 'label'}
def strict(obj, keys, what):
    extra = set(obj) - keys
    if extra: sys.exit(f'edit.json: {what} has unknown field(s) {sorted(extra)}; the format has no place for them')

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--inputs', required=True); ap.add_argument('--edit', required=True); ap.add_argument('--out', required=True)
    a = ap.parse_args()
    edit = json.load(open(a.edit))
    strict(edit, {'output', 'sequence'}, 'top level'); strict(edit['output'], {'width', 'height', 'fps'}, 'output')
    W, H, fps = edit['output']['width'], edit['output']['height'], edit['output']['fps']
    os.makedirs(a.out, exist_ok=True)
    T = tempfile.mkdtemp(prefix='demo-render-') + '/'
    try:
        S = {}
        def src(n):
            if n not in S: S[n] = load(os.path.join(a.inputs, n + '.cast'))[1]
            return S[n]
        durs = {}; order = []

        def single_layout(idx, font):
            return ((W-110*font.cw)/2, 56)
        def dual_layout(idx, font):
            return (W/4+W/2*idx-110*font.cw/2, 170+52)
        layouts = {'single': single_layout, 'dual': dual_layout}

        def scene(s):
            strict(s, SCENE_KEYS, 'scene ' + s['scene'])
            for p in s['panes']: strict(p, PANE_KEYS, 'pane of ' + s['scene'])
            sub = {k: src(k) for k in s['sources']}
            pre, ev = merge(sub, s['t0'], s['t1'])
            rt, tot = retime(ev, s['t0'], speed=s['speed'], type_speed=s['type_speed'], hold=s['hold'], idle=s['idle'])
            panes = [(p['source'], p['label']) for p in s['panes']]
            d = render_scene(T + s['scene'] + '.mp4', sub, panes, s['t0'], s['t1'], rt, tot, s['tail'], s['tag'],
                             s['font_size'], layouts[s['layout']], W, H, fps)
            durs[s['scene']] = d
            print(s['scene'], round(d, 2), 'compressed to', round(tot, 2), '+tail', s['tail'], flush=True)

        def card(c):
            strict(c, CARD_KEYS, 'card ' + c['card'])
            name, lines, dur = c['card'], c['lines'], c['seconds']
            img = Image.new('RGB', (W, H), (14, 14, 22)); d = ImageDraw.Draw(img)
            y = H/2 - sum(l['size'] for l in lines)/2 - 20*(len(lines)-1)/2
            for l in lines:
                strict(l, LINE_KEYS, 'line of card ' + name)
                size = l['size']
                f = ImageFont.truetype(FONTS + ('DejaVuSans-Bold.ttf' if l['style'] == 'bold' else 'DejaVuSans.ttf'), size)
                DRAWN.append(l['text'])
                d.text((W/2, y+size/2), l['text'], font=f, fill=tuple(l['color']), anchor='mm'); y += size + 20
            png = T + name + '.png'; img.save(png)
            fade = 0.4
            subprocess.run([FF, '-y', '-loglevel', 'error', '-loop', '1', '-framerate', str(fps), '-i', png, '-t', str(dur),
              '-vf', f'fade=t=in:st=0:d={fade},fade=t=out:st={dur-fade}:d={fade},format=yuv420p', '-c:v', 'libx264', '-crf', '16',
              '-preset', 'veryfast', '-r', str(fps), '-an', T + name + '.mp4'], check=True)
            durs[name] = dur

        for item in edit['sequence']:
            if 'card' in item: card(item); order.append(item['card'])
            else: scene(item); order.append(item['scene'])
        open(T + 'list.txt', 'w').write(''.join(f"file '{T}{n}.mp4'\n" for n in order))
        subprocess.run([FF, '-y', '-loglevel', 'error', '-f', 'concat', '-safe', '0', '-i', T + 'list.txt', '-c:v', 'libx264',
            '-pix_fmt', 'yuv420p', '-crf', '17', '-preset', 'medium', '-r', str(fps), '-an',
            os.path.join(a.out, 'mash-demo.mp4')], check=True)
        json.dump({n: durs[n] for n in order}, open(os.path.join(a.out, 'durations.json'), 'w'), indent=1)
        json.dump(DRAWN, open(os.path.join(a.out, 'drawn-text.json'), 'w'), indent=1, ensure_ascii=False)
        print({n: round(durs[n], 2) for n in order}, round(sum(durs.values()), 2))
    finally:
        shutil.rmtree(T)

main()
