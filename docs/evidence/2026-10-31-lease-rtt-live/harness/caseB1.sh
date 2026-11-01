#!/bin/bash
. /tmp/claude-1000/lrl/lib.sh
TL=$D/timeline.log
log(){ echo "$(ts) [B1] $*" | tee -a $TL; }
t(){ local s=$(date +%s.%N); "$@" > $R/b1.out 2>&1; local rc=$?; local e=$(date +%s.%N); log "  exit=$rc elapsed=$(python3 -c "print(round($e-$s,2))")s output: $(head -c 400 $R/b1.out | tr '\n' '|')"; }
mkdir -p $R/home-sender/.config/mash
for P in t35 t08; do
PU=nats://127.0.0.1:$(cat $R/px-$P.port)
log "== proxy $P ($(awk "BEGIN{print ($([ $P = t35 ] && echo 3500 || echo 800))*2}") ms RTT) at $PU"
echo "{\"url\":\"$PU\",\"owner\":\"o\",\"open_timeout\":\"2s\"}" > $R/home-sender/.config/mash/config.json
log "config.json: $(cat $R/home-sender/.config/mash/config.json)  (env has no MASH_URL override below)"
log "mash who, open_timeout=2s from config.json:"
t env -i PATH=/usr/bin:/bin HOME=$R/home-sender XDG_STATE_HOME=$R/home-sender/state MASH_NAME=sender MASH_CREDS=$(creds_of sender) $M who
log "mash serve (MCP handshake sent), open_timeout=2s from config.json; stdout/stderr:"
s=$(date +%s.%N)
( printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"b1","version":"1"}}}'; sleep 15 ) | env -i PATH=/usr/bin:/bin HOME=$R/home-sender XDG_STATE_HOME=$R/home-sender/state MASH_NAME=sender MASH_CREDS=$(creds_of sender) $M serve > $R/b1s.out 2> $R/b1s.err; rc=$?
e=$(date +%s.%N); log "  serve exit=$rc elapsed=$(python3 -c "print(round($e-$s,2))")s"
log "  serve stderr: $(head -c 500 $R/b1s.err | tr '\n' '|')"
log "  serve stdout: $(cut -c1-300 $R/b1s.out | head -5 | tr '\n' '|')"
done
rm -f $R/home-sender/.config/mash/config.json
