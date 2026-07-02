CREATE TABLE inventory_items (
  id bigint primary key,
  tenant_id bigint not null,
  sku varchar(128) not null,
  warehouse_id bigint not null,
  available bigint not null,
  reserved bigint not null,
  version bigint not null,
  status varchar(32) not null,
  created_at timestamp,
  updated_at timestamp,
  deleted_at timestamp,
  UNIQUE KEY uk_inventory_tenant_sku_warehouse (tenant_id, sku, warehouse_id),
  KEY idx_inventory_status_updated (status, updated_at)
);
