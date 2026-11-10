#!/usr/bin/env bash
# Local-only fix for the SmartAdmin menu route components.
# The file/dict/config page seeds omitted the '.vue' suffix; the unchanged
# client resolves `../views/<component>` against an import.meta.glob of *.vue,
# so the missing suffix produced route.component=undefined and blanked the UI.
# Idempotent: only appends '.vue' to the three known page rows when missing.
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

echo ">> target ${mysql_user}@${mysql_host}:${mysql_port}/${dbname}"
echo ">> before:"
"${M[@]}" -t -e "SELECT menu_id, path, component FROM menus WHERE path IN ('/support/file','/support/dict','/support/config') ORDER BY menu_id;"

"${M[@]}" -e "
UPDATE menus
SET component = CONCAT(component, '.vue')
WHERE menu_type = 2
  AND path IN ('/support/file','/support/dict','/support/config')
  AND component <> '' AND component NOT LIKE '%.vue';"

echo ">> after:"
"${M[@]}" -t -e "SELECT menu_id, path, component FROM menus WHERE path IN ('/support/file','/support/dict','/support/config') ORDER BY menu_id;"
echo
echo ">> DONE. In the browser: log out/in (or hard-refresh Cmd-Shift-R) so main.ts"
echo "   rebuilds dynamic routes from getLoginInfo. Menus are read live, no gosky restart needed."
