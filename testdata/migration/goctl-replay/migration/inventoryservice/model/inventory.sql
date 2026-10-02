CREATE TABLE `inventory_items` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `sku` varchar(128) NOT NULL DEFAULT '',
  `warehouse_id` bigint(20) NOT NULL DEFAULT 0,
  `available` bigint(20) NOT NULL DEFAULT 0,
  `version` bigint(20) NOT NULL DEFAULT 0,
  `created_at` timestamp NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` timestamp NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `deleted_at` timestamp NULL DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_inventory_sku_warehouse` (`sku`, `warehouse_id`),
  KEY `idx_inventory_available` (`available`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
