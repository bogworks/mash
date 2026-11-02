#!/bin/bash
# Starts a 3-server NATS cluster in a temp dir, runs the heartbeat fan-out cases (dirbench -mode new, 40 s warm-up), kills the servers. Usage: [CASES=...] run.sh <dirbench-binary> <out-file>
set -u
BENCH=$1; OUT=$2
C=(26817 26829 26841); R=(27517 27529 27541); M=(28617 28629 28641)
T=$(mktemp -d); PIDS=()
trap 'kill "${PIDS[@]}" 2>/dev/null; sleep 1; rm -rf "$T"' EXIT
for i in 0 1 2; do
  routes=""; for j in 0 1 2; do [ $j != $i ] && routes="$routes nats://127.0.0.1:${R[$j]}"; done
  cat > $T/s$i.conf <<CONF
server_name: s$i
listen: 127.0.0.1:${C[$i]}
http: 127.0.0.1:${M[$i]}
jetstream { store_dir: "$T/js$i" }
cluster { name: bench
  listen: 127.0.0.1:${R[$i]}
  routes: [$(echo $routes | sed 's/ /, /')] }
CONF
  nice -n 10 /home/randyb/.local/bin/nats-server -c $T/s$i.conf >$T/s$i.log 2>&1 & PIDS+=($!)
done
for t in $(seq 60); do
  curl -s http://127.0.0.1:${M[0]}/jsz | grep -q '"meta_cluster"' && curl -s http://127.0.0.1:${M[0]}/jsz | grep -q '"leader": "s' && break; sleep 1; done
sleep 3
URLS=nats://127.0.0.1:${C[0]},nats://127.0.0.1:${C[1]},nats://127.0.0.1:${C[2]}
MON=http://127.0.0.1:${M[0]},http://127.0.0.1:${M[1]},http://127.0.0.1:${M[2]}
P=$(IFS=,; echo "${PIDS[*]}")
ulimit -n 200000
{ echo "# nproc=$(nproc) kernel=$(uname -r) $(/home/randyb/.local/bin/nats-server --version) $(date -u +%FT%TZ)"
# MODE: old (default, as recorded in results.txt) or new (the poll of ADR 0065, endpoint.DirPoller)
MODE=${MODE:-new}
# CASES: newline separated dirbench argument lists (default: the full matrix)
CASES=${CASES:-$'100 -hb-mode all\n500 -hb-mode all\n1000 -hb-mode all\n100 -hb-mode none\n500 -hb-mode none\n1000 -hb-mode none\n100 -hb-mode probe -who-every 5m\n500 -hb-mode probe -who-every 5m\n1000 -hb-mode probe -who-every 5m\n500 -hb-mode probe -who-every 1m\n1000 -hb-mode probe -who-every 1m\n100 -hb-mode probe -who-every 0 -who-burst\n500 -hb-mode probe -who-every 0 -who-burst'}
while IFS= read -r case; do
  echo "## case n=$case"
  nice -n 10 $BENCH -urls $URLS -mon $MON -pids $P -mode $MODE -warm ${WARM:-40s} -measure ${MEASURE:-60s} -n $case 2>&1
  sleep 5
done <<<"$CASES"; } | tee $OUT
