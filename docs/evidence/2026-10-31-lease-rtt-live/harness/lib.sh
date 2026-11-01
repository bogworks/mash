# source me
D=/tmp/claude-1000/lrl
M=${BIN:-/tmp/claude-1000/mash-main}
R=$D/run
URL=$(awk '$1=="URL"{print $2}' $R/env.txt)
ADMIN=$(awk '$1=="ADMINCREDS"{print $2}' $R/env.txt)
ACCT=$(awk '$1=="ACCOUNT"{print $2}' $R/env.txt)
creds_of(){ awk -v n="$1" '$1=="EP"&&$2==n{print $3}' $R/env.txt; }
user_of(){ awk -v n="$1" '$1=="EP"&&$2==n{print $4}' $R/env.txt; }
# mx NAME URL cmd... : run mash as endpoint NAME with its own temp HOME/XDG_STATE_HOME ($EXTRA = extra env words)
mx(){ n=$1; u=$2; shift 2; H=$R/home-$n; mkdir -p $H/state
  env -i PATH=/usr/bin:/bin HOME=$H XDG_STATE_HOME=$H/state MASH_NAME=$n MASH_URL=$u MASH_OWNER=o MASH_CREDS=$(creds_of $n) $EXTRA "$M" "$@"; }
adm(){ mkdir -p $R/home-admin; env -i PATH=/usr/bin:/bin HOME=$R/home-admin XDG_STATE_HOME=$R/home-admin/state MASH_URL=${ADMURL:-$URL} MASH_OWNER=o MASH_ADMIN_CREDS=$ADMIN $EXTRA "$M" admin "$@"; }
prov(){ adm provision $1 --user $(user_of $1) --account $ACCT --note "live proof"; }
ts(){ date -u +%H:%M:%S.%3N; }
