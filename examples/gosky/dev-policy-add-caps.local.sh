#!/usr/bin/env bash
# Local-only ADDITIVE policy convergence: add the capabilities a current-code
# bootstrap would write but an older (16-capability) bootstrap is missing,
# without dropping any data:
#   support.file.read / support.file.manage / support.dict.read / support.dict.manage
# Granted to fixed role admin in tenant 1001 only (matches DefaultRoleCapabilities).
#
# rule_key is the enforcer's sha256 key (see internal/authz/mysql_policy_store.go:key).
# We only INSERT into casbin_rules (no casbin_policy_events row), so the watcher
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
caps=("support.file.read","support.file.manage","support.dict.read","support.dict.manage")
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
echo ">> after (expect 20):"; "${M[@]}" -t -e "SELECT COUNT(*) AS admin_caps FROM casbin_rules WHERE ptype='p' AND v0='admin' AND domain='1001';"
echo ">> new rows:"; "${M[@]}" -t -e "SELECT v0 AS role,v2 AS capability,v3 AS act FROM casbin_rules WHERE ptype='p' AND domain='1001' AND v2 LIKE 'support.%' ORDER BY v2;"
echo
echo ">> DONE. Now RESTART gosky (Ctrl-C, then: go run ./cmd/gosky) so the enforcer"
echo "   reloads casbin_rules; the watcher only reloads on new casbin_policy_events rows."
