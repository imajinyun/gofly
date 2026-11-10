#!/usr/bin/env bash
# Local-only ADDITIVE policy convergence for T17 (OA notice board):
# add oa.notice.read / oa.notice.manage to fixed role admin in tenant 1001
# when an older bootstrap database misses the new managed capabilities.
# Fresh databases already get them through DefaultRoleCapabilities(admin).
#
# rule_key is the enforcer's sha256 key (see internal/authz/mysql_policy_store.go:key).
# Only INSERT into casbin_rules (no casbin_policy_events row), so the watcher
# will not reload: RESTART gosky after running this script.
set -euo pipefail
cd "$(dirname "$0")"
: "${GOSKY_MYSQL_DSN:?export GOSKY_MYSQL_DSN first}"

DSN="$GOSKY_MYSQL_DSN"
userpass="${DSN%%@tcp(*}"
mysql_user="${userpass%%:*}"; mysql_pass="${userpass#*:}"
hostport="${DSN#*@tcp(}"; hostport="${hostport%%)*}"
mysql_host="${hostport%%:*}"; mysql_port="${hostport##*:}"
rest="${DSN#*/}"; dbname="${rest%%\?*}"
export MYSQL_PWD="$mysql_pass"
M=(mysql -h"$mysql_host" -P"$mysql_port" -u"$mysql_user" "$dbname")

SQL="$(python3 - <<'PY'
import hashlib
def key(ptype, fields):
    h=hashlib.sha256(); h.update(ptype.encode())
    for f in fields:
        h.update(b'\x00'); h.update(f.encode())
    return h.hexdigest()
caps=("oa.notice.read","oa.notice.manage")
print("INSERT IGNORE INTO casbin_rules (rule_key,ptype,domain,v0,v1,v2,v3,v4,v5) VALUES")
rows=[]
for c in caps:
    k=key("p",["admin","1001",c,"EXECUTE","",""])
    rows.append(f"('{k}','p','1001','admin','1001','{c}','EXECUTE','','')")
print(",\n".join(rows)+";")
PY
)"

echo ">> target ${mysql_user}@${mysql_host}:${mysql_port}/${dbname}"
echo ">> before:"; "${M[@]}" -t -e "SELECT COUNT(*) AS admin_caps FROM casbin_rules WHERE ptype='p' AND v0='admin' AND domain='1001';"
echo ">> executing additive grants"; "${M[@]}" -e "$SQL"
echo ">> new rows:"; "${M[@]}" -t -e "SELECT v0 AS role,v2 AS capability,v3 AS act FROM casbin_rules WHERE ptype='p' AND domain='1001' AND v2 LIKE 'oa.%' ORDER BY v2;"
echo
echo ">> DONE. Now RESTART gosky so the enforcer reloads casbin_rules;"
echo "   the watcher only reloads on new casbin_policy_events rows."
