#!/usr/bin/env bash
# Read-only diagnostic: does tenant 1001 admin policy actually contain the new
# capabilities after reset? No writes, no deletes. Run in the server's terminal:
#   bash dev-policy-check.local.sh
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
M=(mysql -h"$mysql_host" -P"$mysql_port" -u"$mysql_user" "$dbname" -t)

echo ">> target ${mysql_user}@${mysql_host}:${mysql_port}/${dbname}"
echo ">> [1] support.dict grants for tenant 1001 (expect admin rows read+manage):"
"${M[@]}" -e "SELECT ptype,v0 AS role,v2 AS capability,v3 AS act FROM casbin_rules WHERE domain='1001' AND v2 LIKE 'support.dict%' ORDER BY v2;"
echo ">> [2] user:admin -> role link (expect one g row):"
"${M[@]}" -e "SELECT ptype,v0 AS subject,v1 AS role,v2 AS domain FROM casbin_rules WHERE ptype='g' AND v2='1001';"
echo ">> [3] count + full capability list for admin/1001 (fresh bootstrap writes 20):"
"${M[@]}" -e "SELECT COUNT(*) AS admin_capability_rows FROM casbin_rules WHERE ptype='p' AND v0='admin' AND domain='1001';"
"${M[@]}" -e "SELECT v2 AS capability FROM casbin_rules WHERE ptype='p' AND v0='admin' AND domain='1001' ORDER BY v2;"
echo ">> [4] identity + dict schema presence:"
"${M[@]}" -e "SELECT user_id,subject,login_name,user_state FROM users WHERE login_name='admin';"
"${M[@]}" -e "SELECT tenant_id,tenant_code,tenant_state FROM tenants;"
"${M[@]}" -e "SHOW TABLES LIKE 'support_dict%';"
echo ">> [5] applied migration versions (expect 20261018130000/20261018131000 present):"
"${M[@]}" -e "SELECT version,dirty FROM schema_migrations ORDER BY version DESC LIMIT 6;" 2>/dev/null \
  || echo "(no schema_migrations table or column differs)"
