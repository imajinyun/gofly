CREATE TABLE orders (
  id bigint primary key,
  buyer_name varchar(128) not null,
  amount_cents bigint not null,
  status varchar(32) not null,
  version bigint not null,
  created_at timestamp,
  updated_at timestamp,
  deleted_at timestamp,
  UNIQUE KEY uk_orders_buyer_status (buyer_name, status)
);
