CREATE TABLE invoices (
  id bigint primary key,
  tenant_id bigint not null,
  invoice_no varchar(64) unique not null,
  customer_id bigint not null,
  currency varchar(8) not null,
  amount_units bigint not null,
  amount_nanos int not null,
  status varchar(32) not null,
  version bigint not null,
  created_at timestamp,
  updated_at timestamp,
  deleted_at timestamp,
  UNIQUE KEY uk_invoice_tenant_customer_status (tenant_id, customer_id, status),
  KEY idx_invoice_customer_updated (customer_id, updated_at)
);
