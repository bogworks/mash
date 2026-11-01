# px.sh NAME DELAY_MS : start a delay proxy, echo its URL
. /tmp/claude-1000/lrl/lib.sh
UP=${URL##*:}
rm -f $R/px-$1.port $R/px-$1.log
setsid python3 $D/proxy.py $R/px-$1.port $UP $2 $R/px-$1.log </dev/null >/dev/null 2>&1 &
echo $! > $R/px-$1.pid
for i in $(seq 1 50); do [ -s $R/px-$1.port ] && break; sleep 0.1; done
echo nats://127.0.0.1:$(cat $R/px-$1.port)
