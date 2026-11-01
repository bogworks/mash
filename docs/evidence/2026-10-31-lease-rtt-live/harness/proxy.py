#!/usr/bin/env python3
# TCP delay proxy: proxy.py PORTFILE UPSTREAM_PORT DELAY_MS_EACH_WAY
import asyncio, sys, time
pf, up, delay = sys.argv[1], int(sys.argv[2]), float(sys.argv[3]) / 1000
logf = open(sys.argv[4], 'a', buffering=1) if len(sys.argv) > 4 else None
import re
async def pipe(r, w, up_dir=False):
    q = asyncio.Queue()
    async def rd():
        try:
            while True:
                d = await r.read(65536)
                if logf and up_dir and d:
                    for m in re.finditer(rb'(?:^|\r\n)(H?PUB|CONNECT|SUB) (\S+)', d):
                        logf.write('%.3f %s %s\n' % (time.time(), m.group(1).decode(), m.group(2)[:60].decode(errors='replace') if m.group(1)!=b'CONNECT' else ''))
                await q.put((time.monotonic() + delay, d))
                if not d: return
        except Exception:
            await q.put((0, b""))
    async def wr():
        try:
            while True:
                t, d = await q.get()
                s = t - time.monotonic()
                if s > 0: await asyncio.sleep(s)
                if not d: break
                w.write(d); await w.drain()
        except Exception: pass
        try: w.close()
        except Exception: pass
    await asyncio.gather(rd(), wr())
async def h(cr, cw):
    if logf: logf.write('%.3f ACCEPT\n' % time.time())
    sr, sw = await asyncio.open_connection('127.0.0.1', up)
    await asyncio.gather(pipe(cr, sw, True), pipe(sr, cw))
async def main():
    s = await asyncio.start_server(h, '127.0.0.1', 0)
    open(pf, 'w').write(str(s.sockets[0].getsockname()[1]))
    async with s: await s.serve_forever()
asyncio.run(main())
