#!/bin/bash
# caseB3.sh LABEL BIN DELAY_MS NAME RUNS
. "${LRL_DIR:?set LRL_DIR}/lib.sh"
LABEL=$1; export BIN=$2; M=$BIN; DELAY=$3; X=$4; RUNS=$5
TL=$D/timeline.log
log(){ echo "$(ts) [B3] $*" | tee -a $TL; }
log "== $LABEL: $($M version | cut -c1-60), endpoints registered=$(adm list | grep -c 'live proof'), proxy $DELAY ms each way (RTT $((DELAY*2)) ms), endpoint $X, $RUNS runs"
for i in $(seq 0 $RUNS); do
  PU=$(bash $D/px.sh b3 $DELAY)
  rm -f $R/b3-$i.log $R/b3-$i.err $R/b3-$i.pid
  python3 $D/drv.py b3-$i $X $PU </dev/null >/dev/null 2>&1 &
  for j in $(seq 1 100); do [ -s $R/b3-$i.pid ] && break; sleep 0.05; done
  SP=$(head -1 $R/b3-$i.log | cut -d' ' -f1)
  for j in $(seq 1 200); do grep -q "CONSUMER.MSG.NEXT" $R/px-b3.log 2>/dev/null && break; sleep 0.1; done
  sleep 0.3
  r=$(python3 $D/b3sum.py $R/px-b3.log $SP)
  [ $i = 0 ] && tag="warmup" || tag="run$i"
  log "  $tag: $(echo "$r" | head -1)"
  [ $i = 1 ] && log "  $(echo "$r" | sed -n 2p | cut -c1-700)"
  kill $(cat $R/b3-$i.pid) 2>/dev/null; sleep 1.5
  kill $(cat $R/px-b3.pid) 2>/dev/null
done
