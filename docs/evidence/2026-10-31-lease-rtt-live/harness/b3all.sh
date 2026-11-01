#!/bin/bash
. "${LRL_DIR:?set LRL_DIR}/lib.sh"
B=$M
set_n(){ # set_n N : register sender,x,probe,e01.. so that N endpoints are registered
  for i in $(seq 8 40); do n=$(printf 'e%02d' $i); idx=$((i+3)); 
    if [ $idx -le $1 ]; then adm provision $n --user $(user_of $n) --account $ACCT --note "live proof" >/dev/null 2>&1; else adm deprovision $n >/dev/null 2>&1; fi; done; }
for N in 10 40; do
  set_n $N
  echo "$(date -u +%H:%M:%S.%3N) [B3] ---- directory has $(adm list | grep -c 'live proof') registered endpoints (target $N)" | tee -a $D/timeline.log
  $D/caseB3.sh main-N$N-rtt0 $B 0 e05 3 | cut -c1-150
  $D/caseB3.sh main-N$N-rtt100 $B 50 e05 3 | cut -c1-1000
  BIN=$D/mash-pre121 $D/caseB3.sh pre121-N$N-rtt100 $D/mash-pre121 50 e05 3 | cut -c1-150
done
