CREATE TABLE tasks (
  id bigint primary key,
  title varchar(255) not null,
  owner_id bigint not null,
  status varchar(32) not null,
  priority varchar(32) not null,
  version bigint not null,
  created_at timestamp,
  updated_at timestamp,
  deleted_at timestamp,
  UNIQUE KEY uk_tasks_owner_title (owner_id, title),
  KEY idx_tasks_owner_status (owner_id, status),
  KEY idx_tasks_priority (priority)
);
