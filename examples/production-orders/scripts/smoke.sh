#!/usr/bin/env sh
set -eu

base_url="${PRODUCTION_ORDERS_URL:-http://127.0.0.1:18090}"
admin_url="${PRODUCTION_ORDERS_ADMIN_URL:-http://127.0.0.1:18091}"

python3 - "$base_url" "$admin_url" <<'PY'
import json
import sys
import time
import urllib.error
import urllib.request

base_url = sys.argv[1]
admin_url = sys.argv[2]

def request(method, url, body=None):
    data = None
    headers = {}
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=3) as resp:
        return resp.status, resp.read().decode()

def authorized_get(url, token):
    req = urllib.request.Request(url, headers={"Authorization": "Bearer " + token}, method="GET")
    with urllib.request.urlopen(req, timeout=3) as resp:
        return resp.status, resp.read().decode()

deadline = time.time() + 30
while True:
    try:
        status, _ = request("GET", admin_url + "/debug/healthz")
        if status == 200:
            break
    except (OSError, urllib.error.URLError):
        if time.time() > deadline:
            raise
        time.sleep(0.5)

status, body = request("POST", base_url + "/orders", {"sku": "coffee", "quantity": 2})
assert status == 202, (status, body)
payload = json.loads(body)
assert payload["status"] == "accepted", payload
assert payload["id"].startswith("order-"), payload

status, body = request("GET", base_url + "/openapi.json")
assert status == 200, (status, body)
assert "production orders" in body, body[:200]

status, body = authorized_get(base_url + "/admin/control-plane", "orders-token")
assert status == 200, (status, body)
snapshot = json.loads(body)
assert snapshot.get("metadata"), snapshot
assert snapshot.get("checksum"), snapshot

print("production-orders smoke passed")
PY
