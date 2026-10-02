CREATE TABLE users (
  id bigint NOT NULL AUTO_INCREMENT,
  email varchar(128) NOT NULL,
  tenant_id bigint NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_email (email),
  UNIQUE KEY uq_tenant_email (tenant_id, email)
);
