-- Local seed verification for tenant 1001 / loginName admin.
-- Usage: mysql -h127.0.0.1 -uroot -p gosky < etc/seed.verify.sql

SELECT '1) identity + tenant + membership (expect 1 active row)' AS step;
SELECT u.user_id, u.subject, u.login_name, u.account_type, u.user_state,
       tu.tenant_id, tu.member_realm, tu.member_state, t.tenant_code, t.tenant_state
FROM users u
JOIN tenant_users tu ON tu.user_id = u.user_id
JOIN tenants t      ON t.tenant_id = tu.tenant_id
WHERE u.login_name = 'admin';

SELECT '2) reserved roles (expect 3: reader/writer/admin)' AS step;
SELECT role_id, role_code, role_state, system_flag FROM roles WHERE tenant_id = 1001;

SELECT '3) menu catalog (expect 44: 7 structural + 16 capability + 21 buttons)' AS step;
SELECT
  SUM(menu_type IN (1,2)) AS structural_pages,
  SUM(menu_type = 3 AND capability <> '') AS capability_points,
  SUM(menu_type = 3 AND web_perms <> '') AS operator_buttons,
  COUNT(*) AS total
FROM menus WHERE tenant_id = 1001;

SELECT '4) casbin g binding (expect one row: user:admin -> admin @ 1001)' AS step;
SELECT ptype, v0, v1, v2, v3 FROM casbin_rules WHERE ptype = 'g' AND v2 = '1001';

SELECT '5) casbin p rules for tenant 1001 (expect ~24: reader 3 + writer 5 + admin 16)' AS step;
SELECT ptype, v0 AS role, COUNT(*) AS grants
FROM casbin_rules
WHERE ptype = 'p' AND v1 = '1001'
GROUP BY ptype, v0 ORDER BY v0;

SELECT '6) critical admin grants (each expect 1)' AS step;
SELECT v0 AS role, v2 AS capability
FROM casbin_rules
WHERE ptype = 'p' AND v1 = '1001'
  AND v2 IN ('authorization.role.read','authorization.role.manage','system.user.manage','system.menu.manage')
ORDER BY v2, v0;
