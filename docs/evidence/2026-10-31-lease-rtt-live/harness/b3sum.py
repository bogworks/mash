import sys
# b3sum.py PXLOG SPAWN_EPOCH  -> prints connect, first pull, waves
spawn=float(sys.argv[2]); ev=[l.split(None,2) for l in open(sys.argv[1]) if l.strip()]
acc=[float(e[0]) for e in ev if e[1]=="ACCEPT"]
pubs=[(float(e[0]),e[2].strip() if len(e)>2 else "") for e in ev if e[1] in("PUB","HPUB")]
pull=[t for t,s in pubs if "CONSUMER.MSG.NEXT" in s]
if not acc or not pull: print("NO_PULL"); sys.exit()
a=acc[0]; p=pull[0]
cl=[]; 
for t,s in pubs:
    if t<=p+0.0001:
        if not cl or t-cl[-1][0]>0.06: cl.append([t,[s]])
        else: cl[-1][1].append(s)
print("tcp_accept=%.3f first_pull=%.3f waves_after_connect=%d" % (a-spawn, p-spawn, len(cl)))
print("PROXYWAVES " + " ; ".join("+%.2fs %s" % (t-a, ",".join(x[:34] for x in ss[:3])+("+%d" % (len(ss)-3) if len(ss)>3 else "")) for t,ss in cl))
