#!/bin/bash
. /tmp/claude-1000/lrl/lib.sh
TL=$D/timeline.log
log(){ echo "$(ts) [B2] $*" | tee -a $TL; }
H=$R/home-sender; mkdir -p $H/.config/mash
P35=nats://127.0.0.1:$(cat $R/px-t35.port); P08=nats://127.0.0.1:$(cat $R/px-t08.port)
run(){ # run "label" ENVWORDS... -- args
  local label=$1; shift; local s=$(date +%s.%N)
  out=$(env -i PATH=/usr/bin:/bin HOME=$H XDG_STATE_HOME=$H/state MASH_NAME=sender MASH_OWNER=o MASH_CREDS=$(creds_of sender) "$@" 2>&1 | head -c 400 | tr '\n' '|'); rc=${PIPESTATUS[0]}
  local e=$(date +%s.%N); log "  $label: elapsed=$(python3 -c "print(round($e-$s,2))")s | $out"; }
log "== env override: no timeout in config.json, MASH_OPEN_TIMEOUT=2s, proxy RTT 7s"
echo "{\"url\":\"$P35\",\"owner\":\"o\"}" > $H/.config/mash/config.json
run "MASH_OPEN_TIMEOUT=2s who" MASH_OPEN_TIMEOUT=2s $M who
log "== env wins over file: config.json open_timeout=60s, MASH_OPEN_TIMEOUT=2s"
echo "{\"url\":\"$P35\",\"owner\":\"o\",\"open_timeout\":\"60s\"}" > $H/.config/mash/config.json
run "file 60s + env 2s who" MASH_OPEN_TIMEOUT=2s $M who
log "== MASH_CALL_TIMEOUT: proxy RTT 1.6s, open_timeout 60s; call_timeout 1s must cut the requests, default 15s must pass"
echo "{\"url\":\"$P08\",\"owner\":\"o\",\"open_timeout\":\"60s\"}" > $H/.config/mash/config.json
run "MASH_CALL_TIMEOUT=1s who" MASH_CALL_TIMEOUT=1s $M who
run "MASH_CALL_TIMEOUT=500ms inbox" MASH_CALL_TIMEOUT=500ms $M inbox
run "default call_timeout who (control)" $M who
run "default call_timeout, open 60s, who (control, 1st lines)" $M who
log "== invalid values via env (direct URL, no proxy)"
echo "{\"url\":\"$URL\",\"owner\":\"o\"}" > $H/.config/mash/config.json
for v in 0 5 abc -1s 0s; do run "MASH_OPEN_TIMEOUT='$v' who" MASH_OPEN_TIMEOUT="$v" $M who; done
for v in 0 5 -1s; do run "MASH_CALL_TIMEOUT='$v' who" MASH_CALL_TIMEOUT="$v" $M who; done
log "== invalid values via config.json"
for v in '"0"' '"5"' '0' '"0s"'; do
 echo "{\"url\":\"$URL\",\"owner\":\"o\",\"open_timeout\":$v}" > $H/.config/mash/config.json
 run "config.json open_timeout=$v who" $M who; done
echo "{\"url\":\"$URL\",\"owner\":\"o\",\"call_timeout\":\"0\"}" > $H/.config/mash/config.json
run "config.json call_timeout=\"0\" who" $M who
log "== valid control: MASH_OPEN_TIMEOUT=45s MASH_CALL_TIMEOUT=30s, direct"
rm $H/.config/mash/config.json
run "valid values who" MASH_URL=$URL MASH_OPEN_TIMEOUT=45s MASH_CALL_TIMEOUT=30s $M who
rm -f $H/.config/mash/config.json
