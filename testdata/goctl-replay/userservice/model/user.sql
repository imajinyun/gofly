CREATE TABLE users (
  id bigint primary key,
  name varchar(128) not null,
  email varchar(255) not null,
  status varchar(32) not null,
  version bigint not null,
  created_at timestamp,
  updated_at timestamp,
  deleted_at timestamp,
  UNIQUE KEY uk_users_email (email),
  KEY idx_users_status (status)
);
