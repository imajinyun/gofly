#!/usr/bin/env bash
# Local-only clean reset for the gosky dev database (safe to delete after use).
# Run from examples/gosky in a shell that has GOSKY_MYSQL_DSN exported, e.g.
#   export GOSKY_MYSQL_DSN='user:pass@tcp(127.0.0.1:3306)/gosky?parseTime=true&charset=utf8mb4'
#   bash dev-reset.local.sh
# Steps: drop/create schema -> migrate up -> seed.dev.sql -> policy bootstrap (tenant 1001).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "${HERE}/../.." && pwd)"
cd "$HERE"

: "${GOSKY_MYSQL_DSN:?export GOSKY_MYSQL_DSN first}"
export GOSKY_POLICY_BOOTSTRAP_TENANT="${GOSKY_POLICY_BOOTSTRAP_TENANT:-1001}"
export GOSKY_POLICY_BOOTSTRAP_ADMIN="${GOSKY_POLICY_BOOTSTRAP_ADMIN:-user:admin}"
export GOSKY_POLICY_BOOTSTRAP_ACTOR="${GOSKY_POLICY_BOOTSTRAP_ACTOR:-local-operator}"

DSN="$GOSKY_MYSQL_DSN"
userpass="${DSN%%@tcp(*}"
mysql_user="${userpass%%:*}"
mysql_pass="${userpass#*:}"
hostport="${DSN#*@tcp(}"; hostport="${hostport%%)*}"
mysql_host="${hostport%%:*}"
mysql_port="${hostport##*:}"
rest="${DSN#*/}"
dbname="${rest%%\?*}"

[ -n "$mysql_user" ] && [ -n "$mysql_host" ] && [ -n "$dbname" ] || {
  echo "cannot parse GOSKY_MYSQL_DSN (expected user:pass@tcp(host:port)/db?params)" >&2; exit 1; }

echo ">> target: ${mysql_user}@${mysql_host}:${mysql_port}/${dbname}"
read -r "reply?This DROPS and recreates database '${dbname}'. Type YES to continue: "
[ "$reply" = "YES" ] || { echo "aborted"; exit 1; }

export MYSQL_PWD="$mysql_pass"
mysql=(mysql -h"$mysql_host" -P"$mysql_port" -u"$mysql_user")

echo ">> 1/4 drop + create database ${dbname}"
"${mysql[@]}" -e "DROP DATABASE IF EXISTS \`${dbname}\`; CREATE DATABASE \`${dbname}\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"

echo ">> 2/4 migrate up (from repo root ${REPO_ROOT})"
(cd "$REPO_ROOT" && go run ./cmd/gofly migrate up --driver mysql \
  --dir examples/gosky/internal/migrations --dsn-env GOSKY_MYSQL_DSN)

echo ">> 3/4 load etc/seed.dev.sql"
"${mysql[@]}" "$dbname" < etc/seed.dev.sql

echo ">> 4/4 policy bootstrap tenant=${GOSKY_POLICY_BOOTSTRAP_TENANT} admin=${GOSKY_POLICY_BOOTSTRAP_ADMIN}"
go run ./cmd/gosky-policy-bootstrap

echo ">> support.dict grants for tenant 1001:"
"${mysql[@]}" "$dbname" -e "SELECT v0 AS role, v2 AS capability FROM casbin_rules WHERE ptype='p' AND domain='1001' AND v2 LIKE 'support.dict%' ORDER BY v2;"
