#!/bin/bash
. "${LRL_DIR:?set LRL_DIR}/lib.sh"
TL=$D/timeline.log
log(){ echo "$(ts) [B2] $*" | tee -a $TL; }
H=$R/home-sender; mkdir -p $H/.config/mash
P08=nats://127.0.0.1:$(cat $R/px-t08.port)
echo "{\"url\":\"$P08\",\"owner\":\"o\",\"open_timeout\":\"60s\"}" > $H/.config/mash/config.json
run(){ local label=$1; shift; local s=$(date +%s.%N)
  out=$(env -i PATH=/usr/bin:/bin HOME=$H XDG_STATE_HOME=$H/state MASH_NAME=sender MASH_OWNER=o MASH_CREDS=$(creds_of sender) "$@" 2>&1 | head -c 300 | tr '\n' '|')
  local e=$(date +%s.%N); log "  $label: elapsed=$(python3 -c "print(round($e-$s,2))")s | $out"; }
log "== MASH_CALL_TIMEOUT on a command whose requests run after open: send, proxy RTT 1.6s, open_timeout 60s (config.json)"
run "default call_timeout (15s) send e07" $M send e07 "calltimeout-control"
run "MASH_CALL_TIMEOUT=1s send e07" MASH_CALL_TIMEOUT=1s $M send e07 "calltimeout-1s"
run "MASH_CALL_TIMEOUT=3s send e07" MASH_CALL_TIMEOUT=3s $M send e07 "calltimeout-3s"
run "MASH_CALL_TIMEOUT=500ms inbox" MASH_CALL_TIMEOUT=500ms $M inbox
run "MASH_CALL_TIMEOUT=500ms read 1" MASH_CALL_TIMEOUT=500ms $M read 1
rm -f $H/.config/mash/config.json
