#!/bin/bash
# caseA.sh LABEL BIN : lease handoff after kill -9. Appends to $TL.
. "${LRL_DIR:?set LRL_DIR}/lib.sh"
LABEL=$1; export BIN=$2; M=$BIN
TL=${TL:-$D/timeline.log}
log(){ echo "$(ts) [$LABEL] $*" | tee -a $TL; }
ep(){ python3 -c "import time;print(f'{time.time():.3f}')"; }
log "binary: $($M version)"
rm -f $R/$LABEL-*.log $R/$LABEL-*.err $R/$LABEL-*.pid
# fresh endpoint per label so no old lease/inbox state: use name given by NAME
X=${NAME:-x}
log "== step 1: serve #1 for endpoint $X starts and takes the lease"
setsid python3 $D/drv.py $LABEL-s1 $X $URL </dev/null >/dev/null 2>&1 &
for i in $(seq 1 100); do mx $X $URL who --long 2>&1 | grep -q "^$X.*online" && break; sleep 0.1; done
sleep 1; PID1=$(cat $R/$LABEL-s1.pid)
log "serve#1 pid=$PID1; who --long: $(mx $X $URL who --long | grep "^$X")"
log "== step 2 (negative 1): serve #2 for $X while #1 is alive and renewing (35 s, spans a 30 s renewal)"
setsid python3 $D/drv.py $LABEL-s2 $X $URL </dev/null >/dev/null 2>&1 &
sleep 1; PID2=$(cat $R/$LABEL-s2.pid)
log "serve#2 pid=$PID2 started"
sleep 5
log "mail m-alive sent while both run: $(mx sender $URL send $X 'm-alive' 2>&1)"
sleep 30
log "serve#1 alive: $(kill -0 $PID1 2>&1 && echo yes)  serve#2 alive: $(kill -0 $PID2 2>&1 && echo yes)"
log "serve#1 events:"; sed 's/^/    /' $R/$LABEL-s1.log | cut -c1-260 | tee -a $TL
log "serve#2 events:"; sed 's/^/    /' $R/$LABEL-s2.log | cut -c1-260 | tee -a $TL
log "serve#2 stderr:"; sed 's/^/    /' $R/$LABEL-s2.err | cut -c1-260 | tee -a $TL
log "who --long: $(mx $X $URL who --long | grep "^$X")"
kill $PID2; sleep 1; log "serve#2 stopped (SIGTERM); serve#1 alive: $(kill -0 $PID1 2>&1 && echo yes)"
log "== step 3: kill -9 serve#1, immediately start serve#3 and send mail"
T0=$(ep); log "kill -9 $PID1 (epoch $T0)"; kill -9 $PID1
log "serve#1 gone: $(kill -0 $PID1 2>&1 || true)"
setsid python3 $D/drv.py $LABEL-s3 $X $URL </dev/null >/dev/null 2>&1 &
T1=$(ep); log "serve#3 spawned (epoch $T1, +$(python3 -c "print(round($T1-$T0,3))") s after kill)"
sleep 0.3; PID3=$(cat $R/$LABEL-s3.pid)
log "mail m-takeover sent: $(mx sender $URL send $X 'm-takeover' 2>&1)  (epoch $(ep))"
GOT=""
for i in $(seq 1 1100); do
  if grep -q seqs $R/$LABEL-s3.log 2>/dev/null; then GOT=1; break; fi; sleep 0.1; done
T2=$(grep seqs $R/$LABEL-s3.log | head -1 | cut -d' ' -f1)
log "serve#3 pid=$PID3 first CHANNEL (mail delivered): epoch ${T2:-NONE}"
[ -n "$T2" ] && log "RESULT: kill -9 -> mail delivered by new serve in $(python3 -c "print(round($T2-$T0,2))") s; serve#3 start -> mail $(python3 -c "print(round($T2-$T1,2))") s"
log "serve#3 events:"; sed 's/^/    /' $R/$LABEL-s3.log | cut -c1-260 | tee -a $TL
log "who --long: $(mx $X $URL who --long | grep "^$X")"
log "mail inbox of $X: $(mx $X $URL inbox --limit 5 2>&1 | cut -c1-200 | tr '\n' '|')"
kill $PID3 2>/dev/null; sleep 1
