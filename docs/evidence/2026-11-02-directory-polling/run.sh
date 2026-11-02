#!/bin/bash
# Starts a 3-server NATS cluster in a temp dir, runs the dirbench cases, kills the servers. Usage: run.sh <dirbench-binary> <out-file>
set -u
BENCH=$1; OUT=$2
C=(23817 23829 23841); R=(24517 24529 24541); M=(25617 25629 25641)
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
# CASES: newline separated dirbench argument lists (default: the full matrix)
CASES=${CASES:-$'10\n100\n500\n1000\n100 -sync\n500 -sync'}
while IFS= read -r case; do
  echo "## case n=$case"
  nice -n 10 $BENCH -urls $URLS -mon $MON -pids $P -n $case 2>&1
  sleep 5
done <<<"$CASES"; } | tee $OUT
